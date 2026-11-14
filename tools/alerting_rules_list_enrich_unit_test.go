//go:build unit

package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	mcpgrafana "github.com/grafana/mcp-grafana/v2"
)

const provisionedRulesForListJSON = `[
  {
    "uid": "test-rule-uid",
    "title": "Test Alert Rule",
    "isPaused": true,
    "notification_settings": {"receiver": "oncall"},
    "data": [
      {"refId": "A", "datasourceUid": "prom-uid"},
      {"refId": "B", "datasourceUid": "prom-uid"},
      {"refId": "C", "datasourceUid": "__expr__"}
    ]
  }
]`

// newRuleListContext serves the Prometheus-style rules list plus the bulk
// provisioning read that enriches it. provisioningStatus other than 200 makes
// the provisioning read fail, as it does for a token without that permission.
func newRuleListContext(t *testing.T, provisioningStatus int, withProvisioningClient bool) context.Context {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/prometheus/grafana/api/v1/rules":
			require.NoError(t, json.NewEncoder(w).Encode(mockrulesResponse()))
		case "/api/v1/provisioning/alert-rules":
			if provisioningStatus != http.StatusOK {
				w.WriteHeader(provisioningStatus)
				_, _ = w.Write([]byte(`{"message":"access denied"}`))
				return
			}
			_, _ = w.Write([]byte(provisionedRulesForListJSON))
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	ctx := context.Background()
	if withProvisioningClient {
		ctx = mockCtxWithClient(server)
	}
	return mcpgrafana.WithGrafanaConfig(ctx, mcpgrafana.GrafanaConfig{URL: server.URL, APIKey: "test-api-key"})
}

func listRulesForTest(ctx context.Context, receiver string) ([]alertRuleSummary, error) {
	params := ManageRulesReadParams{Operation: "list"}
	if receiver != "" {
		params.Receiver = &receiver
	}
	result, err := manageRulesRead(ctx, params)
	if err != nil {
		return nil, err
	}
	return result.(*alertRuleList).Rules, nil
}

func TestManageRulesRead_ListEnrichment(t *testing.T) {
	t.Run("list carries paused state, receiver and datasources", func(t *testing.T) {
		rules, err := listRulesForTest(newRuleListContext(t, http.StatusOK, true), "")
		require.NoError(t, err)
		require.Len(t, rules, 1)
		require.Equal(t, "test-rule-uid", rules[0].UID)
		require.NotNil(t, rules[0].IsPaused)
		require.True(t, *rules[0].IsPaused)
		require.NotNil(t, rules[0].NotificationSettings)
		require.Equal(t, "oncall", *rules[0].NotificationSettings.Receiver)
		require.Equal(t, []string{"prom-uid"}, rules[0].DatasourceUIDs)
	})

	t.Run("unfiltered list is still returned when provisioning is denied", func(t *testing.T) {
		rules, err := listRulesForTest(newRuleListContext(t, http.StatusForbidden, true), "")
		require.NoError(t, err)
		require.Len(t, rules, 1)
		require.Equal(t, "test-rule-uid", rules[0].UID)
		require.Nil(t, rules[0].IsPaused)
		require.Nil(t, rules[0].NotificationSettings)
		require.Empty(t, rules[0].DatasourceUIDs)
	})

	t.Run("unfiltered list is still returned without a provisioning client", func(t *testing.T) {
		rules, err := listRulesForTest(newRuleListContext(t, http.StatusOK, false), "")
		require.NoError(t, err)
		require.Len(t, rules, 1)
		require.Nil(t, rules[0].IsPaused)
	})
}

func TestManageRulesRead_ReceiverFilter(t *testing.T) {
	t.Run("matching receiver keeps the rule", func(t *testing.T) {
		rules, err := listRulesForTest(newRuleListContext(t, http.StatusOK, true), "oncall")
		require.NoError(t, err)
		require.Len(t, rules, 1)
		require.Equal(t, "test-rule-uid", rules[0].UID)
	})

	t.Run("non-matching receiver returns an empty list", func(t *testing.T) {
		rules, err := listRulesForTest(newRuleListContext(t, http.StatusOK, true), "email")
		require.NoError(t, err)
		require.Empty(t, rules)
	})

	// Without the provisioning config no rule has a receiver to match, so an
	// empty list would wrongly say that nothing notifies this receiver.
	t.Run("denied provisioning read is an error, not an empty list", func(t *testing.T) {
		rules, err := listRulesForTest(newRuleListContext(t, http.StatusForbidden, true), "oncall")
		require.Error(t, err)
		require.ErrorContains(t, err, `cannot filter by receiver "oncall"`)
		require.Nil(t, rules)
	})

	t.Run("missing provisioning client is an error, not an empty list", func(t *testing.T) {
		rules, err := listRulesForTest(newRuleListContext(t, http.StatusOK, false), "oncall")
		require.Error(t, err)
		require.ErrorContains(t, err, `cannot filter by receiver "oncall"`)
		require.Nil(t, rules)
	})
}
