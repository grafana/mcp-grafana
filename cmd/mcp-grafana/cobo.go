package main

// Cobo-specific wiring, kept in its own file so upstream syncs touch main.go
// only at a few hook points (see "Cobo:" comments in main.go).

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	mcpgrafana "github.com/grafana/mcp-grafana/v2"
)

const (
	coboAllowedHostsEnvVar   = "MCP_GRAFANA_ALLOWED_HOSTS"
	coboAllowedOriginsEnvVar = "MCP_GRAFANA_ALLOWED_ORIGINS"
)

func loadDotEnv(path string) error {
	file, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	defer file.Close()
	return loadEnvReader(file)
}

func loadDotEnvFiles(paths ...string) error {
	seen := make(map[string]struct{}, len(paths))
	for _, path := range paths {
		if path == "" {
			continue
		}
		absPath, err := filepath.Abs(path)
		if err == nil {
			path = absPath
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		if err := loadDotEnv(path); err != nil {
			return err
		}
	}
	return nil
}

// defaultDotEnvPaths are the cwd .env and the .env next to the binary
// (fabistrano copies the deployment .env into the release directory).
func defaultDotEnvPaths() []string {
	paths := []string{".env"}
	if executable, err := os.Executable(); err == nil {
		paths = append(paths, filepath.Join(filepath.Dir(executable), ".env"))
	}
	return paths
}

func loadEnvReader(r io.Reader) error {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		key = strings.TrimPrefix(key, "export ")
		if key == "" {
			continue
		}
		if _, exists := os.LookupEnv(key); exists {
			continue
		}
		value = strings.TrimSpace(value)
		value = strings.Trim(value, `"'`)
		if err := os.Setenv(key, value); err != nil {
			return err
		}
	}
	return scanner.Err()
}

// applyCoboHTTPSecurityEnv lets the deployment .env supply --allowed-hosts /
// --allowed-origins, so the prod command line does not have to change.
// Explicit flags win.
func applyCoboHTTPSecurityEnv(hsc *httpSecurityConfig) {
	if strings.TrimSpace(hsc.allowedHosts) == "" {
		hsc.allowedHosts = os.Getenv(coboAllowedHostsEnvVar)
	}
	if strings.TrimSpace(hsc.allowedOrigins) == "" {
		hsc.allowedOrigins = os.Getenv(coboAllowedOriginsEnvVar)
	}
}

// coboAuthConfigFromEnv builds inbound auth config for an MCP entry path.
// discoveryPaths are the OAuth metadata paths that must stay public.
func coboAuthConfigFromEnv(resourcePath string, discoveryPaths []string) mcpgrafana.CoboAuthConfig {
	// Authorization server metadata is served by auth-ai, not here; keep the legacy
	// well-known paths public so they answer 404 instead of a 401 challenge.
	exempt := append([]string{"/healthz", "/metrics", "/.well-known/oauth-authorization-server", "/.well-known/openid-configuration"}, discoveryPaths...)
	return mcpgrafana.CoboAuthConfig{
		Enabled:      strings.EqualFold(strings.TrimSpace(os.Getenv("COBO_AUTH_ENABLED")), "true") || strings.EqualFold(strings.TrimSpace(os.Getenv("GRAFANA_AUTH_MODE")), "jwt"),
		JWTSecret:    os.Getenv("COBO_AUTH_JWT_SECRET"),
		JWTAudience:  os.Getenv("COBO_AUTH_JWT_AUDIENCE"),
		ExemptPaths:  exempt,
		Realm:        "Grafana MCP",
		ResourcePath: resourcePath,
	}
}

// coboAuthEnabled reports whether Cobo OAuth JWT auth is configured.
func coboAuthEnabled() bool {
	return coboAuthConfigFromEnv("", nil).Enabled
}

// checkCoboAuthPolicy rejects running Cobo JWT auth together with upstream's
// static --server-auth-token: both claim the Authorization header.
func checkCoboAuthPolicy(callerToken string) error {
	if coboAuthEnabled() && callerToken != "" {
		return fmt.Errorf("refusing to start: Cobo JWT auth (COBO_AUTH_ENABLED / GRAFANA_AUTH_MODE=jwt) and --server-auth-token (%s) both use the Authorization header; configure only one", serverAuthTokenEnvVar)
	}
	return nil
}

// withCoboAuth mounts OAuth protected resource metadata for resourcePath on mux
// and wraps it with Cobo JWT validation. With Cobo auth disabled it only adds
// the metadata routes (when an OAuth server is configured) and returns mux as is.
//
// For SSE (sse=true) sessions are additionally pinned to the user that opened
// them; see mcpgrafana.CoboSSESessionBinder.
func withCoboAuth(mux *http.ServeMux, resourcePath string, sse bool) http.Handler {
	discoveryPaths := mcpgrafana.RegisterOAuthDiscoveryRoutes(mux, mcpgrafana.OAuthMetadataConfigFromEnv(), resourcePath)
	var inner http.Handler = mux
	if sse {
		inner = mcpgrafana.CoboSSESessionBinder(resourcePath, mux)
	}
	return mcpgrafana.CoboAuthMiddleware(coboAuthConfigFromEnv(resourcePath, discoveryPaths), inner)
}
