// This file implements the opt-in Loki query cost guardrail, including a
// small hand-rolled LogQL scanner (selector extraction, matcher parsing,
// range-vector durations). The scanner is deliberate: importing
// grafana/loki/v3/pkg/logql/syntax would pull in a very large dependency
// tree for a few hundred lines of scanning, and any query shape the scanner
// does not understand fails open by design.
package tools

import (
	"context"
	"fmt"
	"math"
	"regexp/syntax"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	mcpgrafana "github.com/grafana/mcp-grafana"
)

// warnUnknownGuardrailModeOnce gates the once-per-process warning for
// unrecognized guardrail modes. A variable (not a literal) so tests can
// reset it.
var warnUnknownGuardrailModeOnce sync.Once

// lokiStatsFunc matches lokiBackend.QueryStats. The guardrail takes it as a
// parameter so the byte-budget check can be exercised in unit tests without
// a real backend.
type lokiStatsFunc func(ctx context.Context, query string, start, end time.Time) (*Stats, error)

// guardLokiQuery applies the opt-in Loki query cost guardrail to a
// query_loki_logs call before it reaches the datasource. Loki's own
// max_query_bytes_read limit is not enforced on log queries without a line
// filter, so a broad selector over a wide range can scan terabytes while
// passing every server-side check. The guardrail closes that gap: it
// requires selective stream selectors, bounds the effective time range
// (including range-vector durations like [30d]), and asks Loki's index/stats
// API for a byte estimate before admitting the query.
//
// In shadow mode violations are logged but the query proceeds; in enforce
// mode a plain error is returned, which ConvertTool surfaces to the client
// as a tool result with isError=true so LLM callers can rewrite and retry.
func guardLokiQuery(ctx context.Context, backend lokiBackend, logql, queryType string, start, end time.Time) error {
	config := mcpgrafana.GrafanaConfigFromContext(ctx)
	mode := config.LokiGuardrailMode
	if mode == "" || mode == mcpgrafana.LokiGuardrailOff {
		return nil
	}
	// Unknown non-empty modes enforce (fail-closed); warn once per process
	// so library embedders that set the mode programmatically get a signal.
	// The CLI validates the mode at startup and never reaches this path.
	switch mode {
	case mcpgrafana.LokiGuardrailShadow, mcpgrafana.LokiGuardrailEnforce:
	default:
		warnUnknownGuardrailModeOnce.Do(func() {
			config.LoggerOrDefault().WarnContext(ctx, "unrecognized Loki guardrail mode; treating as enforce", "mode", mode)
		})
	}

	// All scanning (selectors, durations, logging) operates on the query
	// with # comments stripped, so commented-out text cannot inject phantom
	// selectors or range vectors.
	logql = stripLogQLComments(logql)

	// The byte estimate is only consulted on native Loki, where index/stats
	// is an index-only lookup. The VictoriaLogs QueryStats approximation
	// runs the selector as a real query, which would defeat the purpose.
	var statsFn lokiStatsFunc
	_, native := backend.(*lokiNativeBackend)
	if native {
		statsFn = backend.QueryStats
	}

	reasons := lokiGuardrailReasons(ctx, config, native, logql, queryType, start, end, statsFn)
	if len(reasons) == 0 {
		return nil
	}

	// Log the extracted selectors (the basis of the decision) rather than
	// the full LogQL, which may embed sensitive literals in line filters;
	// the full query is available at debug level.
	logger := config.LoggerOrDefault()
	selectors := parseLogQLSelectors(logql)
	raws := make([]string, len(selectors))
	for i, sel := range selectors {
		raws[i] = sel.raw
	}
	logger.DebugContext(ctx, "loki guardrail query detail", "logql", logql)
	if mode == mcpgrafana.LokiGuardrailShadow {
		logger.WarnContext(ctx, "loki guardrail would block query", "reasons", strings.Join(reasons, "; "), "selectors", strings.Join(raws, " "))
		return nil
	}
	logger.WarnContext(ctx, "loki guardrail blocked query", "reasons", strings.Join(reasons, "; "), "selectors", strings.Join(raws, " "))
	rangeHint := "start narrow, e.g. the last few minutes, and widen only if needed"
	if config.LokiGuardrailMaxRange > 0 {
		rangeHint = fmt.Sprintf("keep it well under the %s cap", config.LokiGuardrailMaxRange)
	}
	return fmt.Errorf("query blocked by the Loki cost guardrail: %s. Rewrite and retry: narrow the stream selector "+
		"with specific label matchers (e.g. namespace and app/container), shorten the time range (%s), "+
		"and use query_loki_stats or list_loki_label_names/list_loki_label_values to scope the query first",
		strings.Join(reasons, "; "), rangeHint)
}

// lokiGuardrailReasons returns the list of block reasons for a query, empty
// when allowed. logql must already have # comments stripped (guardLokiQuery
// does this once for both the checks and the logging). Queries with no
// parseable stream selector pass through: invalid syntax is rejected by Loki
// with a proper error, and guessing here risks blocking legitimate syntax
// the parser doesn't know. The fail-open is logged at warn on native Loki so
// shadow-mode rollouts can spot query shapes the parser misses; on other
// backends (VictoriaLogs), where brace-less queries are the normal shape, it
// logs at debug to avoid per-query noise.
func lokiGuardrailReasons(ctx context.Context, config mcpgrafana.GrafanaConfig, native bool, logql, queryType string, start, end time.Time, statsFn lokiStatsFunc) []string {
	selectors := parseLogQLSelectors(logql)
	if len(selectors) == 0 {
		logger := config.LoggerOrDefault()
		if native {
			logger.WarnContext(ctx, "loki guardrail found no parseable stream selector; passing query through (fail-open)")
		} else {
			logger.DebugContext(ctx, "loki guardrail found no parseable stream selector; passing query through (fail-open)")
		}
		logger.DebugContext(ctx, "loki guardrail query detail", "logql", logql)
		return nil
	}

	var reasons []string
	for _, sel := range selectors {
		if !hasSelectivePositiveMatcher(sel.matchers) {
			reasons = append(reasons, fmt.Sprintf("the stream selector %s has no selective label matcher (catch-all, empty, or negative-only selectors scan every stream)", sel.raw))
		}
	}

	// Range vectors extend the scan beyond the query window, and instant
	// queries carry no window at all — their cost is set entirely by the
	// vector duration ([30d] in count_over_time({...}[30d])).
	vecDur := maxVectorDuration(logql)
	instant := queryType == "instant"

	if config.LokiGuardrailMaxRange > 0 {
		window := time.Duration(0)
		if !instant && !start.IsZero() && !end.IsZero() {
			window = end.Sub(start)
		}
		// end.Sub(start) saturates at MaxInt64 for extreme spans, so a plain
		// addition of vecDur would wrap negative and fail open past the cap.
		effective := window
		if vecDur > 0 && effective > math.MaxInt64-vecDur {
			effective = math.MaxInt64
		} else {
			effective += vecDur
		}
		if effective > config.LokiGuardrailMaxRange {
			reason := fmt.Sprintf("the time range (%s) exceeds the maximum allowed (%s)", effective.Round(time.Minute), config.LokiGuardrailMaxRange)
			if vecDur > 0 {
				reason = fmt.Sprintf("the time range (%s, including the %s range vector) exceeds the maximum allowed (%s)", effective.Round(time.Minute), vecDur, config.LokiGuardrailMaxRange)
			}
			reasons = append(reasons, reason)
		}
	}

	// Only spend the index-stats round trips on queries that pass the static
	// checks. Estimate failures are fail-open: the guardrail must never
	// block users because of its own (or Loki's) availability problems.
	// Line filters and parsers do not reduce bytes scanned, so the
	// selector-only estimates are accurate for exactly the queries that hurt.
	if len(reasons) == 0 && config.LokiGuardrailMaxBytes > 0 && statsFn != nil {
		if statsStart, statsEnd, ok := guardrailStatsWindow(instant, start, end, vecDur); ok {
			if total, ok := sumSelectorBytes(ctx, config, selectors, statsStart, statsEnd, statsFn); ok && total > config.LokiGuardrailMaxBytes {
				reasons = append(reasons, fmt.Sprintf("Loki estimates this query would scan %s, above the %s budget",
					humanizeBytes(total), humanizeBytes(config.LokiGuardrailMaxBytes)))
			}
		}
	}
	return reasons
}

// guardrailStatsWindow computes the window for the index/stats estimate.
// Range vectors widen the window backwards: a [7d] vector over a 1h window
// scans 7d+1h of chunks. Instant queries have no window of their own, so the
// vector duration is anchored at the query time (mirroring fetchQuery: end
// when given, otherwise start, otherwise now); with no vector duration there
// is nothing to estimate and the byte check is skipped. Offset modifiers are
// ignored: the range cap is unaffected (offset shifts the window, it does
// not widen it), and the stats window estimates the unshifted interval — a
// neutral-direction skew in the byte estimate.
func guardrailStatsWindow(instant bool, start, end time.Time, vecDur time.Duration) (time.Time, time.Time, bool) {
	if instant || start.IsZero() || end.IsZero() {
		if vecDur == 0 {
			return time.Time{}, time.Time{}, false
		}
		anchor := end
		if anchor.IsZero() {
			anchor = start
		}
		if anchor.IsZero() {
			anchor = time.Now()
		}
		return anchor.Add(-vecDur), anchor, true
	}
	return start.Add(-vecDur), end, true
}

// sumSelectorBytes sums the index/stats byte estimates across the query's
// selectors (both sides of a binary metric expression are scanned, so the
// sum is the true cost). Identical selectors are fetched once but their
// estimate is multiplied by the occurrence count — each leg of a binary
// expression scans independently. Any estimate failure fails open.
func sumSelectorBytes(ctx context.Context, config mcpgrafana.GrafanaConfig, selectors []parsedSelector, start, end time.Time, statsFn lokiStatsFunc) (int64, bool) {
	counts := make(map[string]int64, len(selectors))
	for _, sel := range selectors {
		counts[sel.raw]++
	}
	seen := make(map[string]bool, len(counts))
	var total int64
	for _, sel := range selectors {
		if seen[sel.raw] {
			continue
		}
		seen[sel.raw] = true
		stats, err := statsFn(ctx, sel.raw, start, end)
		if err != nil {
			// A partial total that is already over budget is a decision the
			// remaining selectors can only reinforce, since bytes are
			// additive. Keep it rather than failing open; fail open only
			// while the verdict is still undetermined.
			if config.LokiGuardrailMaxBytes > 0 && total > config.LokiGuardrailMaxBytes {
				config.LoggerOrDefault().WarnContext(ctx, "loki guardrail byte estimate failed after budget already exceeded (keeping partial total)", "error", err, "partial_bytes", total)
				return total, true
			}
			config.LoggerOrDefault().WarnContext(ctx, "loki guardrail byte estimate failed (fail-open)", "error", err)
			return 0, false
		}
		// Saturate rather than wrap: an overflowing total means "over any
		// conceivable budget", not a free pass.
		count := counts[sel.raw]
		if stats.Bytes > 0 && stats.Bytes > math.MaxInt64/count {
			return math.MaxInt64, true
		}
		total += stats.Bytes * count
		if total < 0 {
			return math.MaxInt64, true
		}
	}
	return total, true
}

// stripLogQLComments removes comments (outside quoted strings) so
// commented-out text cannot inject phantom selectors or range vectors, and
// so a comment inside a selector cannot break matcher parsing into a
// fail-open. Loki's lexer handles # itself and inherits Go-style // and
// /* */ comments from text/scanner's GoTokens mode, so all three forms are
// whitespace to Loki. Comment markers inside string literals are kept. An
// unterminated /* runs to the end of the input, matching text/scanner's
// treatment of the remainder as (malformed) comment rather than tokens.
func stripLogQLComments(logql string) string {
	if !strings.ContainsAny(logql, "#/") {
		return logql
	}
	var b strings.Builder
	b.Grow(len(logql))
	var inStr byte
	for i := 0; i < len(logql); i++ {
		c := logql[i]
		if inStr != 0 {
			if c == '\\' && inStr != '`' && i+1 < len(logql) {
				b.WriteByte(c)
				i++
				b.WriteByte(logql[i])
				continue
			}
			if c == inStr {
				inStr = 0
			}
			b.WriteByte(c)
			continue
		}
		switch {
		case c == '"' || c == '`' || c == '\'':
			inStr = c
		case c == '#' || (c == '/' && i+1 < len(logql) && logql[i+1] == '/'):
			for i < len(logql) && logql[i] != '\n' {
				i++
			}
			if i < len(logql) {
				b.WriteByte('\n')
			}
			continue
		case c == '/' && i+1 < len(logql) && logql[i+1] == '*':
			end := strings.Index(logql[i+2:], "*/")
			if end < 0 {
				i = len(logql)
			} else {
				i += 2 + end + 1 // skip past the closing */
			}
			b.WriteByte(' ') // comments are whitespace to the scanner
			continue
		}
		b.WriteByte(c)
	}
	return b.String()
}

// labelMatcher is one parsed matcher from a stream selector.
type labelMatcher struct {
	Name  string
	Op    string // =, !=, =~, !~
	Value string
}

// parsedSelector is one stream selector extracted from a LogQL query: the
// raw `{...}` text (reused verbatim in the index/stats call) plus its parsed
// matchers.
type parsedSelector struct {
	raw      string
	matchers []labelMatcher
}

// parseLogQLSelectors extracts every stream selector `{...}` from a LogQL
// query (log or metric, including both sides of binary expressions) and
// parses their label matchers. Brace groups that do not parse as selectors
// (e.g. label_format templates) are skipped rather than failing the whole
// query.
func parseLogQLSelectors(logql string) []parsedSelector {
	var out []parsedSelector
	for i := 0; i < len(logql); {
		open := indexOutsideStrings(logql, i, '{')
		if open < 0 {
			break
		}
		close := closingBrace(logql, open)
		if close < 0 {
			break
		}
		if matchers, ok := parseMatchers(logql[open+1 : close]); ok {
			out = append(out, parsedSelector{raw: logql[open : close+1], matchers: matchers})
		}
		i = close + 1
	}
	return out
}

// indexOutsideStrings returns the index of the first target byte at or after
// from, skipping bytes inside quoted strings.
func indexOutsideStrings(s string, from int, target byte) int {
	var inStr byte
	for i := from; i < len(s); i++ {
		c := s[i]
		if inStr != 0 {
			if c == '\\' && inStr != '`' {
				i++
			} else if c == inStr {
				inStr = 0
			}
			continue
		}
		switch c {
		case '"', '`', '\'':
			inStr = c
		case target:
			return i
		}
	}
	return -1
}

// closingBrace finds the index of the '}' closing the brace at open,
// skipping braces inside quoted matcher values.
func closingBrace(s string, open int) int {
	return indexOutsideStrings(s, open+1, '}')
}

// maxVectorDuration returns the largest range-vector duration ([5m], [30d])
// found outside quoted strings, or zero when there is none. The max (rather
// than the sum) is the extra lookback: all range vectors in one query share
// the same evaluation timestamps.
func maxVectorDuration(logql string) time.Duration {
	var out time.Duration
	for i := 0; i < len(logql); {
		open := indexOutsideStrings(logql, i, '[')
		if open < 0 {
			break
		}
		closeIdx := indexOutsideStrings(logql, open+1, ']')
		if closeIdx < 0 {
			break
		}
		if d, ok := parseLogQLDuration(logql[open+1 : closeIdx]); ok && d > out {
			out = d
		}
		i = closeIdx + 1
	}
	return out
}

// parseLogQLDuration parses a range-vector duration: Prometheus style (30d,
// 1h30m, 90s) or, matching Loki's own fallback, a Go duration with
// fractional units (1.5h). Anything else — including values that overflow
// time.Duration (e.g. 300y) — returns false so unrelated bracket content is
// ignored.
func parseLogQLDuration(s string) (time.Duration, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	if d, ok := parsePromStyleDuration(s); ok {
		return d, true
	}
	// Loki accepts fractional durations ([1.5h]) via time.ParseDuration;
	// without this fallback a fractional range vector would fail open and
	// bypass the range cap on instant queries.
	if d, err := time.ParseDuration(s); err == nil && d > 0 {
		return d, true
	}
	return 0, false
}

// parsePromStyleDuration parses a Prometheus-style duration (30d, 1h30m).
func parsePromStyleDuration(s string) (time.Duration, bool) {
	var total time.Duration
	for i := 0; i < len(s); {
		start := i
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i++
		}
		if i == start {
			return 0, false
		}
		num, err := strconv.ParseInt(s[start:i], 10, 64)
		if err != nil {
			return 0, false
		}
		var unit time.Duration
		switch {
		case strings.HasPrefix(s[i:], "ms"):
			unit, i = time.Millisecond, i+2
		case i < len(s) && s[i] == 's':
			unit, i = time.Second, i+1
		case i < len(s) && s[i] == 'm':
			unit, i = time.Minute, i+1
		case i < len(s) && s[i] == 'h':
			unit, i = time.Hour, i+1
		case i < len(s) && s[i] == 'd':
			unit, i = 24*time.Hour, i+1
		case i < len(s) && s[i] == 'w':
			unit, i = 7*24*time.Hour, i+1
		case i < len(s) && s[i] == 'y':
			unit, i = 365*24*time.Hour, i+1
		default:
			return 0, false
		}
		term := time.Duration(num) * unit
		if term < 0 || (num != 0 && term/unit != time.Duration(num)) {
			return 0, false
		}
		total += term
		if total < 0 {
			return 0, false
		}
	}
	return total, true
}

// parseMatchers parses comma-separated label matchers from a selector body.
func parseMatchers(s string) ([]labelMatcher, bool) {
	var out []labelMatcher
	i, n := 0, len(s)
	skipSpace := func() {
		for i < n && (s[i] == ' ' || s[i] == '\t' || s[i] == '\n' || s[i] == '\r') {
			i++
		}
	}
	for {
		skipSpace()
		if i >= n {
			return out, true
		}
		var name string
		if s[i] == '"' || s[i] == '`' {
			// Loki allows quoted label names (e.g. structured metadata keys).
			var ok bool
			name, i, ok = parseQuoted(s, i)
			if !ok || name == "" {
				return nil, false
			}
		} else {
			nameStart := i
			for i < n && isIdentChar(s[i]) {
				i++
			}
			if i == nameStart {
				return nil, false
			}
			name = s[nameStart:i]
		}
		skipSpace()
		var op string
		switch {
		case strings.HasPrefix(s[i:], "=~"):
			op, i = "=~", i+2
		case strings.HasPrefix(s[i:], "!="):
			op, i = "!=", i+2
		case strings.HasPrefix(s[i:], "!~"):
			op, i = "!~", i+2
		case i < n && s[i] == '=':
			op, i = "=", i+1
		default:
			return nil, false
		}
		skipSpace()
		val, next, ok := parseQuoted(s, i)
		if !ok {
			return nil, false
		}
		i = next
		out = append(out, labelMatcher{Name: name, Op: op, Value: val})
		skipSpace()
		if i < n {
			if s[i] != ',' {
				return nil, false
			}
			i++
		}
	}
}

func isIdentChar(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}

// parseQuoted parses a quoted string starting at i, returning the unquoted
// value and the index after the closing quote. Double-quoted values use Go
// escaping (matching LogQL string literals) so regex analysis sees the real
// pattern (e.g. "[\\s\\S]*" becomes [\s\S]*); backticks are raw. On unquote
// failure the literal content is used verbatim — enough fidelity for matcher
// selectivity analysis.
func parseQuoted(s string, i int) (string, int, bool) {
	if i >= len(s) {
		return "", 0, false
	}
	q := s[i]
	if q != '"' && q != '`' && q != '\'' {
		return "", 0, false
	}
	for j := i + 1; j < len(s); j++ {
		c := s[j]
		if c == '\\' && q != '`' {
			j++
			continue
		}
		if c == q {
			if q == '"' {
				if v, err := strconv.Unquote(s[i : j+1]); err == nil {
					return v, j + 1, true
				}
			}
			return s[i+1 : j], j + 1, true
		}
	}
	return "", 0, false
}

// hasSelectivePositiveMatcher reports whether at least one matcher positively
// narrows the stream set: an equality with a non-empty value, or a regex
// match that is not a catch-all.
func hasSelectivePositiveMatcher(ms []labelMatcher) bool {
	for _, m := range ms {
		switch m.Op {
		case "=":
			if m.Value != "" {
				return true
			}
		case "=~":
			if !regexIsCatchAll(m.Value) {
				return true
			}
		}
	}
	return false
}

// regexIsCatchAll reports whether a matcher regex fails to narrow the stream
// set: it matches every value (.*, .+, (?s).*, .*.*, [\s\S]*, ...) or only
// the empty value (which selects every stream missing the label). Matcher
// regexes are fully anchored by Loki, so "matches every value" means the
// pattern can consume any whole string. Unparseable regexes count as
// selective — Loki rejects them with a proper error anyway.
func regexIsCatchAll(pattern string) bool {
	re, err := syntax.Parse(pattern, syntax.Perl)
	if err != nil {
		return false
	}
	re = re.Simplify()
	return matchesAnyString(re) || matchesOnlyEmpty(re)
}

// matchesAnyString reports whether re matches every string (or every
// non-empty string: .+-style patterns are equally unselective for label
// values). Sufficient conditions only; a false negative merely lets a query
// through to the byte-budget check.
func matchesAnyString(re *syntax.Regexp) bool {
	switch re.Op {
	case syntax.OpStar, syntax.OpPlus:
		return matchesAnyChar(re.Sub[0]) || matchesAnyString(re.Sub[0])
	case syntax.OpQuest:
		// x? matches every string iff x does (the empty branch only adds
		// the empty string), e.g. (.+)?.
		return matchesAnyString(re.Sub[0])
	case syntax.OpCapture:
		return matchesAnyString(re.Sub[0])
	case syntax.OpConcat:
		// Any-string as a whole: one element matches any string and every
		// other element can match empty (e.g. ^.*$, .*(foo)?).
		found := false
		for _, sub := range re.Sub {
			if matchesAnyString(sub) {
				found = true
			} else if !nullable(sub) {
				return false
			}
		}
		return found
	case syntax.OpAlternate:
		for _, sub := range re.Sub {
			if matchesAnyString(sub) {
				return true
			}
		}
	}
	return false
}

// matchesOnlyEmpty reports whether re can only ever match the empty string
// (empty pattern, ^$, anchors alone). Word boundaries are deliberately not
// included: \b never matches within the empty string, so ^\b$ matches
// nothing at all — treating it as empty-only would misclassify patterns
// like .*\b as catch-alls.
func matchesOnlyEmpty(re *syntax.Regexp) bool {
	switch re.Op {
	case syntax.OpEmptyMatch, syntax.OpBeginLine, syntax.OpEndLine,
		syntax.OpBeginText, syntax.OpEndText:
		return true
	case syntax.OpCapture:
		return matchesOnlyEmpty(re.Sub[0])
	case syntax.OpStar, syntax.OpPlus, syntax.OpQuest, syntax.OpRepeat:
		// Repeating something that only matches empty still only matches
		// empty, e.g. (^$)*, (^)+, (^$){2,3}. Simplify does not fold these.
		return matchesOnlyEmpty(re.Sub[0])
	case syntax.OpConcat:
		for _, sub := range re.Sub {
			if !matchesOnlyEmpty(sub) {
				return false
			}
		}
		return true
	case syntax.OpAlternate:
		// ^|^ or ()|(): every branch matches only the empty string, so the
		// matcher selects every stream missing the label.
		for _, sub := range re.Sub {
			if !matchesOnlyEmpty(sub) {
				return false
			}
		}
		return true
	}
	return false
}

// nullable reports whether re can match the empty string. Word boundaries
// are conservatively treated as non-nullable (\b does not match within the
// empty string), erring toward classifying patterns as selective.
func nullable(re *syntax.Regexp) bool {
	switch re.Op {
	case syntax.OpEmptyMatch, syntax.OpStar, syntax.OpQuest,
		syntax.OpBeginLine, syntax.OpEndLine, syntax.OpBeginText, syntax.OpEndText:
		return true
	case syntax.OpCapture, syntax.OpPlus:
		return nullable(re.Sub[0])
	case syntax.OpConcat:
		for _, sub := range re.Sub {
			if !nullable(sub) {
				return false
			}
		}
		return true
	case syntax.OpAlternate:
		for _, sub := range re.Sub {
			if nullable(sub) {
				return true
			}
		}
	case syntax.OpRepeat:
		return re.Min == 0 || nullable(re.Sub[0])
	}
	return false
}

// matchesAnyChar reports whether re matches any single character: the dot
// metachar, a character class covering every rune, or an alternation whose
// branches jointly cover every rune.
func matchesAnyChar(re *syntax.Regexp) bool {
	switch re.Op {
	case syntax.OpAnyChar, syntax.OpAnyCharNotNL:
		return true
	case syntax.OpCharClass:
		return charClassCoversAll(re.Rune)
	case syntax.OpCapture:
		return matchesAnyChar(re.Sub[0])
	case syntax.OpAlternate:
		return alternationCoversAllChars(re.Sub)
	}
	return false
}

// alternationCoversAllChars reports whether an alternation matches any single
// character: one branch does on its own (e.g. (foo|.)), or the union of its
// single-character branches covers every rune modulo newline (e.g.
// (a|foo|[^a])). The parser merges adjacent single-char branches itself
// (\s|\S parses as .), so this only fires when other branches keep them
// apart. Branches matching longer strings only add coverage, so skipping
// them in the union errs toward classifying the pattern as selective.
func alternationCoversAllChars(subs []*syntax.Regexp) bool {
	var pairs []rune
	for _, sub := range subs {
		for sub.Op == syntax.OpCapture {
			sub = sub.Sub[0]
		}
		switch sub.Op {
		case syntax.OpAnyChar:
			return true
		case syntax.OpAnyCharNotNL:
			pairs = append(pairs, 0, '\n'-1, '\n'+1, unicode.MaxRune)
		case syntax.OpCharClass:
			pairs = append(pairs, sub.Rune...)
		case syntax.OpLiteral:
			if len(sub.Rune) == 1 {
				pairs = append(pairs, sub.Rune[0], sub.Rune[0])
			}
		}
	}
	// charClassCoversAll expects ranges sorted by low bound; branches can
	// contribute them in any order.
	ranges := make([][2]rune, 0, len(pairs)/2)
	for i := 0; i+1 < len(pairs); i += 2 {
		ranges = append(ranges, [2]rune{pairs[i], pairs[i+1]})
	}
	sort.Slice(ranges, func(i, j int) bool { return ranges[i][0] < ranges[j][0] })
	sorted := pairs[:0]
	for _, r := range ranges {
		sorted = append(sorted, r[0], r[1])
	}
	return charClassCoversAll(sorted)
}

// charClassCoversAll reports whether the class matches every rune, modulo
// newline (so [^\n] counts, matching dot's default semantics).
func charClassCoversAll(pairs []rune) bool {
	var next rune
	for i := 0; i+1 < len(pairs); i += 2 {
		lo, hi := pairs[i], pairs[i+1]
		if lo > next && (next != '\n' || lo != '\n'+1) {
			return false
		}
		if hi >= next {
			next = hi + 1
		}
	}
	return next > unicode.MaxRune
}

// humanizeBytes renders a byte count in binary units for guardrail messages.
func humanizeBytes(n int64) string {
	switch {
	case n >= 1<<40:
		return fmt.Sprintf("%.1f TiB", float64(n)/float64(1<<40))
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GiB", float64(n)/float64(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MiB", float64(n)/float64(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KiB", float64(n)/float64(1<<10))
	default:
		return fmt.Sprintf("%d B", n)
	}
}
