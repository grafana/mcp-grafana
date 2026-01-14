//go:build integration

package mcpgrafana

import (
	"context"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// getTestConfig returns a GrafanaConfig for integration tests.
// It checks for API key first, then falls back to basic auth.
func getTestConfig() GrafanaConfig {
	grafanaURL := os.Getenv("GRAFANA_URL")
	if grafanaURL == "" {
		grafanaURL = "http://localhost:3000"
	}

	config := GrafanaConfig{
		URL:     grafanaURL,
		APIKey:  os.Getenv("GRAFANA_SERVICE_ACCOUNT_TOKEN"),
		Timeout: 30 * time.Second,
	}

	// Fall back to basic auth if no API key
	if config.APIKey == "" {
		username := os.Getenv("GRAFANA_USERNAME")
		password := os.Getenv("GRAFANA_PASSWORD")
		if username == "" {
			username = "admin"
		}
		if password == "" {
			password = "admin"
		}
		config.BasicAuth = url.UserPassword(username, password)
	}

	return config
}

func TestCapabilityDetection_Integration(t *testing.T) {
	// Reset global cache before test
	ResetGlobalCapabilityCache()

	config := getTestConfig()

	// Create HTTP client
	ctx := WithGrafanaConfig(context.Background(), config)
	httpClient := NewHTTPClient(ctx, config)

	instance := NewGrafanaInstance(config, nil, httpClient)

	t.Run("DiscoverCapabilities", func(t *testing.T) {
		err := instance.DiscoverCapabilities(ctx)
		require.NoError(t, err)

		// Check if kubernetes APIs are available
		hasK8s, err := instance.HasKubernetesAPIs(ctx)
		require.NoError(t, err)

		t.Logf("Grafana at %s has kubernetes APIs: %v", config.URL, hasK8s)
	})

	t.Run("GetAPIGroupInfo_Dashboard", func(t *testing.T) {
		info, err := instance.GetAPIGroupInfo(ctx, APIGroupDashboard)
		require.NoError(t, err)

		if info != nil {
			t.Logf("Dashboard API available: %v", info.Available)
			t.Logf("Dashboard API preferred version: %s", info.PreferredVersion)
			t.Logf("Dashboard API all versions: %v", info.AllVersions)

			assert.True(t, info.Available)
			assert.NotEmpty(t, info.PreferredVersion)
			assert.NotEmpty(t, info.AllVersions)
		} else {
			t.Log("Dashboard kubernetes API not available (legacy Grafana)")
		}
	})

	t.Run("GetAPIGroupInfo_Folder", func(t *testing.T) {
		info, err := instance.GetAPIGroupInfo(ctx, APIGroupFolder)
		require.NoError(t, err)

		if info != nil {
			t.Logf("Folder API available: %v", info.Available)
			t.Logf("Folder API preferred version: %s", info.PreferredVersion)
		} else {
			t.Log("Folder kubernetes API not available")
		}
	})
}

func TestDiscoverAPIs_Integration(t *testing.T) {
	config := getTestConfig()

	ctx := WithGrafanaConfig(context.Background(), config)
	httpClient := NewHTTPClient(ctx, config)

	// Create instance to get proper auth headers in requests
	instance := NewGrafanaInstance(config, nil, httpClient)
	err := instance.DiscoverCapabilities(ctx)
	require.NoError(t, err)

	entry := instance.cache.Get(config.URL)
	require.NoError(t, err)
	require.NotNil(t, entry)

	t.Logf("Has kubernetes APIs: %v", entry.hasKubernetesAPIs)

	if entry.hasKubernetesAPIs {
		t.Logf("Discovered %d API groups:", len(entry.apiGroups))
		for name, info := range entry.apiGroups {
			t.Logf("  - %s: preferred=%s, versions=%v", name, info.PreferredVersion, info.AllVersions)
		}
	}
}

func TestGetDashboardKubernetes_Integration(t *testing.T) {
	// Reset global cache before test
	ResetGlobalCapabilityCache()

	config := getTestConfig()

	ctx := WithGrafanaConfig(context.Background(), config)
	httpClient := NewHTTPClient(ctx, config)

	instance := NewGrafanaInstance(config, nil, httpClient)

	// First, check if kubernetes APIs are available
	hasK8s, err := instance.HasKubernetesAPIs(ctx)
	require.NoError(t, err)

	if !hasK8s {
		t.Skip("Kubernetes APIs not available on this Grafana instance")
	}

	// Get the preferred version
	version, err := instance.GetPreferredVersion(ctx, APIGroupDashboard)
	require.NoError(t, err)
	t.Logf("Using dashboard API version: %s", version)

	// Try to get a non-existent dashboard (should return 404, not crash)
	dashboard, err := instance.GetDashboardKubernetes(ctx, "nonexistent-dashboard-uid", version, "default")
	assert.Error(t, err)
	assert.Nil(t, dashboard)
	t.Logf("Expected error for non-existent dashboard: %v", err)
}
