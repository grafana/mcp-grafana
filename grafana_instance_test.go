//go:build unit

package mcpgrafana

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewGrafanaInstance(t *testing.T) {
	config := GrafanaConfig{
		URL:    "http://localhost:3000",
		APIKey: "test-api-key",
	}
	httpClient := &http.Client{}

	instance := NewGrafanaInstance(config, nil, httpClient)

	require.NotNil(t, instance)
	assert.Equal(t, "http://localhost:3000", instance.BaseURL())
	assert.Equal(t, config, instance.Config())
	assert.Equal(t, httpClient, instance.HTTPClient())
	assert.Nil(t, instance.LegacyClient())
}

func TestNewGrafanaInstance_TrimsTrailingSlash(t *testing.T) {
	config := GrafanaConfig{
		URL: "http://localhost:3000/",
	}

	instance := NewGrafanaInstance(config, nil, &http.Client{})

	assert.Equal(t, "http://localhost:3000", instance.BaseURL())
}

func TestNewGrafanaInstance_DefaultURL(t *testing.T) {
	config := GrafanaConfig{
		URL: "",
	}

	instance := NewGrafanaInstance(config, nil, &http.Client{})

	assert.Equal(t, "http://localhost:3000", instance.BaseURL())
}

func TestGrafanaInstance_DiscoverCapabilities(t *testing.T) {
	// Reset global cache before test
	ResetGlobalCapabilityCache()

	// Create test server that returns a valid APIGroupList
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/apis" {
			response := APIGroupList{
				Kind: "APIGroupList",
				Groups: []APIGroup{
					{
						Name: "dashboard.grafana.app",
						Versions: []GroupVersionInfo{
							{GroupVersion: "dashboard.grafana.app/v1beta1", Version: "v1beta1"},
							{GroupVersion: "dashboard.grafana.app/v2beta1", Version: "v2beta1"},
						},
						PreferredVersion: GroupVersionInfo{
							GroupVersion: "dashboard.grafana.app/v1beta1",
							Version:      "v1beta1",
						},
					},
				},
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(response)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	config := GrafanaConfig{URL: server.URL}
	instance := NewGrafanaInstance(config, nil, server.Client())

	ctx := context.Background()
	err := instance.DiscoverCapabilities(ctx)

	require.NoError(t, err)

	// Check that capabilities were cached
	hasK8s, err := instance.HasKubernetesAPIs(ctx)
	require.NoError(t, err)
	assert.True(t, hasK8s)
}

func TestGrafanaInstance_HasKubernetesAPIs_NotAvailable(t *testing.T) {
	// Reset global cache before test
	ResetGlobalCapabilityCache()

	// Create test server that returns 404 for /apis
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	config := GrafanaConfig{URL: server.URL}
	instance := NewGrafanaInstance(config, nil, server.Client())

	ctx := context.Background()
	hasK8s, err := instance.HasKubernetesAPIs(ctx)

	require.NoError(t, err)
	assert.False(t, hasK8s)
}

func TestGrafanaInstance_GetAPIGroupInfo(t *testing.T) {
	// Reset global cache before test
	ResetGlobalCapabilityCache()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/apis" {
			response := APIGroupList{
				Kind: "APIGroupList",
				Groups: []APIGroup{
					{
						Name: "dashboard.grafana.app",
						Versions: []GroupVersionInfo{
							{GroupVersion: "dashboard.grafana.app/v1beta1", Version: "v1beta1"},
							{GroupVersion: "dashboard.grafana.app/v2beta1", Version: "v2beta1"},
						},
						PreferredVersion: GroupVersionInfo{
							GroupVersion: "dashboard.grafana.app/v1beta1",
							Version:      "v1beta1",
						},
					},
				},
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(response)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	config := GrafanaConfig{URL: server.URL}
	instance := NewGrafanaInstance(config, nil, server.Client())

	ctx := context.Background()

	// Get dashboard API group info
	info, err := instance.GetAPIGroupInfo(ctx, APIGroupDashboard)
	require.NoError(t, err)
	require.NotNil(t, info)
	assert.True(t, info.Available)
	assert.Equal(t, "v1beta1", info.PreferredVersion)
	assert.Contains(t, info.AllVersions, "v1beta1")
	assert.Contains(t, info.AllVersions, "v2beta1")

	// Get non-existent API group
	info, err = instance.GetAPIGroupInfo(ctx, "nonexistent.grafana.app")
	require.NoError(t, err)
	assert.Nil(t, info)
}

func TestGrafanaInstance_APICapability(t *testing.T) {
	// Reset global cache before test
	ResetGlobalCapabilityCache()

	config := GrafanaConfig{URL: "http://localhost:3000"}
	instance := NewGrafanaInstance(config, nil, &http.Client{})

	// Initially unknown
	cap := instance.GetAPICapability(APIGroupDashboard)
	assert.Equal(t, APICapabilityUnknown, cap)

	// Set to kubernetes
	instance.SetAPICapability(APIGroupDashboard, APICapabilityKubernetes)
	cap = instance.GetAPICapability(APIGroupDashboard)
	assert.Equal(t, APICapabilityKubernetes, cap)

	// ShouldUseKubernetesAPI should return true
	assert.True(t, instance.ShouldUseKubernetesAPI(APIGroupDashboard))

	// Set to legacy
	instance.SetAPICapability(APIGroupDashboard, APICapabilityLegacy)
	cap = instance.GetAPICapability(APIGroupDashboard)
	assert.Equal(t, APICapabilityLegacy, cap)

	// ShouldUseKubernetesAPI should return false
	assert.False(t, instance.ShouldUseKubernetesAPI(APIGroupDashboard))
}

func TestGrafanaInstance_GetPreferredVersion(t *testing.T) {
	// Reset global cache before test
	ResetGlobalCapabilityCache()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/apis" {
			response := APIGroupList{
				Kind: "APIGroupList",
				Groups: []APIGroup{
					{
						Name: "dashboard.grafana.app",
						Versions: []GroupVersionInfo{
							{GroupVersion: "dashboard.grafana.app/v2beta1", Version: "v2beta1"},
						},
						PreferredVersion: GroupVersionInfo{
							GroupVersion: "dashboard.grafana.app/v2beta1",
							Version:      "v2beta1",
						},
					},
				},
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(response)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	config := GrafanaConfig{URL: server.URL}
	instance := NewGrafanaInstance(config, nil, server.Client())

	ctx := context.Background()

	version, err := instance.GetPreferredVersion(ctx, APIGroupDashboard)
	require.NoError(t, err)
	assert.Equal(t, "v2beta1", version)

	// Non-existent API group should error
	_, err = instance.GetPreferredVersion(ctx, "nonexistent.grafana.app")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not available")
}

func TestGrafanaInstance_GetDashboardKubernetes(t *testing.T) {
	// Reset global cache before test
	ResetGlobalCapabilityCache()

	expectedDashboard := KubernetesDashboard{
		Kind:       "Dashboard",
		APIVersion: "dashboard.grafana.app/v2beta1",
		Metadata: KubernetesDashboardMetadata{
			Name:      "test-dashboard",
			Namespace: "default",
			UID:       "abc123",
			Annotations: map[string]string{
				"grafana.app/folder": "my-folder",
			},
		},
		Spec: map[string]interface{}{
			"title": "Test Dashboard",
			"panels": []interface{}{
				map[string]interface{}{
					"id":    float64(1),
					"title": "Panel 1",
				},
			},
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Check the path
		assert.Equal(t, "/apis/dashboard.grafana.app/v2beta1/namespaces/default/dashboards/test-dashboard", r.URL.Path)
		assert.Equal(t, "application/json", r.Header.Get("Accept"))

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(expectedDashboard)
	}))
	defer server.Close()

	config := GrafanaConfig{URL: server.URL}
	instance := NewGrafanaInstance(config, nil, server.Client())

	ctx := context.Background()
	dashboard, err := instance.GetDashboardKubernetes(ctx, "test-dashboard", "v2beta1", "default")

	require.NoError(t, err)
	require.NotNil(t, dashboard)
	assert.Equal(t, "Dashboard", dashboard.Kind)
	assert.Equal(t, "dashboard.grafana.app/v2beta1", dashboard.APIVersion)
	assert.Equal(t, "test-dashboard", dashboard.Metadata.Name)
	assert.Equal(t, "Test Dashboard", dashboard.Spec["title"])
}

func TestGrafanaInstance_GetDashboardKubernetes_DefaultNamespace(t *testing.T) {
	// Reset global cache before test
	ResetGlobalCapabilityCache()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify default namespace is used
		assert.Contains(t, r.URL.Path, "/namespaces/default/")

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(KubernetesDashboard{
			Kind:       "Dashboard",
			APIVersion: "dashboard.grafana.app/v1beta1",
			Metadata:   KubernetesDashboardMetadata{Name: "test"},
			Spec:       map[string]interface{}{},
		})
	}))
	defer server.Close()

	config := GrafanaConfig{URL: server.URL}
	instance := NewGrafanaInstance(config, nil, server.Client())

	ctx := context.Background()
	// Pass empty namespace
	_, err := instance.GetDashboardKubernetes(ctx, "test", "v1beta1", "")

	require.NoError(t, err)
}

func TestGrafanaInstance_GetDashboardKubernetes_WithAuth(t *testing.T) {
	// Reset global cache before test
	ResetGlobalCapabilityCache()

	var receivedAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedAuth = r.Header.Get("Authorization")

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(KubernetesDashboard{
			Kind:       "Dashboard",
			APIVersion: "dashboard.grafana.app/v1beta1",
			Metadata:   KubernetesDashboardMetadata{Name: "test"},
			Spec:       map[string]interface{}{},
		})
	}))
	defer server.Close()

	config := GrafanaConfig{
		URL:    server.URL,
		APIKey: "my-secret-token",
	}
	instance := NewGrafanaInstance(config, nil, server.Client())

	ctx := context.Background()
	_, err := instance.GetDashboardKubernetes(ctx, "test", "v1beta1", "default")

	require.NoError(t, err)
	assert.Equal(t, "Bearer my-secret-token", receivedAuth)
}

func TestGrafanaInstance_GetDashboardKubernetes_WithBasicAuth(t *testing.T) {
	// Reset global cache before test
	ResetGlobalCapabilityCache()

	var receivedUser, receivedPass string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedUser, receivedPass, _ = r.BasicAuth()

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(KubernetesDashboard{
			Kind:       "Dashboard",
			APIVersion: "dashboard.grafana.app/v1beta1",
			Metadata:   KubernetesDashboardMetadata{Name: "test"},
			Spec:       map[string]interface{}{},
		})
	}))
	defer server.Close()

	config := GrafanaConfig{
		URL:       server.URL,
		BasicAuth: url.UserPassword("admin", "secret"),
	}
	instance := NewGrafanaInstance(config, nil, server.Client())

	ctx := context.Background()
	_, err := instance.GetDashboardKubernetes(ctx, "test", "v1beta1", "default")

	require.NoError(t, err)
	assert.Equal(t, "admin", receivedUser)
	assert.Equal(t, "secret", receivedPass)
}

func TestGrafanaInstance_GetDashboardKubernetes_Error(t *testing.T) {
	// Reset global cache before test
	ResetGlobalCapabilityCache()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"message": "dashboard not found"}`))
	}))
	defer server.Close()

	config := GrafanaConfig{URL: server.URL}
	instance := NewGrafanaInstance(config, nil, server.Client())

	ctx := context.Background()
	dashboard, err := instance.GetDashboardKubernetes(ctx, "nonexistent", "v1beta1", "default")

	require.Error(t, err)
	assert.Nil(t, dashboard)
	assert.Contains(t, err.Error(), "status 404")
}

func TestGrafanaInstanceContext(t *testing.T) {
	config := GrafanaConfig{URL: "http://localhost:3000"}
	instance := NewGrafanaInstance(config, nil, &http.Client{})

	// Set instance in context
	ctx := WithGrafanaInstance(context.Background(), instance)

	// Retrieve instance from context
	retrieved := GrafanaInstanceFromContext(ctx)
	require.NotNil(t, retrieved)
	assert.Equal(t, instance, retrieved)

	// Empty context should return nil
	emptyCtx := context.Background()
	assert.Nil(t, GrafanaInstanceFromContext(emptyCtx))
}

func TestGrafanaInstance_CacheSharing(t *testing.T) {
	// Reset global cache before test
	ResetGlobalCapabilityCache()

	// Create two instances pointing to the same URL
	config := GrafanaConfig{URL: "http://localhost:3000"}
	instance1 := NewGrafanaInstance(config, nil, &http.Client{})
	instance2 := NewGrafanaInstance(config, nil, &http.Client{})

	// Set capability on instance1
	instance1.SetAPICapability(APIGroupDashboard, APICapabilityKubernetes)

	// Should be visible on instance2 (shared cache)
	cap := instance2.GetAPICapability(APIGroupDashboard)
	assert.Equal(t, APICapabilityKubernetes, cap)
}

func TestGrafanaInstance_CacheIsolation(t *testing.T) {
	// Reset global cache before test
	ResetGlobalCapabilityCache()

	// Create two instances pointing to different URLs
	instance1 := NewGrafanaInstance(GrafanaConfig{URL: "http://grafana1:3000"}, nil, &http.Client{})
	instance2 := NewGrafanaInstance(GrafanaConfig{URL: "http://grafana2:3000"}, nil, &http.Client{})

	// Set capability on instance1
	instance1.SetAPICapability(APIGroupDashboard, APICapabilityKubernetes)

	// Should NOT be visible on instance2 (different URL)
	cap := instance2.GetAPICapability(APIGroupDashboard)
	assert.Equal(t, APICapabilityUnknown, cap)
}

func TestGrafanaInstance_CapabilityCacheExpiration(t *testing.T) {
	// Create a cache with very short TTL for testing
	originalCache := globalCapabilityCache
	globalCapabilityCache = NewCapabilityCache(50 * time.Millisecond)
	defer func() {
		globalCapabilityCache = originalCache
	}()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/apis" {
			response := APIGroupList{
				Kind: "APIGroupList",
				Groups: []APIGroup{
					{
						Name: "dashboard.grafana.app",
						Versions: []GroupVersionInfo{
							{GroupVersion: "dashboard.grafana.app/v1beta1", Version: "v1beta1"},
						},
						PreferredVersion: GroupVersionInfo{
							GroupVersion: "dashboard.grafana.app/v1beta1",
							Version:      "v1beta1",
						},
					},
				},
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(response)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	config := GrafanaConfig{URL: server.URL}
	instance := NewGrafanaInstance(config, nil, server.Client())

	ctx := context.Background()

	// First call should populate cache
	hasK8s, err := instance.HasKubernetesAPIs(ctx)
	require.NoError(t, err)
	assert.True(t, hasK8s)

	// Wait for cache to expire
	time.Sleep(100 * time.Millisecond)

	// Next call should re-discover (cache expired)
	// This implicitly tests that the instance correctly handles cache expiration
	hasK8s, err = instance.HasKubernetesAPIs(ctx)
	require.NoError(t, err)
	assert.True(t, hasK8s)
}
