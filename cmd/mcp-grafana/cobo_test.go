package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadDotEnvDoesNotOverrideExistingEnv(t *testing.T) {
	dir := t.TempDir()
	envPath := filepath.Join(dir, ".env")
	require.NoError(t, os.WriteFile(envPath, []byte("DOTENV_A=from-file\nDOTENV_B=\"quoted value\"\nDOTENV_C='single quoted'\nexport DOTENV_D=exported\n# ignored\n"), 0o600))
	t.Setenv("DOTENV_A", "from-env")
	require.NoError(t, os.Unsetenv("DOTENV_B"))
	require.NoError(t, os.Unsetenv("DOTENV_C"))
	require.NoError(t, os.Unsetenv("DOTENV_D"))
	t.Cleanup(func() {
		_ = os.Unsetenv("DOTENV_B")
		_ = os.Unsetenv("DOTENV_C")
		_ = os.Unsetenv("DOTENV_D")
	})

	require.NoError(t, loadDotEnv(envPath))

	assert.Equal(t, "from-env", os.Getenv("DOTENV_A"))
	assert.Equal(t, "quoted value", os.Getenv("DOTENV_B"))
	assert.Equal(t, "single quoted", os.Getenv("DOTENV_C"))
	assert.Equal(t, "exported", os.Getenv("DOTENV_D"))
}

func TestLoadDotEnvIgnoresMissingFile(t *testing.T) {
	require.NoError(t, loadDotEnv(filepath.Join(t.TempDir(), ".env")))
}

func TestLoadDotEnvFilesLoadsFallbackPath(t *testing.T) {
	dir := t.TempDir()
	envPath := filepath.Join(dir, ".env")
	require.NoError(t, os.WriteFile(envPath, []byte("DOTENV_FALLBACK=loaded\n"), 0o600))
	require.NoError(t, os.Unsetenv("DOTENV_FALLBACK"))
	t.Cleanup(func() {
		_ = os.Unsetenv("DOTENV_FALLBACK")
	})

	require.NoError(t, loadDotEnvFiles(filepath.Join(t.TempDir(), ".env"), envPath))

	assert.Equal(t, "loaded", os.Getenv("DOTENV_FALLBACK"))
}

func TestCoboAuthConfigFromEnvExemptsDiscoveryPaths(t *testing.T) {
	t.Setenv("COBO_AUTH_JWT_AUDIENCE", "https://mcp.example.com/sse")
	cfg := coboAuthConfigFromEnv("/sse", []string{"/.well-known/oauth-protected-resource", "/.well-known/oauth-protected-resource/sse"})
	assert.Equal(t, "/sse", cfg.ResourcePath)
	assert.Equal(t, "https://mcp.example.com/sse", cfg.JWTAudience)
	for _, p := range []string{"/healthz", "/metrics", "/.well-known/oauth-protected-resource", "/.well-known/oauth-protected-resource/sse", "/.well-known/oauth-authorization-server", "/.well-known/openid-configuration"} {
		assert.Contains(t, cfg.ExemptPaths, p)
	}
}

func TestCheckCoboAuthPolicyRejectsStaticServerToken(t *testing.T) {
	t.Setenv("COBO_AUTH_ENABLED", "true")
	assert.Error(t, checkCoboAuthPolicy("static-token"))
	assert.NoError(t, checkCoboAuthPolicy(""))

	t.Setenv("COBO_AUTH_ENABLED", "false")
	t.Setenv("GRAFANA_AUTH_MODE", "")
	assert.NoError(t, checkCoboAuthPolicy("static-token"))
}

func TestApplyCoboHTTPSecurityEnv(t *testing.T) {
	t.Setenv(coboAllowedHostsEnvVar, "mcp.example.com,localhost:8001")
	t.Setenv(coboAllowedOriginsEnvVar, "https://app.example.com")

	hsc := httpSecurityConfig{}
	applyCoboHTTPSecurityEnv(&hsc)
	assert.Equal(t, "mcp.example.com,localhost:8001", hsc.allowedHosts)
	assert.Equal(t, "https://app.example.com", hsc.allowedOrigins)

	explicit := httpSecurityConfig{allowedHosts: "flag.example.com"}
	applyCoboHTTPSecurityEnv(&explicit)
	assert.Equal(t, "flag.example.com", explicit.allowedHosts, "flag wins over env")
}

// TestWithCoboAuthOnUpstreamSSEMux exercises the real upstream mux builder
// (newSSEMux / newStreamableHTTPMux) wrapped by withCoboAuth.
func TestWithCoboAuthOnUpstreamMux(t *testing.T) {
	t.Setenv("COBO_AUTH_ENABLED", "true")
	t.Setenv("COBO_AUTH_JWT_SECRET", "test-secret")
	t.Setenv("COBO_OAUTH_SERVER_BASE_URL", "https://auth-ai.example.com")

	mcp := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })
	for name, tc := range map[string]struct {
		handler      http.Handler
		resourcePath string
	}{
		"sse":             {withCoboAuth(newSSEMux(mcp, "", "", nil), sseEndpointPath(""), true), "/sse"},
		"sse base":        {withCoboAuth(newSSEMux(mcp, "/grafana", "", nil), sseEndpointPath("/grafana"), true), "/grafana/sse"},
		"streamable-http": {withCoboAuth(newStreamableHTTPMux(mcp, "/mcp", "", nil), "/mcp", false), "/mcp"},
	} {
		t.Run(name, func(t *testing.T) {
			for _, p := range []string{"/.well-known/oauth-protected-resource", "/.well-known/oauth-protected-resource" + tc.resourcePath} {
				req := httptest.NewRequest(http.MethodGet, p, nil)
				req.Host = "mcp.example.com"
				req.Header.Set("X-Forwarded-Proto", "https")
				rr := httptest.NewRecorder()
				tc.handler.ServeHTTP(rr, req)
				require.Equal(t, http.StatusOK, rr.Code, p)
				var body map[string]any
				require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &body))
				assert.Equal(t, "https://mcp.example.com"+tc.resourcePath, body["resource"], p)
			}

			req := httptest.NewRequest(http.MethodGet, tc.resourcePath, nil)
			req.Host = "mcp.example.com"
			req.Header.Set("X-Forwarded-Proto", "https")
			rr := httptest.NewRecorder()
			tc.handler.ServeHTTP(rr, req)
			require.Equal(t, http.StatusUnauthorized, rr.Code)
			assert.Contains(t, rr.Header().Get("WWW-Authenticate"),
				`resource_metadata="https://mcp.example.com/.well-known/oauth-protected-resource`+tc.resourcePath+`"`)

			req = httptest.NewRequest(http.MethodGet, "/.well-known/oauth-authorization-server", nil)
			rr = httptest.NewRecorder()
			tc.handler.ServeHTTP(rr, req)
			assert.Equal(t, http.StatusNotFound, rr.Code)
		})
	}
}
