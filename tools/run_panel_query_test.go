//go:build unit

package tools

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
			name:     "rate_interval",
			query:    "rate(requests[$__rate_interval])",
			duration: time.Hour,
			expected: "rate(requests[1m])",
		},
		{
			name:     "interval_1_hour",
			query:    "rate(requests[$__interval])",
			duration: time.Hour,
			expected: "rate(requests[36s])", // 3600/100 = 36s
		},
		{
			name:     "interval_braced",
			query:    "rate(requests[${__interval}])",
			duration: time.Hour,
			expected: "rate(requests[36s])",
		},
		{
			name:     "interval_ms",
			query:    "timestamp(up) - $__interval_ms",
			duration: time.Hour,
			expected: "timestamp(up) - 36000", // 36s = 36000ms
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
			name:     "short_duration_floor_to_1s",
			query:    "rate(x[$__interval])",
			duration: 30 * time.Second, // 30s/100 = 0.3s, floors to 1s
			expected: "rate(x[1s])",
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
			expected: "timestamp(up) - 36000",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			start := time.Now()
			end := start.Add(tt.duration)
			result := substituteGrafanaMacros(tt.query, start, end)
			assert.Equal(t, tt.expected, result)
		})
	}
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
			name:     "nil ClickHouseQueryResult",
			results:  (*ClickHouseQueryResult)(nil),
			expected: true,
		},
		{
			name:     "empty ClickHouseQueryResult",
			results:  &ClickHouseQueryResult{Rows: []map[string]interface{}{}},
			expected: true,
		},
		{
			name: "non-empty ClickHouseQueryResult",
			results: &ClickHouseQueryResult{
				Rows: []map[string]interface{}{{"value": 1}},
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
				"No data found",
				"Time range",
				"list_prometheus_metric_names",
				"Label selectors",
			},
		},
		{
			name:           "loki hints",
			datasourceType: "loki",
			query:          "{job=\"app\"} |= \"error\"",
			containsHints: []string{
				"No data found",
				"Time range",
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
				"describe_clickhouse_table",
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
		{"loki", "loki"},
		{"Loki", "loki"},
		{"cloudwatch", "cloudwatch"},
		{"CloudWatch", "cloudwatch"},
		{"grafana-clickhouse-datasource", "clickhouse"},
		{"clickhouse", "clickhouse"},
		{"ClickHouse", "clickhouse"},
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
