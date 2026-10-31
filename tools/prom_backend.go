package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/grafana/grafana-openapi-client-go/models"
	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"github.com/grafana/grafana-plugin-sdk-go/data"
	mcpgrafana "github.com/grafana/mcp-grafana/v2"
	"github.com/prometheus/client_golang/api"
	promv1 "github.com/prometheus/client_golang/api/prometheus/v1"
	"github.com/prometheus/common/model"
)

// promBackend abstracts the differences between datasource types that support
// PromQL-compatible queries (native Prometheus, Cloud Monitoring, etc.).
type promBackend interface {
	// Query executes a PromQL query (instant or range) and returns the result
	// along with any warnings the datasource reported (e.g. partial responses
	// from a Thanos store).
	Query(ctx context.Context, expr string, queryType string, start, end time.Time, stepSeconds int) (model.Value, promv1.Warnings, error)

	// LabelNames returns label names, optionally filtered by matchers and time range.
	LabelNames(ctx context.Context, matchers []string, start, end time.Time) ([]string, error)

	// LabelValues returns values for a label, optionally filtered by matchers and time range.
	LabelValues(ctx context.Context, labelName string, matchers []string, start, end time.Time) ([]string, error)

	// MetricNames returns metric names which match the regex, with an optional limit on results.
	MetricNames(ctx context.Context, re *regexp.Regexp, limit int, start, end time.Time) ([]string, error)

	// MetricMetadata returns metadata about metrics (description, type, unit).
	MetricMetadata(ctx context.Context, metric string, limit int) (map[string][]promv1.Metadata, error)
}

// backendForDatasource looks up the datasource type and returns the appropriate backend.
// An optional projectOverride can be passed for Cloud Monitoring datasources to override
// (or substitute for) the defaultProject configured on the datasource.
func backendForDatasource(ctx context.Context, uid string, projectOverride ...string) (promBackend, error) {
	ds, err := getDatasourceByUID(ctx, GetDatasourceByUIDParams{UID: uid})
	if err != nil {
		return nil, err
	}

	proj := ""
	if len(projectOverride) > 0 {
		proj = projectOverride[0]
	}

	switch ds.Type {
	case "stackdriver":
		return newCloudMonitoringBackend(ctx, ds, proj)
	case "tempo":
		return nil, fmt.Errorf("datasource %s is of type %q, which is not a supported Prometheus-compatible datasource", uid, ds.Type)
	case "loki", victoriaLogsDatasourceType:
		return nil, fmt.Errorf("datasource %s is of type %q, a log datasource, which is not a supported Prometheus-compatible datasource; query it through the Loki tools", uid, ds.Type)
	default:
		// For prometheus, thanos, cortex, mimir, VictoriaMetrics and any other
		// Prometheus-compatible datasource.
		return newPrometheusBackend(ctx, uid, ds)
	}
}

// prometheusBackend runs queries through Grafana's /api/ds/query endpoint, so
// they go through the query service like dashboard and Explore queries. Discovery
// calls (labels, metric names, metadata) use the Prometheus client library via
// the datasource resources/proxy routes.
type prometheusBackend struct {
	api promv1.API

	httpClient *http.Client
	baseURL    string
	dsUID      string
	dsType     string
}

func newPrometheusBackend(ctx context.Context, uid string, ds *models.DataSource) (*prometheusBackend, error) {
	cfg := mcpgrafana.GrafanaConfigFromContext(ctx)
	grafanaURL := trimTrailingSlash(cfg.URL)
	resourcesBase, proxyBase := datasourceProxyPaths(uid)
	primaryBase, fallbackBase := resourcesBase, proxyBase
	legacyMode := false
	if numericBase, uidBase, ok := fallbackProxyBases(ctx, uid); ok {
		// The datasource was resolved through the frontend-settings fallback,
		// meaning this deployment's metadata API is inaccessible — in practice
		// Grafana before 9.0, which has no uid-based routes at all (8.x answers
		// them with a 400 "id is invalid", 7.x with a 500). Route discovery
		// calls through the numeric-id proxy path directly, keeping the
		// uid-based proxy route only as the transport-level fallback for the
		// opposite case: a newer Grafana with an RBAC-restricted token, where
		// the numeric routes may be disabled (404, off by default since
		// Grafana 13). Queries always use /api/ds/query.
		primaryBase, fallbackBase = numericBase, uidBase
		legacyMode = true
	}
	url := grafanaURL + primaryBase

	// A nil base selects cfg.HTTPTransport(), so an embedder's BaseTransport
	// (tracing, connection pooling, limits) applies here as for other tools.
	baseRT, err := mcpgrafana.BuildTransport(&cfg, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create custom transport: %w", err)
	}
	rt := baseRT

	// Only convert POST→GET if the datasource is configured to use GET.
	// The Prometheus client library sends POST first and only falls back to GET
	// on 405/501 responses, but Grafana's datasource proxy returns 500 for POST
	// requests to datasources configured with httpMethod: GET.
	// See https://github.com/grafana/mcp-grafana/issues/632
	if jsonData, ok := ds.JSONData.(map[string]interface{}); ok {
		if httpMethod, ok := jsonData["httpMethod"].(string); ok && strings.EqualFold(httpMethod, "GET") {
			rt = &postToGetRoundTripper{underlying: rt}
		}
	}

	// Wrap with fallback transport: try the primary base first, fall back to
	// the alternate for compatibility with different Grafana deployments (see
	// fallback_transport.go for the per-mode retry rules).
	if legacyMode {
		rt = newLegacyDatasourceFallbackTransport(rt, primaryBase, fallbackBase)
	} else {
		rt = newDatasourceFallbackTransport(rt, primaryBase, fallbackBase)
	}

	rt = &prometheusResponseLimitTransport{underlying: rt}
	c, err := api.NewClient(api.Config{
		Address:      url,
		RoundTripper: &prometheusMetricNamesTransport{underlying: rt},
	})
	if err != nil {
		return nil, fmt.Errorf("creating Prometheus client: %w", err)
	}

	dsUID := ds.UID
	if dsUID == "" {
		dsUID = uid
	}

	return &prometheusBackend{
		api: promv1.NewAPI(c),
		// No client timeout, as on the proxy client: queries are bounded by
		// ctx and Grafana's own datasource timeout.
		httpClient: &http.Client{Transport: baseRT, CheckRedirect: refuseRedirect},
		baseURL:    grafanaURL,
		dsUID:      dsUID,
		dsType:     ds.Type,
	}, nil
}

// Query executes a PromQL query via Grafana's /api/ds/query endpoint.
func (b *prometheusBackend) Query(ctx context.Context, expr string, queryType string, start, end time.Time, stepSeconds int) (model.Value, promv1.Warnings, error) {
	if queryType != "instant" && queryType != "range" {
		return nil, nil, fmt.Errorf("invalid query type: %s", queryType)
	}

	if start.IsZero() && end.IsZero() {
		end = time.Now()
	}
	if start.IsZero() {
		start = end
	}
	if end.Before(start) {
		end = start
	}

	step := stepSeconds
	if step == 0 {
		step = 60
	}

	datasource := map[string]string{"uid": b.dsUID}
	if b.dsType != "" {
		datasource["type"] = b.dsType
	}
	query := map[string]interface{}{
		"refId":      "A",
		"datasource": datasource,
		"expr":       expr,
		"instant":    queryType == "instant",
		"range":      queryType == "range",
		"interval":   fmt.Sprintf("%ds", step),
		"intervalMs": int64(step) * 1000,
		// Without this Grafana assumes 100 points and widens the step to
		// fit; 11000 is Prometheus' own per-series limit.
		"maxDataPoints": 11000,
	}

	resp, err := doDSQuery(ctx, b.httpClient, b.baseURL, dsQueryPayload(start, end, query))
	if err != nil {
		return nil, nil, fmt.Errorf("querying Prometheus %s: %w", queryType, err)
	}
	if hasStringFrames(resp) {
		return nil, nil, fmt.Errorf("querying Prometheus %s: string results are not supported through Grafana's query service", queryType)
	}

	warnings := frameWarnings(resp)
	histograms := takeHistogramFrames(resp)
	v, err := framesToPrometheusValue(resp, queryType)
	if err != nil {
		return nil, nil, err
	}
	if len(histograms) > 0 {
		v, err = appendNativeHistograms(v, histograms)
		if err != nil {
			return nil, nil, err
		}
		warnings = append(warnings, nativeHistogramSumWarning)
	}
	return v, warnings, nil
}

// hasStringFrames reports whether the response holds a string result.
func hasStringFrames(resp *backend.QueryDataResponse) bool {
	for _, r := range resp.Responses {
		for _, frame := range r.Frames {
			for _, f := range frame.Fields {
				if f.Type() == data.FieldTypeString || f.Type() == data.FieldTypeNullableString {
					return true
				}
			}
		}
	}
	return false
}

// nativeHistogramSumWarning is returned with native histograms, whose sum
// Grafana drops when it converts them to frames.
const nativeHistogramSumWarning = "native histogram sums are not available through Grafana's query service: each histogram's sum is reported as 0, and its count is the sum of its bucket counts"

// takeHistogramFrames removes the native-histogram frames from the response
// and returns them. Grafana returns each native-histogram series as a
// "heatmap-cells" frame with one row per bucket: xMax (time), yMin and yMax
// (bucket bounds), count, and yLayout (the bucket boundary rule).
func takeHistogramFrames(resp *backend.QueryDataResponse) data.Frames {
	r, ok := resp.Responses["A"]
	if !ok {
		return nil
	}
	var histograms, rest data.Frames
	for _, frame := range r.Frames {
		if frame.Meta != nil && frame.Meta.Type == "heatmap-cells" {
			histograms = append(histograms, frame)
		} else {
			rest = append(rest, frame)
		}
	}
	r.Frames = rest
	resp.Responses["A"] = r
	return histograms
}

// appendNativeHistograms converts native-histogram frames to histogram
// samples and adds them to v, a model.Vector or model.Matrix.
func appendNativeHistograms(v model.Value, frames data.Frames) (model.Value, error) {
	for _, frame := range frames {
		metric, pairs, err := histogramFrameToPairs(frame)
		if err != nil {
			return nil, err
		}
		switch val := v.(type) {
		case model.Vector:
			if len(pairs) > 0 {
				last := pairs[len(pairs)-1]
				v = append(val, &model.Sample{Metric: metric, Timestamp: last.Timestamp, Histogram: last.Histogram})
			}
		case model.Matrix:
			v = append(val, &model.SampleStream{Metric: metric, Histograms: pairs})
		}
	}
	return v, nil
}

// histogramFrameToPairs groups a native-histogram frame's bucket rows by
// timestamp.
func histogramFrameToPairs(frame *data.Frame) (model.Metric, []model.SampleHistogramPair, error) {
	fields := map[string]*data.Field{}
	for _, f := range frame.Fields {
		fields[f.Name] = f
	}
	ts, lower, upper, count, layout := fields["xMax"], fields["yMin"], fields["yMax"], fields["count"], fields["yLayout"]
	if ts == nil || lower == nil || upper == nil || count == nil || layout == nil {
		return nil, nil, fmt.Errorf("unexpected native histogram frame: want fields xMax, yMin, yMax, count and yLayout")
	}

	var pairs []model.SampleHistogramPair
	for i := 0; i < frame.Rows(); i++ {
		t, ok := toMillis(ts.At(i))
		if !ok {
			continue
		}
		lo, err1 := lower.FloatAt(i)
		hi, err2 := upper.FloatAt(i)
		c, err3 := count.FloatAt(i)
		boundaries, err4 := layout.FloatAt(i)
		if err := errors.Join(err1, err2, err3, err4); err != nil {
			return nil, nil, fmt.Errorf("reading native histogram bucket: %w", err)
		}
		if len(pairs) == 0 || pairs[len(pairs)-1].Timestamp != model.Time(t) {
			pairs = append(pairs, model.SampleHistogramPair{Timestamp: model.Time(t), Histogram: &model.SampleHistogram{}})
		}
		h := pairs[len(pairs)-1].Histogram
		h.Count += model.FloatString(c)
		h.Buckets = append(h.Buckets, &model.HistogramBucket{
			Boundaries: int32(boundaries),
			Lower:      model.FloatString(lo),
			Upper:      model.FloatString(hi),
			Count:      model.FloatString(c),
		})
	}
	return buildMetricFromLabels(count.Labels, frame.Name), pairs, nil
}

// frameWarnings collects the warnings Grafana attaches to response frames as
// notices (e.g. partial responses from a Thanos store), deduplicated because
// Grafana copies each warning onto every frame.
func frameWarnings(resp *backend.QueryDataResponse) promv1.Warnings {
	var warnings promv1.Warnings
	seen := map[string]bool{}
	for _, r := range resp.Responses {
		for _, frame := range r.Frames {
			if frame.Meta == nil {
				continue
			}
			for _, n := range frame.Meta.Notices {
				if n.Severity == data.NoticeSeverityWarning && !seen[n.Text] {
					seen[n.Text] = true
					warnings = append(warnings, n.Text)
				}
			}
		}
	}
	return warnings
}

func (b *prometheusBackend) LabelNames(ctx context.Context, matchers []string, start, end time.Time) ([]string, error) {
	names, _, _, err := b.api.LabelNames(ctx, matchers, start, end)
	if err != nil {
		return nil, fmt.Errorf("listing Prometheus label names: %w", promErrorWithDetail(err))
	}
	result := make([]string, len(names))
	for i, n := range names {
		result[i] = string(n)
	}
	return result, nil
}

func (b *prometheusBackend) LabelValues(ctx context.Context, labelName string, matchers []string, start, end time.Time) ([]string, error) {
	values, _, _, err := b.api.LabelValues(ctx, labelName, matchers, start, end)
	if err != nil {
		return nil, fmt.Errorf("listing Prometheus label values: %w", promErrorWithDetail(err))
	}
	result := make([]string, len(values))
	for i, v := range values {
		result[i] = string(v)
	}
	return result, nil
}

func (b *prometheusBackend) MetricNames(ctx context.Context, re *regexp.Regexp, limit int, start, end time.Time) ([]string, error) {
	var matchers []string
	if re != nil {
		// make the regex matching behavior behave like the previous client-side Go matching behaviour
		pattern := "(?s:.*)(?-s:" + re.String() + ")(?s:.*)"
		// The non-empty matcher keeps regexes like ".*" valid as a series selector.
		matchers = []string{fmt.Sprintf(`{__name__!="",__name__=~%q}`, pattern)}
	}
	values, _, _, err := b.api.LabelValues(ctx, "__name__", matchers, start, end, promv1.WithLimit(uint64(limit)))
	if err != nil {
		return nil, fmt.Errorf("listing Prometheus metric names: %w", promErrorWithDetail(err))
	}
	recordMetricNames(ctx, "prometheus", len(values))
	result := make([]string, len(values))
	for i, value := range values {
		result[i] = string(value)
	}
	return result, nil
}

func (b *prometheusBackend) MetricMetadata(ctx context.Context, metric string, limit int) (map[string][]promv1.Metadata, error) {
	metadata, err := b.api.Metadata(ctx, metric, fmt.Sprintf("%d", limit))
	if err != nil {
		return nil, fmt.Errorf("listing Prometheus metric metadata: %w", promErrorWithDetail(err))
	}
	return metadata, nil
}

// promErrorWithDetail adds the response body to errors from the Prometheus
// client. For status codes other than 400 and 422 the client stores the body in
// Error.Detail, which Error() leaves out, so a Thanos query timeout (503) would
// otherwise surface as just "server error: 503".
func promErrorWithDetail(err error) error {
	var apiErr *promv1.Error
	if !errors.As(err, &apiErr) || apiErr.Detail == "" {
		return err
	}
	var body struct {
		ErrorType string `json:"errorType"`
		Error     string `json:"error"`
	}
	if json.Unmarshal([]byte(apiErr.Detail), &body) == nil && body.Error != "" {
		if body.ErrorType != "" {
			return fmt.Errorf("%w: %s: %s", err, body.ErrorType, body.Error)
		}
		return fmt.Errorf("%w: %s", err, body.Error)
	}
	detail := strings.TrimSpace(apiErr.Detail)
	if len(detail) > 1024 {
		detail = detail[:1024] + "..."
	}
	return fmt.Errorf("%w: %s", err, detail)
}

// postToGetRoundTripper converts POST requests to GET requests by moving the
// URL-encoded form body to the query string. This is needed because the
// Prometheus client library's DoGetFallback sends POST first and only falls
// back to GET on 405/501 responses, but Grafana's datasource resources API
// returns 500 for POST requests to datasources configured with httpMethod: GET.
type postToGetRoundTripper struct {
	underlying http.RoundTripper
}

func (rt *postToGetRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Method != http.MethodPost {
		return rt.underlying.RoundTrip(req)
	}

	cloned := req.Clone(req.Context())
	cloned.Method = http.MethodGet

	// Move URL-encoded form body to query string
	if req.Body != nil && strings.HasPrefix(req.Header.Get("Content-Type"), "application/x-www-form-urlencoded") {
		body, err := io.ReadAll(req.Body)
		if err != nil {
			return nil, fmt.Errorf("reading request body: %w", err)
		}

		params, err := url.ParseQuery(string(body))
		if err != nil {
			return nil, fmt.Errorf("parsing request body: %w", err)
		}

		// Merge body params into query string
		q := cloned.URL.Query()
		for k, vs := range params {
			for _, v := range vs {
				q.Add(k, v)
			}
		}
		cloned.URL.RawQuery = q.Encode()

		cloned.Body = nil
		cloned.ContentLength = 0
		cloned.Header.Del("Content-Type")
	}

	return rt.underlying.RoundTrip(cloned)
}
