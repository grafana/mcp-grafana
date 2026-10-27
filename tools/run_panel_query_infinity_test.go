//go:build unit

package tools

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"github.com/grafana/grafana-plugin-sdk-go/data"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	mcpgrafana "github.com/grafana/mcp-grafana/v2"
)

// infinityDSQueryServer answers /api/ds/query with one empty frame and captures the request.
func infinityDSQueryServer(t *testing.T, payload *map[string]interface{}) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/ds/query", r.URL.Path)
		assert.Equal(t, http.MethodPost, r.Method)
		require.NoError(t, json.NewDecoder(r.Body).Decode(payload))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":{"A":{"status":200,"frames":[{"schema":{"refId":"A","fields":[]},"data":{"values":[]}}]}}}`))
	}))
}

func infinityPanel(parser string) map[string]interface{} {
	return map[string]interface{}{
		"id":    7,
		"title": "NetBox VMs",
		"datasource": map[string]interface{}{
			"uid":  "netbox-infinity",
			"type": InfinityDatasourceType,
		},
		"targets": []interface{}{
			map[string]interface{}{
				"refId":         "A",
				"type":          "json",
				"source":        "url",
				"format":        "table",
				"parser":        parser,
				"url":           "/api/virtualization/virtual-machines/?cf_instance_name=${account}&limit=100",
				"root_selector": "results[custom_fields.instance_name = '${account}']",
				"url_options":   map[string]interface{}{"method": "GET", "data": ""},
			},
		},
	}
}

func TestNormalizeDatasourceType_Infinity(t *testing.T) {
	assert.Equal(t, "infinity", normalizeDatasourceType(InfinityDatasourceType))
	assert.Equal(t, "infinity", normalizeDatasourceType("Yesoreyeram-Infinity-Datasource"))
	assert.True(t, usesStructuredTarget(InfinityDatasourceType))
	assert.True(t, usesStructuredTarget("cloudwatch"))
	assert.False(t, usesStructuredTarget("prometheus"))
}

func TestExtractPanelInfo_Infinity(t *testing.T) {
	info, err := extractPanelInfo(infinityPanel("backend"), 0)
	require.NoError(t, err)
	assert.Equal(t, "netbox-infinity", info.DatasourceUID)
	assert.Equal(t, InfinityDatasourceType, info.DatasourceType)
	assert.Empty(t, info.Query, "Infinity targets carry no query expression")
	require.NotNil(t, info.RawTarget)
	assert.Equal(t, "backend", info.RawTarget["parser"])
	assert.Contains(t, info.RawTarget["url"], "virtual-machines")
}

func TestExecuteInfinityPanelQuery(t *testing.T) {
	var payload map[string]interface{}
	ts := infinityDSQueryServer(t, &payload)
	defer ts.Close()
	ctx := mcpgrafana.WithGrafanaConfig(context.Background(), mcpgrafana.GrafanaConfig{URL: ts.URL})

	panel := infinityPanel("backend")
	info, err := extractPanelInfo(panel, 0)
	require.NoError(t, err)

	variables := templateVariableValues{"account": {"astratech"}}
	results, target, err := executeInfinityPanelQuery(ctx, "netbox-infinity", InfinityDatasourceType, info, "now-1h", "now", variables)
	require.NoError(t, err)
	assert.NotNil(t, results)
	assert.Equal(t, "/api/virtualization/virtual-machines/?cf_instance_name=astratech&limit=100", target["url"], "substituted target is returned")

	queries, ok := payload["queries"].([]interface{})
	require.True(t, ok)
	require.Len(t, queries, 1)
	q, ok := queries[0].(map[string]interface{})
	require.True(t, ok)
	ds, ok := q["datasource"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "netbox-infinity", ds["uid"])
	assert.Equal(t, InfinityDatasourceType, ds["type"])
	assert.Equal(t, "A", q["refId"])
	assert.Equal(t, "backend", q["parser"])
	assert.Equal(t, "/api/virtualization/virtual-machines/?cf_instance_name=astratech&limit=100", q["url"])
	assert.Equal(t, "results[custom_fields.instance_name = 'astratech']", q["root_selector"])
	assert.NotEmpty(t, payload["from"])
	assert.NotEmpty(t, payload["to"])
}

func TestExecuteInfinityPanelQuery_FrontendParserRejected(t *testing.T) {
	ctx := mcpgrafana.WithGrafanaConfig(context.Background(), mcpgrafana.GrafanaConfig{URL: "http://127.0.0.1:1"})
	for _, parser := range []string{"", "simple", "frontend", "uql", "groq"} {
		info, err := extractPanelInfo(infinityPanel(parser), 0)
		require.NoError(t, err)
		_, _, err = executeInfinityPanelQuery(ctx, "netbox-infinity", InfinityDatasourceType, info, "now-1h", "now", nil)
		require.Error(t, err, "parser %q must be rejected before any HTTP call", parser)
		assert.Contains(t, err.Error(), "parser")
	}
}

func TestInfinityBackendQueryError(t *testing.T) {
	tests := []struct {
		name    string
		target  map[string]interface{}
		wantErr string // empty = accepted
	}{
		{"json backend", map[string]interface{}{"type": "json", "parser": "backend"}, ""},
		{"missing type is json", map[string]interface{}{"parser": "backend"}, ""},
		{"csv backend", map[string]interface{}{"type": "csv", "parser": "backend"}, ""},
		{"json jq-backend", map[string]interface{}{"type": "json", "parser": "jq-backend"}, ""},
		{"google-sheets needs no parser", map[string]interface{}{"type": "google-sheets"}, ""},
		{"csv jq-backend runs in the browser", map[string]interface{}{"type": "csv", "parser": "jq-backend"}, "jq-backend"},
		{"missing parser is simple", map[string]interface{}{"type": "json"}, `"simple" parser`},
		{"simple parser", map[string]interface{}{"type": "json", "parser": "simple"}, `"simple" parser`},
		{"uql parser", map[string]interface{}{"type": "json", "parser": "uql"}, `"uql" parser`},
		{"transformations", map[string]interface{}{"type": "transformations"}, "transformations query"},
		{"series", map[string]interface{}{"type": "series"}, "random-walk"},
		{"global", map[string]interface{}{"type": "global"}, "datasource settings"},
		{"uql type", map[string]interface{}{"type": "uql"}, "transformed in the browser"},
		{"unknown type", map[string]interface{}{"type": "bogus", "parser": "backend"}, "unsupported infinity query type"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := infinityBackendQueryError(tt.target)
			if tt.wantErr == "" {
				assert.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestExecuteInfinityPanelQuery_MacrosAndTypePassThrough(t *testing.T) {
	var payload map[string]interface{}
	ts := infinityDSQueryServer(t, &payload)
	defer ts.Close()
	ctx := mcpgrafana.WithGrafanaConfig(context.Background(), mcpgrafana.GrafanaConfig{URL: ts.URL})

	panel := infinityPanel("backend")
	target := panel["targets"].([]interface{})[0].(map[string]interface{})
	target["url"] = "/api/metrics?range=$__range&step=$__interval&step_ms=${__interval_ms}"
	target["url_options"] = map[string]interface{}{"method": "POST", "data": `{"window": "$__range"}`}
	info, err := extractPanelInfo(panel, 0)
	require.NoError(t, err)

	// A renamed plugin ID still routes as infinity and is sent to Grafana as declared.
	_, _, err = executeInfinityPanelQuery(ctx, "netbox-infinity", "acme-infinity-datasource", info, "2024-01-15T10:00:00Z", "2024-01-15T11:00:00Z", nil)
	require.NoError(t, err)

	q := payload["queries"].([]interface{})[0].(map[string]interface{})
	assert.Equal(t, "acme-infinity-datasource", q["datasource"].(map[string]interface{})["type"])
	// 1h range: $__range = 1h, $__interval = range/100 = 36s
	assert.Equal(t, "/api/metrics?range=1h&step=36s&step_ms=36000", q["url"])
	assert.Equal(t, `{"window": "1h"}`, q["url_options"].(map[string]interface{})["data"], "macros are substituted in nested fields too")
}

func TestExecuteInfinityPanelQuery_PerRefIDError(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		wantErr string
	}{
		{
			"datasource error inside a 200",
			`{"results":{"A":{"status":500,"error":"error while performing the infinity query. 404 Not Found","errorSource":"downstream","frames":[]}}}`,
			"refId A (downstream): error while performing the infinity query. 404 Not Found",
		},
		{
			"non-2xx status without error text",
			`{"results":{"A":{"status":400,"frames":[]}}}`,
			"refId A: status 400",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(tt.body))
			}))
			defer ts.Close()
			ctx := mcpgrafana.WithGrafanaConfig(context.Background(), mcpgrafana.GrafanaConfig{URL: ts.URL})
			info, err := extractPanelInfo(infinityPanel("backend"), 0)
			require.NoError(t, err)

			_, _, err = executeInfinityPanelQuery(ctx, "netbox-infinity", InfinityDatasourceType, info, "now-1h", "now", nil)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestDSQueryResponsesError(t *testing.T) {
	assert.NoError(t, dsQueryResponsesError(backend.Responses{}))
	assert.NoError(t, dsQueryResponsesError(backend.Responses{"A": {Status: 200}, "B": {}}))
	err := dsQueryResponsesError(backend.Responses{
		"B": {Status: 502, Error: errors.New("upstream unreachable"), ErrorSource: backend.ErrorSourceDownstream},
		"A": {Status: 400},
		"C": {Status: 200},
	})
	require.Error(t, err)
	assert.Equal(t, "refId A: status 400; refId B (downstream): upstream unreachable", err.Error(), "sorted by refId, 2xx skipped")
}

func TestIsEmptyPanelResult_DSQueryResponses(t *testing.T) {
	assert.True(t, isEmptyPanelResult(backend.Responses{}))
	assert.True(t, isEmptyPanelResult(backend.Responses{"A": {Frames: data.Frames{data.NewFrame("A")}}}), "frame without fields")
	assert.True(t, isEmptyPanelResult(backend.Responses{"A": {Frames: data.Frames{data.NewFrame("A", data.NewField("v", nil, []int64{}))}}}), "fields without rows")
	assert.False(t, isEmptyPanelResult(backend.Responses{"A": {Frames: data.Frames{data.NewFrame("A", data.NewField("v", nil, []int64{1}))}}}))
}

func TestRunSinglePanelQuery_InfinityEmptyResult(t *testing.T) {
	var payload map[string]interface{}
	ts := infinityDSQueryServer(t, &payload)
	defer ts.Close()
	ctx := mcpgrafana.WithGrafanaConfig(context.Background(), mcpgrafana.GrafanaConfig{URL: ts.URL})

	panel := infinityPanel("backend")
	panel["id"] = float64(7) // JSON-decoded dashboards carry numbers as float64
	db := map[string]interface{}{
		"panels": []interface{}{panel},
		"templating": map[string]interface{}{"list": []interface{}{
			map[string]interface{}{"name": "account", "type": "textbox", "current": map[string]interface{}{"value": "default"}},
		}},
	}
	result, err := runSinglePanelQuery(ctx, singlePanelQueryParams{
		DB: db, PanelID: 7, Start: "now-1h", End: "now",
		Variables: map[string]string{"account": "astratech"},
	})
	require.NoError(t, err)
	assert.Equal(t, InfinityDatasourceType, result.DatasourceType)
	assert.Equal(t, "/api/virtualization/virtual-machines/?cf_instance_name=astratech&limit=100", result.Query, "structured targets report the substituted URL")
	require.NotEmpty(t, result.Hints, "an empty /api/ds/query response yields hints")
	assert.Contains(t, strings.Join(result.Hints, "\n"), "the query field shows the substituted URL")
}

func TestExecuteInfinityPanelQuery_NilTarget(t *testing.T) {
	_, _, err := executeInfinityPanelQuery(context.Background(), "netbox-infinity", InfinityDatasourceType, &panelInfo{}, "now-1h", "now", nil)
	require.Error(t, err)
	_, _, err = executeInfinityPanelQuery(context.Background(), "netbox-infinity", InfinityDatasourceType, nil, "now-1h", "now", nil)
	require.Error(t, err)
}

func TestExtractPanelInfoV2_Infinity(t *testing.T) {
	panel := map[string]interface{}{
		"id":    9,
		"title": "NetBox VMs (v2)",
		"data": map[string]interface{}{
			"spec": map[string]interface{}{
				"queries": []interface{}{
					map[string]interface{}{
						"spec": map[string]interface{}{
							"refId": "A",
							"query": map[string]interface{}{
								"group":      InfinityDatasourceType,
								"datasource": map[string]interface{}{"name": "netbox-infinity"},
								"spec": map[string]interface{}{
									"type":          "json",
									"source":        "url",
									"parser":        "backend",
									"url":           "/api/virtualization/virtual-machines/?limit=100",
									"root_selector": "results",
								},
							},
						},
					},
				},
			},
		},
	}
	info, err := extractPanelInfoV2(panel, 0)
	require.NoError(t, err)
	assert.Equal(t, "netbox-infinity", info.DatasourceUID)
	assert.Equal(t, InfinityDatasourceType, info.DatasourceType)
	assert.Empty(t, info.Query)
	require.NotNil(t, info.RawTarget)
	assert.Equal(t, "backend", info.RawTarget["parser"])
	assert.Equal(t, "A", info.RawTarget["refId"])
}
