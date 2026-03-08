package sql

import (
	"fmt"
	"strings"
)

const (
	PostgresDatasourceType    = "postgres"
	PostgresDatasourceTypeAlt = "grafana-postgresql-datasource"
)

type PostgresDataSource struct{}

func NewPostgresDataSource() *PostgresDataSource {
	return &PostgresDataSource{}
}

func (ds *PostgresDataSource) Type() string { return PostgresDatasourceType }

func (ds *PostgresDataSource) GetDatabaseQuery() string {
	return fmt.Sprintf(
		"SELECT datname as %s FROM pg_database WHERE datname NOT LIKE 'template%%' AND datname != 'postgres' ORDER BY datname",
		DatabaseNameColumn,
	)
}

func (ds *PostgresDataSource) GetTablesQuery(dbName string) string {
	// PostgreSQL tables are usually queried within the connected database
	return fmt.Sprintf(
		"SELECT table_schema || '.' || table_name as %s FROM information_schema.tables WHERE table_schema NOT IN ('information_schema', 'pg_catalog') ORDER BY table_schema, table_name",
		TableNameColumn,
	)
}

func (ds *PostgresDataSource) GetSchemaQuery(tableName string, dbName string) string {
	schema := "public"
	table := tableName
	if strings.Contains(tableName, ".") {
		parts := strings.SplitN(tableName, ".", 2)
		schema = parts[0]
		table = parts[1]
	}

	return fmt.Sprintf(
		"SELECT column_name as %s, data_type as %s FROM information_schema.columns WHERE table_schema = '%s' AND table_name = '%s' ORDER BY column_name",
		ColNameColumn, ColTypeColumn, schema, table,
	)
}

func (ds *PostgresDataSource) QueryWithLimit(query string, limit uint) (string, bool) {
	query = strings.TrimRight(query, ";")
	queryWithLimit := fmt.Sprintf(`SELECT * FROM ( %s ) AS query_with_limit LIMIT %d`, query, limit)
	return queryWithLimit, true
}
