//go:build integration

package tools

import (
	"context"
	"fmt"
	"strings"
	"testing"

	mcpgrafana "github.com/grafana/mcp-grafana"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ctxWithEnforcedMatchers overlays Loki enforcement onto the standard
// integration test context, preserving the wired Grafana/K8s clients.
func ctxWithEnforcedMatchers(t *testing.T, expr, fallback string) context.Context {
	t.Helper()
	ctx := newTestContext()
	cfg := mcpgrafana.GrafanaConfigFromContext(ctx)
	matchers, err := ParseEnforcedMatchers(expr)
	require.NoError(t, err)
	cfg.LokiEnforcedMatchers = matchers
	cfg.LokiLabelEnumerationFallback = fallback
	return mcpgrafana.WithGrafanaConfig(ctx, cfg)
}

// discoverContainers returns a container label value that contains "grafana"
// and one that does not, so the tests don't hard-code compose container names.
func discoverContainers(t *testing.T) (grafanaC, otherC string) {
	t.Helper()
	values, err := listLokiLabelValues(newTestContext(), ListLokiLabelValuesParams{
		DatasourceUID: "loki",
		LabelName:     "container",
	})
	require.NoError(t, err)
	require.NotEmpty(t, values, "expected some container label values in Loki")
	// The compose project prefix ("mcp-grafana-") appears in every container
	// name, so match the grafana service by suffix rather than substring.
	for _, c := range values {
		if strings.HasSuffix(c, "-grafana-1") {
			grafanaC = c
			break
		}
	}
	require.NotEmpty(t, grafanaC, "expected a grafana container in %v", values)
	for _, c := range values {
		if c != grafanaC {
			otherC = c
			break
		}
	}
	require.NotEmpty(t, otherC, "expected a second container in %v", values)
	return grafanaC, otherC
}

func TestLokiEnforcedMatchers(t *testing.T) {
	grafanaC, otherC := discoverContainers(t)
	allow := fmt.Sprintf(`container=%q`, grafanaC)
	exclude := fmt.Sprintf(`container!=%q`, grafanaC)

	t.Run("positive matcher restricts query results to allowed streams", func(t *testing.T) {
		ctx := ctxWithEnforcedMatchers(t, allow, LabelEnumFallbackReject)
		// A broad query is narrowed to only the allowed container.
		result, err := queryLokiLogs(ctx, QueryLokiLogsParams{
			DatasourceUID: "loki",
			LogQL:         `{container=~".+"}`,
			Limit:         50,
		})
		require.NoError(t, err)
		require.NotEmpty(t, result.Data, "expected some logs from the allowed container")
		for _, entry := range result.Data {
			assert.Equal(t, grafanaC, entry.Labels["container"],
				"enforcement must restrict results to the allowed container")
		}
	})

	t.Run("enforced matcher cannot be escaped by the user query", func(t *testing.T) {
		ctx := ctxWithEnforcedMatchers(t, allow, LabelEnumFallbackReject)
		// Asking for a different container yields an empty AND with the enforced one.
		result, err := queryLokiLogs(ctx, QueryLokiLogsParams{
			DatasourceUID: "loki",
			LogQL:         fmt.Sprintf(`{container=%q}`, otherC),
			Limit:         50,
		})
		require.NoError(t, err)
		assert.Empty(t, result.Data, "conflicting user matcher must produce no results")
	})

	t.Run("positive matcher scopes label value enumeration", func(t *testing.T) {
		ctx := ctxWithEnforcedMatchers(t, allow, LabelEnumFallbackReject)
		values, err := listLokiLabelValues(ctx, ListLokiLabelValuesParams{
			DatasourceUID: "loki",
			LabelName:     "container",
		})
		require.NoError(t, err)
		assert.Equal(t, []string{grafanaC}, values,
			"label value enumeration must be scoped to the enforced selector")
	})

	t.Run("negative matcher rejects label enumeration by default", func(t *testing.T) {
		ctx := ctxWithEnforcedMatchers(t, exclude, LabelEnumFallbackReject)
		_, err := listLokiLabelValues(ctx, ListLokiLabelValuesParams{
			DatasourceUID: "loki",
			LabelName:     "container",
		})
		assert.Error(t, err, "purely-negative enforcement must fail closed for label enumeration")
	})

	t.Run("negative matcher still enforces content queries", func(t *testing.T) {
		ctx := ctxWithEnforcedMatchers(t, exclude, LabelEnumFallbackUnfiltered)
		result, err := queryLokiLogs(ctx, QueryLokiLogsParams{
			DatasourceUID: "loki",
			LogQL:         `{container=~".+"}`,
			Limit:         50,
		})
		require.NoError(t, err)
		for _, entry := range result.Data {
			assert.NotEqual(t, grafanaC, entry.Labels["container"],
				"negative enforcement must exclude the container from results")
		}
	})
}
