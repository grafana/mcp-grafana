//go:build unit

package tools

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	mcpgrafana "github.com/grafana/mcp-grafana/v2"
)

func elasticsearchPanel(query string) map[string]interface{} {
	return map[string]interface{}{
		"id":    float64(7),
		"title": "Requests per service",
		"datasource": map[string]interface{}{
			"uid":  "elasticsearch",
			"type": elasticsearchDatasourceType,
		},
		"targets": []interface{}{
			map[string]interface{}{
				"refId":     "A",
				"query":     query,
				"timeField": "@timestamp",
				"metrics":   []interface{}{map[string]interface{}{"id": "1", "type": "count"}},
				"bucketAggs": []interface{}{
					map[string]interface{}{
						"id":   "2",
						"type": "filters",
						"settings": map[string]interface{}{"filters": []interface{}{
							map[string]interface{}{"label": "host", "query": "host:$host"},
							map[string]interface{}{"label": "all", "query": ""},
						}},
					},
					map[string]interface{}{
						"id":       "3",
						"type":     "date_histogram",
						"field":    "@timestamp",
						"settings": map[string]interface{}{"interval": "auto"},
					},
				},
			},
		},
	}
}

// elasticsearchDashboard holds the panel and a multi-value service variable plus single-value
// level and host variables.
func elasticsearchDashboard(panel map[string]interface{}) map[string]interface{} {
	return map[string]interface{}{
		"panels": []interface{}{panel},
		"templating": map[string]interface{}{"list": []interface{}{
			map[string]interface{}{"name": "service", "type": "custom", "multi": true,
				"current": map[string]interface{}{"value": []interface{}{"api-gateway", "auth-service"}}},
			map[string]interface{}{"name": "level", "type": "custom",
				"current": map[string]interface{}{"value": "error"}},
			map[string]interface{}{"name": "host", "type": "custom",
				"current": map[string]interface{}{"value": "server/1"}},
		}},
	}
}

// The lucene format follows Grafana's luceneEscape and lucene formatter:
// https://github.com/grafana/scenes/blob/40193645cac97b558b2c6f22225553666f75aae2/packages/scenes/src/variables/interpolation/formatRegistry.ts#L36-L56
func TestLuceneFormat(t *testing.T) {
	in := newVariableInterpolator(templateVariableValues{
		"service":      {"api-gateway", "auth"},
		"one":          {"auth"},
		"host":         {`a b:c/d"e\f`},
		"code":         {"-1.5"},
		"empty":        {},
		"single_empty": {},
	}, map[string]bool{"service": true, "one": true, "empty": true})
	lucene := func(s string) string { return in.interpolateQuery(s, elasticsearchDatasourceType) }

	assert.Equal(t, `("api\-gateway" OR "auth")`, lucene("$service"))
	assert.Equal(t, `("auth")`, lucene("$one"), "a multi-value variable is a list")
	assert.Equal(t, `a\ b\:c\/d\"e\\f`, lucene("$host"), "a single value is escaped")
	assert.Equal(t, "-1.5", lucene("$code"), "numbers are not escaped")
	assert.Equal(t, "", lucene("$single_empty"))
	assert.Equal(t, "__empty__", lucene("$empty"), "an empty selection matches nothing")
	assert.Equal(t, "{api-gateway,auth}", lucene("${service:glob}"), "an explicit format wins")
	assert.Equal(t, "api-gateway", in.interpolate("$service"), "other fields keep generic formatting")

	// Explicit :lucene follows the variable, not the number of selected values.
	for _, datasourceType := range []string{"prometheus", elasticsearchDatasourceType} {
		assert.Equal(t, `("api\-gateway" OR "auth")`, in.interpolateQuery("${service:lucene}", datasourceType))
		assert.Equal(t, `("auth")`, in.interpolateQuery("${one:lucene}", datasourceType))
		assert.Equal(t, "__empty__", in.interpolateQuery("${empty:lucene}", datasourceType))
		assert.Equal(t, `a\ b\:c\/d\"e\\f`, in.interpolateQuery("${host:lucene}", datasourceType))
	}
}

func TestExtractPanelInfo_ElasticsearchEmptyQuery(t *testing.T) {
	info, err := extractPanelInfo(elasticsearchPanel(""), 0)
	require.NoError(t, err, "an empty Lucene query matches all documents")
	assert.Equal(t, elasticsearchDatasourceType, info.DatasourceType)
	assert.Empty(t, info.Query)
}

func TestRunSinglePanelQuery_Elasticsearch(t *testing.T) {
	var payload map[string]interface{}
	ts := neo4jDSQueryServer(t, &payload)
	defer ts.Close()
	ctx := mcpgrafana.WithGrafanaConfig(context.Background(), mcpgrafana.GrafanaConfig{URL: ts.URL})

	panel := elasticsearchPanel("service:$service AND level:${level} AND NOT host:${host:raw}")
	result, err := runSinglePanelQuery(ctx, singlePanelQueryParams{
		DB: elasticsearchDashboard(panel), PanelID: 7,
		Start: "2024-01-15T00:00:00Z", End: "2024-01-16T00:00:00Z",
	})
	require.NoError(t, err)

	want := `service:("api\-gateway" OR "auth\-service") AND level:error AND NOT host:server/1`
	assert.Equal(t, elasticsearchDatasourceType, result.DatasourceType)
	assert.Equal(t, want, result.Query, "the reported query is the one sent")

	q := sentNeo4jQuery(t, payload)
	assert.Equal(t, want, q["query"])
	assert.Equal(t, map[string]interface{}{"uid": "elasticsearch", "type": elasticsearchDatasourceType}, q["datasource"])
	// The plugin sizes auto date histograms from intervalMs and uses 1s without it, which
	// exceeds Elasticsearch's bucket limit for ranges over about 18 hours.
	assert.EqualValues(t, 900000, q["intervalMs"], "the interval $__interval stands for: one day over 100 points, rounded to 15m")

	aggs := q["bucketAggs"].([]interface{})
	filters := aggs[0].(map[string]interface{})["settings"].(map[string]interface{})["filters"].([]interface{})
	assert.Equal(t, `host:server\/1`, filters[0].(map[string]interface{})["query"], "filter queries use the lucene format")
	assert.Equal(t, "*", filters[1].(map[string]interface{})["query"], "an empty filter matches all documents")

	original := panel["targets"].([]interface{})[0].(map[string]interface{})
	assert.Equal(t, "service:$service AND level:${level} AND NOT host:${host:raw}", original["query"], "the dashboard is not modified")

	require.NotEmpty(t, result.Hints, "an empty /api/ds/query response yields hints")
	assert.Contains(t, strings.Join(result.Hints, "\n"), "the query field shows the substituted Lucene query")
}

func TestRunSinglePanelQuery_ElasticsearchVariableOverride(t *testing.T) {
	var payload map[string]interface{}
	ts := neo4jDSQueryServer(t, &payload)
	defer ts.Close()
	ctx := mcpgrafana.WithGrafanaConfig(context.Background(), mcpgrafana.GrafanaConfig{URL: ts.URL})

	result, err := runSinglePanelQuery(ctx, singlePanelQueryParams{
		DB: elasticsearchDashboard(elasticsearchPanel("service:$service")), PanelID: 7,
		Start: "now-1h", End: "now", Variables: map[string]string{"service": "user-service"},
	})
	require.NoError(t, err)
	assert.Equal(t, `service:("user\-service")`, result.Query)
}

// Macros are substituted before variables, so a variable value that looks like a
// macro reaches Elasticsearch as written.
func TestRunSinglePanelQuery_ElasticsearchMacroLikeValueStaysLiteral(t *testing.T) {
	var payload map[string]interface{}
	ts := neo4jDSQueryServer(t, &payload)
	defer ts.Close()
	ctx := mcpgrafana.WithGrafanaConfig(context.Background(), mcpgrafana.GrafanaConfig{URL: ts.URL})

	_, err := runSinglePanelQuery(ctx, singlePanelQueryParams{
		DB: elasticsearchDashboard(elasticsearchPanel("level:$level AND @timestamp:[$__from TO $__to]")), PanelID: 7,
		Start: "2024-01-15T00:00:00Z", End: "2024-01-16T00:00:00Z", Variables: map[string]string{"level": "$__interval"},
	})
	require.NoError(t, err)
	assert.Equal(t, "level:$__interval AND @timestamp:[1705276800000 TO 1705363200000]", sentNeo4jQuery(t, payload)["query"])
}

// intervalMs is the interval $__interval stands for, so it follows the panel's
// interval options: a min interval of 1h wins over the 15m that one day over 100
// points rounds to.
func TestRunSinglePanelQuery_ElasticsearchFollowsPanelInterval(t *testing.T) {
	var payload map[string]interface{}
	ts := neo4jDSQueryServer(t, &payload)
	defer ts.Close()
	ctx := mcpgrafana.WithGrafanaConfig(context.Background(), mcpgrafana.GrafanaConfig{URL: ts.URL})

	panel := elasticsearchPanel("level:error AND took:>$__interval_ms")
	panel["interval"] = "1h"
	_, err := runSinglePanelQuery(ctx, singlePanelQueryParams{
		DB: elasticsearchDashboard(panel), PanelID: 7,
		Start: "2024-01-15T00:00:00Z", End: "2024-01-16T00:00:00Z",
	})
	require.NoError(t, err)
	q := sentNeo4jQuery(t, payload)
	assert.EqualValues(t, 3600000, q["intervalMs"])
	assert.Equal(t, "level:error AND took:>3600000", q["query"], "$__interval_ms is the same interval")
}

func TestRunSinglePanelQuery_ElasticsearchESQLRejected(t *testing.T) {
	panel := elasticsearchPanel("FROM logs | LIMIT 10")
	panel["targets"].([]interface{})[0].(map[string]interface{})["queryType"] = "esql"
	_, err := runSinglePanelQuery(context.Background(), singlePanelQueryParams{
		DB: elasticsearchDashboard(panel), PanelID: 7, Start: "now-1h", End: "now",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ES|QL")
}

func TestRunSinglePanelQuery_OpenSearchNotRoutedAsElasticsearch(t *testing.T) {
	panel := elasticsearchPanel("level:error")
	panel["datasource"] = map[string]interface{}{"uid": "opensearch", "type": openSearchDatasourceType}
	_, err := runSinglePanelQuery(context.Background(), singlePanelQueryParams{
		DB: map[string]interface{}{"panels": []interface{}{panel}}, PanelID: 7, Start: "now-1h", End: "now",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not supported by run_panel_query")
}

func TestRunSinglePanelQuery_ElasticsearchEmptySelectionMatchesNothing(t *testing.T) {
	var payload map[string]interface{}
	ts := neo4jDSQueryServer(t, &payload)
	defer ts.Close()
	ctx := mcpgrafana.WithGrafanaConfig(context.Background(), mcpgrafana.GrafanaConfig{URL: ts.URL})

	panel := elasticsearchPanel("service:$service")
	filter := panel["targets"].([]interface{})[0].(map[string]interface{})["bucketAggs"].([]interface{})[0].(map[string]interface{})["settings"].(map[string]interface{})["filters"].([]interface{})[0].(map[string]interface{})
	filter["query"] = "$service"
	db := elasticsearchDashboard(panel)
	db["templating"].(map[string]interface{})["list"].([]interface{})[0].(map[string]interface{})["current"] = map[string]interface{}{"value": []interface{}{}}

	result, err := runSinglePanelQuery(ctx, singlePanelQueryParams{DB: db, PanelID: 7, Start: "now-1h", End: "now"})
	require.NoError(t, err)
	assert.Equal(t, "service:__empty__", result.Query)
	filters := sentNeo4jQuery(t, payload)["bucketAggs"].([]interface{})[0].(map[string]interface{})["settings"].(map[string]interface{})["filters"].([]interface{})
	assert.Equal(t, "__empty__", filters[0].(map[string]interface{})["query"], "not *, which would match every document")
}
