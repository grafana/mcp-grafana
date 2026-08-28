package tools

import (
	"context"
	"fmt"
	"strings"

	mcpgrafana "github.com/grafana/mcp-grafana"
	"github.com/prometheus/prometheus/model/labels"
)

// Enforced Loki label matchers.
//
// Enforcement rewrites every native-Loki query by AND-ing an operator-configured
// set of label matchers into each of its stream selectors, so the server can
// only ever read the streams the operator allows.
//
// The rewrite is done by scanning for selector spans rather than by parsing
// LogQL, so that this package does not have to depend on Loki. Loki ships as a
// single module, so importing its LogQL parser would pin this repo to Loki's
// entire version graph (Prometheus, OTel, Alertmanager, the AWS SDK, ...). Two
// properties of the grammar make the scan reliable:
//
//   - Outside of string literals and comments, `{` only ever opens a stream
//     selector. So a scan that tracks quoting and comment state finds every
//     selector, and never mistakes a brace inside a line filter — the classic
//     `{app="x"} |= "namespace=\"foo\""` bypass — for one. The string-aware
//     scanning primitives already exist here for the query cost guardrail.
//   - A LogQL stream selector is Prometheus label-matcher syntax, so each span
//     can be validated with the Prometheus parser this repo already depends on.
//
// Both the scan and the validation fail CLOSED: anything not understood is
// rejected rather than forwarded to Loki unfiltered.
//
// Injection splices the matchers in as text, leaving the rest of the user's
// query — including its formatting — byte-for-byte intact.

// ParseEnforcedMatchers parses an operator-supplied label-matcher expression
// (e.g. `namespace!~"vault|payments"`, with or without surrounding braces) into
// a slice of matchers suitable for GrafanaConfig.LokiEnforcedMatchers. It is
// called once at startup so invalid configuration fails fast rather than
// silently disabling enforcement. An empty/whitespace input returns (nil, nil)
// meaning "enforcement disabled".
func ParseEnforcedMatchers(s string) ([]*labels.Matcher, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	// Accept both `{a="b"}` and `a="b"` forms.
	if !strings.HasPrefix(s, "{") {
		s = "{" + s + "}"
	}
	// ParseMetricSelector checks syntax only. Unlike ParseExpr it does not run
	// the "vector selector must contain at least one non-empty matcher" AST
	// check, which is what makes it usable here: a purely-negative set such as
	// `namespace!~"vault"` is a valid enforcement policy even though it is not a
	// valid standalone query, because it is only ever AND-ed into a user
	// selector that carries its own positive matcher.
	matchers, err := promqlParser.ParseMetricSelector(s)
	if err != nil {
		return nil, fmt.Errorf("parsing enforced matchers %q: %w", s, err)
	}
	if len(matchers) == 0 {
		return nil, fmt.Errorf("enforced matchers %q parsed to an empty set", s)
	}
	return matchers, nil
}

// enforcedMatchers returns the configured enforcement matchers for the current
// request, or nil when enforcement is disabled.
func enforcedMatchers(ctx context.Context) []*labels.Matcher {
	return mcpgrafana.GrafanaConfigFromContext(ctx).LokiEnforcedMatchers
}

// selectorSpan is the byte range of one stream selector within a query;
// query[start:end] covers the selector including both braces.
type selectorSpan struct {
	start, end int
}

// blankLogQLComments returns a copy of query with every comment overwritten by
// spaces. Unlike stripLogQLComments it preserves length, so an offset into the
// result also indexes the original — which is what lets the scan run over the
// comment-free text while the matchers are spliced into the query the user
// actually wrote.
func blankLogQLComments(query string) string {
	if !strings.ContainsAny(query, "#/") {
		return query
	}

	b := []byte(query)
	var inStr byte
	for i := 0; i < len(b); {
		c := b[i]
		if inStr != 0 {
			switch {
			case c == '\\' && inStr != '`':
				i += 2 // escaped byte cannot close the string
			case c == inStr:
				inStr = 0
				i++
			default:
				i++
			}
			continue
		}

		switch {
		case c == '"' || c == '`' || c == '\'':
			inStr = c
			i++
		case c == '#', c == '/' && i+1 < len(b) && b[i+1] == '/':
			for i < len(b) && b[i] != '\n' {
				b[i] = ' '
				i++
			}
		case c == '/' && i+1 < len(b) && b[i+1] == '*':
			stop := len(b)
			if end := strings.Index(query[i+2:], "*/"); end >= 0 {
				stop = i + 2 + end + 2
			}
			for ; i < stop; i++ {
				b[i] = ' '
			}
		default:
			i++
		}
	}
	return string(b)
}

// findStreamSelectors returns the span of every stream selector in a LogQL
// query. Braces inside string literals and comments are never mistaken for a
// selector.
//
// The string-aware scanning primitives are shared with the query cost
// guardrail (see loki_guardrail.go), but the policy on top of them is the
// opposite: the guardrail skips what it cannot parse and fails open, because a
// missed cost estimate is not a correctness problem. Enforcement is a security
// control, so anything unrecognised has to become an error here.
func findStreamSelectors(query string) ([]selectorSpan, error) {
	// Offsets are preserved, so spans found here index into query unchanged.
	scan := blankLogQLComments(query)

	var spans []selectorSpan
	for i := 0; i < len(scan); {
		open := indexOutsideStrings(scan, i, '{')
		if open < 0 {
			break
		}
		end := closingBrace(scan, open)
		if end < 0 {
			return nil, fmt.Errorf("unterminated stream selector: no '}' closes the '{' at offset %d", open)
		}
		spans = append(spans, selectorSpan{start: open, end: end + 1})
		i = end + 1
	}
	return spans, nil
}

// enforceLogQL AND-s the configured enforcement matchers into every stream
// selector of a LogQL query (log or metric) and returns the rewritten query.
// When enforcement is disabled it returns the query unchanged.
//
// This is a security control, so it fails CLOSED: any query that cannot be
// scanned, or whose selectors cannot be parsed, returns an error rather than
// being sent to Loki unfiltered. The matchers are appended, so a user selector
// can only ever narrow the result within the enforced bounds — e.g. an enforced
// `namespace!~"vault"` AND-ed with a user's `{namespace="vault"}` yields an
// empty result set.
func enforceLogQL(ctx context.Context, query string) (string, error) {
	enforced := enforcedMatchers(ctx)
	if len(enforced) == 0 {
		return query, nil
	}

	spans, err := findStreamSelectors(query)
	if err != nil {
		return "", fmt.Errorf("enforced matcher injection: could not scan LogQL query %q: %w", query, err)
	}
	// Every LogQL query reads from at least one stream selector. Finding none
	// means we do not understand the query, so refuse it: passing it through
	// would be an unfiltered query, which is exactly what enforcement exists to
	// prevent.
	if len(spans) == 0 {
		return "", fmt.Errorf("enforced matcher injection: no stream selector found in LogQL query %q", query)
	}

	injected := matchersString(enforced)

	var b strings.Builder
	prev := 0
	for _, s := range spans {
		selector := query[s.start:s.end]
		if _, err := promqlParser.ParseMetricSelector(selector); err != nil {
			return "", fmt.Errorf("enforced matcher injection: could not parse stream selector %q in LogQL query %q: %w", selector, query, err)
		}
		b.WriteString(query[prev:s.start])
		b.WriteString(appendMatchers(selector, injected))
		prev = s.end
	}
	b.WriteString(query[prev:])

	return b.String(), nil
}

// appendMatchers splices matchers in just before a selector's closing brace,
// leaving the user's own matchers and formatting untouched. selector includes
// both braces.
func appendMatchers(selector, matchers string) string {
	inner := strings.TrimRight(selector[1:len(selector)-1], " \t\r\n")
	if inner == "" {
		return "{" + matchers + "}"
	}

	// Judge the existing body with its comments blanked out. A comma inside a
	// comment is not a real trailing comma, and a body ending inside a line
	// comment would swallow anything appended on that line.
	blanked := blankLogQLComments(inner)
	real := strings.TrimRight(blanked, " \t\r\n")

	// Loki accepts a trailing comma; do not emit a doubled one.
	needComma := real != "" && !strings.HasSuffix(real, ",")
	// inner has no trailing whitespace, so a blank last byte came from a comment.
	endsInComment := blanked[len(blanked)-1] == ' '

	var sep string
	switch {
	case endsInComment && needComma:
		// Continue on a fresh line, or the matchers and the closing brace both
		// end up commented out and Loki rejects the query.
		sep = "\n, "
	case endsInComment:
		sep = "\n"
	case needComma:
		sep = ", "
	default:
		sep = " "
	}
	return "{" + inner + sep + matchers + "}"
}

// matchersString renders matchers as the inside of a stream selector.
func matchersString(ms []*labels.Matcher) string {
	parts := make([]string, 0, len(ms))
	for _, m := range ms {
		parts = append(parts, m.String())
	}
	return strings.Join(parts, ", ")
}

// hasNonEmptyMatcher reports whether at least one matcher rejects the empty
// string. Loki, like Prometheus, refuses a selector whose every matcher can
// match empty, since that would select all streams.
func hasNonEmptyMatcher(ms []*labels.Matcher) bool {
	for _, m := range ms {
		if m != nil && !m.Matches("") {
			return true
		}
	}
	return false
}

// Label-enumeration fallback policies for when the enforced matchers cannot be
// applied to Loki's label-name / label-value endpoints (see
// GrafanaConfig.LokiLabelEnumerationFallback).
const (
	LabelEnumFallbackReject     = "reject"
	LabelEnumFallbackUnfiltered = "unfiltered"
)

// labelEnumerationSelector returns the `query` parameter to scope Loki's
// label-name / label-value endpoints under the enforcement policy.
//
//   - enforcement disabled            -> ("", nil): enumerate normally.
//   - enforced set is a valid selector -> (selector, nil): scoped enumeration.
//     (Any set containing at least one positive matcher — e.g. an allowlist
//     `environment=~"prod|staging"` — is valid.)
//   - enforced set is purely negative  -> cannot be a standalone Loki selector,
//     so fall back per LokiLabelEnumerationFallback:
//     "unfiltered" -> ("", nil): enumerate unscoped (metadata only, never log
//     lines); "reject" (default) -> ("", error): fail closed.
func labelEnumerationSelector(ctx context.Context) (string, error) {
	enforced := enforcedMatchers(ctx)
	if len(enforced) == 0 {
		return "", nil
	}

	if hasNonEmptyMatcher(enforced) {
		return "{" + matchersString(enforced) + "}", nil
	}

	// Purely-negative enforced set: Loki cannot scope enumeration by it.
	if mcpgrafana.GrafanaConfigFromContext(ctx).LokiLabelEnumerationFallback == LabelEnumFallbackUnfiltered {
		return "", nil
	}
	return "", fmt.Errorf("label enumeration cannot be scoped by the configured (purely-negative) enforced matchers; set the label-enumeration fallback to %q to allow unscoped enumeration, or use a positive matcher", LabelEnumFallbackUnfiltered)
}
