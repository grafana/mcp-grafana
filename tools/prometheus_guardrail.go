// This file holds the opt-in Prometheus query guardrail: a pure assessment of
// a PromQL query against a range and resolution budget, and the policy shell
// that logs, records metrics, and turns a block into guidance for the agent.
package tools

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"

	mcpgrafana "github.com/grafana/mcp-grafana/v2"
	"github.com/prometheus/prometheus/promql/parser"
)

// promGuardParser parses expressions for the guardrail only. Experimental
// functions are accepted so they are assessed rather than failing open.
// Duration expressions stay disabled: their numeric Range is 0 until
// evaluated, so parsing them would silently read as no window at all.
var promGuardParser = parser.NewParser(parser.Options{EnableExperimentalFunctions: true})

// warnUnknownPromGuardrailModeOnce gates the once-per-process warning for
// unrecognized modes. A variable so tests can reset it.
var warnUnknownPromGuardrailModeOnce sync.Once

type promGuardPolicy struct {
	MaxRange  time.Duration // 0 disables the range check
	MaxPoints int64         // 0 disables the points check
}

// promGuardReason values become the `reason` metric attribute, so the set is
// closed.
type promGuardReason string

const (
	promGuardReasonRange       promGuardReason = "range"
	promGuardReasonPoints      promGuardReason = "points"
	promGuardReasonUnparseable promGuardReason = "unparseable"
)

type promGuardVerdict int

const (
	promGuardAllow promGuardVerdict = iota
	promGuardBlock
	// promGuardUnknown means no check was violated but the expression could
	// not be parsed, so its range windows were not counted.
	promGuardUnknown
)

type promGuardViolation struct {
	Reason  promGuardReason
	Message string
	Hint    string
}

type promGuardDecision struct {
	Verdict    promGuardVerdict
	Violations []promGuardViolation // Block only; range before points
	ParseErr   error                // Unknown only
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
	if p.MaxRange > 0 {
		var extension time.Duration
		extension, parseErr = promExprWindow(expr)
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

// promExprWindow returns how far back the expression reads beyond each
// evaluation step: the longest window over its selectors, where a selector's
// window is its own range plus the ranges of every subquery above it. offset
// and @ are ignored: offset shifts the window rather than widening it, and @
// pins it, so ignoring @ can only overestimate.
func promExprWindow(expr string) (time.Duration, error) {
	ast, err := promGuardParser.ParseExpr(expr)
	if err != nil {
		return 0, err
	}
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
	return longest, nil
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
	// The smallest integer step s with floor(windowMs/(s*1000)) + 1 <= maxPoints.
	// maxPoints*1000 cannot overflow here: points > maxPoints bounds maxPoints
	// by windowMs/1000.
	minStep := windowMs/(maxPoints*1000) + 1
	return promGuardViolation{
		Reason:  promGuardReasonPoints,
		Message: fmt.Sprintf("the query requests %d points per series (%s at a %ds step), more than the maximum allowed (%d)", points, window, step, maxPoints),
		Hint:    fmt.Sprintf("raise stepSeconds to at least %ds (query_prometheus), or shorten the time range", minStep),
	}, true
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
func guardPrometheusQuery(ctx context.Context, expr, queryType string, start, end time.Time, stepSeconds int) error {
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
		MaxRange:  config.PrometheusGuardrailMaxRange,
		MaxPoints: config.PrometheusGuardrailMaxPoints,
	}
	d := assessPromQuery(expr, queryType, start, end, stepSeconds, policy)

	metrics := promGuardrailInstrumentsFor(config.MeterProviderOrDefault())
	logger := config.LoggerOrDefault()

	switch d.Verdict {
	case promGuardAllow:
		metrics.recordAdmitted(ctx)
		return nil

	case promGuardUnknown:
		// Grafana macros such as $__rate_interval are the usual cause, and a
		// real syntax error comes back from the datasource anyway, so this is
		// not worth a warning per query; the fail_open counter tracks the rate.
		logger.DebugContext(ctx, "prometheus guardrail could not parse the expression; checked the query's own time range only (fail-open)", "error", d.ParseErr, "expr", expr)
		metrics.recordFailOpen(ctx, promGuardReasonUnparseable)
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
