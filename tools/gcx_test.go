package tools

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/grafana/gcx/embed"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	mcpgrafana "github.com/grafana/mcp-grafana/v2"
)

func TestExecGcxAuth(t *testing.T) {
	var got http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"database":"ok"}`))
	}))
	defer srv.Close()

	tests := []struct {
		name string
		cfg  mcpgrafana.GrafanaConfig
		want map[string]string
	}{
		{
			name: "api key",
			cfg:  mcpgrafana.GrafanaConfig{URL: srv.URL, APIKey: "glsa_key", OrgID: 3, ExtraHeaders: map[string]string{"X-Extra": "1"}},
			want: map[string]string{"Authorization": "Bearer glsa_key", "X-Extra": "1", "X-Grafana-Org-Id": "3"},
		},
		{
			name: "on behalf of",
			cfg:  mcpgrafana.GrafanaConfig{URL: srv.URL, AccessToken: "access", IDToken: "id", APIKey: "ignored"},
			want: map[string]string{"X-Access-Token": "access", "X-Grafana-Id": "id", "Authorization": ""},
		},
		{
			name: "basic",
			cfg:  mcpgrafana.GrafanaConfig{URL: "http://unused.invalid", OverrideURL: srv.URL, BasicAuth: url.UserPassword("admin", "pw")},
			want: map[string]string{"Authorization": "Basic YWRtaW46cHc="},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got = nil
			ctx := mcpgrafana.WithGrafanaConfig(context.Background(), tt.cfg)
			out, err := execGcx(embed.AccessRead)(ctx, ExecGcxParams{Command: "api /api/health"})
			require.NoError(t, err)
			assert.JSONEq(t, `{"database":"ok"}`, out)
			for k, v := range tt.want {
				assert.Equal(t, v, got.Get(k), k)
			}
		})
	}
}

func TestExecGcxReadOnlyRefusesWrites(t *testing.T) {
	var methods []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		methods = append(methods, r.Method)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	ctx := mcpgrafana.WithGrafanaConfig(context.Background(), mcpgrafana.GrafanaConfig{URL: srv.URL, APIKey: "k"})

	_, err := execGcx(embed.AccessRead)(ctx, ExecGcxParams{Command: "api /api/folders -X POST -d '{\"title\":\"x\"}'"})
	require.Error(t, err)
	assert.NotContains(t, methods, http.MethodPost, "a read-only exec_gcx must never send a write")

	methods = nil
	_, err = execGcx(embed.AccessDelete)(ctx, ExecGcxParams{Command: "api /api/folders -X POST -d '{\"title\":\"x\"}'"})
	require.NoError(t, err)
	assert.Contains(t, methods, http.MethodPost)
}

func TestExecGcxErrors(t *testing.T) {
	ctx := mcpgrafana.WithGrafanaConfig(context.Background(), mcpgrafana.GrafanaConfig{URL: "http://127.0.0.1:1", APIKey: "k"})

	_, err := execGcx(embed.AccessRead)(ctx, ExecGcxParams{Command: "slo definitions list | jq ."})
	require.ErrorContains(t, err, "shell syntax")

	_, err = execGcx(embed.AccessRead)(ctx, ExecGcxParams{Command: "resources get dashboards --config /etc/passwd"})
	require.ErrorContains(t, err, "Not available when gcx is embedded")

	_, err = execGcx(embed.AccessRead)(context.Background(), ExecGcxParams{Command: "help"})
	require.ErrorContains(t, err, "url not configured")
}

func TestTruncateGcxOutput(t *testing.T) {
	assert.Equal(t, "short", truncateGcxOutput("short"))
	long := strings.Repeat("x", gcxOutputLimitBytes+10)
	out := truncateGcxOutput(long)
	assert.True(t, strings.HasPrefix(out, strings.Repeat("x", gcxOutputLimitBytes)+"\n[output truncated"))
}
