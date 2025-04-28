//go:build asserts
// +build asserts

// This file contains cloud integration tests that run against a dedicated test instance
// connected to a Grafana instance at (ASSERTS_GRAFANA_URL, ASSERTS_GRAFANA_API_KEY).
// These tests expect this configuration to exist and will skip if the required
// environment variables are not set.

package tools

import (
	"context"
	"os"
	"testing"
	"time"

	mcpgrafana "github.com/grafana/mcp-grafana"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func createAssertsCloudTestContext(t *testing.T, testName string) context.Context {
	grafanaURL := os.Getenv("ASSERTS_GRAFANA_URL")
	if grafanaURL == "" {
		t.Skipf("ASSERTS_GRAFANA_URL environment variable not set, skipping cloud %s integration tests", testName)
	}

	grafanaApiKey := os.Getenv("ASSERTS_GRAFANA_API_KEY")
	if grafanaApiKey == "" {
		t.Skipf("ASSERTS_GRAFANA_API_KEY environment variable not set, skipping cloud %s integration tests", testName)
	}

	ctx := context.Background()
	ctx = mcpgrafana.WithGrafanaURL(ctx, grafanaURL)
	ctx = mcpgrafana.WithGrafanaAPIKey(ctx, grafanaApiKey)

	return ctx
}

func TestAssertsCloudIntegration(t *testing.T) {
	ctx := createAssertsCloudTestContext(t, "Asserts")

	t.Run("get assertions", func(t *testing.T) {
		// Set up time range for the last hour
		endTime := time.Now()
		startTime := endTime.Add(-24 * time.Hour)

		// Test parameters for a known service in the environment
		params := GetAssertionsParams{
			StartTime:  startTime,
			EndTime:    endTime,
			EntityType: "Service", // Adjust these values based on your actual environment
			EntityName: "model-builder",
			Env:        "dev-us-central-0",
			Namespace:  "asserts",
		}

		// Get assertions from the real Grafana instance
		result, err := getAssertions(ctx, params)
		require.NoError(t, err, "Failed to get assertions from Grafana")
		assert.NotEmpty(t, result, "Expected non-empty assertions result")

		// Basic validation of the response structure
		assert.Contains(t, result, "summaries", "Response should contain a summaries field")
	})
}
