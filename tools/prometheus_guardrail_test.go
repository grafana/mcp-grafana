package tools

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/grafana/grafana-openapi-client-go/client"
	mcpgrafana "github.com/grafana/mcp-grafana/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var promGuardEnd = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)

func promGuardReasons(d promGuardDecision) []promGuardReason {
	out := make([]promGuardReason, len(d.Violations))
	for i, v := range d.Violations {
		out[i] = v.Reason
	}
	return out
}

// Each case pins the window an expression reads beyond its evaluation time:
// an instant query with exactly that range budget passes, one nanosecond
// less blocks.
func TestAssessPromQueryExpressionWindow(t *testing.T) {
	day := 24 * time.Hour
	tests := []struct {
		name   string
		expr   string
		window time.Duration
	}{
		{"range vector", `rate(http_requests_total[5m])`, 5 * time.Minute},
		{"plain-vector subquery", `max_over_time(up[30d:1m])`, 30 * day},
		{"subquery adds to the range vector below it", `max_over_time(rate(x[5m])[30d:1m])`, 30*day + 5*time.Minute},
		{"nested subqueries add up", `max_over_time(max_over_time(rate(x[5m])[1d:1m])[7d:1h])`, 8*day + 5*time.Minute},
		{"siblings take the max, not the sum", `rate(a[1d]) + rate(b[2d])`, 2 * day},
		{"siblings under a subquery", `max_over_time((rate(a[1h]) + rate(b[2h]))[1d:5m])`, day + 2*time.Hour},
		{"selectors outside the subquery do not get its range", `max_over_time(up[1d:1m]) + rate(x[2h])`, day},
		{"offset is ignored", `rate(x[5m] offset 30d)`, 5 * time.Minute},
		{"@ is ignored", `rate(x[5m] @ 1609746000)`, 5 * time.Minute},
		{"experimental functions parse", `sort_by_label(rate(x[1h]), "job")`, time.Hour},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			pass := assessPromQuery(tc.expr, "instant", time.Time{}, promGuardEnd, 0, promGuardPolicy{MaxRange: tc.window})
			assert.Equal(t, promGuardAllow, pass.Verdict)

			block := assessPromQuery(tc.expr, "instant", time.Time{}, promGuardEnd, 0, promGuardPolicy{MaxRange: tc.window - 1})
			assert.Equal(t, promGuardBlock, block.Verdict)
			assert.Equal(t, []promGuardReason{promGuardReasonRange}, promGuardReasons(block))
		})
	}

	t.Run("a plain selector reads no window", func(t *testing.T) {
		d := assessPromQuery(`up`, "instant", time.Time{}, promGuardEnd, 0, promGuardPolicy{MaxRange: 1})
		assert.Equal(t, promGuardAllow, d.Verdict)
	})
}

func TestAssessPromQueryRange(t *testing.T) {
	policy := promGuardPolicy{MaxRange: 192 * time.Hour}

	t.Run("range query adds its own window", func(t *testing.T) {
		d := assessPromQuery(`rate(x[2d])`, "range", promGuardEnd.Add(-7*24*time.Hour), promGuardEnd, 3600, policy)
		require.Equal(t, promGuardBlock, d.Verdict)
		assert.Contains(t, d.Violations[0].Message, "216h0m0s")
		assert.Contains(t, d.Violations[0].Message, "including 48h0m0s of range vectors and subqueries")
		assert.Contains(t, d.Violations[0].Hint, "under 192h0m0s")
	})

	t.Run("range query window alone", func(t *testing.T) {
		assert.Equal(t, promGuardAllow, assessPromQuery(`up`, "range", promGuardEnd.Add(-192*time.Hour), promGuardEnd, 3600, policy).Verdict)
		d := assessPromQuery(`up`, "range", promGuardEnd.Add(-192*time.Hour-time.Second), promGuardEnd, 3600, policy)
		require.Equal(t, promGuardBlock, d.Verdict)
		assert.Contains(t, d.Violations[0].Message, "the time range (192h0m1s) exceeds")
	})

	t.Run("instant query has no window of its own", func(t *testing.T) {
		for _, start := range []time.Time{{}, promGuardEnd.Add(-30 * 24 * time.Hour)} {
			d := assessPromQuery(`rate(x[7d])`, "instant", start, promGuardEnd, 1, promGuardPolicy{MaxRange: 192 * time.Hour, MaxPoints: 1})
			assert.Equal(t, promGuardAllow, d.Verdict, "start %v", start)
		}
	})

	t.Run("end before start is an empty window", func(t *testing.T) {
		d := assessPromQuery(`up`, "range", promGuardEnd, promGuardEnd.Add(-30*24*time.Hour), 1, promGuardPolicy{MaxRange: time.Second, MaxPoints: 1})
		assert.Equal(t, promGuardAllow, d.Verdict)
	})

	// Without saturation these sums wrap negative and slip under the cap.
	t.Run("nested 200y ranges saturate", func(t *testing.T) {
		d := assessPromQuery(`max_over_time(rate(x[200y])[200y:1d])`, "instant", time.Time{}, promGuardEnd, 0, policy)
		assert.Equal(t, promGuardBlock, d.Verdict)
	})

	t.Run("saturated window plus a range vector", func(t *testing.T) {
		start := time.Unix(0, math.MinInt64)
		end := time.Unix(0, math.MaxInt64)
		d := assessPromQuery(`rate(x[200y])`, "range", start, end, 0, policy)
		assert.Equal(t, promGuardBlock, d.Verdict)
	})

	t.Run("zero disables the range check", func(t *testing.T) {
		d := assessPromQuery(`rate(x[200y])`, "range", promGuardEnd.Add(-1000*24*time.Hour), promGuardEnd, 1, promGuardPolicy{})
		assert.Equal(t, promGuardAllow, d.Verdict)
	})
}

func TestAssessPromQueryPoints(t *testing.T) {
	t.Run("exactly the maximum passes, one more blocks", func(t *testing.T) {
		start := promGuardEnd.Add(-time.Hour) // 61 points at a 60s step
		assert.Equal(t, promGuardAllow, assessPromQuery(`up`, "range", start, promGuardEnd, 60, promGuardPolicy{MaxPoints: 61}).Verdict)

		d := assessPromQuery(`up`, "range", start, promGuardEnd, 60, promGuardPolicy{MaxPoints: 60})
		require.Equal(t, promGuardBlock, d.Verdict)
		assert.Equal(t, []promGuardReason{promGuardReasonPoints}, promGuardReasons(d))
		assert.Contains(t, d.Violations[0].Message, "61 points")
	})

	t.Run("hint names the smallest step that fits", func(t *testing.T) {
		start := promGuardEnd.Add(-100 * time.Second)
		policy := promGuardPolicy{MaxPoints: 10}

		d := assessPromQuery(`up`, "range", start, promGuardEnd, 10, policy)
		require.Equal(t, promGuardBlock, d.Verdict)
		assert.Contains(t, d.Violations[0].Hint, "raise stepSeconds to at least 11s")
		assert.Contains(t, d.Violations[0].Hint, "shorten the time range")

		assert.Equal(t, promGuardAllow, assessPromQuery(`up`, "range", start, promGuardEnd, 11, policy).Verdict)
	})

	t.Run("huge steps do not overflow", func(t *testing.T) {
		start := promGuardEnd.Add(-time.Hour)
		policy := promGuardPolicy{MaxPoints: 1}
		for _, step := range []int{1 << 61, 1<<61 + 1, math.MaxInt} {
			assert.Equal(t, promGuardAllow, assessPromQuery(`up`, "range", start, promGuardEnd, step, policy).Verdict, "step %d", step)
		}
	})

	t.Run("max points of one", func(t *testing.T) {
		start := promGuardEnd.Add(-time.Hour)
		policy := promGuardPolicy{MaxPoints: 1}

		d := assessPromQuery(`up`, "range", start, promGuardEnd, 60, policy)
		require.Equal(t, promGuardBlock, d.Verdict)
		assert.Contains(t, d.Violations[0].Hint, "at least 3601s")

		assert.Equal(t, promGuardAllow, assessPromQuery(`up`, "range", start, promGuardEnd, 3601, policy).Verdict)
	})

	t.Run("no step means no points check", func(t *testing.T) {
		start := promGuardEnd.Add(-time.Hour)
		for _, step := range []int{0, -60} {
			assert.Equal(t, promGuardAllow, assessPromQuery(`up`, "range", start, promGuardEnd, step, promGuardPolicy{MaxPoints: 1}).Verdict)
		}
	})

	t.Run("zero disables the points check", func(t *testing.T) {
		d := assessPromQuery(`up`, "range", promGuardEnd.Add(-1000*24*time.Hour), promGuardEnd, 1, promGuardPolicy{})
		assert.Equal(t, promGuardAllow, d.Verdict)
	})
}

func TestAssessPromQueryUnparseable(t *testing.T) {
	policy := promGuardPolicy{MaxRange: 192 * time.Hour, MaxPoints: 11000}
	hour := promGuardEnd.Add(-time.Hour)

	t.Run("dashboard variable fails open", func(t *testing.T) {
		d := assessPromQuery(`rate(x[$window])`, "range", hour, promGuardEnd, 60, policy)
		assert.Equal(t, promGuardUnknown, d.Verdict)
		assert.Error(t, d.ParseErr)
		assert.Empty(t, d.Violations)
	})

	t.Run("duration expression fails open rather than reading as no window", func(t *testing.T) {
		d := assessPromQuery(`rate(x[30d + 30d])`, "instant", time.Time{}, promGuardEnd, 0, policy)
		assert.Equal(t, promGuardUnknown, d.Verdict)
	})

	t.Run("outer window over the range cap still blocks", func(t *testing.T) {
		d := assessPromQuery(`rate(x[$window])`, "range", promGuardEnd.Add(-9*24*time.Hour), promGuardEnd, 3600, policy)
		assert.Equal(t, promGuardBlock, d.Verdict)
		assert.Equal(t, []promGuardReason{promGuardReasonRange}, promGuardReasons(d))
		assert.NoError(t, d.ParseErr)
	})

	t.Run("points violation still blocks", func(t *testing.T) {
		d := assessPromQuery(`rate(x[$window])`, "range", promGuardEnd.Add(-4*time.Hour), promGuardEnd, 1, policy)
		assert.Equal(t, promGuardBlock, d.Verdict)
		assert.Equal(t, []promGuardReason{promGuardReasonPoints}, promGuardReasons(d))
	})

	// With the range check off the expression's windows are never used, so
	// failing to parse it is not worth a fail_open.
	t.Run("not parsed when the range check is off", func(t *testing.T) {
		d := assessPromQuery(`rate(x[$window])`, "range", hour, promGuardEnd, 60, promGuardPolicy{MaxPoints: 11000})
		assert.Equal(t, promGuardAllow, d.Verdict)
	})
}

func TestAssessPromQueryGrafanaMacros(t *testing.T) {
	policy := promGuardPolicy{MaxRange: 192 * time.Hour, MaxPoints: 11000}
	hour := promGuardEnd.Add(-time.Hour)

	t.Run("a macro does not hide the other windows", func(t *testing.T) {
		d := assessPromQuery(`rate(x[$__rate_interval]) + avg_over_time(y[90d])`, "range", hour, promGuardEnd, 60, policy)
		assert.Equal(t, promGuardBlock, d.Verdict)
		assert.Equal(t, []promGuardReason{promGuardReasonRange}, promGuardReasons(d))
	})

	t.Run("a macro expression within budget is admitted", func(t *testing.T) {
		d := assessPromQuery(`rate(x[${__rate_interval}]) / rate(y[$__interval])`, "range", hour, promGuardEnd, 60, policy)
		assert.Equal(t, promGuardAllow, d.Verdict)
	})

	t.Run("$__range counts as the query's own range", func(t *testing.T) {
		start := promGuardEnd.Add(-100 * time.Hour)
		d := assessPromQuery(`max_over_time(up[$__range])`, "range", start, promGuardEnd, 60, policy)
		assert.Equal(t, promGuardBlock, d.Verdict)
		assert.Contains(t, d.Violations[0].Message, "including 100h0m0s of range vectors")
	})

	t.Run("$__interval is the step, not a fraction of the range", func(t *testing.T) {
		d := assessPromQuery(`rate(x[$__interval])`, "range", promGuardEnd.Add(-191*time.Hour), promGuardEnd, 63, policy)
		assert.Equal(t, promGuardAllow, d.Verdict)
	})

	t.Run("$__rate_interval is four steps", func(t *testing.T) {
		d := assessPromQuery(`rate(x[$__rate_interval])`, "range", hour, promGuardEnd, 259200, policy)
		require.Equal(t, promGuardBlock, d.Verdict)
		assert.Contains(t, d.Violations[0].Message, "including 288h0m0s of range vectors")
	})

	t.Run("an instant $__rate_interval uses the default step", func(t *testing.T) {
		d := assessPromQuery(`rate(x[$__rate_interval])`, "instant", time.Time{}, promGuardEnd, 0, promGuardPolicy{MaxRange: 239 * time.Second})
		require.Equal(t, promGuardBlock, d.Verdict)
		assert.Contains(t, d.Violations[0].Message, "including 4m0s of range vectors")
	})

	t.Run("a sub-second $__range rounds up like Grafana's", func(t *testing.T) {
		d := assessPromQuery(`rate(x[$__range]) + avg_over_time(y[90d])`, "range", promGuardEnd.Add(-750*time.Millisecond), promGuardEnd, 1, policy)
		assert.Equal(t, promGuardBlock, d.Verdict)
	})
}

func TestAssessPromQueryViolationOrder(t *testing.T) {
	d := assessPromQuery(`up`, "range", promGuardEnd.Add(-30*24*time.Hour), promGuardEnd, 1, promGuardPolicy{MaxRange: 192 * time.Hour, MaxPoints: 11000})
	require.Equal(t, promGuardBlock, d.Verdict)
	assert.Equal(t, []promGuardReason{promGuardReasonRange, promGuardReasonPoints}, promGuardReasons(d))
}

// promGuardConfig returns a GrafanaConfig with the Prometheus guardrail in
// the given mode, capturing log output in the returned buffer.
func promGuardConfig(mode string, maxRange time.Duration, maxPoints int64) (mcpgrafana.GrafanaConfig, *bytes.Buffer) {
	var buf bytes.Buffer
	return mcpgrafana.GrafanaConfig{
		PrometheusGuardrailMode:      mode,
		PrometheusGuardrailMaxRange:  maxRange,
		PrometheusGuardrailMaxPoints: maxPoints,
		Logger:                       slog.New(slog.NewTextHandler(&buf, nil)),
	}, &buf
}

func TestGuardPrometheusQueryModes(t *testing.T) {
	start := promGuardEnd.Add(-100 * time.Second)
	// Over the points budget at a 10s step; the label value stands in for a
	// sensitive literal that must stay out of WARN logs.
	expr := `up{customer="acme-secret"}`

	t.Run("unset mode allows everything", func(t *testing.T) {
		config, buf := promGuardConfig("", 192*time.Hour, 10)
		ctx := mcpgrafana.WithGrafanaConfig(context.Background(), config)
		assert.NoError(t, guardPrometheusQuery(ctx, expr, "range", start, promGuardEnd, 10))
		assert.Empty(t, buf.String())
	})

	t.Run("shadow logs but passes", func(t *testing.T) {
		config, buf := promGuardConfig(mcpgrafana.PrometheusGuardrailShadow, 192*time.Hour, 10)
		ctx := mcpgrafana.WithGrafanaConfig(context.Background(), config)
		assert.NoError(t, guardPrometheusQuery(ctx, expr, "range", start, promGuardEnd, 10))
		assert.Contains(t, buf.String(), "level=WARN")
		assert.Contains(t, buf.String(), "would block")
		assert.NotContains(t, buf.String(), "acme-secret")
	})

	t.Run("enforce returns guidance", func(t *testing.T) {
		config, buf := promGuardConfig(mcpgrafana.PrometheusGuardrailEnforce, 192*time.Hour, 10)
		ctx := mcpgrafana.WithGrafanaConfig(context.Background(), config)
		err := guardPrometheusQuery(ctx, expr, "range", start, promGuardEnd, 10)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "Prometheus query guardrail")
		assert.Contains(t, err.Error(), "11 points per series")
		assert.Contains(t, err.Error(), "raise stepSeconds to at least 11s")
		assert.Contains(t, buf.String(), "blocked query")
		assert.NotContains(t, buf.String(), "acme-secret")
	})

	t.Run("enforce passes a query within budget", func(t *testing.T) {
		config, _ := promGuardConfig(mcpgrafana.PrometheusGuardrailEnforce, 192*time.Hour, 11000)
		ctx := mcpgrafana.WithGrafanaConfig(context.Background(), config)
		assert.NoError(t, guardPrometheusQuery(ctx, expr, "range", start, promGuardEnd, 10))
	})

	t.Run("enforce fails open on an unparseable expression", func(t *testing.T) {
		config, _ := promGuardConfig(mcpgrafana.PrometheusGuardrailEnforce, 192*time.Hour, 11000)
		ctx := mcpgrafana.WithGrafanaConfig(context.Background(), config)
		assert.NoError(t, guardPrometheusQuery(ctx, `rate(x[$window])`, "range", start, promGuardEnd, 10))
	})
}

// TestGuardPrometheusQueryUnknownModeWarnsOnce is the single test asserting
// the once-per-process warning, so it resets the once itself.
func TestGuardPrometheusQueryUnknownModeWarnsOnce(t *testing.T) {
	warnUnknownPromGuardrailModeOnce = sync.Once{}
	start := promGuardEnd.Add(-30 * 24 * time.Hour)

	config, buf := promGuardConfig("enfore", 192*time.Hour, 0)
	ctx := mcpgrafana.WithGrafanaConfig(context.Background(), config)

	require.Error(t, guardPrometheusQuery(ctx, `up`, "range", start, promGuardEnd, 60))
	assert.Contains(t, buf.String(), "unrecognized Prometheus guardrail mode")

	buf.Reset()
	require.Error(t, guardPrometheusQuery(ctx, `up`, "range", start, promGuardEnd, 60))
	assert.NotContains(t, buf.String(), "unrecognized Prometheus guardrail mode")
}

// promGuardGrafana starts a fake Grafana and returns a context whose Grafana
// client and config point at it, with the counter of requests it received.
func promGuardGrafana(t *testing.T, config mcpgrafana.GrafanaConfig) (context.Context, *int) {
	t.Helper()
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)

	u, err := url.Parse(srv.URL)
	require.NoError(t, err)
	cfg := client.DefaultTransportConfig()
	cfg.Host = u.Host
	cfg.Schemes = []string{"http"}
	cfg.APIKey = "test"
	ctx := mcpgrafana.WithGrafanaClient(context.Background(), &mcpgrafana.GrafanaClient{GrafanaHTTPAPI: client.NewHTTPClientWithConfig(nil, cfg)})
	config.URL = srv.URL
	return mcpgrafana.WithGrafanaConfig(ctx, config), &hits
}

func TestQueryPrometheusGuardrailBlocksBeforeHTTP(t *testing.T) {
	args := QueryPrometheusParams{
		DatasourceUID: "prometheus",
		Expr:          "up",
		StartTime:     "now-30d",
		EndTime:       "now",
		StepSeconds:   60,
		QueryType:     "range",
	}

	t.Run("enforce makes no request", func(t *testing.T) {
		config, _ := promGuardConfig(mcpgrafana.PrometheusGuardrailEnforce, 192*time.Hour, 11000)
		ctx, hits := promGuardGrafana(t, config)
		_, err := queryPrometheusWithHints(ctx, args)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "Prometheus query guardrail")
		assert.Zero(t, *hits, "a blocked query must not reach Grafana")
	})

	// The same query in shadow mode does reach the server, so the zero above
	// is the guardrail's doing.
	t.Run("shadow still queries", func(t *testing.T) {
		config, _ := promGuardConfig(mcpgrafana.PrometheusGuardrailShadow, 192*time.Hour, 11000)
		ctx, hits := promGuardGrafana(t, config)
		_, _ = queryPrometheusWithHints(ctx, args)
		assert.NotZero(t, *hits)
	})
}

// run_panel_query has no step parameter, so a long Prometheus panel must not
// trip the points check with a hint the caller cannot act on.
func TestRunPanelPrometheusQueryFitsPointsCheck(t *testing.T) {
	for _, maxPoints := range []int64{11000, 1000} {
		t.Run(fmt.Sprintf("max points %d", maxPoints), func(t *testing.T) {
			config, _ := promGuardConfig(mcpgrafana.PrometheusGuardrailEnforce, 192*time.Hour, maxPoints)
			ctx, hits := promGuardGrafana(t, config)
			_, err := executePrometheusQuery(ctx, "prometheus", "up", "now-190h", "now")
			if err != nil {
				assert.NotContains(t, err.Error(), "Prometheus query guardrail")
			}
			assert.NotZero(t, *hits, "a 190h panel query must reach Grafana")
		})
	}
}

func TestRunPanelPrometheusQueryShadowKeepsStep(t *testing.T) {
	config, _ := promGuardConfig(mcpgrafana.PrometheusGuardrailShadow, 192*time.Hour, 10)
	ctx := mcpgrafana.WithGrafanaConfig(context.Background(), config)
	assert.Equal(t, 60, panelPrometheusStep(ctx, time.Hour))
}
