//go:build unit

package tools

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExtractDashboardVariables(t *testing.T) {
	t.Run("extracts variables from templating", func(t *testing.T) {
		dashboard := map[string]interface{}{
			"templating": map[string]interface{}{
				"list": []interface{}{
					map[string]interface{}{
						"name": "job",
						"type": "query",
						"current": map[string]interface{}{
							"value": "api-server",
						},
						"options": []interface{}{
							map[string]interface{}{
								"value": "default-job",
							},
						},
					},
					map[string]interface{}{
						"name": "instance",
						"type": "query",
						"current": map[string]interface{}{
							"value": "localhost:9090",
						},
					},
				},
			},
		}

		vars := extractDashboardVariables(dashboard)

		require.Len(t, vars, 2)
		assert.Equal(t, "api-server", vars["job"].CurrentValue)
		assert.Equal(t, "default-job", vars["job"].DefaultValue)
		assert.Equal(t, "localhost:9090", vars["instance"].CurrentValue)
	})

	t.Run("handles multi-value variables", func(t *testing.T) {
		dashboard := map[string]interface{}{
			"templating": map[string]interface{}{
				"list": []interface{}{
					map[string]interface{}{
						"name": "hosts",
						"type": "query",
						"current": map[string]interface{}{
							"value": []interface{}{"host1", "host2", "host3"},
						},
					},
				},
			},
		}

		vars := extractDashboardVariables(dashboard)

		require.Len(t, vars, 1)
		assert.Equal(t, "host1,host2,host3", vars["hosts"].CurrentValue)
	})

	t.Run("handles constant type variables", func(t *testing.T) {
		dashboard := map[string]interface{}{
			"templating": map[string]interface{}{
				"list": []interface{}{
					map[string]interface{}{
						"name":  "env",
						"type":  "constant",
						"query": "production",
					},
				},
			},
		}

		vars := extractDashboardVariables(dashboard)

		require.Len(t, vars, 1)
		assert.Equal(t, "production", vars["env"].DefaultValue)
	})

	t.Run("handles empty templating", func(t *testing.T) {
		dashboard := map[string]interface{}{}

		vars := extractDashboardVariables(dashboard)

		assert.Empty(t, vars)
	})
}

func TestFindPanelByID(t *testing.T) {
	t.Run("finds top-level panel", func(t *testing.T) {
		dashboard := map[string]interface{}{
			"panels": []interface{}{
				map[string]interface{}{
					"id":    float64(1),
					"title": "Panel One",
				},
				map[string]interface{}{
					"id":    float64(2),
					"title": "Panel Two",
				},
			},
		}

		panel, err := findPanelByID(dashboard, 2)

		require.NoError(t, err)
		assert.Equal(t, "Panel Two", panel["title"])
	})

	t.Run("finds nested panel in row", func(t *testing.T) {
		dashboard := map[string]interface{}{
			"panels": []interface{}{
				map[string]interface{}{
					"id":   float64(1),
					"type": "row",
					"panels": []interface{}{
						map[string]interface{}{
							"id":    float64(10),
							"title": "Nested Panel",
						},
					},
				},
			},
		}

		panel, err := findPanelByID(dashboard, 10)

		require.NoError(t, err)
		assert.Equal(t, "Nested Panel", panel["title"])
	})

	t.Run("returns error for non-existent panel", func(t *testing.T) {
		dashboard := map[string]interface{}{
			"panels": []interface{}{
				map[string]interface{}{
					"id": float64(1),
				},
			},
		}

		_, err := findPanelByID(dashboard, 999)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "panel with ID 999 not found")
	})

	t.Run("returns error for dashboard without panels", func(t *testing.T) {
		dashboard := map[string]interface{}{}

		_, err := findPanelByID(dashboard, 1)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "has no panels")
	})
}

func TestExtractQueryExpression(t *testing.T) {
	tests := []struct {
		name     string
		target   map[string]interface{}
		expected string
	}{
		{
			name: "prometheus expr",
			target: map[string]interface{}{
				"expr": "rate(http_requests_total[5m])",
			},
			expected: "rate(http_requests_total[5m])",
		},
		{
			name: "loki query",
			target: map[string]interface{}{
				"query": "{job=\"grafana\"} |= \"error\"",
			},
			expected: "{job=\"grafana\"} |= \"error\"",
		},
		{
			name: "cloudwatch expression",
			target: map[string]interface{}{
				"expression": "SELECT AVG(CPUUtilization) FROM SCHEMA(\"AWS/EC2\")",
			},
			expected: "SELECT AVG(CPUUtilization) FROM SCHEMA(\"AWS/EC2\")",
		},
		{
			name: "sql rawSql",
			target: map[string]interface{}{
				"rawSql": "SELECT * FROM metrics WHERE time > $__from",
			},
			expected: "SELECT * FROM metrics WHERE time > $__from",
		},
		{
			name:     "empty target",
			target:   map[string]interface{}{},
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractQueryExpression(tt.target)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestSubstituteVariables(t *testing.T) {
	variables := map[string]string{
		"job":         "api-server",
		"instance":    "localhost:9090",
		"environment": "prod",
	}

	tests := []struct {
		name     string
		query    string
		expected string
	}{
		{
			name:     "dollar sign variable",
			query:    "up{job=\"$job\"}",
			expected: "up{job=\"api-server\"}",
		},
		{
			name:     "curly brace variable",
			query:    "up{job=\"${job}\"}",
			expected: "up{job=\"api-server\"}",
		},
		{
			name:     "curly brace with option",
			query:    "up{job=\"${job:regex}\"}",
			expected: "up{job=\"api-server\"}",
		},
		{
			name:     "double bracket variable",
			query:    "up{job=\"[[job]]\"}",
			expected: "up{job=\"api-server\"}",
		},
		{
			name:     "multiple variables",
			query:    "up{job=\"$job\", instance=\"$instance\"}",
			expected: "up{job=\"api-server\", instance=\"localhost:9090\"}",
		},
		{
			name:     "mixed variable formats",
			query:    "up{job=\"$job\", env=\"${environment}\"}",
			expected: "up{job=\"api-server\", env=\"prod\"}",
		},
		{
			name:     "unknown variable unchanged",
			query:    "up{job=\"$unknown\"}",
			expected: "up{job=\"$unknown\"}",
		},
		{
			name:     "no variables",
			query:    "up{job=\"static\"}",
			expected: "up{job=\"static\"}",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := substituteVariables(tt.query, variables)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestFindVariablesInQuery(t *testing.T) {
	dashboardVars := map[string]VariableInfo{
		"job": {
			Name:         "job",
			CurrentValue: "api-server",
			DefaultValue: "default-job",
		},
	}

	t.Run("finds dollar sign variables", func(t *testing.T) {
		vars := findVariablesInQuery("up{job=\"$job\"}", dashboardVars, nil)

		require.Len(t, vars, 1)
		assert.Equal(t, "job", vars[0].Name)
		assert.Equal(t, "api-server", vars[0].CurrentValue)
		assert.Equal(t, "default-job", vars[0].DefaultValue)
	})

	t.Run("finds curly brace variables", func(t *testing.T) {
		vars := findVariablesInQuery("up{job=\"${job}\"}", dashboardVars, nil)

		require.Len(t, vars, 1)
		assert.Equal(t, "job", vars[0].Name)
	})

	t.Run("finds curly brace variables with options", func(t *testing.T) {
		vars := findVariablesInQuery("up{job=~\"${job:regex}\"}", dashboardVars, nil)

		require.Len(t, vars, 1)
		assert.Equal(t, "job", vars[0].Name)
	})

	t.Run("finds double bracket variables", func(t *testing.T) {
		vars := findVariablesInQuery("up{job=\"[[job]]\"}", dashboardVars, nil)

		require.Len(t, vars, 1)
		assert.Equal(t, "job", vars[0].Name)
	})

	t.Run("applies overrides", func(t *testing.T) {
		overrides := map[string]string{
			"job": "overridden-job",
		}
		vars := findVariablesInQuery("up{job=\"$job\"}", dashboardVars, overrides)

		require.Len(t, vars, 1)
		assert.Equal(t, "overridden-job", vars[0].CurrentValue)
	})

	t.Run("handles unknown variables", func(t *testing.T) {
		vars := findVariablesInQuery("up{job=\"$unknown\"}", dashboardVars, nil)

		require.Len(t, vars, 1)
		assert.Equal(t, "unknown", vars[0].Name)
		assert.Empty(t, vars[0].CurrentValue)
	})

	t.Run("deduplicates variables", func(t *testing.T) {
		vars := findVariablesInQuery("up{job=\"$job\", label=\"$job\"}", dashboardVars, nil)

		require.Len(t, vars, 1)
	})
}

func TestBuildEffectiveVariables(t *testing.T) {
	t.Run("uses current values from dashboard", func(t *testing.T) {
		dashboardVars := map[string]VariableInfo{
			"job": {
				Name:         "job",
				CurrentValue: "api-server",
				DefaultValue: "default-job",
			},
		}

		effective := buildEffectiveVariables(dashboardVars, nil)

		assert.Equal(t, "api-server", effective["job"])
	})

	t.Run("falls back to default when no current value", func(t *testing.T) {
		dashboardVars := map[string]VariableInfo{
			"job": {
				Name:         "job",
				CurrentValue: "",
				DefaultValue: "default-job",
			},
		}

		effective := buildEffectiveVariables(dashboardVars, nil)

		assert.Equal(t, "default-job", effective["job"])
	})

	t.Run("overrides take precedence", func(t *testing.T) {
		dashboardVars := map[string]VariableInfo{
			"job": {
				Name:         "job",
				CurrentValue: "api-server",
				DefaultValue: "default-job",
			},
		}
		overrides := map[string]string{
			"job": "overridden-job",
		}

		effective := buildEffectiveVariables(dashboardVars, overrides)

		assert.Equal(t, "overridden-job", effective["job"])
	})

	t.Run("overrides can add new variables", func(t *testing.T) {
		dashboardVars := map[string]VariableInfo{}
		overrides := map[string]string{
			"new_var": "new_value",
		}

		effective := buildEffectiveVariables(dashboardVars, overrides)

		assert.Equal(t, "new_value", effective["new_var"])
	})
}

func TestExtractPanelQueries(t *testing.T) {
	t.Run("extracts prometheus queries", func(t *testing.T) {
		panel := map[string]interface{}{
			"title": "HTTP Requests",
			"datasource": map[string]interface{}{
				"uid":  "prometheus",
				"type": "prometheus",
			},
			"targets": []interface{}{
				map[string]interface{}{
					"refId": "A",
					"expr":  "rate(http_requests_total{job=\"$job\"}[5m])",
				},
			},
		}
		dashboardVars := map[string]VariableInfo{
			"job": {
				Name:         "job",
				CurrentValue: "api-server",
			},
		}

		queries := extractPanelQueries(panel, dashboardVars, nil)

		require.Len(t, queries, 1)
		assert.Equal(t, "A", queries[0].RefID)
		assert.Equal(t, "rate(http_requests_total{job=\"$job\"}[5m])", queries[0].Query)
		assert.Equal(t, "rate(http_requests_total{job=\"api-server\"}[5m])", queries[0].ProcessedQuery)
		assert.Equal(t, "prometheus", queries[0].Datasource.UID)
		assert.Equal(t, "prometheus", queries[0].Datasource.Type)
		require.Len(t, queries[0].RequiredVariables, 1)
		assert.Equal(t, "job", queries[0].RequiredVariables[0].Name)
	})

	t.Run("uses target-level datasource when available", func(t *testing.T) {
		panel := map[string]interface{}{
			"datasource": map[string]interface{}{
				"uid":  "panel-ds",
				"type": "prometheus",
			},
			"targets": []interface{}{
				map[string]interface{}{
					"refId": "A",
					"expr":  "up",
					"datasource": map[string]interface{}{
						"uid":  "target-ds",
						"type": "loki",
					},
				},
			},
		}

		queries := extractPanelQueries(panel, nil, nil)

		require.Len(t, queries, 1)
		assert.Equal(t, "target-ds", queries[0].Datasource.UID)
		assert.Equal(t, "loki", queries[0].Datasource.Type)
	})

	t.Run("substitutes datasource variable", func(t *testing.T) {
		panel := map[string]interface{}{
			"datasource": map[string]interface{}{
				"uid":  "$datasource",
				"type": "prometheus",
			},
			"targets": []interface{}{
				map[string]interface{}{
					"refId": "A",
					"expr":  "up",
				},
			},
		}
		dashboardVars := map[string]VariableInfo{
			"datasource": {
				Name:         "datasource",
				CurrentValue: "prometheus-prod",
			},
		}

		queries := extractPanelQueries(panel, dashboardVars, nil)

		require.Len(t, queries, 1)
		assert.Equal(t, "prometheus-prod", queries[0].Datasource.UID)
	})

	t.Run("handles panel without targets", func(t *testing.T) {
		panel := map[string]interface{}{
			"title": "Text Panel",
			"type":  "text",
		}

		queries := extractPanelQueries(panel, nil, nil)

		assert.Empty(t, queries)
	})

	t.Run("handles multiple queries", func(t *testing.T) {
		panel := map[string]interface{}{
			"targets": []interface{}{
				map[string]interface{}{
					"refId": "A",
					"expr":  "up",
				},
				map[string]interface{}{
					"refId": "B",
					"expr":  "down",
				},
			},
		}

		queries := extractPanelQueries(panel, nil, nil)

		require.Len(t, queries, 2)
		assert.Equal(t, "A", queries[0].RefID)
		assert.Equal(t, "B", queries[1].RefID)
	})
}

func TestVariableRegex(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected []string
	}{
		{
			name:     "simple dollar sign",
			input:    "$job",
			expected: []string{"job"},
		},
		{
			name:     "curly brace",
			input:    "${job}",
			expected: []string{"job"},
		},
		{
			name:     "curly brace with option",
			input:    "${job:regex}",
			expected: []string{"job"},
		},
		{
			name:     "double bracket",
			input:    "[[job]]",
			expected: []string{"job"},
		},
		{
			name:     "multiple formats in one string",
			input:    "$foo ${bar} [[baz]]",
			expected: []string{"foo", "bar", "baz"},
		},
		{
			name:     "underscore in name",
			input:    "$my_var",
			expected: []string{"my_var"},
		},
		{
			name:     "number in name",
			input:    "$var1",
			expected: []string{"var1"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			matches := variableRegex.FindAllStringSubmatch(tt.input, -1)
			var found []string
			for _, m := range matches {
				for i := 1; i <= 3; i++ {
					if m[i] != "" {
						found = append(found, m[i])
						break
					}
				}
			}
			assert.Equal(t, tt.expected, found)
		})
	}
}

func TestCollectAllPanels(t *testing.T) {
	t.Run("collects top-level panels", func(t *testing.T) {
		db := map[string]interface{}{
			"panels": []interface{}{
				map[string]interface{}{
					"id":    float64(1),
					"title": "Panel One",
				},
				map[string]interface{}{
					"id":    float64(2),
					"title": "Panel Two",
				},
			},
		}

		panels := collectAllPanels(db)

		require.Len(t, panels, 2)
		assert.Equal(t, "Panel One", panels[0]["title"])
		assert.Equal(t, "Panel Two", panels[1]["title"])
	})

	t.Run("includes nested panels from rows", func(t *testing.T) {
		db := map[string]interface{}{
			"panels": []interface{}{
				map[string]interface{}{
					"id":    float64(1),
					"title": "Top Panel",
				},
				map[string]interface{}{
					"id":    float64(2),
					"type":  "row",
					"title": "My Row",
					"panels": []interface{}{
						map[string]interface{}{
							"id":    float64(10),
							"title": "Nested Panel A",
						},
						map[string]interface{}{
							"id":    float64(11),
							"title": "Nested Panel B",
						},
					},
				},
			},
		}

		panels := collectAllPanels(db)

		// Should include: Top Panel, My Row, Nested Panel A, Nested Panel B
		require.Len(t, panels, 4)
		assert.Equal(t, "Top Panel", panels[0]["title"])
		assert.Equal(t, "My Row", panels[1]["title"])
		assert.Equal(t, "Nested Panel A", panels[2]["title"])
		assert.Equal(t, "Nested Panel B", panels[3]["title"])
	})

	t.Run("handles empty dashboard", func(t *testing.T) {
		db := map[string]interface{}{}

		panels := collectAllPanels(db)

		assert.Empty(t, panels)
	})
}

func TestReplaceSimpleDollarVar(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		varName  string
		value    string
		expected string
	}{
		{
			name:     "simple replacement",
			input:    "up{job=\"$job\"}",
			varName:  "job",
			value:    "api-server",
			expected: "up{job=\"api-server\"}",
		},
		{
			name:     "end of string",
			input:    "metric $job",
			varName:  "job",
			value:    "api-server",
			expected: "metric api-server",
		},
		{
			name:     "no match - partial variable name",
			input:    "$jobname",
			varName:  "job",
			value:    "api-server",
			expected: "$jobname",
		},
		{
			name:     "multiple occurrences",
			input:    "$job and $job",
			varName:  "job",
			value:    "api-server",
			expected: "api-server and api-server",
		},
		{
			name:     "no match",
			input:    "no variables here",
			varName:  "job",
			value:    "api-server",
			expected: "no variables here",
		},
		{
			name:     "word boundary with underscore",
			input:    "$my_var_extra",
			varName:  "my_var",
			value:    "replaced",
			expected: "$my_var_extra",
		},
		{
			name:     "followed by non-word char",
			input:    "$job)",
			varName:  "job",
			value:    "api-server",
			expected: "api-server)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := replaceSimpleDollarVar(tt.input, tt.varName, tt.value)
			assert.Equal(t, tt.expected, result)
		})
	}
}
