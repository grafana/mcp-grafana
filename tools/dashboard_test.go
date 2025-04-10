// Requires a Grafana instance running on localhost:3000,
// with a dashboard provisioned.
// Run with `go test -tags integration`.
//go:build integration

package tools

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDashboardTools(t *testing.T) {
	t.Run("get dashboard by uid", func(t *testing.T) {
		ctx := newTestContext()
		
		// First, let's search for a dashboard to get its UID
		searchResults, err := searchDashboards(ctx, SearchDashboardsParams{})
		require.NoError(t, err)
		require.Greater(t, len(searchResults), 0, "No dashboards found")
		
		dashboardUID := searchResults[0].UID
		
		// Now test the get dashboard by uid functionality
		result, err := getDashboardByUID(ctx, GetDashboardByUIDParams{
			UID: dashboardUID,
		})
		require.NoError(t, err)
		dashboardMap, ok := result.Dashboard.(map[string]interface{})
		require.True(t, ok, "Dashboard should be a map")
		assert.Equal(t, dashboardUID, dashboardMap["uid"])
		assert.NotNil(t, result.Meta)
	})

	t.Run("get dashboard by uid - invalid uid", func(t *testing.T) {
		ctx := newTestContext()
		
		_, err := getDashboardByUID(ctx, GetDashboardByUIDParams{
			UID: "non-existent-uid",
		})
		require.Error(t, err)
	})
}