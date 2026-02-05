//go:build !dashboard_helpers
// +build !dashboard_helpers

// This is a stub file that provides minimal dashboard helper functions for compilation.
// It will be replaced when PR #539 (get-panel-query) is merged.
// The actual implementation is in dashboard_helpers.go from PR #539.

package tools

import (
	"fmt"
	"strings"
)

// findPanelByID stub - searches for a panel by ID
func findPanelByID(db map[string]interface{}, panelID int) (map[string]interface{}, error) {
	panels := safeArray(db, "panels")
	if panels == nil {
		return nil, fmt.Errorf("dashboard has no panels")
	}

	for _, p := range panels {
		panel, ok := p.(map[string]interface{})
		if !ok {
			continue
		}

		id := safeInt(panel, "id")
		if id == panelID {
			return panel, nil
		}

		// Check for nested panels in row type
		if safeString(panel, "type") == "row" {
			nestedPanels := safeArray(panel, "panels")
			for _, np := range nestedPanels {
				nestedPanel, ok := np.(map[string]interface{})
				if !ok {
					continue
				}
				if safeInt(nestedPanel, "id") == panelID {
					return nestedPanel, nil
				}
			}
		}
	}

	return nil, fmt.Errorf("panel with ID %d not found", panelID)
}

// extractQueryExpression stub - extracts query string from a target
func extractQueryExpression(target map[string]interface{}) string {
	queryFields := []string{
		"expr",       // Prometheus
		"query",      // Loki, ClickHouse, generic
		"expression", // CloudWatch
		"rawSql",     // SQL databases
		"rawQuery",   // Some datasources
	}

	for _, field := range queryFields {
		if val := safeString(target, field); val != "" {
			return val
		}
	}

	return ""
}

// substituteVariables stub - replaces template variables in a query
func substituteVariables(query string, variables map[string]string) string {
	result := query
	for name, value := range variables {
		// Replace ${varname}
		result = strings.ReplaceAll(result, "${"+name+"}", value)
		// Replace $varname (followed by non-word character)
		result = strings.ReplaceAll(result, "$"+name, value)
		// Replace [[varname]]
		result = strings.ReplaceAll(result, "[["+name+"]]", value)
	}
	return result
}
