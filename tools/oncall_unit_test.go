//go:build unit

package tools

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	mcpgrafana "github.com/grafana/mcp-grafana/v2"
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

func TestGetAlertGroupWithGrafanaURLOverrideUsesRequestToken(t *testing.T) {
	t.Setenv("GRAFANA_SERVICE_ACCOUNT_TOKEN", "env-token")

	oncallHeaders := make(chan http.Header, 1)
	oncall := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		oncallHeaders <- r.Header.Clone()
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Equal(t, "/api/v1/alert_groups/AG123/", r.URL.Path)
		require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
			"id": "AG123", "state": "new", "created_at": "2026-04-29T07:00:00Z",
		}))
	}))
	t.Cleanup(oncall.Close)

	grafana := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "Bearer request-token", r.Header.Get("Authorization"))
		assert.Equal(t, "/api/plugins/grafana-irm-app/settings", r.URL.Path)
		require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
			"jsonData": map[string]string{"onCallApiUrl": oncall.URL},
		}))
	}))
	t.Cleanup(grafana.Close)

	var result *OnCallAlertGroup
	var callErr error
	handler := mcpgrafana.GrafanaURLOverrideMiddleware(true, []string{grafana.URL}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := mcpgrafana.WithGrafanaConfig(r.Context(), mcpgrafana.GrafanaConfig{
			AccessToken: "env-access-token", IDToken: "env-id-token",
		})
		ctx = mcpgrafana.ExtractGrafanaInfoFromHeaders(ctx, r)
		result, callErr = getAlertGroup(ctx, GetAlertGroupParams{AlertGroupID: "AG123"})
		w.WriteHeader(http.StatusNoContent)
	}))
	request := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	request.Header.Set("X-Grafana-URL", grafana.URL)
	request.Header.Set("X-Grafana-Service-Account-Token", "request-token")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, request)

	require.Equal(t, http.StatusNoContent, w.Code)
	require.NoError(t, callErr)
	require.NotNil(t, result)
	assert.Equal(t, "AG123", result.ID)
	headers := <-oncallHeaders
	assert.Equal(t, "request-token", headers.Get("Authorization"))
	assert.Equal(t, grafana.URL, headers.Get("X-Grafana-URL"))
}

func TestGetAlertGroupEscapesID(t *testing.T) {
	ctx, requests := newOnCallPublicAPIServer(t, nil)

	_, err := getAlertGroup(ctx, GetAlertGroupParams{AlertGroupID: "../alert_groups"})
	require.Error(t, err)
	assert.NotContains(t, *requests, "GET /api/v1/alert_groups/")
}

func TestGetAlertGroupUsesConfiguredTLS(t *testing.T) {
	oncall := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
			"id": "AG1", "state": "new", "created_at": "2026-10-19T00:00:00Z",
		}))
	}))
	t.Cleanup(oncall.Close)
	grafana := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
			"jsonData": map[string]string{"onCallApiUrl": oncall.URL},
		}))
	}))
	t.Cleanup(grafana.Close)

	caFile := filepath.Join(t.TempDir(), "ca.pem")
	require.NoError(t, os.WriteFile(caFile, pem.EncodeToMemory(&pem.Block{
		Type: "CERTIFICATE", Bytes: grafana.Certificate().Raw,
	}), 0o600))

	ctx := mcpgrafana.WithGrafanaConfig(context.Background(), mcpgrafana.GrafanaConfig{
		URL:       grafana.URL,
		APIKey:    "oncall-token",
		TLSConfig: &mcpgrafana.TLSConfig{CAFile: caFile},
	})

	result, err := getAlertGroup(ctx, GetAlertGroupParams{AlertGroupID: "AG1"})
	require.NoError(t, err)
	assert.Equal(t, "AG1", result.ID)
}

func TestOnCallTransportUnderOverrideRefusesRedirectOffOnCallHost(t *testing.T) {
	var thirdHits atomic.Int32
	third := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		thirdHits.Add(1)
	}))
	t.Cleanup(third.Close)

	oncall := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redirect/" {
			http.Redirect(w, r, third.URL+"/", http.StatusFound)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(oncall.Close)

	// Allow cross-origin redirects so that only the target guard can refuse them.
	cfg := mcpgrafana.GrafanaConfig{
		URL:                       "http://grafana.example",
		OverrideURL:               "http://grafana.example",
		AllowCrossOriginRedirects: true,
	}
	transport, err := oncallTransport(cfg, oncall.URL)
	require.NoError(t, err)
	client := &http.Client{Transport: transport}

	resp, err := client.Get(oncall.URL + "/api/v1/alert_groups/")
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	_, err = client.Get(oncall.URL + "/redirect/")
	require.ErrorContains(t, err, "grafana URL override blocked request outside selected target")
	assert.Zero(t, thirdHits.Load())
}
