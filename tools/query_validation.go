package tools

import (
	"fmt"

	"github.com/grafana/loki/v3/pkg/logql/syntax"
	"github.com/prometheus/prometheus/promql/parser"
)

// ValidatePromQL validates a PromQL expression using the Prometheus parser
// Returns nil if the expression is valid, or an error describing the syntax issue
func ValidatePromQL(expr string) error {
	if expr == "" {
		return fmt.Errorf("invalid PromQL syntax: expression cannot be empty")
	}

	// Use the Prometheus parser to validate the expression
	_, err := parser.ParseExpr(expr)
	if err != nil {
		return fmt.Errorf("invalid PromQL syntax: %w", err)
	}

	return nil
}

// ValidateLogQL validates a LogQL expression using the Loki parser
// Returns nil if the expression is valid, or an error describing the syntax issue
func ValidateLogQL(expr string) error {
	if expr == "" {
		return fmt.Errorf("invalid LogQL syntax: expression cannot be empty")
	}

	// Use the Loki parser to validate the expression
	// ParseExpr validates both log selector and metric query syntax
	_, err := syntax.ParseExpr(expr)
	if err != nil {
		return fmt.Errorf("invalid LogQL syntax: %w", err)
	}

	return nil
}
