package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/grafana/grafana-openapi-client-go/models"
	"github.com/grafana/grafana-plugin-sdk-go/data"
	mcpgrafana "github.com/grafana/mcp-grafana/v2"
)

// victoriaLogsDatasourceType identifies the VictoriaLogs Grafana plugin.
const victoriaLogsDatasourceType = "victoriametrics-logs-datasource"

// victoriaLogsAllQuery is the LogsQL "match everything" expression. Used for
// label/field discovery when the caller did not narrow the search.
const victoriaLogsAllQuery = "*"

// victoriaLogsBackend implements lokiBackend for a VictoriaLogs datasource.
// Log queries run through Grafana's /api/ds/query endpoint, so they go
// through the query service like Grafana's own queries. Field discovery
// and stats talk to the LogsQL HTTP API at /select/logsql/* through the
// datasource proxy — the shapes are documented at
// https://docs.victoriametrics.com/victorialogs/querying/.
type victoriaLogsBackend struct {
	httpClient *http.Client
	baseURL    string

	dsQueryClient *http.Client
	dsQueryURL    string
	dsUID         string
}

func newVictoriaLogsBackend(ctx context.Context, uid string, ds *models.DataSource) (*victoriaLogsBackend, error) {
	cfg := mcpgrafana.GrafanaConfigFromContext(ctx)
	resourcesBase, proxyBase := datasourceProxyPaths(uid)
	primaryBase, fallbackBase := proxyBase, resourcesBase
	legacyMode := false
	if numericBase, uidBase, ok := fallbackProxyBases(ctx, uid); ok {
		// Legacy-compatible routing — see newPrometheusBackend for the full
		// rationale: route through the numeric-id proxy path directly, keeping
		// the uid-based proxy route as the transport-level fallback.
		primaryBase, fallbackBase = numericBase, uidBase
		legacyMode = true
	}
	baseURL := trimTrailingSlash(cfg.URL) + primaryBase

	transport, err := mcpgrafana.BuildTransport(&cfg, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create custom transport: %w", err)
	}

	// Mirror the native Loki client: try the primary base first and fall back
	// to the alternate for compatibility with different Grafana deployments.
	// See fallback_transport.go for the per-mode retry rules.
	var rt http.RoundTripper
	if legacyMode {
		rt = newLegacyDatasourceFallbackTransport(transport, primaryBase, fallbackBase)
	} else {
		rt = newDatasourceFallbackTransport(transport, primaryBase, fallbackBase)
	}

	dsQueryClient, dsQueryURL, err := newDSQueryHTTPClient(ctx)
	if err != nil {
		return nil, err
	}
	dsUID := uid
	if ds != nil && ds.UID != "" {
		dsUID = ds.UID
	}

	return &victoriaLogsBackend{
		httpClient:    &http.Client{Transport: rt, Timeout: 30 * time.Second},
		baseURL:       baseURL,
		dsQueryClient: dsQueryClient,
		dsQueryURL:    dsQueryURL,
		dsUID:         dsUID,
	}, nil
}

// vlValueHits is the VictoriaLogs response shape for /field_names and
// /field_values: {"values":[{"value":"x","hits":N}, ...]}.
type vlValueHits struct {
	Values []struct {
		Value string `json:"value"`
		Hits  int64  `json:"hits"`
	} `json:"values"`
}

// vlStatsResponse is the Prometheus-style envelope returned by /stats_query.
type vlStatsResponse struct {
	Status string `json:"status"`
	Data   struct {
		ResultType string `json:"resultType"`
		Result     []struct {
			Metric map[string]string `json:"metric"`
			Value  []json.RawMessage `json:"value"` // [ts_float, value_string]
		} `json:"result"`
	} `json:"data"`
}

// addVLTimeRange adds RFC3339 start/end to the given values when set. The
// VictoriaLogs API accepts RFC3339, RFC3339Nano, Unix seconds, and a few
// other shapes — we normalize on RFC3339Nano so sub-second bounds are kept.
func addVLTimeRange(params url.Values, start, end time.Time) {
	if !start.IsZero() {
		params.Set("start", start.Format(time.RFC3339Nano))
	}
	if !end.IsZero() {
		params.Set("end", end.Format(time.RFC3339Nano))
	}
}

// doRequest issues an HTTP request to a VictoriaLogs endpoint and returns
// the response body. POST is used for /query (which can carry large LogsQL
// expressions); everything else is GET.
func (b *victoriaLogsBackend) doRequest(ctx context.Context, method, urlPath string, params url.Values) ([]byte, error) {
	fullURL := buildURL(b.baseURL, urlPath)

	var (
		req *http.Request
		err error
	)
	switch method {
	case http.MethodPost:
		body := strings.NewReader(params.Encode())
		req, err = http.NewRequestWithContext(ctx, method, fullURL, body)
		if err == nil {
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
	default:
		u := fullURL
		if encoded := params.Encode(); encoded != "" {
			u += "?" + encoded
		}
		req, err = http.NewRequestWithContext(ctx, method, u, nil)
	}
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}

	resp, err := b.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("executing request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := readResponseBody(resp.Body, defaultResponseLimitBytes)
	if err != nil {
		return nil, fmt.Errorf("reading response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("VictoriaLogs API returned status %d: %s", resp.StatusCode, string(body))
	}
	return bytes.TrimSpace(body), nil
}

func (b *victoriaLogsBackend) listValueHits(ctx context.Context, urlPath string, params url.Values) ([]string, error) {
	body, err := b.doRequest(ctx, http.MethodGet, urlPath, params)
	if err != nil {
		return nil, err
	}
	if len(body) == 0 {
		return []string{}, nil
	}

	var parsed vlValueHits
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("unmarshalling VictoriaLogs response (content: %s): %w", string(body), err)
	}

	out := make([]string, 0, len(parsed.Values))
	for _, v := range parsed.Values {
		out = append(out, v.Value)
	}
	return out, nil
}

func (b *victoriaLogsBackend) ListLabelNames(ctx context.Context, matcher string, start, end time.Time) ([]string, error) {
	params := url.Values{}
	params.Set("query", logsQLOrAll(matcher))
	addVLTimeRange(params, start, end)
	return b.listValueHits(ctx, "/select/logsql/field_names", params)
}

func (b *victoriaLogsBackend) ListLabelValues(ctx context.Context, labelName, matcher string, start, end time.Time) ([]string, error) {
	params := url.Values{}
	params.Set("query", logsQLOrAll(matcher))
	params.Set("field", labelName)
	addVLTimeRange(params, start, end)
	return b.listValueHits(ctx, "/select/logsql/field_values", params)
}

// logsQLOrAll returns matcher if non-empty, otherwise the "match everything"
// expression, so an empty matcher still searches across all streams.
func logsQLOrAll(matcher string) string {
	if matcher == "" {
		return victoriaLogsAllQuery
	}
	return matcher
}

// QueryLogs runs a LogsQL query. Range and instant requests both go through
// /select/logsql/query; instant is implemented by anchoring the time window
// to the requested point. Metric-style queries (PromQL-compatible result
// types from /stats_query) are not exposed here — callers that want
// aggregations should phrase them as LogsQL `... | stats by (...)` and use
// the vector returned by the regular log query.
func (b *victoriaLogsBackend) QueryLogs(ctx context.Context, p lokiQueryParams) (*lokiQueryResult, error) {
	if p.QueryType != "" && p.QueryType != "range" && p.QueryType != "instant" {
		return nil, fmt.Errorf("invalid query type: %s", p.QueryType)
	}

	start, end := p.Start, p.End
	if p.QueryType == "instant" {
		// Instant ≈ "value at a point in time". VictoriaLogs has no
		// dedicated instant endpoint, so we collapse the window to the
		// chosen instant (preferring End, then Start, then now) and let
		// the limit cap the result.
		var anchor time.Time
		switch {
		case !end.IsZero():
			anchor = end
		case !start.IsZero():
			anchor = start
		default:
			anchor = time.Now()
		}
		start, end = anchor, anchor
	}

	entries, err := b.queryLogsViaDSQuery(ctx, p.Query, start, end, p.Limit)
	if err != nil {
		return nil, err
	}

	// VictoriaLogs returns log records by recency descending by default.
	// If the caller asked for forward (oldest first) order, reverse here
	// so the contract matches the Loki tool's `direction` parameter.
	if strings.EqualFold(p.Direction, "forward") {
		for i, j := 0, len(entries)-1; i < j; i, j = i+1, j-1 {
			entries[i], entries[j] = entries[j], entries[i]
		}
	}

	return &lokiQueryResult{
		Entries:    entries,
		ResultType: "streams",
	}, nil
}

// queryLogsViaDSQuery runs the query through Grafana's /api/ds/query
// endpoint using the victoriametrics-logs-datasource query model.
func (b *victoriaLogsBackend) queryLogsViaDSQuery(ctx context.Context, query string, start, end time.Time, limit int) ([]LogEntry, error) {
	if end.IsZero() {
		end = time.Now()
	}
	if start.IsZero() {
		// The proxy path sent no start, which VictoriaLogs reads as "all time".
		start = time.Unix(0, 0)
	}

	q := map[string]interface{}{
		"refId":      "A",
		"datasource": map[string]string{"uid": b.dsUID, "type": victoriaLogsDatasourceType},
		"expr":       query,
		// "instant" is the plugin's log query type (/select/logsql/query).
		"queryType": "instant",
		"maxLines":  limit,
	}

	// /api/ds/query only takes millisecond bounds, but callers page through
	// logs with exact nanosecond timestamps. Widen the range to whole
	// milliseconds (VictoriaLogs ranges are [start, end)) and trim the extra
	// lines while reading the frame. VictoriaLogs applies the limit to the
	// widened range and keeps the newest lines, so lines in the extra
	// millisecond past end can use up the page; when that happens, ask for
	// more lines until the page is full or VictoriaLogs runs out.
	to := end.Truncate(time.Millisecond)
	if to.Before(end) {
		to = to.Add(time.Millisecond)
	}
	maxLines := limit
	for attempt := 0; ; attempt++ {
		q["maxLines"] = maxLines
		resp, err := doDSQuery(ctx, b.dsQueryClient, b.dsQueryURL, dsQueryPayload(start, to, q))
		if err != nil {
			return nil, fmt.Errorf("querying VictoriaLogs: %w", err)
		}

		entries := []LogEntry{}
		r, ok := resp.Responses["A"]
		if !ok {
			return entries, nil
		}
		if r.Error != nil {
			return nil, fmt.Errorf("querying VictoriaLogs: %s", r.Error.Error())
		}
		returned, leading := 0, 0
		for _, frame := range r.Frames {
			frameEntries, frameLeading, err := vlLogFrameEntries(frame, start, end)
			if err != nil {
				return nil, err
			}
			returned += frame.Rows()
			leading += frameLeading
			entries = append(entries, frameEntries...)
		}
		// ponytail: capped retries; a sub-millisecond burst larger than a
		// few pages can still end the page early.
		if limit <= 0 || returned < maxLines || len(entries) >= limit || leading == 0 || attempt == 3 {
			if limit > 0 && len(entries) > limit {
				// Entries are newest first; keep the newest limit.
				entries = entries[:limit]
			}
			return entries, nil
		}
		// The trimmed lines past end are the newest ones VictoriaLogs kept,
		// so asking for limit plus that many fills the page exactly.
		maxLines = limit + leading
	}
}

// vlLogFrameEntries reads the rows of a VictoriaLogs log frame (Time, Line
// and labels fields) that fall within [start, end). labels holds the record's
// remaining fields. Plugin v0.30.0 and later keep the raw _stream there;
// v0.29.0 drops it and adds a hidden streams field instead; earlier releases
// flatten stream labels and other fields together, so all of them are
// treated as labels. It also returns how many rows were trimmed for falling
// at or after end.
func vlLogFrameEntries(frame *data.Frame, start, end time.Time) ([]LogEntry, int, error) {
	timeField, _ := frame.FieldByName("Time")
	lineField, _ := frame.FieldByName("Line")
	labelsField, _ := frame.FieldByName("labels")
	streamsField, _ := frame.FieldByName("streams")
	if timeField == nil || lineField == nil {
		return nil, 0, nil
	}

	entries := make([]LogEntry, 0, frame.Rows())
	leading := 0
	for i := 0; i < frame.Rows(); i++ {
		ts := fieldTime(timeField, i)
		if !ts.Before(end) {
			leading++
			continue
		}
		if ts.Before(start) {
			continue
		}

		var record map[string]json.RawMessage
		if err := unmarshalVLJSONField(labelsField, i, &record); err != nil {
			return nil, 0, fmt.Errorf("parsing VictoriaLogs labels: %w", err)
		}
		entry := vlRecordToEntry(record)
		entry.Timestamp = strconv.FormatInt(ts.UnixNano(), 10)
		entry.Line = fieldString(lineField, i)

		if _, hasStream := record["_stream"]; !hasStream {
			if streamsField != nil {
				var stream map[string]string
				if err := unmarshalVLJSONField(streamsField, i, &stream); err != nil {
					return nil, 0, fmt.Errorf("parsing VictoriaLogs streams: %w", err)
				}
				if len(stream) > 0 {
					entry.Labels = stream
				}
			} else {
				entry.Labels, entry.Parsed = entry.Parsed, nil
			}
		}
		entries = append(entries, entry)
	}
	return entries, leading, nil
}

// unmarshalVLJSONField decodes row i of a JSON frame field into v. A missing
// field or null value leaves v untouched.
// It duplicates unmarshalJSONField from the Loki /api/ds/query change (#1410).
func unmarshalVLJSONField(f *data.Field, i int, v interface{}) error {
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

// QueryStats approximates Loki's /index/stats by running the selector
// through /stats_query with a `count(*)` aggregation. VictoriaLogs does not
// expose chunk/byte counts, so those fields stay zero.
func (b *victoriaLogsBackend) QueryStats(ctx context.Context, query string, start, end time.Time) (*Stats, error) {
	statsQuery := strings.TrimSpace(query)
	if statsQuery == "" {
		statsQuery = victoriaLogsAllQuery
	}
	// stats_query requires a stats pipe at the end. If the caller already
	// supplied one we use the query as-is; otherwise append `count(*)`.
	if !strings.Contains(strings.ToLower(statsQuery), "| stats") {
		statsQuery += " | stats count(*) as entries"
	}

	params := url.Values{}
	params.Set("query", statsQuery)
	addVLTimeRange(params, start, end)

	body, err := b.doRequest(ctx, http.MethodPost, "/select/logsql/stats_query", params)
	if err != nil {
		return nil, err
	}

	var resp vlStatsResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("unmarshalling VictoriaLogs stats response (content: %s): %w", string(body), err)
	}
	if resp.Status != "" && resp.Status != "success" {
		return nil, fmt.Errorf("VictoriaLogs stats_query returned status %q: %s", resp.Status, string(body))
	}

	var entries int
	for _, r := range resp.Data.Result {
		if len(r.Value) < 2 {
			continue
		}
		// Value is [timestamp, "string-encoded number"].
		var s string
		if err := json.Unmarshal(r.Value[1], &s); err != nil {
			continue
		}
		if n, err := strconv.Atoi(s); err == nil {
			entries += n
		} else if f, err := strconv.ParseFloat(s, 64); err == nil {
			entries += int(f)
		}
	}

	return &Stats{Entries: entries}, nil
}

// QueryPatterns is not implemented: VictoriaLogs has no equivalent of
// Loki's /patterns endpoint. Callers should fall back to label/value
// exploration or a LogsQL `| stats` pipeline instead.
func (b *victoriaLogsBackend) QueryPatterns(ctx context.Context, query, step string, start, end time.Time) ([]Pattern, error) {
	return nil, fmt.Errorf("query_loki_patterns is not supported on VictoriaLogs datasources (no equivalent endpoint); use list_loki_label_values or a LogsQL '| stats' query for similar exploration")
}

// vlRecordToEntry maps one VictoriaLogs record onto a LogEntry: the special
// fields _time, _msg, and _stream map onto Loki's timestamp/line/labels
// respectively, while everything else lands in Parsed so the agent still
// sees parser-extracted fields.
func vlRecordToEntry(record map[string]json.RawMessage) LogEntry {
	entry := LogEntry{
		Labels: map[string]string{},
		Parsed: map[string]string{},
	}

	for k, raw := range record {
		var val string
		if err := json.Unmarshal(raw, &val); err != nil {
			val = string(raw) // keep non-string values verbatim
		}
		switch k {
		case "_time":
			entry.Timestamp = vlTimeToNanos(val)
		case "_msg":
			entry.Line = val
		case "_stream":
			for sk, sv := range parseVLStream(val) {
				entry.Labels[sk] = sv
			}
		default:
			entry.Parsed[k] = val
		}
	}

	if len(entry.Labels) == 0 {
		entry.Labels = nil
	}
	if len(entry.Parsed) == 0 {
		entry.Parsed = nil
	}
	return entry
}

// parseVLStream parses VictoriaLogs' _stream value, which is a Loki-shaped
// label string like `{app="foo",pod="bar"}`. Returns an empty map if the
// shape is unexpected so the caller can degrade gracefully.
func parseVLStream(s string) map[string]string {
	out := map[string]string{}
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "{") || !strings.HasSuffix(s, "}") {
		return out
	}
	inner := s[1 : len(s)-1]
	if inner == "" {
		return out
	}

	for _, kv := range splitVLLabels(inner) {
		eq := strings.Index(kv, "=")
		if eq < 0 {
			continue
		}
		key := strings.TrimSpace(kv[:eq])
		val := strings.TrimSpace(kv[eq+1:])
		if unquoted, err := strconv.Unquote(val); err == nil {
			val = unquoted
		}
		if key != "" {
			out[key] = val
		}
	}
	return out
}

// splitVLLabels splits a comma-separated label list while respecting quoted
// values (so commas inside `name="a,b"` don't break the split).
func splitVLLabels(s string) []string {
	var (
		parts   []string
		buf     strings.Builder
		inQuote bool
		escape  bool
	)
	for _, r := range s {
		switch {
		case escape:
			buf.WriteRune(r)
			escape = false
		case r == '\\':
			buf.WriteRune(r)
			escape = true
		case r == '"':
			buf.WriteRune(r)
			inQuote = !inQuote
		case r == ',' && !inQuote:
			parts = append(parts, buf.String())
			buf.Reset()
		default:
			buf.WriteRune(r)
		}
	}
	if buf.Len() > 0 {
		parts = append(parts, buf.String())
	}
	return parts
}

// vlTimeToNanos converts a VictoriaLogs RFC3339(Nano) timestamp string into
// the nanoseconds-since-epoch string used by the rest of the Loki tool
// surface. Falls back to the original value on parse failure so the caller
// still sees something useful.
func vlTimeToNanos(ts string) string {
	if ts == "" {
		return ""
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		if t, err := time.Parse(layout, ts); err == nil {
			return strconv.FormatInt(t.UnixNano(), 10)
		}
	}
	return ts
}
