package tools

import (
	"context"
	"testing"
	"time"

	mcpgrafana "github.com/grafana/mcp-grafana/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func promEnforceContext(matchers string) (context.Context, error) {
	parsed, err := ParseEnforcedMatchers(matchers)
	if err != nil {
		return nil, err
	}
	gc := mcpgrafana.GrafanaConfig{
		PrometheusEnforcedMatchers: parsed,
	}
	return mcpgrafana.WithGrafanaConfig(context.Background(), gc), nil
}

func TestEnforcePromQL(t *testing.T) {
	ctx, err := promEnforceContext(`namespace="prod"`)
	require.NoError(t, err)

	t.Run("disabled passes query through unchanged", func(t *testing.T) {
		out, err := enforcePromQL(context.Background(), `up`, time.Hour, 60)
		require.NoError(t, err)
		assert.Equal(t, `up`, out)
	})

	t.Run("bare metric", func(t *testing.T) {
		out, err := enforcePromQL(ctx, `up`, time.Hour, 60)
		require.NoError(t, err)
		assert.Equal(t, `up{namespace="prod"}`, out)
	})

	t.Run("selector with existing label", func(t *testing.T) {
		out, err := enforcePromQL(ctx, `up{job="api"}`, time.Hour, 60)
		require.NoError(t, err)
		assert.Equal(t, `up{job="api",namespace="prod"}`, out)
	})

	t.Run("range vector in rate", func(t *testing.T) {
		out, err := enforcePromQL(ctx, `rate(http_requests_total[5m]) > 10`, time.Hour, 60)
		require.NoError(t, err)
		assert.Equal(t, `rate(http_requests_total{namespace="prod"}[5m]) > 10`, out)
	})

	t.Run("subquery and aggregations", func(t *testing.T) {
		out, err := enforcePromQL(ctx, `sum by (job) (rate(http_requests_total{status=~"5.."}[5m]))`, time.Hour, 60)
		require.NoError(t, err)
		assert.Equal(t, `sum by (job) (rate(http_requests_total{namespace="prod",status=~"5.."}[5m]))`, out)
	})

	t.Run("binary expression injects into both sides", func(t *testing.T) {
		out, err := enforcePromQL(ctx, `foo + bar{a="b"}`, time.Hour, 60)
		require.NoError(t, err)
		assert.Equal(t, `foo{namespace="prod"} + bar{a="b",namespace="prod"}`, out)
	})

	t.Run("offset and at modifiers", func(t *testing.T) {
		out, err := enforcePromQL(ctx, `foo offset 5m`, time.Hour, 60)
		require.NoError(t, err)
		assert.Equal(t, `foo{namespace="prod"} offset 5m`, out)
	})

	t.Run("scalar queries untouched", func(t *testing.T) {
		out, err := enforcePromQL(ctx, `1 + 2`, time.Hour, 60)
		require.NoError(t, err)
		assert.Equal(t, `1 + 2`, out)
	})

	t.Run("vector function", func(t *testing.T) {
		out, err := enforcePromQL(ctx, `vector(1)`, time.Hour, 60)
		require.NoError(t, err)
		assert.Equal(t, `vector(1)`, out)
	})

	t.Run("grafana macros expanded and enforced", func(t *testing.T) {
		out, err := enforcePromQL(ctx, `rate(x[$__rate_interval])`, time.Hour, 15)
		require.NoError(t, err)
		assert.Equal(t, `rate(x{namespace="prod"}[1m])`, out)
	})

	t.Run("multiple enforced matchers", func(t *testing.T) {
		multiCtx, err := promEnforceContext(`namespace="prod", cluster=~"us-.*"`)
		require.NoError(t, err)
		out, err := enforcePromQL(multiCtx, `up{job="api"}`, time.Hour, 60)
		require.NoError(t, err)
		assert.Equal(t, `up{cluster=~"us-.*",job="api",namespace="prod"}`, out)
	})

	t.Run("negative enforced matcher", func(t *testing.T) {
		negCtx, err := promEnforceContext(`namespace!="vault"`)
		require.NoError(t, err)
		out, err := enforcePromQL(negCtx, `up{job="api"}`, time.Hour, 60)
		require.NoError(t, err)
		assert.Equal(t, `up{job="api",namespace!="vault"}`, out)
	})

	t.Run("fails closed on unparseable query", func(t *testing.T) {
		_, err := enforcePromQL(ctx, `rate(}`, time.Hour, 60)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "could not parse PromQL query")
	})
}
