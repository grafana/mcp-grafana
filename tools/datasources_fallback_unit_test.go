//go:build unit

package tools

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	mcpgrafana "github.com/grafana/mcp-grafana"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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

func TestGetDatasourceByUID_FrontendSettingsFallback(t *testing.T) {
	server := newFallbackTestServer()
	defer server.Close()
	ctx := mockDatasourcesCtx(server)

	ds, err := getDatasourceByUID(ctx, GetDatasourceByUIDParams{UID: "prom-uid"})
	require.NoError(t, err)
	assert.Equal(t, int64(4), ds.ID)
	assert.Equal(t, "prom-uid", ds.UID)
	assert.Equal(t, "Main Prom", ds.Name)
	assert.Equal(t, "prometheus", ds.Type)
	assert.True(t, ds.IsDefault)

	// An unknown uid surfaces as not-found — not as the misleading
	// permission error from the metadata API.
	_, err = getDatasourceByUID(ctx, GetDatasourceByUIDParams{UID: "nope"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
	assert.NotContains(t, err.Error(), "Permission denied")
}

func TestGetDatasourceByName_FrontendSettingsFallback(t *testing.T) {
	server := newFallbackTestServer()
	defer server.Close()
	ctx := mockDatasourcesCtx(server)

	ds, err := getDatasourceByName(ctx, GetDatasourceByNameParams{Name: "Loki"})
	require.NoError(t, err)
	assert.Equal(t, int64(7), ds.ID)
	assert.Equal(t, "loki-uid", ds.UID)
	assert.Equal(t, "loki", ds.Type)
	assert.False(t, ds.IsDefault)

	// Name matching is case-insensitive, mirroring the metadata API.
	ds, err = getDatasourceByName(ctx, GetDatasourceByNameParams{Name: "loki"})
	require.NoError(t, err)
	assert.Equal(t, int64(7), ds.ID)

	// An unknown name surfaces as not-found, not as a permission error.
	_, err = getDatasourceByName(ctx, GetDatasourceByNameParams{Name: "asda"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
	assert.NotContains(t, err.Error(), "Permission denied")
}

// Health checks need the metadata API and the uid-based health route; on
// deployments where the fallback engages they cannot work, and the tool must
// say so explicitly instead of surfacing a permission error.
func TestCheckDatasourcesHealth_UnsupportedOnFallbackDeployments(t *testing.T) {
	server := newFallbackTestServer()
	defer server.Close()
	ctx := mockDatasourcesCtx(server)

	_, err := checkDatasourcesHealth(ctx, BulkCheckDatasourceHealthParams{UIDs: []string{"prom-uid"}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not supported")
	assert.NotContains(t, err.Error(), "Permission denied")
}

func TestListDatasources_FrontendSettingsFallback(t *testing.T) {
	server := newFallbackTestServer()
	defer server.Close()
	ctx := mockDatasourcesCtx(server)

	result, err := listDatasources(ctx, ListDatasourcesParams{})
	require.NoError(t, err)
	// The "-- Grafana --" pseudo datasource (id -1, uid "grafana" on 8.5) is
	// skipped: only entries with positive ids are real datasources.
	assert.Equal(t, 2, result.Total)
	require.Len(t, result.Datasources, 2)
	for _, ds := range result.Datasources {
		assert.Positive(t, ds.ID, "pseudo datasource leaked into the list: %+v", ds)
	}
	assert.Equal(t, "Loki", result.Datasources[0].Name)
	assert.Equal(t, "Main Prom", result.Datasources[1].Name)
	assert.True(t, result.Datasources[1].IsDefault)

	filtered, err := listDatasources(ctx, ListDatasourcesParams{Type: "loki"})
	require.NoError(t, err)
	assert.Equal(t, 1, filtered.Total)
}

func TestFallbackProxyBase_NumericID(t *testing.T) {
	server := newFallbackTestServer()
	defer server.Close()
	ctx := mockDatasourcesCtx(server)

	// Before any fallback resolution, there is no numeric base to use.
	_, _, ok := fallbackProxyBases(ctx, "prom-uid")
	assert.False(t, ok)

	// Resolving through the fallback records the numeric id...
	_, err := getDatasourceByUID(ctx, GetDatasourceByUIDParams{UID: "prom-uid"})
	require.NoError(t, err)

	// ...after which the numeric-id proxy base is available for the client
	// constructors to route through directly.
	primary, secondary, ok := fallbackProxyBases(ctx, "prom-uid")
	require.True(t, ok)
	assert.Equal(t, "/api/datasources/proxy/4", primary)
	assert.Equal(t, "/api/datasources/proxy/uid/prom-uid", secondary)

	// Datasources the fallback never resolved keep the modern uid routes.
	_, _, ok = fallbackProxyBases(ctx, "untouched-uid")
	assert.False(t, ok)
}

// Resolved ids are scoped per org and credential material: in multi-tenant
// HTTP mode, requests for a different org (or with a different token) on the
// same Grafana URL must not observe another tenant's cached numeric id.
func TestFallbackProxyIDs_ScopedPerOrgAndCredential(t *testing.T) {
	server := newFallbackTestServer()
	defer server.Close()
	ctxA := mockDatasourcesCtx(server)

	_, err := getDatasourceByUID(ctxA, GetDatasourceByUIDParams{UID: "prom-uid"})
	require.NoError(t, err)
	primary, _, ok := fallbackProxyBases(ctxA, "prom-uid")
	require.True(t, ok)
	require.Equal(t, "/api/datasources/proxy/4", primary)

	// Same URL, different org: no cached id visible.
	ctxB := mcpgrafana.WithGrafanaConfig(ctxA, mcpgrafana.GrafanaConfig{URL: server.URL, OrgID: 2})
	_, _, ok = fallbackProxyBases(ctxB, "prom-uid")
	assert.False(t, ok)

	// Same URL, different credentials: also isolated. Covers every auth
	// mechanism, including basic auth and ExtraHeaders-based tenants.
	ctxC := mcpgrafana.WithGrafanaConfig(ctxA, mcpgrafana.GrafanaConfig{URL: server.URL, APIKey: "another-tenant-token"})
	_, _, ok = fallbackProxyBases(ctxC, "prom-uid")
	assert.False(t, ok)

	ctxD := mcpgrafana.WithGrafanaConfig(ctxA, mcpgrafana.GrafanaConfig{URL: server.URL, BasicAuth: url.UserPassword("tenant", "secret")})
	_, _, ok = fallbackProxyBases(ctxD, "prom-uid")
	assert.False(t, ok)

	ctxE := mcpgrafana.WithGrafanaConfig(ctxA, mcpgrafana.GrafanaConfig{URL: server.URL, ExtraHeaders: map[string]string{"X-Tenant-Auth": "abc"}})
	_, _, ok = fallbackProxyBases(ctxE, "prom-uid")
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
	ds, err := getDatasourceByUID(ctx, GetDatasourceByUIDParams{UID: "first-uid"})
	require.NoError(t, err)
	require.Equal(t, int64(10), ds.ID)

	// Resolving "conflict" must match the second datasource's uid, not the
	// first datasource's name...
	ds, err = getDatasourceByUID(ctx, GetDatasourceByUIDParams{UID: "conflict"})
	require.NoError(t, err)
	assert.Equal(t, int64(20), ds.ID)

	// ...and the cached proxy bases must follow the same precedence, with the
	// uid-based fallback built from the winning datasource's real uid.
	primary, secondary, ok := fallbackProxyBases(ctx, "conflict")
	require.True(t, ok)
	assert.Equal(t, "/api/datasources/proxy/20", primary)
	assert.Equal(t, "/api/datasources/proxy/uid/conflict", secondary)
}

// A missing route can surface as a 404 (e.g. the deprecated numeric-id routes
// are disabled by default on Grafana 13+); the fallback transport must retry
// the alternate path in that case too.
func TestFallbackTransport_RetriesOnNotFound(t *testing.T) {
	resetFallbackCache()

	var requests []*http.Request
	mock := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		requests = append(requests, req)
		if strings.Contains(req.URL.Path, "/api/datasources/proxy/4/") {
			return newMockResponse(http.StatusNotFound), nil
		}
		return newMockResponse(http.StatusOK), nil
	})

	rt := newDatasourceFallbackTransport(mock,
		"/api/datasources/proxy/4",
		"/api/datasources/proxy/uid/prom-uid",
	)

	req, _ := http.NewRequest("GET", "http://grafana.example.com/api/datasources/proxy/4/api/v1/query", nil)
	resp, err := rt.RoundTrip(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	require.Len(t, requests, 2)
	assert.Equal(t, "/api/datasources/proxy/uid/prom-uid/api/v1/query", requests[1].URL.Path)
}
