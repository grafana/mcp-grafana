//go:build unit

package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/grafana/grafana-openapi-client-go/client"
	mcpgrafana "github.com/grafana/mcp-grafana"
	"github.com/mark3labs/mcp-go/server"
	"github.com/prometheus/common/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestServiceHealthRegistrationRespectsQueryGate(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		s := server.NewMCPServer("test", "0.0.0")
		AddServiceHealthAppTools(s, enabled)
		tool, registered := s.ListTools()["render_service_health"]
		assert.Equal(t, enabled, registered)
		if enabled {
			require.NotNil(t, tool.Tool.Annotations.ReadOnlyHint)
			assert.True(t, *tool.Tool.Annotations.ReadOnlyHint)
			require.NotNil(t, tool.Tool.Meta)
			ui := tool.Tool.Meta.AdditionalFields["ui"].(map[string]any)
			assert.Equal(t, mcpgrafana.ServiceHealthResourceURI, ui["resourceUri"])
			var schema struct {
				Properties map[string]json.RawMessage `json:"properties"`
				Required   []string                   `json:"required"`
			}
			require.NoError(t, json.Unmarshal(tool.Tool.RawInputSchema, &schema))
			assert.ElementsMatch(t, []string{"datasource_uid", "service_name"}, schema.Required)
			assert.Contains(t, schema.Properties, "service_namespace")
			assert.Contains(t, schema.Properties, "time_range")
		}
	}
}

func TestRenderServiceHealthRequiresGrafanaURL(t *testing.T) {
	args := ServiceHealthParams{DatasourceUID: "prom", ServiceName: "checkout"}
	_, err := renderServiceHealth(t.Context(), args)
	require.EqualError(t, err, "grafana URL is not available in the authenticated request context")

}

func TestRenderServiceHealthQueriesWithConfiguredAuth(t *testing.T) {
	var mu sync.Mutex
	var queries []string
	var authHeaders []string
	grafana := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		authHeaders = append(authHeaders, r.Header.Get("Authorization"))
		mu.Unlock()
		if r.URL.Path == "/api/datasources/uid/prom" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"uid":"prom","type":"prometheus"}`))
			return
		}
		if !strings.HasSuffix(r.URL.Path, "/api/v1/query_range") {
			http.NotFound(w, r)
			return
		}
		_ = r.ParseForm()
		query := r.Form.Get("query")
		mu.Lock()
		queries = append(queries, query)
		mu.Unlock()
		value := "12"
		switch {
		case strings.Contains(query, "latency_bucket"):
			value = "250"
		case strings.Contains(query, `status_code="STATUS_CODE_ERROR"`):
			value = "0.01"
		case strings.Contains(query, "request_client_seconds_bucket"):
			value = "42"
		case strings.Contains(query, "request_failed_total"):
			value = "0.02"
		}
		labels := map[string]string{}
		if strings.Contains(query, "traces_service_graph_") {
			labels = map[string]string{"server": "postgres", "server_service_namespace": "data", "connection_type": "database"}
		}
		end, _ := strconv.ParseFloat(r.Form.Get("end"), 64)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": "success",
			"data": map[string]any{"resultType": "matrix", "result": []any{
				map[string]any{"metric": labels, "values": []any{[]any{end - 60, value}, []any{end, value}}},
			}},
		})
	}))
	defer grafana.Close()

	ctx := serviceHealthTestContext(t, grafana, "test-token")
	namespace := "shop"
	result, err := renderServiceHealth(ctx, ServiceHealthParams{DatasourceUID: "prom", ServiceName: "checkout", ServiceNamespace: &namespace, TimeRange: "6h"})
	require.NoError(t, err)
	got := result.StructuredContent.(ServiceHealthResult)
	assert.Equal(t, "unknown", got.Status)
	assert.Equal(t, "6h", got.TimeRange)
	require.NotNil(t, got.Metrics.LatencyP95.Value)
	assert.Equal(t, 250.0, *got.Metrics.LatencyP95.Value)
	require.NotNil(t, got.Metrics.ErrorRatio.Value)
	assert.Equal(t, 0.01, *got.Metrics.ErrorRatio.Value)
	require.Len(t, got.Dependencies, 1)
	assert.Equal(t, "postgres", got.Dependencies[0].Name)
	assert.Equal(t, "database", got.Dependencies[0].Type)
	require.NotNil(t, got.GrafanaURL)
	assert.Contains(t, *got.GrafanaURL, "/services/service/shop---checkout")
	assert.NotContains(t, *got.GrafanaURL, "test-token")
	assert.LessOrEqual(t, len(got.Metrics.RequestRate.Points), maxServiceHealthPoints)

	mu.Lock()
	defer mu.Unlock()
	require.NotEmpty(t, queries)
	for _, header := range authHeaders {
		assert.Equal(t, "Bearer test-token", header)
	}
	assert.Contains(t, fmt.Sprint(queries), `service="checkout",service_namespace="shop",span_kind=~"SPAN_KIND_SERVER|SPAN_KIND_CONSUMER"`)
}

func TestRenderServiceHealthLinkUsesPublicURLOnlyForViewerOrg(t *testing.T) {
	grafana := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/datasources/uid/prom":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"uid":"prom","type":"prometheus"}`))
		case r.URL.Path == "/api/user":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"orgId":1}`))
		case strings.HasSuffix(r.URL.Path, "/api/v1/query_range"):
			_ = r.ParseForm()
			end, _ := strconv.ParseFloat(r.Form.Get("end"), 64)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"status": "success",
				"data": map[string]any{"resultType": "matrix", "result": []any{
					map[string]any{"metric": map[string]string{}, "values": []any{[]any{end, "1"}}},
				}},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer grafana.Close()

	for _, tc := range []struct {
		name      string
		orgID     int64
		publicURL string
		wantLink  bool
	}{
		{"default org", 0, "https://public.example/grafana", true},
		{"matching org", 1, "https://public.example/grafana", true},
		{"different org", 2, "https://public.example/grafana", false},
		{"invalid public URL", 0, "not a valid URL", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := serviceHealthTestContext(t, grafana, "test-token")
			cfg := mcpgrafana.GrafanaConfigFromContext(ctx)
			cfg.OrgID = tc.orgID
			ctx = mcpgrafana.WithGrafanaConfig(ctx, cfg)
			mcpgrafana.GrafanaClientFromContext(ctx).PublicURL = tc.publicURL

			response, err := renderServiceHealth(ctx, ServiceHealthParams{DatasourceUID: "prom", ServiceName: "checkout"})
			require.NoError(t, err)
			got := response.StructuredContent.(ServiceHealthResult)
			require.NotNil(t, got.Metrics.RequestRate.Value, "metric data remains available when the link is omitted")
			if tc.wantLink {
				require.NotNil(t, got.GrafanaURL)
				assert.Contains(t, *got.GrafanaURL, "https://public.example/grafana/a/grafana-app-observability-app/")
				assert.Equal(t, got.GrafanaURL, got.DashboardURL)
			} else {
				assert.Nil(t, got.GrafanaURL)
				assert.Nil(t, got.DashboardURL)
				assert.Contains(t, strings.Join(got.Warnings, " "), "Application Observability link unavailable")
			}
		})
	}
}

func TestServiceHealthValidation(t *testing.T) {
	for _, tc := range []struct {
		name string
		args ServiceHealthParams
		want string
	}{
		{"default", ServiceHealthParams{DatasourceUID: "prom", ServiceName: "checkout"}, ""},
		{"missing service", ServiceHealthParams{DatasourceUID: "prom"}, "service_name is required"},
		{"bad range", ServiceHealthParams{DatasourceUID: "prom", ServiceName: "checkout", TimeRange: "30d"}, "time_range must be one of 1h, 6h, 24h, or 7d"},
		{"control", ServiceHealthParams{DatasourceUID: "prom", ServiceName: "checkout\nprod"}, "service_name must not contain control characters"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := validateServiceHealthParams(&tc.args)
			if tc.want == "" {
				require.NoError(t, err)
				assert.Equal(t, "1h", tc.args.TimeRange)
			} else {
				require.EqualError(t, err, tc.want)
			}
		})
	}
}

func TestRenderServiceHealthRejectsWrongDatasourceBeforeQuery(t *testing.T) {
	var queryCount atomic.Int32
	grafana := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/datasources/uid/tempo" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"uid":"tempo","type":"tempo"}`))
			return
		}
		queryCount.Add(1)
		http.NotFound(w, r)
	}))
	defer grafana.Close()

	ctx := serviceHealthTestContext(t, grafana, "")
	_, err := renderServiceHealth(ctx, ServiceHealthParams{DatasourceUID: "tempo", ServiceName: "checkout"})
	require.ErrorContains(t, err, "not a supported Prometheus-compatible datasource")
	assert.Zero(t, queryCount.Load())
}

func TestServiceHealthCapsPointsAndClearsOverflow(t *testing.T) {
	now := time.Now().UTC()
	values := make([]model.SamplePair, maxServiceHealthPoints+5)
	for i := range values {
		values[i] = model.SamplePair{Timestamp: model.Time(now.Add(time.Duration(i-len(values)+1) * time.Minute).UnixMilli()), Value: 1}
	}
	matrix := model.Matrix{
		&model.SampleStream{Values: values},
		&model.SampleStream{Values: []model.SamplePair{{Timestamp: values[len(values)-1].Timestamp, Value: model.SampleValue(math.MaxFloat64)}}},
		&model.SampleStream{Values: []model.SamplePair{{Timestamp: values[len(values)-1].Timestamp, Value: model.SampleValue(math.MaxFloat64)}}},
	}
	points, err := aggregatePrometheusPoints(matrix)
	require.NoError(t, err)
	require.Nil(t, points[len(points)-1].Value, "overflow must not become a JSON infinity")
	warnings := []string{}
	metric := serviceHealthMetricFromQuery("request rate", "requests_per_second", rangeQueryResult{value: matrix}, now, time.Minute, &warnings)
	assert.Len(t, metric.Points, maxServiceHealthPoints)
	assert.Nil(t, metric.Value)
	assert.NotEmpty(t, warnings)
}

func serviceHealthTestContext(t *testing.T, grafana *httptest.Server, apiKey string) context.Context {
	t.Helper()
	u, err := url.Parse(grafana.URL)
	require.NoError(t, err)
	cfg := client.DefaultTransportConfig()
	cfg.Host = u.Host
	cfg.Schemes = []string{u.Scheme}
	cfg.APIKey = apiKey
	grafanaClient := client.NewHTTPClientWithConfig(nil, cfg)
	ctx := mcpgrafana.WithGrafanaClient(t.Context(), &mcpgrafana.GrafanaClient{GrafanaHTTPAPI: grafanaClient})
	return mcpgrafana.WithGrafanaConfig(ctx, mcpgrafana.GrafanaConfig{URL: grafana.URL, APIKey: apiKey})
}

func TestRenderServiceHealthUsesNativeHistogramFallback(t *testing.T) {
	grafana := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/datasources/uid/prom" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"uid":"prom","type":"prometheus"}`))
			return
		}
		if !strings.HasSuffix(r.URL.Path, "/api/v1/query_range") {
			http.NotFound(w, r)
			return
		}
		_ = r.ParseForm()
		query := r.Form.Get("query")
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(query, "latency_bucket") || strings.Contains(query, "request_client_seconds_bucket") {
			_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"matrix","result":[]}}`))
			return
		}
		end, _ := strconv.ParseFloat(r.Form.Get("end"), 64)
		value := "0.01"
		labels := map[string]string{}
		if strings.Contains(query, "traces_spanmetrics_latency{") {
			value = "125"
		}
		if strings.Contains(query, "request_client_seconds{") {
			value = "31"
			labels = map[string]string{"server": "inventory", "server_namespace": "shop"}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": "success",
			"data": map[string]any{"resultType": "matrix", "result": []any{
				map[string]any{"metric": labels, "values": []any{[]any{end, value}}},
			}},
		})
	}))
	defer grafana.Close()

	result, err := renderServiceHealth(serviceHealthTestContext(t, grafana, "test-token"), ServiceHealthParams{DatasourceUID: "prom", ServiceName: "checkout"})
	require.NoError(t, err)
	got := result.StructuredContent.(ServiceHealthResult)
	require.NotNil(t, got.Metrics.LatencyP95.Value)
	assert.Equal(t, 125.0, *got.Metrics.LatencyP95.Value)
	require.Len(t, got.Dependencies, 1)
	require.NotNil(t, got.Dependencies[0].LatencyP95MS)
	assert.Equal(t, 31.0, *got.Dependencies[0].LatencyP95MS)
}

func TestRenderServiceHealthKeepsUnknownOnPartialQueryFailure(t *testing.T) {
	grafana := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/datasources/uid/prom" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"uid":"prom","type":"prometheus"}`))
			return
		}
		if !strings.HasSuffix(r.URL.Path, "/api/v1/query_range") {
			http.NotFound(w, r)
			return
		}
		_ = r.ParseForm()
		if strings.Contains(r.Form.Get("query"), "calls_total") {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"matrix","result":[]}}`))
	}))
	defer grafana.Close()

	result, err := renderServiceHealth(serviceHealthTestContext(t, grafana, "test-token"), ServiceHealthParams{DatasourceUID: "prom", ServiceName: "checkout"})
	require.NoError(t, err)
	got := result.StructuredContent.(ServiceHealthResult)
	assert.Equal(t, "unknown", got.Status)
	assert.Equal(t, "Service health is unavailable.", got.Summary)
	assert.Nil(t, got.Metrics.ErrorRatio.Value)
	assert.NotEmpty(t, got.Warnings)
}

func TestServiceMatcherEscapesLabelValues(t *testing.T) {
	namespace := `shop"},server="other`
	matcher := serviceMatcher(`checkout"} or vector(1)`, &namespace, "service", "service_namespace")
	assert.Equal(t, `service="checkout\"} or vector(1)",service_namespace="shop\"},server=\"other"`, matcher)
}
