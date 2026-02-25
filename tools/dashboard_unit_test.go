//go:build unit

package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"testing/quick"

	"github.com/grafana/grafana-openapi-client-go/client"
	mcpgrafana "github.com/grafana/mcp-grafana"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Test for trailing whitespace in paths (bug fix)
func TestApplyJSONPath_TrailingWhitespace(t *testing.T) {
	t.Run("append with trailing space works", func(t *testing.T) {
		data := map[string]interface{}{
			"panels": []interface{}{"a", "b"},
		}
		// Path with trailing space - should be trimmed
		err := applyJSONPath(data, "$.panels/- ", "c", false)
		require.NoError(t, err)
		assert.Equal(t, []interface{}{"a", "b", "c"}, data["panels"])
	})

	t.Run("path with leading and trailing whitespace", func(t *testing.T) {
		data := map[string]interface{}{
			"title": "old",
		}
		err := applyJSONPath(data, "  $.title  ", "new", false)
		require.NoError(t, err)
		assert.Equal(t, "new", data["title"])
	})
}

// Feature: dashboard-remove-array-element, Property 1: Array removal produces correct result
// Validates: Requirements 1.1, 1.2, 1.3
func TestProperty_ArrayRemovalProducesCorrectResult(t *testing.T) {
	f := func(size uint8) bool {
		// Ensure non-empty array (size 1-256)
		n := int(size) + 1

		// Build array of distinguishable elements
		arr := make([]interface{}, n)
		for j := 0; j < n; j++ {
			arr[j] = j
		}

		// Pick a random valid index
		idx := rand.Intn(n)

		// Build expected result
		expected := make([]interface{}, 0, n-1)
		expected = append(expected, arr[:idx]...)
		expected = append(expected, arr[idx+1:]...)

		// Build the map and segment
		current := map[string]interface{}{"items": copySlice(arr)}
		segment := JSONPathSegment{Key: "items", IsArray: true, Index: idx}

		// Execute
		err := removeAtSegment(current, segment)
		if err != nil {
			return false
		}

		// Verify
		result := current["items"].([]interface{})
		if len(result) != n-1 {
			return false
		}
		for k := range result {
			if result[k] != expected[k] {
				return false
			}
		}
		return true
	}

	require.NoError(t, quick.Check(f, &quick.Config{MaxCount: 200}))
}

func copySlice(s []interface{}) []interface{} {
	c := make([]interface{}, len(s))
	copy(c, s)
	return c
}

// Feature: dashboard-remove-array-element, Property 2: Out-of-bounds index returns error
// Validates: Requirements 1.4, 1.5
func TestProperty_OutOfBoundsIndexReturnsError(t *testing.T) {
	f := func(size uint8, offset uint8) bool {
		n := int(size) // array length 0-255

		// Build array
		arr := make([]interface{}, n)
		for j := 0; j < n; j++ {
			arr[j] = j
		}

		// Out-of-bounds index: n + offset (always >= n)
		idx := n + int(offset)

		// Build the map and segment
		original := copySlice(arr)
		current := map[string]interface{}{"items": copySlice(arr)}
		segment := JSONPathSegment{Key: "items", IsArray: true, Index: idx}

		// Execute
		err := removeAtSegment(current, segment)

		// Must return error
		if err == nil {
			return false
		}

		// Array must be unchanged
		result := current["items"].([]interface{})
		if len(result) != len(original) {
			return false
		}
		for k := range result {
			if result[k] != original[k] {
				return false
			}
		}
		return true
	}

	require.NoError(t, quick.Check(f, &quick.Config{MaxCount: 200}))
}

// Feature: dashboard-remove-array-element, Property 4: Object property removal is preserved
// Validates: Requirements 3.1
func TestProperty_ObjectPropertyRemovalPreserved(t *testing.T) {
	f := func(numKeys uint8) bool {
		// Build a map with 1 to 256 keys
		n := int(numKeys) + 1
		current := make(map[string]interface{})
		for j := 0; j < n; j++ {
			current[fmt.Sprintf("key_%d", j)] = j
		}

		// Pick a random key to remove
		targetIdx := rand.Intn(n)
		targetKey := fmt.Sprintf("key_%d", targetIdx)

		// Snapshot other keys
		otherKeys := make(map[string]interface{})
		for k, v := range current {
			if k != targetKey {
				otherKeys[k] = v
			}
		}

		// Execute removal (non-array segment)
		segment := JSONPathSegment{Key: targetKey, IsArray: false}
		err := removeAtSegment(current, segment)
		if err != nil {
			return false
		}

		// Target key must be absent
		if _, exists := current[targetKey]; exists {
			return false
		}

		// All other keys must be unchanged
		if len(current) != len(otherKeys) {
			return false
		}
		for k, v := range otherKeys {
			if current[k] != v {
				return false
			}
		}
		return true
	}

	require.NoError(t, quick.Check(f, &quick.Config{MaxCount: 200}))
}

// Unit tests for removeAtSegment edge cases
// Validates: Requirements 1.1, 1.4, 3.2
func TestRemoveAtSegment_EdgeCases(t *testing.T) {
	t.Run("remove first element", func(t *testing.T) {
		current := map[string]interface{}{"items": []interface{}{"a", "b", "c"}}
		segment := JSONPathSegment{Key: "items", IsArray: true, Index: 0}
		err := removeAtSegment(current, segment)
		require.NoError(t, err)
		assert.Equal(t, []interface{}{"b", "c"}, current["items"])
	})

	t.Run("remove middle element", func(t *testing.T) {
		current := map[string]interface{}{"items": []interface{}{"a", "b", "c"}}
		segment := JSONPathSegment{Key: "items", IsArray: true, Index: 1}
		err := removeAtSegment(current, segment)
		require.NoError(t, err)
		assert.Equal(t, []interface{}{"a", "c"}, current["items"])
	})

	t.Run("remove last element", func(t *testing.T) {
		current := map[string]interface{}{"items": []interface{}{"a", "b", "c"}}
		segment := JSONPathSegment{Key: "items", IsArray: true, Index: 2}
		err := removeAtSegment(current, segment)
		require.NoError(t, err)
		assert.Equal(t, []interface{}{"a", "b"}, current["items"])
	})

	t.Run("remove from single-element array", func(t *testing.T) {
		current := map[string]interface{}{"items": []interface{}{"only"}}
		segment := JSONPathSegment{Key: "items", IsArray: true, Index: 0}
		err := removeAtSegment(current, segment)
		require.NoError(t, err)
		assert.Equal(t, []interface{}{}, current["items"])
	})

	t.Run("remove with append syntax returns error", func(t *testing.T) {
		current := map[string]interface{}{"items": []interface{}{"a", "b"}}
		segment := JSONPathSegment{Key: "items", IsAppend: true}
		err := removeAtSegment(current, segment)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "append syntax")
	})

	t.Run("remove from non-array field returns error", func(t *testing.T) {
		current := map[string]interface{}{"title": "hello"}
		segment := JSONPathSegment{Key: "title", IsArray: true, Index: 0}
		err := removeAtSegment(current, segment)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not an array")
	})
}

// Feature: dashboard-remove-array-element, Property 3: Sequential removal shifts indices correctly
// Validates: Requirements 2.2
func TestProperty_SequentialRemovalShiftsIndices(t *testing.T) {
	f := func(size uint8) bool {
		// Ensure array of length >= 3 (need at least 3 elements for two sequential removals)
		n := int(size) + 3

		// Build array of distinguishable elements
		arr := make([]interface{}, n)
		for j := 0; j < n; j++ {
			arr[j] = j
		}

		// Build the map
		current := map[string]interface{}{"items": copySlice(arr)}

		// Remove index 0 (removes original element 0)
		segment := JSONPathSegment{Key: "items", IsArray: true, Index: 0}
		err := removeAtSegment(current, segment)
		if err != nil {
			return false
		}

		// After removal, index 0 should be what was originally at index 1
		result := current["items"].([]interface{})
		if result[0] != arr[1] {
			return false
		}

		// Remove index 0 again (removes what was originally element 1)
		err = removeAtSegment(current, segment)
		if err != nil {
			return false
		}

		// Now index 0 should be what was originally at index 2
		result = current["items"].([]interface{})
		if result[0] != arr[2] {
			return false
		}

		// Length should be n-2
		return len(result) == n-2
	}

	require.NoError(t, quick.Check(f, &quick.Config{MaxCount: 200}))
}

// Feature: dashboard-remove-array-element, Property 5: Nested array removal via full path
// Validates: Requirements 4.1
func TestApplyJSONPath_NestedArrayRemoval(t *testing.T) {
	t.Run("remove nested array element", func(t *testing.T) {
		// Build a structure mimicking a dashboard with panels containing targets
		data := map[string]interface{}{
			"panels": []interface{}{
				map[string]interface{}{
					"title": "Panel 1",
					"targets": []interface{}{
						map[string]interface{}{"expr": "query_a"},
						map[string]interface{}{"expr": "query_b"},
						map[string]interface{}{"expr": "query_c"},
					},
				},
			},
		}

		// Remove targets[1] from panels[0]
		err := applyJSONPath(data, "$.panels[0].targets[1]", nil, true)
		require.NoError(t, err)

		// Verify outer structure is intact
		panels := data["panels"].([]interface{})
		require.Len(t, panels, 1)

		panel := panels[0].(map[string]interface{})
		assert.Equal(t, "Panel 1", panel["title"])

		// Verify inner array has the correct elements
		targets := panel["targets"].([]interface{})
		require.Len(t, targets, 2)
		assert.Equal(t, "query_a", targets[0].(map[string]interface{})["expr"])
		assert.Equal(t, "query_c", targets[1].(map[string]interface{})["expr"])
	})

	t.Run("remove from deeply nested path", func(t *testing.T) {
		data := map[string]interface{}{
			"panels": []interface{}{
				map[string]interface{}{
					"title":   "Panel 1",
					"targets": []interface{}{"t0", "t1", "t2"},
				},
				map[string]interface{}{
					"title":   "Panel 2",
					"targets": []interface{}{"t3", "t4"},
				},
			},
		}

		// Remove targets[0] from panels[1]
		err := applyJSONPath(data, "$.panels[1].targets[0]", nil, true)
		require.NoError(t, err)

		// Verify panels[0] is untouched
		panel0 := data["panels"].([]interface{})[0].(map[string]interface{})
		assert.Len(t, panel0["targets"].([]interface{}), 3)

		// Verify panels[1].targets has the correct element
		panel1 := data["panels"].([]interface{})[1].(map[string]interface{})
		targets := panel1["targets"].([]interface{})
		require.Len(t, targets, 1)
		assert.Equal(t, "t4", targets[0])
	})
}

// Unit tests for sortArrayRemovesDescending
// Validates: safe ordering of multiple array element removes
func TestSortArrayRemovesDescending(t *testing.T) {
	t.Run("single remove is unchanged", func(t *testing.T) {
		ops := []PatchOperation{
			{Op: "remove", Path: "$.panels[2]"},
		}
		result, err := sortArrayRemovesDescending(ops)
		require.NoError(t, err)
		assert.Equal(t, "$.panels[2]", result[0].Path)
	})

	t.Run("removes in descending order are unchanged", func(t *testing.T) {
		ops := []PatchOperation{
			{Op: "remove", Path: "$.panels[4]"},
			{Op: "remove", Path: "$.panels[2]"},
			{Op: "remove", Path: "$.panels[0]"},
		}
		result, err := sortArrayRemovesDescending(ops)
		require.NoError(t, err)
		assert.Equal(t, "$.panels[4]", result[0].Path)
		assert.Equal(t, "$.panels[2]", result[1].Path)
		assert.Equal(t, "$.panels[0]", result[2].Path)
	})

	t.Run("removes in ascending order are reordered", func(t *testing.T) {
		ops := []PatchOperation{
			{Op: "remove", Path: "$.panels[1]"},
			{Op: "remove", Path: "$.panels[3]"},
		}
		result, err := sortArrayRemovesDescending(ops)
		require.NoError(t, err)
		assert.Equal(t, "$.panels[3]", result[0].Path)
		assert.Equal(t, "$.panels[1]", result[1].Path)
	})

	t.Run("removes on different arrays are independent", func(t *testing.T) {
		ops := []PatchOperation{
			{Op: "remove", Path: "$.panels[1]"},
			{Op: "remove", Path: "$.annotations[3]"},
		}
		result, err := sortArrayRemovesDescending(ops)
		require.NoError(t, err)
		assert.Equal(t, "$.panels[1]", result[0].Path)
		assert.Equal(t, "$.annotations[3]", result[1].Path)
	})

	t.Run("nested array removes are sorted", func(t *testing.T) {
		ops := []PatchOperation{
			{Op: "remove", Path: "$.panels[0].targets[1]"},
			{Op: "remove", Path: "$.panels[0].targets[3]"},
		}
		result, err := sortArrayRemovesDescending(ops)
		require.NoError(t, err)
		assert.Equal(t, "$.panels[0].targets[3]", result[0].Path)
		assert.Equal(t, "$.panels[0].targets[1]", result[1].Path)
	})

	t.Run("mixed operations preserve non-remove order", func(t *testing.T) {
		ops := []PatchOperation{
			{Op: "replace", Path: "$.title", Value: "New Title"},
			{Op: "remove", Path: "$.panels[1]"},
			{Op: "add", Path: "$.panels/-", Value: map[string]interface{}{"id": 1}},
			{Op: "remove", Path: "$.panels[2]"},
		}
		result, err := sortArrayRemovesDescending(ops)
		require.NoError(t, err)
		// Non-remove ops stay in place
		assert.Equal(t, "replace", result[0].Op)
		assert.Equal(t, "$.title", result[0].Path)
		assert.Equal(t, "add", result[2].Op)
		// Remove ops are reordered: 2 before 1
		assert.Equal(t, "$.panels[2]", result[1].Path)
		assert.Equal(t, "$.panels[1]", result[3].Path)
	})

	t.Run("non-array removes are unchanged", func(t *testing.T) {
		ops := []PatchOperation{
			{Op: "remove", Path: "$.description"},
			{Op: "remove", Path: "$.tags"},
		}
		result, err := sortArrayRemovesDescending(ops)
		require.NoError(t, err)
		assert.Equal(t, "$.description", result[0].Path)
		assert.Equal(t, "$.tags", result[1].Path)
	})
}

func TestSortArrayRemovesDescending_SameIndexMultipleTimes(t *testing.T) {
	// Same index multiple times is rejected - likely an LLM mistake
	ops := []PatchOperation{
		{Op: "remove", Path: "$.panels[11]"},
		{Op: "remove", Path: "$.panels[11]"},
		{Op: "remove", Path: "$.panels[11]"},
	}
	_, err := sortArrayRemovesDescending(ops)
	require.Error(t, err, "Same index multiple times should be rejected")
	assert.Contains(t, err.Error(), "duplicate remove")
}

func TestUpdateDashboard_ValidationErrors(t *testing.T) {
	t.Run("uid without operations", func(t *testing.T) {
		_, err := updateDashboard(context.Background(), UpdateDashboardParams{
			UID: "some-uid",
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "'uid' was provided without 'operations'")
	})

	t.Run("operations without uid", func(t *testing.T) {
		_, err := updateDashboard(context.Background(), UpdateDashboardParams{
			Operations: []PatchOperation{
				{Op: "replace", Path: "$.title", Value: "New Title"},
			},
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "'operations' were provided without 'uid'")
	})

	t.Run("empty params", func(t *testing.T) {
		_, err := updateDashboard(context.Background(), UpdateDashboardParams{})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no dashboard content provided")
		assert.Contains(t, err.Error(), "Do NOT retry")
	})
}

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
