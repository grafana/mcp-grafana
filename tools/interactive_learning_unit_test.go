//go:build unit

package tools

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

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

// notFoundServer has one Prometheus datasource, answers every by-UID and
// by-name metadata lookup with lookupStatus, and serves the Interactive
// Learning plugin settings with pluginStatus and pluginEnabled. It records
// every request path.
type notFoundServer struct {
	*httptest.Server
	mu       sync.Mutex
	requests []string
}

func newNotFoundServer(t *testing.T, lookupStatus, pluginStatus int, pluginEnabled bool) *notFoundServer {
	t.Helper()
	s := &notFoundServer{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.requests = append(s.requests, r.URL.Path)
		s.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/plugins/"+interactiveLearningPluginID+"/settings":
			w.WriteHeader(pluginStatus)
			if pluginStatus == http.StatusOK {
				_ = json.NewEncoder(w).Encode(map[string]any{"id": interactiveLearningPluginID, "enabled": pluginEnabled})
			}
		case r.URL.Path == "/api/frontend/settings":
			if lookupStatus != http.StatusForbidden {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			_, _ = w.Write([]byte(`{"datasources": {"Prometheus": {"id": 1, "uid": "prometheus", "name": "Prometheus", "type": "prometheus"}}}`))
		case strings.HasPrefix(r.URL.Path, "/api/datasources/uid/"), strings.HasPrefix(r.URL.Path, "/api/datasources/name/"):
			w.WriteHeader(lookupStatus)
			_, _ = w.Write([]byte(`{"message":"lookup failed"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(func() {
		s.Close()
		interactiveLearningCache.Clear()
	})
	return s
}

func (s *notFoundServer) requested(path string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, p := range s.requests {
		if p == path {
			return true
		}
	}
	return false
}

func TestDatasourceNotFound_InteractiveLearningHint(t *testing.T) {
	lookups := map[string]func(ctx context.Context) error{
		"by uid": func(ctx context.Context) error {
			_, err := getDatasourceByUID(ctx, GetDatasourceByUIDParams{UID: "promethues"})
			return err
		},
		"by name": func(ctx context.Context) error {
			_, err := getDatasourceByName(ctx, GetDatasourceByNameParams{Name: "Promethues"})
			return err
		},
	}

	for name, lookup := range lookups {
		t.Run(name+" not-found gets the hint", func(t *testing.T) {
			status := http.StatusNotFound
			if name == "by name" {
				// The by-name metadata lookup reports not-found via the
				// frontend settings fallback.
				status = http.StatusForbidden
			}
			server := newNotFoundServer(t, status, http.StatusOK, true)
			err := lookup(mockDatasourcesCtx(server.Server))
			require.Error(t, err)
			assert.ErrorAs(t, err, new(datasourceNotFoundError))
			assert.Contains(t, err.Error(), "not found. Please check if the datasource exists and is accessible. Couldn't find that datasource.")
			assert.Contains(t, err.Error(), "Grafana's My Learning page suggests what to set up next: "+server.URL+"/a/grafana-pathfinder-app")
			assert.NotContains(t, err.Error(), "Pathfinder")
			assert.False(t, server.requested("/api/datasources"), "hint must not list datasources")
		})

		t.Run(name+" typo on an instance with datasources still gets the hint", func(t *testing.T) {
			server := newNotFoundServer(t, http.StatusForbidden, http.StatusOK, true)
			err := lookup(mockDatasourcesCtx(server.Server))
			require.Error(t, err)
			assert.ErrorAs(t, err, new(datasourceNotFoundError))
			assert.Contains(t, err.Error(), "My Learning")
		})

		suppressed := map[string]struct {
			pluginStatus  int
			pluginEnabled bool
			disable       bool
		}{
			"flag":            {http.StatusOK, true, true},
			"plugin missing":  {http.StatusNotFound, false, false},
			"plugin disabled": {http.StatusOK, false, false},
		}
		for reason, tc := range suppressed {
			t.Run(name+" hint is off when "+reason, func(t *testing.T) {
				server := newNotFoundServer(t, http.StatusForbidden, tc.pluginStatus, tc.pluginEnabled)
				ctx := mockDatasourcesCtx(server.Server)
				cfg := mcpgrafana.GrafanaConfigFromContext(ctx)
				cfg.DisableInteractiveLearningHints = tc.disable
				err := lookup(mcpgrafana.WithGrafanaConfig(ctx, cfg))
				require.Error(t, err)
				assert.ErrorAs(t, err, new(datasourceNotFoundError))
				assert.True(t, strings.HasSuffix(err.Error(), "Please check if the datasource exists and is accessible"), err.Error())
			})
		}

		t.Run(name+" errors other than not-found are untouched", func(t *testing.T) {
			server := newNotFoundServer(t, http.StatusInternalServerError, http.StatusOK, true)
			err := lookup(mockDatasourcesCtx(server.Server))
			require.Error(t, err)
			assert.False(t, errors.As(err, new(datasourceNotFoundError)))
			assert.NotContains(t, err.Error(), "My Learning")
		})
	}
}
