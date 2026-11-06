//go:build unit

package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/grafana/grafana-openapi-client-go/models"
	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"github.com/grafana/grafana-plugin-sdk-go/data"
	mcpgrafana "github.com/grafana/mcp-grafana/v2"
	"github.com/prometheus/prometheus/model/labels"
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

func TestPrometheusDiscovery_EnforcedMatchers(t *testing.T) {
	for _, policy := range []string{`namespace="prod"`, `namespace!~"vault|payments"`} {
		for _, tool := range []string{"label_names", "label_values", "metric_names", "metric_metadata"} {
			t.Run(policy+"/"+tool, func(t *testing.T) {
				var discoveryRequests int
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					if r.URL.Path == "/api/datasources/uid/prom-uid" {
						_ = json.NewEncoder(w).Encode(models.DataSource{UID: "prom-uid", Type: "prometheus"})
						return
					}
					discoveryRequests++
					if strings.HasSuffix(r.URL.Path, "/metadata") {
						_, _ = w.Write([]byte(`{"status":"success","data":{"vault_secret_total":[{"type":"counter","help":"vault-only metadata"}]}}`))
						return
					}
					if !assert.NoError(t, r.ParseForm()) {
						http.Error(w, "invalid form", http.StatusBadRequest)
						return
					}
					selectors := r.Form["match[]"]
					assert.NotEmpty(t, selectors, "discovery must not be unscoped")
					for _, selector := range selectors {
						matchers, err := promqlParser.ParseMetricSelector(selector)
						if !assert.NoError(t, err) {
							continue
						}
						assert.Contains(t, selector, policy, "each OR branch must include the policy")
						assert.False(t, labels.Selector(matchers).Matches(labels.FromStrings("__name__", "vault_secret_total", "namespace", "vault")))
					}
					_, _ = w.Write([]byte(`{"status":"success","data":[]}`))
				}))
				defer server.Close()
				matchers, err := ParseEnforcedMatchers(policy)
				require.NoError(t, err)
				ctx := mcpgrafana.WithGrafanaConfig(mockDatasourcesCtx(server), mcpgrafana.GrafanaConfig{
					URL: server.URL, PrometheusEnforcedMatchers: matchers,
				})
				excluded := []Selector{
					{Filters: []LabelMatcher{{Name: "namespace", Type: "=", Value: "vault"}}},
					{Filters: []LabelMatcher{{Name: "namespace", Type: "=", Value: "payments"}}},
				}
				switch tool {
				case "label_names":
					_, err = listPrometheusLabelNames(ctx, ListPrometheusLabelNamesParams{DatasourceUID: "prom-uid", Matches: excluded})
				case "label_values":
					_, err = listPrometheusLabelValues(ctx, ListPrometheusLabelValuesParams{DatasourceUID: "prom-uid", LabelName: "secret", Matches: excluded})
				case "metric_names":
					_, err = listPrometheusMetricNames(ctx, ListPrometheusMetricNamesParams{DatasourceUID: "prom-uid"})
				case "metric_metadata":
					_, err = listPrometheusMetricMetadata(ctx, ListPrometheusMetricMetadataParams{DatasourceUID: "prom-uid", Metric: "vault_secret_total"})
					require.ErrorContains(t, err, "cannot be scoped")
					assert.Zero(t, discoveryRequests, "metadata must fail closed before querying the endpoint")
					return
				}
				require.NoError(t, err)
				assert.Equal(t, 1, discoveryRequests)
			})
		}
	}
}

func TestEnforcePrometheusSelectors(t *testing.T) {
	t.Run("disabled preserves input", func(t *testing.T) {
		for _, selectors := range [][]string{nil, {}, {`not a selector`}} {
			got, err := enforcePrometheusSelectors(context.Background(), selectors)
			require.NoError(t, err)
			assert.Equal(t, selectors, got)
		}
	})
	for _, policy := range []string{`namespace="prod",cluster=~"us-.*"`, `namespace!~"vault|payments"`, `namespace!="vault"`} {
		for _, selectors := range [][]string{nil, {}, {`{}`}, {`{namespace="vault"}`, `{job=~".*"}`}} {
			t.Run(policy+"/"+strings.Join(selectors, "|"), func(t *testing.T) {
				ctx, err := promEnforceContext(policy)
				require.NoError(t, err)
				original := append([]string(nil), selectors...)
				got, err := enforcePrometheusSelectors(ctx, selectors)
				require.NoError(t, err)
				require.NotEmpty(t, got)
				assert.Equal(t, original, append([]string(nil), selectors...), "caller input must not be mutated")
				for _, selector := range got {
					_, err := promGuardParser.ParseExpr(selector)
					require.NoError(t, err, "exclusion-only policies need a valid standalone selector")
					matchers, err := promqlParser.ParseMetricSelector(selector)
					require.NoError(t, err)
					assert.False(t, labels.Selector(matchers).Matches(labels.FromStrings("__name__", "vault_secret_total", "namespace", "vault", "cluster", "us-east")))
					for _, matcher := range prometheusEnforcedMatchers(ctx) {
						assert.Contains(t, selector, matcher.String())
					}
				}
			})
		}
	}
	t.Run("malformed selector fails closed", func(t *testing.T) {
		ctx, err := promEnforceContext(`namespace="prod"`)
		require.NoError(t, err)
		for _, selector := range []string{`{job=}`, `{namespace=~"["}`, `up or secret`, `up[5m]`} {
			got, err := enforcePrometheusSelectors(ctx, []string{selector})
			require.ErrorContains(t, err, "could not parse Prometheus selector")
			assert.Nil(t, got)
		}
	})
}

func TestPrometheusDiscovery_CloudMonitoringFailsClosed(t *testing.T) {
	ctx, err := promEnforceContext(`namespace="prod"`)
	require.NoError(t, err)
	// An uninitialized backend would panic on HTTP access: refusal must happen first.
	b := &cloudMonitoringBackend{defaultProject: "test-project"}
	t.Run("label names", func(t *testing.T) {
		_, err := b.LabelNames(ctx, nil, time.Time{}, time.Time{})
		require.ErrorContains(t, err, "cannot be scoped")
	})
	for _, label := range []string{"__name__", "namespace"} {
		t.Run("label values/"+label, func(t *testing.T) {
			_, err := b.LabelValues(ctx, label, nil, time.Time{}, time.Time{})
			require.ErrorContains(t, err, "cannot be scoped")
		})
	}
	t.Run("metric names", func(t *testing.T) {
		_, err := b.MetricNames(ctx, nil, 10, time.Time{}, time.Time{})
		require.ErrorContains(t, err, "cannot be scoped")
	})
	t.Run("metric metadata", func(t *testing.T) {
		_, err := b.MetricMetadata(ctx, "", 10)
		require.ErrorContains(t, err, "cannot be scoped")
	})
}

func TestPrometheusDiscovery_MetricNamesPreservesFilters(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.NoError(t, r.ParseForm())
		assert.Equal(t, "4", r.Form.Get("limit"))
		assert.Equal(t, "1700000000", r.Form.Get("start"))
		assert.Equal(t, "1700003600", r.Form.Get("end"))
		selector := r.Form.Get("match[]")
		assert.Contains(t, selector, `namespace="prod"`)
		assert.Contains(t, selector, `__name__=~`)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"success","data":["http_a","http_b","http_c","http_d"]}`))
	}))
	defer server.Close()
	ctx, err := promEnforceContext(`namespace="prod"`)
	require.NoError(t, err)
	got, err := newTestPrometheusBackend(t, server).MetricNames(ctx, regexp.MustCompile("http_"), 4, time.Unix(1700000000, 0), time.Unix(1700003600, 0))
	require.NoError(t, err)
	assert.Equal(t, []string{"http_a", "http_b", "http_c", "http_d"}, got)
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
