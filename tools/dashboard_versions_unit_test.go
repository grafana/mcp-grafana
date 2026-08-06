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
	mcpgrafana "github.com/grafana/mcp-grafana"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func mockDashboardVersionsCtx(server *httptest.Server) context.Context {
	u, _ := url.Parse(server.URL)
	cfg := client.DefaultTransportConfig()
	cfg.Host = u.Host
	cfg.Schemes = []string{"http"}
	cfg.APIKey = "test"

	c := client.NewHTTPClientWithConfig(nil, cfg)
	return mcpgrafana.WithGrafanaClient(context.Background(), &mcpgrafana.GrafanaClient{GrafanaHTTPAPI: c})
}

func TestListDashboardVersions(t *testing.T) {
	emptyVersionsPayload := map[string]any{"versions": []any{}}

	t.Run("sends uid as path parameter", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "/api/dashboards/uid/my-uid/versions", r.URL.Path)

			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(emptyVersionsPayload)
		}))
		defer server.Close()

		ctx := mockDashboardVersionsCtx(server)
		result, err := listDashboardVersions(ctx, ListDashboardVersionsParams{UID: "my-uid"})
		require.NoError(t, err)
		assert.Empty(t, result)
	})

	t.Run("returns version summaries without data field", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			payload := map[string]any{
				"versions": []map[string]any{
					{
						"version":       3,
						"createdBy":     "alice",
						"created":       "0001-01-01T00:00:00.000Z",
						"message":       "add new panel",
						"parentVersion": 2,
						"restoredFrom":  0,
						"data":          map[string]any{"title": "My Dashboard"},
					},
					{
						"version":       2,
						"createdBy":     "bob",
						"created":       "0001-01-01T00:00:00.000Z",
						"message":       "",
						"parentVersion": 1,
						"restoredFrom":  0,
					},
				},
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(payload)
		}))
		defer server.Close()

		ctx := mockDashboardVersionsCtx(server)
		result, err := listDashboardVersions(ctx, ListDashboardVersionsParams{UID: "my-uid"})
		require.NoError(t, err)
		require.Len(t, result, 2)

		assert.Equal(t, int64(3), result[0].Version)
		assert.Equal(t, "alice", result[0].CreatedBy)
		assert.Equal(t, "add new panel", result[0].Message)
		assert.Equal(t, int64(2), result[0].ParentVersion)

		assert.Equal(t, int64(2), result[1].Version)
		assert.Equal(t, "bob", result[1].CreatedBy)
	})

	t.Run("forwards limit query parameter", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "10", r.URL.Query().Get("limit"))

			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(emptyVersionsPayload)
		}))
		defer server.Close()

		ctx := mockDashboardVersionsCtx(server)
		_, err := listDashboardVersions(ctx, ListDashboardVersionsParams{UID: "my-uid", Limit: 10})
		require.NoError(t, err)
	})

	t.Run("forwards start query parameter", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "5", r.URL.Query().Get("start"))

			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(emptyVersionsPayload)
		}))
		defer server.Close()

		ctx := mockDashboardVersionsCtx(server)
		_, err := listDashboardVersions(ctx, ListDashboardVersionsParams{UID: "my-uid", Start: 5})
		require.NoError(t, err)
	})

	t.Run("returns error when uid is empty", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			t.Error("unexpected HTTP call when uid is empty")
		}))
		defer server.Close()

		ctx := mockDashboardVersionsCtx(server)
		_, err := listDashboardVersions(ctx, ListDashboardVersionsParams{})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "uid is required")
	})

	t.Run("omits limit and start when zero", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Empty(t, r.URL.Query().Get("limit"))
			assert.Empty(t, r.URL.Query().Get("start"))

			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(emptyVersionsPayload)
		}))
		defer server.Close()

		ctx := mockDashboardVersionsCtx(server)
		_, err := listDashboardVersions(ctx, ListDashboardVersionsParams{UID: "my-uid", Limit: 0, Start: 0})
		require.NoError(t, err)
	})
}

func TestGetDashboardVersion(t *testing.T) {
	t.Run("sends uid and version as path parameters", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "/api/dashboards/uid/my-uid/versions/3", r.URL.Path)

			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"version":   3,
				"createdBy": "alice",
				"message":   "fix typo",
				"data":      map[string]any{"title": "My Dashboard"},
			})
		}))
		defer server.Close()

		ctx := mockDashboardVersionsCtx(server)
		result, err := getDashboardVersion(ctx, GetDashboardVersionParams{UID: "my-uid", Version: 3})
		require.NoError(t, err)
		assert.Equal(t, int64(3), result.Version)
		assert.Equal(t, "alice", result.CreatedBy)
		assert.Equal(t, "fix typo", result.Message)
	})

	t.Run("includes dashboard data in response", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"version": 1,
				"data":    map[string]any{"title": "Test Dashboard", "panels": []any{}},
			})
		}))
		defer server.Close()

		ctx := mockDashboardVersionsCtx(server)
		result, err := getDashboardVersion(ctx, GetDashboardVersionParams{UID: "my-uid", Version: 1})
		require.NoError(t, err)
		assert.NotNil(t, result.Data)
		assert.Equal(t, "Test Dashboard", result.Data["title"])
	})

	t.Run("returns error when uid is empty", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			t.Error("unexpected HTTP call when uid is empty")
		}))
		defer server.Close()

		ctx := mockDashboardVersionsCtx(server)
		_, err := getDashboardVersion(ctx, GetDashboardVersionParams{Version: 1})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "uid is required")
	})

	t.Run("returns error when version is zero", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			t.Error("unexpected HTTP call when version is zero")
		}))
		defer server.Close()

		ctx := mockDashboardVersionsCtx(server)
		_, err := getDashboardVersion(ctx, GetDashboardVersionParams{UID: "my-uid", Version: 0})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "version must be a positive integer")
	})
}
