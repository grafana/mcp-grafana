package lokiguard

import (
	"strconv"
	"strings"
	"time"
)

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
