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
	"time"

	"github.com/grafana/grafana-openapi-client-go/models"
	mcpgrafana "github.com/grafana/mcp-grafana"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

const (
	// DefaultLokiLogLimit is the default number of log lines to return if not specified
	DefaultLokiLogLimit = 10

	// MaxLokiLogLimit is the maximum number of log lines that can be requested
	MaxLokiLogLimit = 100
)

type Client struct {
	httpClient *http.Client
	baseURL    string
}

// LabelResponse represents the http json response to a label query
type LabelResponse struct {
	Status string   `json:"status"`
	Data   []string `json:"data,omitempty"`
}

// Stats represents the statistics returned by Loki's index/stats endpoint
type Stats struct {
	Streams int `json:"streams"`
	Chunks  int `json:"chunks"`
	Entries int `json:"entries"`
	Bytes   int `json:"bytes"`
}

// patternsAPIResponse represents the raw response from Loki's patterns API
type patternsAPIResponse struct {
	Status string `json:"status"`
	Data   []struct {
		Pattern string     `json:"pattern"`
		Samples [][2]int64 `json:"samples"` // [[timestamp, value], ...]
	} `json:"data"`
}

// Pattern represents a detected log pattern with summarized count
type Pattern struct {
	Pattern    string `json:"pattern"`
	TotalCount int64  `json:"totalCount"`
}

// newLokiClient builds the HTTP client used by the native Loki backend.
// Callers are expected to have already resolved the datasource (so the
// existence check happens in lokiBackendForDatasource and isn't repeated
// here); the ds argument is currently unused but kept to mirror the
// prom_backend constructor signature and to leave room for per-datasource
// configuration (e.g. JSONData) without churning the call sites again.
func newLokiClient(ctx context.Context, uid string, _ *models.DataSource) (*Client, error) {
	cfg := mcpgrafana.GrafanaConfigFromContext(ctx)
	grafanaURL := cfg.URL
	resourcesBase, proxyBase := datasourceProxyPaths(uid)
	url := grafanaURL + proxyBase

	transport, err := mcpgrafana.BuildTransport(&cfg, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create custom transport: %w", err)
	}

	// Wrap with fallback transport: try /proxy first, fall back to /resources
	// on 403/500 for compatibility with different managed Grafana deployments.
	rt := newDatasourceFallbackTransport(transport, proxyBase, resourcesBase)

	client := &http.Client{
		Transport: rt,
	}

	return &Client{
		httpClient: client,
		baseURL:    url,
	}, nil
}

// buildURL constructs a full URL for a Loki API endpoint
func (c *Client) buildURL(urlPath string) string {
	fullURL := c.baseURL
	if !strings.HasSuffix(fullURL, "/") && !strings.HasPrefix(urlPath, "/") {
		fullURL += "/"
	} else if strings.HasSuffix(fullURL, "/") && strings.HasPrefix(urlPath, "/") {
		// Remove the leading slash from urlPath to avoid double slash
		urlPath = strings.TrimPrefix(urlPath, "/")
	}
	return fullURL + urlPath
}

// makeRequest makes an HTTP request to the Loki API and returns the response body
func (c *Client) makeRequest(ctx context.Context, method, urlPath string, params url.Values) ([]byte, error) {
	fullURL := c.buildURL(urlPath)

	u, err := url.Parse(fullURL)
	if err != nil {
		return nil, fmt.Errorf("parsing URL: %w", err)
	}

	if params != nil {
		u.RawQuery = params.Encode()
	}

	req, err := http.NewRequestWithContext(ctx, method, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}

	// Request categorized labels so Loki returns structured metadata and
	// parsed labels separately from stream/index labels (Loki >= 3.0).
	req.Header.Set("X-Loki-Response-Encoding-Flags", "categorize-labels")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("executing request: %w", err)
	}
	defer func() {
		_ = resp.Body.Close() //nolint:errcheck
	}()

	// Check for non-200 status code
	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("loki API returned status code %d: %s", resp.StatusCode, string(bodyBytes))
	}

	// Read the response body with a limit to prevent memory issues
	bodyBytes, err := readResponseBody(resp.Body, defaultResponseLimitBytes)
	if err != nil {
		return nil, fmt.Errorf("reading response body: %w", err)
	}

	// Check if the response is empty
	if len(bodyBytes) == 0 {
		return nil, fmt.Errorf("empty response from Loki API")
	}

	// Trim any whitespace that might cause JSON parsing issues
	return bytes.TrimSpace(bodyBytes), nil
}

// fetchData is a generic method to fetch data from Loki API
func (c *Client) fetchData(ctx context.Context, urlPath string, startRFC3339, endRFC3339 string) ([]string, error) {
	params := url.Values{}
	if startRFC3339 != "" {
		params.Add("start", startRFC3339)
	}
	if endRFC3339 != "" {
		params.Add("end", endRFC3339)
	}

	bodyBytes, err := c.makeRequest(ctx, "GET", urlPath, params)
	if err != nil {
		return nil, err
	}

	var labelResponse LabelResponse
	err = json.Unmarshal(bodyBytes, &labelResponse)
	if err != nil {
		return nil, fmt.Errorf("unmarshalling response (content: %s): %w", string(bodyBytes), err)
	}

	if labelResponse.Status != "success" {
		return nil, fmt.Errorf("loki API returned unexpected response format: %s", string(bodyBytes))
	}

	// Check if Data is nil or empty and handle it explicitly
	if labelResponse.Data == nil {
		// Return empty slice instead of nil to avoid potential nil pointer issues
		return []string{}, nil
	}

	if len(labelResponse.Data) == 0 {
		return []string{}, nil
	}

	return labelResponse.Data, nil
}

// ListLokiLabelNamesParams defines the parameters for listing Loki label names
type ListLokiLabelNamesParams struct {
	DatasourceUID string `json:"datasourceUid" jsonschema:"required,description=The UID of the datasource to query"`
	StartRFC3339  string `json:"startRfc3339,omitempty" jsonschema:"description=Optionally\\, the start time of the query in RFC3339 format (defaults to 1 hour ago)"`
	EndRFC3339    string `json:"endRfc3339,omitempty" jsonschema:"description=Optionally\\, the end time of the query in RFC3339 format (defaults to now)"`
}

// parseRFC3339OrZero converts an RFC3339 string to a time.Time, returning a
// zero time when the input is empty. Unparseable strings are surfaced as
// errors so callers don't silently send garbage to the backend.
func parseRFC3339OrZero(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("parsing time %q: %w", s, err)
	}
	return t, nil
}

// listLokiLabelNames lists all label names in a Loki (or VictoriaLogs) datasource
func listLokiLabelNames(ctx context.Context, args ListLokiLabelNamesParams) ([]string, error) {
	backend, err := lokiBackendForDatasource(ctx, args.DatasourceUID)
	if err != nil {
		return nil, fmt.Errorf("creating Loki backend: %w", err)
	}

	start, err := parseRFC3339OrZero(args.StartRFC3339)
	if err != nil {
		return nil, err
	}
	end, err := parseRFC3339OrZero(args.EndRFC3339)
	if err != nil {
		return nil, err
	}

	result, err := backend.ListLabelNames(ctx, start, end)
	if err != nil {
		return nil, err
	}

	if len(result) == 0 {
		return []string{}, nil
	}

	return result, nil
}

// ListLokiLabelNames is a tool for listing Loki label names
var ListLokiLabelNames = mcpgrafana.MustTool(
	"list_loki_label_names",
	"Lists all available label/field names (keys) found in logs within a specified Loki or VictoriaLogs datasource and time range. Returns a list of unique label strings (e.g., `[\"app\", \"env\", \"pod\"]`). If the time range is not provided, it defaults to the last hour.",
	listLokiLabelNames,
	mcp.WithTitleAnnotation("List Loki label names"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
)

// ListLokiLabelValuesParams defines the parameters for listing Loki label values
type ListLokiLabelValuesParams struct {
	DatasourceUID string `json:"datasourceUid" jsonschema:"required,description=The UID of the datasource to query"`
	LabelName     string `json:"labelName" jsonschema:"required,description=The name of the label to retrieve values for (e.g. 'app'\\, 'env'\\, 'pod')"`
	StartRFC3339  string `json:"startRfc3339,omitempty" jsonschema:"description=Optionally\\, the start time of the query in RFC3339 format (defaults to 1 hour ago)"`
	EndRFC3339    string `json:"endRfc3339,omitempty" jsonschema:"description=Optionally\\, the end time of the query in RFC3339 format (defaults to now)"`
}

// listLokiLabelValues lists all values for a specific label in a Loki (or VictoriaLogs) datasource
func listLokiLabelValues(ctx context.Context, args ListLokiLabelValuesParams) ([]string, error) {
	backend, err := lokiBackendForDatasource(ctx, args.DatasourceUID)
	if err != nil {
		return nil, fmt.Errorf("creating Loki backend: %w", err)
	}

	start, err := parseRFC3339OrZero(args.StartRFC3339)
	if err != nil {
		return nil, err
	}
	end, err := parseRFC3339OrZero(args.EndRFC3339)
	if err != nil {
		return nil, err
	}

	result, err := backend.ListLabelValues(ctx, args.LabelName, start, end)
	if err != nil {
		return nil, err
	}

	if len(result) == 0 {
		// Return empty slice instead of nil
		return []string{}, nil
	}

	return result, nil
}

// ListLokiLabelValues is a tool for listing Loki label values
var ListLokiLabelValues = mcpgrafana.MustTool(
	"list_loki_label_values",
	"Retrieves all unique values associated with a specific `labelName` within a Loki or VictoriaLogs datasource and time range. Returns a list of string values (e.g., for `labelName=\"env\"`, might return `[\"prod\", \"staging\", \"dev\"]`). Useful for discovering filter options. Defaults to the last hour if the time range is omitted.",
	listLokiLabelValues,
	mcp.WithTitleAnnotation("List Loki label values"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
)

// LokiLogStream represents a stream of log entries from Loki (resultType: "streams")
// Labels are in the "stream" field, timestamps are nanosecond strings
type LokiLogStream struct {
	Stream map[string]string   `json:"stream"`
	Values [][]json.RawMessage `json:"values"` // [[ts_nanos_string, log_line], ...]
}

// LokiMetricSample represents a metric sample from Loki (resultType: "vector" or "matrix")
// Labels are in the "metric" field, timestamps are float seconds, values are strings
type LokiMetricSample struct {
	Metric map[string]string   `json:"metric"`
	Value  []json.RawMessage   `json:"value,omitempty"`  // instant: [ts_float, value_string]
	Values [][]json.RawMessage `json:"values,omitempty"` // range: [[ts_float, value_string], ...]
}

// lokiQueryResponse is a generic response wrapper for Loki query endpoints
type lokiQueryResponse struct {
	Status string `json:"status"`
	Data   struct {
		ResultType    string          `json:"resultType"`              // "streams", "vector", or "matrix"
		EncodingFlags []string        `json:"encodingFlags,omitempty"` // e.g. ["categorize-labels"]
		Result        json.RawMessage `json:"result"`                  // Unmarshal based on resultType
		// Stats is a pointer so we can distinguish "stats missing" (nil) from "stats present with zero values"
		Stats *struct {
			Summary struct {
				TotalLinesProcessed int `json:"totalLinesProcessed"`
			} `json:"summary"`
		} `json:"stats,omitempty"`
	} `json:"data"`
}

// categorizedLabels is the third element of a log entry's values array when
// Loki responds with the categorize-labels encoding flag.
type categorizedLabels struct {
	StructuredMetadata map[string]string `json:"structuredMetadata,omitempty"`
	Parsed             map[string]string `json:"parsed,omitempty"`
}

// hasCategorizeLabelsFlag reports whether the response included the
// "categorize-labels" encoding flag from Loki >= 3.0.
func hasCategorizeLabelsFlag(flags []string) bool {
	for _, f := range flags {
		if f == "categorize-labels" {
			return true
		}
	}
	return false
}

// MetricValue represents a single metric data point with timestamp and value
type MetricValue struct {
	Timestamp string  `json:"timestamp"`
	Value     float64 `json:"value"`
}

// addTimeRangeParams adds start and end time parameters to the URL values
// It handles conversion from RFC3339 to Unix nanoseconds
func addTimeRangeParams(params url.Values, startRFC3339, endRFC3339 string) error {
	if startRFC3339 != "" {
		startTime, err := time.Parse(time.RFC3339, startRFC3339)
		if err != nil {
			return fmt.Errorf("parsing start time: %w", err)
		}
		params.Add("start", fmt.Sprintf("%d", startTime.UnixNano()))
	}

	if endRFC3339 != "" {
		endTime, err := time.Parse(time.RFC3339, endRFC3339)
		if err != nil {
			return fmt.Errorf("parsing end time: %w", err)
		}
		params.Add("end", fmt.Sprintf("%d", endTime.UnixNano()))
	}

	return nil
}

// getDefaultTimeRange returns default start and end times if not provided
// Returns start time (1 hour ago) and end time (now) in RFC3339 format
func getDefaultTimeRange(startRFC3339, endRFC3339 string) (string, string) {
	if startRFC3339 == "" {
		// Default to 1 hour ago if not specified
		startRFC3339 = time.Now().Add(-1 * time.Hour).Format(time.RFC3339)
	}
	if endRFC3339 == "" {
		// Default to now if not specified
		endRFC3339 = time.Now().Format(time.RFC3339)
	}
	return startRFC3339, endRFC3339
}

// fetchQueryParams contains parameters for fetching Loki query results
type fetchQueryParams struct {
	Query       string
	QueryType   string // "instant" or "range" (default)
	Start       string // RFC3339
	End         string // RFC3339
	Limit       int    // For log queries
	Direction   string // For log queries
	StepSeconds int    // For range metric queries
}

// fetchQuery executes a Loki query and returns the raw response for parsing.
// Routes to /query (instant) or /query_range (range) based on queryType.
func (c *Client) fetchQuery(ctx context.Context, p fetchQueryParams) (*lokiQueryResponse, error) {
	params := url.Values{}
	params.Add("query", p.Query)

	var endpoint string

	if p.QueryType == "instant" {
		// Instant queries use /query endpoint with a single "time" parameter
		endpoint = "/loki/api/v1/query"

		// For instant queries, use end time if provided, otherwise start time
		var queryTime string
		if p.End != "" {
			queryTime = p.End
		} else if p.Start != "" {
			queryTime = p.Start
		}

		if queryTime != "" {
			t, err := time.Parse(time.RFC3339, queryTime)
			if err != nil {
				return nil, fmt.Errorf("parsing query time: %w", err)
			}
			// Loki instant query accepts time as Unix timestamp in seconds (float)
			params.Add("time", fmt.Sprintf("%d", t.Unix()))
		}
	} else {
		// Range queries use /query_range endpoint with start/end
		endpoint = "/loki/api/v1/query_range"

		// Add time range parameters (converted to nanoseconds)
		if err := addTimeRangeParams(params, p.Start, p.End); err != nil {
			return nil, err
		}

		// Add log-specific parameters
		if p.Limit > 0 {
			params.Add("limit", fmt.Sprintf("%d", p.Limit))
		}

		if p.Direction != "" {
			params.Add("direction", p.Direction)
		}

		// Add step for metric range queries
		if p.StepSeconds > 0 {
			params.Add("step", fmt.Sprintf("%d", p.StepSeconds))
		}
	}

	bodyBytes, err := c.makeRequest(ctx, "GET", endpoint, params)
	if err != nil {
		return nil, err
	}

	var queryResponse lokiQueryResponse
	if err := json.Unmarshal(bodyBytes, &queryResponse); err != nil {
		return nil, fmt.Errorf("unmarshalling response (content: %s): %w", string(bodyBytes), err)
	}

	if queryResponse.Status != "success" {
		return nil, fmt.Errorf("loki API returned unexpected response format: %s", string(bodyBytes))
	}

	return &queryResponse, nil
}

// QueryLokiLogsParams defines the parameters for querying Loki logs
type QueryLokiLogsParams struct {
	DatasourceUID string `json:"datasourceUid" jsonschema:"required,description=The UID of the datasource to query"`
	LogQL         string `json:"logql" jsonschema:"required,description=The LogQL query to execute against Loki. This can be a simple label matcher or a complex query with filters\\, parsers\\, and expressions. Supports full LogQL syntax including label matchers\\, filter operators\\, pattern expressions\\, and pipeline operations."`
	StartRFC3339  string `json:"startRfc3339,omitempty" jsonschema:"description=Optionally\\, the start time of the query in RFC3339 format"`
	EndRFC3339    string `json:"endRfc3339,omitempty" jsonschema:"description=Optionally\\, the end time of the query in RFC3339 format"`
	Limit         int    `json:"limit,omitempty" jsonschema:"default=10,description=Optionally\\, the maximum number of log lines to return (default max: 100\\, configurable by MCP server)."`
	Direction     string `json:"direction,omitempty" jsonschema:"description=Optionally\\, the direction of the query: 'forward' (oldest first) or 'backward' (newest first\\, default)"`
	QueryType     string `json:"queryType,omitempty" jsonschema:"description=Query type: 'range' (default) or 'instant'. Instant queries return a single value at one point in time. Range queries return values over a time window. Use 'instant' for metric queries when you want the current value."`
	StepSeconds   int    `json:"stepSeconds,omitempty" jsonschema:"description=Resolution step in seconds for range metric queries. When running metric queries with queryType='range'\\, this controls the time resolution of the returned data points."`
}

// QueryMetadata provides context about the query results for AI agents
type QueryMetadata struct {
	LinesReturned     int    `json:"linesReturned"`
	MaxLinesAllowed   int    `json:"maxLinesAllowed"`
	ResultsTruncated  bool   `json:"resultsTruncated"`
	TotalLinesScanned *int   `json:"totalLinesScanned"` // nil if stats unavailable, 0 if actually zero lines scanned
	StartTime         string `json:"startTime,omitempty"`
	EndTime           string `json:"endTime,omitempty"`
}

// QueryLokiLogsResult wraps the Loki query result with optional hints
type QueryLokiLogsResult struct {
	Data     []LogEntry        `json:"data"`
	Hints    *EmptyResultHints `json:"hints,omitempty"`
	Metadata *QueryMetadata    `json:"metadata,omitempty"`
}

// LogEntry represents a single log entry or metric sample with metadata.
// When Loki returns categorized labels (via X-Loki-Response-Encoding-Flags),
// Labels contains only stream/index labels, while StructuredMetadata and
// Parsed carry the remaining label categories per entry.
type LogEntry struct {
	Timestamp          string            `json:"timestamp,omitempty"`
	Line               string            `json:"line,omitempty"`               // For log queries
	Value              *float64          `json:"value,omitempty"`              // For instant metric queries
	Values             []MetricValue     `json:"values,omitempty"`             // For range metric queries
	Labels             map[string]string `json:"labels"`                       // Stream / index labels
	StructuredMetadata map[string]string `json:"structuredMetadata,omitempty"` // Structured metadata labels (Loki >= 3.0)
	Parsed             map[string]string `json:"parsed,omitempty"`             // Parser-extracted labels (Loki >= 3.0)
}

// enforceLogLimit ensures a log limit value is within acceptable bounds
func enforceLogLimit(ctx context.Context, requestedLimit int) int {
	config := mcpgrafana.GrafanaConfigFromContext(ctx)
	maxLimit := config.MaxLokiLogLimit
	if maxLimit <= 0 {
		maxLimit = MaxLokiLogLimit // fallback for programmatic usage
	}

	if requestedLimit <= 0 {
		// Cap default to maxLimit in case admin configured a lower max
		if DefaultLokiLogLimit > maxLimit {
			return maxLimit
		}
		return DefaultLokiLogLimit
	}
	if requestedLimit > maxLimit {
		return maxLimit
	}
	return requestedLimit
}

// parseMetricValue parses a metric value from Loki response (string or number)
func parseMetricValue(raw json.RawMessage) (float64, error) {
	// Try parsing as string first (Loki returns values as strings)
	var strVal string
	if err := json.Unmarshal(raw, &strVal); err == nil {
		return strconv.ParseFloat(strVal, 64)
	}

	// Fall back to direct number parsing
	var numVal float64
	if err := json.Unmarshal(raw, &numVal); err == nil {
		return numVal, nil
	}

	return 0, fmt.Errorf("unable to parse metric value")
}

// parseMetricTimestamp parses a metric timestamp from Loki response (float seconds)
func parseMetricTimestamp(raw json.RawMessage) (string, error) {
	var ts float64
	if err := json.Unmarshal(raw, &ts); err != nil {
		return "", fmt.Errorf("parsing timestamp: %w", err)
	}
	// Convert float seconds to string representation
	return fmt.Sprintf("%.3f", ts), nil
}

// parseLokiQueryResponse converts a raw Loki /query(_range) response body
// into a slice of LogEntry. Extracted from queryLokiLogs so that the native
// Loki backend can reuse it without depending on the tool wrapper.
func parseLokiQueryResponse(response *lokiQueryResponse) ([]LogEntry, error) {
	var entries []LogEntry

	switch response.Data.ResultType {
	case "streams":
		var streams []LokiLogStream
		if err := json.Unmarshal(response.Data.Result, &streams); err != nil {
			return nil, fmt.Errorf("parsing streams result: %w", err)
		}

		// Loki >= 3.0 surfaces structured metadata and parser-extracted
		// labels in values[2] when the categorize-labels encoding flag is
		// echoed back in the response.
		categorized := hasCategorizeLabelsFlag(response.Data.EncodingFlags)

		for _, stream := range streams {
			for _, value := range stream.Values {
				if len(value) >= 2 {
					var logLine string
					if err := json.Unmarshal(value[1], &logLine); err != nil {
						continue
					}

					entry := LogEntry{
						Timestamp: string(value[0]),
						Line:      logLine,
						Labels:    stream.Stream,
					}

					if categorized && len(value) >= 3 {
						var cats categorizedLabels
						if err := json.Unmarshal(value[2], &cats); err == nil {
							entry.StructuredMetadata = cats.StructuredMetadata
							entry.Parsed = cats.Parsed
						}
					}

					entries = append(entries, entry)
				}
			}
		}

	case "vector":
		var samples []LokiMetricSample
		if err := json.Unmarshal(response.Data.Result, &samples); err != nil {
			return nil, fmt.Errorf("parsing vector result: %w", err)
		}
		for _, sample := range samples {
			if len(sample.Value) >= 2 {
				ts, err := parseMetricTimestamp(sample.Value[0])
				if err != nil {
					continue
				}
				val, err := parseMetricValue(sample.Value[1])
				if err != nil {
					continue
				}
				entries = append(entries, LogEntry{
					Timestamp: ts,
					Value:     &val,
					Labels:    sample.Metric,
				})
			}
		}

	case "matrix":
		var samples []LokiMetricSample
		if err := json.Unmarshal(response.Data.Result, &samples); err != nil {
			return nil, fmt.Errorf("parsing matrix result: %w", err)
		}
		for _, sample := range samples {
			var metricValues []MetricValue
			for _, value := range sample.Values {
				if len(value) >= 2 {
					ts, err := parseMetricTimestamp(value[0])
					if err != nil {
						continue
					}
					val, err := parseMetricValue(value[1])
					if err != nil {
						continue
					}
					metricValues = append(metricValues, MetricValue{Timestamp: ts, Value: val})
				}
			}
			if len(metricValues) > 0 {
				entries = append(entries, LogEntry{
					Values: metricValues,
					Labels: sample.Metric,
				})
			}
		}

	default:
		return nil, fmt.Errorf("unsupported result type: %s", response.Data.ResultType)
	}

	if entries == nil {
		entries = []LogEntry{}
	}
	return entries, nil
}

// queryLokiLogs queries logs from a Loki-compatible datasource. The actual
// transport (native Loki vs VictoriaLogs) is selected by the backend
// dispatch; this function owns parameter normalization, truncation
// detection, metadata, and empty-result hints.
func queryLokiLogs(ctx context.Context, args QueryLokiLogsParams) (*QueryLokiLogsResult, error) {
	backend, err := lokiBackendForDatasource(ctx, args.DatasourceUID)
	if err != nil {
		return nil, fmt.Errorf("creating Loki backend: %w", err)
	}

	// Time defaults: range queries default to "last hour"; instant queries
	// pass through verbatim because the backend chooses the anchor itself.
	var startTimeStr, endTimeStr string
	usedDefaultTimeRange := false
	if args.QueryType == "instant" {
		startTimeStr = args.StartRFC3339
		endTimeStr = args.EndRFC3339
	} else {
		usedDefaultTimeRange = args.StartRFC3339 == "" && args.EndRFC3339 == ""
		startTimeStr, endTimeStr = getDefaultTimeRange(args.StartRFC3339, args.EndRFC3339)
	}

	startTime, err := parseRFC3339OrZero(startTimeStr)
	if err != nil {
		return nil, err
	}
	endTime, err := parseRFC3339OrZero(endTimeStr)
	if err != nil {
		return nil, err
	}

	limit := enforceLogLimit(ctx, args.Limit)
	queryLimit := limit + 1 // ask for one extra so we can detect truncation

	direction := args.Direction
	if direction == "" {
		direction = "backward"
	}

	result, err := backend.QueryLogs(ctx, lokiQueryParams{
		Query:       args.LogQL,
		QueryType:   args.QueryType,
		Start:       startTime,
		End:         endTime,
		Limit:       queryLimit,
		Direction:   direction,
		StepSeconds: args.StepSeconds,
	})
	if err != nil {
		return nil, err
	}

	entries := result.Entries
	if entries == nil {
		entries = []LogEntry{}
	}

	// Truncation only applies to log (streams) queries — metric responses
	// don't carry a row limit on Loki and VictoriaLogs returns metric
	// shapes only for stats queries we don't route here.
	truncated := false
	if result.ResultType == "streams" {
		truncated = len(entries) > limit
		if truncated {
			entries = entries[:limit]
		}
	}

	out := &QueryLokiLogsResult{
		Data: entries,
		Metadata: &QueryMetadata{
			LinesReturned:     len(entries),
			MaxLinesAllowed:   limit,
			ResultsTruncated:  truncated,
			TotalLinesScanned: result.TotalLinesScanned,
			StartTime:         startTimeStr,
			EndTime:           endTimeStr,
		},
	}

	if len(entries) == 0 {
		out.Hints = GenerateEmptyResultHints(HintContext{
			DatasourceType: "loki",
			Query:          args.LogQL,
			StartTime:      startTime,
			EndTime:        endTime,
		})
	}

	if usedDefaultTimeRange && out.Hints == nil {
		out.Hints = &EmptyResultHints{
			Summary:          "This query used the default 1-hour lookback window because startRfc3339 and endRfc3339 were not provided.",
			PossibleCauses:   []string{},
			SuggestedActions: []string{"If results seem incomplete or you need data from a wider time range, provide explicit startRfc3339 and endRfc3339 parameters."},
		}
	}

	return out, nil
}

// QueryLokiLogs is a tool for querying logs from Loki
var QueryLokiLogs = mcpgrafana.MustTool(
	"query_loki_logs",
	"Executes a log query against a Loki or VictoriaLogs datasource and returns matching log entries (or metric samples on Loki). Defaults to the last hour, a limit of 10 entries, and 'backward' direction (newest first). The `logql` parameter takes LogQL on Loki and LogsQL on VictoriaLogs (e.g., Loki: `{app=\"foo\"} |= \"error\"`; VictoriaLogs: `{app=\"foo\"} \"error\"`). To count matching log lines precisely, use a `count_over_time()` metric query with queryType='instant'. Prefer using `query_loki_stats` first to cheaply check whether a stream contains data (avoiding expensive queries against empty streams) and `list_loki_label_names` / `list_loki_label_values` to verify labels exist before querying. Note: `query_loki_stats` returns approximate storage-level counts, not exact log line counts.",
	queryLokiLogs,
	mcp.WithTitleAnnotation("Query Loki logs"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
)

// fetchStats is a method to fetch stats data from Loki API
func (c *Client) fetchStats(ctx context.Context, query, startRFC3339, endRFC3339 string) (*Stats, error) {
	params := url.Values{}
	params.Add("query", query)

	// Add time range parameters
	if err := addTimeRangeParams(params, startRFC3339, endRFC3339); err != nil {
		return nil, err
	}

	bodyBytes, err := c.makeRequest(ctx, "GET", "/loki/api/v1/index/stats", params)
	if err != nil {
		return nil, err
	}

	var stats Stats
	err = json.Unmarshal(bodyBytes, &stats)
	if err != nil {
		return nil, fmt.Errorf("unmarshalling response (content: %s): %w", string(bodyBytes), err)
	}

	return &stats, nil
}

// fetchPatterns is a method to fetch pattern data from Loki API
func (c *Client) fetchPatterns(ctx context.Context, query, startRFC3339, endRFC3339, step string) ([]Pattern, error) {
	params := url.Values{}
	params.Add("query", query)

	// Add time range parameters
	if err := addTimeRangeParams(params, startRFC3339, endRFC3339); err != nil {
		return nil, err
	}

	if step != "" {
		params.Add("step", step)
	}

	bodyBytes, err := c.makeRequest(ctx, "GET", "/loki/api/v1/patterns", params)
	if err != nil {
		return nil, err
	}

	var patternsResponse patternsAPIResponse
	err = json.Unmarshal(bodyBytes, &patternsResponse)
	if err != nil {
		return nil, fmt.Errorf("unmarshalling response (content: %s): %w", string(bodyBytes), err)
	}

	if patternsResponse.Status != "success" {
		return nil, fmt.Errorf("loki API returned unexpected response format: %s", string(bodyBytes))
	}

	if patternsResponse.Data == nil {
		return []Pattern{}, nil
	}

	// Convert API response to summarized patterns
	patterns := make([]Pattern, len(patternsResponse.Data))
	for i, p := range patternsResponse.Data {
		var total int64
		for _, s := range p.Samples {
			total += s[1] // s[0] is timestamp, s[1] is value
		}
		patterns[i] = Pattern{
			Pattern:    p.Pattern,
			TotalCount: total,
		}
	}

	return patterns, nil
}

// QueryLokiStatsParams defines the parameters for querying Loki stats
type QueryLokiStatsParams struct {
	DatasourceUID string `json:"datasourceUid" jsonschema:"required,description=The UID of the datasource to query"`
	LogQL         string `json:"logql" jsonschema:"required,description=The LogQL matcher expression to execute. This parameter only accepts label matcher expressions and does not support full LogQL queries. Line filters\\, pattern operations\\, and metric aggregations are not supported by the stats API endpoint. Only simple label selectors can be used here."`
	StartRFC3339  string `json:"startRfc3339,omitempty" jsonschema:"description=Optionally\\, the start time of the query in RFC3339 format"`
	EndRFC3339    string `json:"endRfc3339,omitempty" jsonschema:"description=Optionally\\, the end time of the query in RFC3339 format"`
}

// queryLokiStats queries stats from a Loki-compatible datasource. On
// VictoriaLogs only the entries count is populated (no chunks/streams/bytes).
func queryLokiStats(ctx context.Context, args QueryLokiStatsParams) (*Stats, error) {
	backend, err := lokiBackendForDatasource(ctx, args.DatasourceUID)
	if err != nil {
		return nil, fmt.Errorf("creating Loki backend: %w", err)
	}

	startTimeStr, endTimeStr := getDefaultTimeRange(args.StartRFC3339, args.EndRFC3339)
	startTime, err := parseRFC3339OrZero(startTimeStr)
	if err != nil {
		return nil, err
	}
	endTime, err := parseRFC3339OrZero(endTimeStr)
	if err != nil {
		return nil, err
	}

	return backend.QueryStats(ctx, args.LogQL, startTime, endTime)
}

// QueryLokiStats is a tool for querying stats from Loki
var QueryLokiStats = mcpgrafana.MustTool(
	"query_loki_stats",
	"Retrieves index-level statistics about log streams matching a given selector within a Loki or VictoriaLogs datasource and time range. Returns an object containing the count of streams, chunks, entries, and total bytes (e.g., `{\"streams\": 5, \"chunks\": 50, \"entries\": 10000, \"bytes\": 512000}`). **Important**: the `entries` count reflects storage-level index entries (chunk metadata), NOT the number of individual log lines matching the selector. To count actual matching log lines, use `query_loki_logs` with a `count_over_time()` metric query instead. On VictoriaLogs only `entries` is populated; the other fields remain zero. The `logql` parameter **must** be a simple label selector (e.g., `{app=\"nginx\", env=\"prod\"}`) and does not support line filters, parsers, or aggregations. Defaults to the last hour if the time range is omitted.",
	queryLokiStats,
	mcp.WithTitleAnnotation("Get Loki log statistics"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
)

// QueryLokiPatternsParams defines the parameters for querying Loki patterns
type QueryLokiPatternsParams struct {
	DatasourceUID string `json:"datasourceUid" jsonschema:"required,description=The UID of the datasource to query"`
	LogQL         string `json:"logql" jsonschema:"required,description=A LogQL stream selector to identify the logs to analyze for patterns (e.g. {job=\"foo\"\\, namespace=\"bar\"})"`
	StartRFC3339  string `json:"startRfc3339,omitempty" jsonschema:"description=Optionally\\, the start time of the query in RFC3339 format (defaults to 1 hour ago)"`
	EndRFC3339    string `json:"endRfc3339,omitempty" jsonschema:"description=Optionally\\, the end time of the query in RFC3339 format (defaults to now)"`
	Step          string `json:"step,omitempty" jsonschema:"description=Optionally\\, the query resolution step (e.g. '5m')"`
}

// queryLokiPatterns queries detected log patterns from a Loki-compatible
// datasource. VictoriaLogs has no equivalent endpoint and surfaces a clear
// error from the backend.
func queryLokiPatterns(ctx context.Context, args QueryLokiPatternsParams) ([]Pattern, error) {
	backend, err := lokiBackendForDatasource(ctx, args.DatasourceUID)
	if err != nil {
		return nil, fmt.Errorf("creating Loki backend: %w", err)
	}

	startTimeStr, endTimeStr := getDefaultTimeRange(args.StartRFC3339, args.EndRFC3339)
	startTime, err := parseRFC3339OrZero(startTimeStr)
	if err != nil {
		return nil, err
	}
	endTime, err := parseRFC3339OrZero(endTimeStr)
	if err != nil {
		return nil, err
	}

	return backend.QueryPatterns(ctx, args.LogQL, args.Step, startTime, endTime)
}

// QueryLokiPatterns is a tool for querying detected log patterns from Loki
var QueryLokiPatterns = mcpgrafana.MustTool(
	"query_loki_patterns",
	"Retrieves detected log patterns from a Loki datasource for a given stream selector and time range. Returns a list of patterns, each containing a pattern string and a total count of occurrences. Patterns help identify common log structures and anomalies. The `logql` parameter must be a stream selector (e.g., `{job=\"nginx\"}`) and does not support line filters or aggregations. Defaults to the last hour if the time range is omitted. **Not supported on VictoriaLogs** datasources - use a `| stats` pipeline instead.",
	queryLokiPatterns,
	mcp.WithTitleAnnotation("Query Loki patterns"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
)

// AddLokiTools registers all Loki tools with the MCP server.
// Config-generation tools (e.g. suggest_loki_alloy_label_config) live in
// the separate "config" category — see tools.AddConfigTools.
func AddLokiTools(mcp *server.MCPServer) {
	ListLokiLabelNames.Register(mcp)
	ListLokiLabelValues.Register(mcp)
	QueryLokiStats.Register(mcp)
	QueryLokiLogs.Register(mcp)
	QueryLokiPatterns.Register(mcp)
	AddLokiLabelAnalyzerTools(mcp)
}
