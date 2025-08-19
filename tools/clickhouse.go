package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	mcpgrafana "github.com/grafana/mcp-grafana"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

const (
	// DefaultClickHouseLimit is the default number of rows to return if not specified
	DefaultClickHouseLimit = 100

	// MaxClickHouseLimit is the maximum number of rows that can be requested
	MaxClickHouseLimit = 1000
)

// ClickHouseClient represents a client for ClickHouse HTTP interface
type ClickHouseClient struct {
	httpClient *http.Client
	baseURL    string
}

// ClickHouseTable represents a ClickHouse table with metadata
type ClickHouseTable struct {
	Database               string `json:"database"`
	Name                   string `json:"name"`
	Engine                 string `json:"engine"`
	TotalRows              uint64 `json:"total_rows"`
	TotalBytes             uint64 `json:"total_bytes"`
	TotalBytesUncompressed uint64 `json:"total_bytes_uncompressed"`
	Parts                  uint64 `json:"parts"`
	ActiveParts            uint64 `json:"active_parts"`
	Comment                string `json:"comment"`
}

// ClickHouseColumn represents a ClickHouse column definition
type ClickHouseColumn struct {
	Database          string `json:"database"`
	Table             string `json:"table"`
	Name              string `json:"name"`
	Type              string `json:"type"`
	DefaultKind       string `json:"default_kind"`
	DefaultExpression string `json:"default_expression"`
	Comment           string `json:"comment"`
}

// ClickHouseQueryResult represents the result of a ClickHouse query
type ClickHouseQueryResult struct {
	Columns []string        `json:"columns"`
	Rows    [][]interface{} `json:"rows"`
	Summary struct {
		ReadRows  uint64 `json:"read_rows"`
		ReadBytes uint64 `json:"read_bytes"`
		Written   uint64 `json:"written_rows"`
	} `json:"summary,omitempty"`
}

// newClickHouseClient creates a new ClickHouse client
func newClickHouseClient(ctx context.Context, uid string) (*ClickHouseClient, error) {
	// First check if the datasource exists
	_, err := getDatasourceByUID(ctx, GetDatasourceByUIDParams{UID: uid})
	if err != nil {
		return nil, err
	}

	cfg := mcpgrafana.GrafanaConfigFromContext(ctx)
	url := fmt.Sprintf("%s/api/datasources/proxy/uid/%s", strings.TrimRight(cfg.URL, "/"), uid)

	// Create custom transport with TLS configuration if available
	var transport = http.DefaultTransport
	if tlsConfig := cfg.TLSConfig; tlsConfig != nil {
		var err error
		transport, err = tlsConfig.HTTPTransport(transport.(*http.Transport))
		if err != nil {
			return nil, fmt.Errorf("failed to create custom transport: %w", err)
		}
	}

	authTransport := &clickhouseAuthRoundTripper{
		accessToken: cfg.AccessToken,
		idToken:     cfg.IDToken,
		apiKey:      cfg.APIKey,
		underlying:  transport,
	}

	client := &http.Client{
		Transport: mcpgrafana.NewUserAgentTransport(
			authTransport,
		),
	}

	return &ClickHouseClient{
		httpClient: client,
		baseURL:    url,
	}, nil
}

// clickhouseAuthRoundTripper handles authentication for ClickHouse requests
type clickhouseAuthRoundTripper struct {
	accessToken string
	idToken     string
	apiKey      string
	underlying  http.RoundTripper
}

func (rt *clickhouseAuthRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if rt.accessToken != "" && rt.idToken != "" {
		req.Header.Set("X-Access-Token", rt.accessToken)
		req.Header.Set("X-Grafana-Id", rt.idToken)
	} else if rt.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+rt.apiKey)
	}

	resp, err := rt.underlying.RoundTrip(req)
	if err != nil {
		return nil, err
	}

	return resp, nil
}

// buildURL constructs a full URL for a ClickHouse HTTP endpoint
func (c *ClickHouseClient) buildURL(params url.Values) string {
	fullURL := c.baseURL
	if params != nil {
		fullURL += "?" + params.Encode()
	}
	return fullURL
}

// executeQuery executes a ClickHouse query and returns the response
func (c *ClickHouseClient) executeQuery(ctx context.Context, query string, format string, limit int) ([]byte, error) {
	params := url.Values{}
	params.Add("query", query)
	if format != "" {
		params.Add("format", format)
	}
	if limit > 0 {
		// ClickHouse LIMIT clause should be part of the query, but we can also use settings
		params.Add("max_result_rows", strconv.Itoa(limit))
	}

	fullURL := c.buildURL(params)

	req, err := http.NewRequestWithContext(ctx, "POST", fullURL, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("executing request: %w", err)
	}
	defer func() {
		_ = resp.Body.Close() //nolint:errcheck
	}()

	// Check for non-200 status code
	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("ClickHouse returned status code %d: %s", resp.StatusCode, string(bodyBytes))
	}

	// Read the response body with a limit to prevent memory issues
	body := io.LimitReader(resp.Body, 1024*1024*50) // 50MB limit
	bodyBytes, err := io.ReadAll(body)
	if err != nil {
		return nil, fmt.Errorf("reading response body: %w", err)
	}

	return bytes.TrimSpace(bodyBytes), nil
}

// queryJSON executes a query and returns JSON results
func (c *ClickHouseClient) queryJSON(ctx context.Context, query string, limit int) (*ClickHouseQueryResult, error) {
	bodyBytes, err := c.executeQuery(ctx, query, "JSONCompact", limit)
	if err != nil {
		return nil, err
	}

	if len(bodyBytes) == 0 {
		return &ClickHouseQueryResult{Columns: []string{}, Rows: [][]interface{}{}}, nil
	}

	var jsonResult struct {
		Meta []struct {
			Name string `json:"name"`
			Type string `json:"type"`
		} `json:"meta"`
		Data       [][]interface{} `json:"data"`
		Statistics struct {
			Elapsed   float64 `json:"elapsed"`
			RowsRead  uint64  `json:"rows_read"`
			BytesRead uint64  `json:"bytes_read"`
		} `json:"statistics,omitempty"`
	}

	err = json.Unmarshal(bodyBytes, &jsonResult)
	if err != nil {
		return nil, fmt.Errorf("unmarshalling ClickHouse response: %w", err)
	}

	// Extract column names
	columns := make([]string, len(jsonResult.Meta))
	for i, col := range jsonResult.Meta {
		columns[i] = col.Name
	}

	result := &ClickHouseQueryResult{
		Columns: columns,
		Rows:    jsonResult.Data,
	}

	if jsonResult.Statistics.RowsRead > 0 {
		result.Summary.ReadRows = jsonResult.Statistics.RowsRead
		result.Summary.ReadBytes = jsonResult.Statistics.BytesRead
	}

	return result, nil
}

// queryPlainText executes a query and returns plain text results
func (c *ClickHouseClient) queryPlainText(ctx context.Context, query string) ([]string, error) {
	bodyBytes, err := c.executeQuery(ctx, query, "TSV", 0)
	if err != nil {
		return nil, err
	}

	if len(bodyBytes) == 0 {
		return []string{}, nil
	}

	lines := strings.Split(string(bodyBytes), "\n")
	// Remove empty lines
	var result []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			result = append(result, line)
		}
	}

	return result, nil
}

// QueryClickHouseParams defines the parameters for querying ClickHouse
type QueryClickHouseParams struct {
	DatasourceUID string `json:"datasourceUid" jsonschema:"required,description=The UID of the datasource to query"`
	Query         string `json:"query" jsonschema:"required,description=The SQL query to execute against ClickHouse. Must be a SELECT query for safety."`
	Limit         int    `json:"limit,omitempty" jsonschema:"description=Optionally\\, the maximum number of rows to return (default: 100\\, max: 1000)"`
}

// enforceLimit ensures a limit value is within acceptable bounds
func enforceClickHouseLimit(requestedLimit int) int {
	if requestedLimit <= 0 {
		return DefaultClickHouseLimit
	}
	if requestedLimit > MaxClickHouseLimit {
		return MaxClickHouseLimit
	}
	return requestedLimit
}

// queryClickHouse executes a SQL query against ClickHouse
func queryClickHouse(ctx context.Context, args QueryClickHouseParams) (*ClickHouseQueryResult, error) {
	client, err := newClickHouseClient(ctx, args.DatasourceUID)
	if err != nil {
		return nil, fmt.Errorf("creating ClickHouse client: %w", err)
	}

	// Basic validation - ensure it's a SELECT query
	trimmedQuery := strings.TrimSpace(strings.ToUpper(args.Query))
	if !strings.HasPrefix(trimmedQuery, "SELECT") && !strings.HasPrefix(trimmedQuery, "SHOW") && !strings.HasPrefix(trimmedQuery, "DESCRIBE") {
		return nil, fmt.Errorf("only SELECT, SHOW, and DESCRIBE queries are allowed for safety")
	}

	// Apply limit constraints
	limit := enforceClickHouseLimit(args.Limit)

	result, err := client.queryJSON(ctx, args.Query, limit)
	if err != nil {
		return nil, err
	}

	return result, nil
}

// QueryClickHouse is a tool for querying ClickHouse
var QueryClickHouse = mcpgrafana.MustTool(
	"query_clickhouse",
	"Executes a SQL query against a ClickHouse datasource. Only SELECT, SHOW, and DESCRIBE queries are allowed for safety. Returns column names, data rows, and query statistics. Supports a configurable row limit (default: 100, max: 1000).",
	queryClickHouse,
	mcp.WithTitleAnnotation("Query ClickHouse"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
)

// ListClickHouseDatabasesParams defines the parameters for listing ClickHouse databases
type ListClickHouseDatabasesParams struct {
	DatasourceUID string `json:"datasourceUid" jsonschema:"required,description=The UID of the datasource to query"`
}

// listClickHouseDatabases lists all databases in a ClickHouse datasource
func listClickHouseDatabases(ctx context.Context, args ListClickHouseDatabasesParams) ([]string, error) {
	client, err := newClickHouseClient(ctx, args.DatasourceUID)
	if err != nil {
		return nil, fmt.Errorf("creating ClickHouse client: %w", err)
	}

	databases, err := client.queryPlainText(ctx, "SHOW DATABASES")
	if err != nil {
		return nil, err
	}

	return databases, nil
}

// ListClickHouseDatabases is a tool for listing ClickHouse databases
var ListClickHouseDatabases = mcpgrafana.MustTool(
	"list_clickhouse_databases",
	"Lists all available databases in a ClickHouse datasource. Returns a list of database names that can be used for further table exploration.",
	listClickHouseDatabases,
	mcp.WithTitleAnnotation("List ClickHouse databases"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
)

// ListClickHouseTablesParams defines the parameters for listing ClickHouse tables
type ListClickHouseTablesParams struct {
	DatasourceUID string `json:"datasourceUid" jsonschema:"required,description=The UID of the datasource to query"`
	Database      string `json:"database" jsonschema:"required,description=The name of the database to list tables from"`
	Like          string `json:"like,omitempty" jsonschema:"description=Optionally\\, filter table names using LIKE pattern (e.g. 'log_%' for tables starting with 'log_')"`
	Limit         int    `json:"limit,omitempty" jsonschema:"description=Optionally\\, the maximum number of tables to return (default: 100)"`
}

// listClickHouseTables lists all tables in a ClickHouse database with metadata
func listClickHouseTables(ctx context.Context, args ListClickHouseTablesParams) ([]ClickHouseTable, error) {
	client, err := newClickHouseClient(ctx, args.DatasourceUID)
	if err != nil {
		return nil, fmt.Errorf("creating ClickHouse client: %w", err)
	}

	// Build the query to get table information from system.tables
	query := fmt.Sprintf(`SELECT 
		database,
		name,
		engine,
		total_rows,
		total_bytes,
		total_bytes_uncompressed,
		parts,
		active_parts,
		comment
	FROM system.tables 
	WHERE database = '%s'`, strings.ReplaceAll(args.Database, "'", "''"))

	if args.Like != "" {
		query += fmt.Sprintf(" AND name LIKE '%s'", strings.ReplaceAll(args.Like, "'", "''"))
	}

	query += " ORDER BY name"

	if args.Limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", args.Limit)
	} else {
		query += " LIMIT 100"
	}

	result, err := client.queryJSON(ctx, query, 0)
	if err != nil {
		return nil, err
	}

	var tables []ClickHouseTable
	for _, row := range result.Rows {
		if len(row) >= 9 {
			table := ClickHouseTable{
				Database:               toString(row[0]),
				Name:                   toString(row[1]),
				Engine:                 toString(row[2]),
				TotalRows:              toUint64(row[3]),
				TotalBytes:             toUint64(row[4]),
				TotalBytesUncompressed: toUint64(row[5]),
				Parts:                  toUint64(row[6]),
				ActiveParts:            toUint64(row[7]),
				Comment:                toString(row[8]),
			}
			tables = append(tables, table)
		}
	}

	return tables, nil
}

// Helper functions for type conversion
func toString(val interface{}) string {
	if val == nil {
		return ""
	}
	if str, ok := val.(string); ok {
		return str
	}
	return fmt.Sprintf("%v", val)
}

func toUint64(val interface{}) uint64 {
	if val == nil {
		return 0
	}

	switch v := val.(type) {
	case float64:
		return uint64(v)
	case int:
		return uint64(v)
	case int64:
		return uint64(v)
	case uint64:
		return v
	case string:
		if num, err := strconv.ParseUint(v, 10, 64); err == nil {
			return num
		}
	}
	return 0
}

// ListClickHouseTables is a tool for listing ClickHouse tables
var ListClickHouseTables = mcpgrafana.MustTool(
	"list_clickhouse_tables",
	"Lists all tables in a specified ClickHouse database with detailed metadata including table engine, row count, size information, and comments. Supports filtering with LIKE patterns and result limiting.",
	listClickHouseTables,
	mcp.WithTitleAnnotation("List ClickHouse tables"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
)

// DescribeClickHouseTableParams defines the parameters for describing a ClickHouse table
type DescribeClickHouseTableParams struct {
	DatasourceUID string `json:"datasourceUid" jsonschema:"required,description=The UID of the datasource to query"`
	Database      string `json:"database" jsonschema:"required,description=The name of the database"`
	Table         string `json:"table" jsonschema:"required,description=The name of the table to describe"`
}

// describeClickHouseTable describes the structure of a ClickHouse table
func describeClickHouseTable(ctx context.Context, args DescribeClickHouseTableParams) ([]ClickHouseColumn, error) {
	client, err := newClickHouseClient(ctx, args.DatasourceUID)
	if err != nil {
		return nil, fmt.Errorf("creating ClickHouse client: %w", err)
	}

	// Get column information from system.columns
	query := fmt.Sprintf(`SELECT 
		database,
		table,
		name,
		type,
		default_kind,
		default_expression,
		comment
	FROM system.columns 
	WHERE database = '%s' AND table = '%s'
	ORDER BY position`,
		strings.ReplaceAll(args.Database, "'", "''"),
		strings.ReplaceAll(args.Table, "'", "''"))

	result, err := client.queryJSON(ctx, query, 0)
	if err != nil {
		return nil, err
	}

	var columns []ClickHouseColumn
	for _, row := range result.Rows {
		if len(row) >= 7 {
			column := ClickHouseColumn{
				Database:          toString(row[0]),
				Table:             toString(row[1]),
				Name:              toString(row[2]),
				Type:              toString(row[3]),
				DefaultKind:       toString(row[4]),
				DefaultExpression: toString(row[5]),
				Comment:           toString(row[6]),
			}
			columns = append(columns, column)
		}
	}

	return columns, nil
}

// DescribeClickHouseTable is a tool for describing ClickHouse table structure
var DescribeClickHouseTable = mcpgrafana.MustTool(
	"describe_clickhouse_table",
	"Describes the structure of a ClickHouse table, returning detailed information about each column including name, data type, default values, and comments. Useful for understanding table schema before writing queries.",
	describeClickHouseTable,
	mcp.WithTitleAnnotation("Describe ClickHouse table"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
)

// AddClickHouseTools registers all ClickHouse tools with the MCP server
func AddClickHouseTools(mcp *server.MCPServer) {
	QueryClickHouse.Register(mcp)
	ListClickHouseDatabases.Register(mcp)
	ListClickHouseTables.Register(mcp)
	DescribeClickHouseTable.Register(mcp)
}
