//go:build unit

package tools

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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

// In legacy mode a missing route surfaces as a 404 (the deprecated
// numeric-id routes are disabled by default on Grafana 13+); the legacy
// transport must retry the alternate path in that case — and only that case.
func TestFallbackTransport_LegacyRetriesOnNotFoundOnly(t *testing.T) {
	resetFallbackCache()

	var requests []*http.Request
	mock := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		requests = append(requests, req)
		if strings.Contains(req.URL.Path, "/api/datasources/proxy/4/") {
			return newMockResponse(http.StatusNotFound), nil
		}
		return newMockResponse(http.StatusOK), nil
	})

	rt := newLegacyDatasourceFallbackTransport(mock,
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
