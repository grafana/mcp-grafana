//go:build integration

package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	mcpgrafana "github.com/grafana/mcp-grafana/v2"
	"github.com/stretchr/testify/require"
)

// Opt in against a disposable Grafana with writable Mimir datasources named by
// EXTERNAL_RULER_UID and EXTERNAL_ALERTMANAGER_UID. This test restores the entire
// Alertmanager config, so no other writers should use the instance during it.
func TestExternalAlertingIntegration(t *testing.T) {
	if os.Getenv("EXTERNAL_ALERTING_INTEGRATION") != "1" {
		t.Skip("requires a disposable Grafana and writable Mimir ruler/Alertmanager")
	}
	base := os.Getenv("GRAFANA_URL")
	rulerUID := os.Getenv("EXTERNAL_RULER_UID")
	amUID := os.Getenv("EXTERNAL_ALERTMANAGER_UID")
	require.NotEmpty(t, base)
	require.NotEmpty(t, rulerUID)
	require.NotEmpty(t, amUID)
	cfg := mcpgrafana.GrafanaConfig{URL: base, BasicAuth: url.UserPassword(os.Getenv("GRAFANA_USERNAME"), os.Getenv("GRAFANA_PASSWORD"))}
	ctx, cancel := context.WithTimeout(mcpgrafana.WithGrafanaConfig(t.Context(), cfg), time.Minute)
	defer cancel()
	client, err := newAlertingClientFromContext(ctx)
	require.NoError(t, err)

	t.Run("rules", func(t *testing.T) {
		args := ManageRulesReadWriteParams{
			Operation: "create", ManagedBy: "datasource", DatasourceUID: &rulerUID,
			Title: "FirstAlert", Namespace: "mcp-test-" + uuid.NewString(), RuleGroup: "test/group",
			Expression: "vector(0) > 1", For: "5m", EvaluationInterval: "30s",
		}
		path := "/api/ruler/" + rulerUID + "/api/v1/rules/" + args.Namespace
		query := url.Values{"subtype": {"mimir"}}
		t.Cleanup(func() {
			cleanupCtx := mcpgrafana.WithGrafanaConfig(context.Background(), cfg)
			_, status, err := client.externalAlertingRequest(cleanupCtx, http.MethodDelete, path, query, nil)
			require.NoError(t, err)
			require.Contains(t, []int{200, 202, 204}, status)
		})
		_, err := manageRulesReadWrite(ctx, args)
		require.NoError(t, err)
		args.Title = "SecondAlert"
		args.EvaluationInterval = ""
		_, err = manageRulesReadWrite(ctx, args)
		require.NoError(t, err)
		query.Set("group", args.RuleGroup)
		body, status, err := client.externalAlertingRequest(ctx, http.MethodGet, path+"/QUERY_GROUP", query, nil)
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, status)
		var group struct {
			Interval string `json:"interval"`
			Rules    []struct {
				Alert string `json:"alert"`
			} `json:"rules"`
		}
		require.NoError(t, json.Unmarshal(body, &group))
		require.Equal(t, "30s", group.Interval)
		require.Len(t, group.Rules, 2)
		require.Equal(t, "FirstAlert", group.Rules[0].Alert)
		require.Equal(t, "SecondAlert", group.Rules[1].Alert)
		_, err = manageRulesReadWrite(ctx, args)
		require.ErrorContains(t, err, "already exists")
	})

	t.Run("receivers", func(t *testing.T) {
		path := "/api/alertmanager/" + amUID + "/config/api/v1/alerts"
		before, status, err := client.externalAlertingRequest(ctx, http.MethodGet, path, nil, nil)
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, status)
		original, err := decodeExternalAlertingObject(before)
		require.NoError(t, err)
		t.Cleanup(func() {
			cleanupCtx := mcpgrafana.WithGrafanaConfig(context.Background(), cfg)
			require.NoError(t, client.postExternalAlerting(cleanupCtx, path, nil, original))
		})
		name := "mcp-test-" + uuid.NewString()
		result, err := manageRoutingWrite(ctx, ManageRoutingWriteParams{
			Operation: "create_contact_point", ManagedBy: "datasource", DatasourceUID: amUID,
			Name: name, Type: "webhook", Settings: map[string]any{"url": "http://localhost:1/unused-test-hook"},
		})
		require.NoError(t, err)
		require.Equal(t, amUID, result.DatasourceUID)
		after, status, err := client.externalAlertingRequest(ctx, http.MethodGet, path, nil, nil)
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, status)
		updated, err := decodeExternalAlertingObject(after)
		require.NoError(t, err)
		am, err := decodeExternalAlertingObject(updated["alertmanager_config"])
		require.NoError(t, err)
		var receivers []json.RawMessage
		require.NoError(t, json.Unmarshal(am["receivers"], &receivers))
		require.Greater(t, len(receivers), 1)
		var created struct {
			Name string `json:"name"`
		}
		require.NoError(t, json.Unmarshal(receivers[len(receivers)-1], &created))
		require.Equal(t, name, created.Name)
		require.NoError(t, setExternalAlertingField(am, "receivers", receivers[:len(receivers)-1]))
		priorAM, err := decodeExternalAlertingObject(original["alertmanager_config"])
		require.NoError(t, err)
		// Compare without printing configurations, which can include credentials.
		for key, value := range priorAM {
			require.True(t, jsonSemanticallyEqual(value, am[key]), "changed existing Alertmanager field %s", key)
		}
		require.True(t, jsonSemanticallyEqual(original["template_files"], updated["template_files"]), "changed templates")
	})
}

func jsonSemanticallyEqual(a, b []byte) bool {
	var left, right any
	if json.Unmarshal(a, &left) != nil || json.Unmarshal(b, &right) != nil {
		return false
	}
	l, err := json.Marshal(left)
	if err != nil {
		return false
	}
	r, err := json.Marshal(right)
	return err == nil && string(l) == string(r)
}
