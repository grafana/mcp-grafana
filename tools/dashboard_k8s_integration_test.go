//go:build integration

package tools

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"testing"

	mcpgrafana "github.com/grafana/mcp-grafana"
)

// newK8sTestContext creates a context pointing to the k8s-enabled Grafana instance.
// Uses GRAFANA_K8S_URL env var if set, otherwise defaults to http://localhost:3001.
func newK8sTestContext(t *testing.T) context.Context {
	t.Helper()

	grafanaURL := os.Getenv("GRAFANA_K8S_URL")
	if grafanaURL == "" {
		grafanaURL = "http://localhost:3001"
	}

	cfg := mcpgrafana.GrafanaConfig{
		Debug:     true,
		URL:       grafanaURL,
		BasicAuth: url.UserPassword("admin", "admin"),
	}
	ctx := mcpgrafana.WithGrafanaConfig(context.Background(), cfg)

	// Create and inject a KubernetesClient.
	k8sClient, err := mcpgrafana.NewKubernetesClient(ctx)
	if err != nil {
		t.Fatalf("failed to create k8s client: %v", err)
	}
	ctx = mcpgrafana.WithKubernetesClient(ctx, k8sClient)

	// Also create the legacy Grafana client for fallback tests.
	grafanaClient := mcpgrafana.NewGrafanaClient(ctx, grafanaURL, "", url.UserPassword("admin", "admin"))
	ctx = mcpgrafana.WithGrafanaClient(ctx, grafanaClient)

	return ctx
}

func TestK8sDiscoverCapabilities(t *testing.T) {
	ctx := newK8sTestContext(t)
	k8sClient := mcpgrafana.KubernetesClientFromContext(ctx)
	if k8sClient == nil {
		t.Fatal("expected k8s client in context")
	}

	caps, err := k8sClient.DiscoverCapabilities(ctx)
	if err != nil {
		t.Fatalf("DiscoverCapabilities failed: %v", err)
	}

	if !caps.HasKubernetesAPIs {
		t.Fatal("expected HasKubernetesAPIs to be true for k8s-enabled Grafana")
	}

	if caps.Registry == nil {
		t.Fatal("expected Registry to be non-nil")
	}

	if !caps.Registry.HasGroup("dashboard.grafana.app") {
		t.Fatalf("expected dashboard.grafana.app group; available groups: %v", caps.Registry.Groups())
	}

	version := caps.Registry.PreferredVersion("dashboard.grafana.app")
	t.Logf("dashboard.grafana.app preferred version: %s", version)
	if version == "" {
		t.Fatal("expected non-empty preferred version for dashboard.grafana.app")
	}
}

func TestK8sGetDashboardByUID_K8sAPI(t *testing.T) {
	ctx := newK8sTestContext(t)
	k8sClient := mcpgrafana.KubernetesClientFromContext(ctx)
	if k8sClient == nil {
		t.Fatal("expected k8s client in context")
	}

	// Discover capabilities to find the preferred version.
	caps, err := k8sClient.DiscoverCapabilities(ctx)
	if err != nil {
		t.Fatalf("DiscoverCapabilities failed: %v", err)
	}
	if !caps.ShouldUseKubernetesAPI("dashboard.grafana.app") {
		t.Skip("dashboard.grafana.app not available, skipping")
	}

	version := caps.Registry.PreferredVersion("dashboard.grafana.app")
	desc := mcpgrafana.ResourceDescriptor{
		Group:    "dashboard.grafana.app",
		Version:  version,
		Resource: "dashboards",
	}

	// Create a dashboard via the k8s API.
	uid := fmt.Sprintf("k8s-test-%d", os.Getpid())
	dashboardBody := fmt.Sprintf(`{
		"apiVersion": "dashboard.grafana.app/%s",
		"kind": "Dashboard",
		"metadata": {
			"name": %q,
			"namespace": "default"
		},
		"spec": {
			"title": "K8s Integration Test Dashboard",
			"uid": %q,
			"panels": []
		}
	}`, version, uid, uid)

	// Create the dashboard using a raw PUT request.
	path := desc.BasePath("default") + "/" + uid
	_, putErr := k8sClient.DoRawRequest(ctx, "PUT", path, []byte(dashboardBody))
	if putErr != nil {
		t.Fatalf("failed to create dashboard via k8s API: %v", putErr)
	}

	// Clean up after test.
	t.Cleanup(func() {
		_, _ = k8sClient.DoRawRequest(ctx, "DELETE", path, nil)
	})

	// Now fetch via getDashboardByUID which should route through k8s.
	result, err := getDashboardByUID(ctx, GetDashboardByUIDParams{UID: uid})
	if err != nil {
		t.Fatalf("getDashboardByUID failed: %v", err)
	}

	if result.Dashboard == nil {
		t.Fatal("expected dashboard to be non-nil")
	}

	// Verify the dashboard content.
	dashMap, ok := result.Dashboard.(map[string]interface{})
	if !ok {
		t.Fatalf("expected dashboard to be a map, got %T", result.Dashboard)
	}

	title, _ := dashMap["title"].(string)
	if title != "K8s Integration Test Dashboard" {
		t.Fatalf("expected title 'K8s Integration Test Dashboard', got %q", title)
	}
}

func TestK8sGetDashboardByUID_LegacyPathWithoutK8sClient(t *testing.T) {
	// This test verifies that getDashboardByUID falls back to the legacy API
	// when no KubernetesClient is present in the context. It does NOT test
	// the 406 → k8s retry path (that would require a mock server returning 406
	// from the legacy endpoint).
	ctx := newTestContext()

	// Verify this is a non-k8s Grafana by checking that no k8s client is in context
	// (the standard newTestContext doesn't inject one).
	k8sClient := mcpgrafana.KubernetesClientFromContext(ctx)
	if k8sClient != nil {
		t.Skip("k8s client unexpectedly present in legacy test context")
	}

	// The provisioned test dashboard should be accessible via legacy API.
	// Look for any provisioned dashboard.
	c := mcpgrafana.GrafanaClientFromContext(ctx)
	if c == nil {
		t.Fatal("expected grafana client in context")
	}

	searchResult, err := c.Search.Search(nil)
	if err != nil {
		t.Fatalf("search failed: %v", err)
	}

	if len(searchResult.Payload) == 0 {
		t.Skip("no dashboards found in legacy Grafana, skipping")
	}

	uid := searchResult.Payload[0].UID
	result, err := getDashboardByUID(ctx, GetDashboardByUIDParams{UID: uid})
	if err != nil {
		t.Fatalf("getDashboardByUID failed for legacy path: %v", err)
	}

	if result.Dashboard == nil {
		t.Fatal("expected dashboard to be non-nil from legacy path")
	}
}
