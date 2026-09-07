package tools

import (
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
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
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
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("tempo API returned %d: %s", resp.StatusCode, string(body))
	}

	return string(body), nil
}

func parseRFC3339ToEpochSeconds(value string) (string, error) {
	t, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return "", fmt.Errorf("invalid time: %v", err)
	}
	return fmt.Sprintf("%d", t.Unix()), nil
}

func parseRFC3339ToEpochNanos(value string) (string, error) {
	t, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return "", fmt.Errorf("invalid time: %v", err)
	}
	return fmt.Sprintf("%d", t.UnixNano()), nil
}

// Tool handler functions. These use the raw mcp.CallToolRequest interface
// instead of MustTool's struct-based approach because several Tempo parameters
// use hyphens (e.g. "filter-query") or enum constraints that are easier to
// express with mcp.WithString().

func tempoSearchHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	uid, err := request.RequireString("datasourceUid")
	if err != nil {
		return mcp.NewToolResultError("datasourceUid is required"), nil
	}
	query, err := request.RequireString("query")
	if err != nil {
		return mcp.NewToolResultError("query is required"), nil
	}

	backend, err := tempoBackendForDatasource(ctx, uid)
	if err != nil {
		return nil, err
	}

	params := url.Values{}
	params.Set("q", query)

	if start := request.GetString("start", ""); start != "" {
		epoch, err := parseRFC3339ToEpochSeconds(start)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("invalid start time: %v", err)), nil
		}
		params.Set("start", epoch)
	}
	if end := request.GetString("end", ""); end != "" {
		epoch, err := parseRFC3339ToEpochSeconds(end)
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

func tempoMetricsInstantHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	uid, err := request.RequireString("datasourceUid")
	if err != nil {
		return mcp.NewToolResultError("datasourceUid is required"), nil
	}
	query, err := request.RequireString("query")
	if err != nil {
		return mcp.NewToolResultError("query is required"), nil
	}

	backend, err := tempoBackendForDatasource(ctx, uid)
	if err != nil {
		return nil, err
	}

	params := url.Values{}
	params.Set("q", query)

	if start := request.GetString("start", ""); start != "" {
		epoch, err := parseRFC3339ToEpochNanos(start)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("invalid start time: %v", err)), nil
		}
		params.Set("start", epoch)
	}
	if end := request.GetString("end", ""); end != "" {
		epoch, err := parseRFC3339ToEpochNanos(end)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("invalid end time: %v", err)), nil
		}
		params.Set("end", epoch)
	}

	body, err := backend.doGet(ctx, "/api/metrics/query_range", params)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	return tempoToolResult(body, "metrics-instant", "json"), nil
}

func tempoMetricsRangeHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	uid, err := request.RequireString("datasourceUid")
	if err != nil {
		return mcp.NewToolResultError("datasourceUid is required"), nil
	}
	query, err := request.RequireString("query")
	if err != nil {
		return mcp.NewToolResultError("query is required"), nil
	}

	backend, err := tempoBackendForDatasource(ctx, uid)
	if err != nil {
		return nil, err
	}

	params := url.Values{}
	params.Set("q", query)

	if start := request.GetString("start", ""); start != "" {
		epoch, err := parseRFC3339ToEpochNanos(start)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("invalid start time: %v", err)), nil
		}
		params.Set("start", epoch)
	}
	if end := request.GetString("end", ""); end != "" {
		epoch, err := parseRFC3339ToEpochNanos(end)
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

func tempoGetTraceHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	uid, err := request.RequireString("datasourceUid")
	if err != nil {
		return mcp.NewToolResultError("datasourceUid is required"), nil
	}
	traceID, err := request.RequireString("trace_id")
	if err != nil {
		return mcp.NewToolResultError("trace_id is required"), nil
	}

	backend, err := tempoBackendForDatasource(ctx, uid)
	if err != nil {
		return nil, err
	}

	body, err := backend.doGet(ctx, "/api/v2/traces/"+url.PathEscape(traceID), nil)
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

func tempoTraceDiffHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	uid, err := request.RequireString("datasourceUid")
	if err != nil {
		return mcp.NewToolResultError("datasourceUid is required"), nil
	}
	baseTraceID, err := request.RequireString("base_trace_id")
	if err != nil {
		return mcp.NewToolResultError("base_trace_id is required"), nil
	}
	compareTraceID, err := request.RequireString("compare_trace_id")
	if err != nil {
		return mcp.NewToolResultError("compare_trace_id is required"), nil
	}

	format := request.GetString("format", "trace-summary-v0-composed")

	baseStart, baseEnd, err := parseTraceDiffTimeRange(request, "base_start", "base_end")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	compareStart, compareEnd, err := parseTraceDiffTimeRange(request, "compare_start", "compare_end")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	backend, err := tempoBackendForDatasource(ctx, uid)
	if err != nil {
		return nil, err
	}

	diffReq := traceDiffAPIRequest{
		Base: traceDiffTraceRequest{
			TraceID: baseTraceID,
			Start:   baseStart,
			End:     baseEnd,
		},
		Compare: traceDiffTraceRequest{
			TraceID: compareTraceID,
			Start:   compareStart,
			End:     compareEnd,
		},
		Format: format,
	}

	body, err := backend.doPost(ctx, "/api/v2/trace-diff", diffReq)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	return tempoToolResult(body, "trace-diff", "json"), nil
}

func parseTraceDiffTimeRange(request mcp.CallToolRequest, startName, endName string) (*int64, *int64, error) {
	args := request.GetArguments()
	_, hasStart := args[startName]
	_, hasEnd := args[endName]
	if hasStart != hasEnd {
		return nil, nil, fmt.Errorf("arguments %q and %q must be provided together", startName, endName)
	}
	if !hasStart {
		return nil, nil, nil
	}

	startValue, err := request.RequireString(startName)
	if err != nil {
		return nil, nil, err
	}
	endValue, err := request.RequireString(endName)
	if err != nil {
		return nil, nil, err
	}

	startTS, err := time.Parse(time.RFC3339, startValue)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid %s: %w", startName, err)
	}
	endTS, err := time.Parse(time.RFC3339, endValue)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid %s: %w", endName, err)
	}

	startEpoch := startTS.Unix()
	endEpoch := endTS.Unix()
	return &startEpoch, &endEpoch, nil
}

func tempoGetAttributeNamesHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	uid, err := request.RequireString("datasourceUid")
	if err != nil {
		return mcp.NewToolResultError("datasourceUid is required"), nil
	}

	backend, err := tempoBackendForDatasource(ctx, uid)
	if err != nil {
		return nil, err
	}

	params := url.Values{}
	if scope := request.GetString("scope", ""); scope != "" {
		params.Set("scope", scope)
	}

	body, err := backend.doGet(ctx, "/api/v2/search/tags", params)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	return tempoToolResult(body, "attribute-names", "json"), nil
}

func tempoGetAttributeValuesHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	uid, err := request.RequireString("datasourceUid")
	if err != nil {
		return mcp.NewToolResultError("datasourceUid is required"), nil
	}
	name, err := request.RequireString("name")
	if err != nil {
		return mcp.NewToolResultError("name is required"), nil
	}

	backend, err := tempoBackendForDatasource(ctx, uid)
	if err != nil {
		return nil, err
	}

	params := url.Values{}
	if filterQuery := request.GetString("filter-query", ""); filterQuery != "" {
		params.Set("q", filterQuery)
	}

	body, err := backend.doGet(ctx, "/api/v2/search/tag/"+url.PathEscape(name)+"/values", params)
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

func newTempoReadOnlyTool(name string, opts ...mcp.ToolOption) mcp.Tool {
	readOnlyOpts := []mcp.ToolOption{
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
		mcp.WithOpenWorldHintAnnotation(false),
	}
	opts = append(opts, readOnlyOpts...)
	return mcp.NewTool(name, opts...)
}

// AddTempoTools registers all Tempo tools on the MCP server. Tools call
// Tempo's REST API through the Grafana datasource proxy, replacing the
// MCP-over-MCP proxy layer.
//
// Doc tools (docs-traceql, docs-config) are not included here because they
// serve embedded markdown content that lives inside the Tempo binary. They
// can be added once Tempo publishes a shared tools library.
func AddTempoTools(s *server.MCPServer) {
	dsUidParam := mcp.WithString("datasourceUid", mcp.Required(), mcp.Description("UID of the tempo datasource to query"))

	s.AddTool(
		newTempoReadOnlyTool("tempo_traceql-search",
			mcp.WithDescription("Search for traces using TraceQL queries"),
			dsUidParam,
			mcp.WithString("query", mcp.Required(), mcp.Description("TraceQL query string")),
			mcp.WithString("start", mcp.Description("Start time for the search (RFC3339 format). If not provided will search the past 1 hour. If provided, must be before end.")),
			mcp.WithString("end", mcp.Description("End time for the search (RFC3339 format). If not provided will search the past 1 hour. If provided, must be after start.")),
		),
		tempoSearchHandler,
	)

	s.AddTool(
		newTempoReadOnlyTool("tempo_traceql-metrics-instant",
			mcp.WithDescription("Retrieve a single metric value given a TraceQL metrics query. The value is at the current instant or end. Most metrics questions can be answered with instant values."),
			dsUidParam,
			mcp.WithString("query", mcp.Required(), mcp.Description("TraceQL query string.")),
			mcp.WithString("start", mcp.Description("Start time for the search (RFC3339 format). If not provided will search the past 1 hour. If provided, must be before end.")),
			mcp.WithString("end", mcp.Description("End time for the search (RFC3339 format). If not provided will search the past 1 hour. If provided, must be after start.")),
		),
		tempoMetricsInstantHandler,
	)

	s.AddTool(
		newTempoReadOnlyTool("tempo_traceql-metrics-range",
			mcp.WithDescription("Retrieve a metric series given a TraceQL metrics query. The series ranges from start to end."),
			dsUidParam,
			mcp.WithString("query", mcp.Required(), mcp.Description("TraceQL metrics query string.")),
			mcp.WithString("start", mcp.Description("Start time for the search (RFC3339 format). If not provided will search the past 1 hour. If provided, must be before end.")),
			mcp.WithString("end", mcp.Description("End time for the search (RFC3339 format). If not provided will search the past 1 hour. If provided, must be after start.")),
		),
		tempoMetricsRangeHandler,
	)

	s.AddTool(
		newTempoReadOnlyTool("tempo_get-trace",
			mcp.WithDescription("Retrieve a specific trace by ID"),
			dsUidParam,
			mcp.WithString("trace_id", mcp.Required(), mcp.Description("Trace ID to retrieve")),
		),
		tempoGetTraceHandler,
	)

	s.AddTool(
		newTempoReadOnlyTool("tempo_trace-diff",
			mcp.WithDescription("Compare two complete traces. Returns a compact summary and includes the full span-level patch when it is at most 64 KiB. Request trace-patch-v0 only when full details are required; full patches are not size-bounded."),
			dsUidParam,
			mcp.WithString("base_trace_id", mcp.Required(), mcp.Description("Trace ID for the baseline trace")),
			mcp.WithString("compare_trace_id", mcp.Required(), mcp.Description("Trace ID for the comparison trace")),
			mcp.WithString("base_start", mcp.Description("Optional start of the baseline trace search range in RFC3339 format. Must be provided with base_end."), mcp.MinLength(1)),
			mcp.WithString("base_end", mcp.Description("Optional end of the baseline trace search range in RFC3339 format. Must be provided with base_start."), mcp.MinLength(1)),
			mcp.WithString("compare_start", mcp.Description("Optional start of the comparison trace search range in RFC3339 format. Must be provided with compare_end."), mcp.MinLength(1)),
			mcp.WithString("compare_end", mcp.Description("Optional end of the comparison trace search range in RFC3339 format. Must be provided with compare_start."), mcp.MinLength(1)),
			mcp.WithString("format",
				mcp.Description("Output format. The composed format returns a compact summary and attaches the full patch only when it is at most 64 KiB. trace-patch-v0 has no output-size guarantee."),
				mcp.Enum("trace-summary-v0-composed", "trace-summary-v0-native", "trace-patch-v0"),
				mcp.DefaultString("trace-summary-v0-composed"),
				mcp.MinLength(1),
			),
		),
		tempoTraceDiffHandler,
	)

	s.AddTool(
		newTempoReadOnlyTool("tempo_get-attribute-names",
			mcp.WithDescription("Get a list of available attribute names that can be used in TraceQL queries. This is useful for finding the names of attributes that can be used in a query."),
			dsUidParam,
			mcp.WithString("scope", mcp.Description("Optional scope to filter attributes by (span, resource, event, link, instrumentation). If not provided, returns all attributes.")),
		),
		tempoGetAttributeNamesHandler,
	)

	s.AddTool(
		newTempoReadOnlyTool("tempo_get-attribute-values",
			mcp.WithDescription("Get a list of values for a fully scoped attribute name. This is useful for finding the values of a specific attribute. i.e. you can find all the services in the data by asking for resource.service.name"),
			dsUidParam,
			mcp.WithString("name", mcp.Required(), mcp.Description("The attribute name to get values for (e.g. 'span.http.method', 'resource.service.name')")),
			mcp.WithString("filter-query", mcp.Description("Filter query to apply to the attribute values. It can only have one spanset and only &&'ed conditions like { <cond> && <cond> && ... }.This is useful for filtering the values to a specific set of values. i.e. you can find all endpoints for a given service by asking for span.http.endpoint and filtering resource.service.name.")),
		),
		tempoGetAttributeValuesHandler,
	)
}
