//go:build unit

package datasource

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"testing"

	"github.com/grafana/grafana-openapi-client-go/client"
	"github.com/grafana/grafana-openapi-client-go/models"
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

func newQueryOnlyDatasourceServer() *httptest.Server {
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

func TestList_FallsBackForQueryOnlyCaller(t *testing.T) {
	server := newQueryOnlyDatasourceServer()
	defer server.Close()

	list, err := List(mockDatasourcesCtx(server))
	require.NoError(t, err)
	require.Len(t, list, 2)
	assert.Equal(t, "Loki", list[0].Name)
	assert.Equal(t, "Main Prom", list[1].Name)
	assert.True(t, list[1].IsDefault)
}

func TestByUID_FallsBackForQueryOnlyCaller(t *testing.T) {
	server := newQueryOnlyDatasourceServer()
	defer server.Close()
	ctx := mockDatasourcesCtx(server)

	ds, err := ByUID(ctx, "prom-uid")
	require.NoError(t, err)
	assert.Equal(t, int64(4), ds.ID)
	assert.Equal(t, "Main Prom", ds.Name)
	assert.Equal(t, "prometheus", ds.Type)
	assert.True(t, ds.IsDefault)
	assert.Equal(t, models.JSON(map[string]any{"httpMethod": "POST"}), ds.JSONData)

	_, err = ByUID(ctx, "missing")
	require.ErrorIs(t, err, ErrNotFound)
}

func TestByName_FallsBackForQueryOnlyCaller(t *testing.T) {
	server := newQueryOnlyDatasourceServer()
	defer server.Close()
	ctx := mockDatasourcesCtx(server)

	ds, err := ByName(ctx, "loki")
	require.NoError(t, err)
	assert.Equal(t, "loki-uid", ds.UID)
	assert.Equal(t, "Loki", ds.Name)

	_, err = ByName(ctx, "missing")
	require.ErrorIs(t, err, ErrNotFound)
}

func TestByUID_NotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"Data source not found"}`))
	}))
	defer server.Close()

	_, err := ByUID(mockDatasourcesCtx(server), "missing")
	require.ErrorIs(t, err, ErrNotFound)
	assert.Equal(t, "datasource with UID 'missing' not found. Please check if the datasource exists and is accessible", err.Error())
}

func TestByName_NotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"Data source not found"}`))
	}))
	defer server.Close()

	_, err := ByName(mockDatasourcesCtx(server), "missing")
	require.ErrorIs(t, err, ErrNotFound)
	assert.Equal(t,
		"datasource with name 'missing' not found. Please check if the datasource exists and is accessible",
		err.Error(),
	)
}

// Only a 404 status means not found. Other failures remain server errors even
// when their text mentions 404.
func TestByUID_ServerErrorMentioning404IsNotNotFound(t *testing.T) {
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"message":"upstream returned 404"}`))
	}))
	defer server.Close()

	_, err := ByUID(mockDatasourcesCtx(server), "prom")
	require.Error(t, err)
	assert.False(t, errors.Is(err, ErrNotFound), "got %v", err)
	assert.False(t, slices.Contains(paths, "/api/frontend/settings"), "unexpected fallback requests: %v", paths)
}
