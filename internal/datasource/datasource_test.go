//go:build unit

package datasource

import (
	"context"
	"errors"
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
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"message":"upstream returned 404"}`))
	}))
	defer server.Close()

	_, err := ByUID(mockDatasourcesCtx(server), "prom")
	require.Error(t, err)
	assert.False(t, errors.Is(err, ErrNotFound), "got %v", err)
}
