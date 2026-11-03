//go:build unit

package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/grafana/grafana-openapi-client-go/models"
	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"github.com/grafana/grafana-plugin-sdk-go/data"
	mcpgrafana "github.com/grafana/mcp-grafana/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func promEnforceContext(matchers string) (context.Context, error) {
	parsed, err := ParseEnforcedMatchers(matchers)
	if err != nil {
		return nil, err
	}
	gc := mcpgrafana.GrafanaConfig{
		PrometheusEnforcedMatchers: parsed,
	}
	return mcpgrafana.WithGrafanaConfig(context.Background(), gc), nil
}

func TestEnforcePromQL(t *testing.T) {
	ctx, err := promEnforceContext(`namespace="prod"`)
	require.NoError(t, err)

	t.Run("disabled passes query through unchanged", func(t *testing.T) {
		out, err := enforcePromQL(context.Background(), `up`, time.Hour, 60)
		require.NoError(t, err)
		assert.Equal(t, `up`, out)
	})

	t.Run("bare metric", func(t *testing.T) {
		out, err := enforcePromQL(ctx, `up`, time.Hour, 60)
		require.NoError(t, err)
		assert.Equal(t, `up{namespace="prod"}`, out)
	})

	t.Run("metric with __name__ equal", func(t *testing.T) {
		out, err := enforcePromQL(ctx, `{__name__="up"}`, time.Hour, 60)
		require.NoError(t, err)
		assert.Equal(t, `{__name__="up",namespace="prod"}`, out)
	})

	t.Run("metric with __name__ regex", func(t *testing.T) {
		out, err := enforcePromQL(ctx, `{__name__=~"up|version"}`, time.Hour, 60)
		require.NoError(t, err)
		assert.Equal(t, `{__name__=~"up|version",namespace="prod"}`, out)
	})

	t.Run("selector with existing label", func(t *testing.T) {
		out, err := enforcePromQL(ctx, `up{job="api"}`, time.Hour, 60)
		require.NoError(t, err)
		assert.Equal(t, `up{job="api",namespace="prod"}`, out)
	})

	t.Run("selector with multiple existing labels", func(t *testing.T) {
		out, err := enforcePromQL(ctx, `http_requests_total{job="api",method="GET",handler="/health"}`, time.Hour, 60)
		require.NoError(t, err)
		assert.Equal(t, `http_requests_total{handler="/health",job="api",method="GET",namespace="prod"}`, out)
	})

	t.Run("range vector in rate", func(t *testing.T) {
		out, err := enforcePromQL(ctx, `rate(http_requests_total[5m]) > 10`, time.Hour, 60)
		require.NoError(t, err)
		assert.Equal(t, `rate(http_requests_total{namespace="prod"}[5m]) > 10`, out)
	})

	t.Run("subquery and aggregations by label", func(t *testing.T) {
		out, err := enforcePromQL(ctx, `sum by (job) (rate(http_requests_total{status=~"5.."}[5m]))`, time.Hour, 60)
		require.NoError(t, err)
		assert.Equal(t, `sum by (job) (rate(http_requests_total{namespace="prod",status=~"5.."}[5m]))`, out)
	})

	t.Run("aggregation without label", func(t *testing.T) {
		out, err := enforcePromQL(ctx, `sum without (instance) (rate(http_requests_total[5m]))`, time.Hour, 60)
		require.NoError(t, err)
		assert.Equal(t, `sum without (instance) (rate(http_requests_total{namespace="prod"}[5m]))`, out)
	})

	t.Run("nested subquery", func(t *testing.T) {
		out, err := enforcePromQL(ctx, `deriv(rate(http_requests_total[5m])[30m:1m])[1h:5m]`, time.Hour, 60)
		require.NoError(t, err)
		assert.Equal(t, `deriv(rate(http_requests_total{namespace="prod"}[5m])[30m:1m])[1h:5m]`, out)
	})

	t.Run("histogram_quantile", func(t *testing.T) {
		out, err := enforcePromQL(ctx, `histogram_quantile(0.95, sum(rate(http_duration_seconds_bucket[5m])) by (le))`, time.Hour, 60)
		require.NoError(t, err)
		assert.Equal(t, `histogram_quantile(0.95, sum by (le) (rate(http_duration_seconds_bucket{namespace="prod"}[5m])))`, out)
	})

	t.Run("binary expression injects into both sides", func(t *testing.T) {
		out, err := enforcePromQL(ctx, `foo + bar{a="b"}`, time.Hour, 60)
		require.NoError(t, err)
		assert.Equal(t, `foo{namespace="prod"} + bar{a="b",namespace="prod"}`, out)
	})

	t.Run("binary expression with on and group_left", func(t *testing.T) {
		out, err := enforcePromQL(ctx, `rate(http_requests[5m]) * on(instance) group_left(role) node_role`, time.Hour, 60)
		require.NoError(t, err)
		assert.Equal(t, `rate(http_requests{namespace="prod"}[5m]) * on (instance) group_left (role) node_role{namespace="prod"}`, out)
	})

	t.Run("offset and at modifiers", func(t *testing.T) {
		out, err := enforcePromQL(ctx, `foo offset 5m`, time.Hour, 60)
		require.NoError(t, err)
		assert.Equal(t, `foo{namespace="prod"} offset 5m`, out)
	})

	t.Run("at modifier with timestamp", func(t *testing.T) {
		out, err := enforcePromQL(ctx, `foo @ 1600000000`, time.Hour, 60)
		require.NoError(t, err)
		assert.Equal(t, `foo{namespace="prod"} @ 1600000000.000`, out)
	})

	t.Run("range vector with offset", func(t *testing.T) {
		out, err := enforcePromQL(ctx, `rate(http_requests_total[5m] offset 10m)`, time.Hour, 60)
		require.NoError(t, err)
		assert.Equal(t, `rate(http_requests_total{namespace="prod"}[5m] offset 10m)`, out)
	})

	t.Run("label_replace and absent functions", func(t *testing.T) {
		out, err := enforcePromQL(ctx, `label_replace(up{job="api"}, "env", "$1", "job", "(.*)")`, time.Hour, 60)
		require.NoError(t, err)
		assert.Equal(t, `label_replace(up{job="api",namespace="prod"}, "env", "$1", "job", "(.*)")`, out)

		outAbsent, err := enforcePromQL(ctx, `absent(up{job="api"})`, time.Hour, 60)
		require.NoError(t, err)
		assert.Equal(t, `absent(up{job="api",namespace="prod"})`, outAbsent)
	})

	t.Run("topk and changes functions", func(t *testing.T) {
		out, err := enforcePromQL(ctx, `topk(5, rate(node_cpu_seconds_total{mode!="idle"}[5m]))`, time.Hour, 60)
		require.NoError(t, err)
		assert.Equal(t, `topk(5, rate(node_cpu_seconds_total{mode!="idle",namespace="prod"}[5m]))`, out)

		outChanges, err := enforcePromQL(ctx, `changes(process_start_time_seconds[1d])`, time.Hour, 60)
		require.NoError(t, err)
		assert.Equal(t, `changes(process_start_time_seconds{namespace="prod"}[1d])`, outChanges)
	})

	t.Run("scalar queries untouched", func(t *testing.T) {
		out, err := enforcePromQL(ctx, `1 + 2`, time.Hour, 60)
		require.NoError(t, err)
		assert.Equal(t, `1 + 2`, out)
	})

	t.Run("vector and time functions", func(t *testing.T) {
		out, err := enforcePromQL(ctx, `vector(1)`, time.Hour, 60)
		require.NoError(t, err)
		assert.Equal(t, `vector(1)`, out)

		outTime, err := enforcePromQL(ctx, `time()`, time.Hour, 60)
		require.NoError(t, err)
		assert.Equal(t, `time()`, outTime)
	})

	t.Run("comments and string literals with braces are parsed safely", func(t *testing.T) {
		out, err := enforcePromQL(ctx, "# comment with {braces}\nup", time.Hour, 60)
		require.NoError(t, err)
		assert.Equal(t, `up{namespace="prod"}`, out)

		outStr, err := enforcePromQL(ctx, `label_replace(up, "new", "val{with_braces}", "job", ".*")`, time.Hour, 60)
		require.NoError(t, err)
		assert.Equal(t, `label_replace(up{namespace="prod"}, "new", "val{with_braces}", "job", ".*")`, outStr)
	})

	t.Run("conflicting user matcher narrows to empty set", func(t *testing.T) {
		out, err := enforcePromQL(ctx, `up{namespace="dev"}`, time.Hour, 60)
		require.NoError(t, err)
		// Both matchers exist on the selector; Prometheus evaluates conflicting equal matchers to empty.
		assert.Equal(t, `up{namespace="dev",namespace="prod"}`, out)
	})

	t.Run("user blanket regex is narrowed by enforced matcher", func(t *testing.T) {
		out, err := enforcePromQL(ctx, `up{namespace=~".*"}`, time.Hour, 60)
		require.NoError(t, err)
		assert.Equal(t, `up{namespace="prod",namespace=~".*"}`, out)
	})

	t.Run("grafana macros expanded and enforced", func(t *testing.T) {
		out, err := enforcePromQL(ctx, `rate(x[$__rate_interval])`, time.Hour, 15)
		require.NoError(t, err)
		assert.Equal(t, `rate(x{namespace="prod"}[1m])`, out)

		outRange, err := enforcePromQL(ctx, `rate(x[$__range])`, time.Hour, 60)
		require.NoError(t, err)
		assert.Equal(t, `rate(x{namespace="prod"}[1h])`, outRange)
	})

	t.Run("multiple enforced matchers", func(t *testing.T) {
		multiCtx, err := promEnforceContext(`namespace="prod", cluster=~"us-.*"`)
		require.NoError(t, err)
		out, err := enforcePromQL(multiCtx, `up{job="api"}`, time.Hour, 60)
		require.NoError(t, err)
		assert.Equal(t, `up{cluster=~"us-.*",job="api",namespace="prod"}`, out)
	})

	t.Run("negative enforced matcher", func(t *testing.T) {
		negCtx, err := promEnforceContext(`namespace!="vault"`)
		require.NoError(t, err)
		out, err := enforcePromQL(negCtx, `up{job="api"}`, time.Hour, 60)
		require.NoError(t, err)
		assert.Equal(t, `up{job="api",namespace!="vault"}`, out)
	})

	t.Run("fails closed on unparseable queries", func(t *testing.T) {
		unparseable := []string{
			`rate(}`,
			`rate(`,
			`up{`,
			`up{job=}`,
			`up{job="api"`,
			`foo / * bar`,
			`sum by`,
			`()`,
			``,
			`   `,
		}
		for _, q := range unparseable {
			_, err := enforcePromQL(ctx, q, time.Hour, 60)
			require.Error(t, err, "query %q should fail to parse", q)
			assert.Contains(t, err.Error(), "could not parse PromQL query")
		}
	})
}

func TestQueryPrometheus_EnforcedMatchersApplied(t *testing.T) {
	var receivedExpr string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/datasources/uid/prom-uid":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(models.DataSource{
				UID:  "prom-uid",
				Type: "prometheus",
				Name: "Prometheus",
			})
		case "/api/ds/query":
			var payload map[string]interface{}
			_ = json.NewDecoder(r.Body).Decode(&payload)
			if queries, ok := payload["queries"].([]interface{}); ok && len(queries) > 0 {
				if q, ok := queries[0].(map[string]interface{}); ok {
					receivedExpr, _ = q["expr"].(string)
				}
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(backend.QueryDataResponse{
				Responses: backend.Responses{
					"A": backend.DataResponse{
						Frames: data.Frames{
							data.NewFrame("",
								data.NewField("Time", nil, []time.Time{time.Now()}),
								data.NewField("Value", nil, []float64{1}),
							),
						},
					},
				},
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	matchers, err := ParseEnforcedMatchers(`namespace="prod"`)
	require.NoError(t, err)

	ctx := mcpgrafana.WithGrafanaConfig(mockDatasourcesCtx(server), mcpgrafana.GrafanaConfig{
		URL:                        server.URL,
		PrometheusEnforcedMatchers: matchers,
	})

	t.Run("instant query receives enforced matcher", func(t *testing.T) {
		receivedExpr = ""
		_, err := queryPrometheus(ctx, QueryPrometheusParams{
			DatasourceUID: "prom-uid",
			Expr:          "up",
			QueryType:     "instant",
			EndTime:       "now",
		})
		require.NoError(t, err)
		assert.Equal(t, `up{namespace="prod"}`, receivedExpr)
	})

	t.Run("range query receives enforced matcher", func(t *testing.T) {
		receivedExpr = ""
		_, err := queryPrometheus(ctx, QueryPrometheusParams{
			DatasourceUID: "prom-uid",
			Expr:          "rate(http_requests_total[5m]) > 10",
			QueryType:     "range",
			StartTime:     "now-1h",
			EndTime:       "now",
			StepSeconds:   60,
		})
		require.NoError(t, err)
		assert.Equal(t, `rate(http_requests_total{namespace="prod"}[5m]) > 10`, receivedExpr)
	})

	t.Run("query_prometheus_histogram receives enforced matcher", func(t *testing.T) {
		receivedExpr = ""
		res, err := queryPrometheusHistogram(ctx, QueryPrometheusHistogramParams{
			DatasourceUID: "prom-uid",
			Metric:        "http_duration",
			Percentile:    95,
			Labels:        `job="api"`,
			StartTime:     "now-1h",
			EndTime:       "now",
			StepSeconds:   60,
		})
		require.NoError(t, err)
		require.NotNil(t, res)
		assert.Contains(t, receivedExpr, `namespace="prod"`)
		assert.Contains(t, receivedExpr, `job="api"`)
	})

	t.Run("fails closed on unparseable query without HTTP request", func(t *testing.T) {
		receivedExpr = ""
		_, err := queryPrometheus(ctx, QueryPrometheusParams{
			DatasourceUID: "prom-uid",
			Expr:          "rate(",
			QueryType:     "instant",
			EndTime:       "now",
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "could not parse PromQL query")
		assert.Empty(t, receivedExpr, "unparseable query must not reach Grafana")
	})
}
