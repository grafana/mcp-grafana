package lokiguard

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"
	"time"
)

var (
	testEnd    = time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	testStart  = testEnd.Add(-time.Hour)
	testPolicy = Policy{MaxBytes: 100 << 30, MaxRange: 24 * time.Hour}
)

// statsOf returns a StatsFunc that answers every selector with b and records
// the selectors it was asked about.
func statsOf(b int64, asked *[]string) StatsFunc {
	return func(_ context.Context, selector string, _, _ time.Time) (int64, error) {
		if asked != nil {
			*asked = append(*asked, selector)
		}
		return b, nil
	}
}

func rangeQuery(logql string) Query {
	return Query{LogQL: logql, Start: testStart, End: testEnd}
}

func wantBlock(t *testing.T, d Decision, kind Kind, messageContains string) {
	t.Helper()
	if d.Verdict != Block {
		t.Fatalf("verdict = %s, want block (reasons %+v)", d.Verdict, d.Reasons)
	}
	if len(d.Reasons) != 1 {
		t.Fatalf("got %d reasons, want 1: %+v", len(d.Reasons), d.Reasons)
	}
	if d.Reasons[0].Kind != kind {
		t.Errorf("reason kind = %q, want %q", d.Reasons[0].Kind, kind)
	}
	if !strings.Contains(d.Reasons[0].Message, messageContains) {
		t.Errorf("reason message %q does not contain %q", d.Reasons[0].Message, messageContains)
	}
}

func wantAllow(t *testing.T, d Decision) {
	t.Helper()
	if d.Verdict != Allow || len(d.Reasons) != 0 {
		t.Fatalf("verdict = %s reasons %+v, want allow", d.Verdict, d.Reasons)
	}
}

func TestAssessStaticChecks(t *testing.T) {
	ctx := context.Background()
	p := Policy{MaxRange: 24 * time.Hour}

	d := Assess(ctx, rangeQuery(`{cluster=~".+"} |= "error"`), p, nil)
	wantBlock(t, d, KindSelector, "selective")
	if !strings.Contains(d.Reasons[0].Message, `{cluster=~".+"}`) {
		t.Errorf("message should name the offending selector: %q", d.Reasons[0].Message)
	}
	if len(d.Selectors) != 1 || d.Selectors[0] != `{cluster=~".+"}` {
		t.Errorf("Selectors = %v", d.Selectors)
	}

	d = Assess(ctx, Query{LogQL: `{namespace="foo", app="bar"}`, Start: testEnd.Add(-48 * time.Hour), End: testEnd}, p, nil)
	wantBlock(t, d, KindRange, "time range")

	// A catch-all on either side of a binary expression is flagged.
	d = Assess(ctx, rangeQuery(`sum(rate({app="x"}[5m])) / sum(rate({cluster=~".+"}[5m]))`), p, nil)
	wantBlock(t, d, KindSelector, `{cluster=~".+"}`)

	// Unparseable queries are Unknown, not Allow: no verdict was reached.
	d = Assess(ctx, rangeQuery(`{app=="x"}`), p, nil)
	if d.Verdict != Unknown || d.Cause != CauseUnparseable || len(d.Reasons) != 0 {
		t.Errorf("unparseable: got %s cause %q reasons %+v", d.Verdict, d.Cause, d.Reasons)
	}

	// A selective selector within the range cap is allowed outright.
	wantAllow(t, Assess(ctx, rangeQuery(`{namespace="foo", app="bar"}`), p, nil))
}

func TestAssessIgnoresCommentedOutText(t *testing.T) {
	ctx := context.Background()
	p := Policy{MaxRange: 24 * time.Hour}
	wantAllow(t, Assess(ctx, rangeQuery(`{app="x"} # see {cluster=~".+"} for all`), p, nil))
	wantAllow(t, Assess(ctx, rangeQuery(`{app="x"} # [30d]`), p, nil))
	wantAllow(t, Assess(ctx, rangeQuery("{app=\"x\"} // see {cluster=~\".+\"} [30d]"), p, nil))
	wantBlock(t, Assess(ctx, rangeQuery(`{cluster/*x*/=~".+"}`), p, nil), KindSelector, "selective")
}

func TestAssessRangeVector(t *testing.T) {
	ctx := context.Background()
	p := Policy{MaxRange: 24 * time.Hour}
	instant := func(logql string) Query { return Query{LogQL: logql, Instant: true} }

	// Instant metric query: no window of its own, cost set by the vector.
	wantBlock(t, Assess(ctx, instant(`sum(count_over_time({namespace="foo"}[30d]))`), p, nil), KindRange, "range vector")

	// The vector duration counts on top of the range query window.
	d := Assess(ctx, Query{LogQL: `rate({namespace="foo"}[20h])`, Start: testEnd.Add(-8 * time.Hour), End: testEnd}, p, nil)
	wantBlock(t, d, KindRange, "time range")

	// A small vector on an instant query is fine.
	wantAllow(t, Assess(ctx, instant(`sum(count_over_time({namespace="foo"}[1h]))`), p, nil))

	// Fractional durations count too: [720.5h] must not slip past the cap.
	wantBlock(t, Assess(ctx, instant(`sum(count_over_time({namespace="foo"}[720.5h]))`), p, nil), KindRange, "range vector")

	// A multi-century span saturates End.Sub(Start). Adding the vector
	// duration must saturate too, not wrap negative and pass the cap.
	d = Assess(ctx, Query{LogQL: `sum(count_over_time({namespace="foo"}[7d]))`, Start: time.Date(1000, 1, 1, 0, 0, 0, 0, time.UTC), End: testEnd}, p, nil)
	wantBlock(t, d, KindRange, "time range")
}

func TestAssessByteBudget(t *testing.T) {
	ctx := context.Background()

	var asked []string
	d := Assess(ctx, rangeQuery(`{namespace="foo", app="bar"} |= "error"`), testPolicy, statsOf(200<<30, &asked))
	wantBlock(t, d, KindBytes, "budget")
	if d.EstimatedBytes != 200<<30 {
		t.Errorf("EstimatedBytes = %d, want %d", d.EstimatedBytes, int64(200<<30))
	}
	// The estimate is for the bare selector: line filters do not reduce bytes
	// scanned.
	if len(asked) != 1 || asked[0] != `{namespace="foo", app="bar"}` {
		t.Errorf("stats asked for %v", asked)
	}

	d = Assess(ctx, rangeQuery(`{namespace="foo", app="bar"}`), testPolicy, statsOf(1<<30, nil))
	wantAllow(t, d)
	if d.EstimatedBytes != 1<<30 {
		t.Errorf("an allowed query should still report its estimate, got %d", d.EstimatedBytes)
	}
}

func TestAssessByteBudgetInstantWindow(t *testing.T) {
	// Instant queries anchor the stats window at the query time, widened
	// backwards by the vector duration.
	var gotStart, gotEnd time.Time
	stats := func(_ context.Context, _ string, s, e time.Time) (int64, error) {
		gotStart, gotEnd = s, e
		return 200 << 30, nil
	}
	q := Query{LogQL: `sum(count_over_time({namespace="foo"}[6h]))`, End: testEnd, Instant: true}
	wantBlock(t, Assess(context.Background(), q, testPolicy, stats), KindBytes, "budget")
	if !gotEnd.Equal(testEnd) || !gotStart.Equal(testEnd.Add(-6*time.Hour)) {
		t.Errorf("stats window = [%s, %s]", gotStart, gotEnd)
	}
}

func TestAssessSumsSelectors(t *testing.T) {
	ctx := context.Background()

	// Both sides of a binary expression are scanned; their estimates sum.
	var asked []string
	logql := `sum(rate({namespace="a", app="x"}[5m])) / sum(rate({namespace="a", app="y"}[5m]))`
	wantBlock(t, Assess(ctx, rangeQuery(logql), testPolicy, statsOf(60<<30, &asked)), KindBytes, "budget")
	if len(asked) != 2 || asked[0] != `{namespace="a", app="x"}` || asked[1] != `{namespace="a", app="y"}` {
		t.Errorf("stats asked for %v", asked)
	}

	// Repeated selectors are fetched once but each occurrence counts: both
	// legs scan independently, so 60% of budget twice is over budget, with a
	// single stats round trip.
	asked = nil
	logql = `sum(rate({namespace="a", app="x"}[5m])) / sum(rate({namespace="a", app="x"}[10m]))`
	wantBlock(t, Assess(ctx, rangeQuery(logql), testPolicy, statsOf(60<<30, &asked)), KindBytes, "budget")
	if len(asked) != 1 {
		t.Errorf("repeated selector fetched %d times, want 1", len(asked))
	}
}

func TestAssessByteSumSaturates(t *testing.T) {
	// A huge estimate times many occurrences must saturate, not wrap negative
	// and bypass the budget.
	logql := `sum(rate({app="x"}[5m])) + sum(rate({app="x"}[5m])) + sum(rate({app="x"}[5m]))`
	d := Assess(context.Background(), rangeQuery(logql), testPolicy, statsOf(math.MaxInt64/2, nil))
	wantBlock(t, d, KindBytes, "budget")
	if d.EstimatedBytes != math.MaxInt64 {
		t.Errorf("EstimatedBytes = %d, want saturation at MaxInt64", d.EstimatedBytes)
	}
}

func TestAssessNegativeEstimateCountsAsZero(t *testing.T) {
	// A negative estimate must not reduce the total and mask a later leg.
	stats := func(_ context.Context, selector string, _, _ time.Time) (int64, error) {
		if selector == `{app="a"}` {
			return -500 << 30, nil
		}
		return 200 << 30, nil
	}
	d := Assess(context.Background(), rangeQuery(`rate({app="a"}[5m]) / rate({app="b"}[5m])`), testPolicy, stats)
	wantBlock(t, d, KindBytes, "budget")
}

func TestAssessByteCheckSkippedWithoutWindow(t *testing.T) {
	// An instant log query with no bounds and no range vector has no window
	// to estimate over, so only the static selector check applies.
	var asked []string
	d := Assess(context.Background(), Query{LogQL: `{namespace="foo"}`, Instant: true}, testPolicy, statsOf(200<<30, &asked))
	wantAllow(t, d)
	if len(asked) != 0 {
		t.Errorf("stats called for a query with no window: %v", asked)
	}
}

func TestAssessNilStatsOrZeroBudgetSkipsEstimate(t *testing.T) {
	ctx := context.Background()
	q := rangeQuery(`{namespace="foo"}`)
	wantAllow(t, Assess(ctx, q, testPolicy, nil))

	var asked []string
	wantAllow(t, Assess(ctx, q, Policy{MaxRange: 24 * time.Hour}, statsOf(1<<50, &asked)))
	if len(asked) != 0 {
		t.Errorf("stats called with MaxBytes=0: %v", asked)
	}
}

func TestAssessStatsFailureIsUnknown(t *testing.T) {
	boom := errors.New("boom")
	stats := func(context.Context, string, time.Time, time.Time) (int64, error) { return 0, boom }
	d := Assess(context.Background(), rangeQuery(`{namespace="foo", app="bar"}`), testPolicy, stats)
	if d.Verdict != Unknown || d.Cause != CauseEstimateFailed || len(d.Reasons) != 0 {
		t.Errorf("got %s cause %q reasons %+v", d.Verdict, d.Cause, d.Reasons)
	}
	if !errors.Is(d.Err, boom) {
		t.Errorf("Err = %v, want the stats error", d.Err)
	}
}

func TestAssessPartialTotalOverBudgetStillBlocks(t *testing.T) {
	logql := `sum(rate({namespace="a", app="x"}[5m])) / sum(rate({namespace="a", app="y"}[5m]))`
	boom := errors.New("boom")

	// The first leg alone is over budget and the second leg's stats call
	// fails. Bytes are additive, so the over-budget verdict must stand.
	stats := func(_ context.Context, selector string, _, _ time.Time) (int64, error) {
		if selector == `{namespace="a", app="x"}` {
			return 500 << 30, nil
		}
		return 0, boom
	}
	d := Assess(context.Background(), rangeQuery(logql), testPolicy, stats)
	wantBlock(t, d, KindBytes, "budget")
	if !errors.Is(d.Err, boom) {
		t.Errorf("a partial-total block should still carry the stats error, got %v", d.Err)
	}

	// Under budget when the error hits: still undetermined.
	stats = func(_ context.Context, selector string, _, _ time.Time) (int64, error) {
		if selector == `{namespace="a", app="x"}` {
			return 1 << 30, nil
		}
		return 0, boom
	}
	d = Assess(context.Background(), rangeQuery(logql), testPolicy, stats)
	if d.Verdict != Unknown || d.Cause != CauseEstimateFailed {
		t.Errorf("got %s cause %q, want unknown/estimate_failed", d.Verdict, d.Cause)
	}
}

func TestAssessSkipsStatsForBlockedQuery(t *testing.T) {
	var asked []string
	d := Assess(context.Background(), rangeQuery(`{cluster=~".+"}`), testPolicy, statsOf(0, &asked))
	if d.Verdict != Block {
		t.Fatalf("verdict = %s, want block", d.Verdict)
	}
	if len(asked) != 0 {
		t.Errorf("index/stats round trip must be skipped when static checks already block, got %v", asked)
	}
}

func TestGuidance(t *testing.T) {
	d := Assess(context.Background(), rangeQuery(`{cluster=~".+"}`), testPolicy, nil)
	got := Guidance(d, testPolicy, "use query_loki_stats to scope the query first")
	for _, want := range []string{
		"query blocked by the Loki cost guardrail",
		"selective label matcher",
		"narrow the stream selector",
		"keep it well under the 24h0m0s cap",
		", and use query_loki_stats to scope the query first.",
		"metric query",
		"Splitting a broad scan",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("guidance missing %q:\n%s", want, got)
		}
	}

	if got := Guidance(d, Policy{}, ""); !strings.Contains(got, "start narrow") || strings.Contains(got, ", and use") {
		t.Errorf("guidance without a range cap or hint:\n%s", got)
	}
	if got := Guidance(Decision{Verdict: Allow}, testPolicy, ""); got != "" {
		t.Errorf("guidance for a non-blocked decision should be empty, got %q", got)
	}
}
