//go:build unit

package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"github.com/grafana/grafana-plugin-sdk-go/data"
	mcpgrafana "github.com/grafana/mcp-grafana/v2"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// datasourceAppSession serves render_grafana_app with query_prometheus and the
// query_datasource source. Only query_prometheus is registered on its own.
func datasourceAppSession(t *testing.T, ctx context.Context) *mcp.ClientSession {
	t.Helper()
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	t.Cleanup(cancel)
	s := mcp.NewServer(&mcp.Implementation{Name: "app-test", Version: "1"}, nil)
	QueryPrometheus.Register(s)
	AddAppTool(s, QueryPrometheus, QueryDatasourceSource)
	mcpgrafana.RegisterGrafanaAppResource(s, mcpgrafana.WithDatasourceView())
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	go func() { _ = s.Run(ctx, serverTransport) }()
	c, err := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "1"}, nil).Connect(ctx, clientTransport, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func TestQueryDatasourceIsOnlyAnAppSource(t *testing.T) {
	c := datasourceAppSession(t, t.Context())
	listed, err := c.ListTools(t.Context(), nil)
	require.NoError(t, err)
	var app *mcp.Tool
	for _, tool := range listed.Tools {
		assert.NotEqual(t, QueryDatasourceToolName, tool.Name, "the source must not be a tool of its own")
		if tool.Name == "render_grafana_app" {
			app = tool
		}
	}
	require.NotNil(t, app)

	body, err := json.Marshal(app.InputSchema)
	require.NoError(t, err)
	var schema struct {
		Properties struct {
			ToolName struct {
				Enum []string `json:"enum"`
			} `json:"toolName"`
			Arguments struct {
				AnyOf []struct {
					Description string `json:"description"`
				} `json:"anyOf"`
			} `json:"arguments"`
		} `json:"properties"`
	}
	require.NoError(t, json.Unmarshal(body, &schema))
	assert.Equal(t, []string{"query_prometheus", QueryDatasourceToolName}, schema.Properties.ToolName.Enum)
	require.Len(t, schema.Properties.Arguments.AnyOf, 2)
	assert.True(t, strings.HasPrefix(schema.Properties.Arguments.AnyOf[1].Description, QueryDatasourceToolName+":"))

	// A source that passes raw queries through makes the viewer non-read-only.
	require.NotNil(t, app.Annotations)
	assert.False(t, app.Annotations.ReadOnlyHint)
	require.NotNil(t, app.Annotations.DestructiveHint)
	assert.True(t, *app.Annotations.DestructiveHint)
}

func TestAppToolHintsStayReadOnlyForReadOnlySources(t *testing.T) {
	c := appSession(t, t.Context())
	listed, err := c.ListTools(t.Context(), nil)
	require.NoError(t, err)
	for _, tool := range listed.Tools {
		if tool.Name != "render_grafana_app" {
			continue
		}
		assert.True(t, tool.Annotations.ReadOnlyHint)
		assert.True(t, tool.Annotations.IdempotentHint)
		require.NotNil(t, tool.Annotations.DestructiveHint)
		assert.False(t, *tool.Annotations.DestructiveHint)
		return
	}
	t.Fatal("render_grafana_app is not listed")
}

func TestQueryDatasourceAppResult(t *testing.T) {
	var payload map[string]any
	grafana := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/ds/query" {
			http.NotFound(w, r)
			return
		}
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "Bearer test", r.Header.Get("Authorization"))
		require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
		frame := data.NewFrame("",
			data.NewField("Time", nil, []time.Time{time.Unix(1700000000, 0), time.Unix(1700000060, 0)}),
			data.NewField("Value", data.Labels{"handler": "/api/health"}, []float64{1, 2}).
				SetConfig(&data.FieldConfig{DisplayNameFromDS: "/api/health"}),
		)
		w.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(w).Encode(backend.QueryDataResponse{Responses: backend.Responses{
			"A": backend.DataResponse{Frames: data.Frames{frame}},
		}}))
	}))
	t.Cleanup(grafana.Close)
	ctx := mcpgrafana.WithGrafanaConfig(t.Context(), mcpgrafana.GrafanaConfig{URL: grafana.URL, APIKey: "test"})
	c := datasourceAppSession(t, ctx)

	result, err := c.CallTool(t.Context(), &mcp.CallToolParams{Name: "render_grafana_app", Arguments: map[string]any{
		"toolName": QueryDatasourceToolName,
		"arguments": map[string]any{
			"datasourceUid": "prom",
			"queries":       []any{map[string]any{"expr": "sum by (handler) (rate(x[5m]))", "legendFormat": "{{handler}}"}},
			"from":          "1700000000000",
			"to":            "1700000600000",
		},
	}})
	require.NoError(t, err)
	require.False(t, result.IsError, "%v", result.Content)

	// The request: defaults filled, the range as epoch milliseconds.
	assert.Equal(t, "1700000000000", payload["from"])
	assert.Equal(t, "1700000600000", payload["to"])
	query := dsQueryOf(t, payload)
	assert.Equal(t, "A", query["refId"])
	assert.Equal(t, map[string]any{"uid": "prom"}, query["datasource"])
	assert.Equal(t, "{{handler}}", query["legendFormat"])
	assert.EqualValues(t, defaultDatasourceMaxDataPoints, query["maxDataPoints"])
	assert.EqualValues(t, 1000, query["intervalMs"])

	// The App payload: frames unchanged and a panel picked from their shape.
	var view struct {
		Request struct {
			Queries []map[string]any `json:"queries"`
		} `json:"request"`
		Range   datasourceRange `json:"range"`
		Results map[string]struct {
			Frames []struct {
				Schema struct {
					Fields []struct {
						Config map[string]any `json:"config"`
					} `json:"fields"`
				} `json:"schema"`
			} `json:"frames"`
		} `json:"results"`
		Panel datasourcePanel `json:"panel"`
		Pick  vizPick         `json:"pick"`
	}
	body, err := json.Marshal(result.StructuredContent)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(body, &view))
	assert.Equal(t, datasourceRange{From: 1700000000000, To: 1700000600000}, view.Range)
	require.Len(t, view.Results["A"].Frames, 1)
	assert.Equal(t, "/api/health", view.Results["A"].Frames[0].Schema.Fields[1].Config["displayNameFromDS"])
	assert.Equal(t, "time-series", view.Pick.Rule)
	assert.Equal(t, view.Pick.Type, view.Panel.Type)
	assert.Equal(t, "prom", view.Request.Queries[0]["datasource"].(map[string]any)["uid"])

	require.Len(t, result.Content, 1)
	assert.Equal(t, "Displayed query_datasource result in the Grafana App.", result.Content[0].(*mcp.TextContent).Text)
}

func TestQueryDatasourceRejectsInvalidInput(t *testing.T) {
	grafana := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request %s", r.URL.Path)
	}))
	t.Cleanup(grafana.Close)
	ctx := mcpgrafana.WithGrafanaConfig(t.Context(), mcpgrafana.GrafanaConfig{URL: grafana.URL, APIKey: "test"})
	c := datasourceAppSession(t, ctx)
	for name, args := range map[string]map[string]any{
		"no queries":    {"datasourceUid": "prom", "queries": []any{}},
		"empty range":   {"datasourceUid": "prom", "queries": []any{map[string]any{"expr": "up"}}, "from": "now", "to": "now-1h"},
		"bad time":      {"datasourceUid": "prom", "queries": []any{map[string]any{"expr": "up"}}, "from": "yesterday"},
		"unknown input": {"datasourceUid": "prom", "queries": []any{map[string]any{"expr": "up"}}, "type": "timeseries"},
		"no datasource": {"queries": []any{map[string]any{"expr": "up"}}},
		"nested orgId":  {"datasourceUid": "prom", "queries": []any{map[string]any{"expr": "up"}}, "orgId": 2},
	} {
		t.Run(name, func(t *testing.T) {
			result, err := c.CallTool(t.Context(), &mcp.CallToolParams{Name: "render_grafana_app", Arguments: map[string]any{
				"toolName": QueryDatasourceToolName, "arguments": args,
			}})
			require.NoError(t, err)
			assert.True(t, result.IsError)
		})
	}
}

func TestQueryDatasourceOversizedResultHasNoView(t *testing.T) {
	values := make([]float64, 80_000)
	times := make([]time.Time, len(values))
	for i := range values {
		times[i] = time.Unix(1700000000+int64(i), 0)
		values[i] = float64(i) + 0.123456789
	}
	grafana := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(w).Encode(backend.QueryDataResponse{Responses: backend.Responses{
			"A": backend.DataResponse{Frames: data.Frames{data.NewFrame("",
				data.NewField("Time", nil, times), data.NewField("Value", nil, values))}},
		}}))
	}))
	t.Cleanup(grafana.Close)
	ctx := mcpgrafana.WithGrafanaConfig(t.Context(), mcpgrafana.GrafanaConfig{URL: grafana.URL, APIKey: "test"})

	source, err := queryDatasource(ctx, QueryDatasourceParams{DatasourceUID: "prom", Queries: []map[string]any{{"expr": "up"}}})
	require.NoError(t, err)
	assert.Nil(t, source.StructuredContent)
	assert.Contains(t, source.Content[0].(*mcp.TextContent).Text, "A: 1 frames.")
}
