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

// TestManageRulesRead_ReceiverFilterPagesBeforeLimit checks that the receiver
// filter runs on each page, so paging continues until rule_limit rules match.
// The first page holds only a rule for another receiver.
func TestManageRulesRead_ReceiverFilterPagesBeforeLimit(t *testing.T) {
	rule := func(uid string) map[string]any { return map[string]any{"name": uid, "uid": uid} }
	pages := map[string]map[string]any{
		"": {"groupNextToken": "p2", "groups": []any{
			map[string]any{"name": "a", "rules": []any{rule("email-rule")}},
		}},
		"p2": {"groups": []any{
			map[string]any{"name": "b", "rules": []any{rule("oncall-rule"), rule("oncall-rule-2")}},
		}},
	}
	var tokens []string
	provisioningReads := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/prometheus/grafana/api/v1/rules":
			require.Equal(t, "1", r.URL.Query().Get("rule_limit"))
			token := r.URL.Query().Get("group_next_token")
			tokens = append(tokens, token)
			page, ok := pages[token]
			require.True(t, ok, "unknown token %q", token)
			require.NoError(t, json.NewEncoder(w).Encode(map[string]any{"status": "success", "data": page}))
		case "/api/v1/provisioning/alert-rules":
			provisioningReads++
			_, _ = w.Write([]byte(`[
  {"uid": "email-rule", "notification_settings": {"receiver": "email"}},
  {"uid": "oncall-rule", "notification_settings": {"receiver": "oncall"}},
  {"uid": "oncall-rule-2", "notification_settings": {"receiver": "oncall"}}
]`))
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	ctx := mcpgrafana.WithGrafanaConfig(mockCtxWithClient(server), mcpgrafana.GrafanaConfig{URL: server.URL, APIKey: "test-api-key"})

	list := func(cursor string) *alertRuleList {
		t.Helper()
		receiver := "oncall"
		result, err := manageRulesRead(ctx, ManageRulesReadParams{
			Operation:        "list",
			listFilterParams: listFilterParams{RuleLimit: 1, Receiver: &receiver, Cursor: cursor},
		})
		require.NoError(t, err)
		return result.(*alertRuleList)
	}

	first := list("")
	require.Len(t, first.Rules, 1)
	require.Equal(t, "oncall-rule", first.Rules[0].UID, "the match on the second page is found")
	require.NotNil(t, first.Rules[0].NotificationSettings)
	require.True(t, first.Truncated)
	require.Equal(t, []string{"", "p2"}, tokens)
	require.Equal(t, 1, provisioningReads, "provisioning config is read once per list call")

	second := list(first.NextCursor)
	require.Len(t, second.Rules, 1)
	require.Equal(t, "oncall-rule-2", second.Rules[0].UID, "cursor resumes inside the filtered page")
	require.False(t, second.Truncated)
}

func TestManageRulesRead_ReceiverWithDatasourceUID(t *testing.T) {
	receiver := "oncall"
	datasourceUID := "prom-uid"
	_, err := manageRulesRead(context.Background(), ManageRulesReadParams{
		Operation:        "list",
		DatasourceUID:    &datasourceUID,
		listFilterParams: listFilterParams{Receiver: &receiver},
	})
	require.ErrorContains(t, err, "receiver filters Grafana-managed rules only and cannot be combined with datasource_uid")
}
