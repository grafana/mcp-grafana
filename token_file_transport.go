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
	path          string
	fallbackToken string
	underlying    http.RoundTripper
}

func serviceAccountTokenFileFromEnv() string {
	if os.Getenv(grafanaServiceAccountTokenEnvVar) != "" {
		return ""
	}
	return os.Getenv(grafanaServiceAccountTokenFileEnvVar)
}

func tokenFileMatchesAPIKey(path, apiKey string) bool {
	if apiKey == "" {
		return true
	}
	token, err := os.ReadFile(path)
	return err == nil && strings.TrimSpace(string(token)) == strings.TrimSpace(apiKey)
}

func (t *tokenFileRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	clonedReq := req.Clone(req.Context())
	token, err := os.ReadFile(t.path)
	if err != nil {
		if t.fallbackToken != "" {
			return t.underlying.RoundTrip(clonedReq)
		}
		return nil, fmt.Errorf("read Grafana service account token file: %w", err)
	}

	if authorization := clonedReq.Header.Get("Authorization"); authorization != "" &&
		(t.fallbackToken == "" || authorization != "Bearer "+strings.TrimSpace(t.fallbackToken)) {
		return t.underlying.RoundTrip(clonedReq)
	}
	clonedReq.Header.Del("Authorization")
	if token := strings.TrimSpace(string(token)); token != "" {
		clonedReq.Header.Set("Authorization", "Bearer "+token)
	}
	return t.underlying.RoundTrip(clonedReq)
}
