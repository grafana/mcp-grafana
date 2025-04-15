//go:build unit
// +build unit

package mcpgrafana

import (
	"context"
	"net/http"
	"testing"

	"github.com/go-openapi/runtime/client"
	grafana_client "github.com/grafana/grafana-openapi-client-go/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExtractIncidentClientFromEnv(t *testing.T) {
	t.Setenv("GRAFANA_URL", "http://my-test-url.grafana.com/")
	ctx := ExtractIncidentClientFromEnv(context.Background())

	client := IncidentClientFromContext(ctx)
	require.NotNil(t, client)
	assert.Equal(t, "http://my-test-url.grafana.com/api/plugins/grafana-irm-app/resources/api/v1/", client.RemoteHost)
}

func TestExtractIncidentClientFromHeaders(t *testing.T) {
	t.Run("no headers, no env", func(t *testing.T) {
		req, err := http.NewRequest("GET", "http://example.com", nil)
		require.NoError(t, err)
		ctx := ExtractIncidentClientFromHeaders(context.Background(), req)

		client := IncidentClientFromContext(ctx)
		require.NotNil(t, client)
		assert.Equal(t, "http://localhost:3000/api/plugins/grafana-irm-app/resources/api/v1/", client.RemoteHost)
	})

	t.Run("no headers, with env", func(t *testing.T) {
		t.Setenv("GRAFANA_URL", "http://my-test-url.grafana.com/")
		req, err := http.NewRequest("GET", "http://example.com", nil)
		require.NoError(t, err)
		ctx := ExtractIncidentClientFromHeaders(context.Background(), req)

		client := IncidentClientFromContext(ctx)
		require.NotNil(t, client)
		assert.Equal(t, "http://my-test-url.grafana.com/api/plugins/grafana-irm-app/resources/api/v1/", client.RemoteHost)
	})

	t.Run("with headers, no env", func(t *testing.T) {
		req, err := http.NewRequest("GET", "http://example.com", nil)
		req.Header.Set(grafanaURLHeader, "http://my-test-url.grafana.com")
		require.NoError(t, err)
		ctx := ExtractIncidentClientFromHeaders(context.Background(), req)

		client := IncidentClientFromContext(ctx)
		require.NotNil(t, client)
		assert.Equal(t, "http://my-test-url.grafana.com/api/plugins/grafana-irm-app/resources/api/v1/", client.RemoteHost)
	})

	t.Run("with headers, with env", func(t *testing.T) {
		t.Setenv("GRAFANA_URL", "will-not-be-used")
		req, err := http.NewRequest("GET", "http://example.com", nil)
		req.Header.Set(grafanaURLHeader, "http://my-test-url.grafana.com")
		require.NoError(t, err)
		ctx := ExtractIncidentClientFromHeaders(context.Background(), req)

		client := IncidentClientFromContext(ctx)
		require.NotNil(t, client)
		assert.Equal(t, "http://my-test-url.grafana.com/api/plugins/grafana-irm-app/resources/api/v1/", client.RemoteHost)
	})
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

func TestExtractGrafanaClientPath(t *testing.T) {
	t.Run("no custom path", func(t *testing.T) {
		t.Setenv("GRAFANA_URL", "http://my-test-url.grafana.com/")
		ctx := ExtractGrafanaClientFromEnv(context.Background())

		c := GrafanaClientFromContext(ctx)
		require.NotNil(t, c)
		rt := c.Transport.(*client.Runtime)
		assert.Equal(t, "/api", rt.BasePath)
	})

	t.Run("custom path", func(t *testing.T) {
		t.Setenv("GRAFANA_URL", "http://my-test-url.grafana.com/grafana")
		ctx := ExtractGrafanaClientFromEnv(context.Background())

		c := GrafanaClientFromContext(ctx)
		require.NotNil(t, c)
		rt := c.Transport.(*client.Runtime)
		assert.Equal(t, "/grafana/api", rt.BasePath)
	})

	t.Run("custom path, trailing slash", func(t *testing.T) {
		t.Setenv("GRAFANA_URL", "http://my-test-url.grafana.com/grafana/")
		ctx := ExtractGrafanaClientFromEnv(context.Background())

		c := GrafanaClientFromContext(ctx)
		require.NotNil(t, c)
		rt := c.Transport.(*client.Runtime)
		assert.Equal(t, "/grafana/api", rt.BasePath)
	})
}

// minURL is a helper struct representing what we can extract from a constructed
// Grafana client.
type minURL struct {
	host, basePath string
}

// minURLFromClient extracts some minimal amount of URL info from a Grafana client.
func minURLFromClient(c *grafana_client.GrafanaHTTPAPI) minURL {
	rt := c.Transport.(*client.Runtime)
	return minURL{rt.Host, rt.BasePath}
}

func TestExtractGrafanaClientFromHeaders(t *testing.T) {
	t.Run("no headers, no env", func(t *testing.T) {
		req, err := http.NewRequest("GET", "http://example.com", nil)
		require.NoError(t, err)
		ctx := ExtractGrafanaClientFromHeaders(context.Background(), req)
		c := GrafanaClientFromContext(ctx)
		url := minURLFromClient(c)
		assert.Equal(t, "localhost", url.host)
		assert.Equal(t, "/api", url.basePath)
	})

	t.Run("no headers, with env", func(t *testing.T) {
		t.Setenv("GRAFANA_URL", "http://my-test-url.grafana.com")

		req, err := http.NewRequest("GET", "http://example.com", nil)
		require.NoError(t, err)
		ctx := ExtractGrafanaClientFromHeaders(context.Background(), req)
		c := GrafanaClientFromContext(ctx)
		url := minURLFromClient(c)
		assert.Equal(t, "my-test-url.grafana.com", url.host)
		assert.Equal(t, "/api", url.basePath)
	})

	t.Run("with headers, no env", func(t *testing.T) {
		req, err := http.NewRequest("GET", "http://example.com", nil)
		require.NoError(t, err)
		req.Header.Set(grafanaURLHeader, "http://my-test-url.grafana.com")
		ctx := ExtractGrafanaClientFromHeaders(context.Background(), req)
		c := GrafanaClientFromContext(ctx)
		url := minURLFromClient(c)
		assert.Equal(t, "my-test-url.grafana.com", url.host)
		assert.Equal(t, "/api", url.basePath)
	})

	t.Run("with headers, with env", func(t *testing.T) {
		// Env vars should be ignored if headers are present.
		t.Setenv("GRAFANA_URL", "will-not-be-used")

		req, err := http.NewRequest("GET", "http://example.com", nil)
		require.NoError(t, err)
		req.Header.Set(grafanaURLHeader, "http://my-test-url.grafana.com")
		ctx := ExtractGrafanaClientFromHeaders(context.Background(), req)
		c := GrafanaClientFromContext(ctx)
		url := minURLFromClient(c)
		assert.Equal(t, "my-test-url.grafana.com", url.host)
		assert.Equal(t, "/api", url.basePath)
	})
}
