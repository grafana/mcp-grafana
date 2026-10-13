//go:build unit

package tools

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	mcpgrafana "github.com/grafana/mcp-grafana/v2"
	"github.com/stretchr/testify/require"
)

func TestListDatasourceAlertRulesPagination(t *testing.T) {
	rule := func(name, ruleType, team string) map[string]any {
		return map[string]any{"name": name, "type": ruleType, "query": "up", "labels": map[string]string{"team": team}}
	}
	type page struct {
		token  string
		rules  []map[string]any
		status int
	}
	for _, tt := range []struct {
		name    string
		pages   []page
		limit   int
		want    []string
		wantErr string
	}{
		{
			name: "filter each page before counting matches and stop at limit", limit: 2,
			pages: []page{
				{token: "second", rules: []map[string]any{rule("cpu-alert", "alerting", "ops"), rule("cpu-wrong-team", "recording", "other"), rule("memory", "recording", "ops")}},
				{token: "third", rules: []map[string]any{rule("prefix-cpu-first", "recording", "ops")}},
				{token: "must-not-fetch", rules: []map[string]any{rule("cpu-second", "recording", "ops"), rule("cpu-extra", "recording", "ops")}},
			}, want: []string{"prefix-cpu-first", "cpu-second"},
		},
		{
			name: "follow token on empty page", limit: 2,
			pages: []page{{token: "second"}, {rules: []map[string]any{rule("cpu-last", "recording", "ops")}}},
			want:  []string{"cpu-last"},
		},
		{
			name: "backend without pagination still applies result limit", limit: 1,
			pages: []page{{rules: []map[string]any{rule("cpu-first", "recording", "ops"), rule("cpu-extra", "recording", "ops")}}},
			want:  []string{"cpu-first"},
		},
		{
			name: "reject token cycle", limit: 2,
			pages:   []page{{token: "second"}, {token: "third"}, {token: "second"}},
			wantErr: "repeated a pagination token",
		},
		{
			name: "later page error does not return incomplete results", limit: 2,
			pages:   []page{{token: "second", rules: []map[string]any{rule("cpu-first", "recording", "ops")}}, {status: http.StatusInternalServerError}},
			wantErr: "status code 500",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Path == "/api/datasources/uid/prom-ds" {
					_, _ = w.Write([]byte(`{"uid":"prom-ds","type":"prometheus"}`))
					return
				}
				require.Equal(t, "/api/prometheus/prom-ds/api/v1/rules", r.URL.Path)
				require.Less(t, requests, len(tt.pages), "must stop requesting pages once complete")
				if requests == 0 {
					require.Empty(t, r.URL.Query().Get("group_next_token"))
				} else {
					require.Equal(t, tt.pages[requests-1].token, r.URL.Query().Get("group_next_token"))
				}
				p := tt.pages[requests]
				requests++
				if p.status != 0 {
					w.WriteHeader(p.status)
					return
				}
				require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
					"status": "success", "data": map[string]any{
						"groups": []any{map[string]any{"name": "group", "rules": p.rules}}, "groupNextToken": p.token,
					},
				}))
			}))
			t.Cleanup(server.Close)
			ctx := mockDatasourcesCtx(server)
			result, err := listDatasourceAlertRules(ctx, "prom-ds", &GetRulesOpts{RuleType: "recording", RuleName: "cpu", RuleLimit: tt.limit}, []Selector{{Filters: []LabelMatcher{{Name: "team", Type: "=", Value: "ops"}}}})
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				require.Nil(t, result)
			} else {
				require.NoError(t, err)
				var titles []string
				for _, r := range result {
					titles = append(titles, r.Title)
				}
				require.Equal(t, tt.want, titles)
			}
			require.Equal(t, len(tt.pages), requests)
		})
	}
}

func TestDatasourceRulesResponseLimit(t *testing.T) {
	for _, tt := range []struct {
		name    string
		extra   int
		gzip    bool
		wantErr bool
	}{
		{"exact limit", 0, false, false},
		{"oversized page", 1, false, true},
		{"compressed oversized page", 1, true, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			response := `{"status":"success","data":{"groups":[]}}`
			body := []byte(strings.Repeat(" ", int(defaultResponseLimitBytes)+tt.extra-len(response)) + response)
			if tt.gzip {
				var compressed bytes.Buffer
				writer := gzip.NewWriter(&compressed)
				_, err := writer.Write(body)
				require.NoError(t, err)
				require.NoError(t, writer.Close())
				body = compressed.Bytes()
				require.Less(t, len(body), int(defaultResponseLimitBytes))
			}
			server, client := setupMockServer(func(w http.ResponseWriter, r *http.Request) {
				if tt.gzip {
					w.Header().Set("Content-Encoding", "gzip")
				}
				w.(http.Flusher).Flush() // Exercise unknown Content-Length, too.
				_, _ = w.Write(body)
			})
			t.Cleanup(server.Close)
			result, err := client.GetDatasourceRules(t.Context(), "prom-ds", nil, "")
			if tt.wantErr {
				require.ErrorContains(t, err, "response body exceeds maximum size")
				require.Nil(t, result)
			} else {
				require.NoError(t, err)
				require.Empty(t, result.Groups)
			}
		})
	}
}

func TestListDatasourceAlertRulesLimitDefaults(t *testing.T) {
	for _, limit := range []int{0, 500} {
		t.Run(fmt.Sprint(limit), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Path == "/api/datasources/uid/prom-ds" {
					_, _ = w.Write([]byte(`{"uid":"prom-ds","type":"prometheus"}`))
					return
				}
				var rules []map[string]any
				for i := range 201 {
					rules = append(rules, map[string]any{"name": fmt.Sprint(i), "type": "recording", "query": "up"})
				}
				require.NoError(t, json.NewEncoder(w).Encode(map[string]any{"status": "success", "data": map[string]any{"groups": []any{map[string]any{"name": "group", "rules": rules}}}}))
			}))
			t.Cleanup(server.Close)
			result, err := listDatasourceAlertRules(mockDatasourcesCtx(server), "prom-ds", &GetRulesOpts{RuleLimit: limit}, nil)
			require.NoError(t, err)
			require.Len(t, result, 200)
			require.Equal(t, "199", result[199].Title)
		})
	}
}

func TestListDatasourceAlertRulesCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/datasources/uid/prom-ds" {
			_, _ = w.Write([]byte(`{"uid":"prom-ds","type":"prometheus"}`))
			return
		}
		if r.URL.Query().Get("group_next_token") == "second" {
			cancel()
			<-r.Context().Done()
			return
		}
		_, _ = w.Write([]byte(`{"status":"success","data":{"groups":[],"groupNextToken":"second"}}`))
	}))
	t.Cleanup(server.Close)
	// Preserve the test's cancellation while installing the mock clients.
	cfgCtx := mockDatasourcesCtx(server)
	ctx = mcpgrafana.WithGrafanaConfig(ctx, mcpgrafana.GrafanaConfigFromContext(cfgCtx))
	ctx = mcpgrafana.WithGrafanaClient(ctx, mcpgrafana.GrafanaClientFromContext(cfgCtx))
	result, err := listDatasourceAlertRules(ctx, "prom-ds", nil, nil)
	require.ErrorIs(t, err, context.Canceled)
	require.Nil(t, result)
}
