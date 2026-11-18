package tools

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/grafana/grafana-openapi-client-go/models"
	"github.com/stretchr/testify/require"

	mcpgrafana "github.com/grafana/mcp-grafana/v2"
)

func TestManageRoutingParams_Validate(t *testing.T) {
	tests := []struct {
		name    string
		params  ManageRoutingParams
		wantErr string
	}{
		{
			name:   "get_notification_policies is valid",
			params: ManageRoutingParams{Operation: "get_notification_policies"},
		},
		{
			name:   "get_contact_points with defaults",
			params: ManageRoutingParams{Operation: "get_contact_points"},
		},
		{
			name: "get_contact_points with name filter",
			params: ManageRoutingParams{
				Operation: "get_contact_points",
				Name:      strPtr("my-receiver"),
			},
		},
		{
			name: "get_contact_points with datasource_uid",
			params: ManageRoutingParams{
				Operation:     "get_contact_points",
				DatasourceUID: strPtr("am-datasource-1"),
			},
		},
		{
			name: "get_contact_points with limit",
			params: ManageRoutingParams{
				Operation: "get_contact_points",
				Limit:     50,
			},
		},
		{
			name: "get_contact_points with all params",
			params: ManageRoutingParams{
				Operation:     "get_contact_points",
				DatasourceUID: strPtr("am-uid"),
				Name:          strPtr("email-team"),
				Limit:         10,
			},
		},
		{
			name: "get_contact_points with zero limit (uses default)",
			params: ManageRoutingParams{
				Operation: "get_contact_points",
				Limit:     0,
			},
		},
		{
			name: "get_contact_points with negative limit",
			params: ManageRoutingParams{
				Operation: "get_contact_points",
				Limit:     -1,
			},
			wantErr: "invalid limit",
		},
		{
			name: "get_contact_point with title",
			params: ManageRoutingParams{
				Operation:         "get_contact_point",
				ContactPointTitle: strPtr("Slack Alerts"),
			},
		},
		{
			name:    "get_contact_point without title",
			params:  ManageRoutingParams{Operation: "get_contact_point"},
			wantErr: "contact_point_title is required",
		},
		{
			name: "get_contact_point with empty title",
			params: ManageRoutingParams{
				Operation:         "get_contact_point",
				ContactPointTitle: strPtr(""),
			},
			wantErr: "contact_point_title is required",
		},
		{
			name:   "get_time_intervals is valid",
			params: ManageRoutingParams{Operation: "get_time_intervals"},
		},
		{
			name: "get_time_interval with name",
			params: ManageRoutingParams{
				Operation:        "get_time_interval",
				TimeIntervalName: strPtr("weekends"),
			},
		},
		{
			name:    "get_time_interval without name",
			params:  ManageRoutingParams{Operation: "get_time_interval"},
			wantErr: "time_interval_name is required",
		},
		{
			name: "get_time_interval with empty name",
			params: ManageRoutingParams{
				Operation:        "get_time_interval",
				TimeIntervalName: strPtr(""),
			},
			wantErr: "time_interval_name is required",
		},
		{
			name:    "unknown operation",
			params:  ManageRoutingParams{Operation: "create_contact_point"},
			wantErr: "unknown operation",
		},
		{
			name:    "empty operation",
			params:  ManageRoutingParams{Operation: ""},
			wantErr: "unknown operation",
		},
		{
			name:    "list operation not supported",
			params:  ManageRoutingParams{Operation: "list"},
			wantErr: "unknown operation",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.params.validate()
			if tc.wantErr != "" {
				require.Error(t, err)
				require.Contains(t, err.Error(), tc.wantErr)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestManageRoutingParams_ToListContactPointsParams(t *testing.T) {
	tests := []struct {
		name      string
		params    ManageRoutingParams
		wantDSUID *string
		wantName  *string
		wantLimit int
	}{
		{
			name: "converts all fields",
			params: ManageRoutingParams{
				Operation:     "get_contact_points",
				DatasourceUID: strPtr("am-datasource-1"),
				Name:          strPtr("email-team"),
				Limit:         25,
			},
			wantDSUID: strPtr("am-datasource-1"),
			wantName:  strPtr("email-team"),
			wantLimit: 25,
		},
		{
			name: "nil optional fields stay nil",
			params: ManageRoutingParams{
				Operation: "get_contact_points",
			},
		},
		{
			name: "only datasource_uid set",
			params: ManageRoutingParams{
				Operation:     "get_contact_points",
				DatasourceUID: strPtr("prom-am"),
			},
			wantDSUID: strPtr("prom-am"),
		},
		{
			name: "only name set",
			params: ManageRoutingParams{
				Operation: "get_contact_points",
				Name:      strPtr("slack-notifications"),
			},
			wantName: strPtr("slack-notifications"),
		},
		{
			name: "only limit set",
			params: ManageRoutingParams{
				Operation: "get_contact_points",
				Limit:     100,
			},
			wantLimit: 100,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := tc.params.toListContactPointsParams()
			if tc.wantDSUID != nil {
				require.NotNil(t, result.DatasourceUID)
				require.Equal(t, *tc.wantDSUID, *result.DatasourceUID)
			} else {
				require.Nil(t, result.DatasourceUID)
			}
			if tc.wantName != nil {
				require.NotNil(t, result.Name)
				require.Equal(t, *tc.wantName, *result.Name)
			} else {
				require.Nil(t, result.Name)
			}
			require.Equal(t, tc.wantLimit, result.Limit)
		})
	}
}

func TestGetNotificationPolicies_StringMatchers(t *testing.T) {
	var gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"receiver": "default",
			"group_by": ["alertname"],
			"provenance": "api",
			"object_matchers": [["a", "=", "b"]],
			"routes": [{
				"receiver": "critical",
				"matchers": ["severity=\"critical\"", "env!=\"dev\"", "team!~\"ops|sre\""],
				"routes": [{"receiver": "db", "matchers": ["service.name=~\"db.*\""]}]
			}]
		}`))
	}))
	t.Cleanup(server.Close)
	ctx := mcpgrafana.WithGrafanaConfig(t.Context(), mcpgrafana.GrafanaConfig{URL: server.URL, APIKey: "test-api-key"})

	result, err := manageRouting(ctx, ManageRoutingParams{Operation: "get_notification_policies"})
	require.NoError(t, err)
	require.Equal(t, "/api/v1/provisioning/policies", gotPath)
	tree, ok := result.(*models.Route)
	require.True(t, ok)
	require.Equal(t, "default", tree.Receiver)
	require.Equal(t, []string{"alertname"}, tree.GroupBy)
	require.Equal(t, models.Provenance("api"), tree.Provenance)
	require.Equal(t, models.ObjectMatchers{{"a", "=", "b"}}, tree.ObjectMatchers)
	require.Empty(t, tree.Matchers)

	require.Len(t, tree.Routes, 1)
	critical := tree.Routes[0]
	require.Equal(t, "critical", critical.Receiver)
	require.Equal(t, models.Matchers{
		{Name: strPtr("severity"), Value: strPtr("critical"), IsEqual: true, IsRegex: boolPtr(false)},
		{Name: strPtr("env"), Value: strPtr("dev"), IsEqual: false, IsRegex: boolPtr(false)},
		{Name: strPtr("team"), Value: strPtr("ops|sre"), IsEqual: false, IsRegex: boolPtr(true)},
	}, critical.Matchers)

	require.Len(t, critical.Routes, 1)
	require.Equal(t, models.Matchers{
		{Name: strPtr("service.name"), Value: strPtr("db.*"), IsEqual: true, IsRegex: boolPtr(true)},
	}, critical.Routes[0].Matchers)
}

func TestGetNotificationPolicies_ErrorStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"message":"forbidden"}`, http.StatusForbidden)
	}))
	t.Cleanup(server.Close)
	ctx := mcpgrafana.WithGrafanaConfig(t.Context(), mcpgrafana.GrafanaConfig{URL: server.URL, APIKey: "test-api-key"})

	_, err := manageRouting(ctx, ManageRoutingParams{Operation: "get_notification_policies"})
	require.ErrorContains(t, err, "get notification policies")
	require.ErrorContains(t, err, "403")
}

func strPtr(s string) *string {
	return &s
}
