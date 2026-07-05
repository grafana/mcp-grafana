package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/grafana/loki/v3/pkg/logql/syntax"
	mcpgrafana "github.com/grafana/mcp-grafana"
	"github.com/prometheus/prometheus/model/labels"
)

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
	// validate=false: a purely-negative matcher set (e.g. `namespace!~"vault"`)
	// is a valid enforcement policy even though Loki rejects it as a *standalone*
	// query. It only ever appears AND-ed into a user query that already carries a
	// non-empty-compatible matcher, which makes the combined selector valid.
	matchers, err := syntax.ParseMatchers(s, false)
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

// enforceLogQL AND-s the configured enforcement matchers into every stream
// selector of a LogQL query (log or metric) and returns the rewritten query.
// When enforcement is disabled it returns the query unchanged.
//
// This is a security control, so it fails CLOSED: any query that cannot be
// parsed returns an error rather than being sent to Loki unfiltered. The
// matchers are appended, so a user selector can only ever narrow the result
// within the enforced bounds — e.g. an enforced `namespace!~"vault"` AND-ed
// with a user's `{namespace="vault"}` yields an empty result set.
func enforceLogQL(ctx context.Context, query string) (string, error) {
	enforced := enforcedMatchers(ctx)
	if len(enforced) == 0 {
		return query, nil
	}

	expr, err := syntax.ParseExpr(query)
	if err != nil {
		return "", fmt.Errorf("enforced matcher injection: could not parse LogQL query %q: %w", query, err)
	}

	expr.Walk(func(e syntax.Expr) bool {
		if m, ok := e.(*syntax.MatchersExpr); ok {
			m.AppendMatchers(enforced)
		}
		return true
	})

	return expr.String(), nil
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

	selector := (&syntax.MatchersExpr{Mts: enforced}).String()
	if _, err := syntax.ParseExpr(selector); err == nil {
		return selector, nil
	}

	// Purely-negative enforced set: Loki cannot scope enumeration by it.
	if mcpgrafana.GrafanaConfigFromContext(ctx).LokiLabelEnumerationFallback == LabelEnumFallbackUnfiltered {
		return "", nil
	}
	return "", fmt.Errorf("label enumeration cannot be scoped by the configured (purely-negative) enforced matchers; set the label-enumeration fallback to %q to allow unscoped enumeration, or use a positive matcher", LabelEnumFallbackUnfiltered)
}
