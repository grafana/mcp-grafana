//go:build cloud
// +build cloud

// This file contains cloud integration tests that run against a dedicated test instance
// at mcptests.grafana-dev.net. This instance is configured with a minimal setup on the Sift side:
//   - 2 test investigations
// These tests expect this configuration to exist and will skip if the required
// environment variables (GRAFANA_URL, GRAFANA_API_KEY) are not set.

package tools

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCloudSiftInvestigations(t *testing.T) {
	ctx := createCloudTestContext(t, "Sift")

	// Test listing all investigations
	t.Run("list all investigations", func(t *testing.T) {
		result, err := listInvestigations(ctx, ListInvestigationsParams{})
		require.NoError(t, err, "Should not error when listing investigations")
		assert.NotNil(t, result, "Result should not be nil")
		assert.GreaterOrEqual(t, len(result), 1, "Should have at least one investigation")
	})

	// Test listing investigations with a limit
	t.Run("list investigations with limit", func(t *testing.T) {
		// Get the client
		client, err := siftClientFromContext(ctx)
		require.NoError(t, err, "Should not error when getting Sift client")

		// List investigations with a limit of 1
		investigations, err := client.listInvestigations(ctx, 1)
		require.NoError(t, err, "Should not error when listing investigations with limit")
		assert.NotNil(t, investigations, "Investigations should not be nil")
		assert.LessOrEqual(t, len(investigations), 1, "Should have at most one investigation")

		// If there are investigations, verify their structure
		if len(investigations) > 0 {
			investigation := investigations[0]
			assert.NotEmpty(t, investigation.ID, "Investigation should have an ID")
			assert.NotEmpty(t, investigation.Name, "Investigation should have a name")
			assert.NotEmpty(t, investigation.TenantID, "Investigation should have a tenant ID")
		}
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

	// Test getting analyses for an investigation
	t.Run("get analyses for investigation", func(t *testing.T) {
		// Get the investigation
		result, err := getInvestigation(ctx, GetInvestigationParams{
			ID: investigationID,
		})
		require.NoError(t, err, "Should not error when getting specific investigation")
		assert.NotNil(t, result, "Result should not be nil")

		// Get an analysis ID
		analysisID := result.Analyses.Items[0].ID

		// Get the analysis
		analysis, err := getAnalysis(ctx, GetAnalysisParams{
			InvestigationID: investigationID,
			AnalysisID:      analysisID.String(),
		})
		require.NoError(t, err, "Should not error when getting specific analysis")
		assert.NotNil(t, analysis, "Analysis should not be nil")

		// Verify all required fields are present
		assert.NotEmpty(t, analysis.Name, "Analysis should have a name")
		assert.NotEmpty(t, analysis.InvestigationID, "Analysis should have an investigation ID")
		assert.NotNil(t, analysis.Result, "Analysis should have a result")
	})
}
