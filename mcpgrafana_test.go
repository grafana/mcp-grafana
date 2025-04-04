//go:build unit
// +build unit

package mcpgrafana

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExtractIncidentClientFromEnv(t *testing.T) {
	t.Setenv("GRAFANA_URL", "http://my-test-url.grafana.com/")
	ctx := ExtractIncidentClientFromEnv(context.Background())

	client := IncidentClientFromContext(ctx)
	require.NotNil(t, client)
	assert.Equal(t, "http://my-test-url.grafana.com/api/plugins/grafana-incident-app/resources/api/v1/", client.RemoteHost)
}

func TestExtractGrafanaInfoFromHeaders(t *testing.T) {
	t.Run("no headers, no env", func(t *testing.T) {
		req, err := http.NewRequest("GET", "http://example.com", nil)
		require.NoError(t, err)
		ctx := ExtractGrafanaInfoFromHeaders(context.Background(), req)
		url := GrafanaURLFromContext(ctx)
		assert.Equal(t, defaultGrafanaURL, url)
		apiKey := GrafanaAPIKeyFromContext(ctx)
		assert.Equal(t, "", apiKey)
	})

	t.Run("no headers, with env", func(t *testing.T) {
		t.Setenv("GRAFANA_URL", "http://my-test-url.grafana.com")
		t.Setenv("GRAFANA_API_KEY", "my-test-api-key")

		req, err := http.NewRequest("GET", "http://example.com", nil)
		require.NoError(t, err)
		ctx := ExtractGrafanaInfoFromHeaders(context.Background(), req)
		url := GrafanaURLFromContext(ctx)
		assert.Equal(t, "http://my-test-url.grafana.com", url)
		apiKey := GrafanaAPIKeyFromContext(ctx)
		assert.Equal(t, "my-test-api-key", apiKey)
	})

	t.Run("with headers, no env", func(t *testing.T) {
		req, err := http.NewRequest("GET", "http://example.com", nil)
		require.NoError(t, err)
		req.Header.Set(grafanaURLHeader, "http://my-test-url.grafana.com")
		req.Header.Set(grafanaAPIKeyHeader, "my-test-api-key")
		ctx := ExtractGrafanaInfoFromHeaders(context.Background(), req)
		url := GrafanaURLFromContext(ctx)
		assert.Equal(t, "http://my-test-url.grafana.com", url)
		apiKey := GrafanaAPIKeyFromContext(ctx)
		assert.Equal(t, "my-test-api-key", apiKey)
	})

	t.Run("with headers, with env", func(t *testing.T) {
		// Env vars should be ignored if headers are present.
		t.Setenv("GRAFANA_URL", "will-not-be-used")
		t.Setenv("GRAFANA_API_KEY", "will-not-be-used")

		req, err := http.NewRequest("GET", "http://example.com", nil)
		require.NoError(t, err)
		req.Header.Set(grafanaURLHeader, "http://my-test-url.grafana.com")
		req.Header.Set(grafanaAPIKeyHeader, "my-test-api-key")
		ctx := ExtractGrafanaInfoFromHeaders(context.Background(), req)
		url := GrafanaURLFromContext(ctx)
		assert.Equal(t, "http://my-test-url.grafana.com", url)
		apiKey := GrafanaAPIKeyFromContext(ctx)
		assert.Equal(t, "my-test-api-key", apiKey)
	})
}
