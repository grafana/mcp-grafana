package mcpgrafana

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCoboAuthMiddlewareRequiresValidHS256BearerToken(t *testing.T) {
	secret := "test-secret"
	nextCalled := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nextCalled = true
		identity, ok := CoboIdentityFromContext(r.Context())
		require.True(t, ok)
		assert.Equal(t, "alice@example.com", identity.Email)
		assert.Equal(t, "Alice", identity.Name)
		assert.Equal(t, "s1", identity.SessionID)
		w.WriteHeader(http.StatusNoContent)
	})
	handler := CoboAuthMiddleware(CoboAuthConfig{Enabled: true, JWTSecret: secret}, next)

	req := httptest.NewRequest(http.MethodGet, "/mcp", nil)
	req.Header.Set("Authorization", "Bearer "+mustSignCoboJWT(t, secret, "alice@example.com", "Alice", "s1", time.Now().Add(time.Minute)))
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	assert.True(t, nextCalled)
	assert.Equal(t, http.StatusNoContent, rr.Code)
}

func TestCoboAuthMiddlewareRejectsMissingAndInvalidTokens(t *testing.T) {
	handler := CoboAuthMiddleware(CoboAuthConfig{Enabled: true, JWTSecret: "test-secret"}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("next handler should not be called")
	}))

	for _, tc := range []struct {
		name          string
		authorization string
	}{
		{name: "missing"},
		{name: "malformed", authorization: "not-bearer"},
		{name: "bad signature", authorization: "Bearer " + mustSignCoboJWT(t, "other-secret", "alice@example.com", "Alice", "s1", time.Now().Add(time.Minute))},
		{name: "expired", authorization: "Bearer " + mustSignCoboJWT(t, "test-secret", "alice@example.com", "Alice", "s1", time.Now().Add(-time.Minute))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "https://mcp.example.com/mcp", nil)
			if tc.authorization != "" {
				req.Header.Set("Authorization", tc.authorization)
			}
			rr := httptest.NewRecorder()

			handler.ServeHTTP(rr, req)

			assert.Equal(t, http.StatusUnauthorized, rr.Code)
			assert.Contains(t, rr.Header().Get("WWW-Authenticate"), `Bearer realm="Grafana MCP"`)
			assert.Contains(t, rr.Header().Get("WWW-Authenticate"), `error="invalid_token"`)
			assert.Contains(t, rr.Header().Get("WWW-Authenticate"), `resource_metadata="https://mcp.example.com/.well-known/oauth-protected-resource"`)
		})
	}
}

func TestCoboAuthMiddlewareBypassesExemptPathsAndOptions(t *testing.T) {
	called := 0
	handler := CoboAuthMiddleware(CoboAuthConfig{
		Enabled:     true,
		JWTSecret:   "test-secret",
		ExemptPaths: []string{"/healthz", "/metrics"},
	}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called++
		w.WriteHeader(http.StatusNoContent)
	}))

	for _, tc := range []struct {
		name   string
		method string
		path   string
	}{
		{name: "health", method: http.MethodGet, path: "/healthz"},
		{name: "metrics", method: http.MethodGet, path: "/metrics"},
		{name: "options", method: http.MethodOptions, path: "/mcp"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, nil)
			rr := httptest.NewRecorder()

			handler.ServeHTTP(rr, req)

			assert.Equal(t, http.StatusNoContent, rr.Code)
		})
	}
	assert.Equal(t, 3, called)
}

func TestCoboAuthMiddlewareBypassesOAuthMetadataPaths(t *testing.T) {
	called := 0
	handler := CoboAuthMiddleware(CoboAuthConfig{
		Enabled: true,
		ExemptPaths: []string{
			"/.well-known/oauth-protected-resource",
			"/.well-known/oauth-authorization-server",
			"/.well-known/openid-configuration",
		},
	}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called++
		w.WriteHeader(http.StatusNoContent)
	}))

	for _, path := range []string{
		"/.well-known/oauth-protected-resource",
		"/.well-known/oauth-authorization-server",
		"/.well-known/openid-configuration",
	} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, req)

		assert.Equal(t, http.StatusNoContent, rr.Code)
	}
	assert.Equal(t, 3, called)
}

func TestOAuthProtectedResourceMetadataHandlerReturnsFullResourceURL(t *testing.T) {
	handler := OAuthProtectedResourceMetadataHandler(OAuthMetadataConfig{
		ServerBaseURL: "https://oauth.example.com",
		Scopes:        []string{"profile", "email", "openid"},
	}, "/sse")
	req := httptest.NewRequest(http.MethodGet, "http://internal/.well-known/oauth-protected-resource/sse", nil)
	req.Host = "mcp.example.com"
	req.Header.Set("X-Forwarded-Proto", "https")
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, "application/json", rr.Header().Get("Content-Type"))
	var body map[string]any
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &body))
	assert.Equal(t, "https://mcp.example.com/sse", body["resource"])
	assert.ElementsMatch(t, []any{"https://oauth.example.com"}, body["authorization_servers"].([]any))
	assert.ElementsMatch(t, []any{"profile", "email", "openid"}, body["scopes_supported"].([]any))
}

func TestProtectedResourceMetadataPath(t *testing.T) {
	for in, want := range map[string]string{
		"":          "/.well-known/oauth-protected-resource",
		"/":         "/.well-known/oauth-protected-resource",
		"/sse":      "/.well-known/oauth-protected-resource/sse",
		"mcp":       "/.well-known/oauth-protected-resource/mcp",
		"/base/sse": "/.well-known/oauth-protected-resource/base/sse",
		"/mcp/":     "/.well-known/oauth-protected-resource/mcp",
	} {
		assert.Equal(t, want, ProtectedResourceMetadataPath(in), "input %q", in)
	}
}

// newDiscoveryTestServer mirrors cmd/mcp-grafana wiring: discovery routes plus a
// protected MCP entry behind CoboAuthMiddleware.
func newDiscoveryTestServer(t *testing.T, resourcePath string) http.Handler {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc(resourcePath, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	paths := RegisterOAuthDiscoveryRoutes(mux, OAuthMetadataConfig{ServerBaseURL: "https://auth-ai.example.com"}, resourcePath)
	return CoboAuthMiddleware(CoboAuthConfig{
		Enabled:      true,
		JWTSecret:    "test-secret",
		ExemptPaths:  append([]string{"/.well-known/oauth-authorization-server", "/.well-known/openid-configuration"}, paths...),
		ResourcePath: resourcePath,
	}, mux)
}

func TestOAuthDiscoveryRootAndSuffixedPRMArePublic(t *testing.T) {
	for _, resourcePath := range []string{"/sse", "/mcp"} {
		handler := newDiscoveryTestServer(t, resourcePath)
		for _, path := range []string{
			"/.well-known/oauth-protected-resource",
			"/.well-known/oauth-protected-resource" + resourcePath,
		} {
			t.Run(resourcePath+" "+path, func(t *testing.T) {
				req := httptest.NewRequest(http.MethodGet, path, nil)
				req.Host = "mcp.example.com"
				req.Header.Set("X-Forwarded-Proto", "https")
				rr := httptest.NewRecorder()

				handler.ServeHTTP(rr, req)

				require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
				var body map[string]any
				require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &body))
				assert.Equal(t, "https://mcp.example.com"+resourcePath, body["resource"])
				assert.Equal(t, []any{"https://auth-ai.example.com"}, body["authorization_servers"])
			})
		}
	}
}

func TestOAuthDiscoveryDoesNotServeAuthorizationServerMetadata(t *testing.T) {
	handler := newDiscoveryTestServer(t, "/sse")
	for _, path := range []string{"/.well-known/oauth-authorization-server", "/.well-known/openid-configuration"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, req)

		assert.Equal(t, http.StatusNotFound, rr.Code, path)
		assert.Empty(t, rr.Header().Get("WWW-Authenticate"), path)
	}
}

func TestCoboAuthMiddlewareChallengePointsToSuffixedPRM(t *testing.T) {
	for _, resourcePath := range []string{"/sse", "/mcp"} {
		handler := newDiscoveryTestServer(t, resourcePath)
		req := httptest.NewRequest(http.MethodGet, resourcePath, nil)
		req.Host = "mcp.example.com"
		req.Header.Set("X-Forwarded-Proto", "https")
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, req)

		require.Equal(t, http.StatusUnauthorized, rr.Code)
		assert.Contains(t, rr.Header().Get("WWW-Authenticate"),
			`resource_metadata="https://mcp.example.com/.well-known/oauth-protected-resource`+resourcePath+`"`)
	}
}

func TestParseCoboIdentityAudience(t *testing.T) {
	secret := "test-secret"
	sign := func(aud any) string {
		claims := jwt.MapClaims{"sub": "alice@example.com", "exp": time.Now().Add(time.Minute).Unix()}
		if aud != nil {
			claims["aud"] = aud
		}
		token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(secret))
		require.NoError(t, err)
		return token
	}

	_, err := ParseCoboIdentity(sign("anything"), secret)
	assert.NoError(t, err, "no audience configured keeps previous behaviour")
	_, err = ParseCoboIdentity(sign(nil), secret)
	assert.NoError(t, err)

	_, err = ParseCoboIdentityWithAudience(sign("https://mcp.example.com/sse"), secret, "https://mcp.example.com/sse")
	assert.NoError(t, err)
	_, err = ParseCoboIdentityWithAudience(sign([]string{"other", "https://mcp.example.com/sse"}), secret, "https://mcp.example.com/sse")
	assert.NoError(t, err)
	_, err = ParseCoboIdentityWithAudience(sign("https://other.example.com/sse"), secret, "https://mcp.example.com/sse")
	assert.Error(t, err)
	_, err = ParseCoboIdentityWithAudience(sign(nil), secret, "https://mcp.example.com/sse")
	assert.Error(t, err)
}

func TestParseCoboIdentityRejectsNonHS256(t *testing.T) {
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS512, jwt.MapClaims{
		"sub": "alice@example.com", "exp": time.Now().Add(time.Minute).Unix(),
	}).SignedString([]byte("test-secret"))
	require.NoError(t, err)
	_, err = ParseCoboIdentity(token, "test-secret")
	assert.Error(t, err)
}

func TestGrafanaJWTIssuerPreservesPythonPayloadContract(t *testing.T) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	cfg := GrafanaJWTConfig{
		PrivateKey: privateKey,
		KeyID:      "grafana-jwt-key-1",
		Issuer:     "grafana_mcp_server",
		Audience:   "grafana",
		TTL:        time.Minute,
	}

	token, err := IssueGrafanaJWT(CoboIdentity{Email: "bob@example.com", Name: "Bob"}, cfg)
	require.NoError(t, err)

	headers, err := jwt.Parse(token, func(token *jwt.Token) (any, error) {
		require.Equal(t, "grafana-jwt-key-1", token.Header["kid"])
		require.Equal(t, jwt.SigningMethodRS256, token.Method)
		return &privateKey.PublicKey, nil
	}, jwt.WithAudience("grafana"), jwt.WithIssuer("grafana_mcp_server"))
	require.NoError(t, err)
	require.True(t, headers.Valid)

	claims := headers.Claims.(jwt.MapClaims)
	assert.Equal(t, "bob@example.com", claims["sub"])
	assert.Equal(t, "bob@example.com", claims["email"])
	assert.Equal(t, "Bob", claims["user"])
	assert.NotZero(t, claims["iat"])
	assert.NotZero(t, claims["exp"])
}

func TestGrafanaJWTRoundTripperInjectsHeaderAndSkipsServiceAccount(t *testing.T) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	var gotAuthorization string
	var gotGrafanaJWT string
	rt, err := BuildTransport(&GrafanaConfig{
		APIKey: "service-account-token",
		GrafanaJWT: GrafanaJWTConfig{
			Enabled:    true,
			PrivateKey: privateKey,
			KeyID:      "grafana-jwt-key-1",
			Issuer:     "grafana_mcp_server",
			Audience:   "grafana",
			TTL:        time.Minute,
		},
	}, roundTripFunc(func(req *http.Request) (*http.Response, error) {
		gotAuthorization = req.Header.Get("Authorization")
		gotGrafanaJWT = req.Header.Get("X-Cobo-JWT")
		return &http.Response{StatusCode: http.StatusNoContent, Body: http.NoBody, Request: req}, nil
	}), WithoutOtel(), WithoutUserAgent())
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodGet, "http://grafana.example/api/search", nil)
	req = req.WithContext(WithCoboIdentity(req.Context(), CoboIdentity{Email: "carol@example.com", Name: "Carol"}))

	resp, err := rt.RoundTrip(req)

	require.NoError(t, err)
	assert.Equal(t, http.StatusNoContent, resp.StatusCode)
	assert.Empty(t, gotAuthorization)
	require.NotEmpty(t, gotGrafanaJWT)

	parsed, err := jwt.Parse(gotGrafanaJWT, func(token *jwt.Token) (any, error) {
		return &privateKey.PublicKey, nil
	}, jwt.WithAudience("grafana"), jwt.WithIssuer("grafana_mcp_server"))
	require.NoError(t, err)
	assert.True(t, parsed.Valid)
	assert.Equal(t, "carol@example.com", parsed.Claims.(jwt.MapClaims)["email"])
}

func TestGrafanaJWTConfigFromEnvAcceptsEscapedPEMNewlines(t *testing.T) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	pemBytes := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(privateKey),
	})
	t.Setenv("GRAFANA_AUTH_MODE", "jwt")
	t.Setenv("GRAFANA_JWT_RSA_PRIVATE_KEY", strings.ReplaceAll(string(pemBytes), "\n", `\n`))

	cfg := grafanaJWTConfigFromEnv(slog.New(slog.NewTextHandler(io.Discard, nil)))

	require.True(t, cfg.Enabled)
	require.NotNil(t, cfg.PrivateKey)
	assert.Equal(t, privateKey.N, cfg.PrivateKey.N)
}

func TestPropagateCoboIdentityFromRequestContext(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/mcp", nil)
	req = req.WithContext(WithCoboIdentity(req.Context(), CoboIdentity{Email: "dave@example.com"}))

	ctx := PropagateCoboIdentityFromRequest(context.Background(), req)

	identity, ok := CoboIdentityFromContext(ctx)
	require.True(t, ok)
	assert.Equal(t, "dave@example.com", identity.Email)
}

func mustSignCoboJWT(t *testing.T, secret, email, name, sessionID string, exp time.Time) string {
	t.Helper()
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub":        email,
		"exp":        exp.Unix(),
		"session_id": sessionID,
		"user_info": map[string]any{
			"email": email,
			"name":  name,
		},
	}).SignedString([]byte(secret))
	require.NoError(t, err)
	return token
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestCoboSSESessionBinderRejectsOtherUsersPOST(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte("event: endpoint\ndata: /sse?sessionid=ABC123\n\n"))
			<-r.Context().Done()
			return
		}
		w.WriteHeader(http.StatusAccepted)
	})
	handler := CoboSSESessionBinder("/sse", inner)
	as := func(r *http.Request, email string) *http.Request {
		return r.WithContext(WithCoboIdentity(r.Context(), CoboIdentity{Email: email}))
	}

	ctx, cancel := context.WithCancel(context.Background())
	getDone := make(chan struct{})
	go func() {
		defer close(getDone)
		req := httptest.NewRequest(http.MethodGet, "/sse", nil).WithContext(ctx)
		handler.ServeHTTP(httptest.NewRecorder(), as(req, "alice@example.com"))
	}()
	require.Eventually(t, func() bool {
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, as(httptest.NewRequest(http.MethodPost, "/sse?sessionid=ABC123", nil), "bob@example.com"))
		return rr.Code == http.StatusForbidden
	}, time.Second, 5*time.Millisecond, "bob must be rejected on alice's session")

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, as(httptest.NewRequest(http.MethodPost, "/sse?sessionid=ABC123", nil), "Alice@example.com"))
	assert.Equal(t, http.StatusAccepted, rr.Code, "owner may post")

	cancel()
	<-getDone
	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, as(httptest.NewRequest(http.MethodPost, "/sse?sessionid=ABC123", nil), "bob@example.com"))
	assert.Equal(t, http.StatusAccepted, rr.Code, "binding is dropped when the stream ends")

	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/sse?sessionid=ABC123", nil))
	assert.Equal(t, http.StatusAccepted, rr.Code, "no identity (auth disabled) passes through")
}
