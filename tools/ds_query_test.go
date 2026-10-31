//go:build unit
// +build unit

package tools

import (
	"context"
	"testing"
	"time"

	mcpgrafana "github.com/grafana/mcp-grafana/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewDSQueryHTTPClientTimeout(t *testing.T) {
	for name, tc := range map[string]struct {
		configured time.Duration
		want       time.Duration
	}{
		"unset uses default":     {0, mcpgrafana.DefaultGrafanaClientTimeout},
		"--grafana-timeout 1s":   {time.Second, time.Second},
		"--grafana-timeout 600s": {600 * time.Second, 600 * time.Second},
	} {
		t.Run(name, func(t *testing.T) {
			ctx := mcpgrafana.WithGrafanaConfig(context.Background(), mcpgrafana.GrafanaConfig{
				URL:     "http://localhost:3000",
				Timeout: tc.configured,
			})
			client, _, err := newDSQueryHTTPClient(ctx)
			require.NoError(t, err)
			assert.Equal(t, tc.want, client.Timeout)
		})
	}
}
