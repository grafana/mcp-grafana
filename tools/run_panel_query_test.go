//go:build unit

package tools

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/grafana/mcp-grafana/v2/tools/sql"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These test shims preserve the historic single-value helper signatures used
// by the existing substitution and extraction tests. Production code uses the
// multi-value helpers directly so formatted variables can retain all values.
func testTemplateVariableValues(variables map[string]string) templateVariableValues {
	if variables == nil {
		return nil
	}

	result := make(templateVariableValues, len(variables))
	for name, value := range variables {
		result[name] = []string{value}
	}
	return result
}

func substituteTemplateVariables(query string, variables map[string]string) string {
	return newVariableInterpolator(testTemplateVariableValues(variables), nil).interpolate(query)
}

func substituteTemplateVariablesInMap(target map[string]interface{}, variables map[string]string) map[string]interface{} {
	return newVariableInterpolator(testTemplateVariableValues(variables), nil).interpolateTarget(target)
}

func substituteTemplateVariablesInSlice(slice []interface{}, variables map[string]string) []interface{} {
	return newVariableInterpolator(testTemplateVariableValues(variables), nil).interpolateValue(slice).([]interface{})
}

func extractTemplateVariables(db map[string]interface{}) map[string]string {
	in := newDashboardInterpolator(db, nil)
	first := make(map[string]string)
	for name := range in.values {
		if value, ok := in.firstValue(name); ok {
			first[name] = value
		}
	}
	return first
}

func TestFindPanelByIDForRunPanelQuery(t *testing.T) {
	tests := []struct {
		name      string
		dashboard map[string]interface{}
		panelID   int
		wantTitle string
		wantErr   bool
	}{
		{
			name: "find top-level panel",
			dashboard: map[string]interface{}{
				"panels": []interface{}{
					map[string]interface{}{
						"id":    float64(1), // JSON unmarshaling converts numbers to float64
						"title": "Panel 1",
						"type":  "graph",
					},
					map[string]interface{}{
						"id":    float64(2),
						"title": "Panel 2",
						"type":  "stat",
					},
				},
			},
			panelID:   1,
			wantTitle: "Panel 1",
			wantErr:   false,
		},
		{
			name: "find nested panel in row",
			dashboard: map[string]interface{}{
				"panels": []interface{}{
					map[string]interface{}{
						"id":    float64(1),
						"title": "Row 1",
						"type":  "row",
						"panels": []interface{}{
							map[string]interface{}{
								"id":    float64(10),
								"title": "Nested Panel",
								"type":  "graph",
							},
						},
					},
				},
			},
			panelID:   10,
			wantTitle: "Nested Panel",
			wantErr:   false,
		},
		{
			name: "panel not found",
			dashboard: map[string]interface{}{
				"panels": []interface{}{
					map[string]interface{}{
						"id":    float64(1),
						"title": "Panel 1",
						"type":  "graph",
					},
				},
			},
			panelID: 999,
			wantErr: true,
		},
		{
			name:      "no panels",
			dashboard: map[string]interface{}{},
			panelID:   1,
			wantErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			panel, err := findPanelByID(tt.dashboard, tt.panelID)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantTitle, safeString(panel, "title"))
		})
	}
}

func TestExtractPanelInfo(t *testing.T) {
	tests := []struct {
		name        string
		panel       map[string]interface{}
		wantQuery   string
		wantDSUID   string
		wantDSType  string
		wantErr     bool
		errContains string
	}{
		{
			name: "prometheus panel with expr",
			panel: map[string]interface{}{
				"id":    1,
				"title": "CPU Usage",
				"datasource": map[string]interface{}{
					"uid":  "prometheus-uid",
					"type": "prometheus",
				},
				"targets": []interface{}{
					map[string]interface{}{
						"expr": "rate(cpu_usage[5m])",
					},
				},
			},
			wantQuery:  "rate(cpu_usage[5m])",
			wantDSUID:  "prometheus-uid",
			wantDSType: "prometheus",
			wantErr:    false,
		},
		{
			name: "loki panel with expr",
			panel: map[string]interface{}{
				"id":    2,
				"title": "Logs",
				"datasource": map[string]interface{}{
					"uid":  "loki-uid",
					"type": "loki",
				},
				"targets": []interface{}{
					map[string]interface{}{
						"expr": "{job=\"app\"} |= \"error\"",
					},
				},
			},
			wantQuery:  "{job=\"app\"} |= \"error\"",
			wantDSUID:  "loki-uid",
			wantDSType: "loki",
			wantErr:    false,
		},
		{
			name: "datasource from target level",
			panel: map[string]interface{}{
				"id":    3,
				"title": "Panel with target datasource",
				"targets": []interface{}{
					map[string]interface{}{
						"expr": "up",
						"datasource": map[string]interface{}{
							"uid":  "target-ds-uid",
							"type": "prometheus",
						},
					},
				},
			},
			wantQuery:  "up",
			wantDSUID:  "target-ds-uid",
			wantDSType: "prometheus",
			wantErr:    false,
		},
		{
			name: "panel with query field instead of expr",
			panel: map[string]interface{}{
				"id":    4,
				"title": "Generic Query Panel",
				"datasource": map[string]interface{}{
					"uid":  "ds-uid",
					"type": "some-datasource",
				},
				"targets": []interface{}{
					map[string]interface{}{
						"query": "SELECT * FROM table",
					},
				},
			},
			wantQuery:  "SELECT * FROM table",
			wantDSUID:  "ds-uid",
			wantDSType: "some-datasource",
			wantErr:    false,
		},
		{
			name: "panel with no targets",
			panel: map[string]interface{}{
				"id":    5,
				"title": "Empty Panel",
				"datasource": map[string]interface{}{
					"uid":  "ds-uid",
					"type": "prometheus",
				},
			},
			wantErr:     true,
			errContains: "no query targets",
		},
		{
			name: "panel with no query expression",
			panel: map[string]interface{}{
				"id":    6,
				"title": "No Query",
				"datasource": map[string]interface{}{
					"uid":  "ds-uid",
					"type": "prometheus",
				},
				"targets": []interface{}{
					map[string]interface{}{
						"refId": "A",
					},
				},
			},
			wantErr:     true,
			errContains: "could not extract query",
		},
		{
			name: "mixed datasource panel uses target-level datasource",
			panel: map[string]interface{}{
				"id":    10,
				"title": "Mixed Panel",
				"datasource": map[string]interface{}{
					"uid":  "-- Mixed --",
					"type": "datasource",
				},
				"targets": []interface{}{
					map[string]interface{}{
						"expr": "rate(http_requests_total[5m])",
						"datasource": map[string]interface{}{
							"uid":  "prometheus-uid",
							"type": "prometheus",
						},
					},
				},
			},
			wantQuery:  "rate(http_requests_total[5m])",
			wantDSUID:  "prometheus-uid",
			wantDSType: "prometheus",
			wantErr:    false,
		},
		{
			name: "target-level datasource overrides panel-level",
			panel: map[string]interface{}{
				"id":    11,
				"title": "Override Panel",
				"datasource": map[string]interface{}{
					"uid":  "default-prom",
					"type": "prometheus",
				},
				"targets": []interface{}{
					map[string]interface{}{
						"expr": "up",
						"datasource": map[string]interface{}{
							"uid":  "specific-prom",
							"type": "prometheus",
						},
					},
				},
			},
			wantQuery:  "up",
			wantDSUID:  "specific-prom",
			wantDSType: "prometheus",
			wantErr:    false,
		},
		{
			// Grafana runs such a panel on the default datasource (schema 36
			// migrates a null reference to the default datasource's ref).
			name: "panel with no datasource uses the default",
			panel: map[string]interface{}{
				"id":    7,
				"title": "No Datasource",
				"targets": []interface{}{
					map[string]interface{}{
						"expr": "up",
					},
				},
			},
			wantQuery: "up",
			wantDSUID: "default",
		},
		{
			name: "null panel datasource uses the default",
			panel: map[string]interface{}{
				"id":         7,
				"datasource": nil,
				"targets":    []interface{}{map[string]interface{}{"expr": "up"}},
			},
			wantQuery: "up",
			wantDSUID: "default",
		},
		{
			// Dashboards saved before schema 33 reference datasources by name.
			name: "legacy datasource name",
			panel: map[string]interface{}{
				"id":         8,
				"datasource": "Prometheus",
				"targets":    []interface{}{map[string]interface{}{"expr": "up"}},
			},
			wantQuery: "up",
			wantDSUID: "Prometheus",
		},
		{
			name: "legacy target datasource name wins over the panel's",
			panel: map[string]interface{}{
				"id":         9,
				"datasource": "-- Mixed --",
				"targets":    []interface{}{map[string]interface{}{"expr": "up", "datasource": "$ds"}},
			},
			wantQuery: "up",
			wantDSUID: "$ds",
		},
		{
			name: "mixed panel with an unreferenced target",
			panel: map[string]interface{}{
				"id":         10,
				"datasource": "-- Mixed --",
				"targets":    []interface{}{map[string]interface{}{"expr": "up"}},
			},
			wantErr:     true,
			errContains: "could not determine datasource",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info, err := extractPanelInfo(tt.panel, 0) // queryIndex=0 for first query
			if tt.wantErr {
				assert.Error(t, err)
				if tt.errContains != "" {
					assert.Contains(t, err.Error(), tt.errContains)
				}
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantQuery, info.Query)
			assert.Equal(t, tt.wantDSUID, info.DatasourceUID)
			assert.Equal(t, tt.wantDSType, info.DatasourceType)
		})
	}
}

func TestExtractTemplateVariables(t *testing.T) {
	tests := []struct {
		name      string
		dashboard map[string]interface{}
		want      map[string]string
	}{
		{
			name: "single string variable",
			dashboard: map[string]interface{}{
				"templating": map[string]interface{}{
					"list": []interface{}{
						map[string]interface{}{
							"name": "job",
							"current": map[string]interface{}{
								"value": "api-server",
								"text":  "api-server",
							},
						},
					},
				},
			},
			want: map[string]string{
				"job": "api-server",
			},
		},
		{
			name: "multiple variables",
			dashboard: map[string]interface{}{
				"templating": map[string]interface{}{
					"list": []interface{}{
						map[string]interface{}{
							"name": "job",
							"current": map[string]interface{}{
								"value": "api-server",
							},
						},
						map[string]interface{}{
							"name": "namespace",
							"current": map[string]interface{}{
								"value": "production",
							},
						},
					},
				},
			},
			want: map[string]string{
				"job":       "api-server",
				"namespace": "production",
			},
		},
		{
			name: "array value variable (multi-select)",
			dashboard: map[string]interface{}{
				"templating": map[string]interface{}{
					"list": []interface{}{
						map[string]interface{}{
							"name": "instance",
							"current": map[string]interface{}{
								"value": []interface{}{"instance1", "instance2"},
							},
						},
					},
				},
			},
			want: map[string]string{
				"instance": "instance1", // Takes first value
			},
		},
		{
			name: "fallback to text field",
			dashboard: map[string]interface{}{
				"templating": map[string]interface{}{
					"list": []interface{}{
						map[string]interface{}{
							"name": "datasource",
							"current": map[string]interface{}{
								"text": "Prometheus",
							},
						},
					},
				},
			},
			want: map[string]string{
				"datasource": "Prometheus",
			},
		},
		{
			name:      "no templating",
			dashboard: map[string]interface{}{},
			want:      map[string]string{},
		},
		{
			name: "skip All value in text",
			dashboard: map[string]interface{}{
				"templating": map[string]interface{}{
					"list": []interface{}{
						map[string]interface{}{
							"name": "job",
							"current": map[string]interface{}{
								"text": "All",
							},
						},
					},
				},
			},
			want: map[string]string{},
		},
		{
			name: "skip $__all sentinel in string value",
			dashboard: map[string]interface{}{
				"templating": map[string]interface{}{
					"list": []interface{}{
						map[string]interface{}{
							"name": "cluster",
							"current": map[string]interface{}{
								"value": "$__all",
								"text":  "All",
							},
						},
					},
				},
			},
			want: map[string]string{},
		},
		{
			name: "skip $__all sentinel in array value",
			dashboard: map[string]interface{}{
				"templating": map[string]interface{}{
					"list": []interface{}{
						map[string]interface{}{
							"name": "instance",
							"current": map[string]interface{}{
								"value": []interface{}{"$__all"},
								"text":  "All",
							},
						},
					},
				},
			},
			want: map[string]string{},
		},
		{
			name: "constant variable without current falls back to query",
			dashboard: map[string]interface{}{
				"templating": map[string]interface{}{
					"list": []interface{}{
						map[string]interface{}{
							"name":  "conversionRate",
							"type":  "constant",
							"query": "158.87",
						},
					},
				},
			},
			want: map[string]string{
				"conversionRate": "158.87",
			},
		},
		{
			name: "textbox variable without current falls back to query",
			dashboard: map[string]interface{}{
				"templating": map[string]interface{}{
					"list": []interface{}{
						map[string]interface{}{
							"name":  "daysBack",
							"type":  "textbox",
							"query": "30",
						},
					},
				},
			},
			want: map[string]string{
				"daysBack": "30",
			},
		},
		{
			name: "current takes precedence over query for constant",
			dashboard: map[string]interface{}{
				"templating": map[string]interface{}{
					"list": []interface{}{
						map[string]interface{}{
							"name":  "margin",
							"type":  "constant",
							"query": "1",
							"current": map[string]interface{}{
								"value": "1.5",
							},
						},
					},
				},
			},
			want: map[string]string{
				"margin": "1.5",
			},
		},
		{
			name: "query variable does not fall back to its datasource query",
			dashboard: map[string]interface{}{
				"templating": map[string]interface{}{
					"list": []interface{}{
						map[string]interface{}{
							"name":  "job",
							"type":  "query",
							"query": "label_values(up, job)",
						},
					},
				},
			},
			want: map[string]string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractTemplateVariables(tt.dashboard)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestSubstituteTemplateVariables(t *testing.T) {
	tests := []struct {
		name      string
		query     string
		variables map[string]string
		want      string
	}{
		{
			name:      "simple variable substitution",
			query:     "rate(http_requests_total{job=\"$job\"}[5m])",
			variables: map[string]string{"job": "api-server"},
			want:      "rate(http_requests_total{job=\"api-server\"}[5m])",
		},
		{
			name:      "braced variable substitution",
			query:     "rate(http_requests_total{job=\"${job}\"}[5m])",
			variables: map[string]string{"job": "api-server"},
			want:      "rate(http_requests_total{job=\"api-server\"}[5m])",
		},
		{
			name:  "multiple variables",
			query: "{job=\"$job\", namespace=\"$namespace\"}",
			variables: map[string]string{
				"job":       "api-server",
				"namespace": "production",
			},
			want: "{job=\"api-server\", namespace=\"production\"}",
		},
		{
			name:      "no variables to substitute",
			query:     "up{job=\"static\"}",
			variables: map[string]string{"other": "value"},
			want:      "up{job=\"static\"}",
		},
		{
			name:      "avoid partial match",
			query:     "metric{job=\"$job\", jobname=\"$jobname\"}",
			variables: map[string]string{"job": "api"},
			want:      "metric{job=\"api\", jobname=\"$jobname\"}",
		},
		{
			name:      "mixed formats",
			query:     "metric{a=\"$var\", b=\"${var}\"}",
			variables: map[string]string{"var": "value"},
			want:      "metric{a=\"value\", b=\"value\"}",
		},
		{
			name:      "empty variables map",
			query:     "up{job=\"$job\"}",
			variables: map[string]string{},
			want:      "up{job=\"$job\"}",
		},
		{
			name:      "value containing dollar sign",
			query:     "metric{label=\"$var\"}",
			variables: map[string]string{"var": "price$1"},
			want:      "metric{label=\"price$1\"}",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := substituteTemplateVariables(tt.query, tt.variables)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestSubstituteTemplateVariablesSQLString(t *testing.T) {
	query := "SELECT * FROM orders WHERE order_intent_id = ${order_intent_id:sqlstring}"
	variables := map[string]string{"order_intent_id": "oi-123"}

	assert.Equal(t,
		"SELECT * FROM orders WHERE order_intent_id = 'oi-123'",
		substituteTemplateVariables(query, variables),
	)
}

func TestSubstituteTemplateVariableValuesSQLString(t *testing.T) {
	expression := "$" + "{order_intent_id:sqlstring}"
	tests := []struct {
		name      string
		query     string
		variables templateVariableValues
		want      string
	}{
		{
			name:      "single value",
			query:     "WHERE order_intent_id = " + expression,
			variables: templateVariableValues{"order_intent_id": {"oi-123"}},
			want:      "WHERE order_intent_id = 'oi-123'",
		},
		{
			name:      "multiple values",
			query:     "WHERE order_intent_id IN (" + expression + ")",
			variables: templateVariableValues{"order_intent_id": {"oi-123", "oi-456"}},
			want:      "WHERE order_intent_id IN ('oi-123','oi-456')",
		},
		{
			name:      "single quote is doubled",
			query:     "WHERE order_intent_id = " + expression,
			variables: templateVariableValues{"order_intent_id": {"oi'123"}},
			want:      "WHERE order_intent_id = 'oi''123'",
		},
		{
			name:      "double quote is escaped",
			query:     "WHERE order_intent_id = " + expression,
			variables: templateVariableValues{"order_intent_id": {"oi\"123"}},
			want:      "WHERE order_intent_id = 'oi\\\"123'",
		},
		{
			name:      "empty string is quoted",
			query:     "WHERE order_intent_id = " + expression,
			variables: templateVariableValues{"order_intent_id": {""}},
			want:      "WHERE order_intent_id = ''",
		},
		{
			name:      "empty multi-select is empty",
			query:     "WHERE order_intent_id IN (" + expression + ")",
			variables: templateVariableValues{"order_intent_id": {}},
			want:      "WHERE order_intent_id IN ()",
		},
		{
			name:      "unknown format falls back to glob",
			query:     "WHERE order_intent_id IN ($" + "{order_intent_id:unsupported})",
			variables: templateVariableValues{"order_intent_id": {"oi-123", "oi-456"}},
			want:      "WHERE order_intent_id IN ({oi-123,oi-456})",
		},
		{
			name:      "unformatted multi-select keeps historical first value",
			query:     "WHERE order_intent_id = $order_intent_id",
			variables: templateVariableValues{"order_intent_id": {"oi-123", "oi-456"}},
			want:      "WHERE order_intent_id = oi-123",
		},
		{
			name:      "missing variable remains unchanged",
			query:     "WHERE order_intent_id = " + expression,
			variables: templateVariableValues{},
			want:      "WHERE order_intent_id = " + expression,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, newVariableInterpolator(tt.variables, nil).interpolate(tt.query))
		})
	}
}

func TestRunPanelQueryParams(t *testing.T) {
	// Test that the params struct has the expected fields
	params := RunPanelQueryParams{
		DashboardUID: "test-uid",
		PanelIDs:     []int{5},
		Start:        "now-1h",
		End:          "now",
		Variables: map[string]string{
			"job": "api-server",
		},
	}

	assert.Equal(t, "test-uid", params.DashboardUID)
	assert.Equal(t, []int{5}, params.PanelIDs)
	assert.Equal(t, "now-1h", params.Start)
	assert.Equal(t, "now", params.End)
	assert.Equal(t, "api-server", params.Variables["job"])
}

func TestRunPanelQueryResult(t *testing.T) {
	// Test that the result struct has the expected fields
	result := RunPanelQueryResult{
		DashboardUID: "test-uid",
		Results: map[int]*PanelQueryResult{
			5: {
				PanelID:        5,
				PanelTitle:     "Test Panel",
				DatasourceType: "prometheus",
				DatasourceUID:  "prom-uid",
				Query:          "rate(http_requests_total[5m])",
				Results:        []interface{}{"sample data"},
			},
		},
		TimeRange: QueryTimeRange{
			Start: "now-1h",
			End:   "now",
		},
	}

	assert.Equal(t, "test-uid", result.DashboardUID)
	assert.Contains(t, result.Results, 5)
	assert.Equal(t, "Test Panel", result.Results[5].PanelTitle)
	assert.Equal(t, "prometheus", result.Results[5].DatasourceType)
	assert.Equal(t, "prom-uid", result.Results[5].DatasourceUID)
	assert.Equal(t, "rate(http_requests_total[5m])", result.Results[5].Query)
	assert.Equal(t, "now-1h", result.TimeRange.Start)
	assert.Equal(t, "now", result.TimeRange.End)
}

func TestQueryTimeRange(t *testing.T) {
	tr := QueryTimeRange{
		Start: "2024-01-01T00:00:00Z",
		End:   "2024-01-01T01:00:00Z",
	}

	assert.Equal(t, "2024-01-01T00:00:00Z", tr.Start)
	assert.Equal(t, "2024-01-01T01:00:00Z", tr.End)
}

func TestIsVariableReference(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"$datasource", true},
		{"${datasource}", true},
		{"[[datasource]]", true},
		{"prometheus-uid", false},
		{"", false},
		{"abc$def", false}, // $ not at start
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := isVariableReference(tt.input)
			assert.Equal(t, tt.want, got, "isVariableReference(%q)", tt.input)
		})
	}
}

func TestExtractVariableName(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"$datasource", "datasource"},
		{"${datasource}", "datasource"},
		{"[[datasource]]", "datasource"},
		{"$ds", "ds"},
		{"${ds}", "ds"},
		{"[[ds]]", "ds"},
		{"prometheus-uid", "prometheus-uid"}, // Not a variable, returns as-is
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := extractVariableName(tt.input)
			assert.Equal(t, tt.want, got, "extractVariableName(%q)", tt.input)
		})
	}
}

func TestSubstituteGrafanaMacros(t *testing.T) {
	tests := []struct {
		name     string
		query    string
		duration time.Duration
		expected string
	}{
		{
			name:     "range_14_minutes",
			query:    "increase(requests[$__range])",
			duration: 14 * time.Minute,
			expected: "increase(requests[14m])",
		},
		{
			name:     "range_1_hour",
			query:    "increase(requests[$__range])",
			duration: time.Hour,
			expected: "increase(requests[1h])",
		},
		{
			name:     "range_90_minutes",
			query:    "increase(requests[$__range])",
			duration: 90 * time.Minute,
			expected: "increase(requests[1h30m])",
		},
		{
			name:     "range_keeps_seconds",
			query:    "increase(requests[$__range])",
			duration: 90 * time.Second,
			expected: "increase(requests[1m30s])",
		},
		{
			name:     "range_keeps_minutes_and_seconds",
			query:    "increase(requests[$__range])",
			duration: 2*time.Hour + 30*time.Minute + 45*time.Second,
			expected: "increase(requests[2h30m45s])",
		},
		{
			name:     "interval_matches_interval_ms",
			query:    "rate(requests[$__interval]) * $__interval_ms",
			duration: 3 * time.Hour,
			expected: "rate(requests[2m]) * 120000", // 10800/100 = 108s, rounded to 2m
		},
		{
			name:     "rate_interval",
			query:    "rate(requests[$__rate_interval])",
			duration: time.Hour,
			expected: "rate(requests[1m])",
		},
		{
			name:     "rate_interval_ms_not_corrupted_by_rate_interval",
			query:    "rate(x[$__rate_interval]) * $__rate_interval_ms",
			duration: time.Hour,
			expected: "rate(x[1m]) * 60000",
		},
		{
			name:     "rate_interval_ms_braced",
			query:    "timestamp(up) - ${__rate_interval_ms}",
			duration: time.Hour,
			expected: "timestamp(up) - 60000",
		},
		{
			name:     "interval_1_hour",
			query:    "rate(requests[$__interval])",
			duration: time.Hour,
			expected: "rate(requests[30s])", // 3600/100 = 36s, rounded to 30s
		},
		{
			name:     "interval_braced",
			query:    "rate(requests[${__interval}])",
			duration: time.Hour,
			expected: "rate(requests[30s])",
		},
		{
			name:     "auto",
			query:    `count_over_time({job="x"}[$__auto])`,
			duration: time.Hour,
			expected: `count_over_time({job="x"}[30s])`,
		},
		{
			name:     "auto_braced",
			query:    `count_over_time({job="x"}[${__auto}])`,
			duration: time.Hour,
			expected: `count_over_time({job="x"}[30s])`,
		},
		{
			name:     "interval_ms",
			query:    "timestamp(up) - $__interval_ms",
			duration: time.Hour,
			expected: "timestamp(up) - 30000", // 30s = 30000ms
		},
		{
			name:     "multiple_macros",
			query:    "increase(x[$__range]) / rate(y[$__rate_interval])",
			duration: 30 * time.Minute,
			expected: "increase(x[30m]) / rate(y[1m])",
		},
		{
			name:     "no_macros",
			query:    "up{job='api'}",
			duration: time.Hour,
			expected: "up{job='api'}",
		},
		{
			name:     "short_duration_sub_second",
			query:    "rate(x[$__interval])",
			duration: 30 * time.Second, // 30s/100 = 0.3s, rounded to 200ms
			expected: "rate(x[200ms])",
		},
		{
			name:     "range_s_macro",
			query:    "increase(requests[$__range_s])",
			duration: time.Hour,
			expected: "increase(requests[3600])",
		},
		{
			name:     "range_ms_macro",
			query:    "timestamp(up) - $__range_ms",
			duration: time.Hour,
			expected: "timestamp(up) - 3600000",
		},
		{
			name:     "range_s_and_ms_not_corrupted_by_range",
			query:    "increase(x[$__range]) + $__range_s + $__range_ms",
			duration: 30 * time.Minute,
			expected: "increase(x[30m]) + 1800 + 1800000",
		},
		{
			name:     "range_braced",
			query:    "increase(requests[${__range}])",
			duration: 14 * time.Minute,
			expected: "increase(requests[14m])",
		},
		{
			name:     "rate_interval_braced",
			query:    "rate(requests[${__rate_interval}])",
			duration: time.Hour,
			expected: "rate(requests[1m])",
		},
		{
			name:     "range_ms_braced",
			query:    "timestamp(up) - ${__range_ms}",
			duration: time.Hour,
			expected: "timestamp(up) - 3600000",
		},
		{
			name:     "interval_ms_braced",
			query:    "timestamp(up) - ${__interval_ms}",
			duration: time.Hour,
			expected: "timestamp(up) - 30000",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			start := time.Now()
			end := start.Add(tt.duration)
			result := substituteGrafanaMacros(tt.query, start, end, intervalOptions{})
			assert.Equal(t, tt.expected, result)
		})
	}
}

// Expected values come from Grafana's rangeutil.calculateInterval with 100 data points.
func TestSubstituteGrafanaMacrosRoundsInterval(t *testing.T) {
	day := 24 * time.Hour
	tests := []struct {
		span     time.Duration
		interval string
		ms       string
	}{
		{30 * time.Second, "200ms", "200"},
		{5 * time.Minute, "2s", "2000"},
		{14 * time.Minute, "10s", "10000"},
		{30 * time.Minute, "20s", "20000"},
		{time.Hour, "30s", "30000"},
		{3 * time.Hour, "2m", "120000"},
		{6 * time.Hour, "5m", "300000"},
		{day, "15m", "900000"},
		{7 * day, "2h", "7200000"},
		{30 * day, "6h", "21600000"},
		{90 * day, "12h", "43200000"},
		{365 * day, "1d", "86400000"},
		{730 * day, "7d", "604800000"},
	}
	start := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)
	for _, tt := range tests {
		t.Run(tt.span.String(), func(t *testing.T) {
			got := substituteGrafanaMacros("$__interval ${__interval_ms} $__auto", start, start.Add(tt.span), intervalOptions{})
			assert.Equal(t, tt.interval+" "+tt.ms+" "+tt.interval, got)
		})
	}
}

// Expected values come from Grafana's rangeutil.calculateInterval(range, maxDataPoints, lowerLimit).
func TestSubstituteGrafanaMacrosIntervalOptions(t *testing.T) {
	tests := []struct {
		name    string
		span    time.Duration
		options intervalOptions
		want    string
	}{
		{"fallback data points", time.Hour, intervalOptions{}, "30s 30000"},
		{"panel max data points", time.Hour, intervalOptions{maxDataPoints: 1000}, "5s 5000"},
		{"panel max data points short range", 5 * time.Minute, intervalOptions{maxDataPoints: 1000}, "200ms 200"},
		{"min interval raises", time.Hour, intervalOptions{minInterval: "1m"}, "1m 60000"},
		{"min interval below calculated", time.Hour, intervalOptions{minInterval: "10s"}, "30s 30000"},
		{"prometheus 15s on 5m", 5 * time.Minute, intervalOptions{minInterval: "15s"}, "15s 15000"},
		{"both", time.Hour, intervalOptions{maxDataPoints: 1000, minInterval: "10s"}, "10s 10000"},
	}
	start := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := substituteGrafanaMacros("$__interval ${__interval_ms}", start, start.Add(tt.span), tt.options)
			assert.Equal(t, tt.want, got)
		})
	}
}

// Precedence follows SceneQueryRunner.prepareRequests: the panel's min interval,
// else the datasource's (Prometheus timeInterval or 15s, SQL timeInterval or 1m).
func TestIntervalOptionsLowerLimit(t *testing.T) {
	tests := []struct {
		name          string
		panelInterval string
		dsType        string
		jsonData      interface{}
		want          string
	}{
		{"prometheus default", "", "prometheus", nil, "15s"},
		{"prometheus scrape interval", "", "prometheus", map[string]interface{}{"timeInterval": "30s"}, "30s"},
		{"panel beats datasource", "1m", "prometheus", map[string]interface{}{"timeInterval": "30s"}, "1m"},
		{"panel variable", "$min", "prometheus", nil, "2m"},
		{"postgres default", "", "grafana-postgresql-datasource", nil, "1m"},
		{"mysql time interval", "", "mysql", map[string]interface{}{"timeInterval": "5m"}, "5m"},
		{"mssql default", "", "mssql", nil, "1m"},
		{"loki has none", "", "loki", map[string]interface{}{"timeInterval": "30s"}, ""},
		{"cloudwatch has none", "", "cloudwatch", nil, ""},
		{"stackdriver is not prometheus", "", "stackdriver", nil, ""},
		{"panel on loki", "10s", "loki", nil, "10s"},
	}
	interp := newVariableInterpolator(templateVariableValues{"min": {"2m"}}, nil)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := intervalOptions{maxDataPoints: 500, minInterval: tt.panelInterval}.withLowerLimit(interp, tt.dsType, tt.jsonData)
			assert.Equal(t, intervalOptions{maxDataPoints: 500, minInterval: tt.want}, got)
		})
	}
}

func TestPanelIntervalOptions(t *testing.T) {
	v1 := map[string]interface{}{"maxDataPoints": float64(1000), "interval": "1m"}
	assert.Equal(t, intervalOptions{maxDataPoints: 1000, minInterval: "1m"}, panelIntervalOptions(v1))

	v2 := map[string]interface{}{"data": map[string]interface{}{"spec": map[string]interface{}{
		"queryOptions": map[string]interface{}{"maxDataPoints": float64(800), "interval": "30s"},
	}}}
	assert.Equal(t, intervalOptions{maxDataPoints: 800, minInterval: "30s"}, panelIntervalOptionsV2(v2))

	assert.Equal(t, intervalOptions{}, panelIntervalOptions(map[string]interface{}{"maxDataPoints": nil}))
}

func TestFormatPrometheusDuration(t *testing.T) {
	tests := []struct {
		name     string
		duration time.Duration
		expected string
	}{
		{"seconds", 45 * time.Second, "45s"},
		{"one_minute", time.Minute, "1m"},
		{"minutes", 14 * time.Minute, "14m"},
		{"one_hour", time.Hour, "1h"},
		{"hours_only", 3 * time.Hour, "3h"},
		{"hours_and_minutes", 90 * time.Minute, "1h30m"},
		{"complex", 2*time.Hour + 15*time.Minute, "2h15m"},
		{"minutes_and_seconds", 90 * time.Second, "1m30s"},
		{"hours_minutes_and_seconds", time.Hour + 30*time.Second, "1h30s"},
		{"over_a_day", 25*time.Hour + 36*time.Second, "25h36s"},
		{"zero", 0, "0s"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := formatPrometheusDuration(tt.duration)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestIsEmptyPanelResult(t *testing.T) {
	tests := []struct {
		name     string
		results  interface{}
		expected bool
	}{
		{
			name:     "nil result",
			results:  nil,
			expected: true,
		},
		{
			name:     "empty interface slice",
			results:  []interface{}{},
			expected: true,
		},
		{
			name:     "non-empty interface slice",
			results:  []interface{}{"data"},
			expected: false,
		},
		{
			name:     "empty LogEntry slice",
			results:  []LogEntry{},
			expected: true,
		},
		{
			name: "non-empty LogEntry slice",
			results: []LogEntry{
				{Timestamp: "2024-01-01T00:00:00Z", Line: "test"},
			},
			expected: false,
		},
		{
			name:     "nil sql.SQLQueryResult",
			results:  (*sql.SQLQueryResult)(nil),
			expected: true,
		},
		{
			name:     "empty sql.SQLQueryResult",
			results:  &sql.SQLQueryResult{Rows: []map[string]interface{}{}},
			expected: true,
		},
		{
			name: "non-empty sql.SQLQueryResult",
			results: &sql.SQLQueryResult{
				Rows: []map[string]interface{}{{"value": 1}},
			},
			expected: false,
		},
		{
			name:     "nil InfluxDBQueryResult",
			results:  (*InfluxDBQueryResult)(nil),
			expected: true,
		},
		{
			name:     "empty InfluxDBQueryResult",
			results:  &InfluxDBQueryResult{Rows: []map[string]interface{}{}},
			expected: true,
		},
		{
			name: "non-empty InfluxDBQueryResult",
			results: &InfluxDBQueryResult{
				Rows: []map[string]interface{}{{"_value": 1.5}},
			},
			expected: false,
		},
		{
			name:     "nil sql.SQLQueryResult",
			results:  (*sql.SQLQueryResult)(nil),
			expected: true,
		},
		{
			name:     "empty sql.SQLQueryResult",
			results:  &sql.SQLQueryResult{Rows: []map[string]interface{}{}},
			expected: true,
		},
		{
			name: "non-empty sql.SQLQueryResult",
			results: &sql.SQLQueryResult{
				Rows: []map[string]interface{}{{"count": 42}},
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isEmptyPanelResult(tt.results)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestGeneratePanelQueryHints(t *testing.T) {
	tests := []struct {
		name           string
		datasourceType string
		query          string
		containsHints  []string
	}{
		{
			name:           "prometheus hints",
			datasourceType: "prometheus",
			query:          "rate(http_requests[5m])",
			containsHints: []string{
				"Prometheus query returned no data",
				"time range",
				"list_prometheus_metric_names",
				"target health",
			},
		},
		{
			name:           "loki hints",
			datasourceType: "loki",
			query:          "{job=\"app\"} |= \"error\"",
			containsHints: []string{
				"Loki query returned no log entries",
				"time range",
				"list_loki_label_names",
				"query_loki_stats",
			},
		},
		{
			name:           "clickhouse hints",
			datasourceType: "grafana-clickhouse-datasource",
			query:          "SELECT * FROM logs WHERE Body ILIKE '%error%'",
			containsHints: []string{
				"No data found",
				"Time range",
				"describe_sql_table",
				"COUNT(*)",
			},
		},
		{
			name:           "includes query in hints",
			datasourceType: "prometheus",
			query:          "up{job=\"api\"}",
			containsHints: []string{
				"Query executed:",
				"up{job=\"api\"}",
			},
		},
		{
			name:           "influxdb hints",
			datasourceType: "influxdb",
			query:          `from(bucket: "metrics") |> range(start: -1h)`,
			containsHints: []string{
				"No data found",
				"Bucket",
				"Measurement",
			},
		},
		{
			name:           "bigquery hints",
			datasourceType: "grafana-bigquery-datasource",
			query:          "SELECT COUNT(*) FROM `proj.ds.tbl` WHERE $__timeFilter(ts)",
			containsHints: []string{
				"No data found",
				"Time range",
				"COUNT(*)",
				"processing location",
			},
		},
		{
			name:           "mssql hints",
			datasourceType: "mssql",
			query:          "SELECT COUNT(*) FROM revenue WHERE $__timeFilter(ts)",
			containsHints: []string{
				"No data found",
				"Time range",
				"COUNT(*)",
				"SELECT permission",
			},
		},
		{
			name:           "postgres hints (modern identifier)",
			datasourceType: "grafana-postgresql-datasource",
			query:          "SELECT COUNT(*) FROM revenue WHERE $__timeFilter(ts)",
			containsHints: []string{
				"No data found",
				"Time range",
				"COUNT(*)",
				"PostgreSQL",
				"search_path",
			},
		},
		{
			name:           "postgres hints (legacy identifier)",
			datasourceType: "postgres",
			query:          "SELECT COUNT(*) FROM revenue WHERE $__timeFilter(ts)",
			containsHints: []string{
				"No data found",
				"PostgreSQL",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hints := generatePanelQueryHints(tt.datasourceType, tt.query)
			hintsStr := ""
			for _, h := range hints {
				hintsStr += h + " "
			}
			for _, expected := range tt.containsHints {
				assert.Contains(t, hintsStr, expected)
			}
		})
	}
}

func TestGeneratePanelQueryHintsGroups(t *testing.T) {
	for _, tc := range []struct {
		datasource, query string
		want              []string
	}{
		{
			datasource: "prometheus",
			query:      "up",
			want: []string{
				"The Prometheus query returned no data for the specified time range.",
				"Possible causes:",
				"- The metric, labels, time range or scrape target may have no data.",
				"Try:",
				"- Check metrics/labels with list_prometheus_metric_names and list_prometheus_label_values.",
				"- Expand the time range to the last 6h or 24h.",
				"- Check scrape target health with up{job=\"...\"}.",
				"- Query executed: up",
			},
		},
		{
			datasource: "loki",
			query:      `{job="app"}`,
			want: []string{
				"The Loki query returned no log entries for the specified time range.",
				"Possible causes:",
				"- The stream selector, time range or filters may not match available logs.",
				"Try:",
				"- Check labels with list_loki_label_names and list_loki_label_values.",
				"- Check the stream selector with query_loki_stats.",
				"- Expand the time range to the last 6h or 24h.",
				`- Query executed: {job="app"}`,
			},
		},
	} {
		t.Run(tc.datasource, func(t *testing.T) {
			require.Equal(t, tc.want, generatePanelQueryHints(tc.datasource, tc.query))
		})
	}
}

func TestTruncateString(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		maxLen   int
		expected string
	}{
		{
			name:     "short string unchanged",
			input:    "short",
			maxLen:   10,
			expected: "short",
		},
		{
			name:     "exact length unchanged",
			input:    "exactly10!",
			maxLen:   10,
			expected: "exactly10!",
		},
		{
			name:     "long string truncated",
			input:    "this is a very long string that should be truncated",
			maxLen:   20,
			expected: "this is a very lo...",
		},
		{
			name:     "empty string",
			input:    "",
			maxLen:   10,
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := truncateString(tt.input, tt.maxLen)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestNormalizeDatasourceType(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"prometheus", "prometheus"},
		{"Prometheus", "prometheus"},
		{"PROMETHEUS", "prometheus"},
		{"grafana-amazonprometheus-datasource", "prometheus"},
		{"grafana-azureprometheus-datasource", "prometheus"},
		{"loki", "loki"},
		{"Loki", "loki"},
		{"cloudwatch", "cloudwatch"},
		{"CloudWatch", "cloudwatch"},
		{"grafana-clickhouse-datasource", "clickhouse"},
		{"clickhouse", "clickhouse"},
		{"ClickHouse", "clickhouse"},
		{"influxdb", "influxdb"},
		{"InfluxDB", "influxdb"},
		{"grafana-bigquery-datasource", "bigquery"},
		{"mssql", "mssql"},
		{"MSSQL", "mssql"},
		{"bigquery", "bigquery"},
		{"BigQuery", "bigquery"},
		{"postgres", "postgres"},
		{"Postgres", "postgres"},
		{"grafana-postgresql-datasource", "postgres"},
		{"Grafana-PostgreSQL-Datasource", "postgres"},
		{"some-other-type", "some-other-type"},
		{"", ""},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := normalizeDatasourceType(tt.input)
			assert.Equal(t, tt.want, got, "normalizeDatasourceType(%q)", tt.input)
		})
	}
}

func TestRunPanelQueryResult_WithHints(t *testing.T) {
	// Test that hints field is properly included in per-panel result
	result := RunPanelQueryResult{
		DashboardUID: "test-uid",
		Results: map[int]*PanelQueryResult{
			5: {
				PanelID:        5,
				PanelTitle:     "Empty Panel",
				DatasourceType: "prometheus",
				DatasourceUID:  "prom-uid",
				Query:          "nonexistent_metric",
				Results:        []interface{}{},
				Hints: []string{
					"No data found for the panel query.",
					"- Time range may have no data",
				},
			},
		},
		TimeRange: QueryTimeRange{
			Start: "now-1h",
			End:   "now",
		},
	}

	panelResult := result.Results[5]
	assert.NotNil(t, panelResult.Hints)
	assert.Len(t, panelResult.Hints, 2)
	assert.Contains(t, panelResult.Hints[0], "No data found")
}

func TestExtractPanelInfo_CloudWatch(t *testing.T) {
	tests := []struct {
		name       string
		panel      map[string]interface{}
		wantDSUID  string
		wantDSType string
		wantErr    bool
	}{
		{
			name: "cloudwatch panel with structured target (no expr)",
			panel: map[string]interface{}{
				"id":    1,
				"title": "CloudWatch CPU",
				"datasource": map[string]interface{}{
					"uid":  "cloudwatch-uid",
					"type": "cloudwatch",
				},
				"targets": []interface{}{
					map[string]interface{}{
						"namespace":  "AWS/ECS",
						"metricName": "CPUUtilization",
						"dimensions": map[string]interface{}{
							"ClusterName": []interface{}{"my-cluster"},
						},
						"region": "us-east-1",
						"refId":  "A",
					},
				},
			},
			wantDSUID:  "cloudwatch-uid",
			wantDSType: "cloudwatch",
			wantErr:    false,
		},
		{
			name: "cloudwatch panel stores raw target",
			panel: map[string]interface{}{
				"id":    2,
				"title": "CloudWatch Memory",
				"datasource": map[string]interface{}{
					"uid":  "cloudwatch-uid",
					"type": "cloudwatch",
				},
				"targets": []interface{}{
					map[string]interface{}{
						"namespace":  "AWS/ECS",
						"metricName": "MemoryUtilization",
						"refId":      "B",
					},
				},
			},
			wantDSUID:  "cloudwatch-uid",
			wantDSType: "cloudwatch",
			wantErr:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info, err := extractPanelInfo(tt.panel, 0)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantDSUID, info.DatasourceUID)
			assert.Equal(t, tt.wantDSType, info.DatasourceType)
			// CloudWatch panels should have empty Query but valid RawTarget
			assert.Empty(t, info.Query, "CloudWatch panels should have empty query string")
			assert.NotNil(t, info.RawTarget, "CloudWatch panels should have RawTarget set")
			assert.NotEmpty(t, info.RawTarget["namespace"], "RawTarget should contain CloudWatch fields")
		})
	}
}

func TestExtractPanelInfo_BigQuery(t *testing.T) {
	panel := map[string]interface{}{
		"id":    1,
		"title": "BigQuery Failed Tests",
		"datasource": map[string]interface{}{
			"uid":  "bigquery-uid",
			"type": "grafana-bigquery-datasource",
		},
		"targets": []interface{}{
			map[string]interface{}{
				"refId":    "A",
				"rawSql":   "SELECT count(*) FROM `proj.ds.tbl` WHERE $__timeFilter(ts)",
				"location": "US",
				"format":   float64(1),
			},
		},
	}

	info, err := extractPanelInfo(panel, 0)
	require.NoError(t, err)
	assert.Equal(t, "bigquery-uid", info.DatasourceUID)
	assert.Equal(t, "grafana-bigquery-datasource", info.DatasourceType)
	// BigQuery uses rawSql, which extractQueryExpression should pick up.
	assert.Equal(t, "SELECT count(*) FROM `proj.ds.tbl` WHERE $__timeFilter(ts)", info.Query)
	// The raw target is preserved so BigQuery-specific fields (e.g. location) survive.
	assert.Equal(t, "US", info.RawTarget["location"])
}

func TestExecuteSQLPanelQuery_NilTarget(t *testing.T) {
	// A missing raw target should produce a clear error rather than panicking.
	_, err := executeSQLPanelQuery(t.Context(), "bigquery-uid", &panelInfo{}, "SELECT 1", "now-1h", "now", nil, sql.BigQueryDatasourceType)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "SQL panel target not available")

	_, err = executeSQLPanelQuery(t.Context(), "mssql-uid", &panelInfo{}, "SELECT 1", "now-1h", "now", nil, sql.MSSQLDatasourceType)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "SQL panel target not available")

	_, err = executeSQLPanelQuery(t.Context(), "postgres-uid", &panelInfo{}, "SELECT 1", "now-1h", "now", nil, sql.PostgresDatasourceType)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "SQL panel target not available")
}

func TestDefaultSQLFormat(t *testing.T) {
	// BigQuery's sqlds query model takes a numeric format enum, MSSQL's sqleng model
	// unmarshals format into a string and errors on a number.
	assert.Equal(t, SQLFormatTable, defaultSQLFormat(sql.BigQueryDatasourceType))
	assert.Equal(t, MSSQLFormatTable, defaultSQLFormat(sql.MSSQLDatasourceType))
	// PostgreSQL is also sqleng-based, so it uses the string table format under
	// both its modern and legacy datasource identifiers.
	assert.Equal(t, MSSQLFormatTable, defaultSQLFormat(sql.PostgresDatasourceType))
	assert.Equal(t, MSSQLFormatTable, defaultSQLFormat(sql.PostgresLegacyDatasourceType))
}

func TestExtractPanelInfoMSSQL(t *testing.T) {
	// MSSQL panels carry their statement in rawSql, like the other SQL datasources,
	// and the raw target must survive so format is not overridden.
	panel := map[string]interface{}{
		"id":    81,
		"title": "Revenue",
		"datasource": map[string]interface{}{
			"uid":  "mssql-uid",
			"type": "mssql",
		},
		"targets": []interface{}{
			map[string]interface{}{
				"refId":  "A",
				"rawSql": "DECLARE @From datetime = $__timeFrom(); SELECT SUM(amount) FROM revenue WHERE ts >= @From",
				"format": "table",
			},
		},
	}

	info, err := extractPanelInfo(panel, 0)
	require.NoError(t, err)
	assert.Equal(t, "mssql-uid", info.DatasourceUID)
	assert.Equal(t, "mssql", info.DatasourceType)
	assert.Contains(t, info.Query, "$__timeFrom()")
	assert.Equal(t, "table", info.RawTarget["format"])
}

func TestExtractPanelInfoPostgres(t *testing.T) {
	// PostgreSQL panels carry their statement in rawSql like the other SQL
	// datasources, and the raw target must survive so datasource-specific fields
	// (e.g. format) and the format override are preserved.
	tests := []struct {
		name       string
		dsType     string
		wantDSType string
	}{
		{name: "modern identifier", dsType: "grafana-postgresql-datasource", wantDSType: "grafana-postgresql-datasource"},
		{name: "legacy identifier", dsType: "postgres", wantDSType: "postgres"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			panel := map[string]interface{}{
				"id":    91,
				"title": "Reconciliation",
				"datasource": map[string]interface{}{
					"uid":  "postgres-uid",
					"type": tt.dsType,
				},
				"targets": []interface{}{
					map[string]interface{}{
						"refId":  "A",
						"rawSql": "SELECT $__timeGroup(ts, '1h') AS t, count(*) FROM checks WHERE $__timeFilter(ts) GROUP BY 1",
						"format": "table",
					},
				},
			}

			info, err := extractPanelInfo(panel, 0)
			require.NoError(t, err)
			assert.Equal(t, "postgres-uid", info.DatasourceUID)
			assert.Equal(t, tt.wantDSType, info.DatasourceType)
			assert.Contains(t, info.Query, "$__timeGroup(ts, '1h')")
			assert.Contains(t, info.Query, "$__timeFilter(ts)")
			// The raw target survives so the panel's saved format is preserved.
			assert.Equal(t, "table", info.RawTarget["format"])
		})
	}
}

func TestSubstituteTemplateVariablesInMap(t *testing.T) {
	tests := []struct {
		name      string
		target    map[string]interface{}
		variables map[string]string
		checkKey  string
		wantValue string
	}{
		{
			name: "substitute string value",
			target: map[string]interface{}{
				"namespace": "$namespace",
			},
			variables: map[string]string{"namespace": "AWS/ECS"},
			checkKey:  "namespace",
			wantValue: "AWS/ECS",
		},
		{
			name: "substitute braced variable",
			target: map[string]interface{}{
				"region": "${region}",
			},
			variables: map[string]string{"region": "us-west-2"},
			checkKey:  "region",
			wantValue: "us-west-2",
		},
		{
			name: "preserve non-variable strings",
			target: map[string]interface{}{
				"metricName": "CPUUtilization",
			},
			variables: map[string]string{"other": "value"},
			checkKey:  "metricName",
			wantValue: "CPUUtilization",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := substituteTemplateVariablesInMap(tt.target, tt.variables)
			assert.Equal(t, tt.wantValue, result[tt.checkKey])
		})
	}
}

func TestSubstituteTemplateVariablesInSlice(t *testing.T) {
	variables := map[string]string{"cluster": "my-cluster"}
	slice := []interface{}{"$cluster", "static-value"}

	result := substituteTemplateVariablesInSlice(slice, variables)

	assert.Equal(t, "my-cluster", result[0])
	assert.Equal(t, "static-value", result[1])
}

// executeLokiAutoQuery runs a $__auto count query, expanded as runSinglePanelQuery does.
func executeLokiAutoQuery(t *testing.T, server *httptest.Server, start, end string, options intervalOptions) error {
	t.Helper()
	from, err := parseTime(start)
	require.NoError(t, err)
	to, err := parseTime(end)
	require.NoError(t, err)
	query := substituteGrafanaMacros(`sum(count_over_time({app="a"}[$__auto]))`, from, to, options)
	_, err = executeLokiQuery(enforceTestCtx(server, false), "loki", query, start, end, options)
	return err
}

// Grafana replaces $__auto with the step of a Loki range query, so the step sent to Loki must
// match the value substituted for $__auto. Otherwise Loki falls back to range/250 and
// count_over_time windows overlap, counting each line more than once.
func TestExecuteLokiQuerySendsAutoAsStep(t *testing.T) {
	server, payload := lokiQueryStub()
	defer server.Close()

	err := executeLokiAutoQuery(t, server, "2024-01-15T10:00:00Z", "2024-01-15T11:00:00Z", intervalOptions{})
	require.NoError(t, err)
	q := (*payload)["queries"].([]interface{})[0].(map[string]interface{})
	assert.Equal(t, `sum(count_over_time({app="a"}[30s]))`, q["expr"])
	assert.Equal(t, "30s", q["step"], "step must equal $__auto")
}

// Over 3h the interval rounds to 2m; the step must match it.
func TestExecuteLokiQueryStepMatchesFormattedAuto(t *testing.T) {
	server, payload := lokiQueryStub()
	defer server.Close()

	err := executeLokiAutoQuery(t, server, "2024-01-15T10:00:00Z", "2024-01-15T13:00:00Z", intervalOptions{})
	require.NoError(t, err)
	q := (*payload)["queries"].([]interface{})[0].(map[string]interface{})
	assert.Equal(t, `sum(count_over_time({app="a"}[2m]))`, q["expr"])
	assert.Equal(t, "120s", q["step"], "step must equal $__auto")
}

// Over 24h the interval rounds to 15m; the step must match it.
func TestExecuteLokiQueryStepMatchesFormattedAuto24h(t *testing.T) {
	server, payload := lokiQueryStub()
	defer server.Close()

	err := executeLokiAutoQuery(t, server, "2024-01-15T00:00:00Z", "2024-01-16T00:00:00Z", intervalOptions{})
	require.NoError(t, err)
	q := (*payload)["queries"].([]interface{})[0].(map[string]interface{})
	assert.Equal(t, `sum(count_over_time({app="a"}[15m]))`, q["expr"])
	assert.Equal(t, "900s", q["step"], "step must equal $__auto")
}

// Under 150s the interval is sub-second; Grafana's Loki backend sends it as the step unchanged.
func TestExecuteLokiQuerySubSecondStep(t *testing.T) {
	server, payload := lokiQueryStub()
	defer server.Close()

	err := executeLokiAutoQuery(t, server, "2024-01-15T10:00:00Z", "2024-01-15T10:00:30Z", intervalOptions{})
	require.NoError(t, err)
	q := (*payload)["queries"].([]interface{})[0].(map[string]interface{})
	assert.Equal(t, `sum(count_over_time({app="a"}[200ms]))`, q["expr"])
	assert.Equal(t, "200ms", q["step"], "step must equal $__auto")
}

// The panel's max data points shrink $__auto, and the step follows it.
func TestExecuteLokiQueryStepFollowsPanelMaxDataPoints(t *testing.T) {
	server, payload := lokiQueryStub()
	defer server.Close()

	err := executeLokiAutoQuery(t, server, "2024-01-15T10:00:00Z", "2024-01-15T11:00:00Z", intervalOptions{maxDataPoints: 1000})
	require.NoError(t, err)
	q := (*payload)["queries"].([]interface{})[0].(map[string]interface{})
	assert.Equal(t, `sum(count_over_time({app="a"}[5s]))`, q["expr"])
	assert.Equal(t, "5s", q["step"], "step must equal $__auto")
}

func TestSubstituteGrafanaMacrosPreservesLongerNames(t *testing.T) {
	start := time.Date(2026, 10, 30, 0, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	for _, name := range []string{"__interval_env", "__range_bucket", "__auto_mode", "__rate_interval_custom", "__interval_ms2", "__interval_ENV"} {
		query := "$" + name + " ${" + name + "} $__interval"
		assert.Equal(t, "$"+name+" ${"+name+"} 30s", substituteGrafanaMacros(query, start, end, intervalOptions{}))
	}
}
