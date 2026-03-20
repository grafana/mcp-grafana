package sql

import (
	"fmt"
	"strings"
)

const (
	MSSQLType = "mssql"
)

// MSSQL implements [SQLDatabase]
type MSSQL struct{}

func NewMSSQL() *MSSQL {
	return &MSSQL{}
}

// Type returns the engine type identifier.
func (*MSSQL) Type() string { return MSSQLType }

func (*MSSQL) GetDatabaseQuery() string {
	return fmt.Sprintf(
		"SELECT name as %s FROM sys.databases WHERE name NOT IN ('master', 'tempdb', 'model', 'msdb');",
		DatabaseNameColumn,
	)
}

func (*MSSQL) GetTablesQuery(dbName string) string {
	database := dbName

	if database != "" {
		// append . to dbName to use as prefix in fully qualified name
		database = fmt.Sprintf("%s.", quoteIdentifier(strings.Trim(dbName, " ")))
	}

	//TODO : apply quoting with quote identifier outside
	return fmt.Sprintf(
		`
		SELECT TABLE_SCHEMA + '.' + TABLE_NAME as %s
    	FROM %sINFORMATION_SCHEMA.TABLES
		`,
		TableNameColumn,
		database,
	)
}

func (*MSSQL) GetSchemaQuery(tableName string, dbName string) string {

	databaseContraint := ""

	if dbName != "" {
		databaseContraint = fmt.Sprintf("USE [%s] ", dbName)
	}

	splitTableName := strings.SplitN(tableName, ".", 2)

	schema := ""
	table := splitTableName[0]

	if len(splitTableName) >= 2 {
		schema = splitTableName[0]
		table = splitTableName[1]
	}

	schemaContraint := ""

	if schema != "" {
		schemaContraint = fmt.Sprintf(` TABLE_SCHEMA = '%s' AND `, escapeSingleQuotes(schema))
	}

	query := fmt.Sprintf(
		`%s
		SELECT COLUMN_NAME as %s,DATA_TYPE as %s
		FROM INFORMATION_SCHEMA.COLUMNS WHERE %s TABLE_NAME = '%s';
	`, databaseContraint, ColNameColumn, ColTypeColumn, schemaContraint, escapeSingleQuotes(table),
	)

	return query
}

func (*MSSQL) QueryWithLimit(query string, limit uint) (string, bool) {
	query = strings.TrimRight(query, ";")
	queryWithLimit := fmt.Sprintf(`SELECT TOP %d * FROM ( %s ) AS query_with_limit`, limit, query)
	return queryWithLimit, true
}

// GetInfoQuery builds a query to retrieve the SQL Server product version.
func (*MSSQL) GetInfoQuery() string {
	query := fmt.Sprintf(`SELECT CAST(SERVERPROPERTY('ProductVersion') AS VARCHAR) AS %s`, DBVersionColumn)
	return query
}

func quoteIdentifier(name string) string {
	return "[" + strings.ReplaceAll(name, "]", "]]") + "]"
}

// escapeSingleQuotes escapes ' from value
func escapeSingleQuotes(value string) string {
	return strings.ReplaceAll(value, "'", "''")
}
