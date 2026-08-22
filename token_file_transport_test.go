//go:build unit

package mcpgrafana

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/grafana/grafana-openapi-client-go/client/datasources"
	"github.com/stretchr/testify/require"
)

func TestTokenFileRoundTripperReadsRotatedTokenPerRequest(t *testing.T) {
	tokenFile := t.TempDir() + "/token"
	require.NoError(t, os.WriteFile(tokenFile, []byte("first-token\n"), 0o600))

	var received []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received = append(received, r.Header.Get("Authorization"))
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	transport := &tokenFileRoundTripper{path: tokenFile, underlying: http.DefaultTransport}
	client := &http.Client{Transport: transport}

	request, err := http.NewRequest(http.MethodGet, server.URL, nil)
	require.NoError(t, err)
	response, err := client.Do(request)
	require.NoError(t, err)
	_, _ = io.Copy(io.Discard, response.Body)
	_ = response.Body.Close()

	require.NoError(t, os.WriteFile(tokenFile, []byte("rotated-token\n"), 0o600))
	request, err = http.NewRequest(http.MethodGet, server.URL, nil)
	require.NoError(t, err)
	response, err = client.Do(request)
	require.NoError(t, err)
	_, _ = io.Copy(io.Discard, response.Body)
	_ = response.Body.Close()

	require.Equal(t, []string{"Bearer first-token", "Bearer rotated-token"}, received)
}

func TestNewGrafanaClientUsesRotatedTokenFile(t *testing.T) {
	tokenFile := t.TempDir() + "/token"
	require.NoError(t, os.WriteFile(tokenFile, []byte("first-token"), 0o600))
	t.Setenv(grafanaServiceAccountTokenFileEnvVar, tokenFile)
	t.Setenv(grafanaServiceAccountTokenEnvVar, "")
	t.Setenv(grafanaAPIEnvVar, "")

	var received []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received = append(received, r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/frontend/settings" {
			_, _ = w.Write([]byte(`{}`))
			return
		}
		_, _ = w.Write([]byte(`[]`))
	}))
	defer server.Close()

	grafanaClient := NewGrafanaClient(t.Context(), server.URL, "first-token", nil)
	require.NoError(t, os.WriteFile(tokenFile, []byte("rotated-token"), 0o600))
	_, err := grafanaClient.Datasources.GetDataSourcesWithParams(
		datasources.NewGetDataSourcesParamsWithContext(t.Context()),
	)
	require.NoError(t, err)

	require.GreaterOrEqual(t, len(received), 2)
	require.Equal(t, "Bearer rotated-token", received[len(received)-1])
}

func TestNewGrafanaClientPreservesRequestAPIKeyWhenTokenFileIsConfigured(t *testing.T) {
	tokenFile := t.TempDir() + "/token"
	require.NoError(t, os.WriteFile(tokenFile, []byte("file-token"), 0o600))
	t.Setenv(grafanaServiceAccountTokenFileEnvVar, tokenFile)
	t.Setenv(grafanaServiceAccountTokenEnvVar, "")
	t.Setenv(grafanaAPIEnvVar, "")

	var received []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received = append(received, r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/frontend/settings" {
			_, _ = w.Write([]byte(`{}`))
			return
		}
		_, _ = w.Write([]byte(`[]`))
	}))
	defer server.Close()

	grafanaClient := NewGrafanaClient(t.Context(), server.URL, "request-token", nil)
	_, err := grafanaClient.Datasources.GetDataSourcesWithParams(
		datasources.NewGetDataSourcesParamsWithContext(t.Context()),
	)
	require.NoError(t, err)

	require.GreaterOrEqual(t, len(received), 2)
	require.Equal(t, "Bearer request-token", received[len(received)-1])
}

func TestNewGrafanaClientReloadsTokenFileWithCustomTLS(t *testing.T) {
	tokenFile := t.TempDir() + "/token"
	require.NoError(t, os.WriteFile(tokenFile, []byte("first-token"), 0o600))
	t.Setenv(grafanaServiceAccountTokenFileEnvVar, tokenFile)
	t.Setenv(grafanaServiceAccountTokenEnvVar, "")
	t.Setenv(grafanaAPIEnvVar, "deprecated-token")

	var received []string
	dialed := false
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received = append(received, r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/frontend/settings" {
			_, _ = w.Write([]byte(`{}`))
			return
		}
		_, _ = w.Write([]byte(`[]`))
	}))
	defer server.Close()

	ctx := WithGrafanaConfig(t.Context(), GrafanaConfig{
		TLSConfig: &TLSConfig{SkipVerify: true},
		BaseTransport: &http.Transport{
			DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
				dialed = true
				return (&net.Dialer{}).DialContext(ctx, network, address)
			},
		},
	})
	grafanaClient := NewGrafanaClient(ctx, server.URL, "first-token", nil)
	require.NoError(t, os.WriteFile(tokenFile, []byte("rotated-token"), 0o600))
	_, err := grafanaClient.Datasources.GetDataSourcesWithParams(
		datasources.NewGetDataSourcesParamsWithContext(t.Context()),
	)
	require.NoError(t, err)
	require.True(t, dialed)

	require.GreaterOrEqual(t, len(received), 2)
	require.Equal(t, "Bearer rotated-token", received[len(received)-1])
}

func TestServiceAccountTokenFileRemainsEnabledWithDeprecatedAPIKey(t *testing.T) {
	t.Setenv(grafanaServiceAccountTokenEnvVar, "")
	t.Setenv(grafanaAPIEnvVar, "deprecated-token")
	t.Setenv(grafanaServiceAccountTokenFileEnvVar, "/path/to/token")

	require.Equal(t, "/path/to/token", serviceAccountTokenFileFromEnv())
}
