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

func TestTokenFileRoundTripperPreservesRequestScopedCredentials(t *testing.T) {
	tokenFile := t.TempDir() + "/token"
	require.NoError(t, os.WriteFile(tokenFile, []byte("file-token"), 0o600))

	tests := []struct {
		name   string
		header string
		value  string
	}{
		{name: "authorization", header: "Authorization", value: "Bearer request-token"},
		{name: "service account token", header: grafanaServiceAccountTokenHeader, value: "request-token"},
		{name: "deprecated api key", header: grafanaAPIKeyHeader, value: "request-token"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var captured *http.Request
			transport := &tokenFileRoundTripper{
				path:          tokenFile,
				fallbackToken: "static-token",
				underlying: &capturingMockRT{fn: func(req *http.Request) (*http.Response, error) {
					captured = req
					return &http.Response{StatusCode: http.StatusNoContent}, nil
				}},
			}
			req, err := http.NewRequest(http.MethodGet, "http://example.com", nil)
			require.NoError(t, err)
			req.Header.Set(tt.header, tt.value)

			_, err = transport.RoundTrip(req)
			require.NoError(t, err)
			require.NotNil(t, captured)
			require.Equal(t, tt.value, captured.Header.Get(tt.header))
			if tt.header == "Authorization" {
				require.Equal(t, tt.value, captured.Header.Get("Authorization"))
			} else {
				require.Empty(t, captured.Header.Get("Authorization"), "request-scoped API-key headers must not be replaced by the file token")
			}
		})
	}
}

func TestTokenFileRoundTripperProtectsRequestCredentialsFromAuthLayers(t *testing.T) {
	tokenFile := t.TempDir() + "/token"
	require.NoError(t, os.WriteFile(tokenFile, []byte("file-token"), 0o600))

	var captured *http.Request
	base := &capturingMockRT{fn: func(req *http.Request) (*http.Response, error) {
		captured = req
		return &http.Response{StatusCode: http.StatusNoContent}, nil
	}}
	transport := &tokenFileRoundTripper{
		path:          tokenFile,
		fallbackToken: "static-token",
		underlying: NewExtraHeadersRoundTripper(
			NewAuthRoundTripper(base, "", "", "static-token", nil),
			map[string]string{
				"Authorization":                  "Bearer extra-token",
				grafanaAPIKeyHeader:              "extra-api-key",
				grafanaServiceAccountTokenHeader: "extra-service-token",
			},
		),
	}
	req, err := http.NewRequest(http.MethodGet, "http://example.com", nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer request-token")

	_, err = transport.RoundTrip(req)
	require.NoError(t, err)
	require.NotNil(t, captured)
	require.Equal(t, "Bearer request-token", captured.Header.Get("Authorization"))
	require.Empty(t, captured.Header.Get(grafanaAPIKeyHeader))
	require.Empty(t, captured.Header.Get(grafanaServiceAccountTokenHeader))
}

func TestTokenFileRoundTripperProtectsFallbackCredentialsFromAuthLayers(t *testing.T) {
	tokenFile := t.TempDir() + "/missing-token"

	var captured *http.Request
	base := &capturingMockRT{fn: func(req *http.Request) (*http.Response, error) {
		captured = req
		return &http.Response{StatusCode: http.StatusNoContent}, nil
	}}
	transport := &tokenFileRoundTripper{
		path:          tokenFile,
		fallbackToken: "static-token",
		underlying: NewExtraHeadersRoundTripper(
			NewAuthRoundTripper(base, "", "", "auth-layer-token", nil),
			map[string]string{
				"Authorization":                  "Bearer extra-token",
				grafanaAPIKeyHeader:              "extra-api-key",
				grafanaServiceAccountTokenHeader: "extra-service-token",
			},
		),
	}
	req, err := http.NewRequest(http.MethodGet, "http://example.com", nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer request-token")

	_, err = transport.RoundTrip(req)
	require.NoError(t, err)
	require.NotNil(t, captured)
	require.Equal(t, "Bearer request-token", captured.Header.Get("Authorization"))
	require.Empty(t, captured.Header.Get(grafanaAPIKeyHeader))
	require.Empty(t, captured.Header.Get(grafanaServiceAccountTokenHeader))
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

func TestBuildTransportPreservesTokenFileRoundTripperWithCustomTLS(t *testing.T) {
	tokenFile := t.TempDir() + "/token"
	require.NoError(t, os.WriteFile(tokenFile, []byte("file-token"), 0o600))

	var received string
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	base := &tokenFileRoundTripper{
		path:       tokenFile,
		underlying: http.DefaultTransport,
	}
	transport, err := BuildTransport(&GrafanaConfig{
		TLSConfig: &TLSConfig{SkipVerify: true},
	}, base, WithoutOtel())
	require.NoError(t, err)

	req, err := http.NewRequest(http.MethodGet, server.URL, nil)
	require.NoError(t, err)
	_, err = transport.RoundTrip(req)
	require.NoError(t, err)
	require.Equal(t, "Bearer file-token", received)
}

func TestBuildTransportDoesNotDiscardCustomTokenFileUnderlyingWithTLS(t *testing.T) {
	base := &capturingMockRT{}
	transport := &tokenFileRoundTripper{
		path:       t.TempDir() + "/token",
		underlying: base,
	}

	_, err := BuildTransport(&GrafanaConfig{TLSConfig: &TLSConfig{SkipVerify: true}}, transport, WithoutOtel())
	require.Error(t, err)
	require.Contains(t, err.Error(), "token-file transport")
}

func TestServiceAccountTokenFileRemainsEnabledWithDeprecatedAPIKey(t *testing.T) {
	t.Setenv(grafanaServiceAccountTokenEnvVar, "")
	t.Setenv(grafanaAPIEnvVar, "deprecated-token")
	t.Setenv(grafanaServiceAccountTokenFileEnvVar, "/path/to/token")

	require.Equal(t, "/path/to/token", serviceAccountTokenFileFromEnv())
}

func TestNewGrafanaClientUsesTokenFileWhenDeprecatedAPIKeyAlsoSet(t *testing.T) {
	tokenFile := t.TempDir() + "/token"
	require.NoError(t, os.WriteFile(tokenFile, []byte("file-token"), 0o600))
	t.Setenv(grafanaServiceAccountTokenEnvVar, "")
	t.Setenv(grafanaServiceAccountTokenFileEnvVar, tokenFile)
	t.Setenv(grafanaAPIEnvVar, "deprecated-token")

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

	client := NewGrafanaClient(t.Context(), server.URL, "deprecated-token", nil)
	_, err := client.Datasources.GetDataSourcesWithParams(
		datasources.NewGetDataSourcesParamsWithContext(t.Context()),
	)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(tokenFile, []byte("rotated-token"), 0o600))
	_, err = client.Datasources.GetDataSourcesWithParams(
		datasources.NewGetDataSourcesParamsWithContext(t.Context()),
	)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(received), 3)
	require.Contains(t, received, "Bearer file-token")
	require.Equal(t, "Bearer rotated-token", received[len(received)-1])
}

func TestNewGrafanaClientDoesNotOverwriteRotatedTokenWithContextAPIKey(t *testing.T) {
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

	ctx := WithGrafanaConfig(t.Context(), GrafanaConfig{APIKey: "first-token"})
	client := NewGrafanaClient(ctx, server.URL, "first-token", nil)
	require.NoError(t, os.WriteFile(tokenFile, []byte("rotated-token"), 0o600))
	_, err := client.Datasources.GetDataSourcesWithParams(
		datasources.NewGetDataSourcesParamsWithContext(ctx),
	)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(received), 2)
	require.Equal(t, "Bearer rotated-token", received[len(received)-1])
}
