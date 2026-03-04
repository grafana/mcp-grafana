// Requires a Grafana instance running on localhost:3000,
// with alert rules, contact points, and routing config provisioned.
// Run with `go test -tags integration`.
//go:build integration

package tools

import (
	"testing"

	"github.com/grafana/grafana-openapi-client-go/models"
	"github.com/stretchr/testify/require"
)

func TestManageRouting(t *testing.T) {
	tests := []struct {
		name     string
		params   ManageRoutingParams
		wantErr  string
		assertFn func(t *testing.T, result any)
	}{
		// get_contact_points
		{
			name:   "get_contact_points lists all",
			params: ManageRoutingParams{Operation: "get_contact_points"},
			assertFn: func(t *testing.T, result any) {
				cps, ok := result.([]contactPointSummary)
				require.True(t, ok)
				require.ElementsMatch(t, allExpectedContactPoints, cps)
			},
		},
		{
			name: "get_contact_points with name filter",
			params: ManageRoutingParams{
				Operation: "get_contact_points",
				Name:      strPtr("Email1"),
			},
			assertFn: func(t *testing.T, result any) {
				cps, ok := result.([]contactPointSummary)
				require.True(t, ok)
				require.Len(t, cps, 1)
				require.Equal(t, "Email1", cps[0].Name)
			},
		},
		{
			name: "get_contact_points with limit",
			params: ManageRoutingParams{
				Operation: "get_contact_points",
				Limit:     1,
			},
			assertFn: func(t *testing.T, result any) {
				cps, ok := result.([]contactPointSummary)
				require.True(t, ok)
				require.Len(t, cps, 1)
			},
		},
		{
			name: "get_contact_points with invalid limit",
			params: ManageRoutingParams{
				Operation: "get_contact_points",
				Limit:     -1,
			},
			wantErr: "invalid limit",
		},
		{
			name: "get_contact_points from Alertmanager datasource",
			params: ManageRoutingParams{
				Operation:     "get_contact_points",
				DatasourceUID: strPtr("alertmanager"),
			},
			assertFn: func(t *testing.T, result any) {
				cps, ok := result.([]contactPointSummary)
				require.True(t, ok)
				require.NotEmpty(t, cps)

				names := make([]string, 0, len(cps))
				for _, cp := range cps {
					names = append(names, cp.Name)
				}
				require.Contains(t, names, "test-receiver")
			},
		},

		// get_contact_point
		{
			name: "get_contact_point by title",
			params: ManageRoutingParams{
				Operation:         "get_contact_point",
				ContactPointTitle: strPtr("Email1"),
			},
			assertFn: func(t *testing.T, result any) {
				cps, ok := result.([]*models.EmbeddedContactPoint)
				require.True(t, ok)
				require.NotEmpty(t, cps)
				require.Equal(t, "Email1", cps[0].Name)
			},
		},
		{
			name: "get_contact_point non-existent",
			params: ManageRoutingParams{
				Operation:         "get_contact_point",
				ContactPointTitle: strPtr("NonExistentContactPoint"),
			},
			wantErr: "not found",
		},
		{
			name:    "get_contact_point without title",
			params:  ManageRoutingParams{Operation: "get_contact_point"},
			wantErr: "contact_point_title is required",
		},

		// get_notification_policies
		{
			name:   "get_notification_policies returns policy tree with routes",
			params: ManageRoutingParams{Operation: "get_notification_policies"},
			assertFn: func(t *testing.T, result any) {
				route, ok := result.(*models.Route)
				require.True(t, ok)
				require.NotNil(t, route)
				require.NotEmpty(t, route.Routes, "expected at least one child route")
			},
		},

		// get_time_intervals
		{
			name:   "get_time_intervals returns provisioned intervals",
			params: ManageRoutingParams{Operation: "get_time_intervals"},
			assertFn: func(t *testing.T, result any) {
				intervals, ok := result.([]muteTimingSummary)
				require.True(t, ok)
				require.NotEmpty(t, intervals)

				names := make([]string, 0, len(intervals))
				for _, ti := range intervals {
					names = append(names, ti.Name)
				}
				require.Contains(t, names, "weekends")
			},
		},

		// get_time_interval
		{
			name: "get_time_interval returns provisioned interval",
			params: ManageRoutingParams{
				Operation:        "get_time_interval",
				TimeIntervalName: strPtr("weekends"),
			},
			assertFn: func(t *testing.T, result any) {
				mt, ok := result.(*models.MuteTimeInterval)
				require.True(t, ok)
				require.Equal(t, "weekends", mt.Name)
				require.NotEmpty(t, mt.TimeIntervals)
			},
		},
		{
			name: "get_time_interval non-existent",
			params: ManageRoutingParams{
				Operation:        "get_time_interval",
				TimeIntervalName: strPtr("nonexistent-interval"),
			},
			wantErr: "get time interval",
		},
		{
			name:    "get_time_interval without name",
			params:  ManageRoutingParams{Operation: "get_time_interval"},
			wantErr: "time_interval_name is required",
		},

		// invalid operation
		{
			name:    "unknown operation",
			params:  ManageRoutingParams{Operation: "delete_everything"},
			wantErr: "unknown operation",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := newTestContext()
			result, err := manageRouting(ctx, tc.params)
			if tc.wantErr != "" {
				require.Error(t, err)
				require.Contains(t, err.Error(), tc.wantErr)
				return
			}
			require.NoError(t, err)
			if tc.assertFn != nil {
				tc.assertFn(t, result)
			}
		})
	}
}
