package components

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/rivo/tview"

	"github.com/jorgerojas26/lazysql/app"
)

// txOutcome is the lifecycle state of one executed statement.
type txOutcome string

const (
	txOutcomeRunning    txOutcome = "running"
	txOutcomeAutocommit txOutcome = "autocommit"
	txOutcomePending    txOutcome = "pending"
	txOutcomeCommitted  txOutcome = "committed"
	txOutcomeRolledBack txOutcome = "rolled back"
	txOutcomeFailed     txOutcome = "failed"
)

// txKind describes what the row count of a statement means.
type txKind string

const (
	txKindAffected txKind = "affected"
	txKindRows     txKind = "rows"
	txKindControl  txKind = "control"
)

// txStatement is one entry of the execution history shown in the transaction
// panel.
type txStatement struct {
	ID       uint64
	At       time.Time
	Query    string
	Verb     string
	Kind     txKind
	Rows     int64
	Err      string
	InTx     bool
	Outcome  txOutcome
	Duration time.Duration
}

// TransactionState tracks the statements executed in one editor tab together
// with the explicit transaction they belong to. It is the single source of
// truth for the transaction panel and for the commit/rollback confirmation.
type TransactionState struct {
	mu      sync.Mutex
	entries []txStatement
	nextID  uint64
	active  bool
	failed  bool
	started time.Time
}

// NewTransactionState returns an empty history.
func NewTransactionState() *TransactionState {
	return &TransactionState{}
}

// Record appends a statement that is about to be dispatched and returns its
// identifier so the caller can complete it later.
func (state *TransactionState) Record(query, verb string, inTx bool) uint64 {
	state.mu.Lock()
	defer state.mu.Unlock()

	state.nextID++
	state.entries = append(state.entries, txStatement{
		ID:      state.nextID,
		At:      time.Now(),
		Query:   query,
		Verb:    verb,
		InTx:    inTx,
		Outcome: txOutcomeRunning,
	})
	return state.nextID
}

// Complete records the result of a previously recorded statement. rows is the
// affected-row count for mutations or the returned-row count for reads; pass
// -1 when the driver does not report a count.
func (state *TransactionState) Complete(id uint64, kind txKind, rows int64, err error) {
	state.mu.Lock()
	defer state.mu.Unlock()

	entry := state.entryLocked(id)
	if entry == nil {
		return
	}

	entry.Kind = kind
	entry.Rows = rows
	entry.Duration = time.Since(entry.At)

	if err != nil {
		entry.Err = err.Error()
		if entry.InTx {
			// The statement failed but it is still part of the outstanding
			// transaction, so it stays in the pending set and keeps its error.
			state.failed = true
			entry.Outcome = txOutcomePending
			return
		}
		entry.Outcome = txOutcomeFailed
		return
	}

	if entry.InTx {
		entry.Outcome = txOutcomePending
		return
	}
	entry.Outcome = txOutcomeAutocommit
}

// Begin marks the start of an explicit transaction. Callers open the pinned
// session separately; this only updates the display state.
func (state *TransactionState) Begin() {
	state.mu.Lock()
	defer state.mu.Unlock()

	state.active = true
	state.started = time.Now()
}

// Active reports whether BEGIN has run without a matching COMMIT/ROLLBACK.
func (state *TransactionState) Active() bool {
	state.mu.Lock()
	defer state.mu.Unlock()
	return state.active
}

// Failed reports whether a statement failed while the transaction was open.
// On PostgreSQL a single failure aborts the whole transaction.
func (state *TransactionState) Failed() bool {
	state.mu.Lock()
	defer state.mu.Unlock()
	return state.failed
}

// Resolve ends the explicit transaction and rewrites every pending entry with
// the final outcome.
func (state *TransactionState) Resolve(outcome txOutcome) {
	state.mu.Lock()
	defer state.mu.Unlock()

	for i := range state.entries {
		if state.entries[i].Outcome == txOutcomePending || state.entries[i].Outcome == txOutcomeRunning {
			state.entries[i].Outcome = outcome
		}
	}
	state.active = false
	state.failed = false
}

// Pending returns a copy of the statements that a COMMIT would apply. It
// includes statements that failed while the transaction was open, because on
// most engines they still leave the transaction unresolved.
func (state *TransactionState) Pending() []txStatement {
	state.mu.Lock()
	defer state.mu.Unlock()

	pending := make([]txStatement, 0, len(state.entries))
	for _, entry := range state.entries {
		if !entry.InTx {
			continue
		}
		switch entry.Outcome {
		case txOutcomePending, txOutcomeRunning:
			pending = append(pending, entry)
		}
	}
	return pending
}

// Entries returns a copy of the whole execution history.
func (state *TransactionState) Entries() []txStatement {
	state.mu.Lock()
	defer state.mu.Unlock()

	return append([]txStatement(nil), state.entries...)
}

// PendingSummary aggregates the pending statements for the confirmation dialog.
type PendingSummary struct {
	Statements int
	Rows       int64
	Tables     []string
	Failed     int
	Since      time.Time
}

// SummarizePending aggregates the outstanding statements.
func (state *TransactionState) SummarizePending() PendingSummary {
	pending := state.Pending()

	state.mu.Lock()
	summary := PendingSummary{Statements: len(pending), Since: state.started}
	state.mu.Unlock()

	tables := map[string]struct{}{}
	for _, entry := range pending {
		if entry.Rows > 0 && entry.Kind == txKindAffected {
			summary.Rows += entry.Rows
		}
		if entry.Err != "" {
			summary.Failed++
		}
		if table := txTableName(entry.Query, entry.Verb); table != "" {
			tables[table] = struct{}{}
		}
	}

	for table := range tables {
		summary.Tables = append(summary.Tables, table)
	}
	sort.Strings(summary.Tables)

	return summary
}

// Clear drops finished statements from the history. It refuses while a
// transaction is open so a pending statement can never disappear untracked.
func (state *TransactionState) Clear() bool {
	state.mu.Lock()
	defer state.mu.Unlock()

	if state.active {
		return false
	}

	kept := state.entries[:0]
	for _, entry := range state.entries {
		if entry.Outcome == txOutcomePending || entry.Outcome == txOutcomeRunning {
			kept = append(kept, entry)
		}
	}
	state.entries = kept
	return true
}

func (state *TransactionState) entryLocked(id uint64) *txStatement {
	for i := range state.entries {
		if state.entries[i].ID == id {
			return &state.entries[i]
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// Statement classification
// ---------------------------------------------------------------------------

// isTransactionBeginQuery reports whether a statement opens an explicit
// transaction.
func isTransactionBeginQuery(query, verb string) bool {
	switch verb {
	case "BEGIN":
		return true
	case "START":
		return strings.HasPrefix(strings.ToUpper(strings.TrimSpace(query)), "START TRANSACTION")
	}
	return false
}

// isTransactionEndQuery reports whether a statement closes the explicit
// transaction. ROLLBACK TO SAVEPOINT does not.
func isTransactionEndQuery(query, verb string) bool {
	upper := strings.ToUpper(strings.TrimSpace(query))
	switch verb {
	case "COMMIT":
		return true
	case "ROLLBACK":
		return !strings.HasPrefix(upper, "ROLLBACK TO") && !strings.Contains(upper, " TO SAVEPOINT ")
	case "END":
		return strings.HasPrefix(upper, "END TRANSACTION")
	}
	return false
}

// isTransactionRollbackQuery reports whether a transaction-ending statement
// discards the work instead of keeping it.
func isTransactionRollbackQuery(query, verb string) bool {
	if verb == "ROLLBACK" {
		return true
	}
	return strings.HasPrefix(strings.ToUpper(strings.TrimSpace(query)), "ROLLBACK")
}

// txMutatingVerb reports whether the verb changes data, and therefore whether
// its row count means "rows affected".
func txMutatingVerb(verb string) bool {
	switch verb {
	case "INSERT", "UPDATE", "DELETE", "MERGE", "REPLACE", "UPSERT", "TRUNCATE", "COPY", "CALL":
		return true
	}
	return false
}

// txIdentifierPattern matches a table name, quoted or not, so a mutation on a
// table with spaces or an upper-case name is still reported by name.
const (
	txDquotedIdentifier  = `"[^"]*"`
	txBracketIdentifier  = `\[[^\]]*\]`
	txPlainIdentifier    = `[^\s;,(]+`
	txBackquotedIdent    = "`[^`]*`"
	txIdentifierFragment = `(?:` + txDquotedIdentifier + `|` + txBackquotedIdent + `|` + txBracketIdentifier + `|` + txPlainIdentifier + `)`
)

var txTablePatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?is)^\s*DELETE\s+FROM\s+(` + txIdentifierFragment + `)`),
	regexp.MustCompile(`(?is)^\s*UPDATE\s+(` + txIdentifierFragment + `)`),
	regexp.MustCompile(`(?is)^\s*INSERT\s+INTO\s+(` + txIdentifierFragment + `)`),
	regexp.MustCompile(`(?is)^\s*MERGE\s+INTO\s+(` + txIdentifierFragment + `)`),
	regexp.MustCompile(`(?is)^\s*REPLACE\s+INTO\s+(` + txIdentifierFragment + `)`),
	regexp.MustCompile(`(?is)^\s*TRUNCATE\s+(?:TABLE\s+)?(` + txIdentifierFragment + `)`),
	regexp.MustCompile(`(?is)^\s*COPY\s+(` + txIdentifierFragment + `)`),
}

// txTableName extracts the target table of a mutation so the confirmation
// dialog can tell the user what is being committed.
func txTableName(query, verb string) string {
	if !txMutatingVerb(verb) {
		return ""
	}

	for _, pattern := range txTablePatterns {
		if match := pattern.FindStringSubmatch(query); match != nil {
			return strings.Trim(match[1], `"`+"`"+`[]`)
		}
	}
	return ""
}

// txStatementLabel is the one-line description shown in the panel and the
// confirmation dialog.
func txStatementLabel(entry txStatement, maxQuery int) string {
	query := strings.Join(strings.Fields(entry.Query), " ")
	if maxQuery > 0 && len(query) > maxQuery {
		query = query[:maxQuery-1] + "…"
	}
	return query
}

// txRowLabel describes the row count of one statement.
func txRowLabel(entry txStatement) string {
	switch {
	case entry.Err != "":
		return "failed"
	case entry.Outcome == txOutcomeRunning:
		return "running…"
	case entry.Kind == txKindControl:
		return "-"
	case entry.Rows < 0:
		return "ok"
	case entry.Kind == txKindRows:
		return fmt.Sprintf("%d rows", entry.Rows)
	case entry.Rows == 1:
		return "1 row"
	default:
		return fmt.Sprintf("%d rows", entry.Rows)
	}
}

// txConfirmationText builds the commit/rollback confirmation body. Commit and
// rollback share the statement inventory; the wording and the closing warning
// differ.
func txConfirmationText(action string, entries []txStatement, summary PendingSummary) string {
	var builder strings.Builder

	verb := strings.ToUpper(action)
	fmt.Fprintf(&builder, "%s %d pending statement(s)?\n\n", verb, summary.Statements)

	const maxListed = 8
	for i, entry := range entries {
		if i == maxListed {
			fmt.Fprintf(&builder, "[%s]  … and %d more[-]\n", app.Styles.SecondaryTextColor, len(entries)-maxListed)
			break
		}
		fmt.Fprintf(&builder, "  [%s]%2d.[-] %s [%s](%s)[-]\n",
			app.Styles.SecondaryTextColor,
			i+1,
			tview.Escape(txStatementLabel(entry, 64)),
			app.Styles.SecondaryTextColor,
			txRowLabel(entry))
	}

	builder.WriteString("\n")
	if len(summary.Tables) > 0 {
		fmt.Fprintf(&builder, "Tables affected: [%s]%s[-]\n",
			app.Styles.SecondaryTextColor, tview.Escape(strings.Join(summary.Tables, ", ")))
	}
	fmt.Fprintf(&builder, "Total rows affected: [%s]%d[-]\n", app.Styles.SecondaryTextColor, summary.Rows)
	if !summary.Since.IsZero() {
		fmt.Fprintf(&builder, "Transaction started: [%s]%s[-]\n",
			app.Styles.SecondaryTextColor, summary.Since.Format("15:04:05"))
	}

	builder.WriteString("\n")
	if verb == "ROLLBACK" {
		builder.WriteString("All of these changes will be discarded. This cannot be undone.")
	} else {
		builder.WriteString("These changes will be written to the database.")
	}
	if summary.Failed > 0 {
		fmt.Fprintf(&builder, "\n\n[%s]Warning:[-] %d statement(s) failed while the transaction was open.",
			app.Styles.SecondaryTextColor, summary.Failed)
		if verb == "COMMIT" {
			builder.WriteString(" On PostgreSQL an aborted transaction is rolled back by COMMIT.")
		}
	}

	return builder.String()
}
