package tools

import (
	"testing"

	"github.com/stretchr/testify/require"
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

func strPtr(s string) *string {
	return &s
}
