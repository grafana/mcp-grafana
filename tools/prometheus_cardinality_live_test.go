//go:build integration

package tools

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	mcpgrafana "github.com/grafana/mcp-grafana/v2"
	"github.com/prometheus/common/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Uses the real Grafana and Prometheus services from docker-compose.yaml.
// The forwarding proxy only counts dispatches and injects one metadata failure;
// datasource lookup, TSDB JSON and query results otherwise come from the services.
func TestPrometheusCardinalityLive(t *testing.T) {
	original := newTestContext()
	target, err := url.Parse(mcpgrafana.GrafanaConfigFromContext(original).URL)
	require.NoError(t, err)
	proxy := httputil.NewSingleHostReverseProxy(target)
	var statsCalls, queryCalls atomic.Int64
	var unavailable atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/api/v1/status/tsdb") {
			statsCalls.Add(1)
			if unavailable.Load() {
				http.Error(w, "injected unsupported TSDB endpoint", http.StatusNotFound)
				return
			}
		}
		if r.URL.Path == "/api/ds/query" {
			queryCalls.Add(1)
		}
		proxy.ServeHTTP(w, r)
	}))
	defer server.Close()
	cfg := mcpgrafana.GrafanaConfigFromContext(original)
	cfg.URL = server.URL
	cfg.Debug = false
	bounded, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	ctx := mcpgrafana.WithGrafanaConfig(bounded, cfg)
	ctx = mcpgrafana.WithGrafanaClient(ctx, mcpgrafana.NewGrafanaClient(ctx, cfg.URL, cfg.APIKey, cfg.BasicAuth))
	backend, err := backendForDatasource(ctx, "prometheus")
	require.NoError(t, err)
	b, ok := backend.(*prometheusBackend)
	require.True(t, ok)
	stats, err := b.fetchCardinality(ctx)
	require.NoError(t, err, "Grafana must proxy the real Prometheus TSDB response")
	// Discover a current multi-series metric rather than depending on a fixed
	// Prometheus version's self-metric counts or increasing production limits.
	names := make([]string, 0, len(stats))
	for name, count := range stats {
		if count > 1 {
			names = append(names, name)
		}
	}
	sort.Slice(names, func(i, j int) bool {
		if stats[names[i]] != stats[names[j]] {
			return stats[names[i]] > stats[names[j]]
		}
		return names[i] < names[j]
	})
	require.NotEmpty(t, names, "integration fixture must contain a multi-series metric in TSDB top10")
	metric := names[0]
	expr := "{__name__=" + strconv.Quote(metric) + "}"
	args := QueryPrometheusParams{DatasourceUID: "prometheus", Expr: expr, QueryType: "instant", EndTime: "now"}
	value, err := queryPrometheus(ctx, args)
	require.NoError(t, err)
	vector, ok := value.(model.Vector)
	require.True(t, ok)
	require.NotEmpty(t, vector)
	var scopeName, scopeValue string
	for _, name := range promCardinalityDefaultScope {
		for _, sample := range vector {
			if value := sample.Metric[model.LabelName(name)]; value != "" {
				scopeName, scopeValue = name, string(value)
				break
			}
		}
		if scopeName != "" {
			break
		}
	}
	require.NotEmpty(t, scopeName)
	// A low threshold avoids flaking as live head statistics change.
	cfg.PrometheusGuardrailMaxMetricCardinality = 1
	cfg.PrometheusGuardrailCardinalityCacheTTL = time.Minute
	t.Logf("real TSDB metric=%s head_series=%d threshold=%d scope_label=%s", metric, stats[metric], cfg.PrometheusGuardrailMaxMetricCardinality, scopeName)
	// Each scenario owns a snapshot and checks deltas, so it also works when
	// selected individually with go test -run TestPrometheusCardinalityLive/name.
	var scenario int
	check := func(t *testing.T, mode string, wantStats, wantQueries int64, run func(context.Context)) {
		t.Helper()
		scenario++
		c := cfg
		c.PrometheusGuardrailMode = mode
		c.PrometheusGuardrailCardinalityCacheTTL = time.Minute + time.Duration(scenario)*time.Second
		beforeStats, beforeQueries := statsCalls.Load(), queryCalls.Load()
		run(mcpgrafana.WithGrafanaConfig(ctx, c))
		assert.Equal(t, wantStats, statsCalls.Load()-beforeStats)
		assert.Equal(t, wantQueries, queryCalls.Load()-beforeQueries)
	}
	t.Run("enforce and cache", func(t *testing.T) {
		check(t, "enforce", 1, 0, func(ctx context.Context) {
			for range 2 {
				_, err := queryPrometheus(ctx, args)
				require.ErrorContains(t, err, "Prometheus query guardrail")
				require.ErrorContains(t, err, "TSDB head statistics")
			}
		})
	})
	t.Run("shadow executes real query", func(t *testing.T) {
		check(t, "shadow", 1, 1, func(ctx context.Context) {
			value, err := queryPrometheus(ctx, args)
			require.NoError(t, err)
			assert.NotEmpty(t, value.(model.Vector))
		})
	})
	t.Run("exact scope executes real query", func(t *testing.T) {
		check(t, "enforce", 0, 1, func(ctx context.Context) {
			scoped := args
			scoped.Expr = "{__name__=" + strconv.Quote(metric) + "," + strconv.Quote(scopeName) + "=" + strconv.Quote(scopeValue) + "}"
			value, err := queryPrometheus(ctx, scoped)
			require.NoError(t, err)
			assert.NotEmpty(t, value.(model.Vector))
		})
	})
	t.Run("off skips metadata", func(t *testing.T) {
		check(t, "off", 0, 1, func(ctx context.Context) { _, err := queryPrometheus(ctx, args); require.NoError(t, err) })
	})
	t.Run("range and panel enforce before execution", func(t *testing.T) {
		check(t, "enforce", 1, 0, func(ctx context.Context) {
			rangeArgs := args
			rangeArgs.QueryType = "range"
			rangeArgs.StartTime = "now-1m"
			rangeArgs.StepSeconds = 15
			_, err := queryPrometheus(ctx, rangeArgs)
			require.ErrorContains(t, err, "TSDB head statistics")
			_, err = executePrometheusQuery(ctx, "prometheus", expr, "now-1m", "now")
			require.ErrorContains(t, err, "TSDB head statistics")
		})
	})
	t.Run("histogram enforce before execution", func(t *testing.T) {
		var bucket string
		for _, name := range names {
			if strings.HasSuffix(name, "_bucket") {
				bucket = name
				break
			}
		}
		if bucket == "" {
			t.Skip("no histogram bucket metric in this fixture's TSDB top10")
		}
		check(t, "enforce", 1, 0, func(ctx context.Context) {
			_, err := queryPrometheusHistogram(ctx, QueryPrometheusHistogramParams{DatasourceUID: "prometheus", Metric: strings.TrimSuffix(bucket, "_bucket"), Percentile: 95, StartTime: "now-1m", EndTime: "now"})
			require.ErrorContains(t, err, "TSDB head statistics")
		})
	})
	t.Run("unavailable metadata fails open and caches failure", func(t *testing.T) {
		unavailable.Store(true)
		defer unavailable.Store(false)
		check(t, "enforce", 1, 2, func(ctx context.Context) {
			for range 2 {
				value, err := queryPrometheus(ctx, args)
				require.NoError(t, err)
				assert.NotEmpty(t, value.(model.Vector))
			}
		})
	})
}
