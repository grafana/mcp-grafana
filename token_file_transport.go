package mcpgrafana

import (
	"context"
	"fmt"
	"net/http"
	"net/textproto"
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

// tokenFileAuthContextKey marks a request whose credentials must not be
// replaced by the static auth middleware or extra headers.
type tokenFileAuthContextKey struct{}

func markTokenFileAuth(req *http.Request) *http.Request {
	return req.WithContext(context.WithValue(req.Context(), tokenFileAuthContextKey{}, true))
}

func tokenFileAuthIsProtected(ctx context.Context) bool {
	protected, _ := ctx.Value(tokenFileAuthContextKey{}).(bool)
	return protected
}

func isTokenFileCredentialHeader(name string) bool {
	canonical := textproto.CanonicalMIMEHeaderKey(name)
	return canonical == textproto.CanonicalMIMEHeaderKey("Authorization") ||
		canonical == textproto.CanonicalMIMEHeaderKey(grafanaServiceAccountTokenHeader) ||
		canonical == textproto.CanonicalMIMEHeaderKey(grafanaAPIKeyHeader)
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
	authorization := clonedReq.Header.Get("Authorization")
	if clonedReq.Header.Get(grafanaServiceAccountTokenHeader) != "" ||
		clonedReq.Header.Get(grafanaAPIKeyHeader) != "" {
		if authorization == "Bearer "+strings.TrimSpace(t.fallbackToken) {
			clonedReq.Header.Del("Authorization")
		}
		return t.underlying.RoundTrip(markTokenFileAuth(clonedReq))
	}

	token, err := os.ReadFile(t.path)
	if err != nil {
		if t.fallbackToken != "" {
			return t.underlying.RoundTrip(markTokenFileAuth(clonedReq))
		}
		return nil, fmt.Errorf("read Grafana service account token file: %w", err)
	}

	// The OpenAPI runtime adds the configured API key before invoking the
	// transport. That value is the fallbackToken captured at client creation;
	// replace it with the current file contents so rotation is observable. Any
	// other Authorization value is request-scoped and must win over the file.
	if authorization != "" &&
		(t.fallbackToken == "" || authorization != "Bearer "+strings.TrimSpace(t.fallbackToken)) {
		return t.underlying.RoundTrip(markTokenFileAuth(clonedReq))
	}
	clonedReq.Header.Del("Authorization")
	tokenValue := strings.TrimSpace(string(token))
	if tokenValue != "" {
		clonedReq.Header.Set("Authorization", "Bearer "+tokenValue)
	}
	return t.underlying.RoundTrip(markTokenFileAuth(clonedReq))
}
