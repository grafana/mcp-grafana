package tools

import (
	"bufio"
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
	mcpgrafana "github.com/grafana/mcp-grafana"
)

// victoriaLogsDatasourceType identifies the VictoriaLogs Grafana plugin.
const victoriaLogsDatasourceType = "victoriametrics-logs-datasource"

// victoriaLogsAllQuery is the LogsQL "match everything" expression. Used for
// label/field discovery when the caller did not narrow the search.
const victoriaLogsAllQuery = "*"

// victoriaLogsBackend implements lokiBackend by talking to a VictoriaLogs
// datasource through Grafana's datasource resource proxy. VictoriaLogs
// exposes the LogsQL HTTP API at /select/logsql/* — the shapes are
// documented at https://docs.victoriametrics.com/victorialogs/querying/.
type victoriaLogsBackend struct {
	httpClient *http.Client
	baseURL    string
}

// newVictoriaLogsBackend constructs the backend. The ds argument is
// currently unused but accepted to mirror the prom_backend constructor
// signature and to leave room for per-datasource configuration without
// churning callers later.
func newVictoriaLogsBackend(ctx context.Context, uid string, _ *models.DataSource) (*victoriaLogsBackend, error) {
	cfg := mcpgrafana.GrafanaConfigFromContext(ctx)
	resourcesBase, proxyBase := datasourceProxyPaths(uid)
	baseURL := trimTrailingSlash(cfg.URL) + proxyBase

	transport, err := mcpgrafana.BuildTransport(&cfg, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create custom transport: %w", err)
	}

	// Mirror the native Loki client: try /proxy first and fall back to
	// /resources on 403/500 for compatibility with managed Grafana
	// deployments. See fallback_transport.go for the rationale.
	rt := newDatasourceFallbackTransport(transport, proxyBase, resourcesBase)

	return &victoriaLogsBackend{
		httpClient: &http.Client{Transport: rt, Timeout: 30 * time.Second},
		baseURL:    baseURL,
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
// other shapes — we normalize on RFC3339 because every caller already
// supplies (or defaults to) RFC3339.
func addVLTimeRange(params url.Values, start, end time.Time) {
	if !start.IsZero() {
		params.Set("start", start.Format(time.RFC3339))
	}
	if !end.IsZero() {
		params.Set("end", end.Format(time.RFC3339))
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

func (b *victoriaLogsBackend) ListLabelNames(ctx context.Context, start, end time.Time) ([]string, error) {
	params := url.Values{}
	params.Set("query", victoriaLogsAllQuery)
	addVLTimeRange(params, start, end)
	return b.listValueHits(ctx, "/select/logsql/field_names", params)
}

func (b *victoriaLogsBackend) ListLabelValues(ctx context.Context, labelName string, start, end time.Time) ([]string, error) {
	params := url.Values{}
	params.Set("query", victoriaLogsAllQuery)
	params.Set("field", labelName)
	addVLTimeRange(params, start, end)
	return b.listValueHits(ctx, "/select/logsql/field_values", params)
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

	params := url.Values{}
	params.Set("query", p.Query)
	addVLTimeRange(params, start, end)
	if p.Limit > 0 {
		params.Set("limit", strconv.Itoa(p.Limit))
	}

	body, err := b.doRequest(ctx, http.MethodPost, "/select/logsql/query", params)
	if err != nil {
		return nil, err
	}

	entries, err := parseVictoriaLogsNDJSON(body)
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

// parseVictoriaLogsNDJSON converts the newline-delimited JSON returned by
// /select/logsql/query into a slice of LogEntry. Each line is one record;
// the special fields _time, _msg, and _stream map onto Loki's timestamp/
// line/labels respectively, while everything else lands in Parsed so the
// agent still sees parser-extracted fields.
func parseVictoriaLogsNDJSON(body []byte) ([]LogEntry, error) {
	entries := []LogEntry{}
	if len(body) == 0 {
		return entries, nil
	}

	scanner := bufio.NewScanner(bytes.NewReader(body))
	// Allow long log lines; the default 64 KiB buffer is too small.
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)

	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}

		var record map[string]json.RawMessage
		if err := json.Unmarshal(line, &record); err != nil {
			// Skip malformed lines — VL occasionally emits trailing
			// metadata that isn't a log record.
			continue
		}

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
		entries = append(entries, entry)
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("reading VictoriaLogs response: %w", err)
	}
	return entries, nil
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
