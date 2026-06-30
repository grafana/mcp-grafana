//go:build integration

package mcpgrafana

import (
	"context"
	"log/slog"
	"net/url"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newMultiOrgProxiedContext builds a context whose Grafana client carries the
// OrgID round-tripper (via NewGrafanaClient), so per-org datasource discovery
// actually scopes its requests to each org.
func newMultiOrgProxiedContext(t *testing.T) context.Context {
	t.Helper()
	grafanaURL := "http://localhost:3000"
	if u, ok := os.LookupEnv("GRAFANA_URL"); ok {
		grafanaURL = u
	}
	auth := url.UserPassword("admin", "admin")
	cfg := GrafanaConfig{URL: grafanaURL, BasicAuth: auth}
	ctx := WithGrafanaConfig(context.Background(), cfg)
	gc := NewGrafanaClient(ctx, grafanaURL, "", auth)
	return WithGrafanaClient(ctx, gc)
}

// TestProxiedMultiOrgDiscovery_Integration verifies that with dynamic multi-org
// on, proxied discovery spans every org the user can access: it finds the Tempo
// datasource the orgs-seed job creates only in the secondary org (id 2) and can
// connect to it. This is the case that default-org-only discovery would miss.
func TestProxiedMultiOrgDiscovery_Integration(t *testing.T) {
	DynamicMultiOrgEnabled = true
	t.Cleanup(func() { DynamicMultiOrgEnabled = false })

	ctx := newMultiOrgProxiedContext(t)

	discovered, _, err := discoverMCPDatasources(ctx, slog.Default())
	require.NoError(t, err)

	var org2Tempo *DiscoveredDatasource
	for i := range discovered {
		if discovered[i].OrgID == 2 && discovered[i].Type == "tempo" {
			org2Tempo = &discovered[i]
			break
		}
	}
	require.NotNilf(t, org2Tempo, "expected a Tempo MCP datasource discovered in org 2; got %+v", discovered)
	assert.Equal(t, "tempo-org2", org2Tempo.UID)

	// Connect to the org-2 datasource and confirm it serves tools and is scoped
	// to org 2.
	client, err := NewProxiedClient(ctx, org2Tempo.OrgID, org2Tempo.UID, org2Tempo.Name, org2Tempo.Type, org2Tempo.MCPURL)
	require.NoError(t, err)
	defer func() { _ = client.Close() }()
	assert.Equal(t, int64(2), client.OrgID)
	assert.Greater(t, len(client.ListTools()), 0, "org-2 Tempo should expose MCP tools")
}
