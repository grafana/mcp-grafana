package tools

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/grafana/grafana-openapi-client-go/models"
	mcpgrafana "github.com/grafana/mcp-grafana"
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
	case victoriaMetricsDatasourceType:
		return newVictoriaMetricsBackend(ctx, uid, ds)
	case "tempo":
		return nil, fmt.Errorf("datasource %s is of type %q, which is not a supported Prometheus-compatible datasource", uid, ds.Type)
	default:
		// For prometheus, thanos, cortex, mimir, and any other Prometheus-compatible datasource,
		// use the native Prometheus client via the datasource proxy.
		return newPrometheusBackend(ctx, uid, ds)
	}
}

// prometheusBackend wraps the Prometheus client library, talking to the
// datasource via Grafana's datasource proxy (/api/datasources/uid/{uid}/resources).
type prometheusBackend struct {
	api promv1.API
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
		// them with a 400 "id is invalid", 7.x with a 500). Route through the
		// numeric-id proxy path directly, keeping the uid-based proxy route
		// only as the transport-level fallback for the opposite case: a newer
		// Grafana with an RBAC-restricted token, where the numeric routes may
		// be disabled (404, off by default since Grafana 13).
		primaryBase, fallbackBase = numericBase, uidBase
		legacyMode = true
	}
	url := grafanaURL + primaryBase

	rt, err := mcpgrafana.BuildTransport(&cfg, api.DefaultRoundTripper)
	if err != nil {
		return nil, fmt.Errorf("failed to create custom transport: %w", err)
	}

	// Keep POST as the default so large PromQL expressions stay in the request
	// body. Datasources explicitly configured for GET need the request converted
	// before it reaches Grafana, while other datasources retry with GET only when
	// the proxy rejects or drops the POST body.
	// See https://github.com/grafana/mcp-grafana/issues/632
	if prometheusDatasourceUsesGET(ds) {
		rt = &postToGetRoundTripper{underlying: rt}
	} else {
		rt = &postToGetFallbackRoundTripper{underlying: rt}
	}

	// Wrap with fallback transport: try the primary base first, fall back to
	// the alternate for compatibility with different Grafana deployments (see
	// fallback_transport.go for the per-mode retry rules).
	if legacyMode {
		rt = newLegacyDatasourceFallbackTransport(rt, primaryBase, fallbackBase)
	} else {
		rt = newDatasourceFallbackTransport(rt, primaryBase, fallbackBase)
	}

	c, err := api.NewClient(api.Config{
		Address:      url,
		RoundTripper: rt,
	})
	if err != nil {
		return nil, fmt.Errorf("creating Prometheus client: %w", err)
	}

	return &prometheusBackend{api: promv1.NewAPI(c)}, nil
}

func (b *prometheusBackend) Query(ctx context.Context, expr string, queryType string, start, end time.Time, stepSeconds int) (model.Value, promv1.Warnings, error) {
	switch queryType {
	case "range":
		step := time.Duration(stepSeconds) * time.Second
		result, warnings, err := b.api.QueryRange(ctx, expr, promv1.Range{
			Start: start,
			End:   end,
			Step:  step,
		})
		if err != nil {
			return nil, nil, fmt.Errorf("querying Prometheus range: %w", err)
		}
		return result, warnings, nil
	case "instant":
		result, warnings, err := b.api.Query(ctx, expr, end)
		if err != nil {
			return nil, nil, fmt.Errorf("querying Prometheus instant: %w", err)
		}
		return result, warnings, nil
	default:
		return nil, nil, fmt.Errorf("invalid query type: %s", queryType)
	}
}

func (b *prometheusBackend) LabelNames(ctx context.Context, matchers []string, start, end time.Time) ([]string, error) {
	names, _, err := b.api.LabelNames(ctx, matchers, start, end)
	if err != nil {
		return nil, fmt.Errorf("listing Prometheus label names: %w", err)
	}
	return names, nil
}

func (b *prometheusBackend) LabelValues(ctx context.Context, labelName string, matchers []string, start, end time.Time) ([]string, error) {
	values, _, err := b.api.LabelValues(ctx, labelName, matchers, start, end)
	if err != nil {
		return nil, fmt.Errorf("listing Prometheus label values: %w", err)
	}
	result := make([]string, len(values))
	for i, v := range values {
		result[i] = string(v)
	}
	return result, nil
}

func (b *prometheusBackend) MetricMetadata(ctx context.Context, metric string, limit int) (map[string][]promv1.Metadata, error) {
	metadata, err := b.api.Metadata(ctx, metric, fmt.Sprintf("%d", limit))
	if err != nil {
		return nil, fmt.Errorf("listing Prometheus metric metadata: %w", err)
	}
	return metadata, nil
}

func prometheusDatasourceUsesGET(ds *models.DataSource) bool {
	if ds == nil {
		return false
	}

	jsonData, ok := ds.JSONData.(map[string]interface{})
	if !ok {
		return false
	}

	httpMethod, ok := jsonData["httpMethod"].(string)
	return ok && strings.EqualFold(httpMethod, "GET")
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

	body, err := bufferRequestBody(req)
	if err != nil {
		return nil, err
	}

	cloned, err := postToGetRequest(req, body)
	if err != nil {
		return nil, err
	}

	return rt.underlying.RoundTrip(cloned)
}

// postToGetFallbackRoundTripper preserves POST for the initial request so
// large queries are not placed in the URL. Some Grafana datasource proxy
// deployments do not forward the POST body, however, and surface that as a
// 422 or 500 response. In those cases, retry the request as GET.
type postToGetFallbackRoundTripper struct {
	underlying http.RoundTripper
}

func (rt *postToGetFallbackRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Method != http.MethodPost {
		return rt.underlying.RoundTrip(req)
	}

	body, err := bufferRequestBody(req)
	if err != nil {
		return nil, err
	}

	resp, err := rt.underlying.RoundTrip(req)
	if err != nil || !shouldRetryPostAsGet(resp) {
		return resp, err
	}

	if resp.Body != nil {
		_ = resp.Body.Close()
	}

	retryReq, err := postToGetRequest(req, body)
	if err != nil {
		return nil, err
	}

	return rt.underlying.RoundTrip(retryReq)
}

func shouldRetryPostAsGet(resp *http.Response) bool {
	if resp == nil {
		return false
	}

	return resp.StatusCode == http.StatusUnprocessableEntity || resp.StatusCode == http.StatusInternalServerError
}

// bufferRequestBody reads and restores a request body so the request can be
// sent once as POST and replayed as GET if needed.
func bufferRequestBody(req *http.Request) ([]byte, error) {
	if req.Body == nil {
		return nil, nil
	}

	body, err := io.ReadAll(req.Body)
	_ = req.Body.Close()
	if err != nil {
		return nil, fmt.Errorf("reading request body: %w", err)
	}

	req.Body = io.NopCloser(bytes.NewReader(body))
	req.ContentLength = int64(len(body))
	req.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(body)), nil
	}

	return body, nil
}

func postToGetRequest(req *http.Request, body []byte) (*http.Request, error) {
	cloned := req.Clone(req.Context())
	cloned.Method = http.MethodGet
	cloned.Body = nil
	cloned.ContentLength = 0
	cloned.GetBody = nil
	cloned.Header.Del("Content-Type")

	// Move URL-encoded form body to query string
	if req.Body != nil && strings.HasPrefix(req.Header.Get("Content-Type"), "application/x-www-form-urlencoded") {
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
	}

	return cloned, nil
}
