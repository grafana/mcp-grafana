//go:build unit

package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	mcpgrafana "github.com/grafana/mcp-grafana/v2"
	"github.com/stretchr/testify/require"
)

// TestListGrafanaRules_LabelSelectorsPageBeforeLimit covers #1544.
func TestListGrafanaRules_LabelSelectorsPageBeforeLimit(t *testing.T) {
	rule := func(name, severity string) map[string]any {
		return map[string]any{"name": name, "uid": name, "labels": map[string]string{"severity": severity}}
	}
	pages := map[string]map[string]any{
		"": {"groupNextToken": "p2", "groups": []any{
			map[string]any{"name": "a", "rules": []any{rule("a1", "warning"), rule("a2", "critical")}},
		}},
		"p2": {"groups": []any{
			map[string]any{"name": "b", "rules": []any{rule("b1", "critical"), rule("b2", "critical"), rule("b3", "warning")}},
		}},
	}
	var tokens []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/prometheus/grafana/api/v1/rules", r.URL.Path)
		require.Equal(t, "2", r.URL.Query().Get("rule_limit"))
		token := r.URL.Query().Get("group_next_token")
		tokens = append(tokens, token)
		page, ok := pages[token]
		require.True(t, ok, "unknown token %q", token)
		w.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(w).Encode(map[string]any{"status": "success", "data": page}))
	}))
	t.Cleanup(server.Close)
	ctx := mcpgrafana.WithGrafanaConfig(t.Context(), mcpgrafana.GrafanaConfig{URL: server.URL, APIKey: "test-api-key"})

	type listResult struct {
		Rules      []alertRuleSummary `json:"rules"`
		Truncated  bool               `json:"truncated"`
		NextCursor string             `json:"next_cursor"`
	}
	list := func(cursor string) listResult {
		t.Helper()
		var params ManageRulesReadParams
		require.NoError(t, json.Unmarshal([]byte(`{"operation":"list","rule_limit":2,`+
			`"label_selectors":["{severity=\"critical\"}"],"cursor":"`+cursor+`"}`), &params))
		result, err := manageRulesRead(ctx, params)
		require.NoError(t, err)
		b, err := json.Marshal(result)
		require.NoError(t, err)
		var out listResult
		require.NoError(t, json.Unmarshal(b, &out), "list result: %s", b)
		return out
	}
	titles := func(rules []alertRuleSummary) []string {
		out := make([]string, 0, len(rules))
		for _, r := range rules {
			out = append(out, r.Title)
		}
		return out
	}

	first := list("")
	require.Equal(t, []string{"a2", "b1"}, titles(first.Rules), "matches span both pages, limit applies after filtering")
	require.True(t, first.Truncated)
	require.NotEmpty(t, first.NextCursor)
	require.Equal(t, []string{"", "p2"}, tokens)

	second := list(first.NextCursor)
	require.Equal(t, []string{"b2"}, titles(second.Rules), "cursor resumes inside the cut page")
	require.False(t, second.Truncated)
	require.Empty(t, second.NextCursor)
}

func TestCollectRules(t *testing.T) {
	pages := map[string]struct {
		rules []string
		next  string
	}{"": {[]string{"a", "b"}, "p2"}, "p2": {[]string{"c"}, ""}}
	fetch := func(_ context.Context, token string) ([]alertRuleSummary, string, error) {
		p := pages[token]
		out := make([]alertRuleSummary, 0, len(p.rules))
		for _, r := range p.rules {
			out = append(out, alertRuleSummary{Title: r})
		}
		return out, p.next, nil
	}
	collect := func(limit int, cursor string) ([]string, *alertRuleList) {
		list, err := collectRules(t.Context(), fetch, limit, cursor)
		require.NoError(t, err)
		var titles []string
		for _, r := range list.Rules {
			titles = append(titles, r.Title)
		}
		return titles, list
	}

	t.Run("limit at page end with a next token", func(t *testing.T) {
		got, list := collect(2, "")
		require.Equal(t, []string{"a", "b"}, got)
		require.True(t, list.Truncated)
		got, list = collect(2, list.NextCursor)
		require.Equal(t, []string{"c"}, got)
		require.False(t, list.Truncated)
	})
	t.Run("everything fits", func(t *testing.T) {
		got, list := collect(5, "")
		require.Equal(t, []string{"a", "b", "c"}, got)
		require.False(t, list.Truncated)
		require.Empty(t, list.NextCursor)
	})
	t.Run("invalid cursor", func(t *testing.T) {
		_, err := collectRules(t.Context(), fetch, 2, "not-a-cursor!")
		require.ErrorContains(t, err, "invalid cursor")
	})
}
