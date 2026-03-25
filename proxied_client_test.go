//go:build unit
// +build unit

package mcpgrafana

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"

	"github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProxiedClientAuthHeaders(t *testing.T) {
	t.Run("uses obo headers when access and id tokens are set", func(t *testing.T) {
		headers := proxiedClientAuthHeaders(GrafanaConfig{
			AccessToken: "access-token",
			IDToken:     "id-token",
			APIKey:      "api-key",
		})

		assert.Equal(t, "access-token", headers["X-Access-Token"])
		assert.Equal(t, "id-token", headers["X-Grafana-Id"])
		assert.Empty(t, headers["Authorization"])
	})

	t.Run("falls back to bearer api key", func(t *testing.T) {
		headers := proxiedClientAuthHeaders(GrafanaConfig{APIKey: "api-key"})

		assert.Equal(t, "Bearer api-key", headers["Authorization"])
		assert.Empty(t, headers["X-Access-Token"])
		assert.Empty(t, headers["X-Grafana-Id"])
	})

	t.Run("uses id token as bearer when access token is absent", func(t *testing.T) {
		headers := proxiedClientAuthHeaders(GrafanaConfig{IDToken: "user-token"})

		assert.Equal(t, "Bearer user-token", headers["Authorization"])
		assert.Empty(t, headers["X-Access-Token"])
		assert.Empty(t, headers["X-Grafana-Id"])
	})

	t.Run("falls back to basic auth", func(t *testing.T) {
		headers := proxiedClientAuthHeaders(GrafanaConfig{BasicAuth: url.UserPassword("user", "pass")})

		expected := "Basic " + base64.StdEncoding.EncodeToString([]byte("user:pass"))
		assert.Equal(t, expected, headers["Authorization"])
	})

	t.Run("adds auth proxy identity headers when enabled", func(t *testing.T) {
		headers := proxiedClientAuthHeaders(GrafanaConfig{
			ProxyAuthEnabled: true,
			ProxyUserHeader:  "X-WEBAUTH-USER",
			ProxyEmailHeader: "X-WEBAUTH-EMAIL",
			ProxyNameHeader:  "X-WEBAUTH-NAME",
			ProxyRoleHeader:  "X-WEBAUTH-ROLE",
			AuthenticatedUser: &OAuth2UserInfo{
				Username: "john.doe",
				Email:    "john@example.com",
				Name:     "John Doe",
				Roles:    []string{"Editor", "Viewer"},
			},
		})

		assert.Equal(t, "john.doe", headers["X-WEBAUTH-USER"])
		assert.Equal(t, "john@example.com", headers["X-WEBAUTH-EMAIL"])
		assert.Equal(t, "John Doe", headers["X-WEBAUTH-NAME"])
		assert.Equal(t, "Editor,Viewer", headers["X-WEBAUTH-ROLE"])
	})
}

func TestNewProxiedClientForwardsAuthHeaders(t *testing.T) {
	type observedHeaders struct {
		authorization string
		xAccessToken  string
		xGrafanaID    string
	}

	newEndpoint := func(t *testing.T) (string, func() []observedHeaders) {
		t.Helper()

		type pingParams struct {
			Dummy string `json:"dummy,omitempty" jsonschema:"description=Unused placeholder field"`
		}

		var mu sync.Mutex
		captured := make([]observedHeaders, 0, 4)

		httpCtx := func(ctx context.Context, req *http.Request) context.Context {
			mu.Lock()
			captured = append(captured, observedHeaders{
				authorization: req.Header.Get("Authorization"),
				xAccessToken:  req.Header.Get("X-Access-Token"),
				xGrafanaID:    req.Header.Get("X-Grafana-Id"),
			})
			mu.Unlock()
			return ctx
		}

		s := server.NewMCPServer("test-remote", "1.0.0")
			dummyTool := MustTool("ping", "test tool", func(ctx context.Context, _ pingParams) (string, error) {
				return "pong", nil
			})
			dummyTool.Register(s)
		h := server.NewStreamableHTTPServer(
			s,
			server.WithEndpointPath("/mcp"),
			server.WithHTTPContextFunc(httpCtx),
		)

		mux := http.NewServeMux()
		mux.Handle("/mcp", h)

		ts := httptest.NewServer(mux)
		t.Cleanup(ts.Close)

		getCaptured := func() []observedHeaders {
			mu.Lock()
			defer mu.Unlock()
			out := make([]observedHeaders, len(captured))
			copy(out, captured)
			return out
		}

		return ts.URL + "/mcp", getCaptured
	}

	t.Run("bearer forwarding mode sends Authorization header", func(t *testing.T) {
		endpoint, getCaptured := newEndpoint(t)

		ctx := WithGrafanaConfig(context.Background(), GrafanaConfig{
			IDToken: "user-oauth-token",
		})

		pc, err := NewProxiedClient(ctx, "ds-1", "tempo", "tempo", endpoint)
		require.NoError(t, err)
		t.Cleanup(func() { _ = pc.Close() })

		captured := getCaptured()
		require.GreaterOrEqual(t, len(captured), 2)

		for _, h := range captured {
			assert.Equal(t, "Bearer user-oauth-token", h.authorization)
			assert.Empty(t, h.xAccessToken)
			assert.Empty(t, h.xGrafanaID)
		}
	})

	t.Run("cloud header forwarding mode sends X-Access-Token and X-Grafana-Id headers", func(t *testing.T) {
		endpoint, getCaptured := newEndpoint(t)

		ctx := WithGrafanaConfig(context.Background(), GrafanaConfig{
			AccessToken: "grafana-service-token",
			IDToken:     "user-oauth-token",
		})

		pc, err := NewProxiedClient(ctx, "ds-2", "tempo", "tempo", endpoint)
		require.NoError(t, err)
		t.Cleanup(func() { _ = pc.Close() })

		captured := getCaptured()
		require.GreaterOrEqual(t, len(captured), 2)

		for _, h := range captured {
			assert.Empty(t, h.authorization)
			assert.Equal(t, "grafana-service-token", h.xAccessToken)
			assert.Equal(t, "user-oauth-token", h.xGrafanaID)
		}
	})
}
