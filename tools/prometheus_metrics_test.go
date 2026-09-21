//go:build unit

package tools

import (
	"compress/gzip"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/grafana/grafana-openapi-client-go/models"
	mcpgrafana "github.com/grafana/mcp-grafana"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	otelprom "go.opentelemetry.io/otel/exporters/prometheus"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
)

func TestPrometheusDiscoveryResponseHistograms(t *testing.T) {
	const body = `{"status":"success","data":["a_metric","b_metric","c_metric"]}`
	for _, encoding := range []string{"plain", "chunked", "gzip"} {
		t.Run(encoding, func(t *testing.T) {
			registry := prometheus.NewRegistry()
			exporter, err := otelprom.New(otelprom.WithRegisterer(registry))
			require.NoError(t, err)
			provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(exporter))
			t.Cleanup(func() { require.NoError(t, provider.Shutdown(t.Context())) })

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/api/datasources/uid/test":
					_ = json.NewEncoder(w).Encode(&models.DataSource{UID: "test", Type: "prometheus"})
				case "/api/datasources/uid/test/resources/api/v1/label/__name__/values":
					assert.Equal(t, "1", r.URL.Query().Get("limit"))
					// Ignore the requested limit: telemetry must measure everything received, not the final page.
					switch encoding {
					case "gzip":
						w.Header().Set("Content-Encoding", "gzip")
						zw := gzip.NewWriter(w)
						_, _ = zw.Write([]byte(body))
						require.NoError(t, zw.Close())
					case "chunked":
						w.(http.Flusher).Flush()
						_, _ = w.Write([]byte(body))
					default:
						_, _ = w.Write([]byte(body))
					}
				default:
					t.Errorf("unexpected request: %s", r.URL)
					http.NotFound(w, r)
				}
			}))
			defer server.Close()

			ctx := mockDatasourcesCtx(server)
			config := mcpgrafana.GrafanaConfigFromContext(ctx)
			config.MeterProvider = provider
			ctx = mcpgrafana.WithGrafanaConfig(ctx, config)
			names, err := listPrometheusMetricNames(ctx, ListPrometheusMetricNamesParams{DatasourceUID: "test", Limit: 1})
			require.NoError(t, err)
			assert.Equal(t, []string{"a_metric"}, names)

			families, err := registry.Gather()
			require.NoError(t, err)
			found := map[string]float64{}
			for _, family := range families {
				if family.GetName() == "mcp_prometheus_metric_names_response_size_bytes" || family.GetName() == "mcp_prometheus_metric_names_count" {
					require.Len(t, family.Metric, 1)
					histogram := family.Metric[0].GetHistogram()
					require.NotNil(t, histogram)
					assert.EqualValues(t, 1, histogram.GetSampleCount())
					found[family.GetName()] = histogram.GetSampleSum()
				}
			}
			assert.Equal(t, map[string]float64{
				"mcp_prometheus_metric_names_response_size_bytes": float64(len(body)),
				"mcp_prometheus_metric_names_count":               3,
			}, found)
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
			provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(exporter))
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
				if family.GetName() == "mcp_prometheus_metric_names_response_size_bytes" {
					for _, sample := range family.Metric {
						samples += int(sample.GetHistogram().GetSampleCount())
					}
				}
			}
			assert.Equal(t, tc.wantSamples, samples)
		})
	}
}
