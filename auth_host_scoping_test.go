package mcpgrafana

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// grafanaHostRecordingTransport records the credential seen for each request
// host, so a test can assert what the credential would be attached to without a
// live network.
type grafanaHostRecordingTransport struct {
	credByHost map[string]string
}

func (h *grafanaHostRecordingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	cred := req.Header.Get("Authorization")
	if cred == "" {
		cred = req.Header.Get("X-Access-Token")
	}
	h.credByHost[req.URL.Hostname()] = cred
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       http.NoBody,
		Header:     make(http.Header),
		Request:    req,
	}, nil
}

// TestAuthRoundTripper_WithholdsCredentialsOffBoundHost verifies that the
// credential is attached to a request on the configured Grafana host but
// withheld from any other host. A redirect off the bound instance arrives here
// as a RoundTrip to a different host, so this is the property that keeps the
// credential from following such a redirect.
func TestAuthRoundTripper_WithholdsCredentialsOffBoundHost(t *testing.T) {
	rec := &grafanaHostRecordingTransport{credByHost: map[string]string{}}
	rt := NewAuthRoundTripper(rec, "", "", "secret-api-key", nil)

	ctx := WithGrafanaConfig(context.Background(), GrafanaConfig{
		URL:    "https://grafana.example.com",
		APIKey: "secret-api-key",
	})

	for _, target := range []string{
		"https://grafana.example.com/api/dashboards", // bound host
		"https://attacker.example.com/steal",         // redirect target, different host
	} {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
		require.NoError(t, err)
		resp, err := rt.RoundTrip(req)
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()
	}

	assert.Equal(t, "Bearer secret-api-key", rec.credByHost["grafana.example.com"],
		"credential must reach the bound host")
	assert.Empty(t, rec.credByHost["attacker.example.com"],
		"credential must not follow a redirect to another host")
}

// TestAuthRoundTripper_EmptyBoundURLPreservesBehavior verifies the
// backward-compatible default: with no configured Grafana URL, the credential
// is attached as before this change.
func TestAuthRoundTripper_EmptyBoundURLPreservesBehavior(t *testing.T) {
	rec := &grafanaHostRecordingTransport{credByHost: map[string]string{}}
	rt := NewAuthRoundTripper(rec, "", "", "secret-api-key", nil)

	// No GrafanaConfig in context, so cfg.URL is empty and the credential
	// attaches as before.
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://anywhere.example.com/x", nil)
	require.NoError(t, err)
	resp, err := rt.RoundTrip(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	assert.Equal(t, "Bearer secret-api-key", rec.credByHost["anywhere.example.com"],
		"with no bound URL, credential attaches as before")
}

// TestAuthRoundTripper_BindsWhenContextHasNoConfig verifies that a transport
// built for a specific Grafana instance keeps its construction-time credential
// bound to that instance even when a request carries no GrafanaConfig in its
// context. Without the fallback binding the empty context URL matched every
// host, so construction-time credentials followed a redirect anywhere.
func TestAuthRoundTripper_BindsWhenContextHasNoConfig(t *testing.T) {
	rec := &grafanaHostRecordingTransport{credByHost: map[string]string{}}
	rt := NewAuthRoundTripper(rec, "", "", "secret-api-key", nil)
	rt.boundURL = "https://grafana.example.com"

	// No GrafanaConfig in context at all.
	ctx := context.Background()

	for _, target := range []string{
		"https://grafana.example.com/api/health",
		"https://attacker.example.net/collect",
	} {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
		require.NoError(t, err)
		_, err = rt.RoundTrip(req)
		require.NoError(t, err)
	}

	assert.Equal(t, "Bearer secret-api-key", rec.credByHost["grafana.example.com"],
		"credential should be attached on the bound host")
	assert.Empty(t, rec.credByHost["attacker.example.net"],
		"credential must not be attached off the bound host when context has no config")
}

// TestAuthRoundTripper_UnboundTransportPreservesPriorBehavior verifies the
// genuinely unconfigured case still works: with neither a context URL nor a
// construction-time URL there is nothing to bind to, so behavior is unchanged.
func TestAuthRoundTripper_UnboundTransportPreservesPriorBehavior(t *testing.T) {
	rec := &grafanaHostRecordingTransport{credByHost: map[string]string{}}
	rt := NewAuthRoundTripper(rec, "", "", "secret-api-key", nil)

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet,
		"https://anywhere.example.org/api", nil)
	require.NoError(t, err)
	_, err = rt.RoundTrip(req)
	require.NoError(t, err)

	assert.Equal(t, "Bearer secret-api-key", rec.credByHost["anywhere.example.org"],
		"with no binding configured at all, prior behavior is preserved")
}
