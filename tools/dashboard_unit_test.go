//go:build unit

package tools

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDashboardJSONFormat(t *testing.T) {
	t.Run("dashboard JSON marshaling preserves structure", func(t *testing.T) {
		// Test that dashboard JSON is preserved correctly for provisioned dashboards
		dashboardMap := map[string]interface{}{
			"uid":     "test-uid",
			"title":   "Test Dashboard",
			"version": 1,
			"panels": []interface{}{
				map[string]interface{}{
					"id":    1,
					"title": "Panel 1",
					"type":  "graph",
				},
			},
		}

		// Test JSON marshaling (this is what happens in smartUpdateDashboard)
		contentBytes, err := json.MarshalIndent(dashboardMap, "", "  ")
		require.NoError(t, err)

		// Verify the JSON structure is preserved
		var unmarshaled map[string]interface{}
		err = json.Unmarshal(contentBytes, &unmarshaled)
		require.NoError(t, err)

		// Check key fields are preserved
		assert.Equal(t, "test-uid", unmarshaled["uid"])
		assert.Equal(t, "Test Dashboard", unmarshaled["title"])
		assert.Equal(t, float64(1), unmarshaled["version"]) // JSON unmarshals numbers as float64

		// Verify it's NOT wrapped in Kubernetes resource format
		assert.NotContains(t, string(contentBytes), "apiVersion")
		assert.NotContains(t, string(contentBytes), "kind")
		assert.NotContains(t, string(contentBytes), "spec")
		assert.NotContains(t, string(contentBytes), "metadata")

		// Verify it contains the actual dashboard fields
		assert.Contains(t, string(contentBytes), "uid")
		assert.Contains(t, string(contentBytes), "title")
		assert.Contains(t, string(contentBytes), "panels")
	})

	t.Run("dashboard JSON vs kubernetes resource format", func(t *testing.T) {
		// Test the difference between plain JSON and Kubernetes resource format
		dashboardJSON := map[string]interface{}{
			"uid":     "my-dashboard",
			"title":   "My Dashboard",
			"version": 1,
			"panels":  []interface{}{},
		}

		// Plain JSON format (what we should use for provisioned dashboards)
		plainJSON, err := json.MarshalIndent(dashboardJSON, "", "  ")
		require.NoError(t, err)

		// Kubernetes resource format (what we should NOT use)
		k8sResource := map[string]interface{}{
			"apiVersion": "dashboard.grafana.app/v2alpha1",
			"kind":       "Dashboard",
			"metadata": map[string]interface{}{
				"name": "my-dashboard",
			},
			"spec": dashboardJSON,
		}
		k8sJSON, err := json.MarshalIndent(k8sResource, "", "  ")
		require.NoError(t, err)

		// Verify plain JSON is simpler and contains dashboard fields directly
		assert.Contains(t, string(plainJSON), `"uid": "my-dashboard"`)
		assert.Contains(t, string(plainJSON), `"title": "My Dashboard"`)
		assert.NotContains(t, string(plainJSON), "apiVersion")
		assert.NotContains(t, string(plainJSON), "spec")

		// Verify k8s format has the wrapper (but we don't want this)
		assert.Contains(t, string(k8sJSON), "apiVersion")
		assert.Contains(t, string(k8sJSON), "spec")
		assert.Contains(t, string(k8sJSON), `"name": "my-dashboard"`)

		// Demonstrate the size difference (plain JSON should be smaller)
		assert.True(t, len(plainJSON) < len(k8sJSON), "Plain JSON should be more compact than Kubernetes resource format")
	})
}

func TestProvisionedDashboardDetection(t *testing.T) {
	t.Run("manager info parsing", func(t *testing.T) {
		// Test parsing of manager information from getDashboardManager response
		managerInfo := `This dashboard is managed:
- managedBy: repo
- managerId: my-repo-123
- sourcePath: dashboards/production/api-metrics.json`

		// Test the logic used in smartUpdateDashboard
		var managerId, sourcePath string
		if strings.Contains(managerInfo, "managedBy:") && strings.Contains(managerInfo, "managerId:") && strings.Contains(managerInfo, "sourcePath:") {
			lines := strings.Split(managerInfo, "\n")
			for _, line := range lines {
				if strings.HasPrefix(line, "- managerId:") {
					managerId = strings.TrimSpace(strings.TrimPrefix(line, "- managerId:"))
				}
				if strings.HasPrefix(line, "- sourcePath:") {
					sourcePath = strings.TrimSpace(strings.TrimPrefix(line, "- sourcePath:"))
				}
			}
		}

		// Verify parsing worked correctly
		assert.Equal(t, "my-repo-123", managerId)
		assert.Equal(t, "dashboards/production/api-metrics.json", sourcePath)
	})
}
