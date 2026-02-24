//go:build unit

package mcpgrafana

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAPICapabilityConstants(t *testing.T) {
	assert.Equal(t, APICapability(""), APICapabilityUnknown)
	assert.Equal(t, APICapability("legacy"), APICapabilityLegacy)
	assert.Equal(t, APICapability("kubernetes"), APICapabilityKubernetes)
}

func TestAPIGroupConstants(t *testing.T) {
	assert.Equal(t, "dashboard.grafana.app", APIGroupDashboard)
	assert.Equal(t, "folder.grafana.app", APIGroupFolder)
	assert.Equal(t, "iam.grafana.app", APIGroupIAM)
	assert.Equal(t, "userstorage.grafana.app", APIGroupUserStorage)
	assert.Equal(t, "features.grafana.app", APIGroupFeatures)
}

func TestCapabilityCacheBasics(t *testing.T) {
	cache := NewCapabilityCache(1 * time.Minute)
	require.NotNil(t, cache)

	// Test Get on empty cache
	entry := cache.Get("http://localhost:3000")
	assert.Nil(t, entry)

	// Test Set and Get
	testEntry := &capabilityCacheEntry{
		hasKubernetesAPIs: true,
		apiGroups: map[string]*APIGroupInfo{
			APIGroupDashboard: {
				Available:        true,
				PreferredVersion: "v1beta1",
				AllVersions:      []string{"v1beta1", "v2alpha1", "v2beta1"},
			},
		},
		perAPICapability: make(map[string]APICapability),
		detectedAt:       time.Now(),
	}
	cache.Set("http://localhost:3000", testEntry)

	retrieved := cache.Get("http://localhost:3000")
	require.NotNil(t, retrieved)
	assert.True(t, retrieved.hasKubernetesAPIs)
	assert.Contains(t, retrieved.apiGroups, APIGroupDashboard)
	assert.Equal(t, "v1beta1", retrieved.apiGroups[APIGroupDashboard].PreferredVersion)
}

func TestCapabilityCacheExpiration(t *testing.T) {
	// Create cache with very short TTL
	cache := NewCapabilityCache(10 * time.Millisecond)

	testEntry := &capabilityCacheEntry{
		hasKubernetesAPIs: true,
		detectedAt:        time.Now(),
	}
	cache.Set("http://localhost:3000", testEntry)

	// Should be available immediately
	retrieved := cache.Get("http://localhost:3000")
	require.NotNil(t, retrieved)

	// Wait for expiration
	time.Sleep(20 * time.Millisecond)

	// Should be nil after expiration
	retrieved = cache.Get("http://localhost:3000")
	assert.Nil(t, retrieved)
}

func TestCapabilityCacheAPICapability(t *testing.T) {
	cache := NewCapabilityCache(1 * time.Minute)

	// Test GetAPICapability on empty cache
	cap := cache.GetAPICapability("http://localhost:3000", APIGroupDashboard)
	assert.Equal(t, APICapabilityUnknown, cap)

	// Test SetAPICapability creates entry if needed
	cache.SetAPICapability("http://localhost:3000", APIGroupDashboard, APICapabilityKubernetes)

	cap = cache.GetAPICapability("http://localhost:3000", APIGroupDashboard)
	assert.Equal(t, APICapabilityKubernetes, cap)

	// Test SetAPICapability on existing entry
	testEntry := &capabilityCacheEntry{
		hasKubernetesAPIs: true,
		apiGroups:         make(map[string]*APIGroupInfo),
		perAPICapability:  make(map[string]APICapability),
		detectedAt:        time.Now(),
	}
	cache.Set("http://other:3000", testEntry)
	cache.SetAPICapability("http://other:3000", APIGroupFolder, APICapabilityLegacy)

	cap = cache.GetAPICapability("http://other:3000", APIGroupFolder)
	assert.Equal(t, APICapabilityLegacy, cap)
}

func TestCapabilityCacheClear(t *testing.T) {
	cache := NewCapabilityCache(1 * time.Minute)

	cache.Set("http://localhost:3000", &capabilityCacheEntry{
		hasKubernetesAPIs: true,
		detectedAt:        time.Now(),
	})
	cache.Set("http://other:3000", &capabilityCacheEntry{
		hasKubernetesAPIs: false,
		detectedAt:        time.Now(),
	})

	// Both should exist
	assert.NotNil(t, cache.Get("http://localhost:3000"))
	assert.NotNil(t, cache.Get("http://other:3000"))

	// Clear all
	cache.Clear()

	// Both should be gone
	assert.Nil(t, cache.Get("http://localhost:3000"))
	assert.Nil(t, cache.Get("http://other:3000"))
}

func TestCapabilityCacheInvalidate(t *testing.T) {
	cache := NewCapabilityCache(1 * time.Minute)

	cache.Set("http://localhost:3000", &capabilityCacheEntry{
		hasKubernetesAPIs: true,
		detectedAt:        time.Now(),
	})
	cache.Set("http://other:3000", &capabilityCacheEntry{
		hasKubernetesAPIs: false,
		detectedAt:        time.Now(),
	})

	// Invalidate only one
	cache.Invalidate("http://localhost:3000")

	assert.Nil(t, cache.Get("http://localhost:3000"))
	assert.NotNil(t, cache.Get("http://other:3000"))
}

func TestDiscoverAPIs_Success(t *testing.T) {
	// Create test server that returns a valid APIGroupList
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/apis", r.URL.Path)
		assert.Equal(t, "application/json", r.Header.Get("Accept"))

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
				{
					Name: "folder.grafana.app",
					Versions: []GroupVersionInfo{
						{GroupVersion: "folder.grafana.app/v1beta1", Version: "v1beta1"},
					},
					PreferredVersion: GroupVersionInfo{
						GroupVersion: "folder.grafana.app/v1beta1",
						Version:      "v1beta1",
					},
				},
			},
		}

		w.Header().Set("Content-Type", "application/json")
		err := json.NewEncoder(w).Encode(response)
		require.NoError(t, err)
	}))
	defer server.Close()

	ctx := context.Background()
	entry, err := DiscoverAPIs(ctx, server.Client(), server.URL)

	require.NoError(t, err)
	require.NotNil(t, entry)
	assert.True(t, entry.hasKubernetesAPIs)
	assert.Len(t, entry.apiGroups, 2)

	// Check dashboard API group
	dashboardInfo := entry.apiGroups[APIGroupDashboard]
	require.NotNil(t, dashboardInfo)
	assert.True(t, dashboardInfo.Available)
	assert.Equal(t, "v1beta1", dashboardInfo.PreferredVersion)
	assert.Contains(t, dashboardInfo.AllVersions, "v1beta1")
	assert.Contains(t, dashboardInfo.AllVersions, "v2beta1")

	// Check folder API group
	folderInfo := entry.apiGroups[APIGroupFolder]
	require.NotNil(t, folderInfo)
	assert.True(t, folderInfo.Available)
	assert.Equal(t, "v1beta1", folderInfo.PreferredVersion)
}

func TestDiscoverAPIs_NotFound(t *testing.T) {
	// Create test server that returns 404
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	ctx := context.Background()
	entry, err := DiscoverAPIs(ctx, server.Client(), server.URL)

	require.NoError(t, err)
	require.NotNil(t, entry)
	assert.False(t, entry.hasKubernetesAPIs)
	assert.Empty(t, entry.apiGroups)
}

func TestDiscoverAPIs_Error(t *testing.T) {
	// Create test server that returns an error status
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("internal error"))
	}))
	defer server.Close()

	ctx := context.Background()
	entry, err := DiscoverAPIs(ctx, server.Client(), server.URL)

	require.Error(t, err)
	assert.Nil(t, entry)
	assert.Contains(t, err.Error(), "unexpected status from /apis: 500")
}

func TestDiscoverAPIs_InvalidJSON(t *testing.T) {
	// Create test server that returns invalid JSON
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte("not valid json"))
	}))
	defer server.Close()

	ctx := context.Background()
	entry, err := DiscoverAPIs(ctx, server.Client(), server.URL)

	require.Error(t, err)
	assert.Nil(t, entry)
	assert.Contains(t, err.Error(), "decode /apis response")
}

func TestParseKubernetesAPIPath(t *testing.T) {
	tests := []struct {
		name          string
		path          string
		wantGroup     string
		wantVersion   string
		wantNamespace string
		wantResource  string
		wantOK        bool
	}{
		{
			name:          "valid dashboard path",
			path:          "/apis/dashboard.grafana.app/v2beta1/namespaces/default/dashboards/abc123",
			wantGroup:     "dashboard.grafana.app",
			wantVersion:   "v2beta1",
			wantNamespace: "default",
			wantResource:  "dashboards",
			wantOK:        true,
		},
		{
			name:          "valid folder path",
			path:          "/apis/folder.grafana.app/v1beta1/namespaces/my-org/folders/folder-uid",
			wantGroup:     "folder.grafana.app",
			wantVersion:   "v1beta1",
			wantNamespace: "my-org",
			wantResource:  "folders",
			wantOK:        true,
		},
		{
			name:          "alpha version",
			path:          "/apis/iam.grafana.app/v0alpha1/namespaces/default/users/user-id",
			wantGroup:     "iam.grafana.app",
			wantVersion:   "v0alpha1",
			wantNamespace: "default",
			wantResource:  "users",
			wantOK:        true,
		},
		{
			name:          "path in error message",
			path:          "dashboard api version not supported, use /apis/dashboard.grafana.app/v2beta1/namespaces/default/dashboards/ad8nwk6 instead",
			wantGroup:     "dashboard.grafana.app",
			wantVersion:   "v2beta1",
			wantNamespace: "default",
			wantResource:  "dashboards",
			wantOK:        true,
		},
		{
			name:   "invalid path - legacy API",
			path:   "/api/dashboards/uid/abc123",
			wantOK: false,
		},
		{
			name:   "invalid path - no version",
			path:   "/apis/dashboard.grafana.app/namespaces/default/dashboards/abc123",
			wantOK: false,
		},
		{
			name:   "empty string",
			path:   "",
			wantOK: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			group, version, namespace, resource, ok := ParseKubernetesAPIPath(tt.path)
			assert.Equal(t, tt.wantOK, ok)
			if tt.wantOK {
				assert.Equal(t, tt.wantGroup, group)
				assert.Equal(t, tt.wantVersion, version)
				assert.Equal(t, tt.wantNamespace, namespace)
				assert.Equal(t, tt.wantResource, resource)
			}
		})
	}
}

func TestParse406Error(t *testing.T) {
	tests := []struct {
		name        string
		errMsg      string
		wantGroup   string
		wantVersion string
		wantOK      bool
	}{
		{
			name:        "standard 406 error",
			errMsg:      "dashboard api version not supported, use /apis/dashboard.grafana.app/v2beta1/namespaces/default/dashboards/ad8nwk6 instead",
			wantGroup:   "dashboard.grafana.app",
			wantVersion: "v2beta1",
			wantOK:      true,
		},
		{
			name:        "v1beta1 version",
			errMsg:      "use /apis/dashboard.grafana.app/v1beta1/namespaces/default/dashboards/xyz instead",
			wantGroup:   "dashboard.grafana.app",
			wantVersion: "v1beta1",
			wantOK:      true,
		},
		{
			name:   "non-406 error",
			errMsg: "dashboard not found",
			wantOK: false,
		},
		{
			name:   "empty error",
			errMsg: "",
			wantOK: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			group, version, ok := Parse406Error(tt.errMsg)
			assert.Equal(t, tt.wantOK, ok)
			if tt.wantOK {
				assert.Equal(t, tt.wantGroup, group)
				assert.Equal(t, tt.wantVersion, version)
			}
		})
	}
}

func TestGlobalCapabilityCache(t *testing.T) {
	// Get the global cache
	cache := GetGlobalCapabilityCache()
	require.NotNil(t, cache)

	// Reset it for testing
	ResetGlobalCapabilityCache()

	// Verify it's empty
	assert.Nil(t, cache.Get("http://localhost:3000"))

	// Add something
	cache.Set("http://localhost:3000", &capabilityCacheEntry{
		hasKubernetesAPIs: true,
		detectedAt:        time.Now(),
	})

	// Verify it's there
	assert.NotNil(t, cache.Get("http://localhost:3000"))

	// Reset again
	ResetGlobalCapabilityCache()

	// Verify it's gone
	assert.Nil(t, cache.Get("http://localhost:3000"))
}

func TestCapabilityCacheConcurrency(t *testing.T) {
	cache := NewCapabilityCache(1 * time.Minute)

	// Run concurrent operations
	done := make(chan bool)

	// Writer goroutine
	go func() {
		for i := 0; i < 100; i++ {
			cache.Set("http://localhost:3000", &capabilityCacheEntry{
				hasKubernetesAPIs: true,
				detectedAt:        time.Now(),
			})
			cache.SetAPICapability("http://localhost:3000", APIGroupDashboard, APICapabilityKubernetes)
		}
		done <- true
	}()

	// Reader goroutine
	go func() {
		for i := 0; i < 100; i++ {
			cache.Get("http://localhost:3000")
			cache.GetAPICapability("http://localhost:3000", APIGroupDashboard)
		}
		done <- true
	}()

	// Invalidator goroutine
	go func() {
		for i := 0; i < 100; i++ {
			cache.Invalidate("http://localhost:3000")
		}
		done <- true
	}()

	// Wait for all goroutines
	<-done
	<-done
	<-done
}
