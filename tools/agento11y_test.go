//go:build unit
// +build unit

package tools

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	mcpgrafana "github.com/grafana/mcp-grafana"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupMockAgento11yServer(handler http.HandlerFunc) (*httptest.Server, context.Context) {
	server := httptest.NewServer(handler)
	config := mcpgrafana.GrafanaConfig{
		URL:    server.URL,
		APIKey: "test-api-key",
	}
	ctx := mcpgrafana.WithGrafanaConfig(context.Background(), config)
	return server, ctx
}

func TestAgento11yFetchJSON(t *testing.T) {
	type payload struct {
		ID string `json:"id"`
	}

	testCases := []struct {
		name     string
		status   int
		body     string
		wantErr  string
		wantID   string
		method   string
		urlPath  string
		sendBody any
	}{
		{
			name:    "204 with empty body decodes to the zero value",
			status:  http.StatusNoContent,
			method:  http.MethodDelete,
			urlPath: "/eval/evaluators/quality.helpfulness",
		},
		{
			name:    "200 with an empty body is a decode error, not an empty result",
			status:  http.StatusOK,
			method:  http.MethodPost,
			urlPath: "/eval/rules:preview",
			wantErr: "failed to decode POST /eval/rules:preview response",
		},
		{
			name:    "200 with a whitespace-only body is a decode error",
			status:  http.StatusOK,
			body:    "\n",
			method:  http.MethodPost,
			urlPath: "/eval/rules:preview",
			wantErr: "failed to decode POST /eval/rules:preview response",
		},
		{
			name:     "201 is accepted",
			status:   http.StatusCreated,
			body:     `{"id":"created"}`,
			method:   http.MethodPost,
			urlPath:  "/eval/rules",
			sendBody: map[string]any{"rule_id": "my.rule"},
			wantID:   "created",
		},
		{
			name:    "403 surfaces the plugin body",
			status:  http.StatusForbidden,
			body:    "permission denied: grafana-agento11y-app.eval:write required",
			method:  http.MethodPost,
			urlPath: "/eval/evaluators",
			wantErr: "request failed with status 403: permission denied: grafana-agento11y-app.eval:write required",
		},
		{
			name:    "malformed JSON is reported as a decode error",
			status:  http.StatusOK,
			body:    "{not json",
			method:  http.MethodGet,
			urlPath: "/eval/evaluators",
			wantErr: "failed to decode GET /eval/evaluators response",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			server, ctx := setupMockAgento11yServer(func(w http.ResponseWriter, r *http.Request) {
				require.Equal(t, tc.method, r.Method)
				require.Equal(t, "/api/plugins/grafana-agento11y-app/resources"+tc.urlPath, r.URL.Path)
				w.WriteHeader(tc.status)
				if tc.body != "" {
					_, err := w.Write([]byte(tc.body))
					require.NoError(t, err)
				}
			})
			defer server.Close()

			client, err := newAgento11yClient(ctx)
			require.NoError(t, err)

			got, err := fetchAgento11yJSON[payload](ctx, client, tc.method, tc.urlPath, nil, tc.sendBody)
			if tc.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.wantID, got.ID)
		})
	}
}
