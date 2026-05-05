package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	mcpgrafana "github.com/grafana/mcp-grafana"
)

type GetPluginParams struct {
	PluginID string `json:"pluginId" jsonschema:"required,description=The plugin ID to check (e.g. 'prometheus'\\, 'grafana-piechart-panel'\\, 'grafana-oncall-app')"`
}

type GetPluginResult struct {
	Installed bool   `json:"installed"`
	PluginID  string `json:"pluginId"`
	Name      string `json:"name,omitempty"`
	Version   string `json:"version,omitempty"`
	Type      string `json:"type,omitempty"`
	Enabled   *bool  `json:"enabled,omitempty"`
}

// pluginSettingsResponse mirrors the relevant fields from GET /api/plugins/{id}/settings.
type pluginSettingsResponse struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Type    string `json:"type"`
	Enabled bool   `json:"enabled"`
	Info    struct {
		Version string `json:"version"`
	} `json:"info"`
}

// grafanaPluginGet issues an authenticated GET request to the given Grafana API
// path (e.g. "/api/plugins/foo/settings") and returns the raw response body and
// HTTP status code. Only transport-level errors are returned as Go errors.
func grafanaPluginGet(ctx context.Context, cfg mcpgrafana.GrafanaConfig, apiPath string) ([]byte, int, error) {
	transport, err := mcpgrafana.BuildTransport(&cfg, nil)
	if err != nil {
		return nil, 0, fmt.Errorf("build transport: %w", err)
	}

	endpoint := strings.TrimRight(cfg.URL, "/") + apiPath
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, 0, fmt.Errorf("create request: %w", err)
	}

	resp, err := (&http.Client{Transport: transport}).Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("do request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("read response: %w", err)
	}
	return body, resp.StatusCode, nil
}

func getPlugin(ctx context.Context, args GetPluginParams) (*GetPluginResult, error) {
	cfg := mcpgrafana.GrafanaConfigFromContext(ctx)
	if cfg.URL == "" {
		return nil, fmt.Errorf("grafana URL is not configured")
	}

	pluginID := strings.TrimSpace(args.PluginID)
	body, status, err := grafanaPluginGet(ctx, cfg, "/api/plugins/"+url.PathEscape(pluginID)+"/settings")
	if err != nil {
		return nil, fmt.Errorf("get plugin settings: %w", err)
	}

	if status == http.StatusNotFound {
		return &GetPluginResult{Installed: false, PluginID: pluginID}, nil
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("get plugin settings: unexpected status %d", status)
	}

	var settings pluginSettingsResponse
	if err := json.Unmarshal(body, &settings); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}

	enabled := settings.Enabled
	return &GetPluginResult{
		Installed: true,
		PluginID:  settings.ID,
		Name:      settings.Name,
		Version:   settings.Info.Version,
		Type:      settings.Type,
		Enabled:   &enabled,
	}, nil
}

var GetPlugin = mcpgrafana.MustTool(
	"get_plugin",
	"Check whether a Grafana plugin is installed and retrieve its details (name, version, type, enabled status). Returns installed=false when the plugin is not found.",
	getPlugin,
	mcp.WithTitleAnnotation("Get plugin"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
)

func AddPluginTools(s *server.MCPServer) {
	GetPlugin.Register(s)
}
