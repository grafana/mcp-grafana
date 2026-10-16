//go:build unit

package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

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
	results, err := executeInfinityPanelQuery(ctx, "netbox-infinity", info, "now-1h", "now", variables)
	require.NoError(t, err)
	assert.NotNil(t, results)

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
		_, err = executeInfinityPanelQuery(ctx, "netbox-infinity", info, "now-1h", "now", nil)
		require.Error(t, err, "parser %q must be rejected before any HTTP call", parser)
		assert.Contains(t, err.Error(), "parser")
	}
}

func TestExecuteInfinityPanelQuery_NilTarget(t *testing.T) {
	_, err := executeInfinityPanelQuery(context.Background(), "netbox-infinity", &panelInfo{}, "now-1h", "now", nil)
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
