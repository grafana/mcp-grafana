//go:build unit

package tools

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	mcpgrafana "github.com/grafana/mcp-grafana/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
)

const cardinalityHighJSON = `{"status":"success","data":{"seriesCountByMetricName":[{"name":"http_requests_total","value":100001},{"name":"request_duration_seconds_bucket","value":100001}]}}`

// Exercise the real datasource lookup, authenticated Grafana proxy and query
// dispatch. A fake status response alone would miss accidental query execution
// before enforcement or an unsupported /resources status route.
func cardinalityHTTPContext(t *testing.T, ctx context.Context, status int, body string) (context.Context, *atomic.Int64, *atomic.Int64, *atomic.Int64) {
	t.Helper()
	var lookups, stats, queries atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/datasources/uid/prometheus":
			lookups.Add(1)
			_, _ = w.Write([]byte(`{"uid":"prometheus","id":1,"type":"prometheus","name":"prometheus"}`))
		case "/api/datasources/proxy/uid/prometheus/api/v1/status/tsdb":
			stats.Add(1)
			assert.Equal(t, http.MethodGet, r.Method)
			assert.Equal(t, "10", r.URL.Query().Get("limit"))
			assert.Equal(t, "Bearer test-cardinality-token", r.Header.Get("Authorization"))
			assert.Equal(t, "7", r.Header.Get("X-Grafana-Org-Id"))
			assert.Equal(t, "tenant-a", r.Header.Get("X-Scope-OrgID"))
			if status == -1 {
				<-r.Context().Done()
				return
			}
			w.WriteHeader(status)
			_, _ = w.Write([]byte(body))
		case "/api/ds/query":
			queries.Add(1)
			assert.Equal(t, http.MethodPost, r.Method)
			assert.Equal(t, "Bearer test-cardinality-token", r.Header.Get("Authorization"))
			_, _ = w.Write([]byte(`{"results":{"A":{"frames":[]}}}`))
		default:
			t.Errorf("unexpected Grafana request: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	cfg := mcpgrafana.GrafanaConfigFromContext(ctx)
	cfg.URL = server.URL
	cfg.APIKey = "test-cardinality-token"
	cfg.OrgID = 7
	cfg.ExtraHeaders = map[string]string{"X-Scope-OrgID": "tenant-a"}
	cfg.PrometheusGuardrailMaxMetricCardinality = 100000
	ctx = mcpgrafana.WithGrafanaConfig(ctx, cfg)
	ctx = mcpgrafana.WithGrafanaClient(ctx, mcpgrafana.NewGrafanaClient(ctx, server.URL, cfg.APIKey, nil))
	return ctx, &lookups, &stats, &queries
}

func TestPrometheusCardinalityHTTPDecisions(t *testing.T) {
	tests := []struct {
		name, mode, expr, body, counter, reason string
		status                                  int
		wantStats, wantQuery                    bool
		selective, disabled                     bool
	}{
		{name: "enforce before query", mode: "enforce", expr: `http_requests_total{status="200"}`, counter: "blocked", reason: "cardinality", wantStats: true},
		{name: "shadow executes", mode: "shadow", expr: `http_requests_total{status="200"}`, counter: "would_block", reason: "cardinality", wantStats: true, wantQuery: true},
		{name: "off no metadata", mode: "off", expr: `http_requests_total`, wantQuery: true},
		{name: "threshold disabled", mode: "enforce", expr: `http_requests_total`, disabled: true, counter: "admitted", wantQuery: true},
		{name: "exact namespace exempts", mode: "enforce", expr: `http_requests_total{namespace="production"}`, counter: "admitted", wantQuery: true},
		{name: "regex scope does not exempt", mode: "enforce", expr: `http_requests_total{namespace=~"production|staging"}`, counter: "blocked", reason: "cardinality", wantStats: true},
		{name: "missing top metric unknown", mode: "enforce", expr: `other_metric`, counter: "fail_open", reason: "cardinality_unknown", wantStats: true, wantQuery: true},
		{name: "regex metric unknown", mode: "enforce", expr: `{__name__=~"http_requests_.*"}`, counter: "fail_open", reason: "cardinality_unknown", wantQuery: true},
		{name: "metadata timeout allows query", mode: "enforce", expr: `http_requests_total`, status: -1, counter: "fail_open", reason: "cardinality_unknown", wantStats: true, wantQuery: true},
		{name: "unsupported endpoint", mode: "enforce", expr: `http_requests_total`, status: 404, counter: "fail_open", reason: "cardinality_unknown", wantStats: true, wantQuery: true},
		{name: "unauthorized metadata", mode: "enforce", expr: `http_requests_total`, status: 403, counter: "fail_open", reason: "cardinality_unknown", wantStats: true, wantQuery: true},
		{name: "malformed metadata", mode: "enforce", expr: `http_requests_total`, body: `{`, counter: "fail_open", reason: "cardinality_unknown", wantStats: true, wantQuery: true},
		{name: "missing stats field", mode: "enforce", expr: `http_requests_total`, body: `{"status":"success","data":{}}`, counter: "fail_open", reason: "cardinality_unknown", wantStats: true, wantQuery: true},
		{name: "error envelope", mode: "enforce", expr: `http_requests_total`, body: `{"status":"error","data":{"seriesCountByMetricName":[]}}`, counter: "fail_open", reason: "cardinality_unknown", wantStats: true, wantQuery: true},
		{name: "threshold inclusive", mode: "enforce", expr: `http_requests_total`, body: `{"status":"success","data":{"seriesCountByMetricName":[{"name":"http_requests_total","value":100000}]}}`, counter: "admitted", wantStats: true, wantQuery: true},
		{name: "static selector wins", mode: "enforce", expr: `http_requests_total`, selective: true, counter: "blocked", reason: "selector"},
		{name: "shadow static skips metadata", mode: "shadow", expr: `http_requests_total`, selective: true, counter: "would_block", reason: "selector", wantQuery: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			reader := sdkmetric.NewManualReader()
			ctx := newPromGuardMetricsContext(t, reader, tc.mode, 0, 0)
			status := tc.status
			if status == 0 {
				status = http.StatusOK
			}
			body := tc.body
			if body == "" {
				body = cardinalityHighJSON
			}
			ctx, lookups, stats, queries := cardinalityHTTPContext(t, ctx, status, body)
			cfg := mcpgrafana.GrafanaConfigFromContext(ctx)
			cfg.PrometheusGuardrailRequireSelectiveLabels = tc.selective
			if tc.disabled {
				cfg.PrometheusGuardrailMaxMetricCardinality = 0
			}
			ctx = mcpgrafana.WithGrafanaConfig(ctx, cfg)
			_, err := queryPrometheus(ctx, QueryPrometheusParams{DatasourceUID: "prometheus", Expr: tc.expr, QueryType: "instant", EndTime: "2026-11-11T00:00:00Z"})
			if tc.counter == "blocked" {
				require.ErrorContains(t, err, "Prometheus query guardrail")
			} else {
				require.NoError(t, err)
			}
			if tc.wantStats {
				assert.EqualValues(t, 1, stats.Load())
			} else {
				assert.Zero(t, stats.Load())
			}
			if tc.wantQuery {
				assert.EqualValues(t, 1, queries.Load())
			} else {
				assert.Zero(t, queries.Load())
			}
			// Backend resolution is shared between preflight and execution.
			if tc.wantStats || tc.wantQuery {
				assert.EqualValues(t, 1, lookups.Load())
			} else {
				assert.Zero(t, lookups.Load())
			}
			if tc.counter == "" {
				assert.Empty(t, promGuardCounts(t, reader))
			} else {
				attrs := ""
				if tc.reason != "" {
					attrs = "reason=" + tc.reason
				}
				assert.Equal(t, map[string]map[string]int64{"mcp.prometheus_guardrail." + tc.counter: {attrs: 1}}, promGuardCounts(t, reader))
			}
		})
	}
}

func TestPrometheusCardinalityAllQueryPaths(t *testing.T) {
	paths := []struct {
		name string
		run  func(context.Context) error
	}{
		{"range", func(ctx context.Context) error {
			_, err := queryPrometheus(ctx, QueryPrometheusParams{DatasourceUID: "prometheus", Expr: `http_requests_total{status="200"}`, QueryType: "range", StartTime: "2026-11-10T23:00:00Z", EndTime: "2026-11-11T00:00:00Z", StepSeconds: 60})
			return err
		}},
		{"histogram", func(ctx context.Context) error {
			_, err := queryPrometheusHistogram(ctx, QueryPrometheusHistogramParams{DatasourceUID: "prometheus", Metric: "request_duration_seconds", Labels: `status="200"`, Percentile: 95, StartTime: "2026-11-10T23:00:00Z", EndTime: "2026-11-11T00:00:00Z"})
			return err
		}},
		{"panel", func(ctx context.Context) error {
			_, err := executePrometheusQuery(ctx, "prometheus", `http_requests_total{status="200"}`, "2026-11-10T23:00:00Z", "2026-11-11T00:00:00Z")
			return err
		}},
	}
	for _, path := range paths {
		for _, mode := range []string{"enforce", "shadow", "off"} {
			t.Run(path.name+"/"+mode, func(t *testing.T) {
				reader := sdkmetric.NewManualReader()
				ctx := newPromGuardMetricsContext(t, reader, mode, 192*time.Hour, 11000)
				ctx, _, stats, queries := cardinalityHTTPContext(t, ctx, http.StatusOK, cardinalityHighJSON)
				err := path.run(ctx)
				if mode == "enforce" {
					require.ErrorContains(t, err, "100001 series")
					assert.Zero(t, queries.Load())
				} else {
					require.NoError(t, err)
					assert.EqualValues(t, 1, queries.Load())
				}
				if mode == "off" {
					assert.Zero(t, stats.Load())
				} else {
					assert.EqualValues(t, 1, stats.Load())
				}
			})
		}
	}
}

func TestPrometheusCardinalityHTTPFailureCache(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	ctx := newPromGuardMetricsContext(t, reader, "enforce", 0, 0)
	ctx, lookups, stats, queries := cardinalityHTTPContext(t, ctx, http.StatusNotFound, `{"message":"not found"}`)
	for range 2 {
		_, err := queryPrometheus(ctx, QueryPrometheusParams{DatasourceUID: "prometheus", Expr: "http_requests_total", QueryType: "instant", EndTime: "2026-11-11T00:00:00Z"})
		require.NoError(t, err)
	}
	assert.EqualValues(t, 2, lookups.Load(), "query authorization still resolves the datasource")
	assert.EqualValues(t, 1, stats.Load(), "unsupported metadata must not retry on every query")
	assert.EqualValues(t, 2, queries.Load())
	assert.Equal(t, map[string]map[string]int64{"mcp.prometheus_guardrail.fail_open": {"reason=cardinality_unknown": 2}}, promGuardCounts(t, reader))
}

func TestPrometheusCardinalityResolvedProxyRoutes(t *testing.T) {
	for _, tc := range []struct {
		name          string
		legacy        bool
		numericStatus int
		wantPaths     []string
	}{
		{name: "modern resolved UID", wantPaths: []string{"/api/datasources/proxy/uid/prometheus/api/v1/status/tsdb"}},
		{name: "legacy numeric", legacy: true, wantPaths: []string{"/api/datasources/proxy/1/api/v1/status/tsdb"}},
		{name: "legacy 404 fallback", legacy: true, numericStatus: 404, wantPaths: []string{"/api/datasources/proxy/1/api/v1/status/tsdb", "/api/datasources/proxy/uid/prometheus/api/v1/status/tsdb"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			paths := make(chan string, 4)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/api/datasources/uid/Prometheus":
					if tc.legacy {
						w.WriteHeader(403)
						_, _ = w.Write([]byte(`{"message":"permission denied"}`))
						return
					}
					_, _ = w.Write([]byte(`{"uid":"prometheus","id":1,"type":"prometheus","name":"Prometheus"}`))
				case "/api/frontend/settings":
					_, _ = w.Write([]byte(`{"datasources":{"Prometheus":{"id":1,"uid":"prometheus","type":"prometheus","jsonData":{}}},"defaultDatasource":"Prometheus"}`))
				case "/api/datasources/proxy/1/api/v1/status/tsdb":
					paths <- r.URL.Path
					if tc.numericStatus != 0 {
						w.WriteHeader(tc.numericStatus)
						_, _ = w.Write([]byte(`{"message":"not found"}`))
						return
					}
					_, _ = w.Write([]byte(cardinalityHighJSON))
				case "/api/datasources/proxy/uid/prometheus/api/v1/status/tsdb":
					paths <- r.URL.Path
					_, _ = w.Write([]byte(cardinalityHighJSON))
				default:
					t.Errorf("unexpected request %s", r.URL.Path)
					w.WriteHeader(500)
				}
			}))
			defer server.Close()
			ctx := mockDatasourcesCtx(server)
			cfg := mcpgrafana.GrafanaConfigFromContext(ctx)
			cfg.PrometheusGuardrailMode = "enforce"
			cfg.PrometheusGuardrailMaxMetricCardinality = 100000
			ctx = mcpgrafana.WithGrafanaConfig(ctx, cfg)
			_, err := queryPrometheus(ctx, QueryPrometheusParams{DatasourceUID: "Prometheus", Expr: "http_requests_total", QueryType: "instant", EndTime: "2026-11-11T00:00:00Z"})
			require.ErrorContains(t, err, "100001 series")
			close(paths)
			var got []string
			for path := range paths {
				got = append(got, path)
			}
			assert.Equal(t, tc.wantPaths, got)
		})
	}
}

func TestPrometheusCardinalityFailOpenPreservesQueryAuthorization(t *testing.T) {
	var metadataCalls, queryCalls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/datasources/uid/prometheus":
			_, _ = w.Write([]byte(`{"uid":"prometheus","id":1,"type":"prometheus"}`))
		case "/api/datasources/proxy/uid/prometheus/api/v1/status/tsdb":
			metadataCalls.Add(1)
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"message":"metadata forbidden"}`))
		case "/api/ds/query":
			queryCalls.Add(1)
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"message":"query unauthorized"}`))
		default:
			t.Errorf("unexpected request %s", r.URL.Path)
			w.WriteHeader(500)
		}
	}))
	defer server.Close()
	ctx := mockDatasourcesCtx(server)
	cfg := mcpgrafana.GrafanaConfigFromContext(ctx)
	cfg.PrometheusGuardrailMode = "enforce"
	cfg.PrometheusGuardrailMaxMetricCardinality = 100000
	ctx = mcpgrafana.WithGrafanaConfig(ctx, cfg)
	_, err := queryPrometheus(ctx, QueryPrometheusParams{DatasourceUID: "prometheus", Expr: "http_requests_total", QueryType: "instant", EndTime: "2026-11-11T00:00:00Z"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "401")
	assert.EqualValues(t, 1, metadataCalls.Load())
	assert.EqualValues(t, 1, queryCalls.Load())
}

func TestPrometheusCardinalityHTTPIdentityIsolation(t *testing.T) {
	for _, dimension := range []string{"API token", "organization", "forwarded tenant", "OBO identity", "URL override"} {
		t.Run(dimension, func(t *testing.T) {
			var stats atomic.Int64
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/api/datasources/uid/prometheus":
					_, _ = w.Write([]byte(`{"uid":"prometheus","id":1,"type":"prometheus"}`))
				case "/api/datasources/proxy/uid/prometheus/api/v1/status/tsdb":
					if stats.Add(1) == 1 {
						_, _ = w.Write([]byte(cardinalityHighJSON))
					} else {
						_, _ = w.Write([]byte(`{"status":"success","data":{"seriesCountByMetricName":[{"name":"http_requests_total","value":1}]}}`))
					}
				case "/api/ds/query":
					_, _ = w.Write([]byte(`{"results":{"A":{"frames":[]}}}`))
				default:
					t.Errorf("unexpected request %s", r.URL.Path)
					w.WriteHeader(500)
				}
			})
			server := httptest.NewServer(handler)
			defer server.Close()
			second := httptest.NewServer(handler)
			defer second.Close()
			ctx := mockDatasourcesCtx(server)
			cfg := mcpgrafana.GrafanaConfigFromContext(ctx)
			cfg.PrometheusGuardrailMode = "enforce"
			cfg.PrometheusGuardrailMaxMetricCardinality = 100000
			cfg.APIKey = "first-token"
			cfg.OrgID = 1
			cfg.ExtraHeaders = map[string]string{"X-Scope-OrgID": "first-tenant"}
			if dimension == "OBO identity" {
				cfg.AccessToken = "access-token"
				cfg.IDToken = "first-id"
			}
			args := QueryPrometheusParams{DatasourceUID: "prometheus", Expr: "http_requests_total", QueryType: "instant", EndTime: "2026-11-11T00:00:00Z"}
			_, err := queryPrometheus(mcpgrafana.WithGrafanaConfig(ctx, cfg), args)
			require.ErrorContains(t, err, "100001 series")
			switch dimension {
			case "API token":
				cfg.APIKey = "second-token"
			case "organization":
				cfg.OrgID = 2
			case "forwarded tenant":
				cfg.ExtraHeaders = map[string]string{"X-Scope-OrgID": "second-tenant"}
			case "OBO identity":
				cfg.IDToken = "second-id"
			case "URL override":
				// Override middleware selects the effective URL and pins it.
				cfg.URL = second.URL
				cfg.OverrideURL = second.URL
			}
			_, err = queryPrometheus(mcpgrafana.WithGrafanaConfig(ctx, cfg), args)
			require.NoError(t, err, "the second identity must not inherit the first identity's cached block")
			assert.EqualValues(t, 2, stats.Load())
		})
	}
}

func TestPrometheusCardinalityBackendLookupFailureIsNotRetried(t *testing.T) {
	var lookups atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/datasources/uid/missing" {
			t.Errorf("unexpected request %s", r.URL.Path)
		}
		lookups.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"not found"}`))
	}))
	defer server.Close()
	reader := sdkmetric.NewManualReader()
	metricsCtx := newPromGuardMetricsContext(t, reader, "enforce", 0, 0)
	cfg := mcpgrafana.GrafanaConfigFromContext(mockDatasourcesCtx(server))
	cfg.MeterProvider = mcpgrafana.GrafanaConfigFromContext(metricsCtx).MeterProvider
	cfg.PrometheusGuardrailMode = "enforce"
	cfg.PrometheusGuardrailMaxMetricCardinality = 100000
	ctx := mcpgrafana.WithGrafanaConfig(mockDatasourcesCtx(server), cfg)
	_, err := queryPrometheus(ctx, QueryPrometheusParams{DatasourceUID: "missing", Expr: "http_requests_total", QueryType: "instant", EndTime: "2026-11-11T00:00:00Z"})
	require.ErrorContains(t, err, "not found")
	assert.EqualValues(t, 1, lookups.Load())
	assert.Equal(t, map[string]map[string]int64{"mcp.prometheus_guardrail.fail_open": {"reason=cardinality_unknown": 1}}, promGuardCounts(t, reader))
}
