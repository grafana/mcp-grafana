//go:build unit

package tools

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/grafana/grafana-openapi-client-go/models"
	mcpgrafana "github.com/grafana/mcp-grafana/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPromCardinalityCachesServerTLSConfiguration(t *testing.T) {
	for _, mode := range []string{"custom CA", "skip verification", "client certificate", "custom transport"} {
		t.Run(mode, func(t *testing.T) {
			var calls atomic.Int64
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				fmt.Fprint(w, `{"status":"success","data":{"seriesCountByMetricName":[{"name":"hot","value":100001}]}}`)
			}))
			defer server.Close()
			tlsConfig := &mcpgrafana.TLSConfig{SkipVerify: true}
			if mode == "custom CA" {
				certPath := filepath.Join(t.TempDir(), "ca.pem")
				require.NoError(t, os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600))
				tlsConfig = &mcpgrafana.TLSConfig{CAFile: certPath}
			}
			cfg := mcpgrafana.GrafanaConfig{URL: server.URL, TLSConfig: tlsConfig, PrometheusGuardrailCardinalityCacheTTL: time.Minute}
			wantCalls := int64(1)
			if mode == "client certificate" {
				certPath := filepath.Join(t.TempDir(), "client.pem")
				keyPath := filepath.Join(t.TempDir(), "client-key.pem")
				key, err := x509.MarshalPKCS8PrivateKey(server.TLS.Certificates[0].PrivateKey)
				require.NoError(t, err)
				require.NoError(t, os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600))
				require.NoError(t, os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: key}), 0600))
				cfg.TLSConfig = &mcpgrafana.TLSConfig{SkipVerify: true, CertFile: certPath, KeyFile: keyPath}
				wantCalls = 2
			}
			if mode == "custom transport" {
				cfg.BaseTransport = server.Client().Transport
				wantCalls = 2
			}
			ctx := mcpgrafana.WithGrafanaConfig(context.Background(), cfg)
			for range 2 {
				b, err := newPrometheusBackend(ctx, "prometheus", &models.DataSource{UID: "prometheus", Type: "prometheus"})
				require.NoError(t, err)
				stats, err := cachedPromCardinality(ctx, b, cfg)
				require.NoError(t, err)
				assert.Equal(t, uint64(100001), stats["hot"])
			}
			assert.Equal(t, wantCalls, calls.Load(), "only independently changing caller identity should bypass the TSDB cache")
		})
	}
}
