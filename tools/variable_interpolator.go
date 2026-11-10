package tools

import (
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

// templateVariableValues holds every selected value of each template variable.
// Grafana stores multi-select values in current.value as an array, and formatted
// interpolation needs the whole list.
type templateVariableValues map[string][]string

// variableInterpolator resolves a dashboard's template variables once and
// substitutes them in a single pass, as Grafana's frontend does: a value that
// itself contains a variable reference is never substituted again, and the
// result does not depend on map iteration order.
//
// Use interpolateQuery for a panel's query expression, where the datasource
// decides the default formatting, and interpolate or interpolateValue for other
// fields (datasource UIDs, target fields), which keep generic formatting.
type variableInterpolator struct {
	values   templateVariableValues
	multi    map[string]bool   // multi-value or include-All variables
	warnings map[string]string // variables set to All or without a value, by name
	pattern  *regexp.Regexp    // nil when there is nothing to substitute
}

// newDashboardInterpolator reads the selected values of a dashboard's variables
// (v1 templating; convert v2 with templatingV1FromV2 first) and applies the
// caller's overrides, which replace a variable's selection with one value.
func newDashboardInterpolator(db map[string]interface{}, overrides map[string]string) *variableInterpolator {
	values := extractTemplateVariableValues(db)
	for name, value := range overrides {
		values[name] = []string{value}
	}
	v := newVariableInterpolator(values, multiValueTemplateVariables(db))
	v.warnings = allTemplateSelectionWarnings(db, overrides, values)
	return v
}

// newVariableInterpolator substitutes the given values. multi names the
// variables that Grafana formats as a list in an unformatted reference; nil
// keeps the first value for every variable.
func newVariableInterpolator(values templateVariableValues, multi map[string]bool) *variableInterpolator {
	return &variableInterpolator{values: values, multi: multi, pattern: variableReferencePattern(values)}
}

// variableReferencePattern matches $name, ${name}, ${name:format}, [[name]] and
// [[name:format]] for every known name. The delimiters and the trailing \b keep
// $job from matching inside $jobname. Names are sorted, longest first, so the
// pattern is the same on every call and a name that contains ":" wins over a
// shorter name with a format. A ${name.fieldPath} reference is not matched and
// is left as is.
func variableReferencePattern(values templateVariableValues) *regexp.Regexp {
	names := make([]string, 0, len(values))
	for name := range values {
		// A name that isn't valid UTF-8 can't be compiled into the pattern, and
		// can't occur in a query decoded from JSON, so it is skipped.
		if name != "" && utf8.ValidString(name) {
			names = append(names, regexp.QuoteMeta(name))
		}
	}
	if len(names) == 0 {
		return nil
	}
	sort.Slice(names, func(i, j int) bool {
		if len(names[i]) != len(names[j]) {
			return len(names[i]) > len(names[j])
		}
		return names[i] < names[j]
	})
	alt := strings.Join(names, "|")
	return regexp.MustCompile(fmt.Sprintf(`\$\{(%s)(?::([^}]*))?\}|\[\[(%s)(?::([^\]]*))?\]\]|\$(%s)\b`, alt, alt, alt))
}

// resolveAutoIntervals gives interval variables set to "auto" their value for
// the query's time range (resolveAutoIntervalVariables). It adds the
// $__auto_interval names, so the reference pattern is rebuilt.
func (v *variableInterpolator) resolveAutoIntervals(db map[string]interface{}, start, end string) {
	resolveAutoIntervalVariables(db, v.values, start, end)
	v.pattern = variableReferencePattern(v.values)
}

// firstValue is the first selected value of name, used to resolve a datasource
// variable. A variable with an empty selection has no first value.
func (v *variableInterpolator) firstValue(name string) (string, bool) {
	values := v.values[name]
	if len(values) == 0 {
		return "", false
	}
	return values[0], true
}

// allSelectionWarning explains why a query that references name can't run, or
// is empty: Grafana expands All, or picks a missing value, from the variable's
// options, which mcp-grafana doesn't fetch.
func (v *variableInterpolator) allSelectionWarning(name string) string {
	return v.warnings[name]
}

// interpolateQuery substitutes a query expression for the given datasource type.
func (v *variableInterpolator) interpolateQuery(query, datasourceType string) string {
	return v.substitute(query, formatForDatasource(datasourceType))
}

// interpolate substitutes a field that isn't a query expression.
func (v *variableInterpolator) interpolate(s string) string {
	return v.substitute(s, genericFormat)
}

// interpolateValue returns a deep copy of value with every string inside it
// interpolated. Maps and slices are copied; other values are returned as is.
func (v *variableInterpolator) interpolateValue(value interface{}) interface{} {
	return mapStrings(value, v.interpolate)
}

// mapStrings returns a deep copy of value with f applied to every string in it.
func mapStrings(value interface{}, f func(string) string) interface{} {
	switch val := value.(type) {
	case string:
		return f(val)
	case map[string]interface{}:
		result := make(map[string]interface{}, len(val))
		for k, item := range val {
			result[k] = mapStrings(item, f)
		}
		return result
	case []interface{}:
		result := make([]interface{}, len(val))
		for i, item := range val {
			result[i] = mapStrings(item, f)
		}
		return result
	default:
		return value
	}
}

// interpolateTarget returns an interpolated deep copy of a panel target.
func (v *variableInterpolator) interpolateTarget(target map[string]interface{}) map[string]interface{} {
	if target == nil {
		return map[string]interface{}{}
	}
	return v.interpolateValue(target).(map[string]interface{})
}

func (v *variableInterpolator) substitute(s string, f datasourceFormat) string {
	if v.pattern == nil {
		return s
	}
	matches := v.pattern.FindAllStringSubmatchIndex(s, -1)
	if matches == nil {
		return s
	}
	var b strings.Builder
	last := 0
	for _, m := range matches {
		b.WriteString(s[last:m[0]])
		b.WriteString(v.formatReference(s, m, f))
		last = m[1]
	}
	b.WriteString(s[last:])
	return b.String()
}

// formatReference formats one match of variableReferencePattern. Groups 1-2 are
// ${name:format}, 3-4 are [[name:format]] and 5 is $name.
func (v *variableInterpolator) formatReference(s string, m []int, f datasourceFormat) string {
	group := func(i int) (string, bool) {
		if m[2*i] < 0 {
			return "", false
		}
		return s[m[2*i]:m[2*i+1]], true
	}
	var name, format string
	var hasFormat bool
	switch {
	case m[2] >= 0:
		name, _ = group(1)
		format, hasFormat = group(2)
	case m[6] >= 0:
		name, _ = group(3)
		format, hasFormat = group(4)
	default:
		name, _ = group(5)
	}
	values := v.values[name]
	if hasFormat {
		// Unlike the other formats, lucene depends on whether the variable
		// holds a list: Grafana passes a multi-value selection as an array,
		// even with one value or none.
		if formatName, _, _ := strings.Cut(format, ":"); formatName == "lucene" {
			return formatLucene(values, v.multi[name])
		}
		return f.explicit(values, format)
	}
	if f.unformatted != nil {
		return f.unformatted(values, v.multi[name])
	}
	if f.multi != nil && v.multi[name] && len(values) > 0 {
		return f.multi(values)
	}
	// Only dashboard variables are escaped; query_sql's variables (multi is
	// nil) are inserted as given.
	if f.single != nil && v.multi != nil && !v.multi[name] && len(values) > 0 {
		return f.single(values[0])
	}
	return firstTemplateVariableValue(values)
}

// datasourceFormat is how a datasource formats variables. It is chosen by the
// datasource's own type and is independent of normalizeDatasourceType, which
// routes queries: stackdriver runs on the Prometheus executor but is formatted
// generically.
type datasourceFormat struct {
	// explicit formats ${name:format} and [[name:format]].
	explicit func(values []string, format string) string
	// multi formats an unformatted reference to a multi-value or include-All
	// variable, as Grafana's interpolateQueryExpr (Prometheus, Loki) and
	// interpolateVariable (SQL) do. nil keeps the first value.
	multi func(values []string) string
	// single escapes an unformatted reference to any other dashboard
	// variable, as the same functions do. nil keeps the value raw.
	single func(value string) string
	// unformatted, when set, formats every unformatted reference instead of
	// multi and single; list is whether the variable is multi-value or
	// include-All.
	unformatted func(values []string, list bool) string
}

var genericFormat = datasourceFormat{explicit: formatTemplateVariable}

// formatForDatasource returns the formatting for a datasource type, matched
// case-insensitively. BigQuery has its own sqlstring quoting; Prometheus, Loki
// and the SQL datasources format multi-value variables, and Prometheus and the
// SQL datasources escape single-value ones. Elasticsearch formats every
// variable in the lucene format, as its interpolateLuceneQuery does.
func formatForDatasource(datasourceType string) datasourceFormat {
	lower := strings.ToLower(datasourceType)
	f := genericFormat
	if strings.Contains(lower, "bigquery") {
		f.explicit = formatBigQueryTemplateVariable
	}
	switch lower {
	case "prometheus":
		f.multi = func(values []string) string { return regexAlternation(values, true) }
		f.single = prometheusRegularEscape
	case "loki":
		f.multi = func(values []string) string { return regexAlternation(values, false) }
	case "postgres", "grafana-postgresql-datasource", "mysql", "mssql":
		f.multi = formatSQLLiteralList
		f.single = func(value string) string { return strings.ReplaceAll(value, "'", "''") }
	case elasticsearchDatasourceType:
		f.unformatted = formatLucene
	}
	return f
}

// prometheusRegularEscape is Grafana's classic escaping of a single-value
// variable in PromQL: backslashes are doubled and single quotes escaped.
func prometheusRegularEscape(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	return strings.ReplaceAll(value, "'", `\\'`)
}

// luceneSpecialCharacters are the characters Grafana's luceneEscape escapes.
var luceneSpecialCharacters = regexp.MustCompile(`[!*+\-=<>\s&|()\[\]{}^~?:\\/"]`)

// luceneEscape escapes value as Grafana's luceneEscape does, leaving numbers as
// they are.
func luceneEscape(value string) string {
	trimmed := strings.TrimSpace(value)
	if f, err := strconv.ParseFloat(trimmed, 64); trimmed == "" || err == nil && !math.IsInf(f, 0) {
		return value
	}
	return luceneSpecialCharacters.ReplaceAllString(value, `\${0}`)
}

// formatLucene formats values as Grafana's lucene format does. A list is
// written as ("a" OR "b"), each value escaped, and an empty list as __empty__,
// which matches nothing; otherwise the first value is escaped.
func formatLucene(values []string, list bool) string {
	if !list {
		return luceneEscape(firstTemplateVariableValue(values))
	}
	if len(values) == 0 {
		return "__empty__"
	}
	quoted := make([]string, len(values))
	for i, value := range values {
		quoted[i] = `"` + luceneEscape(value) + `"`
	}
	return "(" + strings.Join(quoted, " OR ") + ")"
}

var regexMetacharacters = regexp.MustCompile(`[$^*{}\[\]+?.()|]`)

// regexAlternation escapes each value as Grafana's prometheusSpecialRegexEscape
// and lokiSpecialRegexEscape do, doubling every escape because the result sits
// inside a double-quoted string. Prometheus groups several values; Loki doesn't.
// Quotes are left alone, as in Prometheus's default (classic) escaping; only the
// experimental prometheusSpecialCharsInLabelValues toggle escapes them.
func regexAlternation(values []string, group bool) string {
	escaped := make([]string, len(values))
	for i, value := range values {
		value = strings.ReplaceAll(value, `\`, `\\\\`)
		escaped[i] = regexMetacharacters.ReplaceAllString(value, `\\${0}`)
	}
	joined := strings.Join(escaped, "|")
	if group && len(escaped) > 1 {
		return "(" + joined + ")"
	}
	return joined
}

// formatSQLLiteralList quotes each value as Grafana's SQL quoteLiteral does.
func formatSQLLiteralList(values []string) string {
	quoted := make([]string, len(values))
	for i, value := range values {
		quoted[i] = "'" + strings.ReplaceAll(value, "'", "''") + "'"
	}
	return strings.Join(quoted, ",")
}

func firstTemplateVariableValue(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

// formatTemplateVariable applies an explicit format. sqlstring follows
// Grafana's formatter (lucene is handled in formatReference); unknown formats fall back to Grafana's glob.
func formatTemplateVariable(values []string, format string) string {
	formatName := format
	if separator := strings.IndexByte(format, ':'); separator >= 0 {
		formatName = format[:separator]
	}

	if formatName == "sqlstring" {
		return formatSQLStringVariable(values)
	}

	// Grafana uses glob as the fallback for unknown formatting options.
	if len(values) > 1 {
		return "{" + strings.Join(values, ",") + "}"
	}
	return firstTemplateVariableValue(values)
}

func formatSQLStringVariable(values []string) string {
	if len(values) == 0 {
		return ""
	}

	formatted := make([]string, len(values))
	for i, value := range values {
		// Match Grafana's SQLString formatter: pair single quotes and escape
		// double quotes with a backslash before surrounding the value in quotes.
		value = strings.ReplaceAll(value, "'", "''")
		value = strings.ReplaceAll(value, `"`, `\"`)
		formatted[i] = "'" + value + "'"
	}
	return strings.Join(formatted, ",")
}

// multiValueTemplateVariables names the dashboard variables with multi-value or
// include-All enabled.
func multiValueTemplateVariables(db map[string]interface{}) map[string]bool {
	multi := make(map[string]bool)
	for _, item := range safeArray(safeObject(db, "templating"), "list") {
		variable, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		isMulti, _ := variable["multi"].(bool)
		includeAll, _ := variable["includeAll"].(bool)
		if name := safeString(variable, "name"); name != "" && (isMulti || includeAll) {
			multi[name] = true
		}
	}
	return multi
}

// extractTemplateVariableValues extracts all selected values of each dashboard
// template variable. Grafana stores multi-select values in current.value as an
// array, and formatted interpolation needs to retain that array.
func extractTemplateVariableValues(db map[string]interface{}) templateVariableValues {
	variables := make(templateVariableValues)

	templating := safeObject(db, "templating")
	if templating == nil {
		return variables
	}

	list := safeArray(templating, "list")
	for _, v := range list {
		variable, ok := v.(map[string]interface{})
		if !ok {
			continue
		}

		name := safeString(variable, "name")
		if name == "" {
			continue
		}

		// Get current value - can be in different formats
		current := safeObject(variable, "current")
		currentValueSet := false
		if current != nil {
			// Try "value" field first (can be string or array).
			if val, ok := current["value"]; ok {
				currentValueSet = true
				switch v := val.(type) {
				case string:
					if v != "$__all" {
						variables[name] = []string{v}
					}
				case nil:
					// Grafana formats a defined null/undefined value as an empty
					// string. Keep the empty slice distinguishable from a missing
					// variable so formatted expressions are still replaced.
					variables[name] = []string{}
				case []interface{}:
					if len(v) == 0 {
						variables[name] = []string{}
					} else if first, ok := v[0].(string); ok && first != "$__all" {
						values := make([]string, 0, len(v))
						for _, item := range v {
							if str, ok := item.(string); ok {
								values = append(values, str)
							}
						}
						variables[name] = values
					}
				case []string:
					if len(v) == 0 || v[0] != "$__all" {
						variables[name] = append([]string(nil), v...)
					}
				}
			}
			// Fall back to "text" field
			if !currentValueSet {
				if text, ok := current["text"].(string); ok && text != "" && text != "All" {
					variables[name] = []string{text}
				}
			}
		}

		// Constant and textbox variables hold their value in "query" and are
		// frequently saved without a "current" at all. Without this fallback
		// the raw $name survives substitution and reaches the datasource,
		// which for SQL datasources is a syntax error.
		if !currentValueSet {
			switch safeString(variable, "type") {
			case "constant", "textbox":
				if q := safeString(variable, "query"); q != "" {
					variables[name] = []string{q}
				}
			}
		}
	}

	return variables
}

func allTemplateSelectionWarnings(db map[string]interface{}, overrides map[string]string, values templateVariableValues) map[string]string {
	warnings := make(map[string]string)
	const message = "Variable %q is set to All. Supply an explicit value in variables."
	const noValueMessage = "Variable %q has no saved value. Supply an explicit value in variables."
	for _, item := range safeArray(safeObject(db, "templating"), "list") {
		variable, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		name := safeString(variable, "name")
		current := safeObject(variable, "current")
		selection, hasValue := current["value"]
		if override, ok := overrides[name]; ok {
			selection = override
			hasValue = true
		}
		_, resolved := values[name]
		switch {
		case isAllTemplateSelection(selection) || (!hasValue && !resolved && safeString(current, "text") == "All"):
			warnings[name] = fmt.Sprintf(message, name)
		case !resolved && name != "" && safeString(variable, "type") != "datasource":
			// Datasource variables fall back to the default datasource instead.
			warnings[name] = fmt.Sprintf(noValueMessage, name)
		}
	}
	for name, value := range overrides {
		if isAllTemplateSelection(value) {
			warnings[name] = fmt.Sprintf(message, name)
		}
	}
	return warnings
}

func isAllTemplateSelection(value interface{}) bool {
	switch selected := value.(type) {
	case string:
		return selected == "$__all"
	case []interface{}:
		return len(selected) > 0 && selected[0] == "$__all"
	case []string:
		return len(selected) > 0 && selected[0] == "$__all"
	default:
		return false
	}
}
