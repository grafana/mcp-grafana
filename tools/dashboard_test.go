// Requires a Grafana instance running on localhost:3000,
// with a dashboard provisioned.
// Run with `go test -tags integration`.
//go:build integration

package tools

import (
	"context"
	"testing"

	"github.com/grafana/grafana-openapi-client-go/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	newTestDashboardName = "Integration Test"
)

// getExistingDashboardUID will fetch an existing dashboard for test purposes
// It will search for exisiting dashboards and return the first, otherwise
// will trigger a test error
func getExistingTestDashboard(t *testing.T, ctx context.Context, dashboardName string) *models.Hit {
	// Make sure we query for the existing dashboard, not a folder
	if dashboardName == "" {
		dashboardName = "Demo"
	}
	searchResults, err := searchDashboards(ctx, SearchDashboardsParams{
		Query: dashboardName,
	})
	require.NoError(t, err)
	require.Greater(t, len(searchResults), 0, "No dashboards found")
	return searchResults[0]
}

// getExistingTestDashboardJSON will fetch the JSON map for an existing
// dashboard in the test environment
func getTestDashboardJSON(t *testing.T, ctx context.Context, dashboard *models.Hit) map[string]interface{} {
	result, err := getDashboardByUID(ctx, GetDashboardByUIDParams{
		UID: dashboard.UID,
	})
	require.NoError(t, err)
	dashboardMap, ok := result.Dashboard.(map[string]interface{})
	require.True(t, ok, "Dashboard should be a map")
	return dashboardMap
}

func TestDashboardTools(t *testing.T) {
	t.Run("get dashboard by uid", func(t *testing.T) {
		ctx := newTestContext()

		// First, let's search for a dashboard to get its UID
		dashboard := getExistingTestDashboard(t, ctx, "")

		// Now test the get dashboard by uid functionality
		result, err := getDashboardByUID(ctx, GetDashboardByUIDParams{
			UID: dashboard.UID,
		})
		require.NoError(t, err)
		dashboardMap, ok := result.Dashboard.(map[string]interface{})
		require.True(t, ok, "Dashboard should be a map")
		assert.Equal(t, dashboard.UID, dashboardMap["uid"])
		assert.NotNil(t, result.Meta)
	})

	t.Run("get dashboard by uid - invalid uid", func(t *testing.T) {
		ctx := newTestContext()

		_, err := getDashboardByUID(ctx, GetDashboardByUIDParams{
			UID: "non-existent-uid",
		})
		require.Error(t, err)
	})

	t.Run("update dashboard - create new", func(t *testing.T) {
		ctx := newTestContext()

		// Get the dashboard JSON
		// In this case, we will create a new dashboard with the same
		// content but different Title, and disable "overwrite"
		dashboard := getExistingTestDashboard(t, ctx, "")
		dashboardMap := getTestDashboardJSON(t, ctx, dashboard)

		// Avoid a clash by unsetting the existing IDs
		delete(dashboardMap, "uid")
		delete(dashboardMap, "id")

		// Set a new title and tag
		dashboardMap["title"] = newTestDashboardName
		dashboardMap["tags"] = []string{"integration-test"}

		params := UpdateDashboardParams{
			Dashboard: dashboardMap,
			Message:   "creating a new dashboard",
			Overwrite: false,
			UserID:    1,
		}

		// Only pass in the Folder UID if it exists
		if dashboard.FolderUID != "" {
			params.FolderUID = dashboard.FolderUID
		}

		// create the dashboard
		_, err := updateDashboard(ctx, params)
		require.NoError(t, err)
	})

	t.Run("update dashboard - overwrite existing", func(t *testing.T) {
		ctx := newTestContext()

		// Get the dashboard JSON for the non-provisioned dashboard we've created
		dashboard := getExistingTestDashboard(t, ctx, newTestDashboardName)
		dashboardMap := getTestDashboardJSON(t, ctx, dashboard)

		params := UpdateDashboardParams{
			Dashboard: dashboardMap,
			Message:   "updating existing dashboard",
			Overwrite: true,
			UserID:    1,
		}

		// Only pass in the Folder UID if it exists
		if dashboard.FolderUID != "" {
			params.FolderUID = dashboard.FolderUID
		}

		// update the dashboard
		_, err := updateDashboard(ctx, params)
		require.NoError(t, err)
	})
}
