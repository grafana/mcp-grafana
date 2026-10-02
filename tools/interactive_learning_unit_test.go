//go:build unit

package tools

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/grafana/grafana-openapi-client-go/models"
	mcpgrafana "github.com/grafana/mcp-grafana/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// interactiveLearningServer serves an empty datasource list and, depending on
// pluginStatus, the Interactive Learning plugin settings.
func interactiveLearningServer(t *testing.T, pluginStatus int, pluginEnabled bool) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/datasources":
			_, _ = w.Write([]byte("[]"))
		case "/api/plugins/" + interactiveLearningPluginID + "/settings":
			w.WriteHeader(pluginStatus)
			if pluginStatus == http.StatusOK {
				_ = json.NewEncoder(w).Encode(map[string]any{"id": interactiveLearningPluginID, "enabled": pluginEnabled})
			}
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(func() {
		server.Close()
		interactiveLearningCache.Clear()
	})
	return server
}

func TestListDatasources_InteractiveLearningHint(t *testing.T) {
	t.Run("empty result with plugin enabled includes hint", func(t *testing.T) {
		server := interactiveLearningServer(t, http.StatusOK, true)
		result, err := listDatasources(mockDatasourcesCtx(server), ListDatasourcesParams{Type: "tempo"})
		require.NoError(t, err)
		assert.Contains(t, result.Hint, "No tempo datasource is configured")
		assert.Contains(t, result.Hint, "My Learning")
		assert.Contains(t, result.Hint, server.URL+"/a/grafana-pathfinder-app")
		assert.NotContains(t, result.Hint, "Pathfinder")
	})

	t.Run("no type filter says no datasources", func(t *testing.T) {
		server := interactiveLearningServer(t, http.StatusOK, true)
		result, err := listDatasources(mockDatasourcesCtx(server), ListDatasourcesParams{})
		require.NoError(t, err)
		assert.Contains(t, result.Hint, "No datasources are configured")
	})

	t.Run("name filter miss is not a hint", func(t *testing.T) {
		server := interactiveLearningServer(t, http.StatusOK, true)
		result, err := listDatasources(mockDatasourcesCtx(server), ListDatasourcesParams{Type: "tempo", Name: "prod"})
		require.NoError(t, err)
		assert.Empty(t, result.Hint)
	})

	t.Run("plugin not installed gives no hint", func(t *testing.T) {
		server := interactiveLearningServer(t, http.StatusNotFound, false)
		result, err := listDatasources(mockDatasourcesCtx(server), ListDatasourcesParams{Type: "tempo"})
		require.NoError(t, err)
		assert.Empty(t, result.Hint)
	})

	t.Run("plugin disabled gives no hint", func(t *testing.T) {
		server := interactiveLearningServer(t, http.StatusOK, false)
		result, err := listDatasources(mockDatasourcesCtx(server), ListDatasourcesParams{Type: "tempo"})
		require.NoError(t, err)
		assert.Empty(t, result.Hint)
	})

	t.Run("flag disables the hint", func(t *testing.T) {
		server := interactiveLearningServer(t, http.StatusOK, true)
		ctx := mockDatasourcesCtx(server)
		cfg := mcpgrafana.GrafanaConfigFromContext(ctx)
		cfg.DisableInteractiveLearningHints = true
		ctx = mcpgrafana.WithGrafanaConfig(ctx, cfg)

		result, err := listDatasources(ctx, ListDatasourcesParams{Type: "tempo"})
		require.NoError(t, err)
		assert.Empty(t, result.Hint)
	})

	t.Run("non-empty result has no hint", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(createMockDatasources(2))
		}))
		defer server.Close()
		result, err := listDatasources(mockDatasourcesCtx(server), ListDatasourcesParams{})
		require.NoError(t, err)
		assert.Empty(t, result.Hint)
	})
}

// deadEndServer answers every datasource-by-UID lookup with 404, serves list as
// the datasource list, and reports the Interactive Learning plugin as enabled.
func deadEndServer(t *testing.T, list []*models.DataSource) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/datasources":
			_ = json.NewEncoder(w).Encode(list)
		case r.URL.Path == "/api/plugins/"+interactiveLearningPluginID+"/settings":
			_ = json.NewEncoder(w).Encode(map[string]any{"id": interactiveLearningPluginID, "enabled": true})
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"not found"}`))
		}
	}))
	t.Cleanup(func() {
		server.Close()
		interactiveLearningCache.Clear()
	})
	return server
}

func TestQueryToolDeadEnds_InteractiveLearningHint(t *testing.T) {
	lookups := map[string]struct {
		dsType string
		lookup func(ctx context.Context) error
	}{
		"prometheus": {"prometheus", func(ctx context.Context) error {
			_, err := backendForDatasource(ctx, "missing")
			return err
		}},
		"loki": {"loki", func(ctx context.Context) error {
			_, err := lokiBackendForDatasource(ctx, "missing")
			return err
		}},
		"tempo": {"tempo", func(ctx context.Context) error {
			_, err := tempoBackendForDatasource(ctx, "missing")
			return err
		}},
		"pyroscope": {"grafana-pyroscope-datasource", func(ctx context.Context) error {
			_, err := newPyroscopeClient(ctx, "missing")
			return err
		}},
	}

	for name, tc := range lookups {
		t.Run(name+" with no datasource of that type gets a hint", func(t *testing.T) {
			server := deadEndServer(t, nil)
			err := tc.lookup(mockDatasourcesCtx(server))
			require.Error(t, err)
			assert.Contains(t, err.Error(), "datasource with UID 'missing' not found")
			assert.Contains(t, err.Error(), "My Learning")
			assert.Contains(t, err.Error(), server.URL+"/a/grafana-pathfinder-app")
			assert.NotContains(t, err.Error(), "Pathfinder")
			var notFound datasourceNotFoundError
			assert.ErrorAs(t, err, &notFound)
		})

		t.Run(name+" with a datasource of that type is a plain not-found", func(t *testing.T) {
			server := deadEndServer(t, []*models.DataSource{{ID: 1, UID: "other", Name: "other", Type: tc.dsType}})
			err := tc.lookup(mockDatasourcesCtx(server))
			require.Error(t, err)
			assert.Contains(t, err.Error(), "datasource with UID 'missing' not found")
			assert.NotContains(t, err.Error(), "My Learning")
		})

		t.Run(name+" hint is off when the flag is set", func(t *testing.T) {
			server := deadEndServer(t, nil)
			ctx := mockDatasourcesCtx(server)
			cfg := mcpgrafana.GrafanaConfigFromContext(ctx)
			cfg.DisableInteractiveLearningHints = true
			err := tc.lookup(mcpgrafana.WithGrafanaConfig(ctx, cfg))
			require.Error(t, err)
			assert.NotContains(t, err.Error(), "My Learning")
		})
	}

	t.Run("errors other than not-found are untouched", func(t *testing.T) {
		server := deadEndServer(t, nil)
		orig := errors.New("boom")
		assert.Same(t, orig, withMissingDatasourceHint(mockDatasourcesCtx(server), orig, "tempo", "Tempo"))
	})
}
