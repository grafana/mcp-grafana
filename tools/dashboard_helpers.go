package tools

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	mcpgrafana "github.com/grafana/mcp-grafana"
)

// GetDashboardPanelQueriesParams defines parameters for extracting queries from a specific panel
type GetDashboardPanelQueriesParams struct {
	DashboardUID string            `json:"dashboardUid" jsonschema:"required,description=Dashboard UID"`
	PanelID      int               `json:"panelId" jsonschema:"required,description=Panel ID"`
	Variables    map[string]string `json:"variables" jsonschema:"description=Variable substitutions (e.g. {\"environment\": \"prod\"})"`
}

// PanelQueryInfo represents a single query from a panel with variable analysis
type PanelQueryInfo struct {
	RefID             string         `json:"refId"`
	RawQuery          string         `json:"rawQuery"`          // Original with ${variables}
	ProcessedQuery    string         `json:"processedQuery"`    // Variables substituted
	RequiredVariables []VariableInfo `json:"requiredVariables"` // Variables with defaults
	DatasourceUID     string         `json:"datasourceUid"`
	DatasourceType    string         `json:"datasourceType"`
}

// VariableInfo contains information about a template variable
type VariableInfo struct {
	Name         string `json:"name"`
	CurrentValue string `json:"currentValue"`           // From dashboard or provided override
	DefaultValue string `json:"defaultValue,omitempty"` // From dashboard definition
}

// GetDashboardPanelQueriesResult contains the result of extracting queries from a panel
type GetDashboardPanelQueriesResult struct {
	DashboardUID string           `json:"dashboardUid"`
	PanelID      int              `json:"panelId"`
	PanelTitle   string           `json:"panelTitle"`
	Queries      []PanelQueryInfo `json:"queries"`
}

// getDashboardPanelQueriesWithVariables extracts queries from a specific panel with variable substitution
func getDashboardPanelQueriesWithVariables(ctx context.Context, args GetDashboardPanelQueriesParams) (*GetDashboardPanelQueriesResult, error) {
	// Fetch the dashboard
	dashboard, err := getDashboardByUID(ctx, GetDashboardByUIDParams{UID: args.DashboardUID})
	if err != nil {
		return nil, fmt.Errorf("get dashboard by uid %s: %w", args.DashboardUID, err)
	}

	db, ok := dashboard.Dashboard.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("dashboard is not a JSON object")
	}

	// Extract variables from dashboard templating
	dashboardVariables := extractDashboardVariables(db)

	// Find the panel by ID
	panel, err := findPanelByID(db, args.PanelID)
	if err != nil {
		return nil, err
	}

	panelTitle := safeString(panel, "title")

	// Extract queries from the panel
	queries := extractPanelQueries(panel, dashboardVariables, args.Variables)

	return &GetDashboardPanelQueriesResult{
		DashboardUID: args.DashboardUID,
		PanelID:      args.PanelID,
		PanelTitle:   panelTitle,
		Queries:      queries,
	}, nil
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

// extractPanelQueries extracts all queries from a panel with variable analysis
func extractPanelQueries(panel map[string]interface{}, dashboardVars map[string]VariableInfo, overrides map[string]string) []PanelQueryInfo {
	var queries []PanelQueryInfo

	targets := safeArray(panel, "targets")
	if targets == nil {
		return queries
	}

	// Get panel-level datasource if set
	panelDsUID := ""
	panelDsType := ""
	if dsField := safeObject(panel, "datasource"); dsField != nil {
		panelDsUID = safeString(dsField, "uid")
		panelDsType = safeString(dsField, "type")
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
		dsUID := panelDsUID
		dsType := panelDsType
		if targetDs := safeObject(target, "datasource"); targetDs != nil {
			if uid := safeString(targetDs, "uid"); uid != "" {
				dsUID = uid
			}
			if t := safeString(targetDs, "type"); t != "" {
				dsType = t
			}
		}

		// Find required variables in the query
		requiredVars := findVariablesInQuery(rawQuery, dashboardVars, overrides)

		// Build effective variable map (dashboard defaults + overrides)
		effectiveVars := buildEffectiveVariables(dashboardVars, overrides)

		// Substitute variables in query
		processedQuery := substituteVariables(rawQuery, effectiveVars)

		// Also substitute variables in datasource UID if it's a variable reference
		dsUID = substituteVariables(dsUID, effectiveVars)

		queries = append(queries, PanelQueryInfo{
			RefID:             refID,
			RawQuery:          rawQuery,
			ProcessedQuery:    processedQuery,
			RequiredVariables: requiredVars,
			DatasourceUID:     dsUID,
			DatasourceType:    dsType,
		})
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
	result = regexp.MustCompile(`\$\{([a-zA-Z_][a-zA-Z0-9_]*)(?::[^}]*)?\}`).ReplaceAllStringFunc(result, func(match string) string {
		// Extract variable name
		re := regexp.MustCompile(`\$\{([a-zA-Z_][a-zA-Z0-9_]*)`)
		m := re.FindStringSubmatch(match)
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
		// Match $varname that is followed by non-word character or end of string
		pattern := regexp.MustCompile(`\$` + regexp.QuoteMeta(name) + `(?:\b|$)`)
		result = pattern.ReplaceAllString(result, value)
	}

	// Replace [[varname]] patterns
	result = regexp.MustCompile(`\[\[([a-zA-Z_][a-zA-Z0-9_]*)\]\]`).ReplaceAllStringFunc(result, func(match string) string {
		name := match[2 : len(match)-2]
		if val, ok := variables[name]; ok {
			return val
		}
		return match
	})

	return result
}

var GetPanelQueriesWithVariables = mcpgrafana.MustTool(
	"get_panel_queries",
	"Extract queries from a specific dashboard panel by panel ID\\, showing both raw queries (with template variables like ${varname}) and processed queries (with variables substituted). Use this to inspect what queries a panel executes and to understand its variable dependencies. The tool identifies all template variables used in the queries and shows their current/default values from the dashboard.",
	getDashboardPanelQueriesWithVariables,
	mcp.WithTitleAnnotation("Get panel queries with variable substitution"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
)

// AddDashboardHelperTools registers dashboard helper tools with the MCP server
func AddDashboardHelperTools(mcp *server.MCPServer) {
	GetPanelQueriesWithVariables.Register(mcp)
}
