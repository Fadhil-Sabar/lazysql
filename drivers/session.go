package drivers

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
)

// ErrSessionsUnsupported is returned by OpenSession for drivers that cannot pin
// a connection (for example ClickHouse, which has no interactive
// transactions).
var ErrSessionsUnsupported = errors.New("driver does not support pinned sessions")

// Session is a single pinned database connection. Every statement executed
// through it shares one physical connection, so an explicit transaction opened
// with BEGIN survives across separate editor runs and only ends with
// COMMIT/ROLLBACK.
type Session interface {
	// Exec runs a statement that does not return rows and reports how many rows
	// it affected. It returns -1 when the driver cannot report a count.
	Exec(ctx context.Context, query string) (int64, error)
	// StreamQuery runs a row-returning statement on the pinned connection so
	// in-transaction readers observe uncommitted writes.
	StreamQuery(ctx context.Context, query string, maxRows int, onBatch func(QueryBatch) error) (QueryStreamResult, error)
	// Close releases the pinned connection. Discarding the connection rolls
	// back any transaction the database still has open on it.
	Close() error
}

// SessionDriver is implemented by drivers that can pin a session for explicit
// transactions.
type SessionDriver interface {
	// OpenSession reserves one connection to database and applies whatever
	// session preamble the driver needs (such as MySQL's USE).
	OpenSession(ctx context.Context, database string) (Session, error)
}

type pinnedSession struct {
	connection editorConnection
	cleanup    func()
}

func (session *pinnedSession) Exec(ctx context.Context, query string) (int64, error) {
	result, err := session.connection.ExecContext(contextOrBackground(ctx), query)
	if err != nil {
		return 0, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		// Statements such as COMMIT/ROLLBACK have no affected-row count; that
		// is not an execution failure.
		return -1, nil
	}
	return affected, nil
}

func (session *pinnedSession) StreamQuery(ctx context.Context, query string, maxRows int, onBatch func(QueryBatch) error) (QueryStreamResult, error) {
	return streamQuery(ctx, session.connection, query, maxRows, onBatch)
}

func (session *pinnedSession) Close() error {
	if session.cleanup != nil {
		cleanup := session.cleanup
		session.cleanup = nil
		cleanup()
	}
	return nil
}

// openPinnedSession reserves one connection from pool. preamble is executed on
// that connection before it is handed out so callers get a session already
// targeting the requested database. owned, when set, is called on Close so a
// temporary pool opened for one session can be released with it.
func openPinnedSession(ctx context.Context, pool *sql.DB, preamble string, owned func()) (Session, error) {
	ctx = contextOrBackground(ctx)
	if pool == nil {
		return nil, errors.New("database pool is nil")
	}

	conn, err := pool.Conn(ctx)
	if err != nil {
		if owned != nil {
			owned()
		}
		return nil, err
	}

	cleanup := func() {
		if preamble != "" {
			// The session's default database changed; return the physical
			// connection to the server instead of to the pool so no unrelated
			// query inherits the switched session.
			_ = conn.Raw(func(any) error { return driver.ErrBadConn })
		}
		_ = conn.Close()
		if owned != nil {
			owned()
		}
	}

	if preamble != "" {
		if _, err := conn.ExecContext(ctx, preamble); err != nil {
			cleanup()
			return nil, err
		}
	}

	return &pinnedSession{connection: conn, cleanup: cleanup}, nil
}

// openSessionWith switches the pinned connection when database is not the one
// the pool is already attached to. Drivers whose database argument selects a
// schema or is ignored can pass an empty preamble.
func openSessionWith(ctx context.Context, pool *sql.DB, database, current string, preamble func(string) string, owned func()) (Session, error) {
	if database == "" || database == current || preamble == nil {
		return openPinnedSession(ctx, pool, "", owned)
	}
	return openPinnedSession(ctx, pool, preamble(database), owned)
}
