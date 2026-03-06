package tools

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	mcpgrafana "github.com/grafana/mcp-grafana"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

const (
	// DefaultVictoriaLogsLimit is the default number of log lines to return if not specified
	DefaultVictoriaLogsLimit = 100

	// MaxVictoriaLogsLimit is the maximum number of log lines that can be requested
	MaxVictoriaLogsLimit = 1000
)

// victoriaLogsClient is an HTTP client for VictoriaLogs API endpoints.
type victoriaLogsClient struct {
	httpClient *http.Client
	baseURL    string
}

func newVictoriaLogsClient(ctx context.Context, uid string) (*victoriaLogsClient, error) {
	// First check if the datasource exists
	_, err := getDatasourceByUID(ctx, GetDatasourceByUIDParams{UID: uid})
	if err != nil {
		return nil, err
	}

	cfg := mcpgrafana.GrafanaConfigFromContext(ctx)
	u := fmt.Sprintf("%s/api/datasources/proxy/uid/%s", strings.TrimRight(cfg.URL, "/"), uid)

	// Create custom transport with TLS configuration if available
	transport, err := mcpgrafana.BuildTransport(&cfg, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create custom transport: %w", err)
	}
	transport = NewAuthRoundTripper(transport, cfg.AccessToken, cfg.IDToken, cfg.APIKey, cfg.BasicAuth)
	transport = mcpgrafana.NewOrgIDRoundTripper(transport, cfg.OrgID)

	client := &http.Client{
		Transport: mcpgrafana.NewUserAgentTransport(transport),
	}

	return &victoriaLogsClient{
		httpClient: client,
		baseURL:    u,
	}, nil
}

// buildURL constructs a full URL for a VictoriaLogs API endpoint.
func (c *victoriaLogsClient) buildURL(urlPath string) string {
	fullURL := c.baseURL
	if !strings.HasSuffix(fullURL, "/") && !strings.HasPrefix(urlPath, "/") {
		fullURL += "/"
	} else if strings.HasSuffix(fullURL, "/") && strings.HasPrefix(urlPath, "/") {
		urlPath = strings.TrimPrefix(urlPath, "/")
	}
	return fullURL + urlPath
}

// makeGetRequest makes a GET request to the VictoriaLogs API and returns the response body.
func (c *victoriaLogsClient) makeGetRequest(ctx context.Context, urlPath string, params url.Values) ([]byte, error) {
	fullURL := c.buildURL(urlPath)

	u, err := url.Parse(fullURL)
	if err != nil {
		return nil, fmt.Errorf("parsing URL: %w", err)
	}

	if params != nil {
		u.RawQuery = params.Encode()
	}

	req, err := http.NewRequestWithContext(ctx, "GET", u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("executing request: %w", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("VictoriaLogs API returned status code %d: %s", resp.StatusCode, string(bodyBytes))
	}

	body := io.LimitReader(resp.Body, 1024*1024*48)
	bodyBytes, err := io.ReadAll(body)
	if err != nil {
		return nil, fmt.Errorf("reading response body: %w", err)
	}

	return bytes.TrimSpace(bodyBytes), nil
}

// makePostRequest makes a POST request with form-encoded body to the VictoriaLogs API.
func (c *victoriaLogsClient) makePostRequest(ctx context.Context, urlPath string, params url.Values) ([]byte, error) {
	fullURL := c.buildURL(urlPath)

	req, err := http.NewRequestWithContext(ctx, "POST", fullURL, strings.NewReader(params.Encode()))
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("executing request: %w", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("VictoriaLogs API returned status code %d: %s", resp.StatusCode, string(bodyBytes))
	}

	body := io.LimitReader(resp.Body, 1024*1024*48)
	bodyBytes, err := io.ReadAll(body)
	if err != nil {
		return nil, fmt.Errorf("reading response body: %w", err)
	}

	return bytes.TrimSpace(bodyBytes), nil
}

// parseNDJSON parses newline-delimited JSON into a slice of json.RawMessage.
func parseNDJSON(data []byte) ([]json.RawMessage, error) {
	var results []json.RawMessage
	scanner := bufio.NewScanner(bytes.NewReader(data))
	// Increase the scanner buffer size for large lines
	scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024*48)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		// Make a copy since scanner reuses its buffer
		lineCopy := make([]byte, len(line))
		copy(lineCopy, line)
		results = append(results, json.RawMessage(lineCopy))
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scanning NDJSON: %w", err)
	}
	return results, nil
}

// victoriaLogsDefaultTimeRange returns default start and end times if not provided.
func victoriaLogsDefaultTimeRange(startRFC3339, endRFC3339 string) (string, string) {
	if startRFC3339 == "" {
		startRFC3339 = time.Now().Add(-1 * time.Hour).Format(time.RFC3339)
	}
	if endRFC3339 == "" {
		endRFC3339 = time.Now().Format(time.RFC3339)
	}
	return startRFC3339, endRFC3339
}

// VictoriaLogsLogEntry represents a single log entry returned by VictoriaLogs.
type VictoriaLogsLogEntry struct {
	Fields map[string]string `json:"fields"`
}

// QueryVictoriaLogsParams defines the parameters for querying VictoriaLogs.
type QueryVictoriaLogsParams struct {
	DatasourceUID string `json:"datasourceUid" jsonschema:"required,description=The UID of the VictoriaLogs datasource to query"`
	Query         string `json:"query" jsonschema:"required,description=The LogsQL query to execute against VictoriaLogs. Supports full LogsQL syntax including stream selectors\\, word filters\\, and pipe operations."`
	StartRFC3339  string `json:"startRfc3339,omitempty" jsonschema:"description=Optionally\\, the start time of the query in RFC3339 format (defaults to 1 hour ago)"`
	EndRFC3339    string `json:"endRfc3339,omitempty" jsonschema:"description=Optionally\\, the end time of the query in RFC3339 format (defaults to now)"`
	Limit         int    `json:"limit,omitempty" jsonschema:"default=100,description=Optionally\\, the maximum number of log entries to return (max: 1000). Applied as a LogsQL pipe (| limit N)."`
}

// QueryVictoriaLogsResult wraps the VictoriaLogs query result.
type QueryVictoriaLogsResult struct {
	Data  []map[string]string `json:"data"`
	Hints *EmptyResultHints   `json:"hints,omitempty"`
}

// queryVictoriaLogs executes a LogsQL query against a VictoriaLogs datasource.
func queryVictoriaLogs(ctx context.Context, args QueryVictoriaLogsParams) (*QueryVictoriaLogsResult, error) {
	client, err := newVictoriaLogsClient(ctx, args.DatasourceUID)
	if err != nil {
		return nil, fmt.Errorf("creating VictoriaLogs client: %w", err)
	}

	startTime, endTime := victoriaLogsDefaultTimeRange(args.StartRFC3339, args.EndRFC3339)

	// Apply limit constraints
	limit := args.Limit
	if limit <= 0 {
		limit = DefaultVictoriaLogsLimit
	}
	if limit > MaxVictoriaLogsLimit {
		limit = MaxVictoriaLogsLimit
	}

	// Build the query with limit pipe
	query := args.Query
	query = fmt.Sprintf("%s | limit %d", query, limit)

	params := url.Values{}
	params.Set("query", query)
	params.Set("start", startTime)
	params.Set("end", endTime)

	bodyBytes, err := client.makePostRequest(ctx, "/select/logsql/query", params)
	if err != nil {
		return nil, err
	}

	// Parse NDJSON response
	var entries []map[string]string
	if len(bodyBytes) > 0 {
		lines, err := parseNDJSON(bodyBytes)
		if err != nil {
			return nil, fmt.Errorf("parsing NDJSON response: %w", err)
		}

		for _, line := range lines {
			var entry map[string]string
			if err := json.Unmarshal(line, &entry); err != nil {
				// Try as map[string]interface{} and convert
				var rawEntry map[string]interface{}
				if err2 := json.Unmarshal(line, &rawEntry); err2 != nil {
					continue
				}
				entry = make(map[string]string)
				for k, v := range rawEntry {
					entry[k] = fmt.Sprintf("%v", v)
				}
			}
			entries = append(entries, entry)
		}
	}

	if entries == nil {
		entries = []map[string]string{}
	}

	result := &QueryVictoriaLogsResult{
		Data: entries,
	}

	if len(entries) == 0 {
		var parsedStartTime, parsedEndTime time.Time
		if startTime != "" {
			parsedStartTime, _ = time.Parse(time.RFC3339, startTime)
		}
		if endTime != "" {
			parsedEndTime, _ = time.Parse(time.RFC3339, endTime)
		}

		result.Hints = GenerateEmptyResultHints(HintContext{
			DatasourceType: "victorialogs",
			Query:          args.Query,
			StartTime:      parsedStartTime,
			EndTime:        parsedEndTime,
		})
	}

	return result, nil
}

// QueryVictoriaLogs is a tool for querying logs from VictoriaLogs.
var QueryVictoriaLogs = mcpgrafana.MustTool(
	"query_victorialogs",
	"Executes a LogsQL query against a VictoriaLogs datasource to retrieve log entries. Returns a list of log entries\\, each as a map of field names to values. Defaults to the last hour and a limit of 100 entries. Supports full LogsQL syntax including stream selectors\\, word filters\\, and pipe operations (e.g.\\, `_stream:{app=\"nginx\"} error | limit 10`). Use `list_victorialogs_field_names` and `list_victorialogs_field_values` to discover available fields before querying.",
	queryVictoriaLogs,
	mcp.WithTitleAnnotation("Query VictoriaLogs"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
)

// VictoriaLogsFieldName represents a field name entry from VictoriaLogs.
type VictoriaLogsFieldName struct {
	Field string `json:"field"`
}

// ListVictoriaLogsFieldNamesParams defines the parameters for listing field names.
type ListVictoriaLogsFieldNamesParams struct {
	DatasourceUID string `json:"datasourceUid" jsonschema:"required,description=The UID of the VictoriaLogs datasource to query"`
	Query         string `json:"query,omitempty" jsonschema:"description=Optionally\\, a LogsQL query to filter which logs to extract field names from"`
	StartRFC3339  string `json:"startRfc3339,omitempty" jsonschema:"description=Optionally\\, the start time of the query in RFC3339 format (defaults to 1 hour ago)"`
	EndRFC3339    string `json:"endRfc3339,omitempty" jsonschema:"description=Optionally\\, the end time of the query in RFC3339 format (defaults to now)"`
}

// listVictoriaLogsFieldNames lists all available field names in a VictoriaLogs datasource.
func listVictoriaLogsFieldNames(ctx context.Context, args ListVictoriaLogsFieldNamesParams) ([]string, error) {
	client, err := newVictoriaLogsClient(ctx, args.DatasourceUID)
	if err != nil {
		return nil, fmt.Errorf("creating VictoriaLogs client: %w", err)
	}

	startTime, endTime := victoriaLogsDefaultTimeRange(args.StartRFC3339, args.EndRFC3339)

	params := url.Values{}
	if args.Query != "" {
		params.Set("query", args.Query)
	}
	params.Set("start", startTime)
	params.Set("end", endTime)

	bodyBytes, err := client.makeGetRequest(ctx, "/select/logsql/field_names", params)
	if err != nil {
		return nil, err
	}

	var fieldNames []string
	if len(bodyBytes) > 0 {
		lines, err := parseNDJSON(bodyBytes)
		if err != nil {
			return nil, fmt.Errorf("parsing NDJSON response: %w", err)
		}

		for _, line := range lines {
			var entry VictoriaLogsFieldName
			if err := json.Unmarshal(line, &entry); err != nil {
				continue
			}
			if entry.Field != "" {
				fieldNames = append(fieldNames, entry.Field)
			}
		}
	}

	if fieldNames == nil {
		fieldNames = []string{}
	}

	return fieldNames, nil
}

// ListVictoriaLogsFieldNames is a tool for listing field names in VictoriaLogs.
var ListVictoriaLogsFieldNames = mcpgrafana.MustTool(
	"list_victorialogs_field_names",
	"Lists all available field names found in logs within a VictoriaLogs datasource and time range. Returns a list of unique field name strings. Optionally filter by a LogsQL query to see fields from specific log streams. Defaults to the last hour if the time range is not provided.",
	listVictoriaLogsFieldNames,
	mcp.WithTitleAnnotation("List VictoriaLogs field names"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
)

// VictoriaLogsFieldValue represents a field value entry with hit count.
type VictoriaLogsFieldValue struct {
	Value string `json:"value"`
	Hits  int64  `json:"hits"`
}

// ListVictoriaLogsFieldValuesParams defines the parameters for listing field values.
type ListVictoriaLogsFieldValuesParams struct {
	DatasourceUID string `json:"datasourceUid" jsonschema:"required,description=The UID of the VictoriaLogs datasource to query"`
	FieldName     string `json:"fieldName" jsonschema:"required,description=The name of the field to retrieve values for (e.g. 'host'\\, 'level'\\, 'service')"`
	Query         string `json:"query,omitempty" jsonschema:"description=Optionally\\, a LogsQL query to filter which logs to extract field values from"`
	StartRFC3339  string `json:"startRfc3339,omitempty" jsonschema:"description=Optionally\\, the start time of the query in RFC3339 format (defaults to 1 hour ago)"`
	EndRFC3339    string `json:"endRfc3339,omitempty" jsonschema:"description=Optionally\\, the end time of the query in RFC3339 format (defaults to now)"`
	Limit         int    `json:"limit,omitempty" jsonschema:"description=Optionally\\, the maximum number of field values to return"`
}

// listVictoriaLogsFieldValues lists all values for a specific field in a VictoriaLogs datasource.
func listVictoriaLogsFieldValues(ctx context.Context, args ListVictoriaLogsFieldValuesParams) ([]VictoriaLogsFieldValue, error) {
	client, err := newVictoriaLogsClient(ctx, args.DatasourceUID)
	if err != nil {
		return nil, fmt.Errorf("creating VictoriaLogs client: %w", err)
	}

	startTime, endTime := victoriaLogsDefaultTimeRange(args.StartRFC3339, args.EndRFC3339)

	params := url.Values{}
	params.Set("field", args.FieldName)
	if args.Query != "" {
		params.Set("query", args.Query)
	}
	params.Set("start", startTime)
	params.Set("end", endTime)
	if args.Limit > 0 {
		params.Set("limit", fmt.Sprintf("%d", args.Limit))
	}

	bodyBytes, err := client.makeGetRequest(ctx, "/select/logsql/field_values", params)
	if err != nil {
		return nil, err
	}

	var fieldValues []VictoriaLogsFieldValue
	if len(bodyBytes) > 0 {
		lines, err := parseNDJSON(bodyBytes)
		if err != nil {
			return nil, fmt.Errorf("parsing NDJSON response: %w", err)
		}

		for _, line := range lines {
			var entry VictoriaLogsFieldValue
			if err := json.Unmarshal(line, &entry); err != nil {
				continue
			}
			fieldValues = append(fieldValues, entry)
		}
	}

	if fieldValues == nil {
		fieldValues = []VictoriaLogsFieldValue{}
	}

	return fieldValues, nil
}

// ListVictoriaLogsFieldValues is a tool for listing field values in VictoriaLogs.
var ListVictoriaLogsFieldValues = mcpgrafana.MustTool(
	"list_victorialogs_field_values",
	"Retrieves all unique values for a specific field within a VictoriaLogs datasource and time range. Returns a list of objects\\, each containing the value and its hit count. Useful for discovering filter options and understanding data distribution. Defaults to the last hour if the time range is not provided.",
	listVictoriaLogsFieldValues,
	mcp.WithTitleAnnotation("List VictoriaLogs field values"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
)

// VictoriaLogsHit represents a hit count for a time bucket.
type VictoriaLogsHit struct {
	Fields map[string]string `json:"fields"`
	Hits   int64             `json:"hits"`
}

// QueryVictoriaLogsHitsParams defines the parameters for querying log volume hits.
type QueryVictoriaLogsHitsParams struct {
	DatasourceUID string `json:"datasourceUid" jsonschema:"required,description=The UID of the VictoriaLogs datasource to query"`
	Query         string `json:"query" jsonschema:"required,description=The LogsQL query to count hits for"`
	StartRFC3339  string `json:"startRfc3339,omitempty" jsonschema:"description=Optionally\\, the start time of the query in RFC3339 format (defaults to 1 hour ago)"`
	EndRFC3339    string `json:"endRfc3339,omitempty" jsonschema:"description=Optionally\\, the end time of the query in RFC3339 format (defaults to now)"`
	Step          string `json:"step,omitempty" jsonschema:"description=Optionally\\, the time bucket duration for aggregation (e.g. '5m'\\, '1h'). Defaults to automatic."`
}

// QueryVictoriaLogsHitsResult wraps the hits query result.
type QueryVictoriaLogsHitsResult struct {
	Data []VictoriaLogsHit `json:"data"`
}

// queryVictoriaLogsHits queries log volume over time from VictoriaLogs.
func queryVictoriaLogsHits(ctx context.Context, args QueryVictoriaLogsHitsParams) (*QueryVictoriaLogsHitsResult, error) {
	client, err := newVictoriaLogsClient(ctx, args.DatasourceUID)
	if err != nil {
		return nil, fmt.Errorf("creating VictoriaLogs client: %w", err)
	}

	startTime, endTime := victoriaLogsDefaultTimeRange(args.StartRFC3339, args.EndRFC3339)

	params := url.Values{}
	params.Set("query", args.Query)
	params.Set("start", startTime)
	params.Set("end", endTime)
	if args.Step != "" {
		params.Set("step", args.Step)
	}

	bodyBytes, err := client.makeGetRequest(ctx, "/select/logsql/hits", params)
	if err != nil {
		return nil, err
	}

	var hits []VictoriaLogsHit
	if len(bodyBytes) > 0 {
		lines, err := parseNDJSON(bodyBytes)
		if err != nil {
			return nil, fmt.Errorf("parsing NDJSON response: %w", err)
		}

		for _, line := range lines {
			var entry VictoriaLogsHit
			if err := json.Unmarshal(line, &entry); err != nil {
				continue
			}
			hits = append(hits, entry)
		}
	}

	if hits == nil {
		hits = []VictoriaLogsHit{}
	}

	return &QueryVictoriaLogsHitsResult{Data: hits}, nil
}

// QueryVictoriaLogsHits is a tool for querying log volume from VictoriaLogs.
var QueryVictoriaLogsHits = mcpgrafana.MustTool(
	"query_victorialogs_hits",
	"Queries log volume over time from a VictoriaLogs datasource. Returns hit counts bucketed by time intervals\\, useful for understanding log volume trends and identifying spikes. The step parameter controls the bucket size. Defaults to the last hour if the time range is not provided.",
	queryVictoriaLogsHits,
	mcp.WithTitleAnnotation("Query VictoriaLogs hits"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
)

// VictoriaLogsStream represents a log stream with its hit count.
type VictoriaLogsStream struct {
	Value string `json:"value"`
	Hits  int64  `json:"hits"`
}

// QueryVictoriaLogsStreamsParams defines the parameters for querying log streams.
type QueryVictoriaLogsStreamsParams struct {
	DatasourceUID string `json:"datasourceUid" jsonschema:"required,description=The UID of the VictoriaLogs datasource to query"`
	Query         string `json:"query" jsonschema:"required,description=The LogsQL query to find matching streams for"`
	StartRFC3339  string `json:"startRfc3339,omitempty" jsonschema:"description=Optionally\\, the start time of the query in RFC3339 format (defaults to 1 hour ago)"`
	EndRFC3339    string `json:"endRfc3339,omitempty" jsonschema:"description=Optionally\\, the end time of the query in RFC3339 format (defaults to now)"`
	Limit         int    `json:"limit,omitempty" jsonschema:"description=Optionally\\, the maximum number of streams to return"`
}

// QueryVictoriaLogsStreamsResult wraps the streams query result.
type QueryVictoriaLogsStreamsResult struct {
	Data []VictoriaLogsStream `json:"data"`
}

// queryVictoriaLogsStreams queries matching log streams from VictoriaLogs.
func queryVictoriaLogsStreams(ctx context.Context, args QueryVictoriaLogsStreamsParams) (*QueryVictoriaLogsStreamsResult, error) {
	client, err := newVictoriaLogsClient(ctx, args.DatasourceUID)
	if err != nil {
		return nil, fmt.Errorf("creating VictoriaLogs client: %w", err)
	}

	startTime, endTime := victoriaLogsDefaultTimeRange(args.StartRFC3339, args.EndRFC3339)

	params := url.Values{}
	params.Set("query", args.Query)
	params.Set("start", startTime)
	params.Set("end", endTime)
	if args.Limit > 0 {
		params.Set("limit", fmt.Sprintf("%d", args.Limit))
	}

	bodyBytes, err := client.makeGetRequest(ctx, "/select/logsql/streams", params)
	if err != nil {
		return nil, err
	}

	var streams []VictoriaLogsStream
	if len(bodyBytes) > 0 {
		lines, err := parseNDJSON(bodyBytes)
		if err != nil {
			return nil, fmt.Errorf("parsing NDJSON response: %w", err)
		}

		for _, line := range lines {
			var entry VictoriaLogsStream
			if err := json.Unmarshal(line, &entry); err != nil {
				continue
			}
			streams = append(streams, entry)
		}
	}

	if streams == nil {
		streams = []VictoriaLogsStream{}
	}

	return &QueryVictoriaLogsStreamsResult{Data: streams}, nil
}

// QueryVictoriaLogsStreams is a tool for querying log streams from VictoriaLogs.
var QueryVictoriaLogsStreams = mcpgrafana.MustTool(
	"query_victorialogs_streams",
	"Lists log streams matching a LogsQL query from a VictoriaLogs datasource. Returns stream identifiers with their hit counts\\, useful for discovering available log streams and understanding their relative volume. Defaults to the last hour if the time range is not provided.",
	queryVictoriaLogsStreams,
	mcp.WithTitleAnnotation("Query VictoriaLogs streams"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
)

// AddVictoriaLogsTools registers all VictoriaLogs tools with the MCP server.
func AddVictoriaLogsTools(s *server.MCPServer) {
	QueryVictoriaLogs.Register(s)
	ListVictoriaLogsFieldNames.Register(s)
	ListVictoriaLogsFieldValues.Register(s)
	QueryVictoriaLogsHits.Register(s)
	QueryVictoriaLogsStreams.Register(s)
}
