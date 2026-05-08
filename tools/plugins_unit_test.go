package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	mcpgrafana "github.com/grafana/mcp-grafana"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func catalogTestServer(t *testing.T, items []catalogPlugin) *httptest.Server {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(catalogListResponse{Items: items})
	}))
	t.Cleanup(func() {
		grafanaComCatalogURL = "https://grafana.com/api/plugins"
		ts.Close()
	})
	grafanaComCatalogURL = ts.URL
	return ts
}

func pluginTestContext(t *testing.T, serverURL string) context.Context {
	t.Helper()
	cfg := mcpgrafana.GrafanaConfig{URL: serverURL}
	return mcpgrafana.WithGrafanaConfig(context.Background(), cfg)
}

func TestGetPlugin_Found(t *testing.T) {
	payload := pluginSettingsResponse{
		ID:      "grafana-piechart-panel",
		Name:    "Pie Chart",
		Type:    "panel",
		Enabled: true,
	}
	payload.Info.Version = "2.0.1"

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/plugins/grafana-piechart-panel/settings", r.URL.Path)
		assert.Equal(t, http.MethodGet, r.Method)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(payload)
	}))
	t.Cleanup(ts.Close)

	ctx := pluginTestContext(t, ts.URL)
	result, err := getPlugin(ctx, GetPluginParams{PluginID: "grafana-piechart-panel"})

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.True(t, result.Installed)
	assert.Equal(t, "grafana-piechart-panel", result.PluginID)
	assert.Equal(t, "Pie Chart", result.Name)
	assert.Equal(t, "2.0.1", result.Version)
	assert.Equal(t, "panel", result.Type)
	require.NotNil(t, result.Enabled)
	assert.True(t, *result.Enabled)
}

func TestGetPlugin_NotFound(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"message":"Plugin not found"}`, http.StatusNotFound)
	}))
	t.Cleanup(ts.Close)

	ctx := pluginTestContext(t, ts.URL)
	result, err := getPlugin(ctx, GetPluginParams{PluginID: "grafana-nonexistent-panel"})

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.False(t, result.Installed)
	assert.Equal(t, "grafana-nonexistent-panel", result.PluginID)
	assert.Empty(t, result.Name)
	assert.Empty(t, result.Version)
	assert.Nil(t, result.Enabled)

	serialized, err := json.Marshal(result)
	require.NoError(t, err)
	assert.NotContains(t, string(serialized), "enabled")
}

func TestGetPlugin_UnexpectedStatus(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal error", http.StatusInternalServerError)
	}))
	t.Cleanup(ts.Close)

	ctx := pluginTestContext(t, ts.URL)
	result, err := getPlugin(ctx, GetPluginParams{PluginID: "some-plugin"})

	require.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "500")
}

func TestGetPlugin_MalformedJSON(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{not valid json`))
	}))
	t.Cleanup(ts.Close)

	ctx := pluginTestContext(t, ts.URL)
	result, err := getPlugin(ctx, GetPluginParams{PluginID: "some-plugin"})

	require.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "parse response")
}

func TestGetPlugin_NoURLConfigured(t *testing.T) {
	ctx := mcpgrafana.WithGrafanaConfig(context.Background(), mcpgrafana.GrafanaConfig{})
	result, err := getPlugin(ctx, GetPluginParams{PluginID: "some-plugin"})

	require.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "grafana URL is not configured")
}

func TestGetPlugin_EmptyPluginID(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("unexpected request with empty plugin ID")
	}))
	t.Cleanup(ts.Close)

	ctx := pluginTestContext(t, ts.URL)
	result, err := getPlugin(ctx, GetPluginParams{PluginID: "   "})

	require.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "plugin ID is required")
}

func TestGetPlugin_TrimsWhitespaceFromPluginID(t *testing.T) {
	var capturedPath string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedPath = r.URL.Path
		payload := pluginSettingsResponse{ID: "my-plugin", Name: "My Plugin", Type: "datasource", Enabled: true}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(payload)
	}))
	t.Cleanup(ts.Close)

	ctx := pluginTestContext(t, ts.URL)
	result, err := getPlugin(ctx, GetPluginParams{PluginID: "  my-plugin  "})

	require.NoError(t, err)
	assert.Equal(t, "/api/plugins/my-plugin/settings", capturedPath)
	assert.True(t, result.Installed)
	assert.Equal(t, "my-plugin", result.PluginID)
}

func TestGetPlugin_EscapesPluginIDPathSegment(t *testing.T) {
	var capturedEscapedPath string
	var capturedRawQuery string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedEscapedPath = r.URL.EscapedPath()
		capturedRawQuery = r.URL.RawQuery
		payload := pluginSettingsResponse{ID: "test?x=1/../../admin", Name: "Test Plugin", Type: "panel", Enabled: true}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(payload)
	}))
	t.Cleanup(ts.Close)

	ctx := pluginTestContext(t, ts.URL)
	result, err := getPlugin(ctx, GetPluginParams{PluginID: "test?x=1/../../admin"})

	require.NoError(t, err)
	assert.Equal(t, "/api/plugins/test%3Fx=1%2F..%2F..%2Fadmin/settings", capturedEscapedPath)
	assert.Empty(t, capturedRawQuery)
	assert.True(t, result.Installed)
}

func TestGetPlugin_SendsAPIKeyHeader(t *testing.T) {
	var capturedAuth string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedAuth = r.Header.Get("Authorization")
		payload := pluginSettingsResponse{ID: "some-plugin", Name: "Some Plugin", Type: "panel", Enabled: true}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(payload)
	}))
	t.Cleanup(ts.Close)

	cfg := mcpgrafana.GrafanaConfig{URL: ts.URL, APIKey: "glsa_test_token"}
	ctx := mcpgrafana.WithGrafanaConfig(context.Background(), cfg)
	_, err := getPlugin(ctx, GetPluginParams{PluginID: "some-plugin"})

	require.NoError(t, err)
	assert.Equal(t, "Bearer glsa_test_token", capturedAuth)
}

func TestGetPlugin_DisabledPlugin(t *testing.T) {
	payload := pluginSettingsResponse{
		ID:      "grafana-clock-panel",
		Name:    "Clock",
		Type:    "panel",
		Enabled: false,
	}
	payload.Info.Version = "2.1.3"

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(payload)
	}))
	t.Cleanup(ts.Close)

	ctx := pluginTestContext(t, ts.URL)
	result, err := getPlugin(ctx, GetPluginParams{PluginID: "grafana-clock-panel"})

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.True(t, result.Installed)
	require.NotNil(t, result.Enabled)
	assert.False(t, *result.Enabled)
	assert.Equal(t, "2.1.3", result.Version)
}

func TestGetPlugin_AppType(t *testing.T) {
	payload := pluginSettingsResponse{
		ID:      "grafana-oncall-app",
		Name:    "Grafana OnCall",
		Type:    "app",
		Enabled: true,
	}
	payload.Info.Version = "1.9.0"

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(payload)
	}))
	t.Cleanup(ts.Close)

	ctx := pluginTestContext(t, ts.URL)
	result, err := getPlugin(ctx, GetPluginParams{PluginID: "grafana-oncall-app"})

	require.NoError(t, err)
	assert.True(t, result.Installed)
	assert.Equal(t, "app", result.Type)
	assert.Equal(t, "grafana-oncall-app", result.PluginID)
}

func TestSearchPlugins_PrioritizesGrafanaOwned(t *testing.T) {
	catalogTestServer(t, []catalogPlugin{
		{Slug: "azure-monitor-app", Name: "Azure Cloud Native Monitoring", SignatureType: "commercial", OrgSlug: "azure", OrgName: "Azure", TypeCode: "app", Status: "active", Popularity: 0.9},
		{Slug: "grafana-azure-monitor-datasource", Name: "Azure Monitor", SignatureType: "grafana", OrgSlug: "grafana", OrgName: "Grafana Labs", TypeCode: "datasource", Status: "active", Popularity: 0.5},
		{Slug: "community-azure-thing", Name: "Azure Community", SignatureType: "community", OrgSlug: "somecorp", OrgName: "Some Corp", TypeCode: "datasource", Status: "active", Popularity: 0.8},
	})

	result, err := searchPlugins(context.Background(), SearchPluginsParams{Query: "azure"})

	require.NoError(t, err)
	require.Len(t, result.Results, 3)
	assert.Equal(t, "grafana-azure-monitor-datasource", result.Results[0].PluginID)
	assert.Equal(t, "grafana", result.Results[0].SignatureType)
	assert.Equal(t, "azure-monitor-app", result.Results[1].PluginID)
	assert.Equal(t, "commercial", result.Results[1].SignatureType)
	assert.Equal(t, "community-azure-thing", result.Results[2].PluginID)
	assert.Equal(t, "community", result.Results[2].SignatureType)
}

func TestSearchPlugins_SortsByPopularityWithinTier(t *testing.T) {
	catalogTestServer(t, []catalogPlugin{
		{Slug: "grafana-loki-1", Name: "Loki A", SignatureType: "grafana", OrgSlug: "grafana", Status: "active", Popularity: 0.3},
		{Slug: "grafana-loki-2", Name: "Loki B", SignatureType: "grafana", OrgSlug: "grafana", Status: "active", Popularity: 0.9},
		{Slug: "grafana-loki-3", Name: "Loki C", SignatureType: "grafana", OrgSlug: "grafana", Status: "active", Popularity: 0.6},
	})

	result, err := searchPlugins(context.Background(), SearchPluginsParams{Query: "loki"})

	require.NoError(t, err)
	require.Len(t, result.Results, 3)
	assert.Equal(t, "grafana-loki-2", result.Results[0].PluginID)
	assert.Equal(t, "grafana-loki-3", result.Results[1].PluginID)
	assert.Equal(t, "grafana-loki-1", result.Results[2].PluginID)
}

func TestSearchPlugins_WarnsEnterprisePlugins(t *testing.T) {
	catalogTestServer(t, []catalogPlugin{
		{Slug: "grafana-enterprise-traces", Name: "Grafana Enterprise Traces", SignatureType: "grafana", OrgSlug: "grafana", Status: "enterprise", Popularity: 0.5},
	})

	result, err := searchPlugins(context.Background(), SearchPluginsParams{Query: "enterprise traces"})

	require.NoError(t, err)
	require.Len(t, result.Results, 1)
	assert.Contains(t, result.Results[0].Warnings, "Requires a Grafana Enterprise license")
}

func TestSearchPlugins_WarnsAngularPlugins(t *testing.T) {
	catalogTestServer(t, []catalogPlugin{
		{Slug: "old-panel", Name: "Old Angular Panel", SignatureType: "community", OrgSlug: "someone", Status: "active", Popularity: 0.1, AngularDetected: true},
	})

	result, err := searchPlugins(context.Background(), SearchPluginsParams{Query: "angular"})

	require.NoError(t, err)
	require.Len(t, result.Results, 1)
	assert.Contains(t, result.Results[0].Warnings, "Uses Angular (being phased out in Grafana; prefer alternatives)")
}

func TestSearchPlugins_WarnsPrivatePlugins(t *testing.T) {
	catalogTestServer(t, []catalogPlugin{
		{Slug: "private-plugin", Name: "Private Datasource", SignatureType: "private", OrgSlug: "internalco", Status: "active", Popularity: 0.05},
	})

	result, err := searchPlugins(context.Background(), SearchPluginsParams{Query: "private datasource"})

	require.NoError(t, err)
	require.Len(t, result.Results, 1)
	assert.Equal(t, "private", result.Results[0].SignatureType)
	assert.Contains(t, result.Results[0].Warnings, "Private plugin: may not be publicly available for installation")
}

func TestSearchPlugins_TruncatesToTenAndReportsTotal(t *testing.T) {
	items := make([]catalogPlugin, 12)
	for i := range items {
		items[i] = catalogPlugin{
			Slug:          fmt.Sprintf("db-plugin-%d", i),
			Name:          fmt.Sprintf("DB Plugin %d", i),
			SignatureType: "community",
			Status:        "active",
		}
	}
	catalogTestServer(t, items)

	result, err := searchPlugins(context.Background(), SearchPluginsParams{Query: "db plugin"})

	require.NoError(t, err)
	assert.Len(t, result.Results, 10)
	assert.Equal(t, 12, result.Total)
	assert.NotEmpty(t, result.Note)
}

func TestSearchPlugins_MatchesSlug(t *testing.T) {
	catalogTestServer(t, []catalogPlugin{
		{Slug: "grafana-piechart-panel", Name: "Some Panel", SignatureType: "grafana", Status: "active"},
	})

	result, err := searchPlugins(context.Background(), SearchPluginsParams{Query: "piechart"})

	require.NoError(t, err)
	require.Len(t, result.Results, 1)
	assert.Equal(t, "grafana-piechart-panel", result.Results[0].PluginID)
}

func TestSearchPlugins_MatchesDescription(t *testing.T) {
	catalogTestServer(t, []catalogPlugin{
		{Slug: "some-datasource", Name: "Some Datasource", Description: "connects to InfluxDB time series", SignatureType: "community", Status: "active"},
	})

	result, err := searchPlugins(context.Background(), SearchPluginsParams{Query: "influxdb"})

	require.NoError(t, err)
	require.Len(t, result.Results, 1)
	assert.Equal(t, "some-datasource", result.Results[0].PluginID)
}

func TestSearchPlugins_MatchesKeywords(t *testing.T) {
	catalogTestServer(t, []catalogPlugin{
		{Slug: "grafana-prometheus", Name: "Prometheus", Keywords: []string{"metrics", "tsdb"}, SignatureType: "grafana", Status: "active"},
	})

	result, err := searchPlugins(context.Background(), SearchPluginsParams{Query: "tsdb"})

	require.NoError(t, err)
	require.Len(t, result.Results, 1)
	assert.Equal(t, "grafana-prometheus", result.Results[0].PluginID)
}

func TestSearchPlugins_EmptyResults(t *testing.T) {
	catalogTestServer(t, []catalogPlugin{
		{Slug: "grafana-loki", Name: "Loki", SignatureType: "grafana", Status: "active"},
	})

	result, err := searchPlugins(context.Background(), SearchPluginsParams{Query: "nonexistentxyz"})

	require.NoError(t, err)
	assert.Empty(t, result.Results)
	assert.Equal(t, 0, result.Total)
}

func TestSearchPlugins_EmptyQuery(t *testing.T) {
	result, err := searchPlugins(context.Background(), SearchPluginsParams{Query: "   "})

	require.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "query is required")
}

func TestSearchPlugins_CatalogError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal error", http.StatusInternalServerError)
	}))
	t.Cleanup(func() {
		grafanaComCatalogURL = "https://grafana.com/api/plugins"
		ts.Close()
	})
	grafanaComCatalogURL = ts.URL

	_, err := searchPlugins(context.Background(), SearchPluginsParams{Query: "azure"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "fetch plugin catalog")
}

func TestSearchPlugins_MalformedCatalogJSON(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{not valid json`))
	}))
	t.Cleanup(func() {
		grafanaComCatalogURL = "https://grafana.com/api/plugins"
		ts.Close()
	})
	grafanaComCatalogURL = ts.URL

	_, err := searchPlugins(context.Background(), SearchPluginsParams{Query: "azure"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "decode catalog")
}

func TestSearchPlugins_NoteEmptyWhenResultsNotTruncated(t *testing.T) {
	catalogTestServer(t, []catalogPlugin{
		{Slug: "grafana-loki", Name: "Loki", SignatureType: "grafana", Status: "active"},
		{Slug: "grafana-loki-2", Name: "Loki 2", SignatureType: "grafana", Status: "active"},
	})

	result, err := searchPlugins(context.Background(), SearchPluginsParams{Query: "loki"})

	require.NoError(t, err)
	assert.Len(t, result.Results, 2)
	assert.Equal(t, 2, result.Total)
	assert.Empty(t, result.Note)
}

// installPlugin unit tests

func versionCatalogTestServer(t *testing.T, pluginID, version string, statusCode int) {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if statusCode != http.StatusOK {
			http.Error(w, "error", statusCode)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(grafanaComPluginResponse{Version: version})
	}))
	t.Cleanup(func() {
		grafanaComCatalogURL = "https://grafana.com/api/plugins"
		ts.Close()
	})
	grafanaComCatalogURL = ts.URL
}

func TestInstallPlugin_NoURLConfigured(t *testing.T) {
	ctx := mcpgrafana.WithGrafanaConfig(context.Background(), mcpgrafana.GrafanaConfig{})
	result, err := installPlugin(ctx, InstallPluginParams{PluginID: "some-plugin", Version: "1.0.0"})

	require.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "grafana URL is not configured")
}

func TestInstallPlugin_NoVersion_ReturnsLatestVersionForConfirmation(t *testing.T) {
	versionCatalogTestServer(t, "grafana-test-plugin", "3.1.4", http.StatusOK)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// install should not be called in this path
		t.Error("unexpected call to Grafana instance")
	}))
	t.Cleanup(ts.Close)

	ctx := pluginTestContext(t, ts.URL)
	result, err := installPlugin(ctx, InstallPluginParams{PluginID: "grafana-test-plugin"})

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.True(t, result.ConfirmationRequired)
	assert.Equal(t, "3.1.4", result.LatestVersion)
	assert.Contains(t, result.Message, "3.1.4")
	assert.Contains(t, result.Message, "grafana-test-plugin")
}

func TestInstallPlugin_WhitespaceVersion_ReturnsLatestVersionForConfirmation(t *testing.T) {
	versionCatalogTestServer(t, "grafana-test-plugin", "3.1.4", http.StatusOK)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("unexpected call to Grafana instance")
	}))
	t.Cleanup(ts.Close)

	ctx := pluginTestContext(t, ts.URL)
	result, err := installPlugin(ctx, InstallPluginParams{PluginID: "grafana-test-plugin", Version: "   "})

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.True(t, result.ConfirmationRequired)
	assert.Equal(t, "3.1.4", result.LatestVersion)
}

func TestInstallPlugin_NoVersion_CatalogLookupFails_StillPromptsForVersion(t *testing.T) {
	versionCatalogTestServer(t, "grafana-test-plugin", "", http.StatusInternalServerError)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("unexpected call to Grafana instance")
	}))
	t.Cleanup(ts.Close)

	ctx := pluginTestContext(t, ts.URL)
	result, err := installPlugin(ctx, InstallPluginParams{PluginID: "grafana-test-plugin"})

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.True(t, result.ConfirmationRequired)
	assert.Empty(t, result.LatestVersion)
	assert.Contains(t, result.Message, "grafana-test-plugin")
	assert.Contains(t, result.Message, "Could not fetch")
}

func TestInstallPlugin_NoVersion_PluginNotInCatalog_PromptsToVerifyID(t *testing.T) {
	versionCatalogTestServer(t, "grafana-test-plugin", "", http.StatusNotFound)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("unexpected call to Grafana instance")
	}))
	t.Cleanup(ts.Close)

	ctx := pluginTestContext(t, ts.URL)
	result, err := installPlugin(ctx, InstallPluginParams{PluginID: "grafana-test-plugin"})

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.True(t, result.ConfirmationRequired)
	assert.Empty(t, result.LatestVersion)
	assert.Contains(t, result.Message, "not found in the Grafana plugin catalog")
	assert.Contains(t, result.Message, "Verify the plugin ID")
}

func TestInstallPlugin_WithVersion_Success(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/api/plugins/grafana-test-plugin/install", r.URL.Path)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(ts.Close)

	ctx := pluginTestContext(t, ts.URL)
	result, err := installPlugin(ctx, InstallPluginParams{PluginID: "grafana-test-plugin", Version: "2.0.0"})

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, "grafana-test-plugin", result.PluginID)
	assert.False(t, result.ConfirmationRequired)
	assert.Contains(t, result.Message, "installed successfully")
}

func TestInstallPlugin_WithVersion_UnexpectedStatus(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal error", http.StatusInternalServerError)
	}))
	t.Cleanup(ts.Close)

	ctx := pluginTestContext(t, ts.URL)
	result, err := installPlugin(ctx, InstallPluginParams{PluginID: "grafana-test-plugin", Version: "2.0.0"})

	require.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "install plugin")
	assert.Contains(t, err.Error(), "500")
	assert.Contains(t, err.Error(), "internal error")
}

func TestInstallPlugin_WithVersion_TrimsWhitespaceFromPluginID(t *testing.T) {
	var capturedPath string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(ts.Close)

	ctx := pluginTestContext(t, ts.URL)
	_, err := installPlugin(ctx, InstallPluginParams{PluginID: "  grafana-test-plugin  ", Version: "1.0.0"})

	require.NoError(t, err)
	assert.Equal(t, "/api/plugins/grafana-test-plugin/install", capturedPath)
}
