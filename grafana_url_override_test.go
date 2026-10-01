//go:build unit

package mcpgrafana

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGrafanaURLOverrideMiddleware(t *testing.T) {
	allowed := []string{"https://grafana.example.com/team"}
	for _, tc := range []struct {
		name, target, token string
		wantStatus          int
	}{
		{"no override", "", "", http.StatusNoContent},
		{"disabled", allowed[0], "request-token", http.StatusForbidden},
		{"no credential", allowed[0], "", http.StatusBadRequest},
		{"unlisted target", "https://other.example.com", "request-token", http.StatusForbidden},
		{"invalid target", "http://127.0.0.1:80/?x=y", "request-token", http.StatusBadRequest},
		{"allowed", allowed[0], "request-token", http.StatusNoContent},
		{"legacy token", allowed[0], "request-token", http.StatusNoContent},
		{"unrestricted", "http://127.0.0.1:3000", "request-token", http.StatusNoContent},
		{"unrestricted no credential", "http://127.0.0.1:3000", "", http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
			if tc.target != "" {
				req.Header.Set(grafanaURLHeader, tc.target)
			}
			if tc.token != "" {
				if tc.name == "legacy token" {
					req.Header.Set(grafanaAPIKeyHeader, tc.token)
				} else {
					req.Header.Set(grafanaServiceAccountTokenHeader, tc.token)
				}
			}
			list := allowed
			if tc.name == "disabled" || strings.HasPrefix(tc.name, "unrestricted") {
				list = nil
			}
			called := false
			h := GrafanaURLOverrideMiddleware(tc.name != "disabled", list, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				called = true
				if tc.name == "allowed" || tc.name == "legacy token" || tc.name == "unrestricted" {
					assert.Equal(t, tc.target, r.Context().Value(grafanaOverrideKey{}))
				}
				w.WriteHeader(http.StatusNoContent)
			}))
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)
			assert.Equal(t, tc.wantStatus, w.Code)
			assert.Equal(t, tc.wantStatus == http.StatusNoContent, called)
		})
	}
}

func TestGrafanaURLOverrideIsolatesConfiguredCredentials(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "Bearer request-token", r.Header.Get("Authorization"))
		assert.Empty(t, r.Header.Get("Cookie"))
		assert.Empty(t, r.Header.Get("X-Env-Secret"))
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	t.Setenv(grafanaURLEnvVar, "https://configured.example.com")
	t.Setenv(grafanaServiceAccountTokenEnvVar, "env-token")
	t.Setenv(grafanaUsernameEnvVar, "env-user")
	t.Setenv(grafanaPasswordEnvVar, "env-password")
	t.Setenv(grafanaExtraHeadersEnvVar, `{"Cookie":"env-cookie","X-Env-Secret":"env-secret"}`)
	request := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	request.Header.Set(grafanaURLHeader, server.URL)
	request.Header.Set(grafanaServiceAccountTokenHeader, "request-token")
	var cfg GrafanaConfig
	h := GrafanaURLOverrideMiddleware(true, []string{server.URL}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := ExtractGrafanaInfoFromHeaders(WithGrafanaConfig(r.Context(), GrafanaConfig{
			TLSConfig: &TLSConfig{CertFile: "client.crt", KeyFile: "client.key", SkipVerify: true},
		}), r)
		cfg = GrafanaConfigFromContext(ctx)
		transport, err := BuildTransport(&cfg, nil)
		require.NoError(t, err)
		out, err := http.NewRequestWithContext(ctx, http.MethodGet, cfg.URL+"/api/health", nil)
		require.NoError(t, err)
		resp, err := (&http.Client{Transport: transport}).Do(out)
		require.NoError(t, err)
		_ = resp.Body.Close()
		w.WriteHeader(resp.StatusCode)
	}))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, request)
	require.Equal(t, http.StatusNoContent, w.Code)
	assert.Equal(t, server.URL, cfg.URL)
	assert.Equal(t, server.URL, cfg.OverrideURL)
	assert.Equal(t, "request-token", cfg.APIKey)
	assert.Nil(t, cfg.BasicAuth)
	require.NotNil(t, cfg.TLSConfig)
	assert.Empty(t, cfg.TLSConfig.CertFile)
	assert.Empty(t, cfg.TLSConfig.KeyFile)
	assert.False(t, cfg.TLSConfig.SkipVerify)
	assert.Empty(t, cfg.ExtraHeaders)
	assert.Equal(t, int64(0), cfg.OrgID)

	// A bare header cannot bypass the middleware and remains on the configured URL.
	bare := GrafanaConfigFromContext(ExtractGrafanaInfoFromHeaders(context.Background(), request))
	assert.Equal(t, "https://configured.example.com", bare.URL)
	assert.Equal(t, "request-token", bare.APIKey)
	assert.Empty(t, bare.OverrideURL)
	request.Header.Del(grafanaServiceAccountTokenHeader)
	bare = GrafanaConfigFromContext(ExtractGrafanaInfoFromHeaders(context.Background(), request))
	assert.Equal(t, "env-token", bare.APIKey)
}

func TestUnrestrictedOverrideUsesTokenFromEachRequest(t *testing.T) {
	t.Setenv(grafanaServiceAccountTokenEnvVar, "configured-secret")
	for _, token := range []string{"instance-one-token", "instance-two-token"} {
		t.Run(token, func(t *testing.T) {
			target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, "Bearer "+token, r.Header.Get("Authorization"))
				w.WriteHeader(http.StatusNoContent)
			}))
			defer target.Close()
			incoming := httptest.NewRequest(http.MethodPost, "/mcp", nil)
			incoming.Header.Set(grafanaURLHeader, target.URL)
			incoming.Header.Set(grafanaServiceAccountTokenHeader, token)
			w := httptest.NewRecorder()
			GrafanaURLOverrideMiddleware(true, nil, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				ctx := ExtractGrafanaInfoFromHeaders(r.Context(), r)
				cfg := GrafanaConfigFromContext(ctx)
				transport, err := BuildTransport(&cfg, nil)
				require.NoError(t, err)
				out, err := http.NewRequestWithContext(ctx, http.MethodGet, cfg.URL+"/api/health", nil)
				require.NoError(t, err)
				resp, err := (&http.Client{Transport: transport}).Do(out)
				require.NoError(t, err)
				_ = resp.Body.Close()
				w.WriteHeader(resp.StatusCode)
			})).ServeHTTP(w, incoming)
			assert.Equal(t, http.StatusNoContent, w.Code)
		})
	}
}

func TestGrafanaURLOverrideBlocksRedirects(t *testing.T) {
	var secondaryCalls int
	secondary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		secondaryCalls++
		w.WriteHeader(http.StatusNoContent)
	}))
	defer secondary.Close()
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Location", secondary.URL+"/stolen")
		w.WriteHeader(http.StatusFound)
	}))
	defer primary.Close()
	cfg := GrafanaConfig{URL: primary.URL, OverrideURL: primary.URL, APIKey: "request-token"}
	transport, err := BuildTransport(&cfg, nil)
	require.NoError(t, err)
	resp, err := (&http.Client{Transport: transport}).Get(primary.URL + "/api/health")
	require.ErrorContains(t, err, "blocked request outside selected target")
	if resp != nil {
		_ = resp.Body.Close()
	}
	assert.Zero(t, secondaryCalls)
}

func TestParseGrafanaURLOverrides(t *testing.T) {
	allowed, err := ParseGrafanaURLOverrides("https://one.example.com, https://two.example.com/grafana/")
	require.NoError(t, err)
	assert.Equal(t, []string{"https://one.example.com", "https://two.example.com/grafana"}, allowed)
	for _, input := range []string{"*", "http://user:password@host", "https://host/path?x=y", "https://host/#fragment", "file:///etc/passwd", "https://host,", "https://host/grafana/../admin", "https://host/grafana/%2e%2e/admin"} {
		_, err := ParseGrafanaURLOverrides(input)
		assert.Error(t, err, input)
	}
	// Direct requests cannot escape an allowed Grafana subpath.
	transport := &grafanaTargetTransport{baseURL: "https://example.com/grafana", next: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusNoContent, Body: io.NopCloser(strings.NewReader(""))}, nil
	})}
	for _, target := range []string{
		"https://example.com/other",
		"https://example.com/grafana/../admin",
		"https://example.com/grafana/%2e%2e/admin",
		"https://example.com/grafana/%252e%252e/admin",
		"https://example.com/grafana//../admin",
		"https://example.com/grafana/%5c..%5cadmin",
	} {
		req := httptest.NewRequest(http.MethodGet, target, nil)
		_, err = transport.RoundTrip(req)
		require.ErrorContains(t, err, "blocked request outside selected target", target)
	}
	req := httptest.NewRequest(http.MethodGet, "https://example.com/grafana/api/health", nil)
	_, err = transport.RoundTrip(req)
	require.NoError(t, err)

	// A host-root target has no subpath boundary and can retain ordinary
	// percent-encoded path segments.
	rootTransport := &grafanaTargetTransport{baseURL: "https://example.com", next: transport.next}
	req = httptest.NewRequest(http.MethodGet, "https://example.com/api/items/100%25", nil)
	_, err = rootTransport.RoundTrip(req)
	require.NoError(t, err)
}

func TestClientCacheSeparatesCredentialsAndOverrideMode(t *testing.T) {
	a := clientCacheKey{url: "https://grafana.example.com", apiKey: "first"}
	b := clientCacheKey{url: "https://grafana.example.com", apiKey: "second"}
	c := clientCacheKey{url: "https://grafana.example.com", apiKey: "first", overrideURL: "https://grafana.example.com"}
	assert.Equal(t, a.String(), b.String()) // redacted display values
	assert.NotEqual(t, a.singleflightKey(), b.singleflightKey())
	assert.NotEqual(t, a.singleflightKey(), c.singleflightKey())
}

func TestClientCacheSeparatesForwardedHeaderValues(t *testing.T) {
	t.Setenv(grafanaForwardHeadersEnvVar, "A,B")
	first := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	first.Header.Set("A", "x,B=y")
	second := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	second.Header.Set("A", "x")
	second.Header.Set("B", "y")

	a := cacheKeyFromRequest("https://grafana.example.com", "same-token", nil, 0, first)
	b := cacheKeyFromRequest("https://grafana.example.com", "same-token", nil, 0, second)
	assert.NotEqual(t, a, b)
	assert.NotEqual(t, a.singleflightKey(), b.singleflightKey())
}

func TestCachedClientSeparatesDefaultAndOverrideAtSameURL(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/frontend/settings" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{}`)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	t.Setenv(grafanaURLEnvVar, server.URL)
	t.Setenv(grafanaServiceAccountTokenEnvVar, "")
	cache := NewClientCache(nil)
	defer cache.Close()
	makeContext := func(r *http.Request) context.Context {
		ctx := ExtractGrafanaInfoFromHeaders(WithGrafanaConfig(r.Context(), GrafanaConfig{}), r)
		return extractGrafanaClientCached(cache)(ctx, r)
	}

	ordinary := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	ordinary.Header.Set(grafanaServiceAccountTokenHeader, "same-token")
	ordinaryCtx := makeContext(ordinary)
	require.NotNil(t, GrafanaClientFromContext(ordinaryCtx))

	override := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	override.Header.Set(grafanaURLHeader, server.URL)
	override.Header.Set(grafanaServiceAccountTokenHeader, "same-token")
	w := httptest.NewRecorder()
	GrafanaURLOverrideMiddleware(true, []string{server.URL}, http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		ctx := makeContext(r)
		require.NotNil(t, GrafanaClientFromContext(ctx))
		assert.Equal(t, server.URL, GrafanaConfigFromContext(ctx).OverrideURL)
	})).ServeHTTP(w, override)
	assert.Equal(t, http.StatusOK, w.Code)
	grafana, _, _ := cache.Size()
	assert.Equal(t, 2, grafana)
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type headerRoundTripper struct {
	header http.Header
	next   http.RoundTripper
}

func (h headerRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	for k, vs := range h.header {
		req.Header[k] = vs
	}
	return h.next.RoundTrip(req)
}

// The override is authorized at the HTTP layer and handed on through the
// request context, while the Grafana config is built later, per call, by
// GrafanaContextMiddleware. The selected URL has to survive that hop.
func TestGrafanaURLOverrideReachesToolCallsOverStreamableHTTP(t *testing.T) {
	t.Setenv("GRAFANA_URL", "https://default.example.com")
	t.Setenv("GRAFANA_SERVICE_ACCOUNT_TOKEN", "env-token")

	s := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0"}, nil)
	mcp.AddTool(s, &mcp.Tool{Name: "whoami"}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
		cfg := GrafanaConfigFromContext(ctx)
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: cfg.URL + " " + cfg.APIKey}}}, nil, nil
	})
	s.AddReceivingMiddleware(GrafanaContextMiddleware(ExtractGrafanaInfoFromHeaders))
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return s }, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true})
	ts := httptest.NewServer(GrafanaURLOverrideMiddleware(true, nil, handler))
	t.Cleanup(ts.Close)

	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil)
	session, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{
		Endpoint: ts.URL,
		HTTPClient: &http.Client{Transport: headerRoundTripper{
			header: http.Header{
				grafanaURLHeader:                  {"https://selected.example.com"},
				"X-Grafana-Service-Account-Token": {"caller-token"},
			},
			next: http.DefaultTransport,
		}},
	}, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Close() })

	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "whoami"})
	require.NoError(t, err)
	require.Len(t, res.Content, 1)
	assert.Equal(t, "https://selected.example.com caller-token", res.Content[0].(*mcp.TextContent).Text)
}
