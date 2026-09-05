package sql

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	DefaultSQLLimit = 100
	MaxSQLLimit     = 1000
)

// Dialect defines the interface that each SQL backend must implement.
type Dialect interface {
	DatasourceTypes() []string
	DefaultFormat() interface{}
	SubstituteMacros(query string, from, to time.Time) string
	EnforceLimit(query string, limit int) string
	ValidateIdentifier(name, field string) error
	ExtraQueryPayloadFields(params QuerySQLParams) map[string]interface{}
	ListDatabases(ctx context.Context, qr QueryRunner, dsType string, params ListSQLDatabasesParams) (*ListSQLDatabasesResult, error)
	ListTables(ctx context.Context, qr QueryRunner, dsType string, params ListSQLTablesParams) (*ListSQLTablesResult, error)
	DescribeTable(ctx context.Context, qr QueryRunner, dsType string, params DescribeSQLTableParams) (*DescribeSQLTableResult, error)
}

// QueryRunner abstracts the execution layer so dialects don't depend on HTTP clients directly.
type QueryRunner interface {
	RunSQL(ctx context.Context, uid, dsType string, rawSQL string, format interface{}, from, to time.Time, extra map[string]interface{}) ([]string, []map[string]interface{}, error)
	ResourceRequest(ctx context.Context, uid, path string, body map[string]string) ([]byte, error)
}

// QuerySQLParams are the parameters for the query_sql tool.
type QuerySQLParams struct {
	DatasourceUID              string            `json:"datasourceUid" jsonschema:"required,description=The UID of the SQL datasource. Use list_datasources to find available UIDs."`
	Query                      string            `json:"query" jsonschema:"required,description=Raw SQL query. Supports datasource-specific macros such as $__timeFilter(column)\\, $__from/$__to\\, $__interval\\, and ${varname} for variable substitution."`
	Start                      string            `json:"start,omitempty" jsonschema:"description=Start time. Formats: 'now-1h'\\, '2026-02-02T19:00:00Z'\\, '1738519200000' (Unix ms). Default: 1 hour ago."`
	End                        string            `json:"end,omitempty" jsonschema:"description=End time. Formats: 'now'\\, '2026-02-02T19:00:00Z'\\, '1738519200000' (Unix ms). Default: now."`
	Variables                  map[string]string `json:"variables,omitempty" jsonschema:"description=Template variable substitutions as key-value pairs. Referenced as ${varname} or $varname in the query."`
	Limit                      int               `json:"limit,omitempty" jsonschema:"description=Maximum number of rows. Default: 100\\, Max: 1000. Appended as LIMIT when query has none; existing LIMIT clauses are capped at 1000."`
	Database                   string            `json:"database,omitempty" jsonschema:"description=Database override (Athena\\, Snowflake\\, ClickHouse). Defaults to datasource config."`
	Schema                     string            `json:"schema,omitempty" jsonschema:"description=Schema override (Snowflake\\, PostgreSQL\\, MSSQL). Defaults to datasource config."`
	Catalog                    string            `json:"catalog,omitempty" jsonschema:"description=Data catalog override (Athena only). Defaults to datasource config."`
	Region                     string            `json:"region,omitempty" jsonschema:"description=AWS region override (Athena only). Defaults to datasource config."`
	ResultReuseEnabled         bool              `json:"resultReuseEnabled,omitempty" jsonschema:"description=Enable Athena query result reuse. Requires Athena engine version 3."`
	ResultReuseMaxAgeInMinutes int               `json:"resultReuseMaxAgeInMinutes,omitempty" jsonschema:"description=Max age in minutes for reused Athena results. Only applies when resultReuseEnabled is true."`
}

// ListSQLTablesParams are the parameters for the list_sql_tables tool.
type ListSQLTablesParams struct {
	DatasourceUID string `json:"datasourceUid" jsonschema:"required,description=The UID of the SQL datasource."`
	Database      string `json:"database,omitempty" jsonschema:"description=Database name to filter tables. Behavior varies by datasource type."`
	Schema        string `json:"schema,omitempty" jsonschema:"description=Schema name to filter tables (Snowflake\\, PostgreSQL\\, MSSQL)."`
	Catalog       string `json:"catalog,omitempty" jsonschema:"description=Data catalog name (Athena only)."`
	Region        string `json:"region,omitempty" jsonschema:"description=AWS region override (Athena only)."`
}

// DescribeSQLTableParams are the parameters for the describe_sql_table tool.
type DescribeSQLTableParams struct {
	DatasourceUID string `json:"datasourceUid" jsonschema:"required,description=The UID of the SQL datasource."`
	Table         string `json:"table" jsonschema:"required,description=Table name to describe."`
	Database      string `json:"database,omitempty" jsonschema:"description=Database name. Behavior varies by datasource type."`
	Schema        string `json:"schema,omitempty" jsonschema:"description=Schema name (Snowflake\\, PostgreSQL\\, MSSQL)."`
	Catalog       string `json:"catalog,omitempty" jsonschema:"description=Data catalog name (Athena only)."`
	Region        string `json:"region,omitempty" jsonschema:"description=AWS region override (Athena only)."`
}

// ListSQLDatabasesParams are the parameters for the list_sql_databases tool.
type ListSQLDatabasesParams struct {
	DatasourceUID string `json:"datasourceUid" jsonschema:"required,description=UID of the SQL datasource."`
	Catalog       string `json:"catalog,omitempty" jsonschema:"description=Catalog name. For Athena: when empty returns catalogs; when set returns databases in that catalog."`
	Database      string `json:"database,omitempty" jsonschema:"description=Database name. For Snowflake: when set returns schemas in that database."`
	Region        string `json:"region,omitempty" jsonschema:"description=AWS region override (Athena)."`
}

// SQLDatabaseInfo represents a database, schema, or catalog entry.
type SQLDatabaseInfo struct {
	Catalog  string `json:"catalog,omitempty"`
	Database string `json:"database,omitempty"`
	Schema   string `json:"schema,omitempty"`
}

// ListSQLDatabasesResult wraps the database/schema listing output.
type ListSQLDatabasesResult struct {
	Databases []SQLDatabaseInfo `json:"databases"`
}

// SQLQueryResult is the unified result type for SQL queries.
type SQLQueryResult struct {
	Columns        []string                 `json:"columns"`
	Rows           []map[string]interface{} `json:"rows"`
	RowCount       int                      `json:"rowCount"`
	ProcessedQuery string                   `json:"processedQuery,omitempty"`
	// Set by the handler in tools/sql_tools.go; typed as *tools.EmptyResultHints.
	Hints interface{} `json:"hints,omitempty"`
}

// SQLTableInfo is the unified result type for table listings.
type SQLTableInfo struct {
	Database string `json:"database,omitempty"`
	Schema   string `json:"schema,omitempty"`
	Name     string `json:"name"`
	Type     string `json:"type,omitempty"`
	Engine   string `json:"engine,omitempty"`
	RowCount int64  `json:"rowCount,omitempty"`
	Bytes    int64  `json:"bytes,omitempty"`
}

// SQLColumnInfo is the unified result type for column descriptions.
type SQLColumnInfo struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Nullable string `json:"nullable,omitempty"`
	Default  string `json:"default,omitempty"`
	Comment  string `json:"comment,omitempty"`
}

// ListSQLTablesResult wraps the table listing output.
type ListSQLTablesResult struct {
	Tables []SQLTableInfo `json:"tables"`
}

// DescribeSQLTableResult wraps the column listing output.
type DescribeSQLTableResult struct {
	Columns []SQLColumnInfo `json:"columns"`
}

// Dialect registry

var (
	dialectsMu sync.RWMutex
	dialects   = map[string]Dialect{}
)

func RegisterDialect(d Dialect) {
	dialectsMu.Lock()
	defer dialectsMu.Unlock()
	for _, t := range d.DatasourceTypes() {
		dialects[t] = d
	}
}

func DialectFor(dsType string) (Dialect, error) {
	dialectsMu.RLock()
	defer dialectsMu.RUnlock()
	if d, ok := dialects[dsType]; ok {
		return d, nil
	}
	return nil, fmt.Errorf("unsupported SQL datasource type %q; supported types: %s", dsType, supportedDialectTypes())
}

func supportedDialectTypes() string {
	dialectsMu.RLock()
	defer dialectsMu.RUnlock()
	types := make([]string, 0, len(dialects))
	for t := range dialects {
		types = append(types, t)
	}
	return strings.Join(types, ", ")
}

// Shared helpers for SQL dialects

var sqlIdentifierRe = regexp.MustCompile(`^[a-zA-Z0-9_]+$`)

func ValidateSQLIdentifier(name, field string) error {
	if name == "" {
		return nil
	}
	if !sqlIdentifierRe.MatchString(name) {
		return fmt.Errorf("invalid %s: must contain only letters, numbers, and underscores", field)
	}
	return nil
}

func EnforceSQLLimit(query string, requestedLimit, defaultLimit, maxLimit int) string {
	limit := requestedLimit
	if limit <= 0 {
		limit = defaultLimit
	}
	if limit > maxLimit {
		limit = maxLimit
	}

	limitRe := regexp.MustCompile(`(?i)\bLIMIT\s+\d+`)
	if limitRe.MatchString(query) {
		query = limitRe.ReplaceAllStringFunc(query, func(match string) string {
			numRe := regexp.MustCompile(`\d+`)
			numStr := numRe.FindString(match)
			existingLimit, _ := strconv.Atoi(numStr)
			if existingLimit > maxLimit {
				return fmt.Sprintf("LIMIT %d", maxLimit)
			}
			return match
		})
		return query
	}

	query = strings.TrimSpace(query)
	query = strings.TrimSuffix(query, ";")
	return fmt.Sprintf("%s LIMIT %d", query, limit)
}

// ToStringFromRow extracts a string from a row value that may be string or
// *string depending on the SDK field type.
func ToStringFromRow(v interface{}) string {
	switch s := v.(type) {
	case string:
		return s
	case *string:
		if s != nil {
			return *s
		}
	}
	return ""
}

// ToInt64FromRow extracts an int64 from a row value that may be any of the
// numeric types (or their pointer variants) the SDK's data.Field can hold.
func ToInt64FromRow(v interface{}) int64 {
	switch n := v.(type) {
	case float64:
		return int64(n)
	case *float64:
		if n != nil {
			return int64(*n)
		}
	case float32:
		return int64(n)
	case *float32:
		if n != nil {
			return int64(*n)
		}
	case int64:
		return n
	case *int64:
		if n != nil {
			return *n
		}
	case int32:
		return int64(n)
	case *int32:
		if n != nil {
			return int64(*n)
		}
	case int16:
		return int64(n)
	case *int16:
		if n != nil {
			return int64(*n)
		}
	case int8:
		return int64(n)
	case *int8:
		if n != nil {
			return int64(*n)
		}
	case uint64:
		return int64(n)
	case *uint64:
		if n != nil {
			return int64(*n)
		}
	case uint32:
		return int64(n)
	case *uint32:
		if n != nil {
			return int64(*n)
		}
	case uint16:
		return int64(n)
	case *uint16:
		if n != nil {
			return int64(*n)
		}
	case uint8:
		return int64(n)
	case *uint8:
		if n != nil {
			return int64(*n)
		}
	case json.Number:
		i, _ := n.Int64()
		return i
	}
	return 0
}

// Datasource type constants used across dialects and run_panel_query.go.
const (
	ClickHouseDatasourceType     = "grafana-clickhouse-datasource"
	SnowflakeDatasourceType      = "grafana-snowflake-datasource"
	AthenaDatasourceType         = "grafana-athena-datasource"
	MySQLDatasourceType          = "mysql"
	PostgresDatasourceType       = "grafana-postgresql-datasource"
	PostgresLegacyDatasourceType = "postgres"
	MSSQLDatasourceType          = "mssql"
	BigQueryDatasourceType       = "grafana-bigquery-datasource"
)
