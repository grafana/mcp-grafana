// Requires a Grafana instance running on localhost:3000,
// with a dashboard named "Demo" provisioned.
// Run with `go test -tags integration`.
//go:build integration

package tools

import (
	"testing"

	"github.com/grafana/grafana-openapi-client-go/client/folders"
	"github.com/grafana/grafana-openapi-client-go/models"
	mcpgrafana "github.com/grafana/mcp-grafana/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSearchTools(t *testing.T) {
	t.Run("search dashboards", func(t *testing.T) {
		ctx := newTestContext()
		result, err := searchDashboards(ctx, SearchDashboardsParams{
			Query: "Demo",
		})
		require.NoError(t, err)
		assert.Len(t, result.Dashboards, 1)
		assert.Equal(t, "dash-db", result.Dashboards[0].Type)
		assert.Equal(t, "Tests", result.Dashboards[0].FolderPath)
	})

	t.Run("search folders", func(t *testing.T) {
		ctx := newTestContext()
		result, err := searchFolders(ctx, SearchFoldersParams{
			Query: "Tests",
		})
		require.NoError(t, err)
		assert.NotEmpty(t, result.Dashboards)
		assert.Equal(t, "dash-folder", result.Dashboards[0].Type)
	})
}

// createNestedFolders creates this tree, with one dashboard in each folder,
// and removes it when the test ends:
//
//	PathOps/Production/Services
//	PathOpsx
//	PathStaging/Production
func createNestedFolders(t *testing.T) {
	t.Helper()
	c := mcpgrafana.GrafanaClientFromContext(newTestContext())
	tree := []struct{ uid, title, parent, dashboard string }{
		{"path-ops", "PathOps", "", "Path Probe Ops"},
		{"path-ops-prod", "Production", "path-ops", "Path Probe Ops Production"},
		{"path-ops-prod-svc", "Services", "path-ops-prod", "Path Probe Services"},
		{"path-opsx", "PathOpsx", "", "Path Probe Opsx"},
		{"path-staging", "PathStaging", "", "Path Probe Staging"},
		{"path-staging-prod", "Production", "path-staging", "Path Probe Staging Production"},
	}
	for _, f := range tree {
		_, err := c.Folders.CreateFolder(&models.CreateFolderCommand{UID: f.uid, Title: f.title, ParentUID: f.parent})
		require.NoError(t, err, "create folder %s", f.uid)
		if f.parent == "" {
			uid := f.uid
			t.Cleanup(func() {
				force := true
				_, _ = c.Folders.DeleteFolder(folders.NewDeleteFolderParams().WithFolderUID(uid).WithForceDeleteRules(&force))
			})
		}
		_, err = c.Dashboards.PostDashboard(&models.SaveDashboardCommand{
			Dashboard: map[string]any{"uid": f.uid + "-dash", "title": f.dashboard},
			FolderUID: f.uid,
		})
		require.NoError(t, err, "create dashboard in %s", f.uid)
	}
}

func dashboardUIDs(result *SearchDashboardsResult) []string {
	uids := make([]string, 0, len(result.Dashboards))
	for _, d := range result.Dashboards {
		uids = append(uids, d.UID)
	}
	return uids
}

func TestSearchDashboardsFolderPath(t *testing.T) {
	createNestedFolders(t)
	ctx := newTestContext()

	t.Run("folderPath includes subfolders", func(t *testing.T) {
		result, err := searchDashboards(ctx, SearchDashboardsParams{FolderPath: "PathOps"})
		require.NoError(t, err)
		assert.ElementsMatch(t, []string{"path-ops-dash", "path-ops-prod-dash", "path-ops-prod-svc-dash"}, dashboardUIDs(result))
	})

	t.Run("folderPath selects a nested folder", func(t *testing.T) {
		result, err := searchDashboards(ctx, SearchDashboardsParams{FolderPath: "pathops/production"})
		require.NoError(t, err)
		assert.ElementsMatch(t, []string{"path-ops-prod-dash", "path-ops-prod-svc-dash"}, dashboardUIDs(result))
	})

	t.Run("folderPath combines with query", func(t *testing.T) {
		result, err := searchDashboards(ctx, SearchDashboardsParams{FolderPath: "PathOps", Query: "Services"})
		require.NoError(t, err)
		assert.Equal(t, []string{"path-ops-prod-svc-dash"}, dashboardUIDs(result))
	})

	t.Run("hits carry their folder path", func(t *testing.T) {
		result, err := searchDashboards(ctx, SearchDashboardsParams{Query: "Production"})
		require.NoError(t, err)
		paths := map[string]string{}
		for _, d := range result.Dashboards {
			paths[d.UID] = d.FolderPath
		}
		assert.Equal(t, "PathOps/Production", paths["path-ops-prod-dash"])
		assert.Equal(t, "PathStaging/Production", paths["path-staging-prod-dash"])
	})

	t.Run("unknown folderPath returns nothing", func(t *testing.T) {
		result, err := searchDashboards(ctx, SearchDashboardsParams{FolderPath: "PathOp"})
		require.NoError(t, err)
		assert.Empty(t, result.Dashboards)
	})
}
