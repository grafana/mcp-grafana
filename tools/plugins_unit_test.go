package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	mcpgrafana "github.com/grafana/mcp-grafana"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
