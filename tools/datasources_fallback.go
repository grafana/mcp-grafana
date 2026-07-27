package tools

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/grafana/grafana-openapi-client-go/models"
	mcpgrafana "github.com/grafana/mcp-grafana"
)

// Some Grafana deployments cannot serve the datasource metadata API
// (/api/datasources*) to the token in use:
//
//   - Before Grafana 9.0 (i.e. 7.x/8.x) those endpoints require the Org Admin
//     role, so viewer/editor tokens receive a 403 — and the uid-based
//     datasource proxy routes do not exist there either (added in 9.0); only
//     /api/datasources/proxy/{id}.
//   - On newer Grafana, RBAC-restricted service accounts may lack
//     datasources:read while still being permitted to query data.
//
// GET /api/frontend/settings is served to any authenticated role and includes
// every datasource's numeric id, uid, name, type and (non-secret) jsonData.
// The helpers in this file resolve datasources from it when the metadata API
// fails, and record the numeric id so datasourceProxyPaths can route the
// datasource proxy through the numeric-id path, which exists on all Grafana
// versions.

// fallbackProxyIDs maps fallbackProxyIDKey results to the numeric datasource
// id resolved via /api/frontend/settings. Entries are only added when the
// datasource metadata API was inaccessible, which is also the signal that the
// uid-based proxy routes are likely unavailable.
var fallbackProxyIDs sync.Map

// fallbackProxyIDKey scopes cached ids to the Grafana URL, org, credential
// material, and identifier kind ("uid" or "name"):
//
//   - In multi-tenant HTTP mode different requests can target different orgs
//     on the same URL — Grafana tokens are themselves org-scoped — and must
//     not observe each other's resolved ids. The credential hash covers every
//     auth mechanism (API key, on-behalf-of tokens, basic auth, and
//     ExtraHeaders, which may carry per-tenant auth).
//   - Kind-prefixing keeps uid and name entries in separate key spaces, so a
//     datasource whose name equals another datasource's uid cannot shadow it.
func fallbackProxyIDKey(ctx context.Context, kind, id string) string {
	cfg := mcpgrafana.GrafanaConfigFromContext(ctx)

	h := sha256.New()
	for _, part := range []string{cfg.APIKey, cfg.AccessToken, cfg.IDToken} {
		h.Write([]byte(part))
		h.Write([]byte{0})
	}
	if cfg.BasicAuth != nil {
		if password, ok := cfg.BasicAuth.Password(); ok {
			h.Write([]byte(cfg.BasicAuth.Username() + ":" + password))
		} else {
			h.Write([]byte(cfg.BasicAuth.Username()))
		}
	}
	h.Write([]byte{0})
	headers := make([]string, 0, len(cfg.ExtraHeaders))
	for k := range cfg.ExtraHeaders {
		headers = append(headers, k)
	}
	sort.Strings(headers)
	for _, k := range headers {
		h.Write([]byte(k))
		h.Write([]byte{0})
		h.Write([]byte(cfg.ExtraHeaders[k]))
		h.Write([]byte{0})
	}
	cred := h.Sum(nil)

	return strings.TrimRight(cfg.URL, "/") + "\x00" + strconv.FormatInt(cfg.OrgID, 10) + "\x00" + hex.EncodeToString(cred[:8]) + "\x00" + kind + "\x00" + id
}

// fallbackProxyBase returns the numeric-id datasource proxy base path for an
// identifier previously resolved through the frontend-settings fallback. The
// uid entry is consulted before the name entry, mirroring the resolution
// precedence of fallbackDatasourceByUID.
func fallbackProxyBase(ctx context.Context, uid string) (string, bool) {
	for _, kind := range []string{"uid", "name"} {
		if id, ok := fallbackProxyIDs.Load(fallbackProxyIDKey(ctx, kind, uid)); ok {
			return fmt.Sprintf("/api/datasources/proxy/%d", id), true
		}
	}
	return "", false
}

// frontendSettingsDatasource mirrors the datasource entries of
// GET /api/frontend/settings.
type frontendSettingsDatasource struct {
	ID        int64          `json:"id"`
	UID       string         `json:"uid"`
	Name      string         `json:"name"`
	Type      string         `json:"type"`
	URL       string         `json:"url"`
	IsDefault bool           `json:"isDefault"`
	JSONData  map[string]any `json:"jsonData"`
}

func fetchFrontendSettingsDatasources(ctx context.Context) (map[string]frontendSettingsDatasource, string, error) {
	cfg := mcpgrafana.GrafanaConfigFromContext(ctx)
	settingsURL := strings.TrimRight(cfg.URL, "/") + "/api/frontend/settings"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, settingsURL, nil)
	if err != nil {
		return nil, "", fmt.Errorf("creating frontend settings request: %w", err)
	}

	transport, err := mcpgrafana.BuildTransport(&cfg, nil)
	if err != nil {
		return nil, "", fmt.Errorf("failed to create transport: %w", err)
	}
	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = mcpgrafana.DefaultGrafanaClientTimeout
	}
	httpClient := &http.Client{Transport: transport, Timeout: timeout}

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("fetching frontend settings: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, "", fmt.Errorf("frontend settings returned status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var settings struct {
		Datasources       map[string]frontendSettingsDatasource `json:"datasources"`
		DefaultDatasource string                                `json:"defaultDatasource"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&settings); err != nil {
		return nil, "", fmt.Errorf("decoding frontend settings: %w", err)
	}

	for name, ds := range settings.Datasources {
		if ds.Name == "" {
			ds.Name = name
			settings.Datasources[name] = ds
		}
	}
	return settings.Datasources, settings.DefaultDatasource, nil
}

// rememberFallbackDatasource records the numeric id under both the
// datasource's uid and name (in separate key spaces) so subsequent
// datasourceProxyPaths lookups hit regardless of which identifier the caller
// used.
func rememberFallbackDatasource(ctx context.Context, ds frontendSettingsDatasource) {
	if ds.ID == 0 {
		return
	}
	if ds.UID != "" {
		fallbackProxyIDs.Store(fallbackProxyIDKey(ctx, "uid", ds.UID), ds.ID)
	}
	if ds.Name != "" {
		fallbackProxyIDs.Store(fallbackProxyIDKey(ctx, "name", ds.Name), ds.ID)
	}
}

func (d frontendSettingsDatasource) toDataSource(defaultName string) *models.DataSource {
	return &models.DataSource{
		ID:        d.ID,
		UID:       d.UID,
		Name:      d.Name,
		Type:      d.Type,
		URL:       d.URL,
		IsDefault: d.IsDefault || d.Name == defaultName,
		JSONData:  models.JSON(d.JSONData),
	}
}

// fallbackDatasourceByUID resolves a datasource by uid — or by name, which
// dashboards on older Grafana commonly use — from /api/frontend/settings.
func fallbackDatasourceByUID(ctx context.Context, uid string) (*models.DataSource, error) {
	dss, defaultName, err := fetchFrontendSettingsDatasources(ctx)
	if err != nil {
		return nil, err
	}
	for _, ds := range dss {
		if ds.UID == uid {
			rememberFallbackDatasource(ctx, ds)
			return ds.toDataSource(defaultName), nil
		}
	}
	for name, ds := range dss {
		if name == uid {
			rememberFallbackDatasource(ctx, ds)
			return ds.toDataSource(defaultName), nil
		}
	}
	return nil, fmt.Errorf("datasource %q not found in frontend settings", uid)
}

// fallbackDatasourceByName resolves a datasource by name from
// /api/frontend/settings, which keys datasources by name.
func fallbackDatasourceByName(ctx context.Context, name string) (*models.DataSource, error) {
	dss, defaultName, err := fetchFrontendSettingsDatasources(ctx)
	if err != nil {
		return nil, err
	}
	if ds, ok := dss[name]; ok {
		rememberFallbackDatasource(ctx, ds)
		return ds.toDataSource(defaultName), nil
	}
	return nil, fmt.Errorf("datasource %q not found in frontend settings", name)
}

// fallbackDatasourceList lists datasources from /api/frontend/settings.
func fallbackDatasourceList(ctx context.Context) (models.DataSourceList, error) {
	dss, defaultName, err := fetchFrontendSettingsDatasources(ctx)
	if err != nil {
		return nil, err
	}
	list := make(models.DataSourceList, 0, len(dss))
	for name, ds := range dss {
		// Skip the built-in pseudo datasources ("-- Grafana --" etc.).
		if ds.ID == 0 && ds.UID == "" {
			continue
		}
		rememberFallbackDatasource(ctx, ds)
		list = append(list, &models.DataSourceListItemDTO{
			ID:        ds.ID,
			UID:       ds.UID,
			Name:      name,
			Type:      ds.Type,
			IsDefault: name == defaultName,
		})
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })
	return list, nil
}
