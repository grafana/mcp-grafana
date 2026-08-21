//go:build unit

package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/grafana/grafana-openapi-client-go/client"
	"github.com/grafana/grafana-openapi-client-go/models"
	mcpgrafana "github.com/grafana/mcp-grafana"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func mockSearchCtx(server *httptest.Server) context.Context {
	u, _ := url.Parse(server.URL)
	cfg := client.DefaultTransportConfig()
	cfg.Host = u.Host
	cfg.Schemes = []string{"http"}
	cfg.APIKey = "test"

	c := client.NewHTTPClientWithConfig(nil, cfg)
	return mcpgrafana.WithGrafanaClient(context.Background(), &mcpgrafana.GrafanaClient{GrafanaHTTPAPI: c})
}

func TestSearchDashboards_Pagination(t *testing.T) {
	t.Run("default pagination uses limit 50 and page 1", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "/api/search", r.URL.Path)
			q := r.URL.Query()
			assert.Equal(t, "50", q.Get("limit"))
			assert.Equal(t, "1", q.Get("page"))

			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(models.HitList{})
		}))
		defer server.Close()

		ctx := mockSearchCtx(server)
		_, err := searchDashboards(ctx, SearchDashboardsParams{Query: "test"})
		require.NoError(t, err)
	})

	t.Run("custom limit and page are sent", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			q := r.URL.Query()
			assert.Equal(t, "25", q.Get("limit"))
			assert.Equal(t, "3", q.Get("page"))

			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(models.HitList{})
		}))
		defer server.Close()

		ctx := mockSearchCtx(server)
		_, err := searchDashboards(ctx, SearchDashboardsParams{Query: "test", Limit: 25, Page: 3})
		require.NoError(t, err)
	})

	t.Run("limit capped at 100", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			q := r.URL.Query()
			assert.Equal(t, "100", q.Get("limit"))

			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(models.HitList{})
		}))
		defer server.Close()

		ctx := mockSearchCtx(server)
		_, err := searchDashboards(ctx, SearchDashboardsParams{Query: "test", Limit: 500})
		require.NoError(t, err)
	})

	t.Run("page defaults to 1 when 0 or negative", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			q := r.URL.Query()
			assert.Equal(t, "1", q.Get("page"))

			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(models.HitList{})
		}))
		defer server.Close()

		ctx := mockSearchCtx(server)
		_, err := searchDashboards(ctx, SearchDashboardsParams{Query: "test", Page: 0})
		require.NoError(t, err)
	})

	t.Run("hasMore true when results equal limit", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Return exactly 10 results (matching the limit)
			results := make(models.HitList, 10)
			for i := 0; i < 10; i++ {
				results[i] = &models.Hit{
					UID:   "dash-" + string(rune('a'+i)),
					Title: "Dashboard " + string(rune('A'+i)),
					Type:  "dash-db",
				}
			}

			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(results)
		}))
		defer server.Close()

		ctx := mockSearchCtx(server)
		result, err := searchDashboards(ctx, SearchDashboardsParams{Query: "test", Limit: 10})
		require.NoError(t, err)
		assert.True(t, result.HasMore)
		assert.Len(t, result.Dashboards, 10)
	})

	t.Run("hasMore false when results less than limit", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Return less than limit results
			results := make(models.HitList, 5)
			for i := 0; i < 5; i++ {
				results[i] = &models.Hit{
					UID:   "dash-" + string(rune('a'+i)),
					Title: "Dashboard " + string(rune('A'+i)),
					Type:  "dash-db",
				}
			}

			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(results)
		}))
		defer server.Close()

		ctx := mockSearchCtx(server)
		result, err := searchDashboards(ctx, SearchDashboardsParams{Query: "test", Limit: 10})
		require.NoError(t, err)
		assert.False(t, result.HasMore)
		assert.Len(t, result.Dashboards, 5)
	})
}

func TestBuildFolderPath(t *testing.T) {
	folderMap := map[string]*models.Hit{
		"root":       {UID: "root", Title: "Root"},
		"child":      {UID: "child", Title: "Child", FolderUID: "root"},
		"grandchild": {UID: "grandchild", Title: "Grandchild", FolderUID: "child"},
	}

	t.Run("empty folderUID returns empty string", func(t *testing.T) {
		assert.Equal(t, "", buildFolderPath("", folderMap))
	})

	t.Run("single level", func(t *testing.T) {
		assert.Equal(t, "/Root", buildFolderPath("root", folderMap))
	})

	t.Run("two levels", func(t *testing.T) {
		assert.Equal(t, "/Root/Child", buildFolderPath("child", folderMap))
	})

	t.Run("three levels", func(t *testing.T) {
		assert.Equal(t, "/Root/Child/Grandchild", buildFolderPath("grandchild", folderMap))
	})

	t.Run("unknown folderUID returns empty string", func(t *testing.T) {
		assert.Equal(t, "", buildFolderPath("unknown", folderMap))
	})
}

func TestMatchesDashboardQuery(t *testing.T) {
	hit := dashboardSearchHitWithPath{
		dashboardSearchHit: dashboardSearchHit{
			Title:       "My Dashboard",
			Description: "Some metrics here",
			FolderTitle: "Production",
			Tags:        []string{"prod", "infra"},
		},
		FolderPath: "/Ops/Production",
	}

	// matchesDashboardQuery expects a pre-lowercased query (as produced by searchDashboardsDeep)
	t.Run("matches title", func(t *testing.T) {
		assert.True(t, matchesDashboardQuery("my dashboard", hit))
	})

	t.Run("matches description", func(t *testing.T) {
		assert.True(t, matchesDashboardQuery("metrics", hit))
	})

	t.Run("matches folderTitle", func(t *testing.T) {
		assert.True(t, matchesDashboardQuery("production", hit))
	})

	t.Run("matches folderPath", func(t *testing.T) {
		assert.True(t, matchesDashboardQuery("/ops/production", hit))
	})

	t.Run("matches tag", func(t *testing.T) {
		assert.True(t, matchesDashboardQuery("infra", hit))
	})

	t.Run("partial match", func(t *testing.T) {
		assert.True(t, matchesDashboardQuery("dash", hit))
	})

	t.Run("no match", func(t *testing.T) {
		assert.False(t, matchesDashboardQuery("grafana", hit))
	})
}

// mockDeepSearchServer returns a test server that serves folders on type=dash-folder
// and dashboards on type=dash-db, with a single page of results each.
func mockDeepSearchServer(t *testing.T, folders models.HitList, dashboards models.HitList) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)

		// Return results only on page 1; empty page 2 terminates the loop.
		if q.Get("page") != "1" {
			_ = json.NewEncoder(w).Encode(models.HitList{})
			return
		}
		switch q.Get("type") {
		case "dash-folder":
			_ = json.NewEncoder(w).Encode(folders)
		case "dash-db":
			_ = json.NewEncoder(w).Encode(dashboards)
		default:
			_ = json.NewEncoder(w).Encode(models.HitList{})
		}
	}))
}

func TestSearchDashboardsDeep(t *testing.T) {
	folders := models.HitList{
		{UID: "ops", Title: "Ops", Type: "dash-folder"},
		{UID: "prod", Title: "Production", FolderUID: "ops", Type: "dash-folder"},
	}
	dashboards := models.HitList{
		{UID: "d1", Title: "CPU Usage", FolderUID: "prod", FolderTitle: "Production", Type: "dash-db", URL: "/d/d1"},
		{UID: "d2", Title: "Memory Overview", FolderUID: "ops", FolderTitle: "Ops", Type: "dash-db", URL: "/d/d2"},
		{UID: "d3", Title: "Unrelated", Type: "dash-db", URL: "/d/d3"},
	}

	t.Run("empty query returns all dashboards", func(t *testing.T) {
		server := mockDeepSearchServer(t, folders, dashboards)
		defer server.Close()

		result, err := searchDashboardsDeep(mockSearchCtx(server), SearchDashboardsDeepParams{})
		require.NoError(t, err)
		assert.Equal(t, 3, result.Total)
		assert.Len(t, result.Dashboards, 3)
	})

	t.Run("filters by title", func(t *testing.T) {
		server := mockDeepSearchServer(t, folders, dashboards)
		defer server.Close()

		result, err := searchDashboardsDeep(mockSearchCtx(server), SearchDashboardsDeepParams{Query: "cpu"})
		require.NoError(t, err)
		assert.Equal(t, 1, result.Total)
		assert.Equal(t, "d1", result.Dashboards[0].UID)
	})

	t.Run("filters by folderPath", func(t *testing.T) {
		server := mockDeepSearchServer(t, folders, dashboards)
		defer server.Close()

		result, err := searchDashboardsDeep(mockSearchCtx(server), SearchDashboardsDeepParams{Query: "/ops/production"})
		require.NoError(t, err)
		assert.Equal(t, 1, result.Total)
		assert.Equal(t, "d1", result.Dashboards[0].UID)
	})

	t.Run("folderPath is reconstructed and returned", func(t *testing.T) {
		server := mockDeepSearchServer(t, folders, dashboards)
		defer server.Close()

		result, err := searchDashboardsDeep(mockSearchCtx(server), SearchDashboardsDeepParams{Query: "cpu"})
		require.NoError(t, err)
		require.Len(t, result.Dashboards, 1)
		assert.Equal(t, "/Ops/Production", result.Dashboards[0].FolderPath)
	})

	t.Run("matching is case insensitive", func(t *testing.T) {
		server := mockDeepSearchServer(t, folders, dashboards)
		defer server.Close()

		result, err := searchDashboardsDeep(mockSearchCtx(server), SearchDashboardsDeepParams{Query: "CPU USAGE"})
		require.NoError(t, err)
		assert.Equal(t, 1, result.Total)
	})

	t.Run("no match returns empty list", func(t *testing.T) {
		server := mockDeepSearchServer(t, folders, dashboards)
		defer server.Close()

		result, err := searchDashboardsDeep(mockSearchCtx(server), SearchDashboardsDeepParams{Query: "/nonexistent"})
		require.NoError(t, err)
		assert.Equal(t, 0, result.Total)
		assert.Empty(t, result.Dashboards)
	})

	t.Run("folderPath filter limits to exact folder", func(t *testing.T) {
		server := mockDeepSearchServer(t, folders, dashboards)
		defer server.Close()

		result, err := searchDashboardsDeep(mockSearchCtx(server), SearchDashboardsDeepParams{FolderPath: "/Ops/Production"})
		require.NoError(t, err)
		assert.Equal(t, 1, result.Total)
		assert.Equal(t, "d1", result.Dashboards[0].UID)
	})

	t.Run("folderPath filter includes subfolders", func(t *testing.T) {
		server := mockDeepSearchServer(t, folders, dashboards)
		defer server.Close()

		// "Ops" should match d1 (Ops/Production) and d2 (Ops)
		result, err := searchDashboardsDeep(mockSearchCtx(server), SearchDashboardsDeepParams{FolderPath: "/Ops"})
		require.NoError(t, err)
		assert.Equal(t, 2, result.Total)
		uids := []string{result.Dashboards[0].UID, result.Dashboards[1].UID}
		assert.ElementsMatch(t, []string{"d1", "d2"}, uids)
	})

	t.Run("folderPath filter is case insensitive", func(t *testing.T) {
		server := mockDeepSearchServer(t, folders, dashboards)
		defer server.Close()

		result, err := searchDashboardsDeep(mockSearchCtx(server), SearchDashboardsDeepParams{FolderPath: "/ops/production"})
		require.NoError(t, err)
		assert.Equal(t, 1, result.Total)
		assert.Equal(t, "d1", result.Dashboards[0].UID)
	})

	t.Run("folderPath filter does not match partial folder name", func(t *testing.T) {
		server := mockDeepSearchServer(t, folders, dashboards)
		defer server.Close()

		// "Op" is a prefix of "Ops" but not a valid path segment boundary
		result, err := searchDashboardsDeep(mockSearchCtx(server), SearchDashboardsDeepParams{FolderPath: "/Op"})
		require.NoError(t, err)
		assert.Equal(t, 0, result.Total)
	})

	t.Run("tags filter restricts results", func(t *testing.T) {
		taggedDashboards := models.HitList{
			{UID: "d1", Title: "CPU Usage", FolderUID: "prod", FolderTitle: "Production", Type: "dash-db", Tags: []string{"infra", "prod"}},
			{UID: "d2", Title: "Memory", FolderUID: "ops", FolderTitle: "Ops", Type: "dash-db", Tags: []string{"infra"}},
			{UID: "d3", Title: "Requests", Type: "dash-db", Tags: []string{"app"}},
		}
		server := mockDeepSearchServer(t, folders, taggedDashboards)
		defer server.Close()

		result, err := searchDashboardsDeep(mockSearchCtx(server), SearchDashboardsDeepParams{Tags: []string{"prod"}})
		require.NoError(t, err)
		assert.Equal(t, 1, result.Total)
		assert.Equal(t, "d1", result.Dashboards[0].UID)
	})

	t.Run("tags filter is case insensitive", func(t *testing.T) {
		taggedDashboards := models.HitList{
			{UID: "d1", Title: "CPU Usage", Type: "dash-db", Tags: []string{"Infra"}},
		}
		server := mockDeepSearchServer(t, folders, taggedDashboards)
		defer server.Close()

		result, err := searchDashboardsDeep(mockSearchCtx(server), SearchDashboardsDeepParams{Tags: []string{"infra"}})
		require.NoError(t, err)
		assert.Equal(t, 1, result.Total)
	})

	t.Run("tags filter AND logic requires all tags", func(t *testing.T) {
		taggedDashboards := models.HitList{
			{UID: "d1", Title: "CPU Usage", Type: "dash-db", Tags: []string{"infra", "prod"}},
			{UID: "d2", Title: "Memory", Type: "dash-db", Tags: []string{"infra"}},
		}
		server := mockDeepSearchServer(t, folders, taggedDashboards)
		defer server.Close()

		// Only d1 has both tags
		result, err := searchDashboardsDeep(mockSearchCtx(server), SearchDashboardsDeepParams{Tags: []string{"infra", "prod"}})
		require.NoError(t, err)
		assert.Equal(t, 1, result.Total)
		assert.Equal(t, "d1", result.Dashboards[0].UID)
	})

	t.Run("tags filter combined with folderPath and query (AND logic)", func(t *testing.T) {
		taggedDashboards := models.HitList{
			{UID: "d1", Title: "CPU Usage", FolderUID: "prod", Type: "dash-db", Tags: []string{"infra"}},
			{UID: "d2", Title: "CPU Idle", FolderUID: "ops", Type: "dash-db", Tags: []string{"infra"}},
			{UID: "d3", Title: "CPU Requests", FolderUID: "prod", Type: "dash-db", Tags: []string{"app"}},
		}
		server := mockDeepSearchServer(t, folders, taggedDashboards)
		defer server.Close()

		// Only d1 matches all three: query="cpu", folderPath="/Ops/Production", tags=["infra"]
		result, err := searchDashboardsDeep(mockSearchCtx(server), SearchDashboardsDeepParams{
			Query:      "cpu",
			FolderPath: "/Ops/Production",
			Tags:       []string{"infra"},
		})
		require.NoError(t, err)
		assert.Equal(t, 1, result.Total)
		assert.Equal(t, "d1", result.Dashboards[0].UID)
	})

	t.Run("folderPath filter combined with query", func(t *testing.T) {
		server := mockDeepSearchServer(t, folders, dashboards)
		defer server.Close()

		// Within "Ops", only d1 matches "cpu"
		result, err := searchDashboardsDeep(mockSearchCtx(server), SearchDashboardsDeepParams{FolderPath: "/Ops", Query: "cpu"})
		require.NoError(t, err)
		assert.Equal(t, 1, result.Total)
		assert.Equal(t, "d1", result.Dashboards[0].UID)
	})
}
