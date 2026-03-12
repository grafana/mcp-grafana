package tools

import (
	"fmt"
	"regexp"
	"strings"
)

// VariableInfo contains information about a template variable
type VariableInfo struct {
	Name         string `json:"name"`
	CurrentValue string `json:"currentValue"`           // From dashboard or provided override
	DefaultValue string `json:"defaultValue,omitempty"` // From dashboard definition
}

// extractDashboardVariables extracts variable definitions from the dashboard templating section
func extractDashboardVariables(db map[string]interface{}) map[string]VariableInfo {
	variables := make(map[string]VariableInfo)

	templating := safeObject(db, "templating")
	if templating == nil {
		return variables
	}

	list := safeArray(templating, "list")
	if list == nil {
		return variables
	}

	for _, v := range list {
		variable, ok := v.(map[string]interface{})
		if !ok {
			continue
		}

		name := safeString(variable, "name")
		if name == "" {
			continue
		}

		varInfo := VariableInfo{
			Name: name,
		}

		// Extract current value
		current := safeObject(variable, "current")
		if current != nil {
			// Current value can be a string or complex object
			if val, ok := current["value"]; ok {
				switch v := val.(type) {
				case string:
					varInfo.CurrentValue = v
				case []interface{}:
					// Multi-value variable, join with comma
					strs := make([]string, 0, len(v))
					for _, item := range v {
						if s, ok := item.(string); ok {
							strs = append(strs, s)
						}
					}
					varInfo.CurrentValue = strings.Join(strs, ",")
				}
			}
		}

		// Extract default value from options if available
		options := safeArray(variable, "options")
		if len(options) > 0 {
			if firstOption, ok := options[0].(map[string]interface{}); ok {
				if val, ok := firstOption["value"].(string); ok {
					varInfo.DefaultValue = val
				}
			}
		}

		// Also check query for default/initial value
		if varInfo.DefaultValue == "" {
			if query := safeString(variable, "query"); query != "" {
				// For constant type, query is the value
				if safeString(variable, "type") == "constant" {
					varInfo.DefaultValue = query
				}
			}
		}

		variables[name] = varInfo
	}

	return variables
}

// findPanelByID searches for a panel by ID, including nested panels in rows
func findPanelByID(db map[string]interface{}, panelID int) (map[string]interface{}, error) {
	panels := safeArray(db, "panels")
	if panels == nil {
		return nil, fmt.Errorf("dashboard has no panels")
	}

	// Search top-level panels
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

// collectAllPanels returns all panels from a dashboard, including nested panels inside rows
func collectAllPanels(db map[string]interface{}) []map[string]interface{} {
	var result []map[string]interface{}

	panels := safeArray(db, "panels")
	if panels == nil {
		return result
	}

	for _, p := range panels {
		panel, ok := p.(map[string]interface{})
		if !ok {
			continue
		}

		result = append(result, panel)

		// Also include nested panels from row-type panels
		if safeString(panel, "type") == "row" {
			nestedPanels := safeArray(panel, "panels")
			for _, np := range nestedPanels {
				if nestedPanel, ok := np.(map[string]interface{}); ok {
					result = append(result, nestedPanel)
				}
			}
		}
	}

	return result
}

// extractPanelQueries extracts all queries from a panel.
// When dashboardVars is non-nil, performs variable analysis and substitution,
// populating ProcessedQuery and RequiredVariables fields.
func extractPanelQueries(panel map[string]interface{}, dashboardVars map[string]VariableInfo, overrides map[string]string) []panelQuery {
	var queries []panelQuery

	title := safeString(panel, "title")
	targets := safeArray(panel, "targets")
	if targets == nil {
		return queries
	}

	// Get panel-level datasource if set
	var panelDs datasourceInfo
	if dsField := safeObject(panel, "datasource"); dsField != nil {
		panelDs.UID = safeString(dsField, "uid")
		panelDs.Type = safeString(dsField, "type")
	}

	for _, t := range targets {
		target, ok := t.(map[string]interface{})
		if !ok {
			continue
		}

		refID := safeString(target, "refId")

		// Extract query expression - try common fields
		rawQuery := extractQueryExpression(target)

		// Get datasource from target or fall back to panel level
		dsInfo := panelDs
		if targetDs := safeObject(target, "datasource"); targetDs != nil {
			if uid := safeString(targetDs, "uid"); uid != "" {
				dsInfo.UID = uid
			}
			if dsType := safeString(targetDs, "type"); dsType != "" {
				dsInfo.Type = dsType
			}
		}

		pq := panelQuery{
			Title:      title,
			Query:      rawQuery,
			Datasource: dsInfo,
			RefID:      refID,
		}

		// Only do variable processing when dashboardVars is provided
		if dashboardVars != nil {
			pq.RequiredVariables = findVariablesInQuery(rawQuery, dashboardVars, overrides)
			effectiveVars := buildEffectiveVariables(dashboardVars, overrides)
			pq.ProcessedQuery = substituteVariables(rawQuery, effectiveVars)
			dsInfo.UID = substituteVariables(dsInfo.UID, effectiveVars)
			pq.Datasource = dsInfo
		}

		if rawQuery != "" {
			queries = append(queries, pq)
		}
	}

	return queries
}

// extractQueryExpression extracts the query string from a target
// Different datasources store queries in different fields
func extractQueryExpression(target map[string]interface{}) string {
	// Try common query field names
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

// variableRegex matches Grafana template variable patterns
// Matches: $varname, ${varname}, ${varname:option}, [[varname]]
var variableRegex = regexp.MustCompile(`\$\{([a-zA-Z_][a-zA-Z0-9_]*)(?::[^}]*)?\}|\$([a-zA-Z_][a-zA-Z0-9_]*)|\[\[([a-zA-Z_][a-zA-Z0-9_]*)\]\]`)

// Pre-compiled regex patterns for substituteVariables
var (
	dollarBraceVarRegex   = regexp.MustCompile(`\$\{([a-zA-Z_][a-zA-Z0-9_]*)(?::[^}]*)?\}`)
	dollarBraceNameRegex  = regexp.MustCompile(`\$\{([a-zA-Z_][a-zA-Z0-9_]*)`)
	doubleBracketVarRegex = regexp.MustCompile(`\[\[([a-zA-Z_][a-zA-Z0-9_]*)\]\]`)
)

// findVariablesInQuery extracts all variable references from a query
func findVariablesInQuery(query string, dashboardVars map[string]VariableInfo, overrides map[string]string) []VariableInfo {
	var variables []VariableInfo
	seen := make(map[string]bool)

	matches := variableRegex.FindAllStringSubmatch(query, -1)
	for _, match := range matches {
		// match[1] is ${varname}, match[2] is $varname, match[3] is [[varname]]
		varName := ""
		for i := 1; i <= 3; i++ {
			if match[i] != "" {
				varName = match[i]
				break
			}
		}

		if varName == "" || seen[varName] {
			continue
		}
		seen[varName] = true

		varInfo := VariableInfo{
			Name: varName,
		}

		// Check if we have dashboard definition for this variable
		if dashVar, ok := dashboardVars[varName]; ok {
			varInfo.DefaultValue = dashVar.DefaultValue
			varInfo.CurrentValue = dashVar.CurrentValue
		}

		// Override with provided value if available
		if override, ok := overrides[varName]; ok {
			varInfo.CurrentValue = override
		}

		variables = append(variables, varInfo)
	}

	return variables
}

// buildEffectiveVariables combines dashboard variables with user overrides
func buildEffectiveVariables(dashboardVars map[string]VariableInfo, overrides map[string]string) map[string]string {
	effective := make(map[string]string)

	// Start with dashboard variable current values
	for name, varInfo := range dashboardVars {
		if varInfo.CurrentValue != "" {
			effective[name] = varInfo.CurrentValue
		} else if varInfo.DefaultValue != "" {
			effective[name] = varInfo.DefaultValue
		}
	}

	// Apply overrides
	for name, value := range overrides {
		effective[name] = value
	}

	return effective
}

// substituteVariables replaces template variables in a query with their values
func substituteVariables(query string, variables map[string]string) string {
	result := query

	// Replace ${varname:option} and ${varname} patterns
	result = dollarBraceVarRegex.ReplaceAllStringFunc(result, func(match string) string {
		// Extract variable name
		m := dollarBraceNameRegex.FindStringSubmatch(match)
		if len(m) > 1 {
			if val, ok := variables[m[1]]; ok {
				return val
			}
		}
		return match
	})

	// Replace $varname patterns (but not ${varname} which was already handled)
	// Be careful not to match variable names that are part of other words
	for name, value := range variables {
		result = replaceSimpleDollarVar(result, name, value)
	}

	// Replace [[varname]] patterns
	result = doubleBracketVarRegex.ReplaceAllStringFunc(result, func(match string) string {
		name := match[2 : len(match)-2]
		if val, ok := variables[name]; ok {
			return val
		}
		return match
	})

	return result
}

// replaceSimpleDollarVar replaces $varName followed by a non-word character or end of string
func replaceSimpleDollarVar(s, varName, value string) string {
	prefix := "$" + varName
	prefixLen := len(prefix)

	var result strings.Builder
	result.Grow(len(s))
	i := 0
	for i < len(s) {
		if s[i] == '$' && i+prefixLen <= len(s) && s[i:i+prefixLen] == prefix {
			afterIdx := i + prefixLen
			if afterIdx == len(s) || !isWordChar(s[afterIdx]) {
				result.WriteString(value)
				i = afterIdx
				continue
			}
		}
		result.WriteByte(s[i])
		i++
	}
	return result.String()
}

// isWordChar returns true if the byte is a word character [a-zA-Z0-9_]
func isWordChar(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9') || b == '_'
}
