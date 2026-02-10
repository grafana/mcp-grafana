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

// createTestContext creates a context with both legacy client and GrafanaInstance
// pointing to the test server.
func createTestContext(server *httptest.Server) context.Context {
	u, _ := url.Parse(server.URL)
	cfg := client.DefaultTransportConfig()
	cfg.Host = u.Host
	cfg.Schemes = []string{"http"}
	cfg.APIKey = "test-api-key"

	legacyClient := client.NewHTTPClientWithConfig(nil, cfg)

	config := mcpgrafana.GrafanaConfig{
		URL:    server.URL,
		APIKey: "test-api-key",
	}

	instance := mcpgrafana.NewGrafanaInstance(config, legacyClient, server.Client())

	ctx := context.Background()
	ctx = mcpgrafana.WithGrafanaClient(ctx, legacyClient)
	ctx = mcpgrafana.WithGrafanaInstance(ctx, instance)

	return ctx
}

// createTestContextWithDiscovery creates a test context and pre-discovers API capabilities
// by making a request to /apis endpoint.
func createTestContextWithDiscovery(t *testing.T, server *httptest.Server) context.Context {
	ctx := createTestContext(server)
	instance := mcpgrafana.GrafanaInstanceFromContext(ctx)
	require.NotNil(t, instance)

	// Trigger API discovery
	err := instance.DiscoverCapabilities(ctx)
	require.NoError(t, err)

	return ctx
}

// createLegacyOnlyContext creates a context with only the legacy client (no GrafanaInstance)
func createLegacyOnlyContext(server *httptest.Server) context.Context {
	u, _ := url.Parse(server.URL)
	cfg := client.DefaultTransportConfig()
	cfg.Host = u.Host
	cfg.Schemes = []string{"http"}
	cfg.APIKey = "test-api-key"

	legacyClient := client.NewHTTPClientWithConfig(nil, cfg)
	return mcpgrafana.WithGrafanaClient(context.Background(), legacyClient)
}

func TestGetDashboardByUID_LegacyAPI(t *testing.T) {
	mcpgrafana.ResetGlobalCapabilityCache()

	dashboardResponse := map[string]interface{}{
		"dashboard": map[string]interface{}{
			"uid":    "test-uid",
			"title":  "Test Dashboard",
			"panels": []interface{}{},
		},
		"meta": map[string]interface{}{
			"slug":      "test-dashboard",
			"folderUid": "folder-123",
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/dashboards/uid/test-uid" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(dashboardResponse)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	ctx := createTestContext(server)

	result, err := getDashboardByUID(ctx, GetDashboardByUIDParams{UID: "test-uid"})

	require.NoError(t, err)
	require.NotNil(t, result)

	dashboardMap, ok := result.Dashboard.(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "test-uid", dashboardMap["uid"])
	assert.Equal(t, "Test Dashboard", dashboardMap["title"])
}

func TestGetDashboardByUID_LegacyOnlyFallback(t *testing.T) {
	mcpgrafana.ResetGlobalCapabilityCache()

	dashboardResponse := map[string]interface{}{
		"dashboard": map[string]interface{}{
			"uid":   "legacy-uid",
			"title": "Legacy Dashboard",
		},
		"meta": map[string]interface{}{
			"slug": "legacy-dashboard",
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/dashboards/uid/legacy-uid" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(dashboardResponse)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	// Use context without GrafanaInstance
	ctx := createLegacyOnlyContext(server)

	result, err := getDashboardByUID(ctx, GetDashboardByUIDParams{UID: "legacy-uid"})

	require.NoError(t, err)
	require.NotNil(t, result)

	dashboardMap, ok := result.Dashboard.(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "legacy-uid", dashboardMap["uid"])
}

func TestGetDashboardByUID_406FallbackToKubernetes(t *testing.T) {
	mcpgrafana.ResetGlobalCapabilityCache()

	k8sDashboard := mcpgrafana.KubernetesDashboard{
		Kind:       "Dashboard",
		APIVersion: "dashboard.grafana.app/v2beta1",
		Metadata: mcpgrafana.KubernetesDashboardMetadata{
			Name:      "k8s-dashboard-uid",
			Namespace: "default",
			Annotations: map[string]string{
				"grafana.app/folder": "k8s-folder",
			},
		},
		Spec: map[string]interface{}{
			"title": "Kubernetes Dashboard",
			"panels": []interface{}{
				map[string]interface{}{
					"id":    float64(1),
					"title": "Panel 1",
				},
			},
		},
	}

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
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/dashboards/uid/k8s-dashboard-uid":
			// Return 406 to trigger kubernetes API fallback
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotAcceptable)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"message": "dashboard api version not supported, use /apis/dashboard.grafana.app/v2beta1/namespaces/default/dashboards/k8s-dashboard-uid instead",
			})
		case "/apis":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(apiGroupList)
		case "/apis/dashboard.grafana.app/v2beta1/namespaces/default/dashboards/k8s-dashboard-uid":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(k8sDashboard)
		default:
			t.Logf("Unexpected request path: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	// Use createTestContextWithDiscovery to trigger /apis call first
	ctx := createTestContextWithDiscovery(t, server)

	result, err := getDashboardByUID(ctx, GetDashboardByUIDParams{UID: "k8s-dashboard-uid"})

	require.NoError(t, err)
	require.NotNil(t, result)

	dashboardMap, ok := result.Dashboard.(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "k8s-dashboard-uid", dashboardMap["uid"])
	assert.Equal(t, "Kubernetes Dashboard", dashboardMap["title"])
	assert.Equal(t, "k8s-folder", result.Meta.FolderUID)
}

func TestGetDashboardByUID_DirectKubernetesWhenCapabilitySet(t *testing.T) {
	mcpgrafana.ResetGlobalCapabilityCache()

	k8sDashboard := mcpgrafana.KubernetesDashboard{
		Kind:       "Dashboard",
		APIVersion: "dashboard.grafana.app/v1beta1",
		Metadata: mcpgrafana.KubernetesDashboardMetadata{
			Name:      "direct-k8s-uid",
			Namespace: "default",
		},
		Spec: map[string]interface{}{
			"title":  "Direct Kubernetes Dashboard",
			"panels": []interface{}{},
		},
	}

	apiGroupList := mcpgrafana.APIGroupList{
		Kind: "APIGroupList",
		Groups: []mcpgrafana.APIGroup{
			{
				Name: "dashboard.grafana.app",
				Versions: []mcpgrafana.GroupVersionInfo{
					{GroupVersion: "dashboard.grafana.app/v1beta1", Version: "v1beta1"},
				},
				PreferredVersion: mcpgrafana.GroupVersionInfo{
					GroupVersion: "dashboard.grafana.app/v1beta1",
					Version:      "v1beta1",
				},
			},
		},
	}

	legacyAPICalled := false

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/dashboards/uid/direct-k8s-uid":
			legacyAPICalled = true
			w.WriteHeader(http.StatusNotFound)
		case "/apis":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(apiGroupList)
		case "/apis/dashboard.grafana.app/v1beta1/namespaces/default/dashboards/direct-k8s-uid":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(k8sDashboard)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	// Use createTestContextWithDiscovery to populate the cache with API group info
	ctx := createTestContextWithDiscovery(t, server)

	// Pre-set the capability to kubernetes
	instance := mcpgrafana.GrafanaInstanceFromContext(ctx)
	require.NotNil(t, instance)
	instance.SetAPICapability(mcpgrafana.APIGroupDashboard, mcpgrafana.APICapabilityKubernetes)

	result, err := getDashboardByUID(ctx, GetDashboardByUIDParams{UID: "direct-k8s-uid"})

	require.NoError(t, err)
	require.NotNil(t, result)

	// Legacy API should not have been called since we pre-set kubernetes capability
	assert.False(t, legacyAPICalled, "Legacy API should not be called when kubernetes capability is set")

	dashboardMap, ok := result.Dashboard.(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "Direct Kubernetes Dashboard", dashboardMap["title"])
}

func TestGetDashboardByUID_NotFound(t *testing.T) {
	mcpgrafana.ResetGlobalCapabilityCache()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"message": "Dashboard not found",
		})
	}))
	defer server.Close()

	ctx := createTestContext(server)

	result, err := getDashboardByUID(ctx, GetDashboardByUIDParams{UID: "nonexistent"})

	require.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "nonexistent")
}

func TestConvertKubernetesDashboardToLegacy(t *testing.T) {
	t.Run("basic conversion", func(t *testing.T) {
		k8sDashboard := &mcpgrafana.KubernetesDashboard{
			Kind:       "Dashboard",
			APIVersion: "dashboard.grafana.app/v2beta1",
			Metadata: mcpgrafana.KubernetesDashboardMetadata{
				Name:      "test-uid",
				Namespace: "default",
				UID:       "resource-uid",
				Annotations: map[string]string{
					"grafana.app/folder": "folder-abc",
				},
			},
			Spec: map[string]interface{}{
				"title":       "Test Dashboard",
				"description": "A test dashboard",
				"panels": []interface{}{
					map[string]interface{}{
						"id":    float64(1),
						"title": "Panel 1",
						"type":  "graph",
					},
				},
			},
		}

		result, err := convertKubernetesDashboardToLegacy(k8sDashboard)

		require.NoError(t, err)
		require.NotNil(t, result)

		// Check dashboard content
		dashboardMap, ok := result.Dashboard.(map[string]interface{})
		require.True(t, ok)
		assert.Equal(t, "Test Dashboard", dashboardMap["title"])
		assert.Equal(t, "test-uid", dashboardMap["uid"])

		// Check meta
		require.NotNil(t, result.Meta)
		assert.Equal(t, "folder-abc", result.Meta.FolderUID)
		assert.Equal(t, "test-uid", result.Meta.Slug)
	})

	t.Run("preserves existing uid in spec", func(t *testing.T) {
		k8sDashboard := &mcpgrafana.KubernetesDashboard{
			Kind:       "Dashboard",
			APIVersion: "dashboard.grafana.app/v1beta1",
			Metadata: mcpgrafana.KubernetesDashboardMetadata{
				Name:      "metadata-name",
				Namespace: "default",
			},
			Spec: map[string]interface{}{
				"uid":   "existing-spec-uid",
				"title": "Dashboard with existing UID",
			},
		}

		result, err := convertKubernetesDashboardToLegacy(k8sDashboard)

		require.NoError(t, err)
		dashboardMap, ok := result.Dashboard.(map[string]interface{})
		require.True(t, ok)
		// Should preserve the existing UID in spec
		assert.Equal(t, "existing-spec-uid", dashboardMap["uid"])
	})

	t.Run("handles missing annotations", func(t *testing.T) {
		k8sDashboard := &mcpgrafana.KubernetesDashboard{
			Kind:       "Dashboard",
			APIVersion: "dashboard.grafana.app/v1beta1",
			Metadata: mcpgrafana.KubernetesDashboardMetadata{
				Name:      "no-annotations",
				Namespace: "default",
				// No annotations
			},
			Spec: map[string]interface{}{
				"title": "Dashboard without annotations",
			},
		}

		result, err := convertKubernetesDashboardToLegacy(k8sDashboard)

		require.NoError(t, err)
		require.NotNil(t, result)
		assert.Empty(t, result.Meta.FolderUID)
	})

	t.Run("handles nil spec", func(t *testing.T) {
		k8sDashboard := &mcpgrafana.KubernetesDashboard{
			Kind:       "Dashboard",
			APIVersion: "dashboard.grafana.app/v1beta1",
			Metadata: mcpgrafana.KubernetesDashboardMetadata{
				Name:      "nil-spec",
				Namespace: "default",
			},
			Spec: nil,
		}

		result, err := convertKubernetesDashboardToLegacy(k8sDashboard)

		require.NoError(t, err)
		require.NotNil(t, result)
		// Spec should be nil, not panic
		assert.Nil(t, result.Dashboard)
	})
}

func TestParse406Error_Integration(t *testing.T) {
	testCases := []struct {
		name        string
		errMsg      string
		wantGroup   string
		wantVersion string
		wantOK      bool
	}{
		{
			name:        "standard 406 error",
			errMsg:      "[GET /dashboards/uid/{uid}][406] getDashboardByUidNotAcceptable {\"message\":\"dashboard api version not supported, use /apis/dashboard.grafana.app/v2beta1/namespaces/default/dashboards/ad8nwk6 instead\"}",
			wantGroup:   "dashboard.grafana.app",
			wantVersion: "v2beta1",
			wantOK:      true,
		},
		{
			name:        "simple 406 message",
			errMsg:      "dashboard api version not supported, use /apis/dashboard.grafana.app/v1beta1/namespaces/default/dashboards/xyz instead",
			wantGroup:   "dashboard.grafana.app",
			wantVersion: "v1beta1",
			wantOK:      true,
		},
		{
			name:   "unrelated error",
			errMsg: "connection refused",
			wantOK: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			group, version, ok := mcpgrafana.Parse406Error(tc.errMsg)
			assert.Equal(t, tc.wantOK, ok)
			if tc.wantOK {
				assert.Equal(t, tc.wantGroup, group)
				assert.Equal(t, tc.wantVersion, version)
			}
		})
	}
}
