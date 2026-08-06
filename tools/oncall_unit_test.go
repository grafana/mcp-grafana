package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	mcpgrafana "github.com/grafana/mcp-grafana"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func mockOnCallContext(t *testing.T, handler http.HandlerFunc) context.Context {
	t.Helper()

	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	return mcpgrafana.WithGrafanaConfig(context.Background(), mcpgrafana.GrafanaConfig{
		URL:    server.URL,
		APIKey: "oncall-token",
	})
}

func writeOnCallSettings(t *testing.T, w http.ResponseWriter, onCallURL string) {
	t.Helper()

	require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
		"jsonData": map[string]string{"onCallApiUrl": onCallURL},
	}))
}

func writeAlertGroupWithLastAlert(t *testing.T, w http.ResponseWriter) {
	t.Helper()

	require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
		"id":              "AG123",
		"integration_id":  "INT123",
		"route_id":        "ROUTE123",
		"alerts_count":    1,
		"state":           "new",
		"created_at":      "2026-04-29T07:00:00Z",
		"acknowledged_at": nil,
		"resolved_at":     nil,
		"title":           "Sentry issue",
		"permalinks": map[string]string{
			"web": "https://grafana.example/alert-groups/AG123",
		},
		"last_alert": map[string]any{
			"id":             "A123",
			"alert_group_id": "AG123",
			"created_at":     "2026-04-29T07:01:00Z",
			"payload": map[string]any{
				"data": map[string]any{
					"event": map[string]any{
						"hashes": []string{"66b46acbdeae7d18599d803d44d7c10f"},
					},
				},
			},
		},
	}))
}

func TestGetAlertGroupIncludesLastAlertPayload(t *testing.T) {
	var serverURL string
	var requests []string
	ctx := mockOnCallContext(t, func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)

		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/plugins/grafana-irm-app/settings":
			writeOnCallSettings(t, w, serverURL)
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/alert_groups/AG123/":
			writeAlertGroupWithLastAlert(t, w)
		default:
			http.Error(w, "unexpected request", http.StatusTeapot)
		}
	})
	serverURL = mcpgrafana.GrafanaConfigFromContext(ctx).URL

	result, err := getAlertGroup(ctx, GetAlertGroupParams{AlertGroupID: "AG123"})
	require.NoError(t, err)
	require.NotNil(t, result.LastAlert)

	assert.Equal(t, "AG123", result.ID)
	assert.Equal(t, []string{
		"GET /api/plugins/grafana-irm-app/settings",
		"GET /api/v1/alert_groups/AG123/",
	}, requests)

	assert.Equal(t, "A123", result.LastAlert.ID)
	assert.Equal(t, "AG123", result.LastAlert.AlertGroupID)

	data, ok := result.LastAlert.Payload["data"].(map[string]any)
	require.True(t, ok)
	event, ok := data["event"].(map[string]any)
	require.True(t, ok)
	hashes, ok := event["hashes"].([]any)
	require.True(t, ok)
	assert.Equal(t, "66b46acbdeae7d18599d803d44d7c10f", hashes[0])
}
