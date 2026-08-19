//go:build unit

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

// newOnCallPublicAPIServer starts a test server that answers both the Grafana
// IRM plugin settings lookup and the public OnCall API, pointing the former at
// itself. It records the path of every request it serves.
func newOnCallPublicAPIServer(t *testing.T, alertGroups map[string]any) (context.Context, *[]string) {
	t.Helper()

	var requests []string
	mux := http.NewServeMux()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		mux.ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)

	mux.HandleFunc("GET /api/plugins/grafana-irm-app/settings", func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
			"jsonData": map[string]string{"onCallApiUrl": server.URL},
		}))
	})
	for id, body := range alertGroups {
		alertGroup := body
		mux.HandleFunc("GET /api/v1/alert_groups/"+id+"/", func(w http.ResponseWriter, r *http.Request) {
			require.NoError(t, json.NewEncoder(w).Encode(alertGroup))
		})
	}
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unexpected request: "+r.URL.Path, http.StatusNotFound)
	})

	ctx := mcpgrafana.WithGrafanaConfig(context.Background(), mcpgrafana.GrafanaConfig{
		URL:    server.URL,
		APIKey: "oncall-token",
	})
	return ctx, &requests
}

func TestGetAlertGroupIncludesLastAlertPayload(t *testing.T) {
	ctx, requests := newOnCallPublicAPIServer(t, map[string]any{
		"AG123": map[string]any{
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
		},
	})

	result, err := getAlertGroup(ctx, GetAlertGroupParams{AlertGroupID: "AG123"})
	require.NoError(t, err)

	assert.Equal(t, "AG123", result.ID)
	assert.Equal(t, "INT123", result.IntegrationID)
	assert.Equal(t, 1, result.AlertsCount)
	assert.Equal(t, "new", result.State)
	assert.Equal(t, "Sentry issue", result.Title)
	assert.Equal(t, "https://grafana.example/alert-groups/AG123", result.Permalinks["web"])

	// The settings lookup resolves the public OnCall API URL, then the alert
	// group is fetched directly rather than via aapi.AlertGroupService.
	assert.Equal(t, []string{
		"GET /api/plugins/grafana-irm-app/settings",
		"GET /api/v1/alert_groups/AG123/",
	}, *requests)

	require.NotNil(t, result.LastAlert)
	assert.Equal(t, "A123", result.LastAlert.ID)
	assert.Equal(t, "AG123", result.LastAlert.AlertGroupID)
	assert.Equal(t, "2026-04-29T07:01:00Z", result.LastAlert.CreatedAt)

	// Integration-specific nesting survives the round trip: this is the
	// Sentry fingerprint an agent uses to correlate recurring alerts.
	data, ok := result.LastAlert.Payload["data"].(map[string]any)
	require.True(t, ok)
	event, ok := data["event"].(map[string]any)
	require.True(t, ok)
	hashes, ok := event["hashes"].([]any)
	require.True(t, ok)
	require.Len(t, hashes, 1)
	assert.Equal(t, "66b46acbdeae7d18599d803d44d7c10f", hashes[0])
}

func TestGetAlertGroupWithoutLastAlert(t *testing.T) {
	ctx, _ := newOnCallPublicAPIServer(t, map[string]any{
		"AG456": map[string]any{
			"id":           "AG456",
			"alerts_count": 0,
			"state":        "resolved",
			"created_at":   "2026-04-29T07:00:00Z",
		},
	})

	result, err := getAlertGroup(ctx, GetAlertGroupParams{AlertGroupID: "AG456"})
	require.NoError(t, err)
	assert.Equal(t, "AG456", result.ID)
	assert.Nil(t, result.LastAlert)

	// last_alert must be omitted rather than serialised as null, so alert
	// groups without alerts do not add noise to the tool response.
	encoded, err := json.Marshal(result)
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), "last_alert")
}

func TestGetAlertGroupNotFound(t *testing.T) {
	ctx, _ := newOnCallPublicAPIServer(t, nil)

	_, err := getAlertGroup(ctx, GetAlertGroupParams{AlertGroupID: "missing"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "getting OnCall alert group missing")
}

func TestGetAlertGroupEscapesID(t *testing.T) {
	ctx, requests := newOnCallPublicAPIServer(t, nil)

	_, err := getAlertGroup(ctx, GetAlertGroupParams{AlertGroupID: "../alert_groups"})
	require.Error(t, err)
	assert.NotContains(t, *requests, "GET /api/v1/alert_groups/")
}
