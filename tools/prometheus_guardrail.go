// This file holds the opt-in Prometheus query guardrail: a pure assessment of
// a PromQL query against selector, range and resolution policies, and the policy shell
// that logs, records metrics, and turns a block into guidance for the agent.
package tools

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"

	mcpgrafana "github.com/grafana/mcp-grafana/v2"
	"github.com/prometheus/common/model"
	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/promql/parser"
)

// promGuardParser parses expressions for the guardrail only. Experimental
// functions are accepted so they are assessed rather than failing open.
// Duration expressions always parse since Prometheus v0.316, but their
// numeric Range is 0 until evaluated, so promGuardDurationExpr rejects them
// rather than letting them read as no window at all.
var promGuardParser = parser.NewParser(parser.Options{EnableExperimentalFunctions: true})

// warnUnknownPromGuardrailModeOnce gates the once-per-process warning for
// unrecognized modes. A variable so tests can reset it.
var warnUnknownPromGuardrailModeOnce sync.Once

type promGuardPolicy struct {
	RequireSelectiveLabels bool          // false disables the selector check
	MaxRange               time.Duration // 0 disables the range check
	MaxPoints              int64         // 0 disables the points check
}

// promGuardReason values become the `reason` metric attribute, so the set is
// closed.
type promGuardReason string

const (
	promGuardReasonCardinality        promGuardReason = "cardinality"
	promGuardReasonCardinalityUnknown promGuardReason = "cardinality_unknown"
	promGuardReasonRange              promGuardReason = "range"
	promGuardReasonPoints             promGuardReason = "points"
	promGuardReasonSelector           promGuardReason = "selector"
	promGuardReasonUnparseable        promGuardReason = "unparseable"
)

type promGuardVerdict int

const (
	promGuardAllow promGuardVerdict = iota
	promGuardBlock
	// promGuardUnknown means no check was violated but the expression could
	// not be parsed, so its selectors and range windows could not be fully assessed.
	promGuardUnknown
)

type promGuardViolation struct {
	Reason  promGuardReason
	Message string
	Hint    string
}

type promGuardDecision struct {
	Verdict                  promGuardVerdict
	Violations               []promGuardViolation         // Block only; selector, then range, then points
	UnknownReason            promGuardReason              // Empty means unparseable.
	ParseErr                 error                        // Unknown only
	CardinalityFailOpenCause promCardinalityFailOpenCause // Cardinality unknown only
}

// assessPromQuery checks a PromQL query against p. queryType is "range" or
// "instant". The range and points checks on the query's own window do not
// need the expression, so a parse failure blocks when either of them trips
// and is reported as Unknown only when nothing else would block.
func assessPromQuery(expr, queryType string, start, end time.Time, stepSeconds int, p promGuardPolicy) promGuardDecision {
	var window time.Duration
	if queryType == "range" && end.After(start) {
		window = end.Sub(start)
	}

	var d promGuardDecision
	var parseErr error

	var extension time.Duration
	if p.MaxRange > 0 || p.RequireSelectiveLabels {
		ast, err := promGuardParser.ParseExpr(promGuardSubstituteMacros(expr, window, stepSeconds))
		if err == nil {
			err = promGuardDurationExpr(ast)
		}
		if err != nil {
			parseErr = err
		} else {
			if p.RequireSelectiveLabels {
				if v, ok := checkPromSelectors(ast); ok {
					d.Violations = append(d.Violations, v)
				}
			}
			if p.MaxRange > 0 {
				extension = promAstWindow(ast)
			}
		}
	}
	if p.MaxRange > 0 {
		if v, ok := checkPromRange(window, extension, p.MaxRange); ok {
			d.Violations = append(d.Violations, v)
		}
	}
	if p.MaxPoints > 0 && queryType == "range" && stepSeconds > 0 {
		if v, ok := checkPromPoints(window, int64(stepSeconds), p.MaxPoints); ok {
			d.Violations = append(d.Violations, v)
		}
	}

	switch {
	case len(d.Violations) > 0:
		d.Verdict = promGuardBlock
	case parseErr != nil:
		d.Verdict = promGuardUnknown
		d.ParseErr = parseErr
	}
	return d
}

// promGuardSubstituteMacros fills in Grafana's built-in macros with the values
// its Prometheus datasource uses server-side, so that one $__rate_interval
// does not leave every other window in the expression unchecked. $__interval
// skips Grafana's rounding to a whole unit, which moves it by a few seconds.
func promGuardSubstituteMacros(expr string, window time.Duration, stepSeconds int) string {
	step := int64(stepSeconds)
	if step <= 0 {
		step = 60 // the backend's default
	}
	// Grafana widens the step so no series exceeds maxDataPoints.
	interval := strconv.FormatInt(max(step, int64(window/time.Second)/prometheusMaxPointsPerSeries), 10)
	// The backend sends the step as the query's min interval, which Grafana
	// takes as the scrape interval: $__rate_interval is four of them.
	rateInterval := strconv.FormatInt(4*step, 10)
	rangeMs := window.Milliseconds()
	rangeS := strconv.FormatInt((rangeMs+500)/1000, 10)
	// Longer names come first: the replacer tries them in argument order.
	return strings.NewReplacer(
		"${__range_ms}", strconv.FormatInt(rangeMs, 10), "$__range_ms", strconv.FormatInt(rangeMs, 10),
		"${__range_s}", rangeS, "$__range_s", rangeS,
		"${__range}", rangeS+"s", "$__range", rangeS+"s",
		"${__rate_interval_ms}", rateInterval+"000", "$__rate_interval_ms", rateInterval+"000",
		"${__rate_interval}", rateInterval+"s", "$__rate_interval", rateInterval+"s",
		"${__interval_ms}", interval+"000", "$__interval_ms", interval+"000",
		"${__interval}", interval+"s", "$__interval", interval+"s",
	).Replace(expr)
}

// checkPromSelectors requires a positive, non-empty, non-blanket label
// constraint other than the metric name in EACH selector. This is a static
// heuristic, not a cardinality estimate. A negative universal matcher makes
// the selector empty and is exempt, regardless of the other matchers.
func checkPromSelectors(ast parser.Node) (promGuardViolation, bool) {
	var blocked bool
	parser.Inspect(ast, func(node parser.Node, _ []parser.Node) error {
		vs, ok := node.(*parser.VectorSelector)
		if !ok {
			return nil
		}
		for _, m := range vs.LabelMatchers {
			if m.Type == labels.MatchNotRegexp && classifyPromRegex(m.Value) == promRegexAll {
				return nil
			}
			if m.Name == model.MetricNameLabel || m.Matches("") {
				continue
			}
			if m.Type == labels.MatchEqual || (m.Type == labels.MatchRegexp && classifyPromRegex(m.Value) == promRegexOther) {
				return nil
			}
		}
		blocked = true
		return errPromUnscopedSelector
	})
	if !blocked {
		return promGuardViolation{}, false
	}
	return promGuardViolation{
		Reason:  promGuardReasonSelector,
		Message: "a vector selector has no selective positive label matcher beyond the metric name",
		Hint:    `add a non-empty exact label value or a constrained positive regex to each unscoped selector (for example, up{job="api"} or up{job=~"api|worker"}), using values appropriate to the intended scope; removing a wildcard alone does not reduce the selected series`,
	}, true
}

var errPromUnscopedSelector = errors.New("unscoped Prometheus selector")

// promGuardDurationExpr reports a range or subquery whose window is a
// duration expression, which the parser leaves unevaluated.
func promGuardDurationExpr(ast parser.Node) error {
	var err error
	parser.Inspect(ast, func(node parser.Node, _ []parser.Node) error {
		switch n := node.(type) {
		case *parser.MatrixSelector:
			if n.RangeExpr != nil {
				err = fmt.Errorf("duration expression %s in a range selector", n.RangeExpr)
			}
		case *parser.SubqueryExpr:
			if n.RangeExpr != nil {
				err = fmt.Errorf("duration expression %s in a subquery range", n.RangeExpr)
			}
		}
		return err
	})
	return err
}

// promAstWindow returns the longest selector window: its own range plus
// all enclosing subquery ranges. offset shifts rather than widens the window,
// and ignoring @ can only overestimate.
func promAstWindow(ast parser.Node) time.Duration {
	var longest time.Duration
	parser.Inspect(ast, func(node parser.Node, path []parser.Node) error {
		if _, ok := node.(*parser.VectorSelector); !ok {
			return nil
		}
		var w time.Duration
		for _, n := range path {
			switch n := n.(type) {
			case *parser.MatrixSelector:
				w = saturatingAdd(w, n.Range)
			case *parser.SubqueryExpr:
				w = saturatingAdd(w, n.Range)
			}
		}
		longest = max(longest, w)
		return nil
	})
	return longest
}

func checkPromRange(window, extension, maxRange time.Duration) (promGuardViolation, bool) {
	effective := saturatingAdd(window, extension)
	if effective <= maxRange {
		return promGuardViolation{}, false
	}
	message := fmt.Sprintf("the time range (%s) exceeds the maximum allowed (%s)", effective, maxRange)
	if extension > 0 {
		message = fmt.Sprintf("the time range (%s, including %s of range vectors and subqueries) exceeds the maximum allowed (%s)", effective, extension, maxRange)
	}
	return promGuardViolation{
		Reason:  promGuardReasonRange,
		Message: message,
		Hint:    fmt.Sprintf("shorten the time range, or the range vectors and subquery ranges in the expression, so the total stays under %s", maxRange),
	}, true
}

// checkPromPoints bounds the resolution the query asks for, not the points
// Grafana returns: Grafana may align the range or widen the step.
func checkPromPoints(window time.Duration, step, maxPoints int64) (promGuardViolation, bool) {
	windowMs := window.Milliseconds()
	// Divide in two steps: step*1000 overflows for very large steps.
	points := windowMs/1000/step + 1
	if points <= maxPoints {
		return promGuardViolation{}, false
	}
	minStep := promMinStepSeconds(window, maxPoints)
	return promGuardViolation{
		Reason:  promGuardReasonPoints,
		Message: fmt.Sprintf("the query requests %d points per series (%s at a %ds step), more than the maximum allowed (%d)", points, window, step, maxPoints),
		Hint:    fmt.Sprintf("raise stepSeconds to at least %ds (query_prometheus), or shorten the time range", minStep),
	}, true
}

// promMinStepSeconds returns the smallest integer step s with
// floor(window/s) + 1 <= maxPoints. maxPoints*1000 overflows only for limits
// far beyond any window that time.Duration can hold.
func promMinStepSeconds(window time.Duration, maxPoints int64) int64 {
	return window.Milliseconds()/(maxPoints*1000) + 1
}

// saturatingAdd adds two non-negative durations, capping at the maximum
// instead of wrapping negative and slipping under the cap.
func saturatingAdd(a, b time.Duration) time.Duration {
	if a > math.MaxInt64-b {
		return math.MaxInt64
	}
	return a + b
}

func promGuardGuidance(d promGuardDecision) string {
	messages := make([]string, len(d.Violations))
	hints := make([]string, len(d.Violations))
	for i, v := range d.Violations {
		messages[i] = v.Message
		hints[i] = v.Hint
	}
	return fmt.Sprintf("query blocked by the Prometheus query guardrail: %s. Rewrite and retry: %s. "+
		"Splitting the query into many narrower-window queries does not reduce the total cost.",
		strings.Join(messages, "; "), strings.Join(hints, "; "))
}

// guardPrometheusQuery applies the opt-in Prometheus query guardrail before a
// query reaches the datasource. In shadow mode a blocked query is logged and
// runs anyway; in enforce mode the guidance is returned as an error, which
// ConvertTool surfaces as a tool result with isError=true.
func guardPrometheusQuery(ctx context.Context, expr, queryType string, start, end time.Time, stepSeconds int, cardinality ...func() promGuardDecision) error {
	config := mcpgrafana.GrafanaConfigFromContext(ctx)
	mode := config.PrometheusGuardrailMode
	if mode == "" || mode == mcpgrafana.PrometheusGuardrailOff {
		return nil
	}
	// The backend rejects any other query type, so there is nothing to guard.
	if queryType != "range" && queryType != "instant" {
		return nil
	}
	// Unknown non-empty modes enforce (fail-closed). The CLI validates the
	// mode at startup; this warns library embedders that set it directly.
	switch mode {
	case mcpgrafana.PrometheusGuardrailShadow, mcpgrafana.PrometheusGuardrailEnforce:
	default:
		warnUnknownPromGuardrailModeOnce.Do(func() {
			config.LoggerOrDefault().WarnContext(ctx, "unrecognized Prometheus guardrail mode; treating as enforce", "mode", mode)
		})
	}
	enforced := mode != mcpgrafana.PrometheusGuardrailShadow

	policy := promGuardPolicy{
		RequireSelectiveLabels: config.PrometheusGuardrailRequireSelectiveLabels,
		MaxRange:               config.PrometheusGuardrailMaxRange,
		MaxPoints:              config.PrometheusGuardrailMaxPoints,
	}
	d := assessPromQuery(expr, queryType, start, end, stepSeconds, policy)
	// Static violations take precedence and avoid an unnecessary metadata call,
	// including in shadow mode. Emit exactly one final decision per query.
	if d.Verdict == promGuardAllow && config.PrometheusGuardrailMaxMetricCardinality > 0 && len(cardinality) > 0 {
		d = cardinality[0]()
	}

	metrics := promGuardrailInstrumentsFor(config.MeterProviderOrDefault())
	logger := config.LoggerOrDefault()

	switch d.Verdict {
	case promGuardAllow:
		metrics.recordAdmitted(ctx)
		return nil

	case promGuardUnknown:
		// Dashboard variables such as $window are the usual cause, and a real
		// syntax error comes back from the datasource anyway, so this is not
		// worth a warning per query; the fail_open counter tracks the rate.
		logger.DebugContext(ctx, "prometheus guardrail assessment incomplete (fail-open)", "error", d.ParseErr, "expr", expr)
		reason := d.UnknownReason
		if reason == "" {
			reason = promGuardReasonUnparseable
		}
		metrics.recordFailOpen(ctx, reason)
		if reason == promGuardReasonCardinalityUnknown {
			metrics.recordCardinalityFailOpen(ctx, d.CardinalityFailOpenCause)
		}
		return nil

	default: // promGuardBlock
		reasons := make([]string, len(d.Violations))
		for i, v := range d.Violations {
			reasons[i] = v.Message
		}
		metrics.recordBlocked(ctx, enforced, d.Violations[0].Reason)
		// The expression may embed sensitive label values, so it is logged
		// at debug level only.
		logger.DebugContext(ctx, "prometheus guardrail query detail", "expr", expr)
		if !enforced {
			logger.WarnContext(ctx, "prometheus guardrail would block query", "reasons", strings.Join(reasons, "; "))
			return nil
		}
		logger.WarnContext(ctx, "prometheus guardrail blocked query", "reasons", strings.Join(reasons, "; "))
		return errors.New(promGuardGuidance(d))
	}
}
