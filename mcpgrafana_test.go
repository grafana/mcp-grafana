package mcpgrafana

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExtractIncidentClientFromEnv(t *testing.T) {
	t.Setenv("GRAFANA_URL", "http://my-test-url.grafana.com/")
	ctx := ExtractIncidentClientFromEnv(context.Background())

	client := IncidentClientFromContext(ctx)
	require.NotNil(t, client)
	assert.Equal(t, "http://my-test-url.grafana.com/api/plugins/grafana-incident-app/resources/api/v1/", client.RemoteHost)
}
