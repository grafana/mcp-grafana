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
			want: `sum(rate({a="1", namespace!~"vault|payments"}[5m])) / sum(rate({b="2", namespace!~"vault|payments"}[5m]))`,
		},
		{
			name: "brace inside a backtick line filter is not a selector",
			in:   "{app=\"x\"} |= `{\"json\": true}`",
			want: "{app=\"x\", namespace!~\"vault|payments\"} |= `{\"json\": true}`",
		},
		{
			name: "escaped quote inside a line filter does not end the string",
			in:   `{app="x"} |= "he said \"{\" loudly"`,
			want: `{app="x", namespace!~"vault|payments"} |= "he said \"{\" loudly"`,
		},
		{
			name: "brace inside a selector's own label value",
			in:   `{app="}"}`,
			want: `{app="}", namespace!~"vault|payments"}`,
		},
		{
			name: "brace inside a comment is not a selector",
			in:   "{app=\"x\"} # note: {not a selector}\n",
			want: "{app=\"x\", namespace!~\"vault|payments\"} # note: {not a selector}\n",
		},
		{
			name: "user formatting and spacing are preserved",
			in:   `sum by (level) (count_over_time({app="x"} |= "e" [5m]))`,
			want: `sum by (level) (count_over_time({app="x", namespace!~"vault|payments"} |= "e" [5m]))`,
		},
		{
			name: "trailing comma is not doubled",
			in:   `{app="x",}`,
			want: `{app="x", namespace!~"vault|payments"}`,
		},
		{
			name: "label format template braces are not selectors",
			in:   `{app="x"} | line_format "{{.msg}}"`,
			want: `{app="x", namespace!~"vault|payments"} | line_format "{{.msg}}"`,
		},
		{
			// Splicing lengthens the output, so the loop must keep indexing the
			// original query. Three selectors of differing lengths would expose
			// an offset that drifts; two symmetric ones might not.
			name: "three selectors of differing lengths",
			in:   `sum(rate({a="1"}[5m])) + sum(rate({bb="22"}[5m])) + sum(rate({ccc="333"}[5m]))`,
			want: `sum(rate({a="1", namespace!~"vault|payments"}[5m])) + sum(rate({bb="22", namespace!~"vault|payments"}[5m])) + sum(rate({ccc="333", namespace!~"vault|payments"}[5m]))`,
		},
		{
			name: "brace-bearing strings between selectors are skipped, both injected",
			in:   `sum(count_over_time({a="1"} |= "{skip}" [5m])) / sum(count_over_time({b="2"} |= "{me}" [5m]))`,
			want: `sum(count_over_time({a="1", namespace!~"vault|payments"} |= "{skip}" [5m])) / sum(count_over_time({b="2", namespace!~"vault|payments"} |= "{me}" [5m]))`,
		},
		{
			name: "selector after a comment is still injected",
			in:   "sum(rate({a=\"1\"}[5m])) # {nope}\n / sum(rate({b=\"2\"}[5m]))",
			want: "sum(rate({a=\"1\", namespace!~\"vault|payments\"}[5m])) # {nope}\n / sum(rate({b=\"2\", namespace!~\"vault|payments\"}[5m]))",
		},
		{
			name: "one selector already carrying the enforced label",
			in:   `sum(rate({a="1", namespace="vault"}[5m])) / sum(rate({b="2"}[5m]))`,
			want: `sum(rate({a="1", namespace="vault", namespace!~"vault|payments"}[5m])) / sum(rate({b="2", namespace!~"vault|payments"}[5m]))`,
		},
		{
			// A trailing comment would otherwise swallow the injected matchers
			// and the closing brace, and Loki would reject the whole query.
			name: "trailing comment inside the selector",
			in:   "{app=\"x\" # note\n}",
			want: "{app=\"x\" # note\n, namespace!~\"vault|payments\"}",
		},
		{
			name: "comment on its own line inside the selector",
			in:   "{app=\"x\"\n# note\n}",
			want: "{app=\"x\"\n# note\n, namespace!~\"vault|payments\"}",
		},
		{
			name: "real trailing comma before a comment is not doubled",
			in:   "{app=\"x\", # note\n}",
			want: "{app=\"x\", # note\nnamespace!~\"vault|payments\"}",
		},
		{
			// The comma is inside the comment, so it is not a real separator.
			name: "comma inside a comment still needs a real one",
			in:   "{app=\"x\" # note,\n}",
			want: "{app=\"x\" # note,\n, namespace!~\"vault|payments\"}",
		},
		{
			name: "brace inside a block comment is not a selector",
			in:   `{app="x"} /* {a="b"} */ |= "e"`,
			want: `{app="x", namespace!~"vault|payments"} /* {a="b"} */ |= "e"`,
		},
		{
			name: "brace inside a line comment is not a selector",
			in:   "{app=\"x\"} // {a=\"b\"}\n",
			want: "{app=\"x\", namespace!~\"vault|payments\"} // {a=\"b\"}\n",
		},
		{
			// Loki accepts //-comments inside a stream selector; the Prometheus
			// parser does not, so the blanked form is what gets validated.
			name: "line comment inside the selector",
			in:   "{app=\"x\" // note\n}",
			want: "{app=\"x\" // note\n, namespace!~\"vault|payments\"}",
		},
		{
			name: "block comment inside the selector",
			in:   `{app="x" /* note */}`,
			want: `{app="x" /* note */, namespace!~"vault|payments"}`,
		},
		{
			name: "block comment between matchers in the selector",
			in:   `{app="x" /* note */, tier="a"}`,
			want: `{app="x" /* note */, tier="a", namespace!~"vault|payments"}`,
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

	// The scan replaces a full LogQL parse, so a query whose stream selectors it
	// cannot locate has to be refused rather than forwarded to Loki unfiltered.
	t.Run("fails closed when no selector can be located", func(t *testing.T) {
		for _, q := range []string{
			``,                      // empty
			`not a logql query`,     // no selector at all
			`count_over_time([5m])`, // metric query with no selector
			`{app="x"`,              // unterminated selector
			// A stray quote makes the rest of the query string state, which
			// could otherwise hide a selector from the scan.
			`{app="x} |= "y"`,
		} {
			t.Run(q, func(t *testing.T) {
				_, err := enforceLogQL(ctx, q)
				assert.Error(t, err, "query %q must not be passed through unfiltered", q)
			})
		}
	})

	// Malformed tails that do not hide a selector are still enforced. They are
	// left for Loki to reject on syntax: the point is that the matchers are
	// present, so there is no unfiltered read either way.
	t.Run("malformed query is still enforced before Loki rejects it", func(t *testing.T) {
		for _, q := range []string{
			`{app="x"}}`,           // unbalanced closing brace
			`{app="x"} |= "oops`,   // unterminated string literal
			"{app=\"x\"} |= `oops", // unterminated backtick string
		} {
			t.Run(q, func(t *testing.T) {
				out, err := enforceLogQL(ctx, q)
				require.NoError(t, err)
				assert.Contains(t, out, `namespace!~"vault|payments"`)
			})
		}
	})
}

func TestFindStreamSelectors(t *testing.T) {
	t.Run("finds every selector", func(t *testing.T) {
		q := `sum(rate({a="1"}[5m])) / sum(rate({b="2"}[5m]))`
		spans, _, err := findStreamSelectors(q)
		require.NoError(t, err)
		require.Len(t, spans, 2)
		assert.Equal(t, `{a="1"}`, q[spans[0].start:spans[0].end])
		assert.Equal(t, `{b="2"}`, q[spans[1].start:spans[1].end])
	})

	t.Run("spans index the original query across strings and comments", func(t *testing.T) {
		q := "sum(rate({a=\"1\"} |= \"{skip}\" [5m])) # {nope}\n / sum(rate({bb=\"22\"}[5m]))"
		spans, _, err := findStreamSelectors(q)
		require.NoError(t, err)
		require.Len(t, spans, 2)
		assert.Equal(t, `{a="1"}`, q[spans[0].start:spans[0].end])
		assert.Equal(t, `{bb="22"}`, q[spans[1].start:spans[1].end])
	})

	t.Run("ignores braces inside strings and comments", func(t *testing.T) {
		q := "{a=\"1\"} |= \"{x}\" | line_format `{y}` # {z}"
		spans, _, err := findStreamSelectors(q)
		require.NoError(t, err)
		require.Len(t, spans, 1)
		assert.Equal(t, `{a="1"}`, q[spans[0].start:spans[0].end])
	})
}

func TestBlankLogQLComments(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{name: "no comment is untouched", in: `{a="1"} |= "x"`, want: `{a="1"} |= "x"`},
		{name: "hash comment", in: `{a="1"} # hi`, want: `{a="1"}     `},
		{name: "line comment", in: `{a="1"} // hi`, want: `{a="1"}      `},
		{name: "block comment", in: `{a="1"} /* hi */ x`, want: `{a="1"}          x`},
		{name: "unterminated block comment", in: `{a="1"} /* hi`, want: `{a="1"}      `},
		{name: "comment marker inside a string is not a comment", in: `{a="1"} |= "# no"`, want: `{a="1"} |= "# no"`},
		{name: "comment marker inside a backtick string", in: "{a=\"1\"} |= `/* no */`", want: "{a=\"1\"} |= `/* no */`"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := blankLogQLComments(tc.in)
			assert.Equal(t, tc.want, got)
			// Offsets must stay valid for splicing into the original.
			assert.Len(t, got, len(tc.in), "must preserve length")
		})
	}
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
