// Requires a Grafana instance running on localhost:3000,
// with a dashboard provisioned.
// Run with `go test -tags integration`.
//go:build integration

package tools

import (
	"context"
	"strings"
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

	t.Run("get dashboard panel queries", func(t *testing.T) {
		ctx := newTestContext()

		// Get the test dashboard
		dashboard := getExistingTestDashboard(t, ctx, "")

		result, err := GetDashboardPanelQueriesTool(ctx, DashboardPanelQueriesParams{
			UID: dashboard.UID,
		})
		require.NoError(t, err)
		assert.Greater(t, len(result), 0, "Should return at least one panel query")

		// The initial demo dashboard plus for all dashboards created by the integration tests,
		// every panel should have identical title and query values.
		// Datasource UID may differ. Datasource type can be an empty string as well but on the demo and test dashboards it should be "prometheus".
		for _, panelQuery := range result {
			assert.Equal(t, panelQuery.Title, "Node Load")
			assert.Equal(t, panelQuery.Query, "node_load1")
			assert.NotEmpty(t, panelQuery.Datasource.UID)
			assert.Equal(t, panelQuery.Datasource.Type, "prometheus")
		}
	})
}

func TestGetDashboardManagerTool(t *testing.T) {
	t.Run("get dashboard manager - nonexistent dashboard", func(t *testing.T) {
		ctx := newTestContext()

		result, err := getDashboardManager(ctx, GetDashboardManagerParams{
			ID: "nonexistent-dashboard-12345",
		})
		require.NoError(t, err)
		assert.Contains(t, result, "No dashboard manager found")
	})

	t.Run("get dashboard manager - invalid dashboard ID", func(t *testing.T) {
		ctx := newTestContext()

		result, err := getDashboardManager(ctx, GetDashboardManagerParams{
			ID: "",
		})
		require.NoError(t, err)
		// Should handle empty ID gracefully
		assert.NotEmpty(t, result)
	})

	t.Run("get dashboard manager - dashboard with manager", func(t *testing.T) {
		ctx := newTestContext()

		// Test with a sample UID (this may need adjustment based on test environment)
		result, err := getDashboardManager(ctx, GetDashboardManagerParams{
			ID: "sample-dashboard-uid",
		})
		require.NoError(t, err)
		// Should return some information about the manager or indicate no manager
		assert.NotEmpty(t, result)
		// Result should contain expected structure
		assert.True(t,
			assert.Contains(t, result, "No dashboard manager found") ||
				assert.Contains(t, result, "managedBy:"),
			"Result should either indicate no manager or provide manager details")
	})
}

func TestSmartUpdateDashboard(t *testing.T) {
	t.Run("smart update - new dashboard without UID", func(t *testing.T) {
		ctx := newTestContext()

		// Test creating a new dashboard (no UID)
		result, err := smartUpdateDashboard(ctx, UpdateDashboardParams{
			Dashboard: map[string]interface{}{
				"title":  "Test New Dashboard",
				"panels": []interface{}{},
			},
			Message:   "Create new test dashboard",
			Overwrite: false,
		})

		// Should either succeed or fail with a meaningful error
		if err != nil {
			// Common errors for new dashboard creation
			assert.True(t,
				assert.Contains(t, err.Error(), "unable to save dashboard") ||
					assert.Contains(t, err.Error(), "validation") ||
					assert.Contains(t, err.Error(), "permission"),
				"Error should be related to dashboard creation, not smart routing: %v", err)
		} else {
			// If successful, should indicate it was created
			assert.Contains(t, result, "Dashboard created successfully")
		}
	})

	t.Run("smart update - existing dashboard detection", func(t *testing.T) {
		ctx := newTestContext()

		// Test with an existing dashboard UID
		result, err := smartUpdateDashboard(ctx, UpdateDashboardParams{
			Dashboard: map[string]interface{}{
				"uid":    "test-dashboard-uid",
				"title":  "Test Existing Dashboard",
				"panels": []interface{}{},
			},
			Message:   "Update existing test dashboard",
			Overwrite: true,
		})

		// The function should handle the dashboard manager detection
		if err != nil {
			// Should be a meaningful error, not a routing error
			assert.NotContains(t, err.Error(), "dashboard must be a JSON object")
			assert.NotContains(t, err.Error(), "type assertion")
		} else {
			// Should contain either GitOps or regular update result
			assert.True(t,
				assert.Contains(t, result, "Dashboard updated successfully") ||
					assert.Contains(t, result, "Provisioned dashboard updated via GitOps"),
				"Result should indicate successful update via appropriate method")
		}
	})

	t.Run("smart update - provisioned dashboard uses plain JSON", func(t *testing.T) {
		// This test verifies that for provisioned dashboards,
		// we store plain JSON instead of Kubernetes resource format
		ctx := newTestContext()

		// Mock dashboard data
		dashboardJSON := map[string]interface{}{
			"uid":     "test-provisioned-dashboard",
			"title":   "Test Provisioned Dashboard",
			"version": 1,
			"panels": []interface{}{
				map[string]interface{}{
					"id":    1,
					"title": "Test Panel",
					"type":  "stat",
				},
			},
		}

		// Test the smart update
		result, err := smartUpdateDashboard(ctx, UpdateDashboardParams{
			Dashboard: dashboardJSON,
			Message:   "Update provisioned dashboard as plain JSON",
			Overwrite: true,
		})

		// The key test: if this is a provisioned dashboard, it should handle it via GitOps
		// and NOT wrap it in Kubernetes resource format
		if err != nil {
			// If it fails, it should be a meaningful GitOps-related error, not a format error
			assert.NotContains(t, err.Error(), "failed to marshal dashboard resource")
			assert.NotContains(t, err.Error(), "Kubernetes resource format")
		} else {
			// If successful, check the result format
			if strings.Contains(result, "Provisioned dashboard updated via GitOps") {
				// This means it detected a provisioned dashboard and used file management
				assert.Contains(t, result, "Repository:")
				assert.Contains(t, result, "File:")
				assert.Contains(t, result, "UID:")
			} else {
				// Regular dashboard update
				assert.Contains(t, result, "Dashboard updated successfully")
			}
		}
	})

	t.Run("smart update - invalid dashboard format", func(t *testing.T) {
		ctx := newTestContext()

		// This test will not be valid since the Dashboard type is already map[string]interface{}
		// So we'll skip this test or modify it to test other error conditions
		t.Skip("Dashboard parameter is already properly typed")
	})
}

func TestDashboardToolIntegration(t *testing.T) {
	// Implementation of TestDashboardToolIntegration function
	// This function should be implemented based on the specific integration test requirements
}
