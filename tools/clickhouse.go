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
	// DefaultClickHouseLimit is the default number of rows to return if not specified
	DefaultClickHouseLimit = 100

	// MaxClickHouseLimit is the maximum number of rows that can be requested
	MaxClickHouseLimit = 1000

	// ClickHouseDatasourceType is the type identifier for ClickHouse datasources
	ClickHouseDatasourceType = "grafana-clickhouse-datasource"

	// ClickHouseFormatTable is the format value for table/tabular query results
	ClickHouseFormatTable = 1
)

var clickHouseIdentifierRe = regexp.MustCompile(`^[a-zA-Z0-9_]+$`)

func validateClickHouseIdentifier(name, field string) error {
	if name == "" {
		return nil
	}
	if !clickHouseIdentifierRe.MatchString(name) {
		return fmt.Errorf("invalid %s: must contain only letters, numbers, and underscores", field)
	}
	return nil
}

// ClickHouseQueryParams defines the parameters for querying ClickHouse
type ClickHouseQueryParams struct {
	DatasourceUID string            `json:"datasourceUid" jsonschema:"required,description=The UID of the ClickHouse datasource to query. Use list_datasources to find available UIDs."`
	Query         string            `json:"query" jsonschema:"required,description=Raw SQL query. Supports ClickHouse macros: $__timeFilter(column) for time filtering\\, $__from/$__to for millisecond timestamps\\, $__interval/$__interval_ms for calculated intervals\\, and ${varname} for variable substitution."`
	Start         string            `json:"start,omitempty" jsonschema:"description=Start time for the query. Time formats: 'now-1h'\\, '2026-02-02T19:00:00Z'\\, '1738519200000' (Unix ms). Defaults to 1 hour ago."`
	End           string            `json:"end,omitempty" jsonschema:"description=End time for the query. Time formats: 'now'\\, '2026-02-02T19:00:00Z'\\, '1738519200000' (Unix ms). Defaults to now."`
	Variables     map[string]string `json:"variables,omitempty" jsonschema:"description=Template variable substitutions as key-value pairs. Variables can be referenced as ${varname} or $varname in the query."`
	Limit         int               `json:"limit,omitempty" jsonschema:"description=Maximum number of rows to return. Default: 100\\, Max: 1000. If query doesn't contain LIMIT\\, one will be appended."`
}

// ClickHouseQueryResult represents the result of a ClickHouse query
type ClickHouseQueryResult struct {
	Columns        []string                 `json:"columns"`
	Rows           []map[string]interface{} `json:"rows"`
	RowCount       int                      `json:"rowCount"`
	ProcessedQuery string                   `json:"processedQuery,omitempty"`
	Hints          *EmptyResultHints        `json:"hints,omitempty"`
}

// substituteClickHouseMacros replaces ClickHouse-specific macros in the query
// Supported macros:
//   - $__timeFilter(column) -> column >= toDateTime(X) AND column <= toDateTime(Y)
//   - $__from -> Unix milliseconds
//   - $__to -> Unix milliseconds
//   - $__interval -> calculated interval string (e.g., "60s")
//   - $__interval_ms -> interval in milliseconds
func substituteClickHouseMacros(query string, from, to time.Time) string {
	fromSeconds := from.Unix()
	toSeconds := to.Unix()
	fromMillis := from.UnixMilli()
	toMillis := to.UnixMilli()

	// Calculate interval based on time range (target ~1000 data points)
	rangeSeconds := toSeconds - fromSeconds
	intervalSeconds := rangeSeconds / 1000
	if intervalSeconds < 1 {
		intervalSeconds = 1
	}

	// $__timeFilter(column) -> column >= toDateTime(X) AND column <= toDateTime(Y)
	// Supports simple identifiers (ts), dotted identifiers (table.ts), and quoted identifiers ("timestamp", `Timestamp`)
	timeFilterRe := regexp.MustCompile(`\$__timeFilter\(([^)]+)\)`)
	query = timeFilterRe.ReplaceAllStringFunc(query, func(match string) string {
		submatch := timeFilterRe.FindStringSubmatch(match)
		if len(submatch) > 1 {
			column := strings.TrimSpace(submatch[1])
			// Remove surrounding quotes/backticks if present for the comparison
			// but keep the original column reference for the query
			return fmt.Sprintf("%s >= toDateTime(%d) AND %s <= toDateTime(%d)", column, fromSeconds, column, toSeconds)
		}
		return match
	})

	// $__from -> Unix milliseconds
	query = strings.ReplaceAll(query, "$__from", strconv.FormatInt(fromMillis, 10))

	// $__to -> Unix milliseconds
	query = strings.ReplaceAll(query, "$__to", strconv.FormatInt(toMillis, 10))

	// $__interval_ms -> interval in milliseconds (must be before $__interval to avoid partial replacement)
	query = strings.ReplaceAll(query, "$__interval_ms", strconv.FormatInt(intervalSeconds*1000, 10))

	// $__interval -> interval string (e.g., "60s")
	query = strings.ReplaceAll(query, "$__interval", fmt.Sprintf("%ds", intervalSeconds))

	return query
}

// enforceClickHouseLimit ensures the query has a LIMIT clause and enforces max limit
func enforceClickHouseLimit(query string, requestedLimit int) string {
	limit := requestedLimit
	if limit <= 0 {
		limit = DefaultClickHouseLimit
	}
	if limit > MaxClickHouseLimit {
		limit = MaxClickHouseLimit
	}

	// Check if query already has a LIMIT clause
	limitRe := regexp.MustCompile(`(?i)\bLIMIT\s+\d+`)
	if limitRe.MatchString(query) {
		// Replace existing limit if it exceeds max
		query = limitRe.ReplaceAllStringFunc(query, func(match string) string {
			// Extract the number from the match
			numRe := regexp.MustCompile(`\d+`)
			numStr := numRe.FindString(match)
			existingLimit, _ := strconv.Atoi(numStr)
			if existingLimit > MaxClickHouseLimit {
				return fmt.Sprintf("LIMIT %d", MaxClickHouseLimit)
			}
			return match
		})
		return query
	}

	// Append LIMIT clause
	query = strings.TrimSpace(query)
	query = strings.TrimSuffix(query, ";")
	return fmt.Sprintf("%s LIMIT %d", query, limit)
}

// queryClickHouse executes a ClickHouse query via Grafana
func queryClickHouse(ctx context.Context, args ClickHouseQueryParams) (*ClickHouseQueryResult, error) {
	ds, err := getDatasourceByUID(ctx, GetDatasourceByUIDParams{UID: args.DatasourceUID})
	if err != nil {
		return nil, fmt.Errorf("creating ClickHouse client: %w", err)
	}
	if ds.Type != ClickHouseDatasourceType {
		return nil, fmt.Errorf("datasource %s is of type %s, not %s", args.DatasourceUID, ds.Type, ClickHouseDatasourceType)
	}

	client, baseURL, err := newDSQueryHTTPClient(ctx)
	if err != nil {
		return nil, err
	}

	// Parse time range
	now := time.Now()
	fromTime := now.Add(-1 * time.Hour) // Default: 1 hour ago
	toTime := now                       // Default: now

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

	// Process the query
	processedQuery := args.Query
	processedQuery = substituteClickHouseMacros(processedQuery, fromTime, toTime)
	processedQuery = substituteVariables(processedQuery, args.Variables)
	processedQuery = enforceClickHouseLimit(processedQuery, args.Limit)

	payload := dsQueryPayload(fromTime, toTime, map[string]interface{}{
		"datasource": map[string]string{
			"uid":  args.DatasourceUID,
			"type": ClickHouseDatasourceType,
		},
		"rawSql": processedQuery,
		"refId":  "A",
		"format": ClickHouseFormatTable,
	})

	resp, err := doDSQuery(ctx, client, baseURL, payload)
	if err != nil {
		return nil, err
	}

	columns, rows, err := framesToTabularRows(resp)
	if err != nil {
		return nil, err
	}

	result := &ClickHouseQueryResult{
		Columns:        columns,
		Rows:           rows,
		RowCount:       len(rows),
		ProcessedQuery: processedQuery,
	}

	if result.RowCount == 0 {
		result.Hints = GenerateEmptyResultHints(HintContext{
			DatasourceType: "clickhouse",
			Query:          args.Query,
			ProcessedQuery: processedQuery,
			StartTime:      fromTime,
			EndTime:        toTime,
		})
	}

	return result, nil
}

// QueryClickHouse is a tool for querying ClickHouse datasources via Grafana
var QueryClickHouse = mcpgrafana.MustTool(
	"query_clickhouse",
	`Query ClickHouse via Grafana. REQUIRED FIRST: Use list_clickhouse_tables to find tables, then describe_clickhouse_table to see column schemas, then query.

Supports macros: $__timeFilter(column), $__from, $__to, $__interval, ${varname}

Time formats: 'now-1h', '2026-02-02T19:00:00Z', '1738519200000' (Unix ms)

Example: SELECT Timestamp, Body FROM otel_logs WHERE $__timeFilter(Timestamp)`,
	queryClickHouse,
	mcp.WithTitleAnnotation("Query ClickHouse"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
)

// ListClickHouseTablesParams defines the parameters for listing ClickHouse tables
type ListClickHouseTablesParams struct {
	DatasourceUID string `json:"datasourceUid" jsonschema:"required,description=The UID of the ClickHouse datasource"`
	Database      string `json:"database,omitempty" jsonschema:"description=Database name to filter tables (lists all non-system databases if not specified)"`
}

// ClickHouseTableInfo represents information about a ClickHouse table
type ClickHouseTableInfo struct {
	Database   string `json:"database"`
	Name       string `json:"name"`
	Engine     string `json:"engine"`
	TotalRows  int64  `json:"totalRows"`
	TotalBytes int64  `json:"totalBytes"`
}

// listClickHouseTables lists tables from a ClickHouse datasource
func listClickHouseTables(ctx context.Context, args ListClickHouseTablesParams) ([]ClickHouseTableInfo, error) {
	if err := validateClickHouseIdentifier(args.Database, "database"); err != nil {
		return nil, err
	}

	// Build the query to list tables
	query := `SELECT database, name, engine, total_rows, total_bytes
FROM system.tables
WHERE database NOT IN ('system', 'INFORMATION_SCHEMA', 'information_schema')`
	if args.Database != "" {
		query += fmt.Sprintf(" AND database = '%s'", args.Database)
	}
	query += " ORDER BY database, name LIMIT 500"

	// Use the existing query infrastructure
	result, err := queryClickHouse(ctx, ClickHouseQueryParams{
		DatasourceUID: args.DatasourceUID,
		Query:         query,
		Limit:         500,
	})
	if err != nil {
		return nil, err
	}

	// Convert rows to table info
	tables := make([]ClickHouseTableInfo, 0, len(result.Rows))
	for _, row := range result.Rows {
		table := ClickHouseTableInfo{
			Database:   toStringFromRow(row["database"]),
			Name:       toStringFromRow(row["name"]),
			Engine:     toStringFromRow(row["engine"]),
			TotalRows:  toInt64FromRow(row["total_rows"]),
			TotalBytes: toInt64FromRow(row["total_bytes"]),
		}
		tables = append(tables, table)
	}

	return tables, nil
}

// ListClickHouseTables is a tool for listing ClickHouse tables
var ListClickHouseTables = mcpgrafana.MustTool(
	"list_clickhouse_tables",
	"START HERE for ClickHouse: List available tables (name, database, engine, row count, size). NEXT: Use describe_clickhouse_table to see column schemas.",
	listClickHouseTables,
	mcp.WithTitleAnnotation("List ClickHouse tables"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
)

// DescribeClickHouseTableParams defines the parameters for describing a ClickHouse table
type DescribeClickHouseTableParams struct {
	DatasourceUID string `json:"datasourceUid" jsonschema:"required,description=The UID of the ClickHouse datasource"`
	Table         string `json:"table" jsonschema:"required,description=Table name to describe"`
	Database      string `json:"database,omitempty" jsonschema:"description=Database name (defaults to 'default')"`
}

// ClickHouseColumnInfo represents information about a ClickHouse column
type ClickHouseColumnInfo struct {
	Name              string `json:"name"`
	Type              string `json:"type"`
	DefaultType       string `json:"defaultType,omitempty"`
	DefaultExpression string `json:"defaultExpression,omitempty"`
	Comment           string `json:"comment,omitempty"`
}

// describeClickHouseTable describes a ClickHouse table schema
func describeClickHouseTable(ctx context.Context, args DescribeClickHouseTableParams) ([]ClickHouseColumnInfo, error) {
	database := args.Database
	if database == "" {
		database = "default"
	}

	if err := validateClickHouseIdentifier(database, "database"); err != nil {
		return nil, err
	}

	if args.Table == "" {
		return nil, fmt.Errorf("table is required")
	}

	if err := validateClickHouseIdentifier(args.Table, "table"); err != nil {
		return nil, err
	}

	// Query system.columns instead of using DESCRIBE TABLE
	// This avoids the LIMIT clause issue with DESCRIBE statements
	query := fmt.Sprintf(`SELECT name, type, default_kind as default_type, default_expression, comment
FROM system.columns
WHERE database = '%s' AND table = '%s'
ORDER BY position`, database, args.Table)

	// Use the existing query infrastructure
	result, err := queryClickHouse(ctx, ClickHouseQueryParams{
		DatasourceUID: args.DatasourceUID,
		Query:         query,
		Limit:         1000,
	})
	if err != nil {
		return nil, err
	}

	// Convert rows to column info
	columns := make([]ClickHouseColumnInfo, 0, len(result.Rows))
	for _, row := range result.Rows {
		columns = append(columns, ClickHouseColumnInfo{
			Name:              toStringFromRow(row["name"]),
			Type:              toStringFromRow(row["type"]),
			DefaultType:       toStringFromRow(row["default_type"]),
			DefaultExpression: toStringFromRow(row["default_expression"]),
			Comment:           toStringFromRow(row["comment"]),
		})
	}

	return columns, nil
}

// DescribeClickHouseTable is a tool for describing a ClickHouse table schema
var DescribeClickHouseTable = mcpgrafana.MustTool(
	"describe_clickhouse_table",
	"Get column schema for a ClickHouse table. Pass the database from list_clickhouse_tables results. NEXT: Use query_clickhouse with discovered column names.",
	describeClickHouseTable,
	mcp.WithTitleAnnotation("Describe ClickHouse table"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
)

// AddClickHouseTools registers all ClickHouse tools with the MCP server
func AddClickHouseTools(mcp *server.MCPServer) {
	QueryClickHouse.Register(mcp)
	ListClickHouseTables.Register(mcp)
	DescribeClickHouseTable.Register(mcp)
}
