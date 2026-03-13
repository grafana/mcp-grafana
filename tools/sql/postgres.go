package sql

import (
	"fmt"
	"strings"
)

// PostgresType is the identifier for PostgreSQL datasources in Grafana.
const (
	PostgresType = "grafana-postgresql-datasource"
)
// Postgres implements [SQLDatabase]
type Postgres struct{}

func NewPostgres() *Postgres {
	return &Postgres{}
}

func (*Postgres) Type() string { return PostgresType }
// GetDatabaseQuery implements [SQLDatabase.GetDatabaseQuery]
func (*Postgres) GetDatabaseQuery() string {
	return fmt.Sprintf(
		`SELECT datname as %s
		FROM pg_database
		WHERE datistemplate = false
		AND datname <> 'postgres'
		`,
		DatabaseNameColumn,
	)
}

// GetTablesQuery builds a query to retrieve table names.
// dbName is not appicable for postgres database
func (*Postgres) GetTablesQuery(dbName string) string {
	// PostgreSQL tables are usually queried within the connected database
	// switching database through an sql query is not supported in postgres
	constraint := buildSchemaConstraint()
	return fmt.Sprintf(
		`SELECT
		CASE WHEN %s
		THEN quote_ident(table_name)
		ELSE quote_ident(table_schema) || '.' || quote_ident(table_name)
		END AS %s
		FROM information_schema.tables
		WHERE quote_ident(table_schema) NOT IN ('information_schema',
								'pg_catalog',
								'_timescaledb_cache',
								'_timescaledb_catalog',
								'_timescaledb_internal',
								'_timescaledb_config',
								'timescaledb_information',
								'timescaledb_experimental')
		ORDER BY CASE WHEN %s THEN 0 ELSE 1 END, 1`,
		constraint,
		TableNameColumn,
		constraint,
	)
}

func (*Postgres) GetSchemaQuery(tableName string, dbName string) string {
	// we will put table-name between single-quotes,
	// and escape single-quotes in the table-name
	table := "'" + strings.ReplaceAll(tableName, "'", "''") + "'"
	constraint := buildSchemaConstraint()

	return fmt.Sprintf(
		`
		SELECT quote_ident(column_name) AS %s, data_type AS %s
			FROM information_schema.columns
			WHERE
			CASE WHEN array_length(parse_ident(%s), 1) = 2
				THEN quote_ident(table_schema) = (parse_ident(%s))[1]
				AND quote_ident(table_name) = (parse_ident(%s))[2]
				ELSE quote_ident(table_name) = (parse_ident(%s))[1]
				AND %s
			END
		`,
		ColNameColumn, ColTypeColumn, table, table, table, table, constraint,
	)
}

// QueryWithLimit builds a wrapped query to enforce a row limit.
func (*Postgres) QueryWithLimit(query string, limit uint) (string, bool) {
	query = strings.TrimSuffix(query, ";")
	queryWithLimit := fmt.Sprintf(`SELECT * FROM ( %s ) AS subquery LIMIT %d`, query, limit)
	return queryWithLimit, true
}

// GetInfoQuery builds a query to retrieve PostgreSQL and TimescaleDB extension versions.
func (*Postgres) GetInfoQuery() string {
	query := fmt.Sprintf(`SELECT
				current_setting('server_version_num')::int / 100 AS %s,
				extversion AS %s
			FROM pg_extension
			WHERE extname = 'timescaledb'`, DBVersionColumn, TimeScaleDbVersionColumn)
	return query
}

// buildSchemaConstraint constructs a constraint to filter schemas based on the search_path.
// It returns the query
//
// Example usecase :
// A statment queries a table without using a qualified schema in table
// table is queried only when its schema is under searchpath
func buildSchemaConstraint() string {
	return `
	 quote_ident(table_schema) IN (
          SELECT
            CASE WHEN trim(s[i]) = '"$user"' THEN user ELSE trim(s[i]) END
          FROM
            generate_series(
              array_lower(string_to_array(current_setting('search_path'),','),1),
              array_upper(string_to_array(current_setting('search_path'),','),1)
            ) as i,
            string_to_array(current_setting('search_path'),',') s
          )
	`
}
