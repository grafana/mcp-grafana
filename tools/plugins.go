package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	mcpgrafana "github.com/grafana/mcp-grafana"
)

type GetPluginParams struct {
	PluginID string `json:"pluginId" jsonschema:"required,description=The plugin ID to check (e.g. 'prometheus'\\, 'grafana-piechart-panel'\\, 'grafana-oncall-app')"`
}

type GetPluginResult struct {
	Installed  bool   `json:"installed"`
	PluginID   string `json:"pluginId"`
	Name       string `json:"name,omitempty"`
	Version    string `json:"version,omitempty"`
	Type       string `json:"type,omitempty"`
	Enabled    *bool  `json:"enabled,omitempty"`
	Suggestion string `json:"suggestion,omitempty"` // Optional suggestion for next steps, e.g. installing the plugin if not found
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

	respBody, err := readResponseBody(resp.Body, defaultResponseLimitBytes)
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
	if pluginID == "" {
		return nil, fmt.Errorf("plugin ID is required")
	}

	body, status, err := grafanaPluginRequest(ctx, cfg, http.MethodGet, "/api/plugins/"+url.PathEscape(pluginID)+"/settings", nil)
	if err != nil {
		return nil, fmt.Errorf("get plugin settings: %w", err)
	}

	if status == http.StatusNotFound {
		return &GetPluginResult{Installed: false, PluginID: pluginID, Suggestion: "Install the plugin using install_plugin."}, nil
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
	"Check whether a Grafana plugin is installed and retrieve its details (name, version, type, enabled status). Returns installed=false when the plugin is not found. Use install_plugin when a plugin is not installed to install plugin after confirming this action with the user.",
	getPlugin,
	mcp.WithTitleAnnotation("Get plugin"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
)

type InstallPluginParams struct {
	PluginID string `json:"pluginId" jsonschema:"required,description=The plugin ID to install (e.g. 'grafana-image-renderer'\\, 'grafana-piechart-panel')"`
	Version  string `json:"version,omitempty" jsonschema:"description=The exact version to install. Must be confirmed with the user before calling — if unknown\\, omit this field to look up the latest version first."`
}

type InstallPluginResult struct {
	PluginID             string `json:"pluginId"`
	Message              string `json:"message"`
	ConfirmationRequired bool   `json:"confirmationRequired,omitempty"`
	LatestVersion        string `json:"latestVersion,omitempty"`
	Suggestion           string `json:"suggestion,omitempty"`
}

// grafanaComCatalogURL is the base URL for the Grafana plugin catalog API.
// It is a variable to allow overriding in tests.
var grafanaComCatalogURL = "https://grafana.com/api/plugins"

// errPluginNotInCatalog is returned by fetchLatestPluginVersion when the plugin
// does not exist in the public Grafana catalog (HTTP 404).
var errPluginNotInCatalog = errors.New("plugin not found in catalog")

// grafanaComPluginResponse mirrors the relevant fields from the Grafana plugin catalog API.
type grafanaComPluginResponse struct {
	Version string `json:"version"`
}

// fetchLatestPluginVersion queries the Grafana plugin catalog for the latest published version of a plugin.
func fetchLatestPluginVersion(ctx context.Context, pluginID string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, grafanaComCatalogURL+"/"+url.PathEscape(pluginID), nil)
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("do request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusNotFound {
		return "", errPluginNotInCatalog
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("unexpected status %d", resp.StatusCode)
	}
	var result grafanaComPluginResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode response: %w", err)
	}
	return result.Version, nil
}

func installPlugin(ctx context.Context, args InstallPluginParams) (*InstallPluginResult, error) {
	cfg := mcpgrafana.GrafanaConfigFromContext(ctx)
	if cfg.URL == "" {
		return nil, fmt.Errorf("grafana URL is not configured")
	}

	pluginID := strings.TrimSpace(args.PluginID)
	if pluginID == "" {
		return nil, fmt.Errorf("plugin ID is required")
	}

	version := strings.TrimSpace(args.Version)
	if version == "" {
		latestVersion, err := fetchLatestPluginVersion(ctx, pluginID)
		result := &InstallPluginResult{
			PluginID:             pluginID,
			ConfirmationRequired: true,
		}
		if err == nil && latestVersion != "" {
			result.LatestVersion = latestVersion
			result.Message = fmt.Sprintf("The latest available version of %s is %s. Ask the user whether to install this version or a specific one, then call install_plugin again with the chosen version.", pluginID, latestVersion)
		} else if errors.Is(err, errPluginNotInCatalog) {
			result.Message = fmt.Sprintf("%s was not found in the Grafana plugin catalog. Verify the plugin ID is correct with the user. If this is a private or custom plugin not listed in the catalog, ask the user to provide a specific version, then call install_plugin again with the chosen version.", pluginID)
		} else {
			result.Message = fmt.Sprintf("Could not fetch the latest version for %s from the Grafana plugin catalog. Ask the user to provide a specific version, then call install_plugin again with the chosen version.", pluginID)
		}
		return result, nil
	}

	body, status, err := grafanaPluginRequest(ctx, cfg, http.MethodPost, "/api/plugins/"+url.PathEscape(pluginID)+"/install", map[string]string{"version": version})
	if err != nil {
		return nil, fmt.Errorf("install plugin: %w", err)
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("install plugin: unexpected status %d: %s", status, body)
	}

	return &InstallPluginResult{
		PluginID:   pluginID,
		Message:    "Plugin installed successfully. Grafana may need to be restarted for the plugin to become active.",
		Suggestion: "Configure a new data source for the plugin.", // For now keeping this static to a single suggestion, down the line we may end up with a list
	}, nil
}

var InstallPlugin = mcpgrafana.MustTool(
	"install_plugin",
	"Install a Grafana plugin by its plugin ID. If the version is not already confirmed with the user, omit it — the tool will look up the latest version and return it for confirmation before installing.",
	installPlugin,
	mcp.WithTitleAnnotation("Install plugin"),
	mcp.WithReadOnlyHintAnnotation(false),
	mcp.WithDestructiveHintAnnotation(true),
	mcp.WithIdempotentHintAnnotation(false),
)

// catalogPlugin mirrors the relevant fields from the Grafana plugin catalog list API.
type catalogPlugin struct {
	Slug            string   `json:"slug"`
	Name            string   `json:"name"`
	Description     string   `json:"description"`
	Keywords        []string `json:"keywords"`
	TypeCode        string   `json:"typeCode"`
	Version         string   `json:"version"`
	OrgName         string   `json:"orgName"`
	OrgSlug         string   `json:"orgSlug"`
	SignatureType   string   `json:"signatureType"`
	Status          string   `json:"status"`
	Popularity      float64  `json:"popularity"`
	AngularDetected bool     `json:"angularDetected"`
}

type catalogListResponse struct {
	Items []catalogPlugin `json:"items"`
}

type SearchPluginsParams struct {
	Query string `json:"query" jsonschema:"required,description=Keyword to search for plugins (e.g. 'azure'\\, 'prometheus'\\, 'loki'\\, 'database'). Matches against plugin name\\, slug\\, description\\, and keywords."`
}

type SearchPluginResult struct {
	PluginID      string   `json:"pluginId"`
	Name          string   `json:"name"`
	Description   string   `json:"description,omitempty"`
	OrgName       string   `json:"orgName"`
	Type          string   `json:"type"`
	Version       string   `json:"version"`
	SignatureType string   `json:"signatureType"`
	Warnings      []string `json:"warnings,omitempty"`
}

type SearchPluginsResult struct {
	Results []SearchPluginResult `json:"results"`
	Total   int                  `json:"total"`
	Note    string               `json:"note,omitempty"`
}

func signaturePriority(sig string) int {
	switch sig {
	case "grafana":
		return 0
	case "commercial":
		return 1
	case "community":
		return 2
	default:
		return 3
	}
}

func pluginMatchesQuery(p catalogPlugin, query string) bool {
	if strings.Contains(strings.ToLower(p.Name), query) {
		return true
	}
	if strings.Contains(strings.ToLower(p.Slug), query) {
		return true
	}
	if strings.Contains(strings.ToLower(p.Description), query) {
		return true
	}
	for _, kw := range p.Keywords {
		if strings.Contains(strings.ToLower(kw), query) {
			return true
		}
	}
	return false
}

func searchPlugins(ctx context.Context, args SearchPluginsParams) (*SearchPluginsResult, error) {
	limit := 10

	query := strings.ToLower(strings.TrimSpace(args.Query))
	if query == "" {
		return nil, fmt.Errorf("query is required")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, grafanaComCatalogURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch plugin catalog: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch plugin catalog: unexpected status %d", resp.StatusCode)
	}

	var catalog catalogListResponse
	if err := json.NewDecoder(resp.Body).Decode(&catalog); err != nil {
		return nil, fmt.Errorf("decode catalog: %w", err)
	}

	var matched []catalogPlugin
	for _, p := range catalog.Items {
		if pluginMatchesQuery(p, query) {
			matched = append(matched, p)
		}
	}

	sort.Slice(matched, func(i, j int) bool {
		// order results by signature type (1. grafana, 2. commercial, 3. community) otherwise by popularity
		pi, pj := signaturePriority(matched[i].SignatureType), signaturePriority(matched[j].SignatureType)
		if pi != pj {
			return pi < pj
		}
		return matched[i].Popularity > matched[j].Popularity
	})

	total := len(matched)
	if len(matched) > limit {
		matched = matched[:limit]
	}

	results := make([]SearchPluginResult, 0, len(matched))
	for _, p := range matched {
		var warnings []string
		if p.Status == "enterprise" {
			warnings = append(warnings, "Requires a Grafana Enterprise license")
		}
		if p.SignatureType == "private" {
			warnings = append(warnings, "Private plugin: may not be publicly available for installation")
		}
		if p.AngularDetected {
			warnings = append(warnings, "Uses Angular (being phased out in Grafana; prefer alternatives)")
		}

		results = append(results, SearchPluginResult{
			PluginID:      p.Slug,
			Name:          p.Name,
			Description:   p.Description,
			OrgName:       p.OrgName,
			Type:          p.TypeCode,
			Version:       p.Version,
			SignatureType: p.SignatureType,
			Warnings:      warnings,
		})
	}

	note := ""
	if total > limit {
		note = fmt.Sprintf("Showing top %d of %d matching plugins. Use a more specific query to narrow results.", limit, total)
	}

	return &SearchPluginsResult{Results: results, Total: total, Note: note}, nil
}

var SearchPlugins = mcpgrafana.MustTool(
	"search_plugin_information",
	"Search the Grafana plugin catalog by keyword to discover available plugins before installing or getting plugin details on a specific instance. "+
		"Returns results sorted by trust: official Grafana Labs plugins first, then commercial partner plugins, then community plugins. "+
		"Use this tool when a user describes a plugin by purpose or partial name (e.g. 'azure monitoring', 'loki', 'database') — "+
		"it returns the exact pluginId to pass to get_plugin or install_plugin. "+
		"Results include warnings for enterprise-only or Angular-based plugins.",
	searchPlugins,
	mcp.WithTitleAnnotation("Search plugins"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
)

func AddPluginTools(s *server.MCPServer, enableWrite bool) {
	SearchPlugins.Register(s)
	GetPlugin.Register(s)
	if enableWrite {
		InstallPlugin.Register(s)
	}
}
