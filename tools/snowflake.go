package tools

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	mcpgrafana "github.com/grafana/mcp-grafana"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

const (
	// DefaultSnowflakeLimit is the default number of rows to return if not specified
	DefaultSnowflakeLimit = 100

	// MaxSnowflakeLimit is the maximum number of rows that can be requested
	MaxSnowflakeLimit = 1000

	// SnowflakeDatasourceType is the type identifier for Snowflake datasources
	// (Grafana Enterprise's Snowflake plugin).
	SnowflakeDatasourceType = "grafana-snowflake-datasource"

	// SnowflakeFormatTable is the format value for table/tabular query results
	SnowflakeFormatTable = 1

	// snowflakeTimeFormat is the layout used when emitting Snowflake timestamp
	// literals from $__timeFilter / $__timeFrom / $__timeTo. Snowflake parses
	// 'YYYY-MM-DD HH:MI:SS' as a TIMESTAMP_NTZ in UTC.
	snowflakeTimeFormat = "2006-01-02 15:04:05"
)

var snowflakeIdentifierRe = regexp.MustCompile(`^[a-zA-Z0-9_]+$`)

func validateSnowflakeIdentifier(name, field string) error {
	if name == "" {
		return nil
	}
	if !snowflakeIdentifierRe.MatchString(name) {
		return fmt.Errorf("invalid %s: must contain only letters, numbers, and underscores", field)
	}
	return nil
}

// SnowflakeQueryParams defines the parameters for querying Snowflake
type SnowflakeQueryParams struct {
	DatasourceUID string            `json:"datasourceUid" jsonschema:"required,description=The UID of the Snowflake datasource to query. Use list_datasources to find available UIDs."`
	Query         string            `json:"query" jsonschema:"required,description=Raw SQL query. Supports Snowflake macros: $__timeFilter(column) for time filtering\\, $__timeFrom/$__timeTo for TIMESTAMP_NTZ literals\\, $__from/$__to for millisecond timestamps\\, $__interval/$__interval_ms for calculated intervals\\, and ${varname} for variable substitution. Use 3-part names (DATABASE.SCHEMA.TABLE) for cross-database queries; SNOWFLAKE.TELEMETRY.EVENTS is the standard event table."`
	Start         string            `json:"start,omitempty" jsonschema:"description=Start time for the query. Time formats: 'now-1h'\\, '2026-02-02T19:00:00Z'\\, '1738519200000' (Unix ms). Defaults to 1 hour ago."`
	End           string            `json:"end,omitempty" jsonschema:"description=End time for the query. Time formats: 'now'\\, '2026-02-02T19:00:00Z'\\, '1738519200000' (Unix ms). Defaults to now."`
	Variables     map[string]string `json:"variables,omitempty" jsonschema:"description=Template variable substitutions as key-value pairs. Variables can be referenced as ${varname} or $varname in the query."`
	Limit         int               `json:"limit,omitempty" jsonschema:"description=Maximum number of rows to return. Default: 100\\, Max: 1000. If query doesn't contain LIMIT\\, one will be appended."`
}

// SnowflakeQueryResult represents the result of a Snowflake query
type SnowflakeQueryResult struct {
	Columns        []string                 `json:"columns"`
	Rows           []map[string]interface{} `json:"rows"`
	RowCount       int                      `json:"rowCount"`
	ProcessedQuery string                   `json:"processedQuery,omitempty"`
	Hints          *EmptyResultHints        `json:"hints,omitempty"`
}

// substituteSnowflakeMacros replaces Snowflake-specific macros in the query.
// Supported macros:
//   - $__timeFilter(column) -> column >= TO_TIMESTAMP_NTZ('YYYY-MM-DD HH:MI:SS') AND column <= ...
//   - $__timeFrom            -> TO_TIMESTAMP_NTZ('YYYY-MM-DD HH:MI:SS')
//   - $__timeTo              -> TO_TIMESTAMP_NTZ('YYYY-MM-DD HH:MI:SS')
//   - $__from                -> Unix milliseconds (integer literal)
//   - $__to                  -> Unix milliseconds (integer literal)
//   - $__interval            -> calculated interval in seconds (integer; suitable for TIME_SLICE)
//   - $__interval_ms         -> calculated interval in milliseconds (integer)
func substituteSnowflakeMacros(query string, from, to time.Time) string {
	fromMillis := from.UnixMilli()
	toMillis := to.UnixMilli()
	fromStr := from.UTC().Format(snowflakeTimeFormat)
	toStr := to.UTC().Format(snowflakeTimeFormat)

	rangeSeconds := to.Unix() - from.Unix()
	intervalSeconds := rangeSeconds / 1000
	if intervalSeconds < 1 {
		intervalSeconds = 1
	}

	// $__timeFilter(column) - supports simple, dotted, and quoted identifiers
	timeFilterRe := regexp.MustCompile(`\$__timeFilter\(([^)]+)\)`)
	query = timeFilterRe.ReplaceAllStringFunc(query, func(match string) string {
		submatch := timeFilterRe.FindStringSubmatch(match)
		if len(submatch) > 1 {
			column := strings.TrimSpace(submatch[1])
			return fmt.Sprintf("%s >= TO_TIMESTAMP_NTZ('%s') AND %s <= TO_TIMESTAMP_NTZ('%s')",
				column, fromStr, column, toStr)
		}
		return match
	})

	// $__timeFrom and $__timeTo - emit TIMESTAMP_NTZ literals.
	query = strings.ReplaceAll(query, "$__timeFrom", fmt.Sprintf("TO_TIMESTAMP_NTZ('%s')", fromStr))
	query = strings.ReplaceAll(query, "$__timeTo", fmt.Sprintf("TO_TIMESTAMP_NTZ('%s')", toStr))

	// $__from / $__to -> Unix milliseconds
	query = strings.ReplaceAll(query, "$__from", strconv.FormatInt(fromMillis, 10))
	query = strings.ReplaceAll(query, "$__to", strconv.FormatInt(toMillis, 10))

	// $__interval_ms before $__interval to avoid partial replacement
	query = strings.ReplaceAll(query, "$__interval_ms", strconv.FormatInt(intervalSeconds*1000, 10))
	query = strings.ReplaceAll(query, "$__interval", strconv.FormatInt(intervalSeconds, 10))

	return query
}

// enforceSnowflakeLimit ensures the query has a LIMIT clause and enforces max limit
func enforceSnowflakeLimit(query string, requestedLimit int) string {
	limit := requestedLimit
	if limit <= 0 {
		limit = DefaultSnowflakeLimit
	}
	if limit > MaxSnowflakeLimit {
		limit = MaxSnowflakeLimit
	}

	limitRe := regexp.MustCompile(`(?i)\bLIMIT\s+\d+`)
	if limitRe.MatchString(query) {
		query = limitRe.ReplaceAllStringFunc(query, func(match string) string {
			numRe := regexp.MustCompile(`\d+`)
			numStr := numRe.FindString(match)
			existingLimit, _ := strconv.Atoi(numStr)
			if existingLimit > MaxSnowflakeLimit {
				return fmt.Sprintf("LIMIT %d", MaxSnowflakeLimit)
			}
			return match
		})
		return query
	}

	query = strings.TrimSpace(query)
	query = strings.TrimSuffix(query, ";")
	return fmt.Sprintf("%s LIMIT %d", query, limit)
}

// querySnowflake executes a Snowflake query via Grafana
func querySnowflake(ctx context.Context, args SnowflakeQueryParams) (*SnowflakeQueryResult, error) {
	ds, err := getDatasourceByUID(ctx, GetDatasourceByUIDParams{UID: args.DatasourceUID})
	if err != nil {
		return nil, fmt.Errorf("creating Snowflake client: %w", err)
	}
	if ds.Type != SnowflakeDatasourceType {
		return nil, fmt.Errorf("datasource %s is of type %s, not %s", args.DatasourceUID, ds.Type, SnowflakeDatasourceType)
	}

	client, baseURL, err := newDSQueryHTTPClient(ctx)
	if err != nil {
		return nil, err
	}

	now := time.Now()
	fromTime := now.Add(-1 * time.Hour)
	toTime := now

	if args.Start != "" {
		parsed, err := parseStartTime(args.Start)
		if err != nil {
			return nil, fmt.Errorf("parsing start time: %w", err)
		}
		if !parsed.IsZero() {
			fromTime = parsed
		}
	}

	if args.End != "" {
		parsed, err := parseEndTime(args.End)
		if err != nil {
			return nil, fmt.Errorf("parsing end time: %w", err)
		}
		if !parsed.IsZero() {
			toTime = parsed
		}
	}

	processedQuery := args.Query
	processedQuery = substituteSnowflakeMacros(processedQuery, fromTime, toTime)
	processedQuery = substituteVariables(processedQuery, args.Variables)
	processedQuery = enforceSnowflakeLimit(processedQuery, args.Limit)

	payload := dsQueryPayload(fromTime, toTime, map[string]interface{}{
		"datasource": map[string]string{
			"uid":  args.DatasourceUID,
			"type": SnowflakeDatasourceType,
		},
		"rawSql": processedQuery,
		"refId":  "A",
		"format": SnowflakeFormatTable,
	})

	resp, err := doDSQuery(ctx, client, baseURL, payload)
	if err != nil {
		return nil, err
	}

	columns, rows, err := framesToTabularRows(resp)
	if err != nil {
		return nil, err
	}

	result := &SnowflakeQueryResult{
		Columns:        columns,
		Rows:           rows,
		RowCount:       len(rows),
		ProcessedQuery: processedQuery,
	}

	if result.RowCount == 0 {
		result.Hints = GenerateEmptyResultHints(HintContext{
			DatasourceType: "snowflake",
			Query:          args.Query,
			ProcessedQuery: processedQuery,
			StartTime:      fromTime,
			EndTime:        toTime,
		})
	}

	return result, nil
}

// QuerySnowflake is a tool for querying Snowflake datasources via Grafana
var QuerySnowflake = mcpgrafana.MustTool(
	"query_snowflake",
	`Query Snowflake via Grafana. REQUIRED FIRST: Use list_snowflake_tables to find tables (filter by database/schema), then describe_snowflake_table to see column schemas, then query.

Supports macros: $__timeFilter(column), $__timeFrom, $__timeTo, $__from, $__to, $__interval, $__interval_ms, ${varname}

Time formats: 'now-1h', '2026-02-02T19:00:00Z', '1738519200000' (Unix ms)

Snowflake event tables (telemetry from logging APIs/auto-instrumentation) live in the database/schema configured by the EVENT TABLE setting; the standard one is SNOWFLAKE.TELEMETRY.EVENTS.

Example: SELECT TIMESTAMP, RECORD['severity_text']::STRING AS LEVEL, VALUE FROM SNOWFLAKE.TELEMETRY.EVENTS WHERE $__timeFilter(TIMESTAMP) AND RECORD_TYPE = 'LOG'`,
	querySnowflake,
	mcp.WithTitleAnnotation("Query Snowflake"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
)

// ListSnowflakeTablesParams defines the parameters for listing Snowflake tables
type ListSnowflakeTablesParams struct {
	DatasourceUID string `json:"datasourceUid" jsonschema:"required,description=The UID of the Snowflake datasource"`
	Database      string `json:"database,omitempty" jsonschema:"description=Database name to scan (uses INFORMATION_SCHEMA in this database). Defaults to the datasource's configured database."`
	Schema        string `json:"schema,omitempty" jsonschema:"description=Schema name to filter tables (lists all non-system schemas if not specified)."`
}

// SnowflakeTableInfo represents information about a Snowflake table
type SnowflakeTableInfo struct {
	Database string `json:"database"`
	Schema   string `json:"schema"`
	Name     string `json:"name"`
	Kind     string `json:"kind"`
	RowCount int64  `json:"rowCount"`
	Bytes    int64  `json:"bytes"`
}

// listSnowflakeTables lists tables from a Snowflake datasource via INFORMATION_SCHEMA.TABLES.
//
// Note: Snowflake's INFORMATION_SCHEMA is per-database. If the user supplies a
// database we qualify the FROM clause; otherwise we leave it unqualified and
// rely on the datasource's configured default database.
func listSnowflakeTables(ctx context.Context, args ListSnowflakeTablesParams) ([]SnowflakeTableInfo, error) {
	if err := validateSnowflakeIdentifier(args.Database, "database"); err != nil {
		return nil, err
	}
	if err := validateSnowflakeIdentifier(args.Schema, "schema"); err != nil {
		return nil, err
	}

	from := "INFORMATION_SCHEMA.TABLES"
	if args.Database != "" {
		from = fmt.Sprintf("%s.INFORMATION_SCHEMA.TABLES", args.Database)
	}

	query := fmt.Sprintf(`SELECT TABLE_CATALOG, TABLE_SCHEMA, TABLE_NAME, TABLE_TYPE, ROW_COUNT, BYTES
FROM %s
WHERE TABLE_SCHEMA NOT IN ('INFORMATION_SCHEMA')`, from)
	if args.Schema != "" {
		query += fmt.Sprintf(" AND TABLE_SCHEMA = '%s'", args.Schema)
	}
	query += " ORDER BY TABLE_CATALOG, TABLE_SCHEMA, TABLE_NAME LIMIT 500"

	result, err := querySnowflake(ctx, SnowflakeQueryParams{
		DatasourceUID: args.DatasourceUID,
		Query:         query,
		Limit:         500,
	})
	if err != nil {
		return nil, err
	}

	tables := make([]SnowflakeTableInfo, 0, len(result.Rows))
	for _, row := range result.Rows {
		t := SnowflakeTableInfo{
			Database: toStringFromRow(row["TABLE_CATALOG"]),
			Schema:   toStringFromRow(row["TABLE_SCHEMA"]),
			Name:     toStringFromRow(row["TABLE_NAME"]),
			Kind:     toStringFromRow(row["TABLE_TYPE"]),
			RowCount: toInt64FromRow(row["ROW_COUNT"]),
			Bytes:    toInt64FromRow(row["BYTES"]),
		}
		tables = append(tables, t)
	}

	return tables, nil
}

// ListSnowflakeTables is a tool for listing Snowflake tables
var ListSnowflakeTables = mcpgrafana.MustTool(
	"list_snowflake_tables",
	"START HERE for Snowflake: List available tables (database, schema, name, kind, row count, size) via INFORMATION_SCHEMA. NEXT: Use describe_snowflake_table to see column schemas.",
	listSnowflakeTables,
	mcp.WithTitleAnnotation("List Snowflake tables"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
)

// DescribeSnowflakeTableParams defines the parameters for describing a Snowflake table
type DescribeSnowflakeTableParams struct {
	DatasourceUID string `json:"datasourceUid" jsonschema:"required,description=The UID of the Snowflake datasource"`
	Table         string `json:"table" jsonschema:"required,description=Table name to describe"`
	Schema        string `json:"schema,omitempty" jsonschema:"description=Schema name (defaults to 'PUBLIC')"`
	Database      string `json:"database,omitempty" jsonschema:"description=Database name (defaults to the datasource's configured database)"`
}

// SnowflakeColumnInfo represents information about a Snowflake column
type SnowflakeColumnInfo struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Nullable string `json:"nullable,omitempty"`
	Default  string `json:"default,omitempty"`
	Comment  string `json:"comment,omitempty"`
}

// describeSnowflakeTable describes a Snowflake table schema via INFORMATION_SCHEMA.COLUMNS
func describeSnowflakeTable(ctx context.Context, args DescribeSnowflakeTableParams) ([]SnowflakeColumnInfo, error) {
	if args.Table == "" {
		return nil, fmt.Errorf("table is required")
	}

	schema := args.Schema
	if schema == "" {
		schema = "PUBLIC"
	}

	if err := validateSnowflakeIdentifier(args.Database, "database"); err != nil {
		return nil, err
	}
	if err := validateSnowflakeIdentifier(schema, "schema"); err != nil {
		return nil, err
	}
	if err := validateSnowflakeIdentifier(args.Table, "table"); err != nil {
		return nil, err
	}

	from := "INFORMATION_SCHEMA.COLUMNS"
	if args.Database != "" {
		from = fmt.Sprintf("%s.INFORMATION_SCHEMA.COLUMNS", args.Database)
	}

	query := fmt.Sprintf(`SELECT COLUMN_NAME, DATA_TYPE, IS_NULLABLE, COLUMN_DEFAULT, COMMENT
FROM %s
WHERE TABLE_SCHEMA = '%s' AND TABLE_NAME = '%s'
ORDER BY ORDINAL_POSITION`, from, schema, args.Table)

	result, err := querySnowflake(ctx, SnowflakeQueryParams{
		DatasourceUID: args.DatasourceUID,
		Query:         query,
		Limit:         1000,
	})
	if err != nil {
		return nil, err
	}

	columns := make([]SnowflakeColumnInfo, 0, len(result.Rows))
	for _, row := range result.Rows {
		col := SnowflakeColumnInfo{
			Name:     toStringFromRow(row["COLUMN_NAME"]),
			Type:     toStringFromRow(row["DATA_TYPE"]),
			Nullable: toStringFromRow(row["IS_NULLABLE"]),
			Default:  toStringFromRow(row["COLUMN_DEFAULT"]),
			Comment:  toStringFromRow(row["COMMENT"]),
		}
		columns = append(columns, col)
	}

	return columns, nil
}

// DescribeSnowflakeTable is a tool for describing a Snowflake table schema
var DescribeSnowflakeTable = mcpgrafana.MustTool(
	"describe_snowflake_table",
	"Get column schema for a Snowflake table. Pass the database/schema from list_snowflake_tables results. NEXT: Use query_snowflake with discovered column names.",
	describeSnowflakeTable,
	mcp.WithTitleAnnotation("Describe Snowflake table"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
)

// AddSnowflakeTools registers all Snowflake tools with the MCP server
func AddSnowflakeTools(mcp *server.MCPServer) {
	QuerySnowflake.Register(mcp)
	ListSnowflakeTables.Register(mcp)
	DescribeSnowflakeTable.Register(mcp)
}
