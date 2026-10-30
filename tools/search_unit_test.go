//go:build unit

package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/grafana/grafana-openapi-client-go/client"
	"github.com/grafana/grafana-openapi-client-go/models"
	mcpgrafana "github.com/grafana/mcp-grafana/v2"
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

func TestSearchDashboards_Filters(t *testing.T) {
	t.Run("folderUid tag and starred are forwarded to Grafana search", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "/api/search", r.URL.Path)
			q := r.URL.Query()
			assert.Equal(t, "dash-db", q.Get("type"))
			assert.Equal(t, []string{"folder-abc"}, q["folderUIDs"])
			assert.Equal(t, []string{"prod", "alerts"}, q["tag"])
			assert.Equal(t, "true", q.Get("starred"))
			assert.Equal(t, "cpu", q.Get("query"))

			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(models.HitList{})
		}))
		defer server.Close()

		ctx := mockSearchCtx(server)
		_, err := searchDashboards(ctx, SearchDashboardsParams{
			Query:     "cpu",
			FolderUID: "folder-abc",
			Tag:       []string{"prod", "alerts"},
			Starred:   true,
		})
		require.NoError(t, err)
	})

	t.Run("folderUid without query still restricts to dashboards", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			q := r.URL.Query()
			assert.Equal(t, "dash-db", q.Get("type"))
			assert.Equal(t, []string{"folder-abc"}, q["folderUIDs"])
			assert.Empty(t, q.Get("query"))
			assert.Empty(t, q["tag"])
			assert.Empty(t, q.Get("starred"))

			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(models.HitList{})
		}))
		defer server.Close()

		ctx := mockSearchCtx(server)
		_, err := searchDashboards(ctx, SearchDashboardsParams{FolderUID: "folder-abc"})
		require.NoError(t, err)
	})

	t.Run("omitted filters are not sent", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			q := r.URL.Query()
			assert.Empty(t, q["folderUIDs"])
			assert.Empty(t, q["tag"])
			assert.Empty(t, q.Get("starred"))

			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(models.HitList{})
		}))
		defer server.Close()

		ctx := mockSearchCtx(server)
		_, err := searchDashboards(ctx, SearchDashboardsParams{Query: "test"})
		require.NoError(t, err)
	})

	t.Run("empty params still restrict to dashboards", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			q := r.URL.Query()
			assert.Equal(t, "dash-db", q.Get("type"))
			assert.Empty(t, q.Get("query"))
			assert.Empty(t, q["folderUIDs"])
			assert.Empty(t, q["tag"])
			assert.Empty(t, q.Get("starred"))

			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(models.HitList{})
		}))
		defer server.Close()

		ctx := mockSearchCtx(server)
		_, err := searchDashboards(ctx, SearchDashboardsParams{})
		require.NoError(t, err)
	})
}

// testFolderTree is Ops/Production/Services plus look-alikes: "Opsx" checks
// whole-segment matching, the second "Production" under Staging checks that
// equal titles are told apart, and "orphan" sits under a parent the caller
// cannot see.
func testFolderTree() folderTree {
	return folderTree{
		"ops":          {title: "Ops"},
		"ops-prod":     {title: "Production", parentUID: "ops"},
		"ops-prod-svc": {title: "Services", parentUID: "ops-prod"},
		"opsx":         {title: "Opsx"},
		"staging":      {title: "Staging"},
		"staging-prod": {title: "Production", parentUID: "staging"},
		"orphan":       {title: "Orphan", parentUID: "hidden"},
	}
}

func TestFolderTree_Path(t *testing.T) {
	tree := testFolderTree()

	assert.Equal(t, "Ops", tree.path("ops"))
	assert.Equal(t, "Ops/Production/Services", tree.path("ops-prod-svc"))
	assert.Equal(t, "Staging/Production", tree.path("staging-prod"))
	assert.Empty(t, tree.path(""), "dashboards in the General folder have no path")
	assert.Empty(t, tree.path("unknown"))
	assert.Empty(t, tree.path("orphan"), "an incomplete chain must not look like a root path")

	cyclic := folderTree{
		"a": {title: "A", parentUID: "b"},
		"b": {title: "B", parentUID: "a"},
	}
	assert.Empty(t, cyclic.path("a"))
}

func TestFolderTree_Subtree(t *testing.T) {
	tree := testFolderTree()

	assert.Equal(t, []string{"ops", "ops-prod", "ops-prod-svc"}, tree.subtree("Ops"))
	assert.Equal(t, []string{"ops-prod", "ops-prod-svc"}, tree.subtree("ops/PRODUCTION"))
	assert.Equal(t, []string{"ops-prod-svc"}, tree.subtree("Ops/Production/Services"))
	assert.Empty(t, tree.subtree("Op"), "partial segments must not match")
	assert.Empty(t, tree.subtree("Production"), "paths start at the root")
	assert.Empty(t, tree.subtree("Orphan"))

	dup := folderTree{
		"a": {title: "Ops"},
		"b": {title: "Ops"},
	}
	assert.Equal(t, []string{"a", "b"}, dup.subtree("Ops"), "folders sharing a path all match")
}

// folderSearchServer serves folders for type=dash-folder and dashboards for
// type=dash-db, and records the dashboard search query.
func folderSearchServer(t *testing.T, folders, dashboards models.HitList, dashQuery *url.Values) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		switch q.Get("type") {
		case "dash-folder":
			if q.Get("page") != "1" {
				_ = json.NewEncoder(w).Encode(models.HitList{})
				return
			}
			_ = json.NewEncoder(w).Encode(folders)
		case "dash-db":
			if dashQuery != nil {
				*dashQuery = q
			}
			_ = json.NewEncoder(w).Encode(dashboards)
		default:
			t.Errorf("unexpected search type %q", q.Get("type"))
		}
	}))
}

var testFolderHits = models.HitList{
	{UID: "ops", Title: "Ops", Type: "dash-folder"},
	{UID: "ops-prod", Title: "Production", FolderUID: "ops", Type: "dash-folder"},
	{UID: "ops-prod-svc", Title: "Services", FolderUID: "ops-prod", Type: "dash-folder"},
	{UID: "opsx", Title: "Opsx", Type: "dash-folder"},
}

func TestSearchDashboards_FolderPath(t *testing.T) {
	t.Run("folderPath expands to the folder and its subfolders", func(t *testing.T) {
		var dashQuery url.Values
		server := folderSearchServer(t, testFolderHits, models.HitList{}, &dashQuery)
		defer server.Close()

		_, err := searchDashboards(mockSearchCtx(server), SearchDashboardsParams{FolderPath: "/ops/production/"})
		require.NoError(t, err)
		assert.Equal(t, []string{"ops-prod", "ops-prod-svc"}, dashQuery["folderUIDs"])
	})

	t.Run("unknown folderPath returns no dashboards without searching", func(t *testing.T) {
		var dashQuery url.Values
		server := folderSearchServer(t, testFolderHits, models.HitList{{UID: "d1"}}, &dashQuery)
		defer server.Close()

		result, err := searchDashboards(mockSearchCtx(server), SearchDashboardsParams{FolderPath: "Op"})
		require.NoError(t, err)
		assert.NotNil(t, result.Dashboards)
		assert.Empty(t, result.Dashboards)
		assert.Nil(t, dashQuery, "dashboard search must not run unfiltered")
	})

	t.Run("folderUid and folderPath together are rejected", func(t *testing.T) {
		_, err := searchDashboards(context.Background(), SearchDashboardsParams{FolderUID: "ops", FolderPath: "Ops"})
		require.Error(t, err)
	})

	t.Run("hits carry their folder path", func(t *testing.T) {
		dashboards := models.HitList{
			{UID: "d1", Title: "Keycloak", FolderUID: "ops-prod-svc", FolderTitle: "Services", Type: "dash-db"},
			{UID: "d2", Title: "Home", Type: "dash-db"},
		}
		server := folderSearchServer(t, testFolderHits, dashboards, nil)
		defer server.Close()

		result, err := searchDashboards(mockSearchCtx(server), SearchDashboardsParams{Query: "k"})
		require.NoError(t, err)
		require.Len(t, result.Dashboards, 2)
		assert.Equal(t, "Ops/Production/Services", result.Dashboards[0].FolderPath)
		assert.Empty(t, result.Dashboards[1].FolderPath)
	})

	t.Run("folder listing failure leaves folder paths empty", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Query().Get("type") == "dash-folder" {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(models.HitList{{UID: "d1", FolderUID: "ops", Type: "dash-db"}})
		}))
		defer server.Close()

		result, err := searchDashboards(mockSearchCtx(server), SearchDashboardsParams{Query: "d"})
		require.NoError(t, err)
		require.Len(t, result.Dashboards, 1)
		assert.Empty(t, result.Dashboards[0].FolderPath)
	})

	t.Run("folder listing pages until a short page", func(t *testing.T) {
		var pages []string
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			q := r.URL.Query()
			w.Header().Set("Content-Type", "application/json")
			if q.Get("type") == "dash-db" {
				_ = json.NewEncoder(w).Encode(models.HitList{})
				return
			}
			pages = append(pages, q.Get("page"))
			assert.Equal(t, "1000", q.Get("limit"))
			if q.Get("page") == "1" {
				full := make(models.HitList, folderListPageSize)
				for i := range full {
					full[i] = &models.Hit{UID: fmt.Sprintf("f%d", i), Title: fmt.Sprintf("F%d", i)}
				}
				_ = json.NewEncoder(w).Encode(full)
				return
			}
			_ = json.NewEncoder(w).Encode(models.HitList{{UID: "last", Title: "Last"}})
		}))
		defer server.Close()

		tree, err := listFolderTree(mockSearchCtx(server))
		require.NoError(t, err)
		assert.Equal(t, []string{"1", "2"}, pages)
		assert.Len(t, tree, folderListPageSize+1)
	})
}
