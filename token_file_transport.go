package mcpgrafana

import (
	"fmt"
	"net/http"
	"os"
	"strings"
)

// tokenFileRoundTripper injects the current service-account token from disk into
// each request. This keeps long-lived stdio servers compatible with rotated
// token files without rebuilding the static stdio context.
type tokenFileRoundTripper struct {
	path       string
	underlying http.RoundTripper
}

func serviceAccountTokenFileFromEnv() string {
	if os.Getenv(grafanaServiceAccountTokenEnvVar) != "" || os.Getenv(grafanaAPIEnvVar) != "" {
		return ""
	}
	return os.Getenv(grafanaServiceAccountTokenFileEnvVar)
}

func (t *tokenFileRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	token, err := os.ReadFile(t.path)
	if err != nil {
		return nil, fmt.Errorf("read Grafana service account token file: %w", err)
	}

	clonedReq := req.Clone(req.Context())
	clonedReq.Header.Del("Authorization")
	if token := strings.TrimSpace(string(token)); token != "" {
		clonedReq.Header.Set("Authorization", "Bearer "+token)
	}
	return t.underlying.RoundTrip(clonedReq)
}
