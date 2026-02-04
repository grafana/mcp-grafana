//go:build unit

package tools

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFindPanelByID(t *testing.T) {
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
			name: "panel with no datasource",
			panel: map[string]interface{}{
				"id":    7,
				"title": "No Datasource",
				"targets": []interface{}{
					map[string]interface{}{
						"expr": "up",
					},
				},
			},
			wantErr:     true,
			errContains: "could not determine datasource",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info, err := extractPanelInfo(tt.panel)
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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractTemplateVariables(tt.dashboard)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestSubstituteVariables(t *testing.T) {
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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := substituteVariables(tt.query, tt.variables)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestRunPanelQueryParams(t *testing.T) {
	// Test that the params struct has the expected fields
	params := RunPanelQueryParams{
		DashboardUID: "test-uid",
		PanelID:      5,
		Start:        "now-1h",
		End:          "now",
		Variables: map[string]string{
			"job": "api-server",
		},
	}

	assert.Equal(t, "test-uid", params.DashboardUID)
	assert.Equal(t, 5, params.PanelID)
	assert.Equal(t, "now-1h", params.Start)
	assert.Equal(t, "now", params.End)
	assert.Equal(t, "api-server", params.Variables["job"])
}

func TestRunPanelQueryResult(t *testing.T) {
	// Test that the result struct has the expected fields
	result := RunPanelQueryResult{
		DashboardUID:   "test-uid",
		PanelID:        5,
		PanelTitle:     "Test Panel",
		DatasourceType: "prometheus",
		DatasourceUID:  "prom-uid",
		Query:          "rate(http_requests_total[5m])",
		TimeRange: QueryTimeRange{
			Start: "now-1h",
			End:   "now",
		},
		Results: []interface{}{"sample data"},
	}

	assert.Equal(t, "test-uid", result.DashboardUID)
	assert.Equal(t, 5, result.PanelID)
	assert.Equal(t, "Test Panel", result.PanelTitle)
	assert.Equal(t, "prometheus", result.DatasourceType)
	assert.Equal(t, "prom-uid", result.DatasourceUID)
	assert.Equal(t, "rate(http_requests_total[5m])", result.Query)
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

func TestGetVariableNames(t *testing.T) {
	vars := map[string]string{
		"job":       "api-server",
		"namespace": "production",
		"instance":  "localhost:9090",
	}

	names := getVariableNames(vars)
	assert.Len(t, names, 3)
	assert.Contains(t, names, "job")
	assert.Contains(t, names, "namespace")
	assert.Contains(t, names, "instance")
}

func TestExtractQueryExpression(t *testing.T) {
	tests := []struct {
		name   string
		target map[string]any
		want   string
	}{
		{
			name:   "expr field (Prometheus/Loki)",
			target: map[string]any{"expr": "rate(http_requests_total[5m])"},
			want:   "rate(http_requests_total[5m])",
		},
		{
			name:   "query field (generic)",
			target: map[string]any{"query": "SELECT * FROM logs"},
			want:   "SELECT * FROM logs",
		},
		{
			name:   "expression field",
			target: map[string]any{"expression": "some_expression"},
			want:   "some_expression",
		},
		{
			name:   "rawSql field (ClickHouse)",
			target: map[string]any{"rawSql": "SELECT * FROM otel_logs WHERE $__timeFilter(Timestamp)"},
			want:   "SELECT * FROM otel_logs WHERE $__timeFilter(Timestamp)",
		},
		{
			name:   "rawQuery field",
			target: map[string]any{"rawQuery": "raw query text"},
			want:   "raw query text",
		},
		{
			name:   "priority order - expr takes precedence",
			target: map[string]any{"expr": "up", "query": "down", "rawSql": "other"},
			want:   "up",
		},
		{
			name:   "no query fields",
			target: map[string]any{"refId": "A", "datasource": "prometheus"},
			want:   "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractQueryExpression(tt.target)
			assert.Equal(t, tt.want, got)
		})
	}
}
