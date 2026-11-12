package mcpgrafana

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newCountingTLSServer starts a TLS server and returns it with a counter of
// the connections it has accepted.
func newCountingTLSServer(t testing.TB) (*httptest.Server, *atomic.Int64) {
	t.Helper()
	var conns atomic.Int64
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	srv.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			conns.Add(1)
		}
	}
	srv.StartTLS()
	t.Cleanup(srv.Close)
	return srv, &conns
}

// getWithNewTransport does what a tool call does: it builds a transport from
// cfg and sends one request through it.
func getWithNewTransport(cfg *GrafanaConfig, url string) error {
	transport, err := BuildTransport(cfg, nil, WithoutOtel())
	if err != nil {
		return err
	}
	resp, err := (&http.Client{Transport: transport}).Get(url)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	_, err = io.Copy(io.Discard, resp.Body)
	return err
}

func writeCAFile(t *testing.T, path string, der []byte) {
	t.Helper()
	data := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	require.NoError(t, os.WriteFile(path, data, 0o600))
}

func unrelatedCA(t *testing.T) []byte {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "unrelated"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	require.NoError(t, err)
	return der
}

func TestBuildTransport_ReusesConnections(t *testing.T) {
	t.Run("with TLS config", func(t *testing.T) {
		srv, conns := newCountingTLSServer(t)
		cfg := &GrafanaConfig{TLSConfig: &TLSConfig{SkipVerify: true}}

		for range 5 {
			require.NoError(t, getWithNewTransport(cfg, srv.URL))
		}

		assert.EqualValues(t, 1, conns.Load())
	})

	t.Run("across copies of the config", func(t *testing.T) {
		srv, conns := newCountingTLSServer(t)

		for range 5 {
			cfg := GrafanaConfig{TLSConfig: &TLSConfig{SkipVerify: true}}
			require.NoError(t, getWithNewTransport(&cfg, srv.URL))
		}

		assert.EqualValues(t, 1, conns.Load())
	})
}

func TestBuildTransport_KeepsConfigsApart(t *testing.T) {
	srv, _ := newCountingTLSServer(t)

	require.NoError(t, getWithNewTransport(&GrafanaConfig{TLSConfig: &TLSConfig{SkipVerify: true}}, srv.URL))

	err := getWithNewTransport(&GrafanaConfig{TLSConfig: &TLSConfig{}}, srv.URL)
	require.Error(t, err, "a config that verifies must not share a transport that skips verification")
}

func TestBuildTransport_PicksUpRotatedCAFile(t *testing.T) {
	srv, _ := newCountingTLSServer(t)
	caFile := filepath.Join(t.TempDir(), "ca.pem")
	cfg := &GrafanaConfig{TLSConfig: &TLSConfig{CAFile: caFile}}

	writeCAFile(t, caFile, unrelatedCA(t))
	require.Error(t, getWithNewTransport(cfg, srv.URL))

	writeCAFile(t, caFile, srv.Certificate().Raw)
	require.NoError(t, getWithNewTransport(cfg, srv.URL))
}

func TestBuildTransport_DoesNotCacheFailures(t *testing.T) {
	caFile := filepath.Join(t.TempDir(), "ca.pem")
	cfg := &GrafanaConfig{TLSConfig: &TLSConfig{CAFile: caFile}}

	_, err := BuildTransport(cfg, nil)
	require.Error(t, err)

	srv, _ := newCountingTLSServer(t)
	writeCAFile(t, caFile, srv.Certificate().Raw)
	require.NoError(t, getWithNewTransport(cfg, srv.URL))
}

func TestBuildTransport_SOCKS5ProxySharesTransport(t *testing.T) {
	cfg := &GrafanaConfig{SOCKS5ProxyURL: "socks5://127.0.0.1:1080"}

	first, err := baseTransport(cfg, http.DefaultTransport)
	require.NoError(t, err)
	second, err := baseTransport(cfg, http.DefaultTransport)
	require.NoError(t, err)

	assert.Same(t, first, second)
	assert.NotSame(t, http.DefaultTransport, first)
}

func TestBuildTransport_LeavesCallerTransportAlone(t *testing.T) {
	caller := http.DefaultTransport.(*http.Transport).Clone()
	cfg := &GrafanaConfig{TLSConfig: &TLSConfig{SkipVerify: true}}

	first, err := baseTransport(cfg, caller)
	require.NoError(t, err)
	second, err := baseTransport(cfg, caller)
	require.NoError(t, err)

	assert.NotSame(t, first, second, "a transport the caller passes in stays the caller's to manage")
}

func BenchmarkToolCallTransport(b *testing.B) {
	for _, tc := range []struct {
		name string
		cfg  *GrafanaConfig
	}{
		{name: "default", cfg: &GrafanaConfig{}},
		{name: "tls_config", cfg: &GrafanaConfig{TLSConfig: &TLSConfig{SkipVerify: true}}},
	} {
		b.Run(tc.name, func(b *testing.B) {
			srv, conns := newCountingTLSServer(b)
			if tc.cfg.TLSConfig == nil {
				defaults := http.DefaultTransport.(*http.Transport)
				saved := defaults.TLSClientConfig
				defaults.TLSClientConfig = srv.Client().Transport.(*http.Transport).TLSClientConfig
				defaults.CloseIdleConnections()
				b.Cleanup(func() {
					defaults.CloseIdleConnections()
					defaults.TLSClientConfig = saved
				})
			}
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				if err := getWithNewTransport(tc.cfg, srv.URL); err != nil {
					b.Fatal(fmt.Errorf("get: %w", err))
				}
			}
			b.ReportMetric(float64(conns.Load()), "conns")
		})
	}
}
