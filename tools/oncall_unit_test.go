//go:build unit
// +build unit

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

func TestAlertGroupActionForState(t *testing.T) {
	tests := []struct {
		name       string
		state      string
		wantAction string
		wantErr    bool
	}{
		{
			name:       "acknowledged",
			state:      "acknowledged",
			wantAction: "acknowledge",
		},
		{
			name:       "unacknowledged",
			state:      "unacknowledged",
			wantAction: "unacknowledge",
		},
		{
			name:       "resolved",
			state:      "resolved",
			wantAction: "resolve",
		},
		{
			name:       "unresolved",
			state:      "unresolved",
			wantAction: "unresolve",
		},
		{
			name:       "normalizes case and whitespace",
			state:      " Resolved ",
			wantAction: "resolve",
		},
		{
			name:    "rejects unsupported state",
			state:   "silenced",
			wantErr: true,
		},
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

func TestUpdateAlertGroupDoesNotFetchAfterWrite(t *testing.T) {
	var requests []string

	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)

		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/plugins/grafana-irm-app/settings":
			require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
				"jsonData": map[string]string{"onCallApiUrl": srv.URL},
			}))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/alert_groups/AG123/acknowledge":
			w.WriteHeader(http.StatusNoContent)
		default:
			http.Error(w, "unexpected request", http.StatusTeapot)
		}
	}))
	defer srv.Close()

	ctx := mcpgrafana.WithGrafanaConfig(context.Background(), mcpgrafana.GrafanaConfig{
		URL:    srv.URL,
		APIKey: "oncall-token",
	})

	result, err := updateAlertGroup(ctx, UpdateAlertGroupParams{
		AlertGroupID: "AG123",
		State:        "acknowledged",
	})
	require.NoError(t, err)

	assert.Equal(t, &UpdateAlertGroupResult{
		AlertGroupID: "AG123",
		State:        "acknowledged",
		Action:       "acknowledge",
		Updated:      true,
	}, result)
	assert.Equal(t, []string{
		"GET /api/plugins/grafana-irm-app/settings",
		"POST /api/v1/alert_groups/AG123/acknowledge",
	}, requests)
}
