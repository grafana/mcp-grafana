// Package lokiguard estimates the cost of a LogQL query before it runs and
// says whether it should be allowed, so agents cannot scan terabytes of logs
// by accident.
//
// Loki does not enforce max_query_bytes_read on log queries without a line
// filter, so a broad stream selector over a wide window passes every
// server-side check. [Assess] closes that gap client-side. It requires a
// selective stream selector, bounds the effective time range (including
// range-vector durations like [30d]), and asks Loki's index/stats API for a
// byte estimate through a caller-supplied [StatsFunc].
//
// The package only returns a [Decision]. What to do with it (block, prompt,
// warn, log) belongs to the caller, as does the choice of what to do when the
// verdict is [Unknown] (an unparseable query or a failed estimate).
//
// It has no dependencies outside the standard library. LogQL is scanned with
// a small hand-written scanner rather than the Loki parser, which would pull
// in a very large dependency tree. Any query shape the scanner does not
// understand yields [Unknown].
package lokiguard

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"
)

// Query is one Loki query to assess.
type Query struct {
	// LogQL is the full query, log or metric. # and // and /* */ comments are
	// ignored.
	LogQL string
	// Start and End bound a range query. Both may be zero for an instant
	// query, whose cost is then set by its range vectors alone.
	Start, End time.Time
	// Instant marks an instant query: Start and End are not a scan window.
	// End, when set, anchors range vectors at that time.
	Instant bool
}

// Policy is the budget a query is assessed against. A zero field disables
// that check.
type Policy struct {
	// MaxBytes caps the estimated bytes scanned, summed over every stream
	// selector in the query. It has no effect without a StatsFunc.
	MaxBytes int64
	// MaxRange caps the effective time range: the query window plus the
	// largest range-vector duration.
	MaxRange time.Duration
}

// StatsFunc returns Loki's index/stats byte estimate for a bare stream
// selector over [start, end]. It is called once per distinct selector, and
// only for queries that pass the static checks.
type StatsFunc func(ctx context.Context, selector string, start, end time.Time) (bytes int64, err error)

// Kind identifies which check a [Reason] came from. The values are stable and
// safe to use as metric labels.
type Kind string

const (
	// KindSelector means a stream selector is catch-all, empty or negative-only.
	KindSelector Kind = "selector"
	// KindRange means the effective time range exceeds [Policy.MaxRange].
	KindRange Kind = "range"
	// KindBytes means the estimated scan exceeds [Policy.MaxBytes].
	KindBytes Kind = "bytes"
)

// Reason is one failed check: a machine-readable kind plus a message that is
// safe to show to a user or an agent.
type Reason struct {
	Kind    Kind
	Message string
}

// Verdict is the outcome of [Assess].
type Verdict int

const (
	// Allow means every enabled check passed.
	Allow Verdict = iota
	// Block means at least one check failed. See [Decision.Reasons].
	Block
	// Unknown means no verdict could be reached. See [Decision.Cause]. The
	// caller decides whether to run the query.
	Unknown
)

func (v Verdict) String() string {
	switch v {
	case Allow:
		return "allow"
	case Block:
		return "block"
	case Unknown:
		return "unknown"
	default:
		return fmt.Sprintf("Verdict(%d)", int(v))
	}
}

// Cause says why a verdict is [Unknown]. The values are stable and safe to
// use as metric labels.
type Cause string

const (
	// CauseUnparseable means the query has no stream selector the scanner
	// understands. Invalid syntax is rejected by Loki with a proper error, so
	// guessing here risks blocking legitimate syntax the scanner doesn't know.
	CauseUnparseable Cause = "unparseable"
	// CauseEstimateFailed means the static checks passed but the byte
	// estimate could not be obtained, so the budget check did not happen.
	CauseEstimateFailed Cause = "estimate_failed"
)

// Decision is the result of [Assess].
type Decision struct {
	Verdict Verdict
	// Reasons is non-empty exactly when Verdict is Block.
	Reasons []Reason
	// Cause is set exactly when Verdict is Unknown.
	Cause Cause
	// Selectors are the raw stream selectors found in the query, for logging.
	// Prefer them to the full LogQL, which can embed sensitive literals in
	// line filters.
	Selectors []string
	// EstimatedBytes is the summed index/stats estimate. It is set only when
	// the byte check ran and produced a total.
	EstimatedBytes int64
	// Err is the first StatsFunc error, if any. It is set for
	// CauseEstimateFailed, and also for a Block that rests on a partial total
	// (see Assess).
	Err error
}

// Assess decides whether a query is cheap enough to run under p.
//
// The checks run in order: selective selectors, then the time-range cap,
// then the byte budget. The StatsFunc is only called when the first two
// pass, so a query that is already blocked costs no index/stats round trip.
// stats may be nil, which skips the byte budget.
//
// Estimates cover the bare selector: line filters and parsers do not reduce
// bytes scanned, so they are accurate for exactly the queries that hurt.
// Both sides of a binary metric expression are scanned, so their estimates
// sum, and a selector repeated in the query counts once per occurrence.
//
// If a stats call fails after the running total is already over budget, the
// result is still Block: bytes are additive, so the remaining selectors can
// only raise the total. Otherwise a stats failure gives Unknown.
func Assess(ctx context.Context, q Query, p Policy, stats StatsFunc) Decision {
	logql := stripLogQLComments(q.LogQL)
	selectors := parseLogQLSelectors(logql)
	if len(selectors) == 0 {
		return Decision{Verdict: Unknown, Cause: CauseUnparseable}
	}
	raws := make([]string, len(selectors))
	for i, sel := range selectors {
		raws[i] = sel.raw
	}
	d := Decision{Selectors: raws}

	for _, sel := range selectors {
		if !hasSelectivePositiveMatcher(sel.matchers) {
			d.Reasons = append(d.Reasons, Reason{
				Kind:    KindSelector,
				Message: fmt.Sprintf("the stream selector %s has no selective label matcher (catch-all, empty, or negative-only selectors scan every stream)", sel.raw),
			})
		}
	}

	// Range vectors extend the scan beyond the query window, and instant
	// queries carry no window at all: their cost is set entirely by the
	// vector duration ([30d] in count_over_time({...}[30d])).
	vecDur := maxVectorDuration(logql)

	if p.MaxRange > 0 {
		window := time.Duration(0)
		if !q.Instant && !q.Start.IsZero() && !q.End.IsZero() {
			window = q.End.Sub(q.Start)
		}
		// End.Sub(Start) saturates at MaxInt64 for extreme spans, so a plain
		// addition of vecDur would wrap negative and pass the cap.
		effective := window
		if vecDur > 0 && effective > math.MaxInt64-vecDur {
			effective = math.MaxInt64
		} else {
			effective += vecDur
		}
		if effective > p.MaxRange {
			message := fmt.Sprintf("the time range (%s) exceeds the maximum allowed (%s)", effective.Round(time.Minute), p.MaxRange)
			if vecDur > 0 {
				message = fmt.Sprintf("the time range (%s, including the %s range vector) exceeds the maximum allowed (%s)", effective.Round(time.Minute), vecDur, p.MaxRange)
			}
			d.Reasons = append(d.Reasons, Reason{Kind: KindRange, Message: message})
		}
	}

	// Only spend the index-stats round trips on queries that pass the static
	// checks.
	if len(d.Reasons) == 0 && p.MaxBytes > 0 && stats != nil {
		if from, to, ok := statsWindow(q, vecDur); ok {
			total, ok, err := sumSelectorBytes(ctx, selectors, from, to, p.MaxBytes, stats)
			d.Err = err
			switch {
			case !ok:
				d.Verdict = Unknown
				d.Cause = CauseEstimateFailed
				return d
			case total > p.MaxBytes:
				d.EstimatedBytes = total
				d.Reasons = append(d.Reasons, Reason{
					Kind: KindBytes,
					Message: fmt.Sprintf("Loki estimates this query would scan %s, above the %s budget",
						humanizeBytes(total), humanizeBytes(p.MaxBytes)),
				})
			default:
				d.EstimatedBytes = total
			}
		}
	}

	if len(d.Reasons) > 0 {
		d.Verdict = Block
	}
	return d
}

// statsWindow computes the window for the index/stats estimate. Range
// vectors widen the window backwards: a [7d] vector over a 1h window scans
// 7d+1h of chunks. Instant queries have no window of their own, so the vector
// duration is anchored at the query time (End when given, otherwise Start,
// otherwise now); with no vector duration there is nothing to estimate and
// the byte check is skipped. Offset modifiers are ignored: the range cap is
// unaffected (offset shifts the window, it does not widen it), and the stats
// window estimates the unshifted interval, a neutral-direction skew in the
// byte estimate.
func statsWindow(q Query, vecDur time.Duration) (time.Time, time.Time, bool) {
	if q.Instant || q.Start.IsZero() || q.End.IsZero() {
		if vecDur == 0 {
			return time.Time{}, time.Time{}, false
		}
		anchor := q.End
		if anchor.IsZero() {
			anchor = q.Start
		}
		if anchor.IsZero() {
			anchor = time.Now()
		}
		return anchor.Add(-vecDur), anchor, true
	}
	return q.Start.Add(-vecDur), q.End, true
}

// sumSelectorBytes sums the index/stats byte estimates across the query's
// selectors. Identical selectors are fetched once but their estimate is
// multiplied by the occurrence count, because each leg of a binary expression
// scans independently. It returns ok=false when an estimate fails while the
// verdict is still undetermined; a failure after the total already exceeds
// maxBytes keeps the partial total and returns the error alongside ok=true.
func sumSelectorBytes(ctx context.Context, selectors []parsedSelector, start, end time.Time, maxBytes int64, stats StatsFunc) (total int64, ok bool, err error) {
	counts := make(map[string]int64, len(selectors))
	for _, sel := range selectors {
		counts[sel.raw]++
	}
	seen := make(map[string]bool, len(counts))
	for _, sel := range selectors {
		if seen[sel.raw] {
			continue
		}
		seen[sel.raw] = true
		b, serr := stats(ctx, sel.raw, start, end)
		if serr != nil {
			if total > maxBytes {
				return total, true, serr
			}
			return 0, false, serr
		}
		if b < 0 {
			b = 0
		}
		// Saturate rather than wrap: an overflowing total means "over any
		// conceivable budget", not a free pass.
		count := counts[sel.raw]
		if b > 0 && b > math.MaxInt64/count {
			return math.MaxInt64, true, nil
		}
		total += b * count
		if total < 0 {
			return math.MaxInt64, true, nil
		}
	}
	return total, true, nil
}

// Guidance renders the message returned to an agent whose query was blocked:
// the reasons, then how to rewrite the query. scopeHint names the caller's own
// discovery tools, for example "use query_loki_stats to scope the query
// first"; it may be empty. Guidance returns "" unless d.Verdict is Block.
func Guidance(d Decision, p Policy, scopeHint string) string {
	if d.Verdict != Block {
		return ""
	}
	reasons := make([]string, len(d.Reasons))
	for i, r := range d.Reasons {
		reasons[i] = r.Message
	}
	rangeHint := "start narrow, e.g. the last few minutes, and widen only if needed"
	if p.MaxRange > 0 {
		rangeHint = fmt.Sprintf("keep it well under the %s cap", p.MaxRange)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "query blocked by the Loki cost guardrail: %s. Rewrite and retry: narrow the stream selector "+
		"with specific label matchers (e.g. namespace and app/container), shorten the time range (%s)",
		strings.Join(reasons, "; "), rangeHint)
	if scopeHint != "" {
		fmt.Fprintf(&b, ", and %s", scopeHint)
	}
	b.WriteString(". Prefer a metric query (e.g. count_over_time) over reading raw lines when you only need counts. " +
		"Splitting a broad scan into many narrower-window queries does not reduce the total cost.")
	return b.String()
}
