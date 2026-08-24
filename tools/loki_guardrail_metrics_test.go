package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	mcpgrafana "github.com/grafana/mcp-grafana"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

// newGuardrailMetricsReader installs a fresh manual-reader MeterProvider as
// the guardrail's instrument source and returns both the reader and a
// GrafanaConfig wired to that provider.
//
// The instruments are built once per process (see lokiGuardrailMetricsOnce),
// and other tests in this package will already have built them against the
// global noop provider, so the once has to be reset — and reset again
// afterwards, so a later test does not record into this test's reader.
func newGuardrailMetricsReader(t *testing.T, mode string, maxBytes int64, maxRange time.Duration) (*sdkmetric.ManualReader, mcpgrafana.GrafanaConfig) {
	t.Helper()
	reset := func() {
		lokiGuardrailMetricsOnce = sync.Once{}
		lokiGuardrailInstruments = lokiGuardrailMetrics{}
	}
	reset()
	t.Cleanup(reset)

	reader := sdkmetric.NewManualReader()
	config, _ := guardrailConfig(mode, maxBytes, maxRange)
	config.MeterProvider = sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	return reader, config
}

// guardrailCounts collects the guardrail counters and returns, for each
// counter name, the recorded value per attribute set (rendered as a sorted
// "k=v,k=v" key so assertions stay readable).
func guardrailCounts(t *testing.T, reader *sdkmetric.ManualReader) map[string]map[string]int64 {
	t.Helper()
	var data metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(context.Background(), &data))

	out := map[string]map[string]int64{}
	for _, sm := range data.ScopeMetrics {
		for _, m := range sm.Metrics {
			sum, ok := m.Data.(metricdata.Sum[int64])
			if !ok {
				continue
			}
			for _, dp := range sum.DataPoints {
				parts := make([]string, 0, dp.Attributes.Len())
				for _, kv := range dp.Attributes.ToSlice() {
					parts = append(parts, string(kv.Key)+"="+kv.Value.Emit())
				}
				key := ""
				for i, p := range parts {
					if i > 0 {
						key += ","
					}
					key += p
				}
				if out[m.Name] == nil {
					out[m.Name] = map[string]int64{}
				}
				out[m.Name][key] += dp.Value
			}
		}
	}
	return out
}

func TestGuardrailMetricsAdmitted(t *testing.T) {
	end := time.Now()
	start := end.Add(-time.Hour)
	reader, config := newGuardrailMetricsReader(t, mcpgrafana.LokiGuardrailShadow, 0, 24*time.Hour)
	ctx := mcpgrafana.WithGrafanaConfig(context.Background(), config)

	var hits int
	backend := newStatsBackend(t, 0, &hits)
	require.NoError(t, guardLokiQuery(ctx, backend, `{namespace="foo", app="bar"}`, "", start, end))

	counts := guardrailCounts(t, reader)
	assert.Equal(t, map[string]int64{"backend=loki": 1}, counts["mcp.loki_guardrail.admitted"])
	assert.Empty(t, counts["mcp.loki_guardrail.would_block"])
	assert.Empty(t, counts["mcp.loki_guardrail.blocked"])
	assert.Empty(t, counts["mcp.loki_guardrail.fail_open"])
}

// The counter that fires is what distinguishes shadow from enforce, so
// alerts survive the promotion that renames the log line.
func TestGuardrailMetricsShadowAndEnforce(t *testing.T) {
	end := time.Now()
	start := end.Add(-time.Hour)

	t.Run("shadow records would_block", func(t *testing.T) {
		reader, config := newGuardrailMetricsReader(t, mcpgrafana.LokiGuardrailShadow, 0, 24*time.Hour)
		ctx := mcpgrafana.WithGrafanaConfig(context.Background(), config)
		require.NoError(t, guardLokiQuery(ctx, nil, `{cluster=~".+"}`, "", start, end))

		counts := guardrailCounts(t, reader)
		assert.Equal(t, map[string]int64{"backend=unknown,reason=selector": 1}, counts["mcp.loki_guardrail.would_block"])
		assert.Empty(t, counts["mcp.loki_guardrail.blocked"])
	})

	t.Run("enforce records blocked", func(t *testing.T) {
		reader, config := newGuardrailMetricsReader(t, mcpgrafana.LokiGuardrailEnforce, 0, 24*time.Hour)
		ctx := mcpgrafana.WithGrafanaConfig(context.Background(), config)
		require.Error(t, guardLokiQuery(ctx, nil, `{cluster=~".+"}`, "", start, end))

		counts := guardrailCounts(t, reader)
		assert.Equal(t, map[string]int64{"backend=unknown,reason=selector": 1}, counts["mcp.loki_guardrail.blocked"])
		assert.Empty(t, counts["mcp.loki_guardrail.would_block"])
	})

	// Unknown modes fail closed, so they must count as blocked rather than
	// as a shadow-mode would-block.
	t.Run("unknown mode records blocked", func(t *testing.T) {
		warnUnknownGuardrailModeOnce = sync.Once{}
		reader, config := newGuardrailMetricsReader(t, "enfore", 0, 24*time.Hour)
		ctx := mcpgrafana.WithGrafanaConfig(context.Background(), config)
		require.Error(t, guardLokiQuery(ctx, nil, `{cluster=~".+"}`, "", start, end))

		counts := guardrailCounts(t, reader)
		assert.Equal(t, map[string]int64{"backend=unknown,reason=selector": 1}, counts["mcp.loki_guardrail.blocked"])
	})
}

// Each reason kind gets its own attribute value, so a would-block population
// dominated by the (unconditional) selectivity check is distinguishable from
// one that raising the byte or range budget would soften.
func TestGuardrailMetricsReasonAttribute(t *testing.T) {
	end := time.Now()

	t.Run("range", func(t *testing.T) {
		reader, config := newGuardrailMetricsReader(t, mcpgrafana.LokiGuardrailShadow, 0, 24*time.Hour)
		ctx := mcpgrafana.WithGrafanaConfig(context.Background(), config)
		require.NoError(t, guardLokiQuery(ctx, nil, `{namespace="foo", app="bar"}`, "", end.Add(-48*time.Hour), end))

		counts := guardrailCounts(t, reader)
		assert.Equal(t, map[string]int64{"backend=unknown,reason=range": 1}, counts["mcp.loki_guardrail.would_block"])
	})

	t.Run("bytes", func(t *testing.T) {
		reader, config := newGuardrailMetricsReader(t, mcpgrafana.LokiGuardrailShadow, 100<<30, 24*time.Hour)
		ctx := mcpgrafana.WithGrafanaConfig(context.Background(), config)
		var hits int
		backend := newStatsBackend(t, 200<<30, &hits)
		require.NoError(t, guardLokiQuery(ctx, backend, `{namespace="foo", app="bar"}`, "", end.Add(-time.Hour), end))

		counts := guardrailCounts(t, reader)
		assert.Equal(t, map[string]int64{"backend=loki,reason=bytes": 1}, counts["mcp.loki_guardrail.would_block"])
	})

	// A query tripping both the selector and the range check is counted once,
	// attributed to the check that ran first. Raising the range budget would
	// not admit it, so `selector` is the actionable answer.
	t.Run("selector wins over range", func(t *testing.T) {
		reader, config := newGuardrailMetricsReader(t, mcpgrafana.LokiGuardrailShadow, 0, 24*time.Hour)
		ctx := mcpgrafana.WithGrafanaConfig(context.Background(), config)
		require.NoError(t, guardLokiQuery(ctx, nil, `{cluster=~".+"}`, "", end.Add(-48*time.Hour), end))

		counts := guardrailCounts(t, reader)
		assert.Equal(t, map[string]int64{"backend=unknown,reason=selector": 1}, counts["mcp.loki_guardrail.would_block"])
	})
}

// The fail-open paths are the ones that make a quiet dashboard ambiguous:
// without these counters "nothing would be blocked" is indistinguishable
// from "the guardrail is not looking".
func TestGuardrailMetricsFailOpen(t *testing.T) {
	end := time.Now()
	start := end.Add(-time.Hour)

	t.Run("unparseable", func(t *testing.T) {
		reader, config := newGuardrailMetricsReader(t, mcpgrafana.LokiGuardrailEnforce, 0, 24*time.Hour)
		ctx := mcpgrafana.WithGrafanaConfig(context.Background(), config)
		require.NoError(t, guardLokiQuery(ctx, nil, `{app=="x"}`, "", start, end))

		counts := guardrailCounts(t, reader)
		assert.Equal(t, map[string]int64{"backend=unknown,cause=unparseable": 1}, counts["mcp.loki_guardrail.fail_open"])
		assert.Empty(t, counts["mcp.loki_guardrail.admitted"], "a fail-open is not a clean admission")
	})

	// A brace-less LogsQL query on VictoriaLogs is the normal shape, not a
	// scanner gap — the backend attribute is what keeps the unparseable rate
	// on native Loki readable.
	t.Run("unparseable on victorialogs", func(t *testing.T) {
		reader, config := newGuardrailMetricsReader(t, mcpgrafana.LokiGuardrailEnforce, 0, 24*time.Hour)
		ctx := mcpgrafana.WithGrafanaConfig(context.Background(), config)
		require.NoError(t, guardLokiQuery(ctx, &victoriaLogsBackend{}, `_time:5m error`, "", start, end))

		counts := guardrailCounts(t, reader)
		assert.Equal(t, map[string]int64{"backend=victorialogs,cause=unparseable": 1}, counts["mcp.loki_guardrail.fail_open"])
	})

	t.Run("estimate failed", func(t *testing.T) {
		reader, config := newGuardrailMetricsReader(t, mcpgrafana.LokiGuardrailEnforce, 100<<30, 24*time.Hour)
		ctx := mcpgrafana.WithGrafanaConfig(context.Background(), config)
		require.NoError(t, guardLokiQuery(ctx, newBrokenStatsBackend(t), `{namespace="foo", app="bar"}`, "", start, end))

		counts := guardrailCounts(t, reader)
		assert.Equal(t, map[string]int64{"backend=loki,cause=estimate_failed": 1}, counts["mcp.loki_guardrail.fail_open"])
		assert.Empty(t, counts["mcp.loki_guardrail.admitted"], "the budget check did not happen")
	})

	// A partial total that is already over budget is a verdict, not a
	// fail-open, even though a later stats call errored.
	t.Run("partial total over budget counts as a block", func(t *testing.T) {
		reader, config := newGuardrailMetricsReader(t, mcpgrafana.LokiGuardrailShadow, 100<<30, 24*time.Hour)
		ctx := mcpgrafana.WithGrafanaConfig(context.Background(), config)
		backend := newSelectiveStatsBackend(t, map[string]int64{`{namespace="a", app="x"}`: 500 << 30})
		logql := `sum(rate({namespace="a", app="x"}[5m])) / sum(rate({namespace="a", app="y"}[5m]))`
		require.NoError(t, guardLokiQuery(ctx, backend, logql, "", start, end))

		counts := guardrailCounts(t, reader)
		assert.Equal(t, map[string]int64{"backend=loki,reason=bytes": 1}, counts["mcp.loki_guardrail.would_block"])
		assert.Empty(t, counts["mcp.loki_guardrail.fail_open"])
	})
}

// Guardrail mode off short-circuits before any instrument is touched: an
// unguarded query must not show up as admitted.
func TestGuardrailMetricsOffRecordsNothing(t *testing.T) {
	end := time.Now()
	start := end.Add(-time.Hour)
	reader, config := newGuardrailMetricsReader(t, mcpgrafana.LokiGuardrailOff, 0, 24*time.Hour)
	ctx := mcpgrafana.WithGrafanaConfig(context.Background(), config)

	require.NoError(t, guardLokiQuery(ctx, nil, `{cluster=~".+"}`, "", start, end))
	assert.Empty(t, guardrailCounts(t, reader))
}

// The counters must reach the injected provider, not the global one: an
// embedder that installs a noop global provider (as the hosted Cloud MCP
// server does) would otherwise drop every recording.
func TestGuardrailMetricsUseInjectedMeterProvider(t *testing.T) {
	end := time.Now()
	start := end.Add(-time.Hour)
	reader, config := newGuardrailMetricsReader(t, mcpgrafana.LokiGuardrailEnforce, 0, 24*time.Hour)
	ctx := mcpgrafana.WithGrafanaConfig(context.Background(), config)
	require.Error(t, guardLokiQuery(ctx, nil, `{cluster=~".+"}`, "", start, end))

	var data metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(context.Background(), &data))
	require.Len(t, data.ScopeMetrics, 1)
	assert.Equal(t, lokiGuardrailMeterName, data.ScopeMetrics[0].Scope.Name)
	require.Len(t, data.ScopeMetrics[0].Metrics, 1)
	assert.Equal(t, "mcp.loki_guardrail.blocked", data.ScopeMetrics[0].Metrics[0].Name)
}

func TestGuardrailBackendLabel(t *testing.T) {
	assert.Equal(t, guardrailBackendLoki, guardrailBackendLabel(&lokiNativeBackend{}))
	assert.Equal(t, guardrailBackendVictoriaLogs, guardrailBackendLabel(&victoriaLogsBackend{}))
	assert.Equal(t, guardrailBackendUnknown, guardrailBackendLabel(nil))
	assert.Equal(t, guardrailBackendUnknown, guardrailBackendLabel(&fakeLokiBackend{}))
}

// TestGuardrailMetricsAttributeKeys pins the attribute keys, since they
// become Prometheus labels that downstream alerts group by.
func TestGuardrailMetricsAttributeKeys(t *testing.T) {
	end := time.Now()
	start := end.Add(-time.Hour)
	reader, config := newGuardrailMetricsReader(t, mcpgrafana.LokiGuardrailShadow, 0, 24*time.Hour)
	ctx := mcpgrafana.WithGrafanaConfig(context.Background(), config)
	require.NoError(t, guardLokiQuery(ctx, nil, `{cluster=~".+"}`, "", start, end))

	var data metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(context.Background(), &data))
	require.Len(t, data.ScopeMetrics, 1)
	require.Len(t, data.ScopeMetrics[0].Metrics, 1)
	sum, ok := data.ScopeMetrics[0].Metrics[0].Data.(metricdata.Sum[int64])
	require.True(t, ok)
	require.Len(t, sum.DataPoints, 1)

	attrs := sum.DataPoints[0].Attributes
	_, hasBackend := attrs.Value(attribute.Key("backend"))
	_, hasReason := attrs.Value(attribute.Key("reason"))
	assert.True(t, hasBackend)
	assert.True(t, hasReason)
	assert.Equal(t, 2, attrs.Len(), "no unbounded attributes (selector, LogQL) may be attached")
}

// newBrokenStatsBackend returns a native Loki backend whose index/stats
// endpoint always fails, standing in for a Loki availability blip.
func newBrokenStatsBackend(t *testing.T) *lokiNativeBackend {
	t.Helper()
	return newSelectiveStatsBackend(t, nil)
}

// newSelectiveStatsBackend returns a native Loki backend whose index/stats
// endpoint answers with the byte estimate registered for the requested
// selector, and fails for any selector not in the map.
func newSelectiveStatsBackend(t *testing.T, estimates map[string]int64) *lokiNativeBackend {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/loki/api/v1/index/stats", func(w http.ResponseWriter, r *http.Request) {
		bytesEstimate, ok := estimates[r.URL.Query().Get("query")]
		if !ok {
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "unavailable"})
			return
		}
		_ = json.NewEncoder(w).Encode(Stats{Bytes: bytesEstimate})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return &lokiNativeBackend{client: &Client{httpClient: srv.Client(), baseURL: srv.URL}}
}
