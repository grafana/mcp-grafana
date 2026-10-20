package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	mcpgrafana "github.com/grafana/mcp-grafana/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// guardrailConfig returns a GrafanaConfig with the guardrail enabled in the
// given mode, capturing log output in the returned buffer.
func guardrailConfig(mode string, maxBytes int64, maxRange time.Duration) (mcpgrafana.GrafanaConfig, *bytes.Buffer) {
	var buf bytes.Buffer
	return mcpgrafana.GrafanaConfig{
		LokiGuardrailMode:     mode,
		LokiGuardrailMaxBytes: maxBytes,
		LokiGuardrailMaxRange: maxRange,
		Logger:                slog.New(slog.NewTextHandler(&buf, nil)),
	}, &buf
}

func TestGuardLokiQueryModes(t *testing.T) {
	end := time.Now()
	start := end.Add(-time.Hour)
	catchAll := `{cluster=~".+"}`

	t.Run("off allows everything", func(t *testing.T) {
		config, _ := guardrailConfig(mcpgrafana.LokiGuardrailOff, 0, 24*time.Hour)
		ctx := mcpgrafana.WithGrafanaConfig(context.Background(), config)
		assert.NoError(t, guardLokiQuery(ctx, nil, catchAll, "", start, end))
	})

	t.Run("unset mode allows everything", func(t *testing.T) {
		ctx := mcpgrafana.WithGrafanaConfig(context.Background(), mcpgrafana.GrafanaConfig{})
		assert.NoError(t, guardLokiQuery(ctx, nil, catchAll, "", start, end))
	})

	t.Run("shadow logs but passes", func(t *testing.T) {
		config, buf := guardrailConfig(mcpgrafana.LokiGuardrailShadow, 0, 24*time.Hour)
		ctx := mcpgrafana.WithGrafanaConfig(context.Background(), config)
		assert.NoError(t, guardLokiQuery(ctx, nil, catchAll, "", start, end))
		assert.Contains(t, buf.String(), "would block")
	})

	t.Run("enforce blocks with rewrite guidance", func(t *testing.T) {
		config, _ := guardrailConfig(mcpgrafana.LokiGuardrailEnforce, 0, 24*time.Hour)
		ctx := mcpgrafana.WithGrafanaConfig(context.Background(), config)
		err := guardLokiQuery(ctx, nil, catchAll, "", start, end)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "selective")
		assert.Contains(t, err.Error(), "Rewrite and retry")
	})

	t.Run("enforce passes selective query", func(t *testing.T) {
		config, _ := guardrailConfig(mcpgrafana.LokiGuardrailEnforce, 0, 24*time.Hour)
		ctx := mcpgrafana.WithGrafanaConfig(context.Background(), config)
		assert.NoError(t, guardLokiQuery(ctx, nil, `{namespace="foo", app="bar"}`, "", start, end))
	})
}

// TestGuardLokiQueryUnknownModeWarnsOnce is the single test asserting the
// once-per-process warning: warnUnknownGuardrailModeOnce is reset here so
// the assertion does not depend on test execution order.
func TestGuardLokiQueryUnknownModeWarnsOnce(t *testing.T) {
	end := time.Now()
	start := end.Add(-time.Hour)
	warnUnknownGuardrailModeOnce = sync.Once{}

	config, buf := guardrailConfig("enfore", 0, 24*time.Hour)
	ctx := mcpgrafana.WithGrafanaConfig(context.Background(), config)

	// Unknown modes enforce (fail-closed) and warn on first sight.
	err := guardLokiQuery(ctx, nil, `{cluster=~".+"}`, "", start, end)
	require.Error(t, err)
	assert.Contains(t, buf.String(), "unrecognized Loki guardrail mode")

	// The warning fires once per process, not once per query.
	buf.Reset()
	err = guardLokiQuery(ctx, nil, `{cluster=~".+"}`, "", start, end)
	require.Error(t, err)
	assert.NotContains(t, buf.String(), "unrecognized Loki guardrail mode")
}

func TestGuardLokiQueryFailOpenLogLevel(t *testing.T) {
	end := time.Now()
	start := end.Add(-time.Hour)

	t.Run("non-native backend logs no warn for brace-less query", func(t *testing.T) {
		config, buf := guardrailConfig(mcpgrafana.LokiGuardrailEnforce, 0, 24*time.Hour)
		ctx := mcpgrafana.WithGrafanaConfig(context.Background(), config)
		assert.NoError(t, guardLokiQuery(ctx, &fakeLokiBackend{}, `_time:5m error`, "", start, end))
		assert.NotContains(t, buf.String(), "level=WARN")
	})

	t.Run("native backend warns on unparseable query", func(t *testing.T) {
		config, buf := guardrailConfig(mcpgrafana.LokiGuardrailEnforce, 0, 24*time.Hour)
		var hits int
		backend := newStatsBackend(t, 0, &hits)
		ctx := mcpgrafana.WithGrafanaConfig(context.Background(), config)
		assert.NoError(t, guardLokiQuery(ctx, backend, `{app=="x"}`, "", start, end))
		assert.Contains(t, buf.String(), "no parseable stream selector")
		assert.Contains(t, buf.String(), "level=WARN")
	})
}

// newStatsBackend returns a native Loki backend pointing at a test server
// that answers index/stats with the given byte count.
func newStatsBackend(t *testing.T, bytesEstimate int64, hits *int) *lokiNativeBackend {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/loki/api/v1/index/stats", func(w http.ResponseWriter, r *http.Request) {
		*hits++
		_ = json.NewEncoder(w).Encode(Stats{Bytes: bytesEstimate})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return &lokiNativeBackend{client: &Client{httpClient: srv.Client(), baseURL: srv.URL}}
}

func TestGuardLokiQueryNativeBackendByteBudget(t *testing.T) {
	end := time.Now()
	start := end.Add(-time.Hour)
	config, _ := guardrailConfig(mcpgrafana.LokiGuardrailEnforce, 100<<30, 24*time.Hour)
	ctx := mcpgrafana.WithGrafanaConfig(context.Background(), config)

	var hits int
	backend := newStatsBackend(t, 200<<30, &hits)
	err := guardLokiQuery(ctx, backend, `{namespace="foo", app="bar"}`, "", start, end)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "budget")
	assert.Equal(t, 1, hits)
}

// fakeLokiBackend is a non-native lokiBackend whose QueryStats must never be
// consulted by the guardrail (the VictoriaLogs approximation runs a real
// query).
type fakeLokiBackend struct {
	statsCalled bool
}

func (f *fakeLokiBackend) ListLabelNames(ctx context.Context, matcher string, start, end time.Time) ([]string, error) {
	return nil, nil
}

func (f *fakeLokiBackend) ListLabelValues(ctx context.Context, labelName, matcher string, start, end time.Time) ([]string, error) {
	return nil, nil
}

func (f *fakeLokiBackend) QueryLogs(ctx context.Context, p lokiQueryParams) (*lokiQueryResult, error) {
	return &lokiQueryResult{}, nil
}

func (f *fakeLokiBackend) QueryStats(ctx context.Context, query string, start, end time.Time) (*Stats, error) {
	f.statsCalled = true
	return &Stats{Bytes: 200 << 30}, nil
}

func (f *fakeLokiBackend) QueryPatterns(ctx context.Context, query, step string, start, end time.Time) ([]Pattern, error) {
	return nil, nil
}

func TestGuardLokiQuerySkipsStatsOnNonNativeBackend(t *testing.T) {
	end := time.Now()
	start := end.Add(-time.Hour)
	config, _ := guardrailConfig(mcpgrafana.LokiGuardrailEnforce, 100<<30, 24*time.Hour)
	ctx := mcpgrafana.WithGrafanaConfig(context.Background(), config)

	backend := &fakeLokiBackend{}
	assert.NoError(t, guardLokiQuery(ctx, backend, `{namespace="foo", app="bar"}`, "", start, end))
	assert.False(t, backend.statsCalled, "non-native backends must not pay for a stats query")
}

func TestGuardLokiQueryIgnoresCommentedOutText(t *testing.T) {
	end := time.Now()
	start := end.Add(-time.Hour)
	config, _ := guardrailConfig(mcpgrafana.LokiGuardrailEnforce, 0, 24*time.Hour)
	ctx := mcpgrafana.WithGrafanaConfig(context.Background(), config)

	// A commented-out catch-all selector or [30d] range vector must not block.
	assert.NoError(t, guardLokiQuery(ctx, nil, `{app="x"} # see {cluster=~".+"} for all`, "", start, end))
	assert.NoError(t, guardLokiQuery(ctx, nil, `{app="x"} # [30d]`, "", start, end))
	// But a comment inside a selector cannot hide a catch-all.
	assert.Error(t, guardLokiQuery(ctx, nil, `{cluster/*x*/=~".+"}`, "", start, end))
}

func TestGuardLokiQueryEnforceMessageUsesGuidance(t *testing.T) {
	end := time.Now()
	config, _ := guardrailConfig(mcpgrafana.LokiGuardrailEnforce, 0, 24*time.Hour)
	ctx := mcpgrafana.WithGrafanaConfig(context.Background(), config)

	err := guardLokiQuery(ctx, nil, `{namespace="foo", app="bar"}`, "", end.Add(-48*time.Hour), end)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "time range")
	assert.Contains(t, err.Error(), "24h0m0s")
	for _, tool := range []string{"query_loki_stats", "list_loki_label_names", "list_loki_label_values"} {
		assert.Contains(t, err.Error(), tool)
	}
}
