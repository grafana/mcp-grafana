package tools

import (
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
	// Query executes a PromQL query (instant or range) and returns the result.
	Query(ctx context.Context, expr string, queryType string, start, end time.Time, stepSeconds int) (model.Value, error)

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
	url := grafanaURL + resourcesBase

	rt, err := mcpgrafana.BuildTransport(&cfg, api.DefaultRoundTripper)
	if err != nil {
		return nil, fmt.Errorf("failed to create custom transport: %w", err)
	}

	rt = NewAuthRoundTripper(rt, cfg.AccessToken, cfg.IDToken, cfg.APIKey, cfg.BasicAuth)
	rt = mcpgrafana.NewOrgIDRoundTripper(rt, cfg.OrgID)

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

	// Wrap with fallback transport: try /resources first, fall back to /proxy
	// on 403/500 for compatibility with different managed Grafana deployments.
	rt = newDatasourceFallbackTransport(rt, resourcesBase, proxyBase)

	c, err := api.NewClient(api.Config{
		Address:      url,
		RoundTripper: rt,
	})
	if err != nil {
		return nil, fmt.Errorf("creating Prometheus client: %w", err)
	}

	return &prometheusBackend{api: promv1.NewAPI(c)}, nil
}

func (b *prometheusBackend) Query(ctx context.Context, expr string, queryType string, start, end time.Time, stepSeconds int) (model.Value, error) {
	switch queryType {
	case "range":
		step := time.Duration(stepSeconds) * time.Second
		result, _, err := b.api.QueryRange(ctx, expr, promv1.Range{
			Start: start,
			End:   end,
			Step:  step,
		})
		if err != nil {
			return nil, fmt.Errorf("querying Prometheus range: %w", err)
		}
		return result, nil
	case "instant":
		result, _, err := b.api.Query(ctx, expr, end)
		if err != nil {
			return nil, fmt.Errorf("querying Prometheus instant: %w", err)
		}
		return result, nil
	default:
		return nil, fmt.Errorf("invalid query type: %s", queryType)
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

func trimTrailingSlash(s string) string {
	for len(s) > 0 && s[len(s)-1] == '/' {
		s = s[:len(s)-1]
	}
	return s
}
