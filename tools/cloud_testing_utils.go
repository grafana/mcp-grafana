//go:build cloud
// +build cloud

package tools

import (
	"context"
	"os"
	"testing"

	mcpgrafana "github.com/grafana/mcp-grafana"
)

// createCloudTestContext creates a context with Grafana URL and API key for cloud integration tests.
// The test will be skipped if required environment variables are not set.
// testName is used to customize the skip message (e.g. "OnCall", "Sift", "Incident")
func createCloudTestContext(t *testing.T, testName string) context.Context {
	grafanaURL := os.Getenv("GRAFANA_URL")
	if grafanaURL == "" {
		t.Skipf("GRAFANA_URL environment variable not set, skipping cloud %s integration tests", testName)
	}

	grafanaApiKey := os.Getenv("GRAFANA_API_KEY")
	if grafanaApiKey == "" {
		t.Skipf("GRAFANA_API_KEY environment variable not set, skipping cloud %s integration tests", testName)
	}

	ctx := context.Background()
	ctx = mcpgrafana.WithGrafanaURL(ctx, grafanaURL)
	ctx = mcpgrafana.WithGrafanaAPIKey(ctx, grafanaApiKey)

	return ctx
}
