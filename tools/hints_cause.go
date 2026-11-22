package tools

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/prometheus/common/model"
	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/promql/parser"
)

// emptyCauseTimeout bounds the lookups that explain an empty result.
const emptyCauseTimeout = 5 * time.Second

// Cap the metric names probed and the labels listed, so the lookup and hint stay small.
const (
	maxCauseMetrics = 10
	maxCauseLabels  = 20
)

// promLookbackDelta is Prometheus's default --query.lookback-delta.
const promLookbackDelta = 5 * time.Minute

// nativeHistogramSuffixes maps each classic-histogram suffix to the function
// that reads the same value from a native histogram.
var nativeHistogramSuffixes = map[string]string{
	"_count":  "histogram_count",
	"_sum":    "histogram_sum",
	"_bucket": "histogram_quantile",
}

// promSelector is one metric selector in a query.
type promSelector struct {
	name     string
	matchers []*labels.Matcher
}

// promEmptyCause explains an empty Prometheus result with at most two
// index-only lookups: metric names, then the label names of one metric.
// It returns nil when it cannot tell, and the caller falls back to generic hints.
func promEmptyCause(ctx context.Context, b promBackend, expr string, start, end time.Time, stepSeconds int) *EmptyCause {
	if _, ok := b.(*prometheusBackend); !ok || end.IsZero() {
		return nil
	}
	window := time.Duration(0)
	if !start.IsZero() {
		window = end.Sub(start)
	}
	ast, err := promGuardParser.ParseExpr(promGuardSubstituteMacros(expr, window, stepSeconds))
	if err != nil {
		return nil
	}
	selectors, lookback := promSelectors(ast)
	if len(selectors) == 0 {
		return nil
	}
	if start.IsZero() {
		start = end.Add(-lookback)
	}

	ctx, cancel := context.WithTimeout(ctx, emptyCauseTimeout)
	defer cancel()

	var probe []string
	for _, s := range selectors {
		probe = append(probe, s.name)
		if base, _, ok := nativeHistogramBase(s.name); ok {
			probe = append(probe, base)
		}
		probe = append(probe, totalVariant(s.name))
	}
	nameMatcher := labels.MustNewMatcher(labels.MatchRegexp, model.MetricNameLabel, regexpAlternation(probe))
	names, err := b.LabelValues(ctx, model.MetricNameLabel, []string{"{" + nameMatcher.String() + "}"}, start, end)
	if err != nil {
		return nil
	}
	found := make(map[string]bool, len(names))
	for _, n := range names {
		found[n] = true
	}

	for _, s := range selectors {
		if found[s.name] {
			continue
		}
		if base, fn, ok := nativeHistogramBase(s.name); ok && found[base] {
			return &EmptyCause{
				Reason:  ReasonNativeHistogram,
				Summary: fmt.Sprintf("%s is a native histogram, so %s has no series.", base, s.name),
				Action:  fmt.Sprintf("Use %s() on %s instead.", fn, base),
			}
		}
		if alt := totalVariant(s.name); found[alt] {
			return &EmptyCause{
				Reason:  ReasonMetricNotFound,
				Summary: fmt.Sprintf("Metric %s has no series in this range, but %s does.", s.name, alt),
				Action:  fmt.Sprintf("Query %s instead.", alt),
			}
		}
		return &EmptyCause{
			Reason:  ReasonMetricNotFound,
			Summary: fmt.Sprintf("Metric %s has no series in this range.", s.name),
			Action:  "Find the metric name with list_prometheus_metric_names.",
		}
	}

	if cause := promLabelCause(ctx, b, selectors, start, end); cause != nil {
		return cause
	}
	return &EmptyCause{
		Reason:  ReasonNoMatchingSeries,
		Summary: "Every metric in the query has series in this range; label matchers, comparisons or vector matching removed them all.",
		Action:  "Check label values with list_prometheus_label_values, or run each part of the query alone.",
	}
}

// promLabelCause reports a label the first selector requires but its metric
// does not have. Matchers that accept "" are skipped: a missing label passes them.
func promLabelCause(ctx context.Context, b promBackend, selectors []promSelector, start, end time.Time) *EmptyCause {
	for _, s := range selectors {
		var required []string
		for _, m := range s.matchers {
			if m.Name != model.MetricNameLabel && !m.Matches("") {
				required = append(required, m.Name)
			}
		}
		if len(required) == 0 {
			continue
		}
		nameMatcher := labels.MustNewMatcher(labels.MatchEqual, model.MetricNameLabel, s.name)
		have, err := b.LabelNames(ctx, []string{"{" + nameMatcher.String() + "}"}, start, end)
		if err != nil {
			return nil
		}
		for _, l := range required {
			if slices.Contains(have, l) {
				continue
			}
			have = slices.DeleteFunc(have, func(n string) bool { return n == model.MetricNameLabel })
			return &EmptyCause{
				Reason:  ReasonLabelNotFound,
				Summary: fmt.Sprintf("Metric %s has no label %s.", s.name, l),
				Action:  "Its labels are: " + strings.Join(have[:min(len(have), maxCauseLabels)], ", ") + ".",
			}
		}
		return nil // one lookup only, for the first selector with required labels
	}
	return nil
}

// promSelectors returns the named selectors in expr, deduplicated and capped,
// and how far back an instant query at the end time reads.
func promSelectors(ast parser.Expr) ([]promSelector, time.Duration) {
	var out []promSelector
	seen := map[string]bool{}
	lookback := promLookbackDelta
	parser.Inspect(ast, func(node parser.Node, _ []parser.Node) error {
		switch n := node.(type) {
		case *parser.MatrixSelector:
			lookback = max(lookback, n.Range)
		case *parser.SubqueryExpr:
			lookback = max(lookback, n.Range)
		case *parser.VectorSelector:
			name := n.Name
			for _, m := range n.LabelMatchers {
				if m.Name == model.MetricNameLabel && m.Type == labels.MatchEqual {
					name = m.Value
				}
			}
			key := n.String()
			if name == "" || seen[key] || len(out) == maxCauseMetrics {
				return nil
			}
			seen[key] = true
			out = append(out, promSelector{name: name, matchers: n.LabelMatchers})
		}
		return nil
	})
	return out, lookback
}

// nativeHistogramBase returns the base name and native-histogram function for
// a classic histogram series name like foo_count.
func nativeHistogramBase(name string) (base, fn string, ok bool) {
	for suffix, fn := range nativeHistogramSuffixes {
		if base, ok := strings.CutSuffix(name, suffix); ok && base != "" {
			return base, fn, true
		}
	}
	return "", "", false
}

// totalVariant adds or removes the _total counter suffix.
func totalVariant(name string) string {
	if base, ok := strings.CutSuffix(name, "_total"); ok {
		return base
	}
	return name + "_total"
}

// regexpAlternation matches any of names exactly; PromQL anchors regex matchers.
func regexpAlternation(names []string) string {
	quoted := make([]string, 0, len(names))
	for _, n := range slices.Compact(slices.Sorted(slices.Values(names))) {
		quoted = append(quoted, regexp.QuoteMeta(n))
	}
	return strings.Join(quoted, "|")
}

// lokiEmptyCause explains an empty native-Loki result. Lines scanned > 0 needs
// no lookup; otherwise one index/stats call on the query's single stream
// selector, plus one over the last hour when the range ended before that.
func lokiEmptyCause(ctx context.Context, b lokiBackend, logql string, start, end, now time.Time, linesScanned *int) *EmptyCause {
	if _, ok := b.(*lokiNativeBackend); !ok || start.IsZero() || end.IsZero() {
		return nil
	}
	spans, scan, err := findStreamSelectors(logql)
	if err != nil {
		return nil
	}
	var selector string
	rest := []byte(scan)
	for _, sp := range spans {
		text := scan[sp.start:sp.end]
		if _, err := promqlParser.ParseMetricSelector(text); err != nil {
			continue // e.g. a {{.label}} template, not a selector
		}
		if selector != "" && selector != text {
			return nil // several selectors: no single one to blame
		}
		selector = text
		for i := sp.start; i < sp.end; i++ {
			rest[i] = ' '
		}
	}
	if selector == "" {
		return nil
	}
	hasPipeline := strings.Contains(string(rest), "|")
	linesFiltered := &EmptyCause{
		Reason:  ReasonLinesFiltered,
		Summary: "The stream selector has logs in this range, but the pipeline removed every line.",
		Action:  "Remove line filters and parsers one at a time to find the one that drops them.",
	}
	if linesScanned != nil && *linesScanned > 0 {
		if !hasPipeline {
			return nil
		}
		return linesFiltered
	}

	ctx, cancel := context.WithTimeout(ctx, emptyCauseTimeout)
	defer cancel()
	stats, err := b.QueryStats(ctx, selector, start, end)
	if err != nil {
		return nil
	}
	if stats.Streams > 0 {
		if !hasPipeline {
			return nil
		}
		return linesFiltered
	}

	recent := now.Add(-time.Hour)
	if end.Before(recent) {
		if s, err := b.QueryStats(ctx, selector, recent, now); err == nil && s.Streams > 0 {
			return &EmptyCause{
				Reason:  ReasonNoDataInRange,
				Summary: "The stream selector has logs in the last hour but none in this range, which may be past the retention period.",
				Action:  "Query a more recent range.",
			}
		}
	}
	return &EmptyCause{
		Reason:  ReasonNoMatchingStreams,
		Summary: "The stream selector matches no logs in this range.",
		Action:  "Check labels with list_loki_label_names and list_loki_label_values.",
	}
}
