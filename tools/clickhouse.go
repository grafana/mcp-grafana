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
)

// ClickHouseQueryParams defines the parameters for querying ClickHouse
type ClickHouseQueryParams struct {
	DatasourceUID string            `json:"datasourceUid" jsonschema:"required,description=The UID of the ClickHouse datasource to query. Use list_datasources to find available UIDs."`
	Query         string            `json:"query" jsonschema:"required,description=Raw SQL query. Supports ClickHouse macros: $__timeFilter(column) for time filtering\\, $__from/$__to for millisecond timestamps\\, $__interval/$__interval_ms for calculated intervals\\, and ${varname} for variable substitution."`
	Start         string            `json:"start,omitempty" jsonschema:"description=Start time for the query. Supports RFC3339\\, relative times (now-1h\\, now-6h)\\, or Unix timestamps. Defaults to 1 hour ago."`
	End           string            `json:"end,omitempty" jsonschema:"description=End time for the query. Supports RFC3339\\, relative times (now)\\, or Unix timestamps. Defaults to now."`
	Variables     map[string]string `json:"variables,omitempty" jsonschema:"description=Template variable substitutions as key-value pairs. Variables can be referenced as ${varname} or $varname in the query."`
	Limit         int               `json:"limit,omitempty" jsonschema:"description=Maximum number of rows to return. Default: 100\\, Max: 1000. If query doesn't contain LIMIT\\, one will be appended."`
}

// ClickHouseQueryResult represents the result of a ClickHouse query
type ClickHouseQueryResult struct {
	Columns []string                 `json:"columns"`
	Rows    []map[string]interface{} `json:"rows"`
	RowCount int                     `json:"rowCount"`
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
				"format": 1, // Table format
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

// parseClickHouseTime parses time strings in various formats
// Supports: "now", "now-Xs/m/h/d/w", RFC3339, ISO dates, and Unix timestamps
func parseClickHouseTime(timeStr string) (time.Time, error) {
	if timeStr == "" {
		return time.Time{}, nil
	}

	tr := gtime.TimeRange{
		From: timeStr,
		Now:  time.Now(),
	}
	return tr.ParseFrom()
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
	timeFilterRe := regexp.MustCompile(`\$__timeFilter\((\w+)\)`)
	query = timeFilterRe.ReplaceAllStringFunc(query, func(match string) string {
		submatch := timeFilterRe.FindStringSubmatch(match)
		if len(submatch) > 1 {
			column := submatch[1]
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
	toTime := now                        // Default: now

	if args.Start != "" {
		parsed, err := parseClickHouseTime(args.Start)
		if err != nil {
			return nil, fmt.Errorf("parsing start time: %w", err)
		}
		if !parsed.IsZero() {
			fromTime = parsed
		}
	}

	if args.End != "" {
		parsed, err := parseClickHouseTime(args.End)
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
	return result, nil
}

// QueryClickHouse is a tool for querying ClickHouse datasources via Grafana
var QueryClickHouse = mcpgrafana.MustTool(
	"query_clickhouse",
	`Query ClickHouse datasource via Grafana using raw SQL. Supports ClickHouse macros and template variable substitution.

Supported macros:
- $__timeFilter(column): Expands to "column >= toDateTime(X) AND column <= toDateTime(Y)"
- $__from: Unix timestamp in milliseconds for the start time
- $__to: Unix timestamp in milliseconds for the end time
- $__interval: Calculated interval string based on time range (e.g., "60s")
- $__interval_ms: Calculated interval in milliseconds

Template variables can be referenced as ${varname} or $varname in the query.

Example query for OTel logs:
SELECT TimestampTime, ServiceName, SeverityText, Body
FROM otel_logs
WHERE $__timeFilter(TimestampTime) AND ServiceName = '${service}'
ORDER BY TimestampTime DESC`,
	queryClickHouse,
	mcp.WithTitleAnnotation("Query ClickHouse"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
)

// AddClickHouseTools registers all ClickHouse tools with the MCP server
func AddClickHouseTools(mcp *server.MCPServer) {
	QueryClickHouse.Register(mcp)
}
