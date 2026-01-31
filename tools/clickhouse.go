package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/grafana/grafana-plugin-sdk-go/backend/gtime"
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

// ClickHouseQueryParams defines the parameters for querying ClickHouse
type ClickHouseQueryParams struct {
	DatasourceUID string            `json:"datasourceUid" jsonschema:"required,description=The UID of the ClickHouse datasource to query. Use list_datasources to find available UIDs."`
	Query         string            `json:"query" jsonschema:"required,description=Raw SQL query. Supports ClickHouse macros: $__timeFilter(column) for time filtering\\, $__from/$__to for millisecond timestamps\\, $__interval/$__interval_ms for calculated intervals\\, and ${varname} for variable substitution."`
	Start         string            `json:"start,omitempty" jsonschema:"description=Start time for the query. Supports RFC3339\\, relative times (now-1h\\, now-6h)\\, or Unix milliseconds. Defaults to 1 hour ago."`
	End           string            `json:"end,omitempty" jsonschema:"description=End time for the query. Supports RFC3339\\, relative times (now)\\, or Unix milliseconds. Defaults to now."`
	Variables     map[string]string `json:"variables,omitempty" jsonschema:"description=Template variable substitutions as key-value pairs. Variables can be referenced as ${varname} or $varname in the query."`
	Limit         int               `json:"limit,omitempty" jsonschema:"description=Maximum number of rows to return. Default: 100\\, Max: 1000. If query doesn't contain LIMIT\\, one will be appended."`
}

// ClickHouseQueryResult represents the result of a ClickHouse query
type ClickHouseQueryResult struct {
	Columns  []string                 `json:"columns"`
	Rows     []map[string]interface{} `json:"rows"`
	RowCount int                      `json:"rowCount"`
	Hints    []string                 `json:"hints,omitempty"`
}

// clickHouseQueryResponse represents the raw API response from Grafana's /api/ds/query
type clickHouseQueryResponse struct {
	Results map[string]struct {
		Status int `json:"status,omitempty"`
		Frames []struct {
			Schema struct {
				Name   string `json:"name,omitempty"`
				RefID  string `json:"refId,omitempty"`
				Fields []struct {
					Name     string `json:"name"`
					Type     string `json:"type"`
					TypeInfo struct {
						Frame string `json:"frame,omitempty"`
					} `json:"typeInfo,omitempty"`
				} `json:"fields"`
			} `json:"schema"`
			Data struct {
				Values [][]interface{} `json:"values"`
			} `json:"data"`
		} `json:"frames,omitempty"`
		Error string `json:"error,omitempty"`
	} `json:"results"`
}

// clickHouseClient handles communication with Grafana's ClickHouse datasource
type clickHouseClient struct {
	httpClient *http.Client
	baseURL    string
}

// newClickHouseClient creates a new ClickHouse client for the given datasource
func newClickHouseClient(ctx context.Context, uid string) (*clickHouseClient, error) {
	// Verify the datasource exists and is a ClickHouse datasource
	ds, err := getDatasourceByUID(ctx, GetDatasourceByUIDParams{UID: uid})
	if err != nil {
		return nil, err
	}

	if ds.Type != ClickHouseDatasourceType {
		return nil, fmt.Errorf("datasource %s is of type %s, not %s", uid, ds.Type, ClickHouseDatasourceType)
	}

	cfg := mcpgrafana.GrafanaConfigFromContext(ctx)
	baseURL := strings.TrimRight(cfg.URL, "/")

	// Create custom transport with TLS configuration if available
	var transport = http.DefaultTransport
	if tlsConfig := cfg.TLSConfig; tlsConfig != nil {
		var err error
		transport, err = tlsConfig.HTTPTransport(transport.(*http.Transport))
		if err != nil {
			return nil, fmt.Errorf("failed to create custom transport: %w", err)
		}
	}

	transport = NewAuthRoundTripper(transport, cfg.AccessToken, cfg.IDToken, cfg.APIKey, cfg.BasicAuth)
	transport = mcpgrafana.NewOrgIDRoundTripper(transport, cfg.OrgID)

	client := &http.Client{
		Transport: mcpgrafana.NewUserAgentTransport(transport),
	}

	return &clickHouseClient{
		httpClient: client,
		baseURL:    baseURL,
	}, nil
}

// query executes a ClickHouse query via Grafana's /api/ds/query endpoint
func (c *clickHouseClient) query(ctx context.Context, datasourceUID, rawSQL string, from, to time.Time) (*clickHouseQueryResponse, error) {
	// Build the query payload
	payload := map[string]interface{}{
		"queries": []map[string]interface{}{
			{
				"datasource": map[string]string{
					"uid":  datasourceUID,
					"type": ClickHouseDatasourceType,
				},
				"rawSql": rawSQL,
				"refId":  "A",
				"format": ClickHouseFormatTable,
			},
		},
		"from": strconv.FormatInt(from.UnixMilli(), 10),
		"to":   strconv.FormatInt(to.UnixMilli(), 10),
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshaling query payload: %w", err)
	}

	url := c.baseURL + "/api/ds/query"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payloadBytes))
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("executing request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("ClickHouse query returned status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	// Read and parse response
	body := io.LimitReader(resp.Body, 1024*1024*48) // 48MB limit
	bodyBytes, err := io.ReadAll(body)
	if err != nil {
		return nil, fmt.Errorf("reading response body: %w", err)
	}

	var queryResp clickHouseQueryResponse
	if err := json.Unmarshal(bodyBytes, &queryResp); err != nil {
		return nil, fmt.Errorf("unmarshaling response: %w", err)
	}

	return &queryResp, nil
}

// parseClickHouseStartTime parses start time strings in various formats
// Supports: "now", "now-Xs/m/h/d/w", RFC3339, ISO dates, and Unix timestamps
func parseClickHouseStartTime(timeStr string) (time.Time, error) {
	if timeStr == "" {
		return time.Time{}, nil
	}

	tr := gtime.TimeRange{
		From: timeStr,
		Now:  time.Now(),
	}
	return tr.ParseFrom()
}

// parseClickHouseEndTime parses end time strings in various formats
// For end times, date-only strings resolve to end of day rather than start
func parseClickHouseEndTime(timeStr string) (time.Time, error) {
	if timeStr == "" {
		return time.Time{}, nil
	}

	tr := gtime.TimeRange{
		To:  timeStr,
		Now: time.Now(),
	}
	return tr.ParseTo()
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

// substituteVariables replaces template variables in the query
// Supports both ${varname} and $varname patterns
func substituteVariables(query string, variables map[string]string) string {
	if variables == nil {
		return query
	}

	for name, value := range variables {
		// Replace ${varname} pattern
		query = strings.ReplaceAll(query, fmt.Sprintf("${%s}", name), value)
		// Replace $varname pattern (with word boundary)
		varRe := regexp.MustCompile(fmt.Sprintf(`\$%s\b`, regexp.QuoteMeta(name)))
		query = varRe.ReplaceAllString(query, value)
	}

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
	client, err := newClickHouseClient(ctx, args.DatasourceUID)
	if err != nil {
		return nil, fmt.Errorf("creating ClickHouse client: %w", err)
	}

	// Parse time range
	now := time.Now()
	fromTime := now.Add(-1 * time.Hour) // Default: 1 hour ago
	toTime := now                       // Default: now

	if args.Start != "" {
		parsed, err := parseClickHouseStartTime(args.Start)
		if err != nil {
			return nil, fmt.Errorf("parsing start time: %w", err)
		}
		if !parsed.IsZero() {
			fromTime = parsed
		}
	}

	if args.End != "" {
		parsed, err := parseClickHouseEndTime(args.End)
		if err != nil {
			return nil, fmt.Errorf("parsing end time: %w", err)
		}
		if !parsed.IsZero() {
			toTime = parsed
		}
	}

	// Process the query
	processedQuery := args.Query

	// Substitute ClickHouse macros
	processedQuery = substituteClickHouseMacros(processedQuery, fromTime, toTime)

	// Substitute user variables
	processedQuery = substituteVariables(processedQuery, args.Variables)

	// Enforce limit
	processedQuery = enforceClickHouseLimit(processedQuery, args.Limit)

	// Execute query
	resp, err := client.query(ctx, args.DatasourceUID, processedQuery, fromTime, toTime)
	if err != nil {
		return nil, err
	}

	// Process response
	result := &ClickHouseQueryResult{
		Columns: []string{},
		Rows:    []map[string]interface{}{},
	}

	// Check for errors in the response
	for refID, r := range resp.Results {
		if r.Error != "" {
			return nil, fmt.Errorf("query error (refId=%s): %s", refID, r.Error)
		}

		// Process frames
		for _, frame := range r.Frames {
			// Extract column names
			columns := make([]string, len(frame.Schema.Fields))
			for i, field := range frame.Schema.Fields {
				columns[i] = field.Name
			}
			result.Columns = columns

			// Convert columnar data to rows
			if len(frame.Data.Values) == 0 {
				continue
			}

			rowCount := len(frame.Data.Values[0])
			for i := 0; i < rowCount; i++ {
				row := make(map[string]interface{})
				for colIdx, colName := range columns {
					if colIdx < len(frame.Data.Values) && i < len(frame.Data.Values[colIdx]) {
						row[colName] = frame.Data.Values[colIdx][i]
					}
				}
				result.Rows = append(result.Rows, row)
			}
		}
	}

	result.RowCount = len(result.Rows)

	// Add hints if no data was found
	if result.RowCount == 0 {
		result.Hints = GenerateEmptyResultHints("clickhouse")
	}

	return result, nil
}

// QueryClickHouse is a tool for querying ClickHouse datasources via Grafana
var QueryClickHouse = mcpgrafana.MustTool(
	"query_clickhouse",
	`Query ClickHouse via Grafana. DISCOVER FIRST: Use list_clickhouse_tables to find tables, then describe_clickhouse_table to see column schemas.

Supports macros: $__timeFilter(column), $__from, $__to, $__interval, ${varname}

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
		table := ClickHouseTableInfo{}
		if v, ok := row["database"].(string); ok {
			table.Database = v
		}
		if v, ok := row["name"].(string); ok {
			table.Name = v
		}
		if v, ok := row["engine"].(string); ok {
			table.Engine = v
		}
		if v, ok := row["total_rows"].(float64); ok {
			table.TotalRows = int64(v)
		}
		if v, ok := row["total_bytes"].(float64); ok {
			table.TotalBytes = int64(v)
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
		col := ClickHouseColumnInfo{}
		if v, ok := row["name"].(string); ok {
			col.Name = v
		}
		if v, ok := row["type"].(string); ok {
			col.Type = v
		}
		if v, ok := row["default_type"].(string); ok {
			col.DefaultType = v
		}
		if v, ok := row["default_expression"].(string); ok {
			col.DefaultExpression = v
		}
		if v, ok := row["comment"].(string); ok {
			col.Comment = v
		}
		columns = append(columns, col)
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
