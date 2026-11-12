package tools

import (
	"context"
	"fmt"
	"time"

	mcpgrafana "github.com/grafana/mcp-grafana/v2"
	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/promql/parser"
)

// prometheusEnforcedMatchers returns the configured enforcement matchers for
// Prometheus requests, or nil when enforcement is disabled.
func prometheusEnforcedMatchers(ctx context.Context) []*labels.Matcher {
	return mcpgrafana.GrafanaConfigFromContext(ctx).PrometheusEnforcedMatchers
}

// Discovery selectors are OR-ed, so every branch must include the policy.
func enforcePrometheusSelectors(ctx context.Context, selectors []string) ([]string, error) {
	enforced := prometheusEnforcedMatchers(ctx)
	if len(enforced) == 0 {
		return selectors, nil
	}
	if len(selectors) == 0 {
		selectors = []string{"{}"}
	}
	result := make([]string, 0, len(selectors))
	for _, selector := range selectors {
		matchers, err := promqlParser.ParseMetricSelector(selector)
		if err != nil {
			return nil, fmt.Errorf("enforced matcher injection: could not parse Prometheus selector %q: %w", selector, err)
		}
		matchers = append(matchers, enforced...)
		if !hasNonEmptyMatcher(matchers) {
			// Prometheus requires a non-empty matcher even for exclusion-only policies.
			matchers = append(matchers, labels.MustNewMatcher(labels.MatchNotEqual, "__name__", ""))
		}
		result = append(result, "{"+matchersString(matchers)+"}")
	}
	return result, nil
}

func guardUnscopedPrometheusDiscovery(ctx context.Context, operation string) error {
	if len(prometheusEnforcedMatchers(ctx)) > 0 {
		return fmt.Errorf("%s cannot be scoped by the configured Prometheus enforced matchers; refusing unfiltered discovery", operation)
	}
	return nil
}

// enforcePromQL AND-s the configured enforcement matchers into every vector
// selector of a PromQL query (instant or range) and returns the rewritten query.
// When enforcement is disabled it returns the query unchanged.
//
// This is a security control, so it fails CLOSED: any query that cannot be
// parsed returns an error rather than being sent to Prometheus unfiltered.
// If the query contains vector selectors, the matchers are appended to each
// selector so a user selector can only ever narrow results within the enforced
// bounds.
func enforcePromQL(ctx context.Context, query string, window time.Duration, stepSeconds int) (string, error) {
	enforced := prometheusEnforcedMatchers(ctx)
	if len(enforced) == 0 {
		return query, nil
	}

	// Substitute Grafana macros so the parser does not fail on $__interval, etc.
	expanded := promGuardSubstituteMacros(query, window, stepSeconds)

	ast, err := promGuardParser.ParseExpr(expanded)
	if err != nil {
		return "", fmt.Errorf("enforced matcher injection: could not parse PromQL query %q: %w", query, err)
	}

	var selectors []*parser.VectorSelector
	parser.Inspect(ast, func(node parser.Node, _ []parser.Node) error {
		if vs, ok := node.(*parser.VectorSelector); ok {
			selectors = append(selectors, vs)
		}
		return nil
	})

	for _, vs := range selectors {
		vs.LabelMatchers = append(vs.LabelMatchers, enforced...)
	}

	return ast.String(), nil
}
