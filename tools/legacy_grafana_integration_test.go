// Requires the grafana-legacy-8 docker-compose service: Grafana 8.5 on
// localhost:3003 with the shared testdata provisioning. Grafana 8.5 predates
// the uid-based datasource routes (added in 9.0) and serves the datasource
// metadata API to Org Admins only, so a basic-auth editor user exercises the
// frontend-settings datasource fallback and the numeric-id proxy routing end
// to end — the exact scenario from
// https://github.com/grafana/mcp-grafana/pull/1015.
//
// Run with `go test -tags integration`.
//go:build integration

package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"testing"

	"github.com/go-openapi/strfmt"
	"github.com/grafana/grafana-openapi-client-go/client"
	mcpgrafana "github.com/grafana/mcp-grafana"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func legacyGrafana8URL() string {
	if u, ok := os.LookupEnv("GRAFANA_LEGACY_8_URL"); ok {
		return u
	}
	return "http://localhost:3003"
}

// ensureLegacyEditorUser idempotently creates a basic-auth `editor`/`editor`
// user with the Editor org role, using the default admin credentials. Grafana
// cannot provision users from files, so the test creates it at runtime.
func ensureLegacyEditorUser(t *testing.T, base string) {
	t.Helper()

	do := func(method, path string, payload any) *http.Response {
		var body bytes.Buffer
		if payload != nil {
			require.NoError(t, json.NewEncoder(&body).Encode(payload))
		}
		req, err := http.NewRequest(method, base+path, &body)
		require.NoError(t, err)
		req.SetBasicAuth("admin", "admin")
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		return resp
	}

	resp := do(http.MethodPost, "/api/admin/users", map[string]any{
		"name": "editor", "login": "editor", "password": "editor", "OrgId": 1,
	})
	defer resp.Body.Close() //nolint:errcheck
	// 412 means the user already exists from an earlier run.
	require.Contains(t, []int{http.StatusOK, http.StatusPreconditionFailed}, resp.StatusCode)

	lookup := do(http.MethodGet, "/api/users/lookup?loginOrEmail=editor", nil)
	defer lookup.Body.Close() //nolint:errcheck
	require.Equal(t, http.StatusOK, lookup.StatusCode)
	var user struct {
		ID int64 `json:"id"`
	}
	require.NoError(t, json.NewDecoder(lookup.Body).Decode(&user))

	role := do(http.MethodPatch, fmt.Sprintf("/api/org/users/%d", user.ID), map[string]any{"role": "Editor"})
	defer role.Body.Close() //nolint:errcheck
	require.Equal(t, http.StatusOK, role.StatusCode)
}

// newLegacyEditorContext builds a context authenticated as the basic-auth
// editor user. It mirrors newTestContextForURL, which hardcodes admin
// credentials and would defeat the point of this test.
func newLegacyEditorContext(grafanaURL string) context.Context {
	cfg := client.DefaultTransportConfig()
	parsed, err := url.Parse(grafanaURL)
	if err != nil {
		panic(fmt.Errorf("invalid Grafana URL %q: %w", grafanaURL, err))
	}
	cfg.Host = parsed.Host
	if parsed.Scheme == "http" {
		cfg.Schemes = []string{"http"}
	} else {
		cfg.Schemes = []string{"https"}
	}
	cfg.BasicAuth = url.UserPassword("editor", "editor")

	c := client.NewHTTPClientWithConfig(strfmt.Default, cfg)

	ctx := mcpgrafana.WithGrafanaConfig(context.Background(), mcpgrafana.GrafanaConfig{
		Debug:     true,
		URL:       grafanaURL,
		BasicAuth: cfg.BasicAuth,
	})
	return mcpgrafana.WithGrafanaClient(ctx, &mcpgrafana.GrafanaClient{GrafanaHTTPAPI: c})
}

func TestLegacyGrafana8DatasourceFallback(t *testing.T) {
	base := legacyGrafana8URL()
	ensureLegacyEditorUser(t, base)
	ctx := newLegacyEditorContext(base)

	t.Run("list datasources via frontend settings", func(t *testing.T) {
		result, err := listDatasources(ctx, ListDatasourcesParams{})
		require.NoError(t, err)
		var found bool
		for _, ds := range result.Datasources {
			if ds.UID == "prometheus" {
				found = true
				assert.Equal(t, int64(1), ds.ID)
				assert.Equal(t, "prometheus", ds.Type)
			}
		}
		assert.True(t, found, "provisioned Prometheus datasource not resolved: %+v", result.Datasources)
	})

	t.Run("get datasource by uid", func(t *testing.T) {
		ds, err := getDatasourceByUID(ctx, GetDatasourceByUIDParams{UID: "prometheus"})
		require.NoError(t, err)
		assert.Equal(t, int64(1), ds.ID)
		assert.Equal(t, "prometheus", ds.Type)
	})

	t.Run("query prometheus instant", func(t *testing.T) {
		result, err := queryPrometheus(ctx, QueryPrometheusParams{
			DatasourceUID: "prometheus",
			Expr:          "time()",
			QueryType:     "instant",
			EndTime:       "now-1m",
		})
		require.NoError(t, err)
		require.NotNil(t, result)
		assert.NotEmpty(t, result.String())
	})

	t.Run("get datasource by lowercase name", func(t *testing.T) {
		ds, err := getDatasourceByName(ctx, GetDatasourceByNameParams{Name: "prometheus"})
		require.NoError(t, err)
		assert.Equal(t, int64(1), ds.ID)
		assert.Equal(t, "Prometheus", ds.Name)
	})

	t.Run("list excludes pseudo datasources", func(t *testing.T) {
		result, err := listDatasources(ctx, ListDatasourcesParams{})
		require.NoError(t, err)
		for _, ds := range result.Datasources {
			assert.Positive(t, ds.ID, "pseudo datasource leaked into the list: %+v", ds)
		}
	})

	t.Run("unknown uid reports not found", func(t *testing.T) {
		_, err := getDatasourceByUID(ctx, GetDatasourceByUIDParams{UID: "does-not-exist"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
		assert.NotContains(t, err.Error(), "Permission denied")
	})

	t.Run("datasource health reports unsupported", func(t *testing.T) {
		_, err := checkDatasourcesHealth(ctx, BulkCheckDatasourceHealthParams{UIDs: []string{"prometheus"}})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not supported")
	})

	t.Run("list prometheus label names", func(t *testing.T) {
		names, err := listPrometheusLabelNames(ctx, ListPrometheusLabelNamesParams{DatasourceUID: "prometheus"})
		require.NoError(t, err)
		assert.NotEmpty(t, names)
	})
}
