package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	mcpgrafana "github.com/grafana/mcp-grafana"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

const (
	tempoAcceptLLM = "application/llm"
	// Tempo's own MCP server, used only for the embedded TraceQL docs; every
	// other Tempo capability is reachable over plain REST.
	tempoMCPPath            = "/api/mcp"
	tempoAcceptMCP          = "application/json, text/event-stream"
	tempoMCPProtocolVersion = "2025-06-18"
)

type tempoBackend struct {
	httpClient *http.Client
	baseURL    string
}

func newTempoBackend(ctx context.Context, datasourceUID string) (*tempoBackend, error) {
	cfg := mcpgrafana.GrafanaConfigFromContext(ctx)
	proxyURL := fmt.Sprintf("%s/api/datasources/proxy/uid/%s", strings.TrimRight(cfg.URL, "/"), datasourceUID)

	transport, err := mcpgrafana.BuildTransport(&cfg, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create transport: %w", err)
	}

	return &tempoBackend{
		httpClient: &http.Client{Transport: transport, CheckRedirect: refuseRedirect},
		baseURL:    proxyURL,
	}, nil
}

func tempoBackendForDatasource(ctx context.Context, uid string) (*tempoBackend, error) {
	ds, err := getDatasourceByUID(ctx, GetDatasourceByUIDParams{UID: uid})
	if err != nil {
		return nil, err
	}
	if ds.Type != "tempo" {
		return nil, fmt.Errorf("datasource %s is of type %s, not tempo", uid, ds.Type)
	}
	return newTempoBackend(ctx, ds.UID)
}

func (b *tempoBackend) doGet(ctx context.Context, path string, query url.Values) (string, error) {
	u := b.baseURL + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Accept", tempoAcceptLLM)

	resp, err := b.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := readResponseBody(resp.Body, defaultResponseLimitBytes)
	if err != nil {
		return "", fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", tempoAPIError(resp.StatusCode, body)
	}

	return string(body), nil
}

func (b *tempoBackend) doGetWithAccept(ctx context.Context, path string, query url.Values, accept string) (string, error) {
	u := b.baseURL + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Accept", accept)

	resp, err := b.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := readResponseBody(resp.Body, defaultResponseLimitBytes)
	if err != nil {
		return "", fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", tempoAPIError(resp.StatusCode, body)
	}

	return string(body), nil
}

func (b *tempoBackend) doPost(ctx context.Context, path string, payload any) (string, error) {
	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("failed to marshal request body: %w", err)
	}

	u := b.baseURL + path
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(bodyBytes))
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Accept", tempoAcceptLLM)
	req.Header.Set("Content-Type", "application/json")

	resp, err := b.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := readResponseBody(resp.Body, defaultResponseLimitBytes)
	if err != nil {
		return "", fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", tempoAPIError(resp.StatusCode, body)
	}

	return string(body), nil
}

// mcpToolCall invokes a tool on Tempo's own MCP server, reached through the same
// datasource proxy as the REST tools.
//
// Tempo rejects a tools/call that carries no session, so each call opens a
// throwaway one: initialize, read the session id off the response header, then
// call. Nothing is cached between calls, which is what keeps this from
// reintroducing the per-session tool store the old proxy layer required. Only
// worth it for content Tempo serves nowhere else, i.e. the embedded docs.
func (b *tempoBackend) mcpToolCall(ctx context.Context, name string, args map[string]any) (string, error) {
	sessionID, err := b.mcpInitialize(ctx)
	if err != nil {
		return "", err
	}

	body, _, err := b.doMCPPost(ctx, map[string]any{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  "tools/call",
		"params":  map[string]any{"name": name, "arguments": args},
	}, sessionID)
	if err != nil {
		return "", err
	}
	return tempoMCPToolText(body)
}

func (b *tempoBackend) mcpInitialize(ctx context.Context) (string, error) {
	_, header, err := b.doMCPPost(ctx, map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params": map[string]any{
			"protocolVersion": tempoMCPProtocolVersion,
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]any{"name": "mcp-grafana", "version": "1"},
		},
	}, "")
	if err != nil {
		return "", err
	}

	sessionID := header.Get("Mcp-Session-Id")
	if sessionID == "" {
		return "", fmt.Errorf("tempo MCP server did not return a session id")
	}
	return sessionID, nil
}

func (b *tempoBackend) doMCPPost(ctx context.Context, payload any, sessionID string) (string, http.Header, error) {
	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return "", nil, fmt.Errorf("failed to marshal request body: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, b.baseURL+tempoMCPPath, bytes.NewReader(bodyBytes))
	if err != nil {
		return "", nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Accept", tempoAcceptMCP)
	req.Header.Set("Content-Type", "application/json")
	if sessionID != "" {
		req.Header.Set("Mcp-Session-Id", sessionID)
	}

	resp, err := b.httpClient.Do(req)
	if err != nil {
		return "", nil, fmt.Errorf("request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := readResponseBody(resp.Body, defaultResponseLimitBytes)
	if err != nil {
		return "", nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", nil, tempoAPIError(resp.StatusCode, body)
	}

	return string(body), resp.Header, nil
}

// tempoMCPToolText pulls the text content out of a tools/call result. The
// response is JSON, or the same JSON inside an SSE frame when Tempo streams it.
func tempoMCPToolText(body string) (string, error) {
	payload := strings.TrimSpace(body)
	if rest, ok := strings.CutPrefix(payload, "event:"); ok {
		_, payload, _ = strings.Cut(rest, "data:")
	} else if rest, ok := strings.CutPrefix(payload, "data:"); ok {
		payload = rest
	}

	var envelope struct {
		Result struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
			IsError bool `json:"isError"`
		} `json:"result"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(payload)), &envelope); err != nil {
		return "", fmt.Errorf("failed to parse tempo MCP response: %w", err)
	}
	if envelope.Error != nil {
		return "", fmt.Errorf("tempo MCP error: %s", envelope.Error.Message)
	}

	var text strings.Builder
	for _, item := range envelope.Result.Content {
		if item.Type == "text" {
			text.WriteString(item.Text)
		}
	}
	if envelope.Result.IsError {
		return "", fmt.Errorf("tempo MCP tool error: %s", strings.TrimSpace(text.String()))
	}
	if text.Len() == 0 {
		return "", fmt.Errorf("tempo MCP tool returned no text content")
	}
	return text.String(), nil
}

func tempoAPIError(statusCode int, body []byte) error {
	msg := strings.TrimSpace(string(body))
	if msg == "" && statusCode == 499 {
		msg = "query timed out — try narrowing the time range or simplifying the query"
	}
	return fmt.Errorf("tempo API returned %d: %s", statusCode, msg)
}

func tempoParseStartToEpochSeconds(value string) (string, error) {
	t, err := parseStartTime(value)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%d", t.Unix()), nil
}

func tempoParseEndToEpochSeconds(value string) (string, error) {
	t, err := parseEndTime(value)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%d", t.Unix()), nil
}

func tempoParseStartToEpochNanos(value string) (string, error) {
	t, err := parseStartTime(value)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%d", t.UnixNano()), nil
}

func tempoParseEndToEpochNanos(value string) (string, error) {
	t, err := parseEndTime(value)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%d", t.UnixNano()), nil
}

// Parameter structs for Tempo tools.

type SearchTempoTracesParams struct {
	DatasourceUID string `json:"datasourceUid" jsonschema:"required,description=UID of the tempo datasource to query"`
	Query         string `json:"query" jsonschema:"required,description=TraceQL query string"`
	Start         string `json:"start,omitempty" jsonschema:"description=Start time for the search (RFC3339 format). If not provided will search the past 1 hour. If provided\\, must be before end."`
	End           string `json:"end,omitempty" jsonschema:"description=End time for the search (RFC3339 format). If not provided will search the past 1 hour. If provided\\, must be after start."`
}

type QueryTempoMetricsParams struct {
	DatasourceUID string `json:"datasourceUid" jsonschema:"required,description=UID of the tempo datasource to query"`
	Query         string `json:"query" jsonschema:"required,description=TraceQL metrics query string (e.g. '{ } | count_over_time()' or '{ } | rate()')."`
	Type          string `json:"type,omitempty" jsonschema:"enum=instant,enum=range,default=range,description=Query type: 'instant' returns a single value at the end of the time range; 'range' returns a time series. Default is 'range'."`
	Start         string `json:"start,omitempty" jsonschema:"description=Start time (RFC3339 format). If not provided will search the past 1 hour."`
	End           string `json:"end,omitempty" jsonschema:"description=End time (RFC3339 format). If not provided will search the past 1 hour."`
}

type GetTempoTraceParams struct {
	DatasourceUID string `json:"datasourceUid" jsonschema:"required,description=UID of the tempo datasource to query"`
	TraceID       string `json:"trace_id" jsonschema:"required,description=Trace ID to retrieve"`
}

type DiffTempoTracesParams struct {
	DatasourceUID  string `json:"datasourceUid" jsonschema:"required,description=UID of the tempo datasource to query"`
	BaseTraceID    string `json:"base_trace_id" jsonschema:"required,description=Trace ID for the baseline trace"`
	CompareTraceID string `json:"compare_trace_id" jsonschema:"required,description=Trace ID for the comparison trace"`
	BaseStart      string `json:"base_start,omitempty" jsonschema:"description=Optional start of the baseline trace search range in RFC3339 format. Must be provided with base_end."`
	BaseEnd        string `json:"base_end,omitempty" jsonschema:"description=Optional end of the baseline trace search range in RFC3339 format. Must be provided with base_start."`
	CompareStart   string `json:"compare_start,omitempty" jsonschema:"description=Optional start of the comparison trace search range in RFC3339 format. Must be provided with compare_end."`
	CompareEnd     string `json:"compare_end,omitempty" jsonschema:"description=Optional end of the comparison trace search range in RFC3339 format. Must be provided with compare_start."`
	Format         string `json:"format,omitempty" jsonschema:"enum=trace-summary-v0-composed,enum=trace-summary-v0-native,enum=trace-patch-v0,default=trace-summary-v0-composed,description=Output format. The composed format returns a compact summary and attaches the full patch only when it is at most 64 KiB. trace-patch-v0 has no output-size guarantee."`
}

type ListTempoAttributeNamesParams struct {
	DatasourceUID string `json:"datasourceUid" jsonschema:"required,description=UID of the tempo datasource to query"`
	Scope         string `json:"scope,omitempty" jsonschema:"description=Scope to filter attributes by (span\\, resource\\, event\\, link\\, instrumentation). Strongly recommended — omitting scope returns all attributes across all scopes which can be very large (100K+ chars)."`
}

type ListTempoAttributeValuesParams struct {
	DatasourceUID string `json:"datasourceUid" jsonschema:"required,description=UID of the tempo datasource to query"`
	Name          string `json:"name" jsonschema:"required,description=The attribute name to get values for (e.g. 'span.http.method'\\, 'resource.service.name')"`
	FilterQuery   string `json:"filter-query,omitempty" jsonschema:"description=Filter query to apply to the attribute values. It can only have one spanset and only &&'ed conditions like { <cond> && <cond> && ... }. This is useful for filtering the values to a specific set of values."`
}

type GetTempoTraceQLDocsParams struct {
	DatasourceUID string `json:"datasourceUid" jsonschema:"required,description=UID of the tempo datasource to query"`
	Topic         string `json:"topic" jsonschema:"required,enum=basic,enum=aggregates,enum=structural,enum=metrics,description=Which section of the TraceQL reference to retrieve: 'basic' for attribute and duration filters\\, 'aggregates' for count/avg/sum and by() grouping\\, 'structural' for parent/child and descendant operators\\, 'metrics' for rate/quantile_over_time metrics queries"`
}

// Handler functions.

func searchTempoTraces(ctx context.Context, args SearchTempoTracesParams) (*mcp.CallToolResult, error) {
	backend, err := tempoBackendForDatasource(ctx, args.DatasourceUID)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	params := url.Values{}
	params.Set("q", args.Query)

	if args.Start != "" {
		epoch, err := tempoParseStartToEpochSeconds(args.Start)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("invalid start time: %v", err)), nil
		}
		params.Set("start", epoch)
	}
	if args.End != "" {
		epoch, err := tempoParseEndToEpochSeconds(args.End)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("invalid end time: %v", err)), nil
		}
		params.Set("end", epoch)
	}

	body, err := backend.doGet(ctx, "/api/search", params)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	return tempoToolResult(body, "search-results", "json"), nil
}

func queryTempoMetrics(ctx context.Context, args QueryTempoMetricsParams) (*mcp.CallToolResult, error) {
	backend, err := tempoBackendForDatasource(ctx, args.DatasourceUID)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	params := url.Values{}
	params.Set("q", args.Query)

	if args.Start != "" {
		epoch, err := tempoParseStartToEpochNanos(args.Start)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("invalid start time: %v", err)), nil
		}
		params.Set("start", epoch)
	}
	if args.End != "" {
		epoch, err := tempoParseEndToEpochNanos(args.End)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("invalid end time: %v", err)), nil
		}
		params.Set("end", epoch)
	}

	queryType := args.Type
	if queryType == "" {
		queryType = "range"
	}

	var endpoint string
	var resultType string
	switch queryType {
	case "instant":
		endpoint = "/api/metrics/query"
		resultType = "metrics-instant"
	case "range":
		endpoint = "/api/metrics/query_range"
		resultType = "metrics-range"
	default:
		return mcp.NewToolResultError(fmt.Sprintf("invalid type %q: must be 'instant' or 'range'", queryType)), nil
	}

	body, err := backend.doGet(ctx, endpoint, params)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	return tempoToolResult(body, resultType, "json"), nil
}

func getTempoTrace(ctx context.Context, args GetTempoTraceParams) (*mcp.CallToolResult, error) {
	backend, err := tempoBackendForDatasource(ctx, args.DatasourceUID)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	body, err := backend.doGetWithAccept(ctx, "/api/v2/traces/"+url.PathEscape(args.TraceID), nil, tempoAcceptLLM+", application/json")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	return tempoToolResult(body, "trace", "json"), nil
}

type traceDiffAPIRequest struct {
	Base    traceDiffTraceRequest `json:"base"`
	Compare traceDiffTraceRequest `json:"compare"`
	Format  string                `json:"format"`
}

type traceDiffTraceRequest struct {
	TraceID string `json:"traceID"`
	Start   *int64 `json:"start,omitempty"`
	End     *int64 `json:"end,omitempty"`
}

func diffTempoTraces(ctx context.Context, args DiffTempoTracesParams) (*mcp.CallToolResult, error) {
	backend, err := tempoBackendForDatasource(ctx, args.DatasourceUID)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	format := args.Format
	if format == "" {
		format = "trace-summary-v0-composed"
	}

	baseStart, baseEnd, err := parseOptionalTimeRange(args.BaseStart, args.BaseEnd, "base_start", "base_end")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	compareStart, compareEnd, err := parseOptionalTimeRange(args.CompareStart, args.CompareEnd, "compare_start", "compare_end")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	diffReq := traceDiffAPIRequest{
		Base: traceDiffTraceRequest{
			TraceID: args.BaseTraceID,
			Start:   baseStart,
			End:     baseEnd,
		},
		Compare: traceDiffTraceRequest{
			TraceID: args.CompareTraceID,
			Start:   compareStart,
			End:     compareEnd,
		},
		Format: format,
	}

	body, err := backend.doPost(ctx, "/api/v2/traces/diff", diffReq)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	return tempoToolResult(body, "trace-diff", "json"), nil
}

func parseOptionalTimeRange(startStr, endStr, startName, endName string) (*int64, *int64, error) {
	hasStart := startStr != ""
	hasEnd := endStr != ""
	if hasStart != hasEnd {
		return nil, nil, fmt.Errorf("arguments %q and %q must be provided together", startName, endName)
	}
	if !hasStart {
		return nil, nil, nil
	}

	startTS, err := parseStartTime(startStr)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid %s: %w", startName, err)
	}
	endTS, err := parseEndTime(endStr)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid %s: %w", endName, err)
	}

	startNanos := startTS.UnixNano()
	endNanos := endTS.UnixNano()
	return &startNanos, &endNanos, nil
}

const tempoAttributeNamesSummaryThreshold = 32_000

func listTempoAttributeNames(ctx context.Context, args ListTempoAttributeNamesParams) (*mcp.CallToolResult, error) {
	backend, err := tempoBackendForDatasource(ctx, args.DatasourceUID)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	params := url.Values{}
	if args.Scope != "" {
		params.Set("scope", args.Scope)
	}

	body, err := backend.doGet(ctx, "/api/v2/search/tags", params)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	if args.Scope == "" && len(body) > tempoAttributeNamesSummaryThreshold {
		if summary, ok := summarizeTempoAttributeNames(body); ok {
			return tempoToolResult(summary, "attribute-names-summary", "text"), nil
		}
	}

	return tempoToolResult(body, "attribute-names", "json"), nil
}

func summarizeTempoAttributeNames(body string) (string, bool) {
	var resp struct {
		Scopes []struct {
			Name string   `json:"name"`
			Tags []string `json:"tags"`
		} `json:"scopes"`
	}
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		return "", false
	}

	var b strings.Builder
	total := 0
	b.WriteString("Response too large to return in full. Attribute counts per scope:\n\n")
	for _, s := range resp.Scopes {
		total += len(s.Tags)
		fmt.Fprintf(&b, "  %s: %d attributes\n", s.Name, len(s.Tags))
	}
	fmt.Fprintf(&b, "\nTotal: %d attributes across %d scopes.\n", total, len(resp.Scopes))
	b.WriteString("Call again with the scope parameter (e.g. scope=resource or scope=span) to see the attribute names for a specific scope.")
	return b.String(), true
}

func listTempoAttributeValues(ctx context.Context, args ListTempoAttributeValuesParams) (*mcp.CallToolResult, error) {
	backend, err := tempoBackendForDatasource(ctx, args.DatasourceUID)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	params := url.Values{}
	if args.FilterQuery != "" {
		params.Set("q", args.FilterQuery)
	}

	body, err := backend.doGet(ctx, "/api/v2/search/tag/"+url.PathEscape(args.Name)+"/values", params)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	return tempoToolResult(body, "attribute-values", "json"), nil
}

func getTempoTraceQLDocs(ctx context.Context, args GetTempoTraceQLDocsParams) (*mcp.CallToolResult, error) {
	switch args.Topic {
	case "basic", "aggregates", "structural", "metrics":
	default:
		return mcp.NewToolResultError(fmt.Sprintf("invalid topic %q: must be 'basic', 'aggregates', 'structural', or 'metrics'", args.Topic)), nil
	}

	backend, err := tempoBackendForDatasource(ctx, args.DatasourceUID)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	body, err := backend.mcpToolCall(ctx, "docs-traceql", map[string]any{"name": args.Topic})
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	return tempoToolResult(body, "traceql-docs", "markdown"), nil
}

func tempoToolResult(body string, contentType string, encoding string) *mcp.CallToolResult {
	res := mcp.NewToolResultText(body)
	res.Meta = &mcp.Meta{AdditionalFields: map[string]any{
		"type":     contentType,
		"encoding": encoding,
	}}
	return res
}

// Tool definitions.

var SearchTempoTracesTool = mcpgrafana.MustTool(
	"search_tempo_traces",
	"Search for traces using TraceQL queries",
	searchTempoTraces,
	mcp.WithTitleAnnotation("Search Tempo traces"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
	mcp.WithDestructiveHintAnnotation(false),
	mcp.WithOpenWorldHintAnnotation(false),
)

var QueryTempoMetricsTool = mcpgrafana.MustTool(
	"query_tempo_metrics",
	"Compute trace-derived metrics using a TraceQL metrics query. Use type 'instant' for a single value or 'range' for a time series (default). Instant queries over large time ranges may timeout — keep the window under 15 minutes for instant, or use range instead.",
	queryTempoMetrics,
	mcp.WithTitleAnnotation("Query Tempo metrics"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
	mcp.WithDestructiveHintAnnotation(false),
	mcp.WithOpenWorldHintAnnotation(false),
)

var GetTempoTraceTool = mcpgrafana.MustTool(
	"get_tempo_trace",
	"Retrieve a specific trace by ID",
	getTempoTrace,
	mcp.WithTitleAnnotation("Get Tempo trace"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
	mcp.WithDestructiveHintAnnotation(false),
	mcp.WithOpenWorldHintAnnotation(false),
)

var DiffTempoTracesTool = mcpgrafana.MustTool(
	"diff_tempo_traces",
	"Compare two complete traces. Returns a compact summary and includes the full span-level patch when it is at most 64 KiB. Request trace-patch-v0 only when full details are required; full patches are not size-bounded.",
	diffTempoTraces,
	mcp.WithTitleAnnotation("Diff Tempo traces"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
	mcp.WithDestructiveHintAnnotation(false),
	mcp.WithOpenWorldHintAnnotation(false),
)

var ListTempoAttributeNamesTool = mcpgrafana.MustTool(
	"list_tempo_attribute_names",
	"List available attribute names for TraceQL queries. Always pass a scope (resource, span, etc.) to avoid very large responses.",
	listTempoAttributeNames,
	mcp.WithTitleAnnotation("List Tempo attribute names"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
	mcp.WithDestructiveHintAnnotation(false),
	mcp.WithOpenWorldHintAnnotation(false),
)

var ListTempoAttributeValuesTool = mcpgrafana.MustTool(
	"list_tempo_attribute_values",
	"List values for a fully scoped attribute name (e.g. resource.service.name). Useful for discovering what values exist for a specific attribute.",
	listTempoAttributeValues,
	mcp.WithTitleAnnotation("List Tempo attribute values"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
	mcp.WithDestructiveHintAnnotation(false),
	mcp.WithOpenWorldHintAnnotation(false),
)

var GetTempoTraceQLDocsTool = mcpgrafana.MustTool(
	"get_tempo_traceql_docs",
	"Retrieve TraceQL reference documentation with examples, covering attribute filters, aggregates, structural operators, and metrics queries. Consult this before writing a non-trivial TraceQL query, or after one returns an error or no results.",
	getTempoTraceQLDocs,
	mcp.WithTitleAnnotation("Get TraceQL documentation"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
	mcp.WithDestructiveHintAnnotation(false),
	mcp.WithOpenWorldHintAnnotation(false),
)

// AddTempoTools registers all Tempo tools on the MCP server. Tools call
// Tempo's REST API through the Grafana datasource proxy, except for
// get_tempo_traceql_docs: the docs are embedded in the Tempo binary and served
// only over Tempo's MCP endpoint, so that one tool speaks MCP to fetch them
// rather than shipping a copy that drifts from the deployed Tempo.
//
// docs-config is still not exposed — it describes how to configure a Tempo
// deployment, which is not something these tools can act on.
func AddTempoTools(s *server.MCPServer, enableQueryTools bool) {
	if !enableQueryTools {
		return
	}
	SearchTempoTracesTool.Register(s)
	QueryTempoMetricsTool.Register(s)
	GetTempoTraceTool.Register(s)
	DiffTempoTracesTool.Register(s)
	ListTempoAttributeNamesTool.Register(s)
	ListTempoAttributeValuesTool.Register(s)
	GetTempoTraceQLDocsTool.Register(s)
}
