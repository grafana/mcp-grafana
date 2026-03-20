package sql

import (
	"fmt"
	"strings"
)

// MySQLType is the identifier for MySQL datasources in Grafana.
const (
	MySQLType = "mysql"
)

// MYSQL implements [SQLDatabase]
//
// In MySQL, "Database" and "Schema" are synonyms.
type MySQL struct{}

func NewMySQL() *MySQL {
	return &MySQL{}
}

func (*MySQL) Type() string { return MySQLType }

func (*MySQL) GetDatabaseQuery() string {
	return fmt.Sprintf(
		"SELECT DISTINCT TABLE_SCHEMA as %s from information_schema.TABLES where TABLE_TYPE != 'SYSTEM VIEW' ORDER BY %s",
		DatabaseNameColumn, DatabaseNameColumn,
	)
}

func (*MySQL) GetTablesQuery(dbName string) string {
	database := dbName

	if database == "" {
		database = "database()"
	} else {
		database = quoteIdentAsLiteral(database)
	}

	return fmt.Sprintf(
		"SELECT table_name as %s FROM information_schema.tables WHERE table_schema = %s ORDER BY table_name",
		TableNameColumn,
		database,
	)
}

// GetSchemaQuery builds a query to retrieve column names and types for a specific table.
// The tableName can be schema-qualified (e.g., "db.table").
func (*MySQL) GetSchemaQuery(tableName string, dbName string) string {
	query := fmt.Sprintf("SELECT column_name as %s, data_type as %s FROM information_schema.columns WHERE ", ColNameColumn, ColTypeColumn)
	query += buildTableConstraint(tableName, dbName)
	query += fmt.Sprintf(" ORDER BY %s", ColNameColumn)
	return query
}

func (*MySQL) QueryWithLimit(query string, limit uint) (string, bool) {
	query = strings.TrimSuffix(query, ";")
	queryWithLimit := fmt.Sprintf(`( %s ) LIMIT %d`, query, limit)
	return queryWithLimit, true
}

// GetInfoQuery builds a query to retrieve the MySQL server version.
func (*MySQL) GetInfoQuery() string {
	query := fmt.Sprintf("SELECT VERSION() AS %s", DBVersionColumn)
	return query
}

// buildTableConstraint builds a contraint to select table from default database
// when not using database qualified table names
// It returns the contraint query
func buildTableConstraint(table string, dbName string) string {
	// check for schema qualified table
	if strings.Contains(table, ".") {
		parts := strings.SplitN(table, ".", 2)

		dbName = parts[0]
		table = parts[1]
	}

	if dbName != "" {
		dbName = quoteIdentAsLiteral(dbName)
	} else {
		dbName = "database()"
	}

	table = quoteIdentAsLiteral(table)

	query := fmt.Sprintf("table_schema = %s AND table_name = %s",
		dbName,
		table,
	)

	return query
}

// unquoteIdentifier removes quotes (double or backticks) from a MySQL identifier.
func unquoteIdentifier(value string) string {
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

// quoteLiteral wraps a string in single quotes and escapes existing single quotes.
func quoteLiteral(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

func quoteIdentAsLiteral(value string) string {
	return quoteLiteral(unquoteIdentifier(value))
}
