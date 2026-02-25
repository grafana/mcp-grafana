//go:build unit

package tools

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	mcpgrafana "github.com/grafana/mcp-grafana"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newKubernetesMockServer creates a mock server that simulates a kubernetes-only Grafana.
// Legacy dashboard endpoints return 406; kubernetes endpoints serve dashboards.
// Returns the server and atomic counters for legacy and kubernetes request counts.
func newKubernetesMockServer(t *testing.T) (server *httptest.Server, legacyHits, k8sHits *atomic.Int32) {
	t.Helper()

	legacyHits = &atomic.Int32{}
	k8sHits = &atomic.Int32{}

	apiGroupList := mcpgrafana.APIGroupList{
		Kind: "APIGroupList",
		Groups: []mcpgrafana.APIGroup{
			{
				Name: "dashboard.grafana.app",
				Versions: []mcpgrafana.GroupVersionInfo{
					{GroupVersion: "dashboard.grafana.app/v2beta1", Version: "v2beta1"},
				},
				PreferredVersion: mcpgrafana.GroupVersionInfo{
					GroupVersion: "dashboard.grafana.app/v2beta1",
					Version:      "v2beta1",
				},
			},
			{
				Name: "folder.grafana.app",
				Versions: []mcpgrafana.GroupVersionInfo{
					{GroupVersion: "folder.grafana.app/v1beta1", Version: "v1beta1"},
				},
				PreferredVersion: mcpgrafana.GroupVersionInfo{
					GroupVersion: "folder.grafana.app/v1beta1",
					Version:      "v1beta1",
				},
			},
		},
	}

	dashboards := map[string]mcpgrafana.KubernetesDashboard{
		"dash-1": {
			Kind:       "Dashboard",
			APIVersion: "dashboard.grafana.app/v2beta1",
			Metadata: mcpgrafana.KubernetesDashboardMetadata{
				Name:      "dash-1",
				Namespace: "default",
				Annotations: map[string]string{
					"grafana.app/folder": "folder-a",
				},
			},
			Spec: map[string]interface{}{
				"title":  "Dashboard One",
				"panels": []interface{}{},
			},
		},
		"dash-2": {
			Kind:       "Dashboard",
			APIVersion: "dashboard.grafana.app/v2beta1",
			Metadata: mcpgrafana.KubernetesDashboardMetadata{
				Name:      "dash-2",
				Namespace: "default",
				Annotations: map[string]string{
					"grafana.app/folder": "folder-b",
				},
			},
			Spec: map[string]interface{}{
				"title":  "Dashboard Two",
				"panels": []interface{}{},
			},
		},
	}

	mux := http.NewServeMux()

	// Legacy dashboard endpoint: always returns 406
	mux.HandleFunc("/api/dashboards/uid/", func(w http.ResponseWriter, r *http.Request) {
		legacyHits.Add(1)
		uid := r.URL.Path[len("/api/dashboards/uid/"):]
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotAcceptable)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"message": "dashboard api version not supported, use /apis/dashboard.grafana.app/v2beta1/namespaces/default/dashboards/" + uid + " instead",
		})
	})

	// Kubernetes API discovery
	mux.HandleFunc("/apis", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(apiGroupList)
	})

	// Kubernetes dashboard endpoint
	mux.HandleFunc("/apis/dashboard.grafana.app/v2beta1/namespaces/default/dashboards/", func(w http.ResponseWriter, r *http.Request) {
		k8sHits.Add(1)
		uid := r.URL.Path[len("/apis/dashboard.grafana.app/v2beta1/namespaces/default/dashboards/"):]
		dash, ok := dashboards[uid]
		if !ok {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"message": "dashboard not found",
			})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(dash)
	})

	server = httptest.NewServer(mux)
	return server, legacyHits, k8sHits
}

func TestKubernetesFallback_FullLifecycle(t *testing.T) {
	mcpgrafana.ResetGlobalCapabilityCache()

	server, legacyHits, k8sHits := newKubernetesMockServer(t)
	defer server.Close()

	ctx := createTestContextWithDiscovery(t, server)

	t.Run("first call triggers 406 fallback", func(t *testing.T) {
		result, err := getDashboardByUID(ctx, GetDashboardByUIDParams{UID: "dash-1"})
		require.NoError(t, err)
		require.NotNil(t, result)

		dashboardMap, ok := result.Dashboard.(map[string]interface{})
		require.True(t, ok)
		assert.Equal(t, "dash-1", dashboardMap["uid"])
		assert.Equal(t, "Dashboard One", dashboardMap["title"])
		assert.Equal(t, "folder-a", result.Meta.FolderUID)

		// First call: legacy was attempted (got 406), then kubernetes was called
		assert.Equal(t, int32(1), legacyHits.Load(), "legacy endpoint should be hit once for 406 detection")
		assert.Equal(t, int32(1), k8sHits.Load(), "kubernetes endpoint should be hit once for fallback")
	})

	t.Run("second call skips legacy via capability cache", func(t *testing.T) {
		// Reset counters
		legacyHits.Store(0)
		k8sHits.Store(0)

		result, err := getDashboardByUID(ctx, GetDashboardByUIDParams{UID: "dash-2"})
		require.NoError(t, err)
		require.NotNil(t, result)

		dashboardMap, ok := result.Dashboard.(map[string]interface{})
		require.True(t, ok)
		assert.Equal(t, "dash-2", dashboardMap["uid"])
		assert.Equal(t, "Dashboard Two", dashboardMap["title"])
		assert.Equal(t, "folder-b", result.Meta.FolderUID)

		// Second call: legacy should NOT be hit (cache says kubernetes)
		assert.Equal(t, int32(0), legacyHits.Load(), "legacy endpoint should not be hit when capability is cached")
		assert.Equal(t, int32(1), k8sHits.Load(), "kubernetes endpoint should be hit directly")
	})
}

func TestKubernetesFallback_LegacyPreferredWhenAvailable(t *testing.T) {
	mcpgrafana.ResetGlobalCapabilityCache()

	legacyDashboard := map[string]interface{}{
		"dashboard": map[string]interface{}{
			"uid":    "legacy-works",
			"title":  "Legacy Works Dashboard",
			"panels": []interface{}{},
		},
		"meta": map[string]interface{}{
			"slug":      "legacy-works",
			"folderUid": "legacy-folder",
		},
	}

	var legacyHits, k8sHits atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/apis":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(mcpgrafana.APIGroupList{
				Kind: "APIGroupList",
				Groups: []mcpgrafana.APIGroup{
					{
						Name: "dashboard.grafana.app",
						Versions: []mcpgrafana.GroupVersionInfo{
							{GroupVersion: "dashboard.grafana.app/v2beta1", Version: "v2beta1"},
						},
						PreferredVersion: mcpgrafana.GroupVersionInfo{
							GroupVersion: "dashboard.grafana.app/v2beta1",
							Version:      "v2beta1",
						},
					},
				},
			})
		case r.URL.Path == "/api/dashboards/uid/legacy-works":
			legacyHits.Add(1)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(legacyDashboard)
		default:
			if len(r.URL.Path) > 5 && r.URL.Path[:5] == "/apis" {
				k8sHits.Add(1)
			}
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	ctx := createTestContextWithDiscovery(t, server)

	result, err := getDashboardByUID(ctx, GetDashboardByUIDParams{UID: "legacy-works"})
	require.NoError(t, err)
	require.NotNil(t, result)

	dashboardMap, ok := result.Dashboard.(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "legacy-works", dashboardMap["uid"])
	assert.Equal(t, "Legacy Works Dashboard", dashboardMap["title"])

	// Legacy should be used when it works (no 406)
	assert.Equal(t, int32(1), legacyHits.Load(), "legacy endpoint should be called")
	assert.Equal(t, int32(0), k8sHits.Load(), "kubernetes endpoint should not be called when legacy works")
}

func TestKubernetesFallback_NotFoundAfter406(t *testing.T) {
	mcpgrafana.ResetGlobalCapabilityCache()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/apis":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(mcpgrafana.APIGroupList{
				Kind: "APIGroupList",
				Groups: []mcpgrafana.APIGroup{
					{
						Name: "dashboard.grafana.app",
						Versions: []mcpgrafana.GroupVersionInfo{
							{GroupVersion: "dashboard.grafana.app/v2beta1", Version: "v2beta1"},
						},
						PreferredVersion: mcpgrafana.GroupVersionInfo{
							GroupVersion: "dashboard.grafana.app/v2beta1",
							Version:      "v2beta1",
						},
					},
				},
			})
		case r.URL.Path == "/api/dashboards/uid/nonexistent":
			// Return 406 to trigger fallback
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotAcceptable)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"message": "dashboard api version not supported, use /apis/dashboard.grafana.app/v2beta1/namespaces/default/dashboards/nonexistent instead",
			})
		case r.URL.Path == "/apis/dashboard.grafana.app/v2beta1/namespaces/default/dashboards/nonexistent":
			// Kubernetes API also returns 404
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"message": "dashboard not found",
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	ctx := createTestContextWithDiscovery(t, server)

	result, err := getDashboardByUID(ctx, GetDashboardByUIDParams{UID: "nonexistent"})

	require.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "nonexistent")
}

func TestKubernetesFallback_IndependentAPIGroups(t *testing.T) {
	mcpgrafana.ResetGlobalCapabilityCache()

	server, legacyHits, _ := newKubernetesMockServer(t)
	defer server.Close()

	ctx := createTestContextWithDiscovery(t, server)

	// First: trigger 406 for dashboards to set capability to kubernetes
	_, err := getDashboardByUID(ctx, GetDashboardByUIDParams{UID: "dash-1"})
	require.NoError(t, err)

	// Verify dashboard capability is now kubernetes
	instance := mcpgrafana.GrafanaInstanceFromContext(ctx)
	require.NotNil(t, instance)
	assert.True(t, instance.ShouldUseKubernetesAPI(mcpgrafana.APIGroupDashboard),
		"dashboard API should be marked as kubernetes after 406")

	// Verify folder API is NOT affected by the dashboard 406
	assert.False(t, instance.ShouldUseKubernetesAPI(mcpgrafana.APIGroupFolder),
		"folder API capability should be independent from dashboard API")

	// Make another dashboard request and verify legacy is not called
	legacyHits.Store(0)
	_, err = getDashboardByUID(ctx, GetDashboardByUIDParams{UID: "dash-2"})
	require.NoError(t, err)
	assert.Equal(t, int32(0), legacyHits.Load(),
		"legacy should not be called for dashboards after 406 detection")
}

func TestKubernetesFallback_CacheExpiration(t *testing.T) {
	// Use a custom cache with very short TTL to test expiration behavior.
	// We can't easily test real TTL expiry in the global cache, but we can
	// verify that after the cache is reset, legacy is tried again.
	mcpgrafana.ResetGlobalCapabilityCache()

	server, legacyHits, k8sHits := newKubernetesMockServer(t)
	defer server.Close()

	ctx := createTestContextWithDiscovery(t, server)

	// First call: triggers 406 + kubernetes fallback
	result, err := getDashboardByUID(ctx, GetDashboardByUIDParams{UID: "dash-1"})
	require.NoError(t, err)
	require.NotNil(t, result)

	assert.Equal(t, int32(1), legacyHits.Load())
	assert.Equal(t, int32(1), k8sHits.Load())

	// Second call: goes directly to kubernetes (cache hit)
	legacyHits.Store(0)
	k8sHits.Store(0)
	_, err = getDashboardByUID(ctx, GetDashboardByUIDParams{UID: "dash-2"})
	require.NoError(t, err)
	assert.Equal(t, int32(0), legacyHits.Load(), "legacy should not be hit with cached capability")

	// Simulate cache expiration by resetting
	mcpgrafana.ResetGlobalCapabilityCache()
	legacyHits.Store(0)
	k8sHits.Store(0)

	// Re-discover APIs (needed after cache reset for kubernetes version info)
	instance := mcpgrafana.GrafanaInstanceFromContext(ctx)
	require.NotNil(t, instance)
	err = instance.DiscoverCapabilities(ctx)
	require.NoError(t, err)

	// After cache reset, legacy should be tried again (capability unknown)
	result, err = getDashboardByUID(ctx, GetDashboardByUIDParams{UID: "dash-1"})
	require.NoError(t, err)
	require.NotNil(t, result)

	assert.Equal(t, int32(1), legacyHits.Load(), "legacy should be tried again after cache expiry")
	assert.Equal(t, int32(1), k8sHits.Load(), "kubernetes should be called after 406 re-detection")
}
