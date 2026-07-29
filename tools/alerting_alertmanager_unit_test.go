package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"testing"

	"github.com/prometheus/alertmanager/api/v2/models"
	"github.com/prometheus/prometheus/model/labels"
	"github.com/stretchr/testify/require"
)

func boolPtr(b bool) *bool { return &b }

func TestGetAlertGroupsOpts_queryValues(t *testing.T) {
	tests := []struct {
		name     string
		opts     GetAlertGroupsOpts
		expected url.Values
	}{
		{
			name:     "empty opts produces empty values",
			opts:     GetAlertGroupsOpts{},
			expected: url.Values{},
		},
		{
			name: "active=true excludes other states",
			opts: GetAlertGroupsOpts{
				Active:    boolPtr(true),
				Silenced:  boolPtr(false),
				Inhibited: boolPtr(false),
			},
			expected: url.Values{
				"active":    {"true"},
				"silenced":  {"false"},
				"inhibited": {"false"},
			},
		},
		{
			name: "nil bools are omitted (API defaults apply)",
			opts: GetAlertGroupsOpts{
				Filter:   []string{"severity=critical"},
				Receiver: "team-slack",
			},
			expected: url.Values{
				"filter":   {"severity=critical"},
				"receiver": {"team-slack"},
			},
		},
		{
			name: "all fields populated",
			opts: GetAlertGroupsOpts{
				Active:    boolPtr(true),
				Silenced:  boolPtr(true),
				Inhibited: boolPtr(false),
				Filter:    []string{"severity=critical", "grafana_folder=Platform"},
				Receiver:  "oncall-slack",
			},
			expected: url.Values{
				"active":    {"true"},
				"silenced":  {"true"},
				"inhibited": {"false"},
				"filter":    {"severity=critical", "grafana_folder=Platform"},
				"receiver":  {"oncall-slack"},
			},
		},
		{
			name: "explicit false sent for all states",
			opts: GetAlertGroupsOpts{
				Active:    boolPtr(false),
				Silenced:  boolPtr(false),
				Inhibited: boolPtr(false),
			},
			expected: url.Values{
				"active":    {"false"},
				"silenced":  {"false"},
				"inhibited": {"false"},
			},
		},
		{
			name: "multiple filters",
			opts: GetAlertGroupsOpts{
				Filter: []string{"severity=critical", "alertname=HighCPU"},
			},
			expected: url.Values{
				"filter": {"severity=critical", "alertname=HighCPU"},
			},
		},
		{
			name: "receiver only",
			opts: GetAlertGroupsOpts{
				Receiver: "slack-ops",
			},
			expected: url.Values{
				"receiver": {"slack-ops"},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.opts.queryValues()
			require.Equal(t, tc.expected, got)
		})
	}
}

func TestAlertingClient_GetAlertGroups(t *testing.T) {
	mockAlertGroup := &models.AlertGroup{
		Labels:   models.LabelSet{"alertname": "HighCPU", "grafana_folder": "Production"},
		Receiver: &models.Receiver{Name: strPtr("slack-ops")},
		Alerts: []*models.GettableAlert{
			{
				Alert: models.Alert{
					Labels: models.LabelSet{"instance": "server-1"},
				},
				Annotations: models.LabelSet{
					"summary":     "CPU usage high",
					"description": "CPU usage above 90% on server-1",
				},
				Status: &models.AlertStatus{State: strPtr("active")},
			},
		},
	}

	t.Run("success without opts", func(t *testing.T) {
		server, client := setupMockServer(func(w http.ResponseWriter, r *http.Request) {
			require.Equal(t, "/api/alertmanager/grafana/api/v2/alerts/groups", r.URL.Path)
			require.Equal(t, "Bearer test-api-key", r.Header.Get("Authorization"))
			require.Empty(t, r.URL.RawQuery)

			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			err := json.NewEncoder(w).Encode([]*models.AlertGroup{mockAlertGroup})
			require.NoError(t, err)
		})
		defer server.Close()

		groups, err := client.GetAlertGroups(context.Background(), nil)
		require.NoError(t, err)
		require.Len(t, groups, 1)
		require.Equal(t, "HighCPU", groups[0].Labels["alertname"])
	})

	t.Run("success with opts", func(t *testing.T) {
		server, client := setupMockServer(func(w http.ResponseWriter, r *http.Request) {
			require.Equal(t, "/api/alertmanager/grafana/api/v2/alerts/groups", r.URL.Path)
			q := r.URL.Query()
			require.Equal(t, "true", q.Get("active"))
			require.Equal(t, "false", q.Get("silenced"))
			require.Equal(t, "false", q.Get("inhibited"))
			require.Equal(t, "slack-ops", q.Get("receiver"))
			require.Equal(t, []string{"severity=critical"}, q["filter"])

			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			err := json.NewEncoder(w).Encode([]*models.AlertGroup{mockAlertGroup})
			require.NoError(t, err)
		})
		defer server.Close()

		opts := &GetAlertGroupsOpts{
			Active:    boolPtr(true),
			Silenced:  boolPtr(false),
			Inhibited: boolPtr(false),
			Filter:    []string{"severity=critical"},
			Receiver:  "slack-ops",
		}
		groups, err := client.GetAlertGroups(context.Background(), opts)
		require.NoError(t, err)
		require.Len(t, groups, 1)
	})

	t.Run("server error", func(t *testing.T) {
		server, client := setupMockServer(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			_, err := w.Write([]byte("internal error"))
			require.NoError(t, err)
		})
		defer server.Close()

		groups, err := client.GetAlertGroups(context.Background(), nil)
		require.Error(t, err)
		require.Nil(t, groups)
		require.ErrorContains(t, err, "500")
	})

	t.Run("network error", func(t *testing.T) {
		server, client := setupMockServer(func(w http.ResponseWriter, r *http.Request) {})
		server.Close()

		groups, err := client.GetAlertGroups(context.Background(), nil)
		require.Error(t, err)
		require.Nil(t, groups)
		require.ErrorContains(t, err, "failed to execute request")
	})
}

func TestListNotificationGroupsTool(t *testing.T) {
	t.Run("tool is defined", func(t *testing.T) {
		require.NotNil(t, ListNotificationGroups)
		require.Equal(t, "alerting_list_notification_groups", ListNotificationGroups.Tool.Name)
	})
}

func TestParseMatcherStrings_AlertmanagerConversion(t *testing.T) {
	t.Run("simple equality", func(t *testing.T) {
		ls, err := parseMatcherStrings([]string{`severity="critical"`})
		require.NoError(t, err)
		require.Len(t, ls, 1)
		require.Equal(t, labels.MatchEqual, ls[0].Type)
		require.Equal(t, "severity", ls[0].Name)
		require.Equal(t, "critical", ls[0].Value)
	})

	t.Run("not equal", func(t *testing.T) {
		ls, err := parseMatcherStrings([]string{`alertname!="HighCPU"`})
		require.NoError(t, err)
		require.Len(t, ls, 1)
		require.Equal(t, labels.MatchNotEqual, ls[0].Type)
	})

	t.Run("regex", func(t *testing.T) {
		ls, err := parseMatcherStrings([]string{`env=~"prod.*"`})
		require.NoError(t, err)
		require.Len(t, ls, 1)
		require.Equal(t, labels.MatchRegexp, ls[0].Type)
	})

	t.Run("not regex", func(t *testing.T) {
		ls, err := parseMatcherStrings([]string{`env!~"dev.*"`})
		require.NoError(t, err)
		require.Len(t, ls, 1)
		require.Equal(t, labels.MatchNotRegexp, ls[0].Type)
	})

	t.Run("invalid matcher", func(t *testing.T) {
		ls, err := parseMatcherStrings([]string{`invalid`})
		require.Error(t, err)
		require.Nil(t, ls)
	})
}

