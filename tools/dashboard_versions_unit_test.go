//go:build unit

package tools

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetDashboardByUID_Version(t *testing.T) {
	t.Run("omitted version uses the current dashboard API", func(t *testing.T) {
		var versionsCalled bool
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasSuffix(r.URL.Path, "/versions") || strings.Contains(r.URL.Path, "/versions/") {
				versionsCalled = true
				t.Errorf("unexpected versions API call: %s", r.URL.Path)
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			assert.Equal(t, "/api/dashboards/uid/my-uid", r.URL.Path)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"dashboard": map[string]any{"title": "Current Dashboard", "uid": "my-uid"},
				"meta":      map[string]any{"version": 7},
			})
		}))
		defer server.Close()

		ctx := mockSearchCtx(server)
		result, err := getDashboardByUID(ctx, GetDashboardByUIDParams{UID: "my-uid"})
		require.NoError(t, err)
		assert.False(t, versionsCalled)
		dash, ok := result.Dashboard.(map[string]any)
		require.True(t, ok)
		assert.Equal(t, "Current Dashboard", dash["title"])
	})

	t.Run("version hits the versions API and skips the current dashboard", func(t *testing.T) {
		var currentCalled bool
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/api/dashboards/uid/my-uid" {
				currentCalled = true
				t.Errorf("unexpected current dashboard call: %s", r.URL.Path)
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			assert.Equal(t, "/api/dashboards/uid/my-uid/versions/3", r.URL.Path)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"version":   3,
				"createdBy": "alice",
				"message":   "fix typo",
				"data":      map[string]any{"title": "Historical Dashboard", "panels": []any{}},
			})
		}))
		defer server.Close()

		ctx := mockSearchCtx(server)
		version := int64(3)
		result, err := getDashboardByUID(ctx, GetDashboardByUIDParams{UID: "my-uid", Version: &version})
		require.NoError(t, err)
		assert.False(t, currentCalled)
		assert.False(t, result.IsV2)
		require.NotNil(t, result.Meta)
		assert.Equal(t, int64(3), result.Meta.Version)
		assert.Equal(t, "alice", result.Meta.CreatedBy)
		assert.Empty(t, result.Meta.UpdatedBy)
		assert.True(t, time.Time(result.Meta.Updated).IsZero())
		dash, ok := result.Dashboard.(map[string]any)
		require.True(t, ok)
		assert.Equal(t, "Historical Dashboard", dash["title"])
	})

	t.Run("returns error when version is zero", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			t.Error("unexpected HTTP call when version is zero")
		}))
		defer server.Close()

		ctx := mockSearchCtx(server)
		version := int64(0)
		_, err := getDashboardByUID(ctx, GetDashboardByUIDParams{UID: "my-uid", Version: &version})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "version must be a positive integer")
	})

	t.Run("returns error when uid is empty and version is set", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			t.Error("unexpected HTTP call when uid is empty")
		}))
		defer server.Close()

		ctx := mockSearchCtx(server)
		version := int64(1)
		_, err := getDashboardByUID(ctx, GetDashboardByUIDParams{Version: &version})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "uid is required")
	})
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

		ctx := mockSearchCtx(server)
		result, err := listDashboardVersions(ctx, ListDashboardVersionsParams{UID: "my-uid"})
		require.NoError(t, err)
		assert.Empty(t, result)
	})

	t.Run("returns compact metadata without dashboard data", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			payload := map[string]any{
				"versions": []map[string]any{
					{
						"version":       3,
						"createdBy":     "alice",
						"created":       "2024-06-08T17:24:33Z",
						"message":       "add new panel",
						"parentVersion": 2,
						"restoredFrom":  0,
						"data":          map[string]any{"title": "My Dashboard"},
					},
					{
						"version":   2,
						"createdBy": "bob",
						"created":   "2024-06-07T09:00:00Z",
						"message":   "",
					},
				},
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(payload)
		}))
		defer server.Close()

		ctx := mockSearchCtx(server)
		result, err := listDashboardVersions(ctx, ListDashboardVersionsParams{UID: "my-uid"})
		require.NoError(t, err)
		require.Len(t, result, 2)

		assert.Equal(t, int64(3), result[0].Version)
		assert.Equal(t, "alice", result[0].CreatedBy)
		assert.Equal(t, "2024-06-08T17:24:33Z", result[0].Created)
		assert.Equal(t, "add new panel", result[0].Message)
		raw, err := json.Marshal(result[0])
		require.NoError(t, err)
		assert.NotContains(t, string(raw), `"data"`)
		assert.NotContains(t, string(raw), "My Dashboard")

		assert.Equal(t, int64(2), result[1].Version)
		assert.Equal(t, "bob", result[1].CreatedBy)
		assert.Equal(t, "2024-06-07T09:00:00Z", result[1].Created)
	})

	t.Run("accepts Grafana 11 array payload", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "/api/dashboards/uid/my-uid/versions", r.URL.Path)

			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{
					"version":   2,
					"createdBy": "admin",
					"created":   "2024-06-08T17:24:33Z",
					"message":   "Updated panel title",
				},
			})
		}))
		defer server.Close()

		ctx := mockSearchCtx(server)
		result, err := listDashboardVersions(ctx, ListDashboardVersionsParams{UID: "my-uid"})
		require.NoError(t, err)
		require.Len(t, result, 1)
		assert.Equal(t, int64(2), result[0].Version)
		assert.Equal(t, "admin", result[0].CreatedBy)
		assert.Equal(t, "2024-06-08T17:24:33Z", result[0].Created)
		assert.Equal(t, "Updated panel title", result[0].Message)
	})

	t.Run("forwards limit query parameter", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "10", r.URL.Query().Get("limit"))

			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(emptyVersionsPayload)
		}))
		defer server.Close()

		ctx := mockSearchCtx(server)
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

		ctx := mockSearchCtx(server)
		_, err := listDashboardVersions(ctx, ListDashboardVersionsParams{UID: "my-uid", Start: 5})
		require.NoError(t, err)
	})

	t.Run("returns error when uid is empty", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			t.Error("unexpected HTTP call when uid is empty")
		}))
		defer server.Close()

		ctx := mockSearchCtx(server)
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

		ctx := mockSearchCtx(server)
		_, err := listDashboardVersions(ctx, ListDashboardVersionsParams{UID: "my-uid", Limit: 0, Start: 0})
		require.NoError(t, err)
	})
}

func TestDecodeDashboardVersionList(t *testing.T) {
	t.Run("wrapped object", func(t *testing.T) {
		versions, err := decodeDashboardVersionList([]byte(`{"versions":[{"version":3,"createdBy":"alice"}]}`))
		require.NoError(t, err)
		require.Len(t, versions, 1)
		assert.Equal(t, int64(3), versions[0].Version)
		assert.Equal(t, "alice", versions[0].CreatedBy)
	})

	t.Run("bare array", func(t *testing.T) {
		versions, err := decodeDashboardVersionList([]byte(`[{"version":2,"createdBy":"admin"}]`))
		require.NoError(t, err)
		require.Len(t, versions, 1)
		assert.Equal(t, int64(2), versions[0].Version)
		assert.Equal(t, "admin", versions[0].CreatedBy)
	})
}
