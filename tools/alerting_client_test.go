package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	grafanaModels "github.com/grafana/grafana-openapi-client-go/models"
	"github.com/prometheus/prometheus/model/labels"
	"github.com/stretchr/testify/require"

	mcpgrafana "github.com/grafana/mcp-grafana"
)

var (
	fakeruleGroup = ruleGroup{
		Name:      "TestGroup",
		FolderUID: "test-folder",
		Rules: []alertingRule{
			{
				State:     "firing",
				Name:      "Test Alert Rule",
				UID:       "test-rule-uid",
				FolderUID: "test-folder",
				Labels:    labels.New(labels.Label{Name: "severity", Value: "critical"}),
				Alerts: []alert{
					{
						Labels:      labels.New(labels.Label{Name: "instance", Value: "test-instance"}),
						Annotations: labels.New(labels.Label{Name: "summary", Value: "Test alert firing"}),
						State:       "firing",
						Value:       "1",
					},
				},
			},
		},
	}
)

func setupMockServer(handler http.HandlerFunc) (*httptest.Server, *alertingClient) {
	server := httptest.NewServer(handler)
	baseURL, _ := url.Parse(server.URL)
	client := &alertingClient{
		baseURL:    baseURL,
		apiKey:     "test-api-key",
		httpClient: &http.Client{},
	}
	return server, client
}

func mockrulesResponse() rulesResponse {
	resp := rulesResponse{}
	resp.Data.RuleGroups = []ruleGroup{fakeruleGroup}
	return resp
}

func TestAlertingClient_GetRules(t *testing.T) {
	server, client := setupMockServer(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/prometheus/grafana/api/v1/rules", r.URL.Path)
		require.Equal(t, "Bearer test-api-key", r.Header.Get("Authorization"))

		resp := mockrulesResponse()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		err := json.NewEncoder(w).Encode(resp)
		require.NoError(t, err)
	})
	defer server.Close()

	rules, err := client.GetRules(context.Background(), nil)
	require.NoError(t, err)
	require.NotNil(t, rules)
	require.ElementsMatch(t, rules.Data.RuleGroups, []ruleGroup{fakeruleGroup})
}

func TestAlertingClient_GetRules_Error(t *testing.T) {
	t.Run("internal server error", func(t *testing.T) {
		server, client := setupMockServer(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			_, err := w.Write([]byte("internal server error"))
			require.NoError(t, err)
		})
		defer server.Close()

		rules, err := client.GetRules(context.Background(), nil)
		require.Error(t, err)
		require.Nil(t, rules)
		require.ErrorContains(t, err, "grafana API returned status code 500: internal server error")
	})

	t.Run("network error", func(t *testing.T) {
		server, client := setupMockServer(func(w http.ResponseWriter, r *http.Request) {})
		server.Close()

		rules, err := client.GetRules(context.Background(), nil)

		require.Error(t, err)
		require.Nil(t, rules)
		require.ErrorContains(t, err, "failed to execute request")
	})
}

func TestGetRulesOpts_queryValues(t *testing.T) {
	tests := []struct {
		name     string
		opts     GetRulesOpts
		expected url.Values
	}{
		{
			name:     "empty opts produces empty values",
			opts:     GetRulesOpts{},
			expected: url.Values{},
		},
		{
			name: "all fields populated",
			opts: GetRulesOpts{
				FolderUID:    "folder-1",
				SearchFolder: "Grafana/Alerts",
				RuleGroup:    "group-1",
				RuleName:     "my-rule",
				RuleType:     "alerting",
				States:       []string{"firing", "pending"},
				RuleLimit:    10,
				LimitAlerts:  5,
				Matchers:     []*labels.Matcher{labels.MustNewMatcher(labels.MatchEqual, "severity", "critical")},
			},
			expected: url.Values{
				"folder_uid":       {"folder-1"},
				"search.folder":    {"Grafana/Alerts"},
				"rule_group":       {"group-1"},
				"search.rule_name": {"my-rule"},
				"rule_type":        {"alerting"},
				"state":            {"firing", "pending"},
				"rule_limit":       {"10"},
				"limit_alerts":     {"5"},
				"matcher":          {`{"Type":0,"Name":"severity","Value":"critical"}`},
			},
		},
		{
			name: "single filter",
			opts: GetRulesOpts{
				FolderUID: "abc",
			},
			expected: url.Values{
				"folder_uid": {"abc"},
			},
		},
		{
			name: "zero limit is omitted",
			opts: GetRulesOpts{
				RuleLimit:   0,
				LimitAlerts: 0,
			},
			expected: url.Values{},
		},
		{
			name: "search_folder is mapped",
			opts: GetRulesOpts{
				SearchFolder: "Grafana/Alerts",
			},
			expected: url.Values{
				"search.folder": {"Grafana/Alerts"},
			},
		},
		{
			name: "rule_limit capped at 200",
			opts: GetRulesOpts{
				RuleLimit: 500,
			},
			expected: url.Values{
				"rule_limit": {"200"},
			},
		},
		{
			name: "limit_alerts capped at 200",
			opts: GetRulesOpts{
				LimitAlerts: 999,
			},
			expected: url.Values{
				"limit_alerts": {"200"},
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

func TestAlertingClient_GetRules_WithOpts(t *testing.T) {
	tests := []struct {
		name          string
		opts          *GetRulesOpts
		assertRequest func(t *testing.T, r *http.Request)
	}{
		{
			name: "nil opts produces no query params",
			opts: nil,
			assertRequest: func(t *testing.T, r *http.Request) {
				t.Helper()
				require.Equal(t, "", r.URL.RawQuery, "nil opts should produce no query params")
			},
		},
		{
			name: "basic filters",
			opts: &GetRulesOpts{
				FolderUID: "test-folder",
				RuleGroup: "test-group",
				RuleType:  "alerting",
				RuleLimit: 10,
			},
			assertRequest: func(t *testing.T, r *http.Request) {
				t.Helper()
				q := r.URL.Query()
				require.Equal(t, "test-folder", q.Get("folder_uid"))
				require.Equal(t, "test-group", q.Get("rule_group"))
				require.Equal(t, "alerting", q.Get("rule_type"))
				require.Equal(t, "10", q.Get("rule_limit"))
			},
		},
		{
			name: "multiple states",
			opts: &GetRulesOpts{
				States: []string{"firing", "pending"},
			},
			assertRequest: func(t *testing.T, r *http.Request) {
				t.Helper()
				require.Equal(t, []string{"firing", "pending"}, r.URL.Query()["state"])
			},
		},
		{
			name: "search and matcher",
			opts: &GetRulesOpts{
				RuleName:    "cpu-alert",
				LimitAlerts: 5,
				Matchers:    []*labels.Matcher{labels.MustNewMatcher(labels.MatchEqual, "severity", "critical")},
			},
			assertRequest: func(t *testing.T, r *http.Request) {
				t.Helper()
				q := r.URL.Query()
				require.Equal(t, "cpu-alert", q.Get("search.rule_name"))
				require.Equal(t, `{"Type":0,"Name":"severity","Value":"critical"}`, q.Get("matcher"))
				require.Equal(t, "5", q.Get("limit_alerts"))
			},
		},
		{
			name: "all fields",
			opts: &GetRulesOpts{
				FolderUID:    "prod-folder",
				SearchFolder: "Grafana/Alerts",
				RuleGroup:    "infra-group",
				RuleName:     "high-cpu",
				RuleType:     "alerting",
				States:       []string{"firing", "pending"},
				RuleLimit:    20,
				LimitAlerts:  3,
				Matchers:     []*labels.Matcher{labels.MustNewMatcher(labels.MatchEqual, "team", "alerting")},
			},
			assertRequest: func(t *testing.T, r *http.Request) {
				t.Helper()
				q := r.URL.Query()
				require.Equal(t, "prod-folder", q.Get("folder_uid"))
				require.Equal(t, "Grafana/Alerts", q.Get("search.folder"))
				require.Equal(t, "infra-group", q.Get("rule_group"))
				require.Equal(t, "high-cpu", q.Get("search.rule_name"))
				require.Equal(t, "alerting", q.Get("rule_type"))
				require.Equal(t, []string{"firing", "pending"}, q["state"])
				require.Equal(t, "20", q.Get("rule_limit"))
				require.Equal(t, "3", q.Get("limit_alerts"))
				require.Equal(t, `{"Type":0,"Name":"team","Value":"alerting"}`, q.Get("matcher"))
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			server, client := setupMockServer(func(w http.ResponseWriter, r *http.Request) {
				require.Equal(t, "/api/prometheus/grafana/api/v1/rules", r.URL.Path)
				tc.assertRequest(t, r)

				resp := mockrulesResponse()
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				err := json.NewEncoder(w).Encode(resp)
				require.NoError(t, err)
			})
			defer server.Close()

			rules, err := client.GetRules(context.Background(), tc.opts)
			require.NoError(t, err)
			require.NotNil(t, rules)
		})
	}
}

func TestAlertingClient_GetRuleVersions(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		server, client := setupMockServer(func(w http.ResponseWriter, r *http.Request) {
			require.Equal(t, "/api/ruler/grafana/api/v1/rule/test-uid/versions", r.URL.Path)
			require.Equal(t, "Bearer test-api-key", r.Header.Get("Authorization"))

			resp := []grafanaModels.GettableExtendedRuleNode{
				{
					GrafanaAlert: &grafanaModels.GettableGrafanaRule{
						UID:     "test-uid",
						Title:   "Test Rule",
						Version: 2,
					},
				},
				{
					GrafanaAlert: &grafanaModels.GettableGrafanaRule{
						UID:     "test-uid",
						Title:   "Test Rule Old",
						Version: 1,
					},
				},
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			err := json.NewEncoder(w).Encode(resp)
			require.NoError(t, err)
		})
		defer server.Close()

		versions, err := client.GetRuleVersions(context.Background(), "test-uid")
		require.NoError(t, err)
		require.Len(t, versions, 2)
		require.Equal(t, "test-uid", versions[0].GrafanaAlert.UID)
		require.Equal(t, int64(2), versions[0].GrafanaAlert.Version)
	})

	t.Run("not found", func(t *testing.T) {
		server, client := setupMockServer(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
			_, err := w.Write([]byte("rule not found"))
			require.NoError(t, err)
		})
		defer server.Close()

		versions, err := client.GetRuleVersions(context.Background(), "nonexistent")
		require.Error(t, err)
		require.Nil(t, versions)
		require.ErrorContains(t, err, "404")
	})
}

func TestNewAlertingClientFromContext(t *testing.T) {
	config := mcpgrafana.GrafanaConfig{
		URL:    "http://localhost:3000/",
		APIKey: "test-api-key",
	}
	ctx := mcpgrafana.WithGrafanaConfig(context.Background(), config)

	client, err := newAlertingClientFromContext(ctx)
	require.NoError(t, err)

	require.Equal(t, "http://localhost:3000", client.baseURL.String())
	require.Equal(t, "test-api-key", client.apiKey)
	require.NotNil(t, client.httpClient)
}
