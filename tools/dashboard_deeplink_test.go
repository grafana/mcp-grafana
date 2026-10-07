package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/grafana/grafana-openapi-client-go/client"
	mcpgrafana "github.com/grafana/mcp-grafana/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUpdateDashboardDeeplink(t *testing.T) {
	for _, tc := range []struct {
		name      string
		publicURL string
		configURL string
		wantURL   string
	}{
		{
			name:      "prefers public URL over internal address",
			publicURL: "https://grafana.example.com",
			configURL: "http://grafana.internal:3000",
			wantURL:   "https://grafana.example.com/d/test-dashboard",
		},
		{
			name:      "preserves public subpath without duplicate slash",
			publicURL: "https://example.com/grafana/",
			configURL: "http://grafana.internal:3000",
			wantURL:   "https://example.com/grafana/d/test-dashboard",
		},
		{
			name:      "falls back to configured URL",
			configURL: "http://localhost:3000/grafana/",
			wantURL:   "http://localhost:3000/grafana/d/test-dashboard",
		},
		{
			name:    "preserves successful save without instance URL",
			wantURL: "/d/test-dashboard/test",
		},
	} {
		for _, mode := range []string{"full JSON", "patch"} {
			t.Run(tc.name+"/"+mode, func(t *testing.T) {
				var saves int
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					switch r.Method + " " + r.URL.Path {
					case "GET /api/dashboards/uid/test-dashboard":
						_, _ = w.Write([]byte(`{"dashboard":{"uid":"test-dashboard","title":"Old title"},"meta":{"folderUid":"test-folder"}}`))
					case "POST /api/dashboards/db":
						saves++
						var body struct {
							Dashboard map[string]interface{} `json:"dashboard"`
						}
						assert.NoError(t, json.NewDecoder(r.Body).Decode(&body))
						assert.Equal(t, "New title", body.Dashboard["title"])
						_, _ = w.Write([]byte(`{"id":1,"uid":"test-dashboard","url":"/d/test-dashboard/test","status":"success","version":2,"folderUid":"test-folder"}`))
					default:
						t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
						w.WriteHeader(http.StatusNotFound)
					}
				}))
				defer srv.Close()

				u, err := url.Parse(srv.URL)
				require.NoError(t, err)
				cfg := client.DefaultTransportConfig()
				cfg.Host = u.Host
				cfg.Schemes = []string{"http"}
				ctx := mcpgrafana.WithGrafanaConfig(context.Background(), mcpgrafana.GrafanaConfig{URL: tc.configURL})
				ctx = mcpgrafana.WithGrafanaClient(ctx, &mcpgrafana.GrafanaClient{
					GrafanaHTTPAPI: client.NewHTTPClientWithConfig(nil, cfg),
					PublicURL:      tc.publicURL,
				})
				args := UpdateDashboardParams{Dashboard: map[string]interface{}{"title": "New title"}}
				if mode == "patch" {
					args = UpdateDashboardParams{
						UID:        "test-dashboard",
						Operations: []PatchOperation{{Op: "replace", Path: "$.title", Value: "New title"}},
					}
				}

				result, err := updateDashboard(ctx, args)
				require.NoError(t, err)
				require.NotNil(t, result)
				assert.Equal(t, 1, saves)
				assert.Equal(t, tc.wantURL, *result.URL)
				assert.Equal(t, "test-dashboard", *result.UID)
				assert.Equal(t, "success", *result.Status)
				assert.Equal(t, int64(2), *result.Version)
				assert.Equal(t, "test-folder", result.FolderUID)
			})
		}
	}
}
