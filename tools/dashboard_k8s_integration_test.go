//go:build integration

package tools

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"testing"

	"github.com/go-openapi/strfmt"
	"github.com/grafana/grafana-openapi-client-go/client"
	mcpgrafana "github.com/grafana/mcp-grafana"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// k8sDashboardAPIVersion is the version used to create test dashboards.
// We use v2beta1 because Grafana's legacy /api/dashboards/uid/ endpoint
// returns 406 for dashboards whose APIVersion starts with "v2".
const k8sDashboardAPIVersion = "v2beta1"

func k8sGrafanaURL() string {
	if u, ok := os.LookupEnv("GRAFANA_K8S_URL"); ok {
		return u
	}
	return "http://localhost:3001"
}

// newK8sTestContext creates a test context pointing at the kubernetes-only
// Grafana instance (grafana-k8s on port 3001) with basic auth admin/admin.
func newK8sTestContext(t *testing.T) context.Context {
	t.Helper()

	grafanaURL := k8sGrafanaURL()

	parsedURL, err := url.Parse(grafanaURL)
	require.NoError(t, err)

	cfg := client.DefaultTransportConfig()
	cfg.Host = parsedURL.Host
	cfg.Schemes = []string{"http"}
	cfg.BasicAuth = url.UserPassword("admin", "admin")

	legacyClient := client.NewHTTPClientWithConfig(strfmt.Default, cfg)

	grafanaCfg := mcpgrafana.GrafanaConfig{
		Debug:     true,
		URL:       grafanaURL,
		BasicAuth: cfg.BasicAuth,
	}

	ctx := mcpgrafana.WithGrafanaConfig(context.Background(), grafanaCfg)
	ctx = mcpgrafana.WithGrafanaClient(ctx, legacyClient)

	httpClient := mcpgrafana.NewHTTPClient(ctx, grafanaCfg)
	instance := mcpgrafana.NewGrafanaInstance(grafanaCfg, legacyClient, httpClient)
	ctx = mcpgrafana.WithGrafanaInstance(ctx, instance)

	return ctx
}

// createK8sDashboard creates a dashboard via the v2beta1 kubernetes API on the
// k8s Grafana instance and registers a t.Cleanup to delete it afterwards.
// Dashboards created via v2beta1 will return HTTP 406 on the legacy endpoint,
// which is exactly the scenario we need to test.
func createK8sDashboard(t *testing.T, ctx context.Context, title string) string {
	t.Helper()

	randBytes := make([]byte, 6)
	_, err := rand.Read(randBytes)
	require.NoError(t, err)
	uid := "k8stest-" + hex.EncodeToString(randBytes)

	// Use the v2beta1 spec format — it uses layout/elements, not panels.
	dashboard := map[string]interface{}{
		"apiVersion": "dashboard.grafana.app/" + k8sDashboardAPIVersion,
		"kind":       "Dashboard",
		"metadata": map[string]interface{}{
			"name":      uid,
			"namespace": "default",
		},
		"spec": map[string]interface{}{
			"title": title,
		},
	}

	body, err := json.Marshal(dashboard)
	require.NoError(t, err)

	grafanaURL := k8sGrafanaURL()
	createURL := fmt.Sprintf("%s/apis/dashboard.grafana.app/%s/namespaces/default/dashboards",
		grafanaURL, k8sDashboardAPIVersion)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, createURL, bytes.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.SetBasicAuth("admin", "admin")

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	respBody, _ := io.ReadAll(resp.Body)
	require.Truef(t, resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusCreated,
		"create dashboard failed: status %d, body: %s", resp.StatusCode, string(respBody))

	t.Cleanup(func() {
		deleteURL := fmt.Sprintf("%s/apis/dashboard.grafana.app/%s/namespaces/default/dashboards/%s",
			grafanaURL, k8sDashboardAPIVersion, uid)
		delReq, err := http.NewRequestWithContext(context.Background(), http.MethodDelete, deleteURL, nil)
		if err != nil {
			return
		}
		delReq.SetBasicAuth("admin", "admin")
		delResp, err := http.DefaultClient.Do(delReq)
		if err != nil {
			return
		}
		_ = delResp.Body.Close()
	})

	return uid
}

// TestK8sGrafana_LegacyEndpointReturns406 validates that the kubernetes-only
// Grafana actually returns HTTP 406 on the legacy dashboard endpoint for
// dashboards created via the v2beta1 API.
func TestK8sGrafana_LegacyEndpointReturns406(t *testing.T) {
	mcpgrafana.ResetGlobalCapabilityCache()

	ctx := newK8sTestContext(t)
	uid := createK8sDashboard(t, ctx, "406 Test Dashboard")

	legacyURL := fmt.Sprintf("%s/api/dashboards/uid/%s", k8sGrafanaURL(), uid)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, legacyURL, nil)
	require.NoError(t, err)
	req.SetBasicAuth("admin", "admin")

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	assert.Equal(t, http.StatusNotAcceptable, resp.StatusCode,
		"legacy endpoint should return 406 for v2 dashboards")

	var body map[string]interface{}
	err = json.NewDecoder(resp.Body).Decode(&body)
	require.NoError(t, err)

	msg, ok := body["message"].(string)
	require.True(t, ok, "response should contain a 'message' field")
	assert.Contains(t, msg, "dashboard api version not supported")
	assert.Contains(t, msg, "/apis/dashboard.grafana.app/")
}

// TestK8sGrafana_FallbackLifecycle verifies that getDashboardByUID transparently
// falls back from the legacy 406 to the kubernetes API and caches the result.
func TestK8sGrafana_FallbackLifecycle(t *testing.T) {
	mcpgrafana.ResetGlobalCapabilityCache()

	ctx := newK8sTestContext(t)
	uid := createK8sDashboard(t, ctx, "Fallback Lifecycle Dashboard")

	// Reset cache after dashboard creation, then re-discover API groups
	// without pre-setting any per-API capability.
	mcpgrafana.ResetGlobalCapabilityCache()
	instance := mcpgrafana.GrafanaInstanceFromContext(ctx)
	require.NotNil(t, instance)
	err := instance.DiscoverCapabilities(ctx)
	require.NoError(t, err)

	// Verify capability is unknown before the call
	assert.False(t, instance.ShouldUseKubernetesAPI(mcpgrafana.APIGroupDashboard),
		"dashboard API should NOT be marked as kubernetes before first call")

	// Call getDashboardByUID — should hit legacy, get 406, fall back to k8s
	result, err := getDashboardByUID(ctx, GetDashboardByUIDParams{UID: uid})
	require.NoError(t, err, "getDashboardByUID should succeed via fallback")
	require.NotNil(t, result)

	dashboardMap, ok := result.Dashboard.(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, uid, dashboardMap["uid"])
	assert.Equal(t, "Fallback Lifecycle Dashboard", dashboardMap["title"])

	// After the call, the capability should be cached as kubernetes
	assert.True(t, instance.ShouldUseKubernetesAPI(mcpgrafana.APIGroupDashboard),
		"dashboard API should be marked as kubernetes after 406 detection")
}

// TestK8sGrafana_CachedCapabilitySkipsLegacy verifies that once the kubernetes
// capability is cached, subsequent requests go directly to the kubernetes API.
func TestK8sGrafana_CachedCapabilitySkipsLegacy(t *testing.T) {
	mcpgrafana.ResetGlobalCapabilityCache()

	ctx := newK8sTestContext(t)
	uid1 := createK8sDashboard(t, ctx, "Cached Cap Dashboard 1")
	uid2 := createK8sDashboard(t, ctx, "Cached Cap Dashboard 2")

	// Reset and re-discover
	mcpgrafana.ResetGlobalCapabilityCache()
	instance := mcpgrafana.GrafanaInstanceFromContext(ctx)
	require.NotNil(t, instance)
	err := instance.DiscoverCapabilities(ctx)
	require.NoError(t, err)

	// First call: triggers 406 fallback, caches kubernetes capability
	result1, err := getDashboardByUID(ctx, GetDashboardByUIDParams{UID: uid1})
	require.NoError(t, err)
	require.NotNil(t, result1)

	assert.True(t, instance.ShouldUseKubernetesAPI(mcpgrafana.APIGroupDashboard),
		"dashboard API should be kubernetes after first call")

	// Second call: should go directly to kubernetes (no 406 round-trip)
	result2, err := getDashboardByUID(ctx, GetDashboardByUIDParams{UID: uid2})
	require.NoError(t, err)
	require.NotNil(t, result2)

	dashboardMap, ok := result2.Dashboard.(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, uid2, dashboardMap["uid"])
	assert.Equal(t, "Cached Cap Dashboard 2", dashboardMap["title"])

	// Capability should still be kubernetes
	assert.True(t, instance.ShouldUseKubernetesAPI(mcpgrafana.APIGroupDashboard),
		"dashboard API should still be kubernetes after second call")
}

// TestK8sGrafana_IndependentAPIGroups verifies that detecting kubernetes
// capability for dashboards does NOT affect other API groups like folders.
func TestK8sGrafana_IndependentAPIGroups(t *testing.T) {
	mcpgrafana.ResetGlobalCapabilityCache()

	ctx := newK8sTestContext(t)
	uid := createK8sDashboard(t, ctx, "Independent Groups Dashboard")

	// Reset and re-discover
	mcpgrafana.ResetGlobalCapabilityCache()
	instance := mcpgrafana.GrafanaInstanceFromContext(ctx)
	require.NotNil(t, instance)
	err := instance.DiscoverCapabilities(ctx)
	require.NoError(t, err)

	// Trigger 406 detection for dashboards
	_, err = getDashboardByUID(ctx, GetDashboardByUIDParams{UID: uid})
	require.NoError(t, err)

	// Dashboard should be kubernetes
	assert.True(t, instance.ShouldUseKubernetesAPI(mcpgrafana.APIGroupDashboard),
		"dashboard API should be kubernetes after 406")

	// Folder should NOT be affected
	assert.False(t, instance.ShouldUseKubernetesAPI(mcpgrafana.APIGroupFolder),
		"folder API should remain independent from dashboard API")
}
