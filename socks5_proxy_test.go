package mcpgrafana

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseSOCKS5ProxyURL(t *testing.T) {
	t.Run("valid URLs", func(t *testing.T) {
		for _, raw := range []string{
			"socks5://proxy.example.com:1080",
			"socks5h://proxy.example.com",
			"socks5://user:pass@proxy.example.com:1080",
			"socks5://[::1]:1080",
		} {
			u, err := parseSOCKS5ProxyURL(raw)
			require.NoError(t, err, "expected %q to be valid", raw)
			require.NotNil(t, u)
		}
	})

	t.Run("invalid URLs", func(t *testing.T) {
		for name, raw := range map[string]string{
			"empty string":     "",
			"http scheme":      "http://proxy.example.com:1080",
			"https scheme":     "https://proxy.example.com:1080",
			"empty host":       "socks5://:1080",
			"port zero":        "socks5://proxy.example.com:0",
			"port too large":   "socks5://proxy.example.com:65536",
			"non-numeric port": "socks5://proxy.example.com:abc",
			"path":             "socks5://proxy.example.com:1080/path",
			"query":            "socks5://proxy.example.com:1080?key=value",
			"force query":      "socks5://proxy.example.com:1080?",
			"fragment":         "socks5://proxy.example.com:1080#frag",
			"opaque":           "socks5:proxy.example.com",
			"no scheme":        "proxy.example.com:1080",
		} {
			_, err := parseSOCKS5ProxyURL(raw)
			assert.Error(t, err, "expected %q (%s) to be invalid", raw, name)
		}
	})

	t.Run("errors do not leak credentials or the raw URL", func(t *testing.T) {
		for _, raw := range []string{
			"socks5://user:secretpw@proxy.example.com:99999",
			"socks5://user:secretpw@proxy.example.com:bad%port",
		} {
			_, err := parseSOCKS5ProxyURL(raw)
			require.Error(t, err)
			assert.NotContains(t, err.Error(), "secretpw")
			assert.NotContains(t, err.Error(), raw)
		}
	})
}

// TestNewGrafanaClientUsesSOCKS5Proxy is a behavioral regression test: every
// request made by NewGrafanaClient — the public-URL fetch during construction
// (fetchCfg) and OpenAPI client calls (oboConfig) — must be dialed through the
// configured SOCKS5 proxy, never directly. The fake proxy accepts TCP
// connections and immediately closes them; a full SOCKS handshake is not
// needed to observe the dial.
func TestNewGrafanaClientUsesSOCKS5Proxy(t *testing.T) {
	var directHits atomic.Int32
	ts := newTestHTTPServer(t, func(w http.ResponseWriter, r *http.Request) {
		directHits.Add(1)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	})

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })
	var proxyConns atomic.Int32
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			proxyConns.Add(1)
			_ = conn.Close()
		}
	}()

	ctx := WithGrafanaConfig(context.Background(), GrafanaConfig{
		SOCKS5ProxyURL: "socks5://" + ln.Addr().String(),
	})
	c := NewGrafanaClient(ctx, ts.URL, "test-key", nil)
	require.NotNil(t, c)

	// The public-URL fetch inside NewGrafanaClient uses fetchCfg.
	assert.Positive(t, proxyConns.Load(), "public URL fetch must be dialed through the proxy")
	assert.Zero(t, directHits.Load(), "no request may bypass the proxy")

	// An OpenAPI client call uses the transport built from oboConfig.
	before := proxyConns.Load()
	_, err = c.Search.Search(nil, nil)
	require.Error(t, err, "the fake proxy drops connections, so the call must fail")
	assert.Greater(t, proxyConns.Load(), before, "OpenAPI calls must be dialed through the proxy")
	assert.Zero(t, directHits.Load(), "no request may bypass the proxy")
}

func TestProxiedToolSetKeyIncludesSOCKS5Proxy(t *testing.T) {
	ctxA := WithGrafanaConfig(context.Background(), GrafanaConfig{SOCKS5ProxyURL: "socks5://proxy-a.example.com:1080"})
	ctxB := WithGrafanaConfig(context.Background(), GrafanaConfig{SOCKS5ProxyURL: "socks5://user:secretpw@proxy-b.example.com:1080"})
	keyA := proxiedToolSetKeyFromContext(ctxA)
	keyB := proxiedToolSetKeyFromContext(ctxB)
	keyNone := proxiedToolSetKeyFromContext(context.Background())

	assert.NotEqual(t, keyA, keyB, "different proxy URLs must produce different keys")
	assert.NotEqual(t, keyA, keyNone, "proxy vs no proxy must produce different keys")

	s := keyB.String()
	assert.NotContains(t, s, "secretpw", "String() must not leak proxy credentials")
	assert.NotContains(t, s, "proxy-b.example.com", "String() must not leak the proxy host")
	assert.Contains(t, s, "socks5Proxy=true")
	assert.Contains(t, keyNone.String(), "socks5Proxy=false")
	assert.Equal(t, s, keyB.LogValue().String())
}

// TestProxiedToolSetKeyLogRedaction logs the key through a real slog JSON
// handler (the way production code logs it) and asserts the proxy URL and its
// credentials never reach the log output.
func TestProxiedToolSetKeyLogRedaction(t *testing.T) {
	key := proxiedToolSetKeyFromContext(WithGrafanaConfig(context.Background(),
		GrafanaConfig{SOCKS5ProxyURL: "socks5://user:secretpw@proxy-b.example.com:1080"}))

	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	logger.Info("test", "key", key)

	out := buf.String()
	assert.NotContains(t, out, "secretpw")
	assert.NotContains(t, out, "proxy-b.example.com")
	assert.Contains(t, out, "socks5Proxy=true")
}

// TestExtractIncidentClientFromHeadersFailsClosed mirrors the env-based
// fail-closed test for the request-scoped incident client constructor.
func TestExtractIncidentClientFromHeadersFailsClosed(t *testing.T) {
	baseCalled := false
	mock := &capturingMockRT{fn: func(req *http.Request) (*http.Response, error) {
		baseCalled = true
		return &http.Response{StatusCode: 200}, nil
	}}
	cfg := GrafanaConfig{SOCKS5ProxyURL: "socks5://proxy.example.com:1080", BaseTransport: mock}
	ctx := WithGrafanaConfig(context.Background(), cfg)
	req, _ := http.NewRequest("GET", "http://mcp.example.com/mcp", nil)
	ctx = ExtractIncidentClientFromHeaders(ctx, req)
	client := IncidentClientFromContext(ctx)
	require.NotNil(t, client)
	require.NotNil(t, client.HTTPClient.Transport, "transport must be set to a fail-closed RoundTripper")

	outReq, _ := http.NewRequest("GET", "http://grafana.example.com/api/health", nil)
	_, err := client.HTTPClient.Transport.RoundTrip(outReq)
	require.Error(t, err)
	assert.False(t, baseCalled, "request must not reach the base transport")
}

func TestFailClosedTransport(t *testing.T) {
	sentinel := errors.New("transport misconfigured")
	rt := failClosedTransport(sentinel)
	req, _ := http.NewRequest("GET", "http://grafana.example.com", nil)
	resp, err := rt.RoundTrip(req)
	require.Nil(t, resp)
	require.ErrorIs(t, err, sentinel)
}

func TestValidateSOCKS5ProxyURL(t *testing.T) {
	assert.NoError(t, ValidateSOCKS5ProxyURL("socks5://proxy.example.com:1080"))
	assert.Error(t, ValidateSOCKS5ProxyURL("http://proxy.example.com:1080"))
}

// innermostTransport builds a transport with all optional layers disabled and
// unwraps the always-present ExtraHeaders layer, returning the innermost
// RoundTripper that BuildTransport produced.
func innermostTransport(t *testing.T, cfg *GrafanaConfig, base http.RoundTripper) (http.RoundTripper, error) {
	t.Helper()
	transport, err := BuildTransport(cfg, base, WithoutAuth(), WithoutOrgID(), WithoutUserAgent(), WithoutOtel())
	if err != nil {
		return nil, err
	}
	eh, ok := transport.(*ExtraHeadersRoundTripper)
	require.True(t, ok, "expected outermost layer to be ExtraHeadersRoundTripper, got %T", transport)
	return eh.underlying, nil
}

func TestBuildTransportSOCKS5Proxy(t *testing.T) {
	const proxyRaw = "socks5://proxy.example.com:1080"
	grafanaReq, _ := http.NewRequest("GET", "http://grafana.example.com/api/health", nil)

	t.Run("proxy applied to default transport", func(t *testing.T) {
		inner, err := innermostTransport(t, &GrafanaConfig{SOCKS5ProxyURL: proxyRaw}, nil)
		require.NoError(t, err)
		ht, ok := inner.(*http.Transport)
		require.True(t, ok, "expected *http.Transport, got %T", inner)
		proxyURL, err := ht.Proxy(grafanaReq)
		require.NoError(t, err)
		require.NotNil(t, proxyURL)
		assert.Equal(t, proxyRaw, proxyURL.String())
	})

	t.Run("proxy preserved when TLS config is applied", func(t *testing.T) {
		cfg := &GrafanaConfig{
			SOCKS5ProxyURL: proxyRaw,
			TLSConfig:      &TLSConfig{SkipVerify: true},
		}
		inner, err := innermostTransport(t, cfg, nil)
		require.NoError(t, err)
		ht, ok := inner.(*http.Transport)
		require.True(t, ok, "expected *http.Transport, got %T", inner)
		require.NotNil(t, ht.TLSClientConfig)
		assert.True(t, ht.TLSClientConfig.InsecureSkipVerify)
		proxyURL, err := ht.Proxy(grafanaReq)
		require.NoError(t, err)
		require.NotNil(t, proxyURL)
		assert.Equal(t, proxyRaw, proxyURL.String())
	})

	t.Run("unset proxy leaves base untouched", func(t *testing.T) {
		base := &http.Transport{MaxIdleConns: 123}
		inner, err := innermostTransport(t, &GrafanaConfig{}, base)
		require.NoError(t, err)
		assert.Same(t, base, inner, "base transport must not be cloned when no proxy is set")
		assert.Nil(t, base.Proxy)
	})

	t.Run("custom *http.Transport base is cloned, not mutated", func(t *testing.T) {
		base := &http.Transport{MaxIdleConns: 123}
		inner, err := innermostTransport(t, &GrafanaConfig{SOCKS5ProxyURL: proxyRaw}, base)
		require.NoError(t, err)
		ht, ok := inner.(*http.Transport)
		require.True(t, ok, "expected *http.Transport, got %T", inner)
		assert.NotSame(t, base, ht)
		assert.Equal(t, 123, ht.MaxIdleConns, "clone must preserve base transport settings")
		assert.Nil(t, base.Proxy, "original base transport must not be mutated")
		proxyURL, err := ht.Proxy(grafanaReq)
		require.NoError(t, err)
		require.NotNil(t, proxyURL)
		assert.Equal(t, proxyRaw, proxyURL.String())
	})

	t.Run("invalid proxy URL returns error", func(t *testing.T) {
		_, err := BuildTransport(&GrafanaConfig{SOCKS5ProxyURL: "http://proxy.example.com:1080"}, nil)
		require.Error(t, err)
	})

	t.Run("fail-closed incident client never sends requests when proxy is misconfigured", func(t *testing.T) {
		baseCalled := false
		mock := &capturingMockRT{fn: func(req *http.Request) (*http.Response, error) {
			baseCalled = true
			return &http.Response{StatusCode: 200}, nil
		}}
		// A non-*http.Transport base makes BuildTransport fail when a SOCKS5
		// proxy is configured; the incident client must then refuse to send
		// requests instead of falling back to a direct connection.
		cfg := GrafanaConfig{SOCKS5ProxyURL: proxyRaw, BaseTransport: mock}
		ctx := WithGrafanaConfig(context.Background(), cfg)
		ctx = ExtractIncidentClientFromEnv(ctx)
		client := IncidentClientFromContext(ctx)
		require.NotNil(t, client)
		require.NotNil(t, client.HTTPClient.Transport, "transport must be set to a fail-closed RoundTripper")

		req, _ := http.NewRequest("GET", "http://grafana.example.com/api/health", nil)
		_, err := client.HTTPClient.Transport.RoundTrip(req)
		require.Error(t, err)
		assert.False(t, baseCalled, "request must not reach the base transport")
	})

	t.Run("non-Transport BaseTransport conflicts with proxy", func(t *testing.T) {
		mock := &capturingMockRT{fn: func(req *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200}, nil
		}}
		cfg := &GrafanaConfig{
			SOCKS5ProxyURL: "socks5://user:secretpw@proxy.example.com:1080",
			BaseTransport:  mock,
		}
		_, err := BuildTransport(cfg, nil)
		require.Error(t, err)
		assert.NotContains(t, err.Error(), "secretpw", "error must not leak proxy credentials")
		assert.NotContains(t, err.Error(), "proxy.example.com", "error must not leak the proxy URL")
	})
}
