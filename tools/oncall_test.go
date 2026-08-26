package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	aapi "github.com/grafana/amixr-api-go-client"
	mcpgrafana "github.com/grafana/mcp-grafana"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAlertGroupActionForState(t *testing.T) {
	tests := []struct {
		name       string
		state      string
		wantAction string
		wantErr    bool
	}{
		{name: "acknowledged", state: "acknowledged", wantAction: "acknowledge"},
		{name: "unacknowledged", state: "unacknowledged", wantAction: "unacknowledge"},
		{name: "resolved", state: "resolved", wantAction: "resolve"},
		{name: "unresolved", state: "unresolved", wantAction: "unresolve"},
		{name: "normalizes case and whitespace", state: " Resolved ", wantAction: "resolve"},
		{name: "rejects a state with no action endpoint", state: "silenced", wantErr: true},
		{name: "rejects an unknown state", state: "banana", wantErr: true},
		{name: "rejects an empty state", state: "", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			action, err := alertGroupActionForState(tt.state)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantAction, action)
		})
	}
}

// oncallAPITestServer stands in for a Grafana instance that serves IRM plugin
// settings pointing at its own OnCall API, so the amixr (non-OBO) code path can
// be exercised end to end. alertGroup is what the read-back GET returns; a nil
// alertGroup makes that GET fail with 403, mimicking a write-only caller.
func oncallAPITestServer(t *testing.T, requests *[]string, alertGroup *aapi.AlertGroup, actionStatus int) *httptest.Server {
	t.Helper()

	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*requests = append(*requests, r.Method+" "+r.URL.Path)

		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/plugins/grafana-irm-app/settings":
			require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
				"jsonData": map[string]string{"onCallApiUrl": srv.URL},
			}))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/alert_groups/AG123/":
			if alertGroup == nil {
				http.Error(w, `{"detail":"permission denied"}`, http.StatusForbidden)
				return
			}
			require.NoError(t, json.NewEncoder(w).Encode(alertGroup))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/alert_groups/AG123/unresolve":
			w.WriteHeader(actionStatus)
		default:
			http.Error(w, "unexpected request", http.StatusTeapot)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestUpdateAlertGroupReadsBackRealState(t *testing.T) {
	var requests []string
	// OnCall reports "new" after an unresolve; "unresolved" is a transition,
	// not a state, so the result must not echo the requested value back.
	srv := oncallAPITestServer(t, &requests, &aapi.AlertGroup{
		ID:          "AG123",
		State:       "new",
		Title:       "Disk full",
		AlertsCount: 2,
	}, http.StatusOK)

	ctx := mcpgrafana.WithGrafanaConfig(context.Background(), mcpgrafana.GrafanaConfig{
		URL:    srv.URL,
		APIKey: "oncall-token",
	})

	result, err := updateAlertGroup(ctx, UpdateAlertGroupParams{
		AlertGroupID: "AG123",
		State:        "unresolved",
	})
	require.NoError(t, err)
	assert.Equal(t, &UpdateAlertGroupResult{
		AlertGroupID: "AG123",
		Action:       "unresolve",
		Updated:      true,
		State:        "new",
	}, result)
	assert.Equal(t, []string{
		"GET /api/plugins/grafana-irm-app/settings",
		"POST /api/v1/alert_groups/AG123/unresolve",
		"GET /api/plugins/grafana-irm-app/settings",
		"GET /api/v1/alert_groups/AG123/",
	}, requests, "the action must be posted before the state is read back")
}

func TestUpdateAlertGroupStillSucceedsWhenReadBackIsForbidden(t *testing.T) {
	var requests []string
	srv := oncallAPITestServer(t, &requests, nil, http.StatusOK)

	ctx := mcpgrafana.WithGrafanaConfig(context.Background(), mcpgrafana.GrafanaConfig{
		URL:    srv.URL,
		APIKey: "oncall-token",
	})

	result, err := updateAlertGroup(ctx, UpdateAlertGroupParams{
		AlertGroupID: "AG123",
		State:        "unresolved",
	})
	// The write already landed, so a failed read-back must not be reported as a
	// failed tool call: that would invite a retry of a completed mutation.
	require.NoError(t, err)
	assert.True(t, result.Updated)
	assert.Equal(t, "unresolve", result.Action)
	assert.Empty(t, result.State)
	assert.Contains(t, result.StateWarning, "the unresolve succeeded")
	assert.Contains(t, requests, "POST /api/v1/alert_groups/AG123/unresolve")
}

func TestUpdateAlertGroupFailsWhenActionFails(t *testing.T) {
	var requests []string
	srv := oncallAPITestServer(t, &requests, nil, http.StatusForbidden)

	ctx := mcpgrafana.WithGrafanaConfig(context.Background(), mcpgrafana.GrafanaConfig{
		URL:    srv.URL,
		APIKey: "oncall-token",
	})

	_, err := updateAlertGroup(ctx, UpdateAlertGroupParams{
		AlertGroupID: "AG123",
		State:        "unresolved",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unresolve OnCall alert group AG123")
	assert.NotContains(t, requests, "GET /api/v1/alert_groups/AG123/",
		"no state should be read back when the action itself failed")
}

func TestUpdateAlertGroupUsesProxyWhenOboTokensAvailable(t *testing.T) {
	var requests []string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)

		switch {
		case r.Method == http.MethodPost &&
			r.URL.Path == "/api/plugins/grafana-irm-app/resources/alertgroups/AG123/resolve/":
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodGet &&
			r.URL.Path == "/api/plugins/grafana-irm-app/resources/alertgroups/AG123/":
			w.Header().Set("Content-Type", "application/json")
			// Status 2 is "resolved" in the internal API's numeric encoding.
			require.NoError(t, json.NewEncoder(w).Encode(onCallAlertGroupInternal{
				PK:          "AG123",
				AlertsCount: 1,
				Status:      float64(2),
			}))
		default:
			http.Error(w, "unexpected request", http.StatusTeapot)
		}
	}))
	defer srv.Close()

	ctx := mcpgrafana.WithGrafanaConfig(context.Background(), mcpgrafana.GrafanaConfig{
		URL:         srv.URL,
		AccessToken: "test-access",
		IDToken:     "test-id",
	})

	result, err := updateAlertGroup(ctx, UpdateAlertGroupParams{
		AlertGroupID: "AG123",
		State:        "resolved",
	})
	require.NoError(t, err)
	assert.Equal(t, &UpdateAlertGroupResult{
		AlertGroupID: "AG123",
		Action:       "resolve",
		Updated:      true,
		State:        "resolved",
	}, result)
	assert.Equal(t, []string{
		"POST /api/plugins/grafana-irm-app/resources/alertgroups/AG123/resolve/",
		"GET /api/plugins/grafana-irm-app/resources/alertgroups/AG123/",
	}, requests)
}

func TestUpdateAlertGroupRejectsBadInputWithoutCallingOnCall(t *testing.T) {
	// No Grafana config in the context, so any HTTP attempt would error with a
	// connection or settings failure rather than the validation message.
	ctx := context.Background()

	_, err := updateAlertGroup(ctx, UpdateAlertGroupParams{AlertGroupID: "AG123", State: "silenced"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), `unsupported alert group state "silenced"`)

	_, err = updateAlertGroup(ctx, UpdateAlertGroupParams{AlertGroupID: "  ", State: "resolved"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "alertGroupId is required")
}
