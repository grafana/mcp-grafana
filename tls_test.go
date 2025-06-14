package mcpgrafana

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTLSConfig_CreateTLSConfig(t *testing.T) {
	t.Run("nil config returns nil", func(t *testing.T) {
		var config *TLSConfig
		tlsCfg, err := config.CreateTLSConfig()
		assert.NoError(t, err)
		assert.Nil(t, tlsCfg)
	})

	t.Run("skip verify only", func(t *testing.T) {
		config := &TLSConfig{SkipVerify: true}
		tlsCfg, err := config.CreateTLSConfig()
		assert.NoError(t, err)
		require.NotNil(t, tlsCfg)
		assert.True(t, tlsCfg.InsecureSkipVerify)
		assert.Empty(t, tlsCfg.Certificates)
		assert.Nil(t, tlsCfg.RootCAs)
	})

	t.Run("invalid cert file", func(t *testing.T) {
		config := &TLSConfig{
			CertFile: "nonexistent.pem",
			KeyFile:  "nonexistent.key",
		}
		_, err := config.CreateTLSConfig()
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to load client certificate")
	})

	t.Run("invalid CA file", func(t *testing.T) {
		config := &TLSConfig{
			CAFile: "nonexistent-ca.pem",
		}
		_, err := config.CreateTLSConfig()
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to read CA certificate")
	})
}

func TestHTTPTransport(t *testing.T) {
	t.Run("nil TLS config", func(t *testing.T) {
		var tlsConfig *TLSConfig
		transport, err := tlsConfig.HTTPTransport(http.DefaultTransport.(*http.Transport))
		assert.NoError(t, err)
		assert.NotNil(t, transport)

		// Should be default transport clone
		httpTransport, ok := transport.(*http.Transport)
		require.True(t, ok)
		assert.NotNil(t, httpTransport)
	})

	t.Run("skip verify config", func(t *testing.T) {
		tlsConfig := &TLSConfig{SkipVerify: true}
		transport, err := tlsConfig.HTTPTransport(http.DefaultTransport.(*http.Transport))
		assert.NoError(t, err)
		require.NotNil(t, transport)

		httpTransport, ok := transport.(*http.Transport)
		require.True(t, ok)
		require.NotNil(t, httpTransport.TLSClientConfig)
		assert.True(t, httpTransport.TLSClientConfig.InsecureSkipVerify)
	})

	t.Run("invalid TLS config", func(t *testing.T) {
		tlsConfig := &TLSConfig{
			CertFile: "nonexistent.pem",
			KeyFile:  "nonexistent.key",
		}
		_, err := tlsConfig.HTTPTransport(http.DefaultTransport.(*http.Transport))
		assert.Error(t, err)
	})
}
