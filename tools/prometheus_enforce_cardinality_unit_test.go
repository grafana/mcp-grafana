//go:build unit

package tools

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	mcpgrafana "github.com/grafana/mcp-grafana/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPrometheusEnforcementWithCardinalityGuardrail(t *testing.T) {
	for _, tc := range []struct {
		name      string
		mode      string
		expr      string
		wantError bool
		wantStats int64
	}{
		{name: "enforce blocks unscoped request", mode: "enforce", expr: `http_requests_total`, wantError: true, wantStats: 1},
		{name: "shadow keeps matcher on forwarded request", mode: "shadow", expr: `http_requests_total`, wantStats: 1},
		{name: "scoped request keeps both matchers", mode: "enforce", expr: `http_requests_total{job="api"}`},
		{name: "disabled guardrail still enforces matcher", mode: "off", expr: `http_requests_total`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var stats atomic.Int64
			queries := make(chan string, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/api/datasources/uid/prometheus":
					_, _ = w.Write([]byte(`{"uid":"prometheus","id":1,"type":"prometheus","name":"prometheus"}`))
				case "/api/datasources/proxy/uid/prometheus/api/v1/status/tsdb":
					stats.Add(1)
					_, _ = w.Write([]byte(cardinalityHighJSON))
				case "/api/ds/query":
					var payload struct {
						Queries []struct {
							Expr string `json:"expr"`
						} `json:"queries"`
					}
					if err := json.NewDecoder(r.Body).Decode(&payload); err != nil || len(payload.Queries) != 1 {
						t.Errorf("invalid query payload: %v", err)
						w.WriteHeader(http.StatusBadRequest)
						return
					}
					queries <- payload.Queries[0].Expr
					_, _ = w.Write([]byte(`{"results":{"A":{"frames":[]}}}`))
				default:
					t.Errorf("unexpected request path %s", r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			t.Cleanup(server.Close)
			matchers, err := ParseEnforcedMatchers(`namespace="prod"`)
			require.NoError(t, err)
			ctx := mcpgrafana.WithGrafanaConfig(mockDatasourcesCtx(server), mcpgrafana.GrafanaConfig{
				URL: server.URL, PrometheusEnforcedMatchers: matchers,
				PrometheusGuardrailMode: tc.mode, PrometheusGuardrailMaxMetricCardinality: 100000,
			})
			_, err = queryPrometheus(ctx, QueryPrometheusParams{
				DatasourceUID: "prometheus", Expr: tc.expr, QueryType: "instant", EndTime: "2026-11-11T00:00:00Z",
			})
			if tc.wantError {
				require.ErrorContains(t, err, "100001 series")
				select {
				case q := <-queries:
					t.Fatalf("blocked query reached Grafana: %q", q)
				default:
				}
			} else {
				require.NoError(t, err)
				select {
				case q := <-queries:
					assert.Contains(t, q, `namespace="prod"`)
					if tc.expr == `http_requests_total{job="api"}` {
						assert.Contains(t, q, `job="api"`)
					}
				default:
					t.Fatal("query did not reach Grafana")
				}
			}
			assert.Equal(t, tc.wantStats, stats.Load())
		})
	}
}
