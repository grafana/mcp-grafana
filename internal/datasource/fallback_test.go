//go:build unit

package datasource

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/grafana/grafana-openapi-client-go/client"
	mcpgrafana "github.com/grafana/mcp-grafana/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func mockDatasourcesCtx(server *httptest.Server) context.Context {
	u, _ := url.Parse(server.URL)
	cfg := client.DefaultTransportConfig()
	cfg.Host = u.Host
	cfg.Schemes = []string{"http"}
	cfg.APIKey = "test"

	c := client.NewHTTPClientWithConfig(nil, cfg)
	ctx := mcpgrafana.WithGrafanaClient(context.Background(), &mcpgrafana.GrafanaClient{GrafanaHTTPAPI: c})
	return mcpgrafana.WithGrafanaConfig(ctx, mcpgrafana.GrafanaConfig{URL: server.URL})
}

// newFallbackTestServer simulates a Grafana deployment where the datasource
// metadata API is inaccessible to the token (as on Grafana 7.x, where it
// requires the Org Admin role) but /api/frontend/settings is served normally.
func newFallbackTestServer() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/frontend/settings" {
			_, _ = w.Write([]byte(`{
				"datasources": {
					"-- Grafana --": {"id": -1, "uid": "grafana", "type": "datasource"},
					"Main Prom": {"id": 4, "uid": "prom-uid", "type": "prometheus", "jsonData": {"httpMethod": "POST"}},
					"Loki": {"id": 7, "uid": "loki-uid", "type": "loki"}
				},
				"defaultDatasource": "Main Prom"
			}`))
			return
		}
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"message":"Permission denied"}`))
	}))
}

func TestByUID_FrontendSettingsFallback(t *testing.T) {
	server := newFallbackTestServer()
	defer server.Close()
	ctx := mockDatasourcesCtx(server)

	ds, err := ByUID(ctx, "prom-uid")
	require.NoError(t, err)
	assert.Equal(t, int64(4), ds.ID)
	assert.Equal(t, "prom-uid", ds.UID)
	assert.Equal(t, "Main Prom", ds.Name)
	assert.Equal(t, "prometheus", ds.Type)
	assert.True(t, ds.IsDefault)

	// An unknown uid surfaces as not-found — not as the misleading
	// permission error from the metadata API.
	_, err = ByUID(ctx, "nope")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
	assert.NotContains(t, err.Error(), "Permission denied")
}

func TestByName_FrontendSettingsFallback(t *testing.T) {
	server := newFallbackTestServer()
	defer server.Close()
	ctx := mockDatasourcesCtx(server)

	ds, err := ByName(ctx, "Loki")
	require.NoError(t, err)
	assert.Equal(t, int64(7), ds.ID)
	assert.Equal(t, "loki-uid", ds.UID)
	assert.Equal(t, "loki", ds.Type)
	assert.False(t, ds.IsDefault)

	// Name matching is case-insensitive, mirroring the metadata API.
	ds, err = ByName(ctx, "loki")
	require.NoError(t, err)
	assert.Equal(t, int64(7), ds.ID)

	// An unknown name surfaces as not-found, not as a permission error.
	_, err = ByName(ctx, "asda")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
	assert.NotContains(t, err.Error(), "Permission denied")
}

func TestProxyBases_NumericID(t *testing.T) {
	server := newFallbackTestServer()
	defer server.Close()
	ctx := mockDatasourcesCtx(server)

	// Before any fallback resolution, there is no numeric base to use.
	_, _, ok := ProxyBases(ctx, "prom-uid")
	assert.False(t, ok)

	// Resolving through the fallback records the numeric id...
	_, err := ByUID(ctx, "prom-uid")
	require.NoError(t, err)

	// ...after which the numeric-id proxy base is available for the client
	// constructors to route through directly.
	primary, secondary, ok := ProxyBases(ctx, "prom-uid")
	require.True(t, ok)
	assert.Equal(t, "/api/datasources/proxy/4", primary)
	assert.Equal(t, "/api/datasources/proxy/uid/prom-uid", secondary)

	// Datasources the fallback never resolved keep the modern uid routes.
	_, _, ok = ProxyBases(ctx, "untouched-uid")
	assert.False(t, ok)
}

// Resolved ids are scoped per org and credential material: in multi-tenant
// HTTP mode, requests for a different org (or with a different token) on the
// same Grafana URL must not observe another tenant's cached numeric id.
func TestFallbackProxyIDs_ScopedPerOrgAndCredential(t *testing.T) {
	server := newFallbackTestServer()
	defer server.Close()
	ctxA := mockDatasourcesCtx(server)

	_, err := ByUID(ctxA, "prom-uid")
	require.NoError(t, err)
	primary, _, ok := ProxyBases(ctxA, "prom-uid")
	require.True(t, ok)
	require.Equal(t, "/api/datasources/proxy/4", primary)

	// Same URL, different org: no cached id visible.
	ctxB := mcpgrafana.WithGrafanaConfig(ctxA, mcpgrafana.GrafanaConfig{URL: server.URL, OrgID: 2})
	_, _, ok = ProxyBases(ctxB, "prom-uid")
	assert.False(t, ok)

	// Same URL, different credentials: also isolated. Covers every auth
	// mechanism, including basic auth and ExtraHeaders-based tenants.
	ctxC := mcpgrafana.WithGrafanaConfig(ctxA, mcpgrafana.GrafanaConfig{URL: server.URL, APIKey: "another-tenant-token"})
	_, _, ok = ProxyBases(ctxC, "prom-uid")
	assert.False(t, ok)

	ctxD := mcpgrafana.WithGrafanaConfig(ctxA, mcpgrafana.GrafanaConfig{URL: server.URL, BasicAuth: url.UserPassword("tenant", "secret")})
	_, _, ok = ProxyBases(ctxD, "prom-uid")
	assert.False(t, ok)

	ctxE := mcpgrafana.WithGrafanaConfig(ctxA, mcpgrafana.GrafanaConfig{URL: server.URL, ExtraHeaders: map[string]string{"X-Tenant-Auth": "abc"}})
	_, _, ok = ProxyBases(ctxE, "prom-uid")
	assert.False(t, ok)
}

// A datasource whose name equals another datasource's uid must not shadow it:
// uid and name entries live in separate key spaces, and uid takes precedence,
// mirroring the resolution order of fallbackDatasourceByUID.
func TestFallbackProxyIDs_UIDAndNameDoNotCollide(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/frontend/settings" {
			// "First" is *named* "conflict"; "Second" has *uid* "conflict".
			_, _ = w.Write([]byte(`{
				"datasources": {
					"conflict": {"id": 10, "uid": "first-uid", "type": "prometheus"},
					"Second": {"id": 20, "uid": "conflict", "type": "prometheus"}
				},
				"defaultDatasource": "Second"
			}`))
			return
		}
		w.WriteHeader(http.StatusForbidden)
	}))
	defer server.Close()
	ctx := mockDatasourcesCtx(server)

	// Resolve the first datasource by its uid; this records its name entry
	// ("conflict") in the name key space.
	ds, err := ByUID(ctx, "first-uid")
	require.NoError(t, err)
	require.Equal(t, int64(10), ds.ID)

	// Resolving "conflict" must match the second datasource's uid, not the
	// first datasource's name...
	ds, err = ByUID(ctx, "conflict")
	require.NoError(t, err)
	assert.Equal(t, int64(20), ds.ID)

	// ...and the cached proxy bases must follow the same precedence, with the
	// uid-based fallback built from the winning datasource's real uid.
	primary, secondary, ok := ProxyBases(ctx, "conflict")
	require.True(t, ok)
	assert.Equal(t, "/api/datasources/proxy/20", primary)
	assert.Equal(t, "/api/datasources/proxy/uid/conflict", secondary)
}
