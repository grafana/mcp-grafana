//go:build unit

package tools

import (
	"compress/gzip"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/grafana/grafana-openapi-client-go/models"
	mcpgrafana "github.com/grafana/mcp-grafana"
	"github.com/grafana/mcp-grafana/observability"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	otelprom "go.opentelemetry.io/otel/exporters/prometheus"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
)

func TestMetricNamesDiscoveryResponseHistograms(t *testing.T) {
	for _, tc := range []struct {
		backend        string
		datasourceType string
		body           string
		regex          string
		expected       string
	}{
		{backend: "prometheus", datasourceType: "prometheus", body: `{"status":"success","data":["a_metric","b_metric","c_metric"]}`, expected: "a_metric"},
		{backend: "cloud_monitoring", datasourceType: "stackdriver", body: `[{"type":"a_other","description":"a descriptor"},{"type":"b_metric"},{"type":"c_metric"}]`, regex: "metric", expected: "b_metric"},
	} {
		t.Run(tc.backend, func(t *testing.T) {
			for _, encoding := range []string{"plain", "chunked", "gzip"} {
				t.Run(encoding, func(t *testing.T) {
					registry := prometheus.NewRegistry()
					exporter, err := otelprom.New(otelprom.WithRegisterer(registry))
					require.NoError(t, err)
					provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(exporter), sdkmetric.WithView(observability.MetricNamesHistogramView()))
					t.Cleanup(func() { require.NoError(t, provider.Shutdown(t.Context())) })

					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						w.Header().Set("Content-Type", "application/json")
						switch r.URL.Path {
						case "/api/datasources/uid/test":
							_ = json.NewEncoder(w).Encode(&models.DataSource{UID: "test", Type: tc.datasourceType, JSONData: map[string]interface{}{"defaultProject": "project"}})
							return
						case "/api/datasources/uid/test/resources/api/v1/label/__name__/values":
							assert.Equal(t, "1", r.URL.Query().Get("limit"))
						case "/api/datasources/uid/test/resources/metricDescriptors/v3/projects/project/metricDescriptors":
							assert.Empty(t, r.URL.Query())
						default:
							t.Errorf("unexpected request: %s", r.URL)
							http.NotFound(w, r)
							return
						}
						// Return the whole catalog: telemetry must measure everything received, not the final page.
						switch encoding {
						case "gzip":
							w.Header().Set("Content-Encoding", "gzip")
							zw := gzip.NewWriter(w)
							_, _ = zw.Write([]byte(tc.body))
							require.NoError(t, zw.Close())
						case "chunked":
							w.(http.Flusher).Flush()
							_, _ = w.Write([]byte(tc.body))
						default:
							_, _ = w.Write([]byte(tc.body))
						}
					}))
					defer server.Close()

					ctx := mockDatasourcesCtx(server)
					config := mcpgrafana.GrafanaConfigFromContext(ctx)
					config.MeterProvider = provider
					ctx = mcpgrafana.WithGrafanaConfig(ctx, config)
					names, err := listPrometheusMetricNames(ctx, ListPrometheusMetricNamesParams{DatasourceUID: "test", Limit: 1, Regex: tc.regex})
					require.NoError(t, err)
					assert.Equal(t, []string{tc.expected}, names)
					if tc.backend == "cloud_monitoring" {
						// This uses the same descriptor endpoint but must not add discovery samples.
						metadata, err := listPrometheusMetricMetadata(ctx, ListPrometheusMetricMetadataParams{DatasourceUID: "test"})
						require.NoError(t, err)
						assert.Len(t, metadata, 3)
					}

					families, err := registry.Gather()
					require.NoError(t, err)
					found := map[string]float64{}
					for _, family := range families {
						if family.GetName() == "mcp_metric_names_response_size_bytes" || family.GetName() == "mcp_metric_names_count" {
							require.Len(t, family.Metric, 1)
							labels := map[string]string{}
							for _, label := range family.Metric[0].Label {
								labels[label.GetName()] = label.GetValue()
							}
							assert.Equal(t, tc.backend, labels["backend"])
							histogram := family.Metric[0].GetHistogram()
							require.NotNil(t, histogram)
							require.NotNil(t, histogram.Schema, "expected a native histogram")
							assert.NotEmpty(t, histogram.PositiveSpan)
							assert.Empty(t, histogram.Bucket, "must not export classic buckets")
							assert.EqualValues(t, 1, histogram.GetSampleCount())
							found[family.GetName()] = histogram.GetSampleSum()
						}
					}
					assert.Equal(t, map[string]float64{
						"mcp_metric_names_response_size_bytes": float64(len(tc.body)),
						"mcp_metric_names_count":               3,
					}, found)
				})
			}
		})
	}
}

func TestPrometheusResponseHistogramOnlyCountsCompleteSuccessfulBodies(t *testing.T) {
	for _, tc := range []struct {
		name        string
		path        string
		status      int
		incomplete  bool
		wantSamples int
	}{
		{name: "complete", path: "/api/v1/label/__name__/values", status: 200, wantSamples: 1},
		{name: "HTTP error", path: "/api/v1/label/__name__/values", status: 500},
		{name: "incomplete", path: "/api/v1/label/__name__/values", status: 200, incomplete: true},
		{name: "other endpoint", path: "/api/v1/label/job/values", status: 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			registry := prometheus.NewRegistry()
			exporter, err := otelprom.New(otelprom.WithRegisterer(registry))
			require.NoError(t, err)
			provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(exporter), sdkmetric.WithView(observability.MetricNamesHistogramView()))
			t.Cleanup(func() { require.NoError(t, provider.Shutdown(t.Context())) })
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if tc.incomplete {
					w.Header().Set("Content-Length", "1000")
				}
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte("[]"))
			}))
			defer server.Close()
			ctx := mcpgrafana.WithGrafanaConfig(t.Context(), mcpgrafana.GrafanaConfig{MeterProvider: provider})
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+tc.path, nil)
			require.NoError(t, err)
			transport := &prometheusMetricNamesTransport{underlying: http.DefaultTransport}
			resp, err := transport.RoundTrip(req)
			require.NoError(t, err)
			_, err = io.ReadAll(resp.Body)
			if tc.incomplete {
				require.ErrorIs(t, err, io.ErrUnexpectedEOF)
			} else {
				require.NoError(t, err)
				// Another EOF read must not record the same response twice.
				_, err = io.ReadAll(resp.Body)
				require.NoError(t, err)
			}
			require.NoError(t, resp.Body.Close())
			families, err := registry.Gather()
			require.NoError(t, err)
			samples := 0
			for _, family := range families {
				if family.GetName() == "mcp_metric_names_response_size_bytes" {
					for _, sample := range family.Metric {
						samples += int(sample.GetHistogram().GetSampleCount())
					}
				}
			}
			assert.Equal(t, tc.wantSamples, samples)
		})
	}
}

func TestCloudMonitoringDiscoveryHistogramFailures(t *testing.T) {
	for _, tc := range []struct {
		name       string
		body       string
		status     int
		incomplete bool
		wantError  bool
		want       map[string]float64
	}{
		{name: "empty catalog", body: "[]", status: 200, want: map[string]float64{"mcp_metric_names_response_size_bytes": 2, "mcp_metric_names_count": 0}},
		{name: "invalid JSON", body: "bad", status: 200, wantError: true, want: map[string]float64{"mcp_metric_names_response_size_bytes": 3}},
		{name: "HTTP error", body: "[]", status: 500, wantError: true, want: map[string]float64{}},
		{name: "incomplete response", body: "[]", status: 200, incomplete: true, wantError: true, want: map[string]float64{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			registry := prometheus.NewRegistry()
			exporter, err := otelprom.New(otelprom.WithRegisterer(registry))
			require.NoError(t, err)
			provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(exporter), sdkmetric.WithView(observability.MetricNamesHistogramView()))
			t.Cleanup(func() { require.NoError(t, provider.Shutdown(t.Context())) })
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if tc.incomplete {
					w.Header().Set("Content-Length", "1000")
				}
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()
			backend := &cloudMonitoringBackend{httpClient: server.Client(), baseURL: server.URL, datasourceUID: "test", defaultProject: "project"}
			ctx := mcpgrafana.WithGrafanaConfig(t.Context(), mcpgrafana.GrafanaConfig{MeterProvider: provider})
			_, err = backend.MetricNames(ctx, nil, 1, time.Time{}, time.Time{})
			if tc.wantError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			families, err := registry.Gather()
			require.NoError(t, err)
			found := map[string]float64{}
			for _, family := range families {
				if family.GetName() == "mcp_metric_names_response_size_bytes" || family.GetName() == "mcp_metric_names_count" {
					require.Len(t, family.Metric, 1)
					histogram := family.Metric[0].GetHistogram()
					require.EqualValues(t, 1, histogram.GetSampleCount())
					found[family.GetName()] = histogram.GetSampleSum()
				}
			}
			assert.Equal(t, tc.want, found)
		})
	}
}
