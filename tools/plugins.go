package tools

import (
	"bytes"
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
	PluginID string `json:"pluginId" jsonschema:"required,description=The plugin ID to check (e.g. 'grafana-image-renderer'\\, 'grafana-piechart-panel'\\, 'grafana-oncall-app')"`
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

// grafanaPluginRequest issues an authenticated HTTP request to the given Grafana
// API path and returns the raw response body and status code.
func grafanaPluginRequest(ctx context.Context, cfg mcpgrafana.GrafanaConfig, method, apiPath string, body any) ([]byte, int, error) {
	transport, err := mcpgrafana.BuildTransport(&cfg, nil)
	if err != nil {
		return nil, 0, fmt.Errorf("build transport: %w", err)
	}

	var reqBody *bytes.Buffer
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, 0, fmt.Errorf("marshal body: %w", err)
		}
		reqBody = bytes.NewBuffer(b)
	} else {
		reqBody = bytes.NewBuffer(nil)
	}

	endpoint := strings.TrimRight(cfg.URL, "/") + apiPath
	req, err := http.NewRequestWithContext(ctx, method, endpoint, reqBody)
	if err != nil {
		return nil, 0, fmt.Errorf("create request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := (&http.Client{Transport: transport}).Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("do request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("read response: %w", err)
	}
	return respBody, resp.StatusCode, nil
}

func getPlugin(ctx context.Context, args GetPluginParams) (*GetPluginResult, error) {
	cfg := mcpgrafana.GrafanaConfigFromContext(ctx)
	if cfg.URL == "" {
		return nil, fmt.Errorf("grafana URL is not configured")
	}

	pluginID := strings.TrimSpace(args.PluginID)
	body, status, err := grafanaPluginRequest(ctx, cfg, http.MethodGet, "/api/plugins/"+url.PathEscape(pluginID)+"/settings", nil)
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

type InstallPluginParams struct {
	PluginID string `json:"pluginId" jsonschema:"required,description=The plugin ID to install (e.g. 'grafana-image-renderer'\\, 'grafana-piechart-panel')"`
	Version  string `json:"version,omitempty" jsonschema:"description=The version to install. Omit to install the latest version."`
}

type InstallPluginResult struct {
	PluginID string `json:"pluginId"`
	Message  string `json:"message"`
}

func installPlugin(ctx context.Context, args InstallPluginParams) (*InstallPluginResult, error) {
	cfg := mcpgrafana.GrafanaConfigFromContext(ctx)
	if cfg.URL == "" {
		return nil, fmt.Errorf("grafana URL is not configured")
	}

	pluginID := strings.TrimSpace(args.PluginID)

	var body any
	if args.Version != "" {
		body = map[string]string{"version": args.Version}
	}

	_, status, err := grafanaPluginRequest(ctx, cfg, http.MethodPost, "/api/plugins/"+url.PathEscape(pluginID)+"/install", body)
	if err != nil {
		return nil, fmt.Errorf("install plugin: %w", err)
	}

	if status != http.StatusOK {
		return nil, fmt.Errorf("install plugin: unexpected status %d", status)
	}

	return &InstallPluginResult{
		PluginID: pluginID,
		Message:  "Plugin installed successfully. Grafana may need to be restarted for the plugin to become active.",
	}, nil
}

var InstallPlugin = mcpgrafana.MustTool(
	"install_plugin",
	"Install a Grafana plugin by its plugin ID. Optionally specify a version; omit to install the latest. Grafana may need to be restarted after installation for the plugin to become active.",
	installPlugin,
	mcp.WithTitleAnnotation("Install plugin"),
)

func AddPluginTools(s *server.MCPServer) {
	GetPlugin.Register(s)
	InstallPlugin.Register(s)
}
