// Requires a Cloud or other Grafana instance with Grafana Incident available,
// with a Prometheus datasource provisioned.
// Run with `go test -tags integration,cloud`.
//go:build integration && cloud

package tools

import (
	"context"
	"testing"

	mcpgrafana "github.com/grafana/mcp-grafana"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCloudIncidentTools(t *testing.T) {
	t.Run("list incidents", func(t *testing.T) {
		ctx := mcpgrafana.ExtractIncidentClientFromEnv(context.Background())
		result, err := listIncidents(ctx, ListIncidentsParams{
			Limit: 2,
		})
		require.NoError(t, err)
		assert.Len(t, result.Incidents, 2)
	})
}
