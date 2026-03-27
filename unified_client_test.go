package mcpgrafana

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/grafana/grafana-openapi-client-go/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestK8sServer creates an httptest server that serves /apis discovery and
// individual resource GET requests. It returns the server and a counter for
// how many times /apis was called (for caching tests).
func newTestK8sServer(t *testing.T, groups []APIGroup, resources map[string]map[string]interface{}) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	discoverCount := &atomic.Int32{}

	mux := http.NewServeMux()

	mux.HandleFunc("/apis", func(w http.ResponseWriter, r *http.Request) {
		discoverCount.Add(1)
		resp := APIGroupList{
			Kind:   "APIGroupList",
			Groups: groups,
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	})

	// Serve individual resources.
	for path, obj := range resources {
		obj := obj // capture
		mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(obj)
		})
	}

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, discoverCount
}

func testContext() context.Context {
	ctx := context.Background()
	return WithGrafanaConfig(ctx, GrafanaConfig{
		APIKey: "test-api-key",
	})
}

func TestDiscoverCapabilities_K8sAvailable(t *testing.T) {
	groups := []APIGroup{
		{
			Name: "dashboard.grafana.app",
			Versions: []GroupVersionInfo{
				{GroupVersion: "dashboard.grafana.app/v0alpha1", Version: "v0alpha1"},
			},
			PreferredVersion: GroupVersionInfo{
				GroupVersion: "dashboard.grafana.app/v0alpha1",
				Version:      "v0alpha1",
			},
		},
	}

	srv, discoverCount := newTestK8sServer(t, groups, nil)

	k8s := &KubernetesClient{BaseURL: srv.URL, HTTPClient: srv.Client()}
	client := NewGrafanaAPIClient(nil, k8s)

	ctx := testContext()
	caps := client.discoverCapabilities(ctx)

	assert.True(t, caps.hasK8sAPIs)
	assert.NotNil(t, caps.registry)
	assert.True(t, caps.registry.HasGroup("dashboard.grafana.app"))
	assert.Equal(t, int32(1), discoverCount.Load())
}

func TestDiscoverCapabilities_NoK8sClient(t *testing.T) {
	client := NewGrafanaAPIClient(nil, nil)
	ctx := testContext()

	caps := client.discoverCapabilities(ctx)
	assert.False(t, caps.hasK8sAPIs)
	assert.Nil(t, caps.registry)
}

func TestDiscoverCapabilities_CachingSecondCallSkipsDiscovery(t *testing.T) {
	groups := []APIGroup{
		{
			Name: "dashboard.grafana.app",
			Versions: []GroupVersionInfo{
				{GroupVersion: "dashboard.grafana.app/v0alpha1", Version: "v0alpha1"},
			},
			PreferredVersion: GroupVersionInfo{
				GroupVersion: "dashboard.grafana.app/v0alpha1",
				Version:      "v0alpha1",
			},
		},
	}

	srv, discoverCount := newTestK8sServer(t, groups, nil)

	k8s := &KubernetesClient{BaseURL: srv.URL, HTTPClient: srv.Client()}
	client := NewGrafanaAPIClient(nil, k8s)

	ctx := testContext()

	// First call: triggers discovery.
	caps1 := client.discoverCapabilities(ctx)
	assert.True(t, caps1.hasK8sAPIs)
	assert.Equal(t, int32(1), discoverCount.Load())

	// Second call: should use cache.
	caps2 := client.discoverCapabilities(ctx)
	assert.True(t, caps2.hasK8sAPIs)
	assert.Equal(t, int32(1), discoverCount.Load(), "second call should not trigger discovery")
}

func TestDiscoverCapabilities_DiscoveryError(t *testing.T) {
	// Server that returns 404 for /apis.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)

	k8s := &KubernetesClient{BaseURL: srv.URL, HTTPClient: srv.Client()}
	client := NewGrafanaAPIClient(nil, k8s)

	ctx := testContext()
	caps := client.discoverCapabilities(ctx)
	assert.False(t, caps.hasK8sAPIs)
}

func TestGetDashboardByUID_ViaK8s(t *testing.T) {
	groups := []APIGroup{
		{
			Name: "dashboard.grafana.app",
			Versions: []GroupVersionInfo{
				{GroupVersion: "dashboard.grafana.app/v0alpha1", Version: "v0alpha1"},
			},
			PreferredVersion: GroupVersionInfo{
				GroupVersion: "dashboard.grafana.app/v0alpha1",
				Version:      "v0alpha1",
			},
		},
	}

	dashObj := map[string]interface{}{
		"apiVersion": "dashboard.grafana.app/v0alpha1",
		"kind":       "Dashboard",
		"metadata": map[string]interface{}{
			"name":      "test-uid-123",
			"namespace": "default",
			"annotations": map[string]interface{}{
				"grafana.app/folder": "folder-abc",
			},
		},
		"spec": map[string]interface{}{
			"title": "My Test Dashboard",
			"panels": []interface{}{
				map[string]interface{}{"id": 1, "title": "Panel 1"},
			},
		},
	}

	resources := map[string]map[string]interface{}{
		"/apis/dashboard.grafana.app/v0alpha1/namespaces/default/dashboards/test-uid-123": dashObj,
	}

	srv, _ := newTestK8sServer(t, groups, resources)

	k8s := &KubernetesClient{BaseURL: srv.URL, HTTPClient: srv.Client()}
	client := NewGrafanaAPIClient(nil, k8s)

	ctx := testContext()
	dashboard, err := client.GetDashboardByUID(ctx, "test-uid-123")
	require.NoError(t, err)
	require.NotNil(t, dashboard)

	// Verify the conversion.
	spec, ok := dashboard.Dashboard.(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "My Test Dashboard", spec["title"])
	assert.Equal(t, "test-uid-123", spec["uid"])

	require.NotNil(t, dashboard.Meta)
	assert.Equal(t, "folder-abc", dashboard.Meta.FolderUID)
	assert.Equal(t, "test-uid-123", dashboard.Meta.Slug)
}

func TestGetDashboardByUID_FallbackToLegacy(t *testing.T) {
	// No k8s APIs available => should use legacy.
	// We simulate by having the server return empty groups.
	srv, _ := newTestK8sServer(t, nil, nil)

	k8s := &KubernetesClient{BaseURL: srv.URL, HTTPClient: srv.Client()}

	// We can't easily create a real legacy client in unit tests, so we test
	// the routing logic: with no k8s groups, shouldUseK8s should be false.
	client := NewGrafanaAPIClient(nil, k8s)

	ctx := testContext()
	assert.False(t, client.shouldUseK8s(ctx, "dashboard.grafana.app"))
}

func TestGetDashboardByUID_406RetryLegacyCalledFirst(t *testing.T) {
	// When discovery returns no groups, legacy is called first.
	// If legacy returns 406, the client attempts k8s retry (which also fails
	// since no groups are available). The key assertion: legacy WAS called.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/apis" {
			resp := APIGroupList{Kind: "APIGroupList", Groups: nil}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(resp)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)

	k8s := &KubernetesClient{BaseURL: srv.URL, HTTPClient: srv.Client()}
	client := NewGrafanaAPIClient(nil, k8s)
	ctx := testContext()

	simulated406 := fmt.Errorf("simulated 406")
	legacyCalled := false

	result, err := client.getResource(ctx, getResourceOpts{
		apiGroup:  "dashboard.grafana.app",
		resource:  "dashboards",
		name:      "retry-uid",
		namespace: "default",
		legacyFetch: func(ctx context.Context) (interface{}, error) {
			legacyCalled = true
			return nil, simulated406
		},
		convert: convertK8sDashboard,
		check406: func(err error) bool {
			return err == simulated406
		},
	})

	assert.True(t, legacyCalled)
	// k8s retry fails because no groups are available.
	assert.Error(t, err)
	assert.Nil(t, result)
}

func TestGetDashboardByUID_406RetrySuccess(t *testing.T) {
	// Simulate a server that initially has no k8s groups (so legacy is tried),
	// then after a 406 and cache invalidation, re-discovery finds the groups.
	var discoverCallCount atomic.Int32
	groups := []APIGroup{
		{
			Name: "dashboard.grafana.app",
			Versions: []GroupVersionInfo{
				{GroupVersion: "dashboard.grafana.app/v0alpha1", Version: "v0alpha1"},
			},
			PreferredVersion: GroupVersionInfo{
				GroupVersion: "dashboard.grafana.app/v0alpha1",
				Version:      "v0alpha1",
			},
		},
	}

	dashObj := map[string]interface{}{
		"apiVersion": "dashboard.grafana.app/v0alpha1",
		"kind":       "Dashboard",
		"metadata": map[string]interface{}{
			"name":      "retry-uid",
			"namespace": "default",
			"annotations": map[string]interface{}{
				"grafana.app/folder": "folder-xyz",
			},
		},
		"spec": map[string]interface{}{
			"title": "Retry Dashboard",
		},
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/apis", func(w http.ResponseWriter, r *http.Request) {
		call := discoverCallCount.Add(1)
		w.Header().Set("Content-Type", "application/json")
		if call == 1 {
			// First discovery: no groups.
			json.NewEncoder(w).Encode(APIGroupList{Kind: "APIGroupList"})
		} else {
			// After cache invalidation: groups appear.
			json.NewEncoder(w).Encode(APIGroupList{Kind: "APIGroupList", Groups: groups})
		}
	})
	mux.HandleFunc("/apis/dashboard.grafana.app/v0alpha1/namespaces/default/dashboards/retry-uid", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(dashObj)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	k8s := &KubernetesClient{BaseURL: srv.URL, HTTPClient: srv.Client()}
	client := NewGrafanaAPIClient(nil, k8s)
	ctx := testContext()

	simulated406 := fmt.Errorf("simulated 406")
	legacyCalled := false
	result, err := client.getResource(ctx, getResourceOpts{
		apiGroup:  "dashboard.grafana.app",
		resource:  "dashboards",
		name:      "retry-uid",
		namespace: "default",
		legacyFetch: func(ctx context.Context) (interface{}, error) {
			legacyCalled = true
			return nil, simulated406
		},
		convert: convertK8sDashboard,
		check406: func(err error) bool {
			return err == simulated406
		},
	})

	assert.True(t, legacyCalled, "legacy should be called first since initial discovery returns no groups")
	require.NoError(t, err)
	require.NotNil(t, result)

	dashboard, ok := result.(*models.DashboardFullWithMeta)
	require.True(t, ok)

	spec, ok := dashboard.Dashboard.(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "Retry Dashboard", spec["title"])
}

func TestConvertK8sDashboard(t *testing.T) {
	obj := map[string]interface{}{
		"apiVersion": "dashboard.grafana.app/v0alpha1",
		"kind":       "Dashboard",
		"metadata": map[string]interface{}{
			"name":      "dash-uid-1",
			"namespace": "default",
			"annotations": map[string]interface{}{
				"grafana.app/folder": "my-folder",
			},
		},
		"spec": map[string]interface{}{
			"title":       "Test Dash",
			"description": "A test dashboard",
		},
	}

	result, err := convertK8sDashboard(obj)
	require.NoError(t, err)

	dashboard, ok := result.(*models.DashboardFullWithMeta)
	require.True(t, ok)

	spec, ok := dashboard.Dashboard.(map[string]interface{})
	require.True(t, ok)

	assert.Equal(t, "Test Dash", spec["title"])
	assert.Equal(t, "dash-uid-1", spec["uid"])
	assert.Equal(t, "A test dashboard", spec["description"])

	require.NotNil(t, dashboard.Meta)
	assert.Equal(t, "my-folder", dashboard.Meta.FolderUID)
	assert.Equal(t, "dash-uid-1", dashboard.Meta.Slug)
	assert.Equal(t, "db", dashboard.Meta.Type)
}

func TestConvertK8sDashboard_MissingSpec(t *testing.T) {
	obj := map[string]interface{}{
		"metadata": map[string]interface{}{
			"name": "no-spec",
		},
	}

	_, err := convertK8sDashboard(obj)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "spec")
}

func TestConvertK8sDashboard_NoAnnotations(t *testing.T) {
	obj := map[string]interface{}{
		"metadata": map[string]interface{}{
			"name": "no-annotations",
		},
		"spec": map[string]interface{}{
			"title": "No Folder",
		},
	}

	result, err := convertK8sDashboard(obj)
	require.NoError(t, err)

	dashboard, ok := result.(*models.DashboardFullWithMeta)
	require.True(t, ok)
	assert.Equal(t, "", dashboard.Meta.FolderUID)
	assert.Equal(t, "no-annotations", dashboard.Meta.Slug)
}

func TestContextHelpers(t *testing.T) {
	ctx := context.Background()

	// Should return nil when not set.
	assert.Nil(t, GrafanaAPIClientFromContext(ctx))

	// Set and retrieve.
	client := NewGrafanaAPIClient(nil, nil)
	ctx = WithGrafanaAPIClient(ctx, client)

	retrieved := GrafanaAPIClientFromContext(ctx)
	assert.Same(t, client, retrieved)
}

func TestShouldUseK8s_NoK8sClient(t *testing.T) {
	client := NewGrafanaAPIClient(nil, nil)
	ctx := testContext()
	assert.False(t, client.shouldUseK8s(ctx, "dashboard.grafana.app"))
}

func TestShouldUseK8s_GroupNotAvailable(t *testing.T) {
	groups := []APIGroup{
		{
			Name: "other.grafana.app",
			Versions: []GroupVersionInfo{
				{GroupVersion: "other.grafana.app/v1", Version: "v1"},
			},
			PreferredVersion: GroupVersionInfo{
				GroupVersion: "other.grafana.app/v1",
				Version:      "v1",
			},
		},
	}

	srv, _ := newTestK8sServer(t, groups, nil)

	k8s := &KubernetesClient{BaseURL: srv.URL, HTTPClient: srv.Client()}
	client := NewGrafanaAPIClient(nil, k8s)

	ctx := testContext()
	assert.False(t, client.shouldUseK8s(ctx, "dashboard.grafana.app"))
	assert.True(t, client.shouldUseK8s(ctx, "other.grafana.app"))
}
