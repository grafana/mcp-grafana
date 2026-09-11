//go:build unit

package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	openapiclient "github.com/grafana/grafana-openapi-client-go/client"
	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"github.com/grafana/grafana-plugin-sdk-go/data"
	mcpgrafana "github.com/grafana/mcp-grafana"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// panelWithType builds a one-panel dashboard whose panel declares the given
// datasource uid/type and carries both an `expr` (as a Loki panel would) and a
// `rawSql`, so whichever executor it is routed to has a usable target.
func panelWithType(uid, dsType string) map[string]interface{} {
	return map[string]interface{}{
		"panels": []interface{}{
			map[string]interface{}{
				"id":    float64(1),
				"title": "logs",
				"type":  "logs",
				"datasource": map[string]interface{}{
					"uid":  uid,
					"type": dsType,
				},
				"targets": []interface{}{
					map[string]interface{}{
						"refId":  "A",
						"expr":   `{app="secret"}`,
						"rawSql": "SELECT 1",
					},
				},
			},
		},
	}
}

// enforceTestCtx wires a real Grafana OpenAPI client at server.URL and, when
// enforce is true, activates Loki label-matcher enforcement.
func enforceTestCtx(server *httptest.Server, enforce bool) context.Context {
	u, _ := url.Parse(server.URL)
	cfg := openapiclient.DefaultTransportConfig()
	cfg.Host = u.Host
	cfg.Schemes = []string{"http"}
	cfg.APIKey = "test"
	c := openapiclient.NewHTTPClientWithConfig(nil, cfg)

	ctx := mcpgrafana.WithGrafanaClient(context.Background(), &mcpgrafana.GrafanaClient{GrafanaHTTPAPI: c})
	gc := mcpgrafana.GrafanaConfig{URL: server.URL}
	if enforce {
		matchers, err := ParseEnforcedMatchers(`agent_safe="true"`)
		if err != nil {
			panic(err)
		}
		gc.LokiEnforcedMatchers = matchers
	}
	return mcpgrafana.WithGrafanaConfig(ctx, gc)
}

// TestRunSinglePanelQuery_OverrideTypeCannotBypassLokiEnforcement verifies the
// fix for the run_panel_query enforcement bypass: a caller-supplied
// datasourceType must not be able to divert a query onto the /api/ds/query
// proxy path, which does not apply Loki label-matcher enforcement.
func TestRunSinglePanelQuery_OverrideTypeCannotBypassLokiEnforcement(t *testing.T) {
	// The real datasource behind override-uid is Loki, even though the caller
	// declares it as a BigQuery datasource to try to reach executeSQLPanelQuery.
	t.Run("resolved type wins over caller override", func(t *testing.T) {
		var dsQueryHit bool
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch {
			case r.URL.Path == "/api/datasources/uid/override-uid":
				// Real type is Postgres; the caller claimed BigQuery.
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"uid": "override-uid", "type": "grafana-postgresql-datasource", "name": "pg",
				})
			case r.URL.Path == "/api/ds/query":
				dsQueryHit = true
				var payload map[string]interface{}
				_ = json.NewDecoder(r.Body).Decode(&payload)
				q := payload["queries"].([]interface{})[0].(map[string]interface{})
				dsObj := q["datasource"].(map[string]interface{})
				// The datasource sent downstream must carry the RESOLVED type,
				// not the caller's bigquery claim.
				assert.Equal(t, "grafana-postgresql-datasource", dsObj["type"])
				resp := backend.QueryDataResponse{Responses: backend.Responses{
					"A": backend.DataResponse{Frames: data.Frames{data.NewFrame("",
						data.NewField("v", nil, []int64{1}))}},
				}}
				b, _ := json.Marshal(resp)
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write(b)
			default:
				t.Errorf("unexpected request path %q", r.URL.Path)
			}
		}))
		defer srv.Close()

		result, err := runSinglePanelQuery(enforceTestCtx(srv, false), singlePanelQueryParams{
			DB:      panelWithType("override-uid", "loki"),
			PanelID: 1,
			Start:   "now-1h",
			End:     "now",
			DsUID:   "override-uid",
			DsType:  "grafana-bigquery-datasource", // the spoofed type
		})
		require.NoError(t, err)
		// Routed on the resolved Postgres type, not the caller's BigQuery.
		assert.Equal(t, "grafana-postgresql-datasource", result.DatasourceType)
		assert.True(t, dsQueryHit, "SQL executor should have been used for a real Postgres datasource")
	})

	// When the datasource cannot be read and enforcement is active, an
	// unverified non-Loki override must fail closed rather than proxy.
	t.Run("unreadable datasource under enforcement fails closed", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/api/datasources/uid/mystery-uid":
				w.WriteHeader(http.StatusForbidden)
			case "/api/frontend/settings":
				// Readable, but does not list the datasource, so the fallback
				// resolves to not-found and the type stays unverified.
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"datasources":{}}`))
			case "/api/ds/query":
				t.Errorf("ds/query must not be reached when failing closed")
			default:
				// Any other path (e.g. a Loki query) would also be a bypass.
				t.Errorf("unexpected request path %q", r.URL.Path)
			}
		}))
		defer srv.Close()

		_, err := runSinglePanelQuery(enforceTestCtx(srv, true), singlePanelQueryParams{
			DB:      panelWithType("mystery-uid", "loki"),
			PanelID: 1,
			Start:   "now-1h",
			End:     "now",
			DsUID:   "mystery-uid",
			DsType:  "grafana-bigquery-datasource",
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "enforcement is enabled")
	})
}
