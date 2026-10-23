package tools

import (
	"encoding/json"
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

// findPanelByID searches for a panel by ID, including nested panels in rows.
// Supports both modern dashboards (top-level "panels", with row-typed entries
// holding nested panels) and legacy schemaVersion <= 14 dashboards
// (top-level "rows":[{panels:[...]}], no top-level "panels" array).
// The legacy fallback only triggers when "panels" is absent, mirroring
// getDashboardSummary so dashboards carrying both keys aren't walked twice.
func findPanelByID(db map[string]interface{}, panelID int) (map[string]interface{}, error) {
	if panels := safeArray(db, "panels"); panels != nil {
		for _, p := range panels {
			panel, ok := p.(map[string]interface{})
			if !ok {
				continue
			}

			if safeInt(panel, "id") == panelID {
				return panel, nil
			}

			if safeString(panel, "type") == "row" {
				for _, np := range safeArray(panel, "panels") {
					if nested, ok := np.(map[string]interface{}); ok && safeInt(nested, "id") == panelID {
						return nested, nil
					}
				}
			}
		}
		return nil, fmt.Errorf("panel with ID %d not found", panelID)
	}

	rows := safeArray(db, "rows")
	if rows == nil {
		return nil, fmt.Errorf("dashboard has no panels")
	}
	for _, r := range rows {
		row, ok := r.(map[string]interface{})
		if !ok {
			continue
		}
		for _, np := range safeArray(row, "panels") {
			if nested, ok := np.(map[string]interface{}); ok && safeInt(nested, "id") == panelID {
				return nested, nil
			}
		}
	}

	return nil, fmt.Errorf("panel with ID %d not found", panelID)
}

// collectAllPanels returns all panels from a dashboard, including nested
// panels inside rows. Handles modern dashboards (top-level "panels", with
// row-typed entries that hold nested panels) and legacy schemaVersion <= 14
// dashboards (top-level "rows":[{panels:[...]}], no top-level "panels").
// The legacy fallback only triggers when "panels" is absent, mirroring
// getDashboardSummary so dashboards carrying both keys don't yield duplicates.
func collectAllPanels(db map[string]interface{}) []map[string]interface{} {
	var result []map[string]interface{}

	if panels := safeArray(db, "panels"); panels != nil {
		for _, p := range panels {
			panel, ok := p.(map[string]interface{})
			if !ok {
				continue
			}

			result = append(result, panel)

			if safeString(panel, "type") == "row" {
				for _, np := range safeArray(panel, "panels") {
					if nested, ok := np.(map[string]interface{}); ok {
						result = append(result, nested)
					}
				}
			}
		}
		return result
	}

	for _, r := range safeArray(db, "rows") {
		row, ok := r.(map[string]interface{})
		if !ok {
			continue
		}
		for _, np := range safeArray(row, "panels") {
			if nested, ok := np.(map[string]interface{}); ok {
				result = append(result, nested)
			}
		}
	}

	return result
}

// extractPanelQueries extracts all queries from a panel.
func extractPanelQueries(panel map[string]interface{}) []panelQuery {
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
			rawTarget:  queryTargetFields(target),
			Datasource: dsInfo,
			RefID:      refID,
		}

		// Targets built in a datasource's visual editor carry no string
		// expression at all. Hand back the target itself instead of dropping
		// them silently.
		if rawQuery == "" {
			if !targetDescribesQuery(target) {
				continue
			}
			pq.Target = queryTargetFields(target)
		}

		queries = append(queries, pq)
	}

	return queries
}

// extractQueryExpression extracts the query string from a target
// Different datasources store queries in different fields
func extractQueryExpression(target map[string]interface{}) string {
	// Try common query field names
	queryFields := []string{
		"expr",        // Prometheus
		"query",       // Loki, ClickHouse, generic
		"expression",  // CloudWatch
		"rawSql",      // SQL databases
		"rawSQL",      // Athena (grafana-athena-datasource)
		"rawQuery",    // Some datasources
		"target",      // Graphite metric path
		"cypherQuery", // Neo4j
	}

	for _, field := range queryFields {
		if val := safeString(target, field); val != "" {
			return val
		}
	}

	return ""
}

// structuredQueryFields are target fields we recognise as part of a query built
// in a datasource's visual editor rather than written out as a string
// expression: CloudWatch metric search, Pyroscope profile selection, InfluxDB's
// query builder and so on. Recognising a field is not enough to call the target
// a query -- see identifyingQueryFields.
var structuredQueryFields = []string{
	// CloudWatch metric search
	"region", "namespace", "metricName", "statistic", "statistics", "dimensions", "accountId",
	// Pyroscope
	"profileTypeId", "labelSelector", "spanSelector",
	// InfluxDB query builder
	"policy", "measurement", "select", "tags", "groupBy",
	// Elasticsearch aggregations
	"metrics", "bucketAggs",
	// Azure Monitor
	"azureMonitor", "azureLogAnalytics", "azureTraces",
}

// structuredFieldSet indexes structuredQueryFields for lookups.
var structuredFieldSet = func() map[string]bool {
	set := make(map[string]bool, len(structuredQueryFields))
	for _, field := range structuredQueryFields {
		set[field] = true
	}
	return set
}()

// identifyingQueryFields is the subset of structuredQueryFields that says what a
// query is *about*. The rest only refine one: Grafana persists them straight
// from the query editor's defaults -- CloudWatch's "Average" statistic and
// "default" region, InfluxDB's default retention policy -- so a query row nobody
// filled in has them set and nothing else.
var identifyingQueryFields = map[string]bool{
	"metricName":        true, // CloudWatch metric search
	"profileTypeId":     true, // Pyroscope
	"measurement":       true, // InfluxDB query builder
	"metrics":           true, // Elasticsearch aggregations (an empty lucene query is match-all, and still runs)
	"azureMonitor":      true,
	"azureLogAnalytics": true,
	"azureTraces":       true,
}

// editorStateFields hold rendering, transport or query-editor state. Their
// presence says nothing about whether a target asks for anything, so they don't
// keep an otherwise-empty query row in the results. They are still returned to
// the caller: queryType, for one, is what tells InfluxDB whether the panel is
// influxql or flux.
var editorStateFields = map[string]bool{
	"datasource":       true,
	"datasourceId":     true,
	"refId":            true,
	"hide":             true,
	"hidden":           true,
	"key":              true,
	"id":               true,
	"editorMode":       true,
	"queryType":        true,
	"queryMode":        true,
	"metricQueryType":  true,
	"metricEditorMode": true,
	"matchExact":       true,
	"format":           true,
	"resultFormat":     true,
	"instant":          true,
	"range":            true,
	"exemplar":         true,
	"legendFormat":     true,
	"alias":            true,
	"interval":         true,
	"intervalMs":       true,
	"intervalFactor":   true,
	"maxDataPoints":    true,
}

// targetDescribesQuery reports whether a target that carries no string
// expression nevertheless describes a query. It distinguishes a real
// visual-editor query from a query row somebody added in the UI and never
// filled in, which Grafana saves with the editor's defaults in place.
func targetDescribesQuery(target map[string]interface{}) bool {
	for field, val := range target {
		if isEmptyTargetValue(val) {
			continue
		}
		if identifyingQueryFields[field] {
			return true
		}
		// A field we don't recognise at all belongs to a datasource we have no
		// list for. Take the target at its word rather than dropping the panel,
		// which is the failure this whole path exists to avoid.
		if !structuredFieldSet[field] && !editorStateFields[field] {
			return true
		}
	}
	return false
}

// queryTargetFields returns a target's fields for the caller to interpret,
// minus the two the result already carries in their own fields. Nothing else is
// filtered: Grafana runs a CloudWatch panel by posting the whole target back to
// /api/ds/query, so any field may be part of what the panel asks for.
func queryTargetFields(target map[string]interface{}) map[string]interface{} {
	fields := make(map[string]interface{}, len(target))
	for k, v := range target {
		if k == "datasource" || k == "refId" {
			continue
		}
		fields[k] = v
	}
	return fields
}

// isEmptyTargetValue reports whether a target field is set but carries no
// information, which query editors leave behind routinely.
func isEmptyTargetValue(val interface{}) bool {
	switch v := val.(type) {
	case nil:
		return true
	case string:
		return v == ""
	case []interface{}:
		return len(v) == 0
	case map[string]interface{}:
		return len(v) == 0
	default:
		return false
	}
}

// variableSearchText includes both the expression and target fields, since
// expression queries can also reference variables in location or dimensions.
// rawTarget preserves those fields without changing the public response shape.
func variableSearchText(pq panelQuery) string {
	target := pq.rawTarget
	if target == nil {
		target = pq.Target
	}
	encoded, err := json.Marshal(target)
	if err != nil {
		return pq.Query
	}
	return pq.Query + " " + string(encoded)
}

// variableRegex matches Grafana template variable patterns
// Matches: $varname, ${varname}, ${varname:option}, [[varname]], [[varname:option]]
var variableRegex = regexp.MustCompile(`\$\{([a-zA-Z_][a-zA-Z0-9_]*)(?::[^}]*)?\}|\$([a-zA-Z_][a-zA-Z0-9_]*)|\[\[([a-zA-Z_][a-zA-Z0-9_]*)(?::[^\]]*)?\]\]`)

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
