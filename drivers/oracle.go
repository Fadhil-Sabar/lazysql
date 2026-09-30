package drivers

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"

	// import Oracle driver (pure Go, thin mode: no Oracle client required)
	_ "github.com/sijms/go-ora/v2"

	"github.com/jorgerojas26/lazysql/models"
)

// Oracle implements the Driver interface on top of the pure-Go go-ora driver.
//
// Oracle has no "database" concept like PostgreSQL/MySQL: a connection always
// targets one service and objects live in schemas. LazySQL's per-connection
// "database" argument is therefore treated as a schema name, and
// GetDatabases() returns the schemas visible to the connected user.
type Oracle struct {
	Connection      *sql.DB
	Provider        string
	CurrentDatabase string
	Urlstr          string
	PoolConfig      models.ConnectionPoolConfig
}

func (db *Oracle) TestConnection(ctx context.Context, urlstr string) error {
	return db.Connect(ctx, urlstr)
}

func (db *Oracle) Connect(ctx context.Context, urlstr string) error {
	ctx = contextOrBackground(ctx)
	db.SetProvider(DriverOracle)

	connection, err := sql.Open("oracle", urlstr)
	if err != nil {
		return err
	}

	if err = applyConnectionPoolConfig(connection, db.PoolConfig); err != nil {
		_ = connection.Close()
		return err
	}

	db.Connection = connection

	if err = db.Connection.PingContext(ctx); err != nil {
		_ = db.Connection.Close()
		return err
	}

	db.Urlstr = urlstr

	var schema sql.NullString
	if err := db.Connection.QueryRowContext(ctx, "SELECT SYS_CONTEXT('USERENV', 'CURRENT_SCHEMA') FROM dual").Scan(&schema); err != nil {
		return err
	}
	db.CurrentDatabase = schema.String

	return nil
}

// connectionFor always reuses the single Oracle connection: the database
// argument is a schema, not a separate connection target.
func (db *Oracle) connectionFor(_ context.Context, _ string) (*sql.DB, bool, error) {
	if db.Connection == nil {
		return nil, false, errors.New("not connected")
	}
	return db.Connection, false, nil
}

func (db *Oracle) GetDatabases(ctx context.Context) ([]string, error) {
	ctx = contextOrBackground(ctx)
	rows, err := db.Connection.QueryContext(ctx, "SELECT username FROM all_users ORDER BY username")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var databases []string
	for rows.Next() {
		var database string
		if err := rows.Scan(&database); err != nil {
			return nil, err
		}
		databases = append(databases, database)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return databases, nil
}

func (db *Oracle) GetTables(ctx context.Context, database string) (map[string][]string, error) {
	ctx = contextOrBackground(ctx)
	if database == "" {
		return nil, errors.New("schema name is required")
	}

	rows, err := db.Connection.QueryContext(ctx, `
		SELECT object_name, owner
		FROM all_objects
		WHERE owner = :1
		  AND object_type IN ('TABLE', 'VIEW')
		  AND object_name NOT LIKE 'BIN$%'
		ORDER BY object_name
	`, strings.ToUpper(database))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	tables := make(map[string][]string)
	for rows.Next() {
		var (
			tableName string
			owner     string
		)
		if err := rows.Scan(&tableName, &owner); err != nil {
			return nil, err
		}
		tables[owner] = append(tables[owner], tableName)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return tables, nil
}

func (db *Oracle) GetTableColumns(ctx context.Context, database, table string) ([][]string, error) {
	ctx = contextOrBackground(ctx)
	if database == "" {
		return nil, errors.New("schema name is required")
	}
	if table == "" {
		return nil, errors.New("table name is required")
	}

	tableSchema, tableName, err := db.splitTable(table)
	if err != nil {
		return nil, err
	}

	rows, err := db.Connection.QueryContext(ctx, `
		SELECT c.column_name,
		       c.data_type,
		       c.nullable,
		       '' AS data_default,
		       NVL(cc.comments, '') AS comment
		FROM all_tab_columns c
		LEFT JOIN all_col_comments cc
		       ON cc.owner = c.owner
		      AND cc.table_name = c.table_name
		      AND cc.column_name = c.column_name
		WHERE c.owner = :1 AND c.table_name = :2
		ORDER BY c.column_id
	`, tableSchema, tableName)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanStringGrid(rows)
}

func (db *Oracle) GetConstraints(ctx context.Context, database, table string) ([][]string, error) {
	ctx = contextOrBackground(ctx)
	tableSchema, tableName, err := db.splitTable(table)
	if err != nil {
		return nil, err
	}

	rows, err := db.Connection.QueryContext(ctx, `
		SELECT ac.constraint_name, acc.column_name, ac.constraint_type
		FROM all_constraints ac
		JOIN all_cons_columns acc
		  ON ac.owner = acc.owner
		 AND ac.constraint_name = acc.constraint_name
		WHERE ac.owner = :1
		  AND ac.table_name = :2
		  AND ac.constraint_type <> 'R'
		ORDER BY ac.constraint_name, acc.position
	`, tableSchema, tableName)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanStringGrid(rows)
}

func (db *Oracle) GetForeignKeys(ctx context.Context, database, table string) ([][]string, error) {
	ctx = contextOrBackground(ctx)
	tableSchema, tableName, err := db.splitTable(table)
	if err != nil {
		return nil, err
	}

	rows, err := db.Connection.QueryContext(ctx, `
		SELECT ac.constraint_name,
		       acc.column_name,
		       rc.owner AS foreign_table_schema,
		       rc.table_name AS foreign_table_name,
		       rcc.column_name AS foreign_column_name
		FROM all_constraints ac
		JOIN all_cons_columns acc
		  ON ac.owner = acc.owner
		 AND ac.constraint_name = acc.constraint_name
		JOIN all_constraints rc
		  ON ac.r_owner = rc.owner
		 AND ac.r_constraint_name = rc.constraint_name
		JOIN all_cons_columns rcc
		  ON rc.owner = rcc.owner
		 AND rc.constraint_name = rcc.constraint_name
		 AND acc.position = rcc.position
		WHERE ac.constraint_type = 'R'
		  AND ac.owner = :1
		  AND ac.table_name = :2
		ORDER BY ac.constraint_name, acc.position
	`, tableSchema, tableName)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanStringGrid(rows)
}

func (db *Oracle) GetReferencingTables(ctx context.Context, database, table string) ([][]string, error) {
	ctx = contextOrBackground(ctx)
	tableSchema, tableName, err := db.splitTable(table)
	if err != nil {
		return nil, err
	}

	rows, err := db.Connection.QueryContext(ctx, `
		SELECT ac.constraint_name,
		       ac.owner AS table_schema,
		       ac.table_name,
		       acc.column_name,
		       rcc.column_name AS referenced_column_name
		FROM all_constraints ac
		JOIN all_cons_columns acc
		  ON ac.owner = acc.owner
		 AND ac.constraint_name = acc.constraint_name
		JOIN all_constraints rc
		  ON ac.r_owner = rc.owner
		 AND ac.r_constraint_name = rc.constraint_name
		JOIN all_cons_columns rcc
		  ON rc.owner = rcc.owner
		 AND rc.constraint_name = rcc.constraint_name
		 AND acc.position = rcc.position
		WHERE ac.constraint_type = 'R'
		  AND rc.owner = :1
		  AND rc.table_name = :2
		ORDER BY ac.owner, ac.table_name, ac.constraint_name, acc.position
	`, tableSchema, tableName)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanReferencingTables(rows)
}

func (db *Oracle) GetIndexes(ctx context.Context, database, table string) ([][]string, error) {
	ctx = contextOrBackground(ctx)
	tableSchema, tableName, err := db.splitTable(table)
	if err != nil {
		return nil, err
	}

	rows, err := db.Connection.QueryContext(ctx, `
		SELECT i.index_name, ic.column_name, i.index_type
		FROM all_indexes i
		JOIN all_ind_columns ic
		  ON i.owner = ic.index_owner
		 AND i.index_name = ic.index_name
		WHERE i.table_owner = :1 AND i.table_name = :2
		ORDER BY i.index_name, ic.column_position
	`, tableSchema, tableName)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanStringGrid(rows)
}

func (db *Oracle) GetRecords(ctx context.Context, database, table, where, sort string, offset, limit int) (PageResult, error) {
	ctx = contextOrBackground(ctx)
	formattedTableName, err := db.formatTableName(table)
	if err != nil {
		return PageResult{}, err
	}

	pageSize, fetchLimit := pageSizeAndFetchLimit(limit)
	queryString := "SELECT * FROM " + formattedTableName

	if where != "" {
		queryString += fmt.Sprintf(" %s", where)
	}

	if sort != "" {
		queryString += fmt.Sprintf(" ORDER BY %s", sort)
	}

	queryString += " OFFSET :1 ROWS FETCH NEXT :2 ROWS ONLY"

	paginatedRows, err := db.Connection.QueryContext(ctx, queryString, offset, fetchLimit)
	if err != nil {
		return PageResult{Query: queryString}, err
	}
	defer paginatedRows.Close()

	columns, err := paginatedRows.Columns()
	if err != nil {
		return PageResult{Query: queryString}, err
	}

	records := [][]string{columns}
	for paginatedRows.Next() {
		nullStringSlice := make([]sql.NullString, len(columns))

		rowValues := make([]any, len(columns))
		for i := range nullStringSlice {
			rowValues[i] = &nullStringSlice[i]
		}

		if err := paginatedRows.Scan(rowValues...); err != nil {
			return PageResult{Query: queryString}, err
		}

		var row []string
		for _, col := range nullStringSlice {
			if col.Valid {
				if col.String == "" {
					row = append(row, "EMPTY&")
				} else {
					row = append(row, col.String)
				}
			} else {
				row = append(row, "NULL&")
			}
		}

		records = append(records, row)
	}

	if err := paginatedRows.Err(); err != nil {
		return PageResult{Query: queryString}, err
	}
	if err := paginatedRows.Close(); err != nil {
		return PageResult{Query: queryString}, err
	}

	queryString = strings.Replace(queryString, ":1", strconv.Itoa(offset), 1)
	queryString = strings.Replace(queryString, ":2", strconv.Itoa(fetchLimit), 1)

	return newPageResult(records, queryString, pageSize), nil
}

func (db *Oracle) GetEstimatedRowCount(ctx context.Context, database, table string) (*int64, error) {
	ctx = contextOrBackground(ctx)
	tableSchema, tableName, err := db.splitTable(table)
	if err != nil {
		return nil, err
	}

	var estimate sql.NullInt64
	err = db.Connection.QueryRowContext(ctx, `
		SELECT num_rows
		FROM all_tables
		WHERE owner = :1 AND table_name = :2
	`, tableSchema, tableName).Scan(&estimate)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !estimate.Valid || estimate.Int64 < 0 {
		return nil, nil
	}

	return &estimate.Int64, nil
}

func (db *Oracle) GetExactRowCount(ctx context.Context, database, table, where string) (int64, error) {
	ctx = contextOrBackground(ctx)
	formattedTableName, err := db.formatTableName(table)
	if err != nil {
		return 0, err
	}

	query := "SELECT COUNT(*) FROM " + formattedTableName
	if where != "" {
		query += " " + where
	}

	var count int64
	if err := db.Connection.QueryRowContext(ctx, query).Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}

func (db *Oracle) UpdateRecord(ctx context.Context, database, table, column, value, primaryKeyColumnName, primaryKeyValue string) error {
	ctx = contextOrBackground(ctx)
	if column == "" {
		return errors.New("column name is required")
	}
	if primaryKeyColumnName == "" {
		return errors.New("primary key column name is required")
	}

	formattedTableName, err := db.formatTableName(table)
	if err != nil {
		return err
	}

	query := "UPDATE " + formattedTableName
	query += fmt.Sprintf(" SET %s = :1 WHERE %s = :2", db.FormatReference(column), db.FormatReference(primaryKeyColumnName))

	_, err = db.Connection.ExecContext(ctx, query, value, primaryKeyValue)
	return err
}

func (db *Oracle) DeleteRecord(ctx context.Context, database, table, primaryKeyColumnName, primaryKeyValue string) error {
	ctx = contextOrBackground(ctx)
	if primaryKeyColumnName == "" {
		return errors.New("primary key column name is required")
	}

	formattedTableName, err := db.formatTableName(table)
	if err != nil {
		return err
	}

	query := "DELETE FROM " + formattedTableName
	query += fmt.Sprintf(" WHERE %s = :1", db.FormatReference(primaryKeyColumnName))

	_, err = db.Connection.ExecContext(ctx, query, primaryKeyValue)
	return err
}

func (db *Oracle) ExecuteDMLStatement(ctx context.Context, database, query string) (result string, err error) {
	ctx = contextOrBackground(ctx)
	res, err := db.Connection.ExecContext(ctx, query)
	if err != nil {
		return result, err
	}
	rowsAffected, err := res.RowsAffected()
	if err != nil {
		return result, err
	}
	return fmt.Sprintf("%d rows affected", rowsAffected), nil
}

// StreamQuery incrementally emits interactive SQL results and honors context
// cancellation through database/sql.
// OpenSession pins one connection so an explicit transaction started by BEGIN
// keeps running across separate editor executions. For Oracle the database
// argument is a schema and statements are schema-qualified, so no session
// preamble is needed.
func (db *Oracle) OpenSession(ctx context.Context, _ string) (Session, error) {
	return openPinnedSession(ctx, db.Connection, "", nil)
}

func (db *Oracle) StreamQuery(ctx context.Context, database, query string, maxRows int, onBatch func(QueryBatch) error) (QueryStreamResult, error) {
	return streamQuery(ctx, db.Connection, query, maxRows, onBatch)
}

func (db *Oracle) ExecuteQuery(ctx context.Context, database, query string) ([][]string, int, error) {
	ctx = contextOrBackground(ctx)
	rows, err := db.Connection.QueryContext(ctx, query)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	columns, err := rows.Columns()
	if err != nil {
		return nil, 0, err
	}

	records := make([][]string, 0)
	for rows.Next() {
		rowValues := make([]any, len(columns))
		for i := range columns {
			rowValues[i] = new(sql.RawBytes)
		}

		if err := rows.Scan(rowValues...); err != nil {
			return nil, 0, err
		}

		var row []string
		for _, col := range rowValues {
			row = append(row, string(*col.(*sql.RawBytes)))
		}

		records = append(records, row)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}

	results := append([][]string{columns}, records...)
	return results, len(records), nil
}

func (db *Oracle) ExecutePendingChanges(ctx context.Context, changes []models.DBDMLChange) error {
	ctx = contextOrBackground(ctx)
	if len(changes) == 0 {
		return nil
	}

	var queries []models.Query
	for _, change := range changes {
		formattedTableName, err := db.formatTableName(change.Table)
		if err != nil {
			return err
		}

		switch change.Type {
		case models.DMLInsertType:
			queries = append(queries, buildInsertQuery(formattedTableName, change.Values, db))
		case models.DMLUpdateType:
			queries = append(queries, buildUpdateQuery(formattedTableName, change.Values, change.PrimaryKeyInfo, db))
		case models.DMLDeleteType:
			queries = append(queries, buildDeleteQuery(formattedTableName, change.PrimaryKeyInfo, db))
		}
	}

	return queriesInTransaction(ctx, db.Connection, queries)
}

func (db *Oracle) GetPrimaryKeyColumnNames(ctx context.Context, database, table string) ([]string, error) {
	ctx = contextOrBackground(ctx)
	tableSchema, tableName, err := db.splitTable(table)
	if err != nil {
		return nil, err
	}

	rows, err := db.Connection.QueryContext(ctx, `
		SELECT acc.column_name
		FROM all_constraints ac
		JOIN all_cons_columns acc
		  ON ac.owner = acc.owner
		 AND ac.constraint_name = acc.constraint_name
		WHERE ac.owner = :1
		  AND ac.table_name = :2
		  AND ac.constraint_type = 'P'
		ORDER BY acc.position
	`, tableSchema, tableName)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var primaryKeyColumnName []string
	for rows.Next() {
		var colName string
		if err := rows.Scan(&colName); err != nil {
			return nil, err
		}
		primaryKeyColumnName = append(primaryKeyColumnName, colName)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return primaryKeyColumnName, nil
}

func (db *Oracle) SetProvider(provider string) {
	db.Provider = provider
}

func (db *Oracle) GetProvider() string {
	return db.Provider
}

// formatTableName turns "SCHEMA.TABLE" into a quoted, schema-qualified name.
func (db *Oracle) formatTableName(table string) (string, error) {
	schema, name, err := db.splitTable(table)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("\"%s\".\"%s\"", schema, name), nil
}

func (db *Oracle) splitTable(table string) (schema, name string, err error) {
	if table == "" {
		return "", "", errors.New("table name is required")
	}

	parts := strings.SplitN(table, ".", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", errors.New("table must be in the format schema.table")
	}

	return strings.ToUpper(parts[0]), strings.ToUpper(parts[1]), nil
}

func (db *Oracle) FormatArg(arg any, colType models.CellValueType) any {
	if colType == models.Null {
		return sql.NullString{String: "", Valid: false}
	}

	if colType == models.Empty {
		return ""
	}

	if colType == models.String {
		switch v := arg.(type) {
		case int, int64:
			return fmt.Sprintf("%d", v)
		case float64, float32:
			s := fmt.Sprintf("%f", v)
			trimmed := strings.TrimRight(s, "0")
			if strings.HasSuffix(trimmed, ".") {
				trimmed += "0"
			}
			return trimmed
		case string:
			return v
		case []byte:
			return string(v)
		case nil:
			return sql.NullString{String: "", Valid: false}
		default:
			return fmt.Sprintf("%v", v)
		}
	}

	return fmt.Sprintf("%v", arg)
}

func (db *Oracle) FormatArgForQueryString(arg any) string {
	switch v := arg.(type) {
	case string:
		if v == "NULL" || v == "DEFAULT" {
			return v
		}
		escaped := strings.ReplaceAll(v, "'", "''")
		return "'" + escaped + "'"
	case sql.NullString:
		if !v.Valid {
			return "NULL"
		}
		escaped := strings.ReplaceAll(v.String, "'", "''")
		return "'" + escaped + "'"
	default:
		return fmt.Sprintf("%v", v)
	}
}

func (db *Oracle) FormatReference(reference string) string {
	return fmt.Sprintf("\"%s\"", reference)
}

func (db *Oracle) FormatPlaceholder(index int) string {
	return fmt.Sprintf(":%d", index)
}

func (db *Oracle) DMLChangeToQueryString(change models.DBDMLChange) (string, error) {
	var queryStr string

	formattedTableName, err := db.formatTableName(change.Table)
	if err != nil {
		return "", err
	}

	columnNames, values := getColNamesAndArgsAsString(change.Values)

	switch change.Type {
	case models.DMLInsertType:
		queryStr = buildInsertQueryString(formattedTableName, columnNames, values, db)
	case models.DMLUpdateType:
		queryStr = buildUpdateQueryString(formattedTableName, columnNames, values, change.PrimaryKeyInfo, db)
	case models.DMLDeleteType:
		queryStr = buildDeleteQueryString(formattedTableName, change.PrimaryKeyInfo, db)
	}

	return queryStr, nil
}

func (db *Oracle) GetFunctions(_ context.Context, _ string) (map[string][]string, error) {
	return nil, errors.New("not implemented")
}

func (db *Oracle) GetProcedures(_ context.Context, _ string) (map[string][]string, error) {
	return nil, errors.New("not implemented")
}

func (db *Oracle) GetViews(_ context.Context, _ string) (map[string][]string, error) {
	return nil, errors.New("not implemented")
}

func (db *Oracle) SupportsProgramming() bool {
	return false
}

func (db *Oracle) UseSchemas() bool {
	return false
}

func (db *Oracle) GetFunctionDefinition(_ context.Context, _, _ string) (string, error) {
	return "", errors.New("not implemented")
}

func (db *Oracle) GetProcedureDefinition(_ context.Context, _, _ string) (string, error) {
	return "", errors.New("not implemented")
}

func (db *Oracle) GetViewDefinition(_ context.Context, _, _ string) (string, error) {
	return "", errors.New("not implemented")
}

// scanStringGrid materializes a result set as [][]string with a leading header
// row, mirroring the convention used by the other drivers.
func scanStringGrid(rows *sql.Rows) ([][]string, error) {
	columns, err := rows.Columns()
	if err != nil {
		return nil, err
	}

	results := [][]string{columns}
	for rows.Next() {
		rowValues := make([]any, len(columns))
		for i := range columns {
			rowValues[i] = new(sql.RawBytes)
		}

		if err := rows.Scan(rowValues...); err != nil {
			return nil, err
		}

		row := make([]string, 0, len(rowValues))
		for _, col := range rowValues {
			row = append(row, string(*col.(*sql.RawBytes)))
		}
		results = append(results, row)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return results, nil
}

var _ Driver = (*Oracle)(nil)
var _ QueryStreamer = (*Oracle)(nil)
