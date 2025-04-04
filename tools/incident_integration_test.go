// Requires a Cloud or other Grafana instance with Grafana Incident available,
// with a Prometheus datasource provisioned.
//go:build cloud
// +build cloud

package tools

import (
	"context"
	"os"
	"testing"

	mcpgrafana "github.com/grafana/mcp-grafana"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// This file contains cloud integration tests that run against a dedicated test instance
// at mcptests.grafana-dev.net. This instance is configured with a minimal setup on the Incident side
// with two incidents created, one minor and one major, and both of them resolved.
// These tests expect this configuration to exist and will skip if the required
// environment variables (GRAFANA_URL, GRAFANA_API_KEY) are not set.

func createCloudTestContext(t *testing.T) context.Context {
	grafanaURL := os.Getenv("GRAFANA_URL")
	if grafanaURL == "" {
		t.Skip("GRAFANA_URL environment variable not set, skipping cloud Incident integration tests")
	}

	grafanaApiKey := os.Getenv("GRAFANA_API_KEY")
	if grafanaApiKey == "" {
		t.Skip("GRAFANA_API_KEY environment variable not set, skipping cloud Incident integration tests")
	}

	ctx := context.Background()
	ctx = mcpgrafana.WithGrafanaURL(ctx, grafanaURL)
	ctx = mcpgrafana.WithGrafanaAPIKey(ctx, grafanaApiKey)
	ctx = mcpgrafana.ExtractIncidentClientFromEnv(ctx)
	return ctx
}

func TestCloudIncidentTools(t *testing.T) {
	t.Run("list incidents", func(t *testing.T) {
		ctx := createCloudTestContext(t)
		result, err := listIncidents(ctx, ListIncidentsParams{
			Limit: 1,
		})
		require.NoError(t, err)
		assert.NotNil(t, result, "Result should not be nil")
		assert.NotNil(t, result.IncidentPreviews, "IncidentPreviews should not be nil")
		assert.LessOrEqual(t, len(result.IncidentPreviews), 1, "Should not return more incidents than the limit")
	})

	t.Run("get incident by ID", func(t *testing.T) {
		ctx := createCloudTestContext(t)
		result, err := getIncident(ctx, GetIncidentParams{
			ID: "1",
		})
		require.NoError(t, err)
		assert.NotNil(t, result, "Result should not be nil")
		assert.Equal(t, "1", result.IncidentID, "Should return the requested incident ID")
		assert.NotEmpty(t, result.Title, "Incident should have a title")
		assert.NotEmpty(t, result.Status, "Incident should have a status")
	})
}
