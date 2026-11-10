//go:build unit

package tools

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A value that contains a variable reference is not substituted again, whatever
// order the variables are visited in. Map iteration order is random, so the
// check is repeated.
func TestVariableInterpolatorSinglePass(t *testing.T) {
	values := templateVariableValues{"a": {"$b"}, "b": {"x"}, "c": {"${a}"}, "d": {"[[b]]"}}
	for i := 0; i < 200; i++ {
		in := newVariableInterpolator(values, nil)
		require.Equal(t, "$b/x/${a}/[[b]]/$b/x", in.interpolate("$a/$b/$c/$d/${a}/[[b]]"))
	}
}

func TestVariableInterpolatorReferences(t *testing.T) {
	values := templateVariableValues{
		"job":     {"api", "web"},
		"jobname": {"checkout"},
		"env":     {"prod"},
		"empty":   {},
		"re.x":    {"dotted"},
	}
	tests := []struct{ name, in, want string }{
		{"longer name wins", "$job $jobname ${jobname} [[jobname]]", "api checkout checkout checkout"},
		{"word boundary", "$jobs $job_x $job-x", "$jobs $job_x api-x"},
		{"all three syntaxes", "$env ${env} [[env]]", "prod prod prod"},
		{"explicit format", "${job:csv} [[job:pipe]] ${env:sqlstring}", "{api,web} {api,web} 'prod'"},
		{"format with arguments", "${job:sqlstring:x}", "'api','web'"},
		{"empty format after colon", "${env:}", "prod"},
		{"field path is left alone", "${env.text} ${env.text:raw}", "${env.text} ${env.text:raw}"},
		{"unknown variable is left alone", "$other ${other} [[other]]", "$other ${other} [[other]]"},
		{"empty selection", "[$empty] [${empty:csv}]", "[] []"},
		{"metacharacters in a name are literal", "$re.x $reax", "dotted $reax"},
		{"value with a dollar sign", "${env}$1", "prod$1"},
		{"adjacent references", "$env$env${env}", "prodprodprod"},
		{"no references", "up{job=\"static\"}", "up{job=\"static\"}"},
		{"empty string", "", ""},
	}
	in := newVariableInterpolator(values, nil)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, in.interpolate(tt.in))
		})
	}
}

func TestVariableInterpolatorNoVariables(t *testing.T) {
	for _, values := range []templateVariableValues{nil, {}, {"": {"x"}}} {
		in := newVariableInterpolator(values, nil)
		assert.Nil(t, in.pattern)
		assert.Equal(t, "$job ${job} [[job]] $", in.interpolate("$job ${job} [[job]] $"))
		assert.Equal(t, "$job", in.interpolateQuery("$job", "prometheus"))
	}
}

// Formatting is chosen from the raw datasource type, independently of the
// routing done by normalizeDatasourceType.
func TestFormatForDatasource(t *testing.T) {
	values := templateVariableValues{"v": {"a.b", "O'Brien"}}
	multi := map[string]bool{"v": true}
	tests := []struct{ datasourceType, unformatted, sqlstring string }{
		{"prometheus", `(a\\.b|O'Brien)`, `'a.b','O''Brien'`},
		{"Prometheus", `(a\\.b|O'Brien)`, `'a.b','O''Brien'`},
		{"loki", `a\\.b|O'Brien`, `'a.b','O''Brien'`},
		{"postgres", `'a.b','O''Brien'`, `'a.b','O''Brien'`},
		{"grafana-postgresql-datasource", `'a.b','O''Brien'`, `'a.b','O''Brien'`},
		{"mysql", `'a.b','O''Brien'`, `'a.b','O''Brien'`},
		{"MSSQL", `'a.b','O''Brien'`, `'a.b','O''Brien'`},
		{"elasticsearch", `("a.b" OR "O'Brien")`, `'a.b','O''Brien'`},
		{"grafana-bigquery-datasource", `a.b`, `"a.b","O'Brien"`},
		{"stackdriver", `a.b`, `'a.b','O''Brien'`},
		{"influxdb", `a.b`, `'a.b','O''Brien'`},
		{"cloudwatch", `a.b`, `'a.b','O''Brien'`},
		{"grafana-clickhouse-datasource", `a.b`, `'a.b','O''Brien'`},
		{"", `a.b`, `'a.b','O''Brien'`},
	}
	in := newVariableInterpolator(values, multi)
	for _, tt := range tests {
		t.Run(tt.datasourceType, func(t *testing.T) {
			assert.Equal(t, tt.unformatted, in.interpolateQuery("$v", tt.datasourceType))
			assert.Equal(t, tt.sqlstring, in.interpolateQuery("${v:sqlstring}", tt.datasourceType))
		})
	}
	// Routing is unchanged: stackdriver still runs on the Prometheus executor.
	assert.Equal(t, "prometheus", normalizeDatasourceType("stackdriver"))
}

// Grafana escapes an unformatted single-value dashboard variable too: SQL
// doubles single quotes, Prometheus escapes backslashes and single quotes, and
// Loki keeps the value raw. Backslashes are spelled BS.
func TestVariableInterpolatorSingleValueEscape(t *testing.T) {
	bs := func(s string) string { return strings.ReplaceAll(s, "BS", `\`) }
	tests := []struct{ name, datasourceType, query, value, want string }{
		{"PostgreSQL", "grafana-postgresql-datasource", "WHERE name = '$name'", "O'Reilly", "WHERE name = 'O''Reilly'"},
		{"MySQL", "mysql", "WHERE name = '${name}'", "it's", "WHERE name = 'it''s'"},
		{"MSSQL", "mssql", "WHERE name = '[[name]]'", "a'b'c", "WHERE name = 'a''b''c'"},
		{"Prometheus quote and backslash", "prometheus", `up{path="$name"}`, "C:BSdir's", `up{path="C:BSBSdirBSBS's"}`},
		{"Prometheus regex metacharacters", "prometheus", `up{job="$name"}`, "api.v1", `up{job="api.v1"}`},
		{"Loki", "loki", `{name="$name"}`, "it'sBS", `{name="it'sBS"}`},
		{"generic", "influxdb", `WHERE name = '$name'`, "O'Reilly", `WHERE name = 'O'Reilly'`},
		{"explicit format", "mysql", "WHERE name = '${name:raw}'", "O'Reilly", "WHERE name = 'O'Reilly'"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			values := templateVariableValues{"name": {bs(tt.value)}}
			dashboard := newVariableInterpolator(values, map[string]bool{})
			assert.Equal(t, bs(tt.want), dashboard.interpolateQuery(tt.query, tt.datasourceType))
		})
	}

	// query_sql has no dashboard variables, so its values are not escaped.
	querySQL := newVariableInterpolator(templateVariableValues{"name": {"O'Reilly"}}, nil)
	assert.Equal(t, "WHERE name = 'O'Reilly'", querySQL.interpolateQuery("WHERE name = '$name'", "mysql"))
}

// Fields other than the query expression keep generic formatting, so a
// multi-value variable gives its first value there.
func TestVariableInterpolatorTarget(t *testing.T) {
	in := newVariableInterpolator(templateVariableValues{"job": {"api", "web"}, "region": {"eu"}}, map[string]bool{"job": true})
	target := map[string]interface{}{
		"location":   "$region",
		"expr":       `up{job=~"$job"}`,
		"dimensions": map[string]interface{}{"job": []interface{}{"$job", float64(1), true, nil}},
		"refId":      "A",
		"hide":       false,
	}
	got := in.interpolateTarget(target)
	assert.Equal(t, map[string]interface{}{
		"location":   "eu",
		"expr":       `up{job=~"api"}`,
		"dimensions": map[string]interface{}{"job": []interface{}{"api", float64(1), true, nil}},
		"refId":      "A",
		"hide":       false,
	}, got)

	// The result is a deep copy.
	got["dimensions"].(map[string]interface{})["job"].([]interface{})[0] = "changed"
	assert.Equal(t, "$job", target["dimensions"].(map[string]interface{})["job"].([]interface{})[0])
	assert.Equal(t, map[string]interface{}{}, in.interpolateTarget(nil))
}

func TestDashboardInterpolator(t *testing.T) {
	db := map[string]interface{}{"templating": map[string]interface{}{"list": []interface{}{
		map[string]interface{}{"name": "job", "multi": true, "current": map[string]interface{}{"value": []interface{}{"api", "web"}}},
		map[string]interface{}{"name": "env", "current": map[string]interface{}{"value": "prod"}},
		map[string]interface{}{"name": "all", "includeAll": true, "current": map[string]interface{}{"value": []interface{}{"$__all"}, "text": "All"}},
		map[string]interface{}{"name": "none", "multi": true, "current": map[string]interface{}{"value": []interface{}{}}},
		map[string]interface{}{"name": "const", "type": "constant", "query": "c1"},
	}}}

	in := newDashboardInterpolator(db, map[string]string{"env": "staging", "extra": "x"})
	assert.Equal(t, `{job=~"(api|web)",env="staging",extra="x",c="c1"}`,
		in.interpolateQuery(`{job=~"$job",env="$env",extra="$extra",c="$const"}`, "prometheus"))

	first, ok := in.firstValue("job")
	assert.True(t, ok)
	assert.Equal(t, "api", first)
	_, ok = in.firstValue("none")
	assert.False(t, ok, "an empty selection has no first value")
	_, ok = in.firstValue("missing")
	assert.False(t, ok)

	assert.Contains(t, in.allSelectionWarning("all"), `"all" is set to All`)
	assert.Empty(t, in.allSelectionWarning("job"))
	// An All selection is not substituted: the reference is left for the warning.
	assert.Equal(t, "$all", in.interpolate("$all"))

	// An override can select All, and one can also clear it.
	assert.Contains(t, newDashboardInterpolator(db, map[string]string{"job": "$__all"}).allSelectionWarning("job"), "set to All")
	cleared := newDashboardInterpolator(db, map[string]string{"all": "api"})
	assert.Empty(t, cleared.allSelectionWarning("all"))
	assert.Equal(t, "api", cleared.interpolate("$all"))
}

// Grafana picks a value for a variable saved without one from its options,
// which mcp-grafana doesn't fetch, so a reference to it is rejected.
func TestDashboardInterpolatorNoSavedValue(t *testing.T) {
	db := map[string]interface{}{"templating": map[string]interface{}{"list": []interface{}{
		map[string]interface{}{"name": "job", "type": "query", "current": map[string]interface{}{}},
		map[string]interface{}{"name": "env", "type": "custom"},
		map[string]interface{}{"name": "source", "type": "datasource", "current": map[string]interface{}{}},
		map[string]interface{}{"name": "none", "type": "query", "current": map[string]interface{}{"value": []interface{}{}}},
	}}}
	in := newDashboardInterpolator(db, nil)
	assert.Equal(t, `Variable "job" has no saved value. Supply an explicit value in variables.`, in.allSelectionWarning("job"))
	assert.Contains(t, in.allSelectionWarning("env"), "no saved value")
	assert.Empty(t, in.allSelectionWarning("source"), "datasource variables resolve to the default datasource")
	assert.Empty(t, in.allSelectionWarning("none"), "an empty selection is a value")
	assert.Empty(t, in.allSelectionWarning("missing"), "names outside the dashboard are not variables")
	assert.Empty(t, newDashboardInterpolator(db, map[string]string{"job": "api"}).allSelectionWarning("job"))
}

// The single pass reaches dashboard inspection: a value that contains another
// variable's reference is sent as written.
func TestPreparePanelQueriesSinglePass(t *testing.T) {
	db := map[string]interface{}{"templating": map[string]interface{}{"list": []interface{}{
		map[string]interface{}{"name": "a", "current": map[string]interface{}{"value": "$b"}},
		map[string]interface{}{"name": "b", "current": map[string]interface{}{"value": "x"}},
	}}}
	for i := 0; i < 50; i++ {
		queries, err := preparePanelQueries(db, DashboardPanelQueriesParams{Variables: map[string]string{}},
			[]panelQuery{{Query: `up{a="$a",b="$b"}`, Datasource: datasourceInfo{UID: "prom", Type: "prometheus"}}}, nil)
		require.NoError(t, err)
		require.Equal(t, `up{a="$b",b="x"}`, queries[0].ProcessedQuery)
	}
}

// One pattern holds every name, so it must compile for far more variables than
// a dashboard has.
func TestVariableInterpolatorManyVariables(t *testing.T) {
	suffix := strings.Repeat("x", 40)
	values := make(templateVariableValues, 5000)
	for i := 0; i < 5000; i++ {
		values[fmt.Sprintf("var_%d_%s", i, suffix)] = []string{strconv.Itoa(i)}
	}
	in := newVariableInterpolator(values, nil)
	require.NotNil(t, in.pattern)
	assert.Equal(t, "7 4999 $var_5000_"+suffix,
		in.interpolate("$var_7_"+suffix+" ${var_4999_"+suffix+"} $var_5000_"+suffix))
}

var wordName = regexp.MustCompile(`^\w+$`)

// Any query, name and value: no panic; text without "$" or "[" is unchanged;
// and a bare reference gives the first value as written, never substituted again.
func FuzzVariableInterpolator(f *testing.F) {
	f.Add(`up{job=~"$job"} ${job:csv} [[job]] $jobs`, "job", "a$job")
	f.Add("${a.b} [[a:]] ${a:} $", "a", "")
	f.Add("[[re.x]] $re.x", "re.x", `\1`)
	f.Add("", "", "")
	f.Fuzz(func(t *testing.T, query, name, value string) {
		in := newVariableInterpolator(templateVariableValues{name: {value, "second"}}, map[string]bool{name: true})
		for _, datasourceType := range []string{"prometheus", "loki", "postgres", "mysql", "grafana-bigquery-datasource", "influxdb", ""} {
			got := in.interpolateQuery(query, datasourceType)
			if !strings.ContainsAny(query, "$[") && got != query {
				t.Fatalf("%s: %q changed to %q without a reference", datasourceType, query, got)
			}
		}
		if wordName.MatchString(name) {
			if got := in.interpolate("$" + name); got != value {
				t.Fatalf("$%s gave %q, want %q", name, got, value)
			}
		}
	})
}

// The default formatting of an unformatted reference to a multi-value variable
// matches Grafana's own functions on generated inputs. The fixtures come from
// testdata/variable_interpolation/harness.js, which runs Grafana's code; see the
// README there. Grafana passes a multi-value variable's selection as an array,
// so where the string and array paths differ (PostgreSQL) want_if_array applies.
func TestVariableInterpolatorMatchesGrafana(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "variable_interpolation", "golden.json"))
	require.NoError(t, err)
	var golden struct {
		Cases []struct {
			Datasource  string   `json:"datasource"`
			Values      []string `json:"values"`
			Want        string   `json:"want"`
			WantIfArray *string  `json:"want_if_array"`
		} `json:"cases"`
	}
	require.NoError(t, json.Unmarshal(raw, &golden))
	require.Len(t, golden.Cases, 1600)

	mismatches := 0
	for i, c := range golden.Cases {
		want := c.Want
		if c.WantIfArray != nil {
			want = *c.WantIfArray
		}
		in := newVariableInterpolator(templateVariableValues{"v": c.Values}, map[string]bool{"v": true})
		if got := in.interpolateQuery("$v", c.Datasource); got != want {
			mismatches++
			if mismatches <= 10 {
				t.Errorf("case %d (%s, %q): got %q, want %q", i, c.Datasource, c.Values, got, want)
			}
		}
	}
	assert.Zero(t, mismatches, "mismatches out of %d cases", len(golden.Cases))
}
