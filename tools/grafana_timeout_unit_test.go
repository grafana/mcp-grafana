//go:build unit

package tools

import (
	"context"
	"testing"
	"time"

	mcpgrafana "github.com/grafana/mcp-grafana/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewAlertingClient_Timeout(t *testing.T) {
	for cfg, want := range map[time.Duration]time.Duration{
		0:                 30 * time.Second,
		300 * time.Second: 300 * time.Second,
	} {
		ctx := mcpgrafana.WithGrafanaConfig(context.Background(), mcpgrafana.GrafanaConfig{
			URL:     "http://grafana.example",
			Timeout: cfg,
		})
		client, err := newAlertingClientFromContext(ctx)
		require.NoError(t, err)
		assert.Equal(t, want, client.httpClient.Timeout)
	}
}

func TestNewDSQueryHTTPClient_Timeout(t *testing.T) {
	for _, tc := range []struct {
		name string
		cfg  time.Duration
		want time.Duration
	}{
		{name: "unset keeps the 30s default", cfg: 0, want: 30 * time.Second},
		{name: "longer --grafana-timeout is used", cfg: 300 * time.Second, want: 300 * time.Second},
		{name: "shorter --grafana-timeout is used", cfg: 10 * time.Second, want: 10 * time.Second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := mcpgrafana.WithGrafanaConfig(context.Background(), mcpgrafana.GrafanaConfig{
				URL:     "http://grafana.example",
				Timeout: tc.cfg,
			})
			client, _, err := newDSQueryHTTPClient(ctx)
			require.NoError(t, err)
			assert.Equal(t, tc.want, client.Timeout)
		})
	}
}
