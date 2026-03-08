package sql

import (
	"fmt"
	"strings"
)

const (
	MSSQLDatasourceType = "mssql"
)

type MSSQLDataSource struct{}

func NewMSSqlDataSource() *MSSQLDataSource {
	return &MSSQLDataSource{}
}

func (ds *MSSQLDataSource) Type() string { return MSSQLDatasourceType }

func (ds *MSSQLDataSource) GetDatabaseQuery() string {
	return fmt.Sprintf(
		"SELECT name as %s FROM sys.databases WHERE name NOT IN ('master', 'tempdb', 'model', 'msdb');",
		DatabaseNameColumn,
	)
}

func (ds *MSSQLDataSource) GetTablesQuery(dbName string) string {
	database := dbName

	if database != "" {
		database = fmt.Sprintf(`[%s].`, strings.Trim(dbName, " "))
	}

	return fmt.Sprintf(
		`
		SELECT TABLE_SCHEMA + '.' + TABLE_NAME as %s
    	FROM %sINFORMATION_SCHEMA.TABLES
		`,
		TableNameColumn,
		database,
	)
}

//supports schema.table | table format for tableName
//optional dbName defaults to default configured database
func (ds *MSSQLDataSource) GetSchemaQuery(tableName string, dbName string) string {

	databaseContraint := ""

	if dbName != "" {
		databaseContraint = fmt.Sprintf("USE [%s] ", dbName)
	}

	//TABLE_SCHEMA = 'dbo' AND
	splitTableName := strings.SplitN(tableName, ".", 2)

	schema := ""
	table := splitTableName[0]

	if len(splitTableName) >= 2 {
		schema = splitTableName[0]
		table = splitTableName[1]
	}

	schemaContraint := ""

	if schema != "" {
		schemaContraint = fmt.Sprintf(` TABLE_SCHEMA = '%s' AND `, schema)
	}

	query := fmt.Sprintf(
		`%s
		SELECT COLUMN_NAME as %s,DATA_TYPE as %s
		FROM INFORMATION_SCHEMA.COLUMNS WHERE %s TABLE_NAME='%s';
	`, databaseContraint, ColNameColumn, ColTypeColumn, schemaContraint, table,
	)
	return query
}

// enforces limit on rows selected through `SELECT TOP x`
func (ds *MSSQLDataSource) QueryWithLimit(query string, limit uint) (string, bool) {
	query = strings.TrimRight(query, ";")
	queryWithLimit := fmt.Sprintf(`SELECT TOP %d * FROM ( %s ) AS query_with_limit`, limit, query)
	return queryWithLimit, true
}
