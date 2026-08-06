package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"math"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	mcpgrafana "github.com/grafana/mcp-grafana"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseLogQLSelectorsSelectivity(t *testing.T) {
	tests := []struct {
		name          string
		logql         string
		wantCount     int
		wantSelective bool // of the first selector, when present
	}{
		{"empty selector", `{}`, 1, false},
		{"single equality", `{namespace="foo"}`, 1, true},
		{"catch-all regex", `{cluster=~".+"}`, 1, false},
		{"catch-all star", `{cluster=~".*"}`, 1, false},
		{"anchored catch-all", `{cluster=~"^.+$"}`, 1, false},
		{"double star", `{cluster=~".*.*"}`, 1, false},
		{"dotall catch-all", `{cluster=~"(?s).*"}`, 1, false},
		{"char class catch-all", `{cluster=~"[\\s\\S]*"}`, 1, false},
		{"negated newline class", `{cluster=~"[^\\n]*"}`, 1, false},
		{"alternation catch-all", `{cluster=~".*|foo"}`, 1, false},
		{"grouped catch-all", `{cluster=~"(.+)"}`, 1, false},
		{"optional catch-all plus", `{cluster=~"(.+)?"}`, 1, false},
		{"optional catch-all star", `{cluster=~"(.*)?"}`, 1, false},
		{"lazy plus", `{cluster=~".+?"}`, 1, false},
		{"dot-or-newline alternation", `{cluster=~"(.|\\n)*"}`, 1, false},
		{"complementary class alternation", `{cluster=~"(\\s|\\S)+"}`, 1, false},
		{"alternation with any-char branch", `{cluster=~"(foo|.)*"}`, 1, false},
		{"alternation union covers all", `{cluster=~"(a|foo|[^a])*"}`, 1, false},
		{"split complementary classes", `{cluster=~"(\\s|foo|\\S)+"}`, 1, false},
		{"partial alternation union is selective", `{app=~"(a|foo|[b-z])*"}`, 1, true},
		{"empty regex", `{cluster=~""}`, 1, false},
		{"negative only", `{app!="x", job!~"y.*"}`, 1, false},
		{"selective regex", `{app=~"api-.*"}`, 1, true},
		{"selective alternation", `{app=~"a|b"}`, 1, true},
		{"optional literal is selective", `{app=~"(foo)?"}`, 1, true},
		{"alternate empty-only", `{cluster=~"^|^"}`, 1, false},
		{"alternate empty groups", `{cluster=~"()|()"}`, 1, false},
		{"catch-all plus equality", `{cluster=~".+", namespace="foo"}`, 1, true},
		{"metric query", `sum by (level) (count_over_time({namespace="x", app="y"}[5m]))`, 1, true},
		{"escaped quote in value", `{app="a\"b"}`, 1, true},
		{"backtick value", "{app=`x`}", 1, true},
		{"brace in value", `{app="a}b"}`, 1, true},
		{"quoted label name", `{"service.name"="checkout"}`, 1, true},
		{"spacing", `{ app = "x" , container = "y" }`, 1, true},
		{"binary expression", `sum(rate({app="x"}[5m])) / sum(rate({app="y"}[5m]))`, 2, true},
		{"no selector", `foo`, 0, false},
		{"unterminated", `{app="x"`, 0, false},
		{"garbage matchers", `{app=="x"}`, 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			selectors := parseLogQLSelectors(tt.logql)
			require.Len(t, selectors, tt.wantCount, "parseLogQLSelectors(%q)", tt.logql)
			if tt.wantCount == 0 {
				return
			}
			assert.Equal(t, tt.wantSelective, hasSelectivePositiveMatcher(selectors[0].matchers), "hasSelectivePositiveMatcher(%q)", tt.logql)
		})
	}
}

func TestMaxVectorDuration(t *testing.T) {
	tests := []struct {
		name  string
		logql string
		want  time.Duration
	}{
		{"no range vector", `{app="x"} |= "error"`, 0},
		{"minutes", `count_over_time({app="x"}[5m])`, 5 * time.Minute},
		{"days", `sum(count_over_time({app="x"}[30d]))`, 30 * 24 * time.Hour},
		{"weeks", `rate({app="x"}[1w])`, 7 * 24 * time.Hour},
		{"compound", `rate({app="x"}[1h30m])`, 90 * time.Minute},
		{"max of several", `rate({a="x"}[5m]) / rate({a="x"}[2h])`, 2 * time.Hour},
		{"bracket inside string", `{app="x"} |~ "err[0-9]m"`, 0},
		{"overflowing duration ignored", `count_over_time({app="x"}[300y])`, 0},
		{"fractional hours", `rate({app="x"}[1.5h])`, 90 * time.Minute},
		{"fractional long", `sum(count_over_time({app="x"}[720.5h]))`, 720*time.Hour + 30*time.Minute},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, maxVectorDuration(tt.logql))
		})
	}
}

func TestParseLogQLDurationRejects(t *testing.T) {
	for _, s := range []string{"", "5", "5x", "m", "-5m", "300y", "9999999999y"} {
		t.Run(s, func(t *testing.T) {
			_, ok := parseLogQLDuration(s)
			assert.False(t, ok, "parseLogQLDuration(%q) must be rejected", s)
		})
	}
}

func TestStripLogQLComments(t *testing.T) {
	config, _ := guardrailConfig(mcpgrafana.LokiGuardrailEnforce, 0, 24*time.Hour)
	end := time.Now()
	start := end.Add(-time.Hour)

	// A commented-out catch-all selector must not be scanned. Asserted
	// through guardLokiQuery, which owns the comment stripping.
	ctx := mcpgrafana.WithGrafanaConfig(context.Background(), config)
	logql := `{app="x"} # see {cluster=~".+"} for all`
	selectors := parseLogQLSelectors(stripLogQLComments(logql))
	require.Len(t, selectors, 1)
	assert.Equal(t, `{app="x"}`, selectors[0].raw)
	assert.NoError(t, guardLokiQuery(ctx, nil, logql, "", start, end))

	// A commented-out range vector must not trip the range cap.
	assert.NoError(t, guardLokiQuery(ctx, nil, `{app="x"} # [30d]`, "", start, end))

	// A # inside a string literal is not a comment.
	logql = `{app="x"} |= "#not a comment"`
	assert.Equal(t, logql, stripLogQLComments(logql))
	selectors = parseLogQLSelectors(logql)
	require.Len(t, selectors, 1)

	// Loki's lexer (text/scanner GoTokens) also skips Go-style comments; a
	// /* */ inside a selector must not break matcher parsing into fail-open.
	selectors = parseLogQLSelectors(stripLogQLComments(`{cluster/*x*/=~".+"}`))
	require.Len(t, selectors, 1)
	require.Len(t, selectors[0].matchers, 1)
	assert.Equal(t, "cluster", selectors[0].matchers[0].Name)
	err := guardLokiQuery(ctx, nil, `{cluster/*x*/=~".+"}`, "", start, end)
	require.Error(t, err, "block comment must not hide a catch-all selector")
	assert.Contains(t, err.Error(), "selective")

	// // line comments are stripped; commented-out text must not inject.
	assert.NoError(t, guardLokiQuery(ctx, nil, "{app=\"x\"} // see {cluster=~\".+\"} [30d]", "", start, end))

	// Comment markers inside string literals are kept.
	logql = `{app="x"} |= "//not" |= "/*neither*/"`
	assert.Equal(t, logql, stripLogQLComments(logql))

	// An unterminated /* consumes the rest of the input.
	selectors = parseLogQLSelectors(stripLogQLComments(`{app="x"} /* {cluster=~".+"}`))
	require.Len(t, selectors, 1)
	assert.Equal(t, `{app="x"}`, selectors[0].raw)

	// A lone / (e.g. division) is not a comment.
	logql = `rate({app="x"}[5m]) / rate({app="y"}[5m])`
	assert.Equal(t, logql, stripLogQLComments(logql))
}

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

func TestLokiGuardrailReasonsStaticChecks(t *testing.T) {
	config, _ := guardrailConfig(mcpgrafana.LokiGuardrailEnforce, 0, 24*time.Hour)
	end := time.Now()
	start := end.Add(-time.Hour)

	reasons := lokiGuardrailReasons(context.Background(), config, true, `{cluster=~".+"} |= "error"`, "", start, end, nil)
	require.Len(t, reasons, 1)
	assert.Contains(t, reasons[0], "selective")
	assert.Contains(t, reasons[0], `{cluster=~".+"}`)

	reasons = lokiGuardrailReasons(context.Background(), config, true, `{namespace="foo", app="bar"}`, "", end.Add(-48*time.Hour), end, nil)
	require.Len(t, reasons, 1)
	assert.Contains(t, reasons[0], "time range")

	// A catch-all on either side of a binary expression is flagged.
	reasons = lokiGuardrailReasons(context.Background(), config, true, `sum(rate({app="x"}[5m])) / sum(rate({cluster=~".+"}[5m]))`, "", start, end, nil)
	require.Len(t, reasons, 1)
	assert.Contains(t, reasons[0], `{cluster=~".+"}`)

	// Unparseable queries pass through: Loki rejects them with a proper error.
	assert.Empty(t, lokiGuardrailReasons(context.Background(), config, true, `{app=="x"}`, "", start, end, nil))

	// Selective selector within the range cap is allowed.
	assert.Empty(t, lokiGuardrailReasons(context.Background(), config, true, `{namespace="foo", app="bar"}`, "", start, end, nil))
}

func TestLokiGuardrailReasonsRangeVector(t *testing.T) {
	config, _ := guardrailConfig(mcpgrafana.LokiGuardrailEnforce, 0, 24*time.Hour)
	end := time.Now()

	// Instant metric query: no window of its own, cost set by the vector.
	reasons := lokiGuardrailReasons(context.Background(), config, true, `sum(count_over_time({namespace="foo"}[30d]))`, "instant", time.Time{}, time.Time{}, nil)
	require.Len(t, reasons, 1)
	assert.Contains(t, reasons[0], "range vector")

	// The vector duration counts on top of the range query window.
	reasons = lokiGuardrailReasons(context.Background(), config, true, `rate({namespace="foo"}[20h])`, "", end.Add(-8*time.Hour), end, nil)
	require.Len(t, reasons, 1)
	assert.Contains(t, reasons[0], "time range")

	// A small vector on an instant query is fine.
	assert.Empty(t, lokiGuardrailReasons(context.Background(), config, true, `sum(count_over_time({namespace="foo"}[1h]))`, "instant", time.Time{}, time.Time{}, nil))

	// Fractional durations (Loki's time.ParseDuration fallback) count too:
	// [720.5h] on an instant query must not fail open past the range cap.
	reasons = lokiGuardrailReasons(context.Background(), config, true, `sum(count_over_time({namespace="foo"}[720.5h]))`, "instant", time.Time{}, time.Time{}, nil)
	require.Len(t, reasons, 1)
	assert.Contains(t, reasons[0], "range vector")

	// A multi-century span saturates end.Sub(start) at MaxInt64; adding the
	// vector duration must saturate too, not wrap negative and fail open.
	reasons = lokiGuardrailReasons(context.Background(), config, true, `sum(count_over_time({namespace="foo"}[7d]))`, "", time.Date(1000, 1, 1, 0, 0, 0, 0, time.UTC), end, nil)
	require.Len(t, reasons, 1)
	assert.Contains(t, reasons[0], "time range")
}

func TestLokiGuardrailReasonsByteBudget(t *testing.T) {
	config, _ := guardrailConfig(mcpgrafana.LokiGuardrailEnforce, 100<<30, 24*time.Hour)
	end := time.Now()
	start := end.Add(-time.Hour)

	var gotQuery string
	overBudget := func(ctx context.Context, query string, s, e time.Time) (*Stats, error) {
		gotQuery = query
		return &Stats{Bytes: 200 << 30}, nil
	}
	reasons := lokiGuardrailReasons(context.Background(), config, true, `{namespace="foo", app="bar"} |= "error"`, "", start, end, overBudget)
	require.Len(t, reasons, 1)
	assert.Contains(t, reasons[0], "budget")
	// The estimate is for the bare selector: line filters do not reduce
	// bytes scanned.
	assert.Equal(t, `{namespace="foo", app="bar"}`, gotQuery)

	underBudget := func(ctx context.Context, query string, s, e time.Time) (*Stats, error) {
		return &Stats{Bytes: 1 << 30}, nil
	}
	assert.Empty(t, lokiGuardrailReasons(context.Background(), config, true, `{namespace="foo", app="bar"}`, "", start, end, underBudget))
}

func TestLokiGuardrailReasonsByteBudgetInstantWindow(t *testing.T) {
	config, _ := guardrailConfig(mcpgrafana.LokiGuardrailEnforce, 100<<30, 24*time.Hour)
	end := time.Now()

	// Instant queries anchor the stats window at the query time, widened
	// backwards by the vector duration.
	var gotStart, gotEnd time.Time
	statsFn := func(ctx context.Context, query string, s, e time.Time) (*Stats, error) {
		gotStart, gotEnd = s, e
		return &Stats{Bytes: 200 << 30}, nil
	}
	reasons := lokiGuardrailReasons(context.Background(), config, true, `sum(count_over_time({namespace="foo"}[6h]))`, "instant", time.Time{}, end, statsFn)
	require.Len(t, reasons, 1)
	assert.Contains(t, reasons[0], "budget")
	assert.Equal(t, end, gotEnd)
	assert.Equal(t, end.Add(-6*time.Hour), gotStart)
}

func TestLokiGuardrailReasonsSumsSelectors(t *testing.T) {
	config, _ := guardrailConfig(mcpgrafana.LokiGuardrailEnforce, 100<<30, 24*time.Hour)
	end := time.Now()
	start := end.Add(-time.Hour)

	var queries []string
	statsFn := func(ctx context.Context, query string, s, e time.Time) (*Stats, error) {
		queries = append(queries, query)
		return &Stats{Bytes: 60 << 30}, nil
	}

	// Both sides of a binary expression are scanned; their estimates sum.
	logql := `sum(rate({namespace="a", app="x"}[5m])) / sum(rate({namespace="a", app="y"}[5m]))`
	reasons := lokiGuardrailReasons(context.Background(), config, true, logql, "", start, end, statsFn)
	require.Len(t, reasons, 1)
	assert.Contains(t, reasons[0], "budget")
	assert.Equal(t, []string{`{namespace="a", app="x"}`, `{namespace="a", app="y"}`}, queries)

	// Repeated selectors are fetched once but each occurrence counts: both
	// legs of the binary expression scan independently, so 60% of budget
	// twice is over budget — with a single stats round trip.
	queries = nil
	logql = `sum(rate({namespace="a", app="x"}[5m])) / sum(rate({namespace="a", app="x"}[10m]))`
	reasons = lokiGuardrailReasons(context.Background(), config, true, logql, "", start, end, statsFn)
	require.Len(t, reasons, 1)
	assert.Contains(t, reasons[0], "budget")
	assert.Len(t, queries, 1)
}

func TestLokiGuardrailReasonsByteSumSaturates(t *testing.T) {
	config, _ := guardrailConfig(mcpgrafana.LokiGuardrailEnforce, 100<<30, 24*time.Hour)
	end := time.Now()
	start := end.Add(-time.Hour)

	// A huge estimate times many occurrences must saturate, not wrap
	// negative and bypass the budget.
	statsFn := func(ctx context.Context, query string, s, e time.Time) (*Stats, error) {
		return &Stats{Bytes: math.MaxInt64 / 2}, nil
	}
	logql := `sum(rate({app="x"}[5m])) + sum(rate({app="x"}[5m])) + sum(rate({app="x"}[5m]))`
	reasons := lokiGuardrailReasons(context.Background(), config, true, logql, "", start, end, statsFn)
	require.Len(t, reasons, 1)
	assert.Contains(t, reasons[0], "budget")
}

func TestLokiGuardrailReasonsByteCheckSkippedWithoutWindow(t *testing.T) {
	config, _ := guardrailConfig(mcpgrafana.LokiGuardrailEnforce, 100<<30, 24*time.Hour)

	// Instant log query with no bounds and no range vector: there is no
	// window to estimate over, so only the static selector check applies.
	called := false
	statsFn := func(ctx context.Context, query string, s, e time.Time) (*Stats, error) {
		called = true
		return &Stats{Bytes: 200 << 30}, nil
	}
	assert.Empty(t, lokiGuardrailReasons(context.Background(), config, true, `{namespace="foo"}`, "instant", time.Time{}, time.Time{}, statsFn))
	assert.False(t, called)
}

func TestLokiGuardrailReasonsStatsFailureFailsOpen(t *testing.T) {
	config, _ := guardrailConfig(mcpgrafana.LokiGuardrailEnforce, 100<<30, 24*time.Hour)
	end := time.Now()
	start := end.Add(-time.Hour)

	boom := func(ctx context.Context, query string, s, e time.Time) (*Stats, error) {
		return nil, errors.New("boom")
	}
	assert.Empty(t, lokiGuardrailReasons(context.Background(), config, true, `{namespace="foo", app="bar"}`, "", start, end, boom))
}

func TestLokiGuardrailReasonsStatsSkippedForBlockedQuery(t *testing.T) {
	config, _ := guardrailConfig(mcpgrafana.LokiGuardrailEnforce, 100<<30, 24*time.Hour)
	end := time.Now()
	start := end.Add(-time.Hour)

	called := false
	statsFn := func(ctx context.Context, query string, s, e time.Time) (*Stats, error) {
		called = true
		return &Stats{}, nil
	}
	reasons := lokiGuardrailReasons(context.Background(), config, true, `{cluster=~".+"}`, "", start, end, statsFn)
	require.NotEmpty(t, reasons)
	assert.False(t, called, "index/stats round trip must be skipped when static checks already block")
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

func (f *fakeLokiBackend) ListLabelNames(ctx context.Context, start, end time.Time) ([]string, error) {
	return nil, nil
}

func (f *fakeLokiBackend) ListLabelValues(ctx context.Context, labelName string, start, end time.Time) ([]string, error) {
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

func TestHumanizeBytes(t *testing.T) {
	assert.Equal(t, "100.0 GiB", humanizeBytes(100<<30))
	assert.Equal(t, "1.5 TiB", humanizeBytes(3<<40/2))
	assert.Equal(t, "10.0 MiB", humanizeBytes(10<<20))
	assert.Equal(t, "4.0 KiB", humanizeBytes(4<<10))
	assert.Equal(t, "512 B", humanizeBytes(512))
}
