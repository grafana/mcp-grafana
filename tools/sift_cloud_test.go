//go:build cloud
// +build cloud

// This file contains cloud integration tests that run against a dedicated test instance
// at mcptests.grafana-dev.net. This instance is configured with a minimal setup on the Sift side:
//   - 1 test investigation
// These tests expect this configuration to exist and will skip if the required
// environment variables (GRAFANA_URL, GRAFANA_API_KEY) are not set.

package tools

import (
	"context"
	"os"
	"testing"

	mcpgrafana "github.com/grafana/mcp-grafana"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func createSiftCloudTestContext(t *testing.T) context.Context {
	grafanaURL := os.Getenv("GRAFANA_URL")
	if grafanaURL == "" {
		t.Skip("GRAFANA_URL environment variable not set, skipping cloud Sift integration tests")
	}

	grafanaApiKey := os.Getenv("GRAFANA_API_KEY")
	if grafanaApiKey == "" {
		t.Skip("GRAFANA_API_KEY environment variable not set, skipping cloud Sift integration tests")
	}

	ctx := context.Background()
	ctx = mcpgrafana.WithGrafanaURL(ctx, grafanaURL)
	ctx = mcpgrafana.WithGrafanaAPIKey(ctx, grafanaApiKey)

	return ctx
}

func TestCloudSiftInvestigations(t *testing.T) {
	ctx := createSiftCloudTestContext(t)

	// Test listing all investigations
	t.Run("list all investigations", func(t *testing.T) {
		result, err := listInvestigations(ctx, ListInvestigationsParams{})
		require.NoError(t, err, "Should not error when listing investigations")
		assert.NotNil(t, result, "Result should not be nil")
		assert.GreaterOrEqual(t, len(result), 1, "Should have at least one investigation")
	})

	// Get an investigation ID from the list to test getting a specific investigation
	investigations, err := listInvestigations(ctx, ListInvestigationsParams{Limit: 1})
	require.NoError(t, err, "Should not error when listing investigations")
	require.NotEmpty(t, investigations, "Should have at least one investigation to test with")

	investigationID := investigations[0].ID.String()

	// Test getting a specific investigation
	t.Run("get specific investigation", func(t *testing.T) {
		result, err := getInvestigation(ctx, GetInvestigationParams{
			ID: investigationID,
		})
		require.NoError(t, err, "Should not error when getting specific investigation")
		assert.NotNil(t, result, "Result should not be nil")
		assert.Equal(t, investigationID, result.ID.String(), "Should return the correct investigation")

		// Verify all required fields are present
		assert.NotEmpty(t, result.Name, "Investigation should have a name")
		assert.NotEmpty(t, result.TenantID, "Investigation should have a tenant ID")
		assert.NotNil(t, result.Datasources, "Investigation should have datasources")
		assert.NotNil(t, result.RequestData, "Investigation should have request data")
		assert.NotNil(t, result.Analyses, "Investigation should have analyses")
	})

	// Test getting a non-existent investigation
	t.Run("get non-existent investigation", func(t *testing.T) {
		_, err := getInvestigation(ctx, GetInvestigationParams{
			ID: "00000000-0000-0000-0000-000000000000",
		})
		assert.NoError(t, err, "Should not error when getting non-existent investigation")
	})
}
