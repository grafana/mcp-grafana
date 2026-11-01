//go:build unit

package resources

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/grafana/grafana-openapi-client-go/client"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	mcpgrafana "github.com/grafana/mcp-grafana/v2"
)

func newGrafana(t *testing.T) *httptest.Server {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/datasources":
			_, _ = w.Write([]byte(`[
				{"id": 1, "uid": "prom", "name": "Prometheus", "type": "prometheus", "isDefault": true},
				{"id": 2, "uid": "loki", "name": "Loki", "type": "loki"}
			]`))
		case "/api/datasources/uid/prom":
			_, _ = w.Write([]byte(`{"id": 1, "uid": "prom", "name": "Prometheus", "type": "prometheus", "url": "http://prometheus:9090"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"not found"}`))
		}
	}))
	t.Cleanup(server.Close)
	return server
}

// connect serves AddDatasources plus a static resource, with a Grafana
// client for grafanaURL injected the way GrafanaContextMiddleware does.
func connect(t *testing.T, grafanaURL string) *mcp.ClientSession {
	t.Helper()
	s := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	s.AddResource(&mcp.Resource{URI: "ui://test/app.html", Name: "app"},
		func(context.Context, *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) { return nil, nil })
	AddDatasources(s)
	s.AddReceivingMiddleware(func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			u, _ := url.Parse(grafanaURL)
			cfg := client.DefaultTransportConfig()
			cfg.Host = u.Host
			cfg.Schemes = []string{"http"}
			ctx = mcpgrafana.WithGrafanaClient(ctx, &mcpgrafana.GrafanaClient{GrafanaHTTPAPI: client.NewHTTPClientWithConfig(nil, cfg)})
			ctx = mcpgrafana.WithGrafanaConfig(ctx, mcpgrafana.GrafanaConfig{URL: grafanaURL})
			return next(ctx, method, req)
		}
	})
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	go func() { _ = s.Run(context.Background(), serverTransport) }()
	session, err := mcp.NewClient(&mcp.Implementation{Name: "client", Version: "0"}, nil).Connect(t.Context(), clientTransport, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Close() })
	return session
}

func TestListDatasourceResources(t *testing.T) {
	session := connect(t, newGrafana(t).URL)

	result, err := session.ListResources(t.Context(), nil)
	require.NoError(t, err)
	byURI := map[string]*mcp.Resource{}
	for _, r := range result.Resources {
		byURI[r.URI] = r
	}
	require.Len(t, byURI, 3)
	assert.Contains(t, byURI, "ui://test/app.html")
	assert.Equal(t, "Prometheus", byURI["grafana://datasources/prom"].Name)
	assert.Equal(t, "prometheus datasource (default)", byURI["grafana://datasources/prom"].Description)
	assert.Equal(t, "loki datasource", byURI["grafana://datasources/loki"].Description)
	assert.Equal(t, "private", result.CacheScope)

	templates, err := session.ListResourceTemplates(t.Context(), nil)
	require.NoError(t, err)
	require.Len(t, templates.ResourceTemplates, 1)
	assert.Equal(t, "grafana://datasources/{uid}", templates.ResourceTemplates[0].URITemplate)
}

func TestListDatasourceResources_GrafanaUnavailable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	session := connect(t, server.URL)

	result, err := session.ListResources(t.Context(), nil)
	require.NoError(t, err)
	require.Len(t, result.Resources, 1)
	assert.Equal(t, "ui://test/app.html", result.Resources[0].URI)
}

func TestReadDatasourceResource(t *testing.T) {
	session := connect(t, newGrafana(t).URL)

	result, err := session.ReadResource(t.Context(), &mcp.ReadResourceParams{URI: "grafana://datasources/prom"})
	require.NoError(t, err)
	require.Len(t, result.Contents, 1)
	assert.Equal(t, "grafana://datasources/prom", result.Contents[0].URI)
	assert.Equal(t, "application/json", result.Contents[0].MIMEType)
	assert.Contains(t, result.Contents[0].Text, `"url":"http://prometheus:9090"`)

	_, err = session.ReadResource(t.Context(), &mcp.ReadResourceParams{URI: "grafana://datasources/missing"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}
