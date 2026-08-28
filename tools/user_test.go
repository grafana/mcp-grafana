//go:build unit

package tools

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	mcpgrafana "github.com/grafana/mcp-grafana"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func userInfoTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/user":
			_, _ = w.Write([]byte(`{"login":"admin","email":"admin@example.com","name":"Admin","isGrafanaAdmin":true,"orgId":1}`))
		case "/api/user/orgs":
			_, _ = w.Write([]byte(`[{"orgId":1,"name":"Main Org.","role":"Admin"},{"orgId":2,"name":"Second","role":"Editor"}]`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(ts.Close)
	return ts
}

func TestUserInfoTool(t *testing.T) {
	ts := userInfoTestServer(t)
	ctx := mcpgrafana.WithGrafanaConfig(context.Background(), mcpgrafana.GrafanaConfig{URL: ts.URL})

	t.Run("returns identity and accessible orgs", func(t *testing.T) {
		res, err := getUserInfo(ctx, UserInfoParams{})
		require.NoError(t, err)
		assert.Equal(t, "admin", res.Login)
		assert.Equal(t, "admin@example.com", res.Email)
		assert.True(t, res.IsGrafanaAdmin)
		assert.Equal(t, int64(1), res.CurrentOrgID)
		require.Len(t, res.Orgs, 2)
		assert.Equal(t, int64(2), res.Orgs[1].OrgID)
		assert.Equal(t, "Editor", res.Orgs[1].Role)
	})

	t.Run("usage explains startup-time multi-org when disabled", func(t *testing.T) {
		mcpgrafana.DynamicMultiOrgEnabled = false
		res, err := getUserInfo(ctx, UserInfoParams{})
		require.NoError(t, err)
		assert.Contains(t, res.Usage, "--dynamic-multi-org")
		assert.Contains(t, res.Usage, "GRAFANA_ORG_ID")
	})

	t.Run("usage explains per-call selection when enabled", func(t *testing.T) {
		mcpgrafana.DynamicMultiOrgEnabled = true
		t.Cleanup(func() { mcpgrafana.DynamicMultiOrgEnabled = false })
		res, err := getUserInfo(ctx, UserInfoParams{})
		require.NoError(t, err)
		assert.Contains(t, res.Usage, "orgId=")
	})

	t.Run("no usage note when only one org is accessible", func(t *testing.T) {
		single := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			switch r.URL.Path {
			case "/api/user":
				_, _ = w.Write([]byte(`{"login":"sa","orgId":1}`))
			case "/api/user/orgs":
				_, _ = w.Write([]byte(`[{"orgId":1,"name":"Main Org.","role":"Admin"}]`))
			default:
				w.WriteHeader(http.StatusNotFound)
			}
		}))
		t.Cleanup(single.Close)
		singleCtx := mcpgrafana.WithGrafanaConfig(context.Background(), mcpgrafana.GrafanaConfig{URL: single.URL})

		// Even with per-call selection enabled, a single org has nothing to switch to.
		mcpgrafana.DynamicMultiOrgEnabled = true
		t.Cleanup(func() { mcpgrafana.DynamicMultiOrgEnabled = false })
		res, err := getUserInfo(singleCtx, UserInfoParams{})
		require.NoError(t, err)
		require.Len(t, res.Orgs, 1)
		assert.Empty(t, res.Usage)
	})
}
