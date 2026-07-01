package tools

import (
	"context"
	"testing"

	mcpgrafana "github.com/grafana/mcp-grafana"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func ctxWithEnforced(t *testing.T, expr string) context.Context {
	t.Helper()
	matchers, err := ParseEnforcedMatchers(expr)
	require.NoError(t, err)
	require.NotEmpty(t, matchers)
	return mcpgrafana.WithGrafanaConfig(context.Background(), mcpgrafana.GrafanaConfig{
		LokiEnforcedMatchers: matchers,
	})
}

func TestParseEnforcedMatchers(t *testing.T) {
	t.Run("empty disables enforcement", func(t *testing.T) {
		m, err := ParseEnforcedMatchers("")
		require.NoError(t, err)
		assert.Empty(t, m)
	})
	t.Run("whitespace disables enforcement", func(t *testing.T) {
		m, err := ParseEnforcedMatchers("   ")
		require.NoError(t, err)
		assert.Empty(t, m)
	})
	t.Run("bare expression", func(t *testing.T) {
		m, err := ParseEnforcedMatchers(`namespace!~"vault|payments"`)
		require.NoError(t, err)
		require.Len(t, m, 1)
		assert.Equal(t, "namespace", m[0].Name)
	})
	t.Run("brace-wrapped expression", func(t *testing.T) {
		m, err := ParseEnforcedMatchers(`{namespace!~"vault", env="prod"}`)
		require.NoError(t, err)
		assert.Len(t, m, 2)
	})
	t.Run("invalid expression errors", func(t *testing.T) {
		_, err := ParseEnforcedMatchers(`namespace!~`)
		assert.Error(t, err)
	})
}

func TestEnforceLogQL(t *testing.T) {
	t.Run("disabled passes query through unchanged", func(t *testing.T) {
		ctx := mcpgrafana.WithGrafanaConfig(context.Background(), mcpgrafana.GrafanaConfig{})
		in := `{app="x"} |= "err"`
		out, err := enforceLogQL(ctx, in)
		require.NoError(t, err)
		assert.Equal(t, in, out)
	})

	ctx := ctxWithEnforced(t, `namespace!~"vault|payments"`)
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "bare selector",
			in:   `{app="x"}`,
			want: `{app="x", namespace!~"vault|payments"}`,
		},
		{
			name: "pipeline with line filter",
			in:   `{app="x"} |= "err"`,
			want: `{app="x", namespace!~"vault|payments"} |= "err"`,
		},
		{
			name: "line filter containing braces is not a selector",
			in:   `{app="x"} |= "{json}"`,
			want: `{app="x", namespace!~"vault|payments"} |= "{json}"`,
		},
		{
			name: "range metric query",
			in:   `count_over_time({app="x"}[5m])`,
			want: `count_over_time({app="x", namespace!~"vault|payments"}[5m])`,
		},
		{
			name: "binary metric op injects into both selectors",
			in:   `sum(rate({a="1"}[5m])) / sum(rate({b="2"}[5m]))`,
			want: `(sum(rate({a="1", namespace!~"vault|payments"}[5m])) / sum(rate({b="2", namespace!~"vault|payments"}[5m])))`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := enforceLogQL(ctx, tc.in)
			require.NoError(t, err)
			assert.Equal(t, tc.want, out)
		})
	}

	t.Run("user constraint on enforced label is still AND-ed (fail-safe)", func(t *testing.T) {
		out, err := enforceLogQL(ctx, `{namespace="vault"}`)
		require.NoError(t, err)
		// Both matchers survive; the query can only ever return an empty set.
		assert.Contains(t, out, `namespace="vault"`)
		assert.Contains(t, out, `namespace!~"vault|payments"`)
	})

	t.Run("invalid LogQL fails closed", func(t *testing.T) {
		_, err := enforceLogQL(ctx, `{app=}`)
		assert.Error(t, err)
	})
}

func TestEnforceLogQLPositiveMatcher(t *testing.T) {
	ctx := ctxWithEnforced(t, `namespace=~"prod|staging"`)
	out, err := enforceLogQL(ctx, `{app="x"} |= "err"`)
	require.NoError(t, err)
	assert.Equal(t, `{app="x", namespace=~"prod|staging"} |= "err"`, out)
}

func TestLabelEnumerationSelector(t *testing.T) {
	t.Run("disabled enumerates normally", func(t *testing.T) {
		ctx := mcpgrafana.WithGrafanaConfig(context.Background(), mcpgrafana.GrafanaConfig{})
		q, err := labelEnumerationSelector(ctx)
		require.NoError(t, err)
		assert.Empty(t, q)
	})

	t.Run("positive matcher scopes enumeration", func(t *testing.T) {
		ctx := ctxWithEnforced(t, `namespace=~"prod|staging"`)
		q, err := labelEnumerationSelector(ctx)
		require.NoError(t, err)
		assert.Equal(t, `{namespace=~"prod|staging"}`, q)
	})

	t.Run("negative matcher rejects by default (fail closed)", func(t *testing.T) {
		ctx := ctxWithEnforced(t, `namespace!~"vault|payments"`)
		_, err := labelEnumerationSelector(ctx)
		assert.Error(t, err)
	})

	t.Run("negative matcher enumerates unfiltered when configured", func(t *testing.T) {
		matchers, err := ParseEnforcedMatchers(`namespace!~"vault|payments"`)
		require.NoError(t, err)
		ctx := mcpgrafana.WithGrafanaConfig(context.Background(), mcpgrafana.GrafanaConfig{
			LokiEnforcedMatchers:         matchers,
			LokiLabelEnumerationFallback: LabelEnumFallbackUnfiltered,
		})
		q, err := labelEnumerationSelector(ctx)
		require.NoError(t, err)
		assert.Empty(t, q) // unscoped: no query param sent
	})
}
