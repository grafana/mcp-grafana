package sql

import (
	"fmt"
	"strings"
)

const (
	MySQLDatasourceType = "mysql"
)

type MySQLDataSource struct{}

func NewMySqlDataSource() *MySQLDataSource {
	return &MySQLDataSource{}
}

func (ds *MySQLDataSource) Type() string { return MySQLDatasourceType }

func (ds *MySQLDataSource) GetDatabaseQuery() string {
	return "SELECT DISTINCT TABLE_SCHEMA from information_schema.TABLES where TABLE_TYPE != 'SYSTEM VIEW' ORDER BY TABLE_SCHEMA"
}

func (ds *MySQLDataSource) GetTablesQuery(dbName string) string {
	database := dbName

	if database == "" {
		database = "database()"
	} else {
		database = QuoteIdentAsLiteral(database)
	}

	return fmt.Sprintf("SELECT table_name FROM information_schema.tables WHERE table_schema = %s ORDER BY table_name", database)
}

// tableName : supports schema.table format
// optional dbName defaults to default configured database
func (ds *MySQLDataSource) GetSchemaQuery(tableName string, dbName string) string {
	query := "SELECT column_name, data_type FROM information_schema.columns WHERE "
	query += buildTableConstraint(tableName, dbName)
	query += " ORDER BY column_name"
	return query
}

// enforces limit : wraps the query  with`( )`  + appends limit clause `LIMIT X`
func (ds *MySQLDataSource) QueryWithLimit(query string, limit uint) (string, bool) {
	query = strings.TrimSuffix(query, ";")
	queryWithLimit := fmt.Sprintf(`( %s ) LIMIT %d`, query, limit)
	return queryWithLimit, true
}

func buildTableConstraint(table string, dbName string) string {
	var query string

	// check for schema qualified table
	if strings.Contains(table, ".") {
		parts := strings.SplitN(table, ".", 2)

		dbName = parts[0]
		table = parts[1]
	}

	if dbName != "" {
		dbName = QuoteIdentAsLiteral(dbName)
	} else {
		dbName = "database()"
	}

	table = QuoteIdentAsLiteral(table)

	query = fmt.Sprintf("table_schema = %s AND table_name = %s",
		dbName,
		table,
	)

	return query
}

// remove identifier quoting from identifier to use in metadata queries
func UnquoteIdentifier(value string) string {
	if len(value) < 2 {
		return value
	}

	if value[0] == '"' && value[len(value)-1] == '"' {
		unquoted := value[1 : len(value)-1]
		return strings.ReplaceAll(unquoted, `""`, `"`)
	} else if value[0] == '`' && value[len(value)-1] == '`' {
		return value[1 : len(value)-1]
	}

	return value
}

func QuoteLiteral(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

func QuoteIdentAsLiteral(value string) string {
	return QuoteLiteral(UnquoteIdentifier(value))
}
