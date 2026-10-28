package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/grafana/grafana-openapi-client-go/models"
	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"github.com/grafana/grafana-plugin-sdk-go/data"
)

// lokiBackend abstracts the differences between datasource types that serve
// log data through Loki-compatible tools (native Loki, VictoriaLogs, etc.).
//
// The tool entry points in loki.go normalize user-provided parameters
// (defaults, limits, direction) and delegate the actual datasource query to
// an implementation of this interface. Each backend is responsible for
// translating the request to its native API and parsing the response back
// into the shared LogEntry shape.
type lokiBackend interface {
	// ListLabelNames returns the available label/field names within the
	// given time range. Empty start/end means "use server defaults". An
	// empty matcher means "no filter"; a non-empty matcher narrows the
	// search to streams selected by it (Loki: a LogQL stream selector,
	// e.g. `{namespace="prod"}`; VictoriaLogs: a LogsQL filter).
	ListLabelNames(ctx context.Context, matcher string, start, end time.Time) ([]string, error)

	// ListLabelValues returns the values for a single label/field within
	// the given time range, optionally narrowed to streams selected by
	// matcher (see ListLabelNames).
	ListLabelValues(ctx context.Context, labelName, matcher string, start, end time.Time) ([]string, error)

	// QueryLogs runs a log/metric query and returns the raw entries plus
	// the result type ("streams" | "vector" | "matrix") and (when known)
	// the total number of lines scanned by the backend.
	QueryLogs(ctx context.Context, p lokiQueryParams) (*lokiQueryResult, error)

	// QueryStats returns counts about the streams matching a selector.
	// Backends without an exact equivalent (e.g. VictoriaLogs) populate
	// only the fields they can produce and leave the rest as zero.
	QueryStats(ctx context.Context, query string, start, end time.Time) (*Stats, error)

	// QueryPatterns returns detected log patterns. Backends that do not
	// implement pattern detection should return a clear error.
	QueryPatterns(ctx context.Context, query, step string, start, end time.Time) ([]Pattern, error)
}

// lokiQueryParams is the normalized request shape passed to lokiBackend.QueryLogs.
type lokiQueryParams struct {
	Query       string
	QueryType   string // "instant" or "range"
	Start, End  time.Time
	Limit       int    // already clamped to the configured max
	Direction   string // "forward" or "backward"
	StepSeconds int    // for range metric queries
}

// lokiQueryResult is the backend's contribution to QueryLokiLogsResult.
// The tool wrapper layers truncation handling, metadata, and hints on top.
type lokiQueryResult struct {
	Entries           []LogEntry
	ResultType        string // "streams", "vector", "matrix"
	TotalLinesScanned *int   // nil when the backend doesn't expose stats
}

// lokiBackendForDatasource resolves a UID to the appropriate backend by
// inspecting the datasource type. Native Loki is the default; VictoriaLogs
// is selected when the type matches the victoriametrics-logs-datasource
// plugin. The looked-up DataSource is forwarded to the constructors so
// they don't have to re-fetch it (mirroring backendForDatasource in
// prom_backend.go).
func lokiBackendForDatasource(ctx context.Context, uid string) (lokiBackend, error) {
	ds, err := getDatasourceByUID(ctx, GetDatasourceByUIDParams{UID: uid})
	if err != nil {
		return nil, err
	}

	switch ds.Type {
	case victoriaLogsDatasourceType:
		// Enforced label matchers are expressed in LogQL and can only be
		// injected into native-Loki queries. VictoriaLogs uses LogsQL, which
		// we cannot safely rewrite, so fail closed rather than serve an
		// unfiltered path around the enforcement policy.
		if len(enforcedMatchers(ctx)) > 0 {
			return nil, fmt.Errorf("loki label-matcher enforcement is enabled but datasource %q is VictoriaLogs (LogsQL); refusing to query it because the enforced matchers cannot be applied", uid)
		}
		return newVictoriaLogsBackend(ctx, uid, ds)
	default:
		return newLokiNativeBackend(ctx, uid, ds)
	}
}

// lokiNativeBackend runs log and metric queries through Grafana's
// /api/ds/query endpoint, so they go through the query service and show up
// in its query log. Label, stats and patterns calls use the Loki HTTP client
// via the datasource proxy/resources routes.
type lokiNativeBackend struct {
	client *Client

	httpClient *http.Client
	baseURL    string
	dsUID      string
	dsType     string
	// legacy is set on Grafana before 9.0, whose /api/ds/query does not
	// accept datasource UIDs; queries there stay on the proxy.
	legacy bool
}

func newLokiNativeBackend(ctx context.Context, uid string, ds *models.DataSource) (*lokiNativeBackend, error) {
	c, err := newLokiClient(ctx, uid, ds)
	if err != nil {
		return nil, err
	}
	httpClient, baseURL, err := newDSQueryHTTPClient(ctx)
	if err != nil {
		return nil, err
	}
	dsUID := ds.UID
	if dsUID == "" {
		dsUID = uid
	}
	_, _, legacy := fallbackProxyBases(ctx, uid)
	return &lokiNativeBackend{
		client:     c,
		httpClient: httpClient,
		baseURL:    baseURL,
		dsUID:      dsUID,
		dsType:     ds.Type,
		legacy:     legacy,
	}, nil
}

func formatRFC3339OrEmpty(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format(time.RFC3339Nano)
}

func (b *lokiNativeBackend) ListLabelNames(ctx context.Context, matcher string, start, end time.Time) ([]string, error) {
	query, err := labelEnumerationQuery(ctx, matcher)
	if err != nil {
		return nil, err
	}
	return b.client.fetchData(ctx, "/loki/api/v1/labels", query, formatRFC3339OrEmpty(start), formatRFC3339OrEmpty(end))
}

func (b *lokiNativeBackend) ListLabelValues(ctx context.Context, labelName, matcher string, start, end time.Time) ([]string, error) {
	urlPath := fmt.Sprintf("/loki/api/v1/label/%s/values", labelName)
	query, err := labelEnumerationQuery(ctx, matcher)
	if err != nil {
		return nil, err
	}
	return b.client.fetchData(ctx, urlPath, query, formatRFC3339OrEmpty(start), formatRFC3339OrEmpty(end))
}

func (b *lokiNativeBackend) QueryLogs(ctx context.Context, p lokiQueryParams) (*lokiQueryResult, error) {
	enforcedQuery, err := enforceLogQL(ctx, p.Query)
	if err != nil {
		return nil, err
	}
	p.Query = enforcedQuery

	var result *lokiQueryResult
	if b.legacy {
		result, err = b.queryLogsViaProxy(ctx, p)
	} else {
		result, err = b.queryLogsViaDSQuery(ctx, p)
	}
	if err != nil {
		return nil, err
	}

	// Loki groups results by stream, so flattening does not preserve global time order.
	// Sort before queryLokiLogs trims the page to the requested limit.
	if result.ResultType == "streams" {
		if err := sortLogEntries(result.Entries, p.Direction); err != nil {
			return nil, err
		}
	}
	return result, nil
}

// queryLogsViaDSQuery runs the query through Grafana's /api/ds/query endpoint.
func (b *lokiNativeBackend) queryLogsViaDSQuery(ctx context.Context, p lokiQueryParams) (*lokiQueryResult, error) {
	start, end := p.Start, p.End
	if p.QueryType == "instant" {
		// Like the proxy path, evaluate at end if given, else start, else now.
		if end.IsZero() {
			end = start
		}
		if end.IsZero() {
			end = time.Now()
		}
		start = end
	} else {
		if end.IsZero() {
			end = time.Now()
		}
		if start.IsZero() {
			start = end
		}
	}

	datasource := map[string]string{"uid": b.dsUID}
	if b.dsType != "" {
		datasource["type"] = b.dsType
	}
	queryType := p.QueryType
	if queryType != "instant" {
		queryType = "range"
	}
	query := map[string]interface{}{
		"refId":      "A",
		"datasource": datasource,
		"expr":       p.Query,
		"queryType":  queryType,
		"direction":  p.Direction,
	}
	if p.StepSeconds > 0 {
		query["step"] = fmt.Sprintf("%ds", p.StepSeconds)
	}

	if queryType == "instant" {
		resp, err := doDSQuery(ctx, b.httpClient, b.baseURL, dsQueryPayload(start, ceilMillis(end), query))
		if err != nil {
			return nil, fmt.Errorf("querying Loki: %w", err)
		}
		return lokiFramesToResult(resp, queryType)
	}

	// /api/ds/query only takes millisecond bounds, but callers page through
	// logs with exact nanosecond timestamps. Widen the range to whole
	// milliseconds (Loki ranges are [start, end)) and trim the extra lines.
	// Loki applies maxLines to the widened range, so lines in the extra
	// millisecond at the leading edge can use up the page; when that happens,
	// ask for more lines until the page is full or Loki runs out.
	to := ceilMillis(end)
	maxLines := p.Limit
	for attempt := 0; ; attempt++ {
		query["maxLines"] = maxLines
		resp, err := doDSQuery(ctx, b.httpClient, b.baseURL, dsQueryPayload(start, to, query))
		if err != nil {
			return nil, fmt.Errorf("querying Loki: %w", err)
		}
		result, err := lokiFramesToResult(resp, queryType)
		if err != nil || result.ResultType != "streams" {
			return result, err
		}
		returned := len(result.Entries)
		kept := result.Entries[:0]
		for _, e := range result.Entries {
			ts, err := strconv.ParseInt(e.Timestamp, 10, 64)
			if err != nil {
				return nil, fmt.Errorf("invalid Loki stream timestamp %q: %w", e.Timestamp, err)
			}
			if ts >= start.UnixNano() && ts < end.UnixNano() {
				kept = append(kept, e)
			}
		}
		result.Entries = kept
		// ponytail: capped retries; a sub-millisecond burst larger than
		// a few pages can still end the page early.
		if returned < maxLines || len(kept) >= p.Limit || attempt == 3 {
			return result, nil
		}
		maxLines += returned - len(kept)
	}
}

// ceilMillis rounds t up to the next whole millisecond.
func ceilMillis(t time.Time) time.Time {
	ms := t.Truncate(time.Millisecond)
	if ms.Before(t) {
		ms = ms.Add(time.Millisecond)
	}
	return ms
}

// lokiFramesToResult converts the frames Grafana's Loki datasource returns
// into LogEntry values. Log queries return one frame with labels, Time, Line,
// tsNs and labelTypes fields; metric queries return one frame per series.
func lokiFramesToResult(resp *backend.QueryDataResponse, queryType string) (*lokiQueryResult, error) {
	result := &lokiQueryResult{Entries: []LogEntry{}, ResultType: "matrix"}
	if queryType == "instant" {
		result.ResultType = "vector"
	}
	r, ok := resp.Responses["A"]
	if !ok {
		return result, nil
	}
	if r.Error != nil {
		return nil, fmt.Errorf("querying Loki: %s", r.Error.Error())
	}

	for _, frame := range r.Frames {
		if result.TotalLinesScanned == nil && frame.Meta != nil {
			for _, s := range frame.Meta.Stats {
				if s.DisplayName == "Summary: total lines processed" {
					v := int(s.Value)
					result.TotalLinesScanned = &v
				}
			}
		}

		if _, idx := frame.FieldByName("Line"); idx != -1 {
			result.ResultType = "streams"
			entries, err := lokiLogFrameEntries(frame)
			if err != nil {
				return nil, err
			}
			result.Entries = append(result.Entries, entries...)
			continue
		}

		timeIdx, valueIdx := findTimeAndValueFields(frame.Fields)
		if timeIdx == -1 || valueIdx == -1 {
			continue
		}
		labels := map[string]string(frame.Fields[valueIdx].Labels)
		if labels == nil {
			labels = map[string]string{}
		}
		var values []MetricValue
		for i := 0; i < frame.Rows(); i++ {
			ts, tsOk := toMillis(frame.At(timeIdx, i))
			val, valOk := toFloat(frame.At(valueIdx, i))
			if !tsOk || !valOk {
				continue
			}
			values = append(values, MetricValue{Timestamp: fmt.Sprintf("%.3f", float64(ts)/1000), Value: val})
		}
		if len(values) == 0 {
			continue
		}
		if queryType == "instant" {
			last := values[len(values)-1]
			result.Entries = append(result.Entries, LogEntry{Timestamp: last.Timestamp, Value: &last.Value, Labels: labels})
		} else {
			result.Entries = append(result.Entries, LogEntry{Values: values, Labels: labels})
		}
	}
	return result, nil
}

// lokiLogFrameEntries reads the rows of a Loki log frame. labelTypes marks
// each label as indexed (I), structured metadata (S) or parsed (P); without
// it (Loki before 3.0) every label is treated as a stream label.
func lokiLogFrameEntries(frame *data.Frame) ([]LogEntry, error) {
	field := func(name string) *data.Field {
		f, _ := frame.FieldByName(name)
		return f
	}
	lineField, tsField, labelsField, typesField := field("Line"), field("tsNs"), field("labels"), field("labelTypes")
	if tsField == nil {
		return nil, fmt.Errorf("loki log frame has no tsNs field")
	}

	entries := make([]LogEntry, 0, frame.Rows())
	for i := 0; i < frame.Rows(); i++ {
		entry := LogEntry{Labels: map[string]string{}}
		if v, ok := lineField.ConcreteAt(i); ok {
			entry.Line, _ = v.(string)
		}
		if v, ok := tsField.ConcreteAt(i); ok {
			entry.Timestamp, _ = v.(string)
		}

		var labels, types map[string]string
		if err := unmarshalJSONField(labelsField, i, &labels); err != nil {
			return nil, fmt.Errorf("parsing Loki labels: %w", err)
		}
		if err := unmarshalJSONField(typesField, i, &types); err != nil {
			return nil, fmt.Errorf("parsing Loki label types: %w", err)
		}
		for k, v := range labels {
			switch types[k] {
			case "S":
				if entry.StructuredMetadata == nil {
					entry.StructuredMetadata = map[string]string{}
				}
				entry.StructuredMetadata[k] = v
			case "P":
				if entry.Parsed == nil {
					entry.Parsed = map[string]string{}
				}
				entry.Parsed[k] = v
			default:
				entry.Labels[k] = v
			}
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

// unmarshalJSONField decodes row i of a json.RawMessage frame field into v.
// A missing field or null value leaves v untouched.
func unmarshalJSONField(f *data.Field, i int, v interface{}) error {
	if f == nil {
		return nil
	}
	raw, ok := f.ConcreteAt(i)
	if !ok {
		return nil
	}
	msg, ok := raw.(json.RawMessage)
	if !ok || len(msg) == 0 {
		return nil
	}
	return json.Unmarshal(msg, v)
}

// sortLogEntries orders log entries by their nanosecond timestamps in the
// requested direction.
func sortLogEntries(entries []LogEntry, direction string) error {
	type timedEntry struct {
		log       LogEntry
		timestamp int64
	}
	ordered := make([]timedEntry, len(entries))
	for i, entry := range entries {
		timestamp, err := strconv.ParseInt(entry.Timestamp, 10, 64)
		if err != nil {
			return fmt.Errorf("invalid Loki stream timestamp %q: %w", entry.Timestamp, err)
		}
		ordered[i] = timedEntry{log: entry, timestamp: timestamp}
	}
	forward := strings.EqualFold(direction, "forward")
	sort.SliceStable(ordered, func(i, j int) bool {
		if forward {
			return ordered[i].timestamp < ordered[j].timestamp
		}
		return ordered[i].timestamp > ordered[j].timestamp
	})
	for i, entry := range ordered {
		entries[i] = entry.log
	}
	return nil
}

// queryLogsViaProxy queries the Loki HTTP API through the datasource proxy.
func (b *lokiNativeBackend) queryLogsViaProxy(ctx context.Context, p lokiQueryParams) (*lokiQueryResult, error) {
	response, err := b.client.fetchQuery(ctx, fetchQueryParams{
		Query:       p.Query,
		QueryType:   p.QueryType,
		Start:       formatRFC3339OrEmpty(p.Start),
		End:         formatRFC3339OrEmpty(p.End),
		Limit:       p.Limit,
		Direction:   p.Direction,
		StepSeconds: p.StepSeconds,
	})
	if err != nil {
		return nil, err
	}

	entries, err := parseLokiQueryResponse(response)
	if err != nil {
		return nil, err
	}

	var linesScanned *int
	if response.Data.Stats != nil {
		val := response.Data.Stats.Summary.TotalLinesProcessed
		linesScanned = &val
	}

	return &lokiQueryResult{
		Entries:           entries,
		ResultType:        response.Data.ResultType,
		TotalLinesScanned: linesScanned,
	}, nil
}

func (b *lokiNativeBackend) QueryStats(ctx context.Context, query string, start, end time.Time) (*Stats, error) {
	enforcedQuery, err := enforceLogQL(ctx, query)
	if err != nil {
		return nil, err
	}
	return b.client.fetchStats(ctx, enforcedQuery, formatRFC3339OrEmpty(start), formatRFC3339OrEmpty(end))
}

func (b *lokiNativeBackend) QueryPatterns(ctx context.Context, query, step string, start, end time.Time) ([]Pattern, error) {
	enforcedQuery, err := enforceLogQL(ctx, query)
	if err != nil {
		return nil, err
	}
	return b.client.fetchPatterns(ctx, enforcedQuery, formatRFC3339OrEmpty(start), formatRFC3339OrEmpty(end), step)
}
