package tools

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPrometheusTools(t *testing.T) {
	t.Run("list prometheus metric metadata", func(t *testing.T) {
		ctx := newTestContext()
		result, err := listPrometheusMetricMetadata(ctx, ListPrometheusMetricMetadataParams{
			DatasourceUID: "prometheus",
		})
		require.NoError(t, err)
		assert.Len(t, result, 10)
	})
}
