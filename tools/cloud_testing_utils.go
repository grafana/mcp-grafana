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
// urlEnv and apiKeyEnv specify the environment variable names for the Grafana URL and API key.
func createCloudTestContext(t *testing.T, testName, urlEnv, apiKeyEnv string) context.Context {
	grafanaURL := os.Getenv(urlEnv)
	if grafanaURL == "" {
		t.Skipf("%s environment variable not set, skipping cloud %s integration tests", urlEnv, testName)
	}

	grafanaApiKey := os.Getenv(apiKeyEnv)
	if grafanaApiKey == "" {
		t.Skipf("%s environment variable not set, skipping cloud %s integration tests", apiKeyEnv, testName)
	}

	ctx := context.Background()
	ctx = mcpgrafana.WithGrafanaURL(ctx, grafanaURL)
	ctx = mcpgrafana.WithGrafanaAPIKey(ctx, grafanaApiKey)

	return ctx
}
