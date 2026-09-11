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
		return "", fmt.Errorf("tempo API returned %d: %s", resp.StatusCode, string(body))
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
		return "", fmt.Errorf("tempo API returned %d: %s", resp.StatusCode, string(body))
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
		return "", fmt.Errorf("tempo API returned %d: %s", resp.StatusCode, string(body))
	}

	return string(body), nil
}

func tempoParseToEpochSeconds(value string) (string, error) {
	t, err := parseStartTime(value)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%d", t.Unix()), nil
}

func tempoParseToEpochNanos(value string) (string, error) {
	t, err := parseStartTime(value)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%d", t.UnixNano()), nil
}

// Parameter structs for Tempo tools.

type TempoSearchParams struct {
	DatasourceUID string `json:"datasourceUid" jsonschema:"required,description=UID of the tempo datasource to query"`
	Query         string `json:"query" jsonschema:"required,description=TraceQL query string"`
	Start         string `json:"start,omitempty" jsonschema:"description=Start time for the search (RFC3339 format). If not provided will search the past 1 hour. If provided\\, must be before end."`
	End           string `json:"end,omitempty" jsonschema:"description=End time for the search (RFC3339 format). If not provided will search the past 1 hour. If provided\\, must be after start."`
}

type TempoMetricsInstantParams struct {
	DatasourceUID string `json:"datasourceUid" jsonschema:"required,description=UID of the tempo datasource to query"`
	Query         string `json:"query" jsonschema:"required,description=TraceQL query string."`
	Start         string `json:"start,omitempty" jsonschema:"description=Start time for the search (RFC3339 format). If not provided will search the past 1 hour. If provided\\, must be before end."`
	End           string `json:"end,omitempty" jsonschema:"description=End time for the search (RFC3339 format). If not provided will search the past 1 hour. If provided\\, must be after start."`
}

type TempoMetricsRangeParams struct {
	DatasourceUID string `json:"datasourceUid" jsonschema:"required,description=UID of the tempo datasource to query"`
	Query         string `json:"query" jsonschema:"required,description=TraceQL metrics query string."`
	Start         string `json:"start,omitempty" jsonschema:"description=Start time for the search (RFC3339 format). If not provided will search the past 1 hour. If provided\\, must be before end."`
	End           string `json:"end,omitempty" jsonschema:"description=End time for the search (RFC3339 format). If not provided will search the past 1 hour. If provided\\, must be after start."`
}

type TempoGetTraceParams struct {
	DatasourceUID string `json:"datasourceUid" jsonschema:"required,description=UID of the tempo datasource to query"`
	TraceID       string `json:"trace_id" jsonschema:"required,description=Trace ID to retrieve"`
}

type TempoTraceDiffParams struct {
	DatasourceUID  string `json:"datasourceUid" jsonschema:"required,description=UID of the tempo datasource to query"`
	BaseTraceID    string `json:"base_trace_id" jsonschema:"required,description=Trace ID for the baseline trace"`
	CompareTraceID string `json:"compare_trace_id" jsonschema:"required,description=Trace ID for the comparison trace"`
	BaseStart      string `json:"base_start,omitempty" jsonschema:"description=Optional start of the baseline trace search range in RFC3339 format. Must be provided with base_end."`
	BaseEnd        string `json:"base_end,omitempty" jsonschema:"description=Optional end of the baseline trace search range in RFC3339 format. Must be provided with base_start."`
	CompareStart   string `json:"compare_start,omitempty" jsonschema:"description=Optional start of the comparison trace search range in RFC3339 format. Must be provided with compare_end."`
	CompareEnd     string `json:"compare_end,omitempty" jsonschema:"description=Optional end of the comparison trace search range in RFC3339 format. Must be provided with compare_start."`
	Format         string `json:"format,omitempty" jsonschema:"enum=trace-summary-v0-composed,enum=trace-summary-v0-native,enum=trace-patch-v0,default=trace-summary-v0-composed,description=Output format. The composed format returns a compact summary and attaches the full patch only when it is at most 64 KiB. trace-patch-v0 has no output-size guarantee."`
}

type TempoGetAttributeNamesParams struct {
	DatasourceUID string `json:"datasourceUid" jsonschema:"required,description=UID of the tempo datasource to query"`
	Scope         string `json:"scope,omitempty" jsonschema:"description=Optional scope to filter attributes by (span\\, resource\\, event\\, link\\, instrumentation). If not provided\\, returns all attributes."`
}

type TempoGetAttributeValuesParams struct {
	DatasourceUID string `json:"datasourceUid" jsonschema:"required,description=UID of the tempo datasource to query"`
	Name          string `json:"name" jsonschema:"required,description=The attribute name to get values for (e.g. 'span.http.method'\\, 'resource.service.name')"`
	FilterQuery   string `json:"filter-query,omitempty" jsonschema:"description=Filter query to apply to the attribute values. It can only have one spanset and only &&'ed conditions like { <cond> && <cond> && ... }. This is useful for filtering the values to a specific set of values."`
}

// Handler functions.

func tempoSearch(ctx context.Context, args TempoSearchParams) (*mcp.CallToolResult, error) {
	backend, err := tempoBackendForDatasource(ctx, args.DatasourceUID)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	params := url.Values{}
	params.Set("q", args.Query)

	if args.Start != "" {
		epoch, err := tempoParseToEpochSeconds(args.Start)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("invalid start time: %v", err)), nil
		}
		params.Set("start", epoch)
	}
	if args.End != "" {
		epoch, err := tempoParseToEpochSeconds(args.End)
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

func tempoMetricsInstant(ctx context.Context, args TempoMetricsInstantParams) (*mcp.CallToolResult, error) {
	backend, err := tempoBackendForDatasource(ctx, args.DatasourceUID)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	params := url.Values{}
	params.Set("q", args.Query)

	if args.Start != "" {
		epoch, err := tempoParseToEpochNanos(args.Start)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("invalid start time: %v", err)), nil
		}
		params.Set("start", epoch)
	}
	if args.End != "" {
		epoch, err := tempoParseToEpochNanos(args.End)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("invalid end time: %v", err)), nil
		}
		params.Set("end", epoch)
	}

	body, err := backend.doGet(ctx, "/api/metrics/query", params)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	return tempoToolResult(body, "metrics-instant", "json"), nil
}

func tempoMetricsRange(ctx context.Context, args TempoMetricsRangeParams) (*mcp.CallToolResult, error) {
	backend, err := tempoBackendForDatasource(ctx, args.DatasourceUID)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	params := url.Values{}
	params.Set("q", args.Query)

	if args.Start != "" {
		epoch, err := tempoParseToEpochNanos(args.Start)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("invalid start time: %v", err)), nil
		}
		params.Set("start", epoch)
	}
	if args.End != "" {
		epoch, err := tempoParseToEpochNanos(args.End)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("invalid end time: %v", err)), nil
		}
		params.Set("end", epoch)
	}

	body, err := backend.doGet(ctx, "/api/metrics/query_range", params)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	return tempoToolResult(body, "metrics-range", "json"), nil
}

func tempoGetTrace(ctx context.Context, args TempoGetTraceParams) (*mcp.CallToolResult, error) {
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

func tempoTraceDiff(ctx context.Context, args TempoTraceDiffParams) (*mcp.CallToolResult, error) {
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
	endTS, err := parseStartTime(endStr)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid %s: %w", endName, err)
	}

	startNanos := startTS.UnixNano()
	endNanos := endTS.UnixNano()
	return &startNanos, &endNanos, nil
}

func tempoGetAttributeNames(ctx context.Context, args TempoGetAttributeNamesParams) (*mcp.CallToolResult, error) {
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

	return tempoToolResult(body, "attribute-names", "json"), nil
}

func tempoGetAttributeValues(ctx context.Context, args TempoGetAttributeValuesParams) (*mcp.CallToolResult, error) {
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

func tempoToolResult(body string, contentType string, encoding string) *mcp.CallToolResult {
	res := mcp.NewToolResultText(body)
	res.Meta = &mcp.Meta{AdditionalFields: map[string]any{
		"type":     contentType,
		"encoding": encoding,
	}}
	return res
}

// Tool definitions.

var TempoSearchTool = mcpgrafana.MustTool(
	"tempo_traceql-search",
	"Search for traces using TraceQL queries",
	tempoSearch,
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
	mcp.WithDestructiveHintAnnotation(false),
	mcp.WithOpenWorldHintAnnotation(false),
)

var TempoMetricsInstantTool = mcpgrafana.MustTool(
	"tempo_traceql-metrics-instant",
	"Retrieve a single metric value given a TraceQL metrics query. The value is at the current instant or end. Most metrics questions can be answered with instant values.",
	tempoMetricsInstant,
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
	mcp.WithDestructiveHintAnnotation(false),
	mcp.WithOpenWorldHintAnnotation(false),
)

var TempoMetricsRangeTool = mcpgrafana.MustTool(
	"tempo_traceql-metrics-range",
	"Retrieve a metric series given a TraceQL metrics query. The series ranges from start to end.",
	tempoMetricsRange,
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
	mcp.WithDestructiveHintAnnotation(false),
	mcp.WithOpenWorldHintAnnotation(false),
)

var TempoGetTraceTool = mcpgrafana.MustTool(
	"tempo_get-trace",
	"Retrieve a specific trace by ID",
	tempoGetTrace,
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
	mcp.WithDestructiveHintAnnotation(false),
	mcp.WithOpenWorldHintAnnotation(false),
)

var TempoTraceDiffTool = mcpgrafana.MustTool(
	"tempo_trace-diff",
	"Compare two complete traces. Returns a compact summary and includes the full span-level patch when it is at most 64 KiB. Request trace-patch-v0 only when full details are required; full patches are not size-bounded.",
	tempoTraceDiff,
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
	mcp.WithDestructiveHintAnnotation(false),
	mcp.WithOpenWorldHintAnnotation(false),
)

var TempoGetAttributeNamesTool = mcpgrafana.MustTool(
	"tempo_get-attribute-names",
	"Get a list of available attribute names that can be used in TraceQL queries. This is useful for finding the names of attributes that can be used in a query.",
	tempoGetAttributeNames,
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
	mcp.WithDestructiveHintAnnotation(false),
	mcp.WithOpenWorldHintAnnotation(false),
)

var TempoGetAttributeValuesTool = mcpgrafana.MustTool(
	"tempo_get-attribute-values",
	"Get a list of values for a fully scoped attribute name. This is useful for finding the values of a specific attribute. i.e. you can find all the services in the data by asking for resource.service.name",
	tempoGetAttributeValues,
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
	mcp.WithDestructiveHintAnnotation(false),
	mcp.WithOpenWorldHintAnnotation(false),
)

// AddTempoTools registers all Tempo tools on the MCP server. Tools call
// Tempo's REST API through the Grafana datasource proxy.
//
// Doc tools (docs-traceql, docs-config) are not included here because they
// serve embedded markdown content that lives inside the Tempo binary. They
// can be added once Tempo publishes a shared tools library.
func AddTempoTools(s *server.MCPServer, enableQueryTools bool) {
	if !enableQueryTools {
		return
	}
	TempoSearchTool.Register(s)
	TempoMetricsInstantTool.Register(s)
	TempoMetricsRangeTool.Register(s)
	TempoGetTraceTool.Register(s)
	TempoTraceDiffTool.Register(s)
	TempoGetAttributeNamesTool.Register(s)
	TempoGetAttributeValuesTool.Register(s)
}
