package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	mcpgrafana "github.com/grafana/mcp-grafana"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

const (
	tempoAcceptLLM = "application/vnd.grafana.llm"
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

func (b *tempoBackend) doPost(ctx context.Context, path string, payload any) (string, error) {
	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("failed to marshal request body: %w", err)
	}

	u := b.baseURL + path
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, strings.NewReader(string(bodyBytes)))
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

// tempoFilterHasOR checks whether a TraceQL filter query contains an OR (||)
// operator outside of quoted strings. Tempo 2.10 silently reduces unsupported
// OR filters to empty via ExtractMatchers.
func tempoFilterHasOR(q string) bool {
	inQuote := false
	for i := 0; i < len(q); i++ {
		switch {
		case q[i] == '\\' && inQuote:
			i++ // skip escaped character
		case q[i] == '"':
			inQuote = !inQuote
		case !inQuote && i+1 < len(q) && q[i] == '|' && q[i+1] == '|':
			return true
		}
	}
	return false
}

// tempoDefaultTimeBoundsSeconds fills in default start (now-1h) and end (now)
// for any omitted time bound. Tempo 2.10 interprets omitted bounds as an
// ingester-only search, silently missing traces already flushed to backend
// storage.
func tempoDefaultTimeBoundsSeconds(params url.Values, start, end string) (url.Values, error) {
	now := time.Now()
	if start == "" {
		start = fmt.Sprintf("%d", now.Add(-time.Hour).Unix())
		params.Set("start", start)
	} else {
		epoch, err := tempoParseStartToEpochSeconds(start)
		if err != nil {
			return nil, fmt.Errorf("invalid start time: %v", err)
		}
		params.Set("start", epoch)
	}
	if end == "" {
		params.Set("end", fmt.Sprintf("%d", now.Unix()))
	} else {
		epoch, err := tempoParseEndToEpochSeconds(end)
		if err != nil {
			return nil, fmt.Errorf("invalid end time: %v", err)
		}
		params.Set("end", epoch)
	}
	return params, nil
}

// tempoDefaultTimeBoundsNanos is like tempoDefaultTimeBoundsSeconds but emits
// nanosecond-precision epoch values, as required by the metrics endpoints.
func tempoDefaultTimeBoundsNanos(params url.Values, start, end string) (url.Values, error) {
	now := time.Now()
	if start == "" {
		params.Set("start", fmt.Sprintf("%d", now.Add(-time.Hour).UnixNano()))
	} else {
		epoch, err := tempoParseStartToEpochNanos(start)
		if err != nil {
			return nil, fmt.Errorf("invalid start time: %v", err)
		}
		params.Set("start", epoch)
	}
	if end == "" {
		params.Set("end", fmt.Sprintf("%d", now.UnixNano()))
	} else {
		epoch, err := tempoParseEndToEpochNanos(end)
		if err != nil {
			return nil, fmt.Errorf("invalid end time: %v", err)
		}
		params.Set("end", epoch)
	}
	return params, nil
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
	Topic string `json:"topic" jsonschema:"required,enum=basic,enum=aggregates,enum=structural,enum=metrics,description=Which section of the TraceQL reference to retrieve: 'basic' for attribute and duration filters\\, 'aggregates' for count/avg/sum and by() grouping\\, 'structural' for parent/child and descendant operators\\, 'metrics' for rate/quantile_over_time metrics queries"`
}

// Handler functions.

func searchTempoTraces(ctx context.Context, args SearchTempoTracesParams) (*mcp.CallToolResult, error) {
	backend, err := tempoBackendForDatasource(ctx, args.DatasourceUID)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	params := url.Values{}
	params.Set("q", args.Query)

	params, err = tempoDefaultTimeBoundsSeconds(params, args.Start, args.End)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
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

	params, err = tempoDefaultTimeBoundsNanos(params, args.Start, args.End)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
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

	body, err := backend.doGet(ctx, "/api/v2/traces/"+url.PathEscape(args.TraceID), nil)
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
		if tempoFilterHasOR(args.FilterQuery) {
			return mcp.NewToolResultError("OR conditions (||) are not supported in filter queries — Tempo silently reduces them to an empty filter, returning unfiltered values. Use separate calls for each alternative instead."), nil
		}
		params.Set("q", args.FilterQuery)
	}

	body, err := backend.doGet(ctx, "/api/v2/search/tag/"+url.PathEscape(args.Name)+"/values", params)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	return tempoToolResult(body, "attribute-values", "json"), nil
}

// traceQLDocs holds the static TraceQL reference documentation, keyed by topic.
var traceQLDocs = map[string]string{
	"basic": `# TraceQL: Basic Filters

TraceQL queries select spans from traces. Every query starts with ` + "`{}`" + ` (match all spans) and adds conditions inside the braces.

## Attribute Filters
- Span attributes: ` + "`{ span.http.method = \"GET\" }`" + `
- Resource attributes: ` + "`{ resource.service.name = \"frontend\" }`" + `
- Intrinsics: ` + "`{ name = \"HTTP GET\" }`" + `, ` + "`{ duration > 100ms }`" + `, ` + "`{ status = error }`" + `, ` + "`{ kind = server }`" + `
- Nested: ` + "`{ span.http.request.header.x-custom = \"value\" }`" + `

## Operators
- Comparison: ` + "`=`" + `, ` + "`!=`" + `, ` + "`>`" + `, ` + "`>=`" + `, ` + "`<`" + `, ` + "`<=`" + `, ` + "`=~`" + ` (regex), ` + "`!~`" + `
- Logical: ` + "`&&`" + ` (AND within a spanset), ` + "`||`" + ` (OR, alternative spansets)
- String: ` + "`=~\"regex\"`" + `, ` + "`!~\"regex\"`" + `

## Combining Conditions
- AND (same span): ` + "`{ span.http.method = \"GET\" && status = error }`" + `
- OR (either match): ` + "`{ span.http.method = \"GET\" } || { span.http.method = \"POST\" }`" + `
- Pipeline: ` + "`{ status = error } | count() > 2`" + `

## Duration Filters
- ` + "`{ duration > 500ms }`" + `
- ` + "`{ duration >= 1s && duration < 5s }`" + `
- Units: ` + "`ns`" + `, ` + "`us`" + `/` + "`µs`" + `, ` + "`ms`" + `, ` + "`s`" + `, ` + "`m`" + `, ` + "`h`" + `

## Status Filters
- ` + "`{ status = ok }`" + `, ` + "`{ status = error }`" + `, ` + "`{ status = unset }`" + `
- Status message: ` + "`{ statusMessage =~ \".*timeout.*\" }`" + `

## Typed Values
- String: ` + "`\"value\"`" + ` (double quotes required)
- Integer: ` + "`200`" + `, ` + "`-1`" + `
- Float: ` + "`1.5`" + `
- Duration: ` + "`100ms`" + `, ` + "`2s`" + `
- Status: ` + "`ok`" + `, ` + "`error`" + `, ` + "`unset`" + ``,

	"aggregates": `# TraceQL: Aggregates and Grouping

Aggregate functions operate on spansets (groups of spans within a trace) and return scalar values. Use them in pipelines after a spanset selector.

## Aggregate Functions
- ` + "`count()`" + ` — number of matching spans
- ` + "`avg(field)`" + ` — average of a numeric field (e.g. ` + "`avg(duration)`" + `)
- ` + "`min(field)`" + ` — minimum value
- ` + "`max(field)`" + ` — maximum value
- ` + "`sum(field)`" + ` — sum of values

## Pipeline Syntax
Aggregates are used in pipelines with ` + "`|`" + `:
- ` + "`{ status = error } | count() > 5`" + ` — traces with more than 5 error spans
- ` + "`{ } | avg(duration) > 500ms`" + ` — traces whose spans average over 500ms
- ` + "`{ name = \"HTTP GET\" } | max(duration) > 2s`" + ` — traces with a slow GET span

## Grouping with by()
` + "`by()`" + ` groups spans before aggregation:
- ` + "`{ } | count() by(resource.service.name) > 10`" + ` — services with >10 spans
- ` + "`{ } | avg(duration) by(span.http.method)`" + ` — average duration per HTTP method

## Coalesce
` + "`coalesce()`" + ` merges sibling spansets after ` + "`by()`" + `:
- ` + "`{ } | count() by(resource.service.name) | coalesce() | count() > 3`" + `

## Select
` + "`select()`" + ` picks specific fields to include in results:
- ` + "`{ duration > 1s } | select(span.http.url, resource.service.name)`" + ``,

	"structural": `# TraceQL: Structural Operators

Structural operators query relationships between spans. The result is always the right-hand spanset.

## Child (>>)
The right-hand spans are direct children of the left-hand spans:
- ` + "`{ resource.service.name = \"frontend\" } >> { resource.service.name = \"backend\" }`" + ` — backend spans directly called by frontend

## Parent (<<)
The right-hand spans are direct parents of the left-hand spans:
- ` + "`{ resource.service.name = \"database\" } << { resource.service.name = \"backend\" }`" + ` — backend spans that are parents of database spans

## Descendant (>>)
` + "`>>`" + ` matches only direct parent-child. To match across multiple levels, use a pipeline or nest structural operators:
- ` + "`{ resource.service.name = \"gateway\" } >> { } >> { status = error }`" + ` — error spans two levels below gateway

## Sibling (~)
Match spans that share the same parent:
- ` + "`{ span.http.method = \"GET\" } ~ { span.http.method = \"POST\" }`" + ` — GET and POST siblings

## Combining with Conditions
Structural queries can include attribute filters on both sides:
- ` + "`{ resource.service.name = \"frontend\" && span.http.method = \"GET\" } >> { status = error && duration > 500ms }`" + `

## Negation with !
Negate structural operators:
- ` + "`{ resource.service.name = \"frontend\" } !>> { status = error }`" + ` — frontend spans with no direct error children
- ` + "`{ } !<< { status = error }`" + ` — spans that are not parents of error spans`,

	"metrics": `# TraceQL: Metrics Queries

TraceQL metrics queries compute aggregate time series from trace data. They use a different endpoint than search queries.

## Functions
- ` + "`rate()`" + ` — spans per second matching the selector
- ` + "`count_over_time()`" + ` — total count of matching spans per interval
- ` + "`min_over_time(field)`" + ` — minimum value per interval
- ` + "`max_over_time(field)`" + ` — maximum value per interval
- ` + "`avg_over_time(field)`" + ` — average value per interval
- ` + "`quantile_over_time(field, quantile)`" + ` — quantile value per interval (0.0-1.0)
- ` + "`histogram_over_time(field)`" + ` — histogram buckets per interval

## Basic Examples
- ` + "`{ } | rate()`" + ` — total span throughput
- ` + "`{ status = error } | rate()`" + ` — error rate
- ` + "`{ } | avg_over_time(duration)`" + ` — average span duration over time
- ` + "`{ } | quantile_over_time(duration, 0.95)`" + ` — p95 latency
- ` + "`{ resource.service.name = \"frontend\" } | count_over_time()`" + ` — frontend span count

## Grouping with by()
- ` + "`{ } | rate() by(resource.service.name)`" + ` — throughput per service
- ` + "`{ } | quantile_over_time(duration, 0.99) by(span.http.method)`" + ` — p99 per HTTP method

## Query Types
- **Range queries** (default): return time series data points, one per interval across the time range
- **Instant queries**: return a single value at the end of the time range; keep the window under 15 minutes to avoid timeouts

## Tips
- Metrics queries scan all matching spans, so narrow your selector for performance
- Use ` + "`rate()`" + ` rather than ` + "`count_over_time()`" + ` for comparing across different time ranges
- Combine with grouping for RED metrics: Rate, Errors, Duration`,
}

func getTempoTraceQLDocs(_ context.Context, args GetTempoTraceQLDocsParams) (*mcp.CallToolResult, error) {
	doc, ok := traceQLDocs[args.Topic]
	if !ok {
		return mcp.NewToolResultError(fmt.Sprintf("invalid topic %q: must be 'basic', 'aggregates', 'structural', or 'metrics'", args.Topic)), nil
	}
	return tempoToolResult(doc, "traceql-docs", "markdown"), nil
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
// Tempo's REST API through the Grafana datasource proxy. The TraceQL docs
// tool returns static reference material inlined in the binary.
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
