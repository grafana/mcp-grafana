package tools

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
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
// fails, and record the numeric id so the datasource client constructors (see
// newPrometheusBackend) can route the datasource proxy through the numeric-id
// path directly. That route choice matters: the uid-based proxy routes only
// exist from Grafana 9.0 — requests to them fall into the numeric :id route
// and fail with a status that varies per version (verified live: 400 "id is
// invalid" on 8.5, 500 "Unable to load datasource meta data" on 7.5) — so the
// numeric path must be the primary, not a retry target.

// fallbackRoute is the cached result of a frontend-settings resolution: the
// numeric datasource id plus the datasource's real uid, so route construction
// never depends on which identifier (uid or name) the caller happened to use.
type fallbackRoute struct {
	id  int64
	uid string
}

// fallbackProxyIDs maps fallbackProxyIDKey results to the fallbackRoute
// resolved via /api/frontend/settings. Entries are only added when the
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

	// Name matching is case-insensitive (mirroring the metadata API), so
	// name-kind keys are normalized on both store and lookup. Uids stay
	// case-sensitive.
	if kind == "name" {
		id = strings.ToLower(id)
	}

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

// fallbackProxyBases returns the datasource proxy base paths for an
// identifier previously resolved through the frontend-settings fallback:
// the numeric-id route as the primary, and a uid-based proxy route as the
// transport-level fallback. The fallback is built from the datasource's
// *resolved* uid, not the caller-supplied identifier — callers may reference
// datasources by name, and /api/datasources/proxy/uid/{name} would miss.
// ok=false means the datasource was resolved normally, and callers keep the
// modern uid-based routes. The uid entry is consulted before the name entry,
// mirroring the resolution precedence of fallbackDatasourceByUID.
func fallbackProxyBases(ctx context.Context, identifier string) (primaryBase, fallbackBase string, ok bool) {
	for _, kind := range []string{"uid", "name"} {
		if v, found := fallbackProxyIDs.Load(fallbackProxyIDKey(ctx, kind, identifier)); found {
			route := v.(fallbackRoute)
			uid := route.uid
			if uid == "" {
				// Very old datasources may predate uids entirely; fall back
				// to the caller's identifier as a best effort.
				uid = identifier
			}
			return fmt.Sprintf("/api/datasources/proxy/%d", route.id),
				fmt.Sprintf("/api/datasources/proxy/uid/%s", uid), true
		}
	}
	return "", "", false
}

// frontendSettingsDatasource mirrors the datasource entries of
// GET /api/frontend/settings.
type frontendSettingsDatasource struct {
	ID        int64                           `json:"id"`
	UID       string                          `json:"uid"`
	Name      string                          `json:"name"`
	Type      string                          `json:"type"`
	URL       string                          `json:"url"`
	IsDefault bool                            `json:"isDefault"`
	JSONData  map[string]any                  `json:"jsonData"`
	Meta      *frontendSettingsDatasourceMeta `json:"meta,omitempty"`
}

// frontendSettingsDatasourceMeta mirrors the subset of a datasource's plugin
// metadata that GET /api/frontend/settings exposes. Backend is nil when the
// server's response omits it (older Grafana, or a deployment that strips
// plugin metadata), which callers must treat as "unknown" rather than false.
type frontendSettingsDatasourceMeta struct {
	Backend *bool `json:"backend,omitempty"`
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
		// Drop the built-in pseudo datasources ("-- Grafana --", "-- Mixed --",
		// ...): they carry non-positive ids (-1 etc., or none at all), are not
		// queryable through the datasource proxy, and the metadata API never
		// returns them either.
		if ds.ID <= 0 {
			delete(settings.Datasources, name)
			continue
		}
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
	if ds.ID <= 0 {
		return
	}
	route := fallbackRoute{id: ds.ID, uid: ds.UID}
	if ds.UID != "" {
		fallbackProxyIDs.Store(fallbackProxyIDKey(ctx, "uid", ds.UID), route)
	}
	if ds.Name != "" {
		fallbackProxyIDs.Store(fallbackProxyIDKey(ctx, "name", ds.Name), route)
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

// errFallbackDatasourceNotFound reports that /api/frontend/settings was read
// successfully but contains no datasource matching the identifier — i.e. the
// datasource genuinely does not exist, as opposed to the settings themselves
// being unavailable. Callers use this to surface a not-found message instead
// of the original (permission) error, so agents can tell a typo from a
// credentials problem.
var errFallbackDatasourceNotFound = errors.New("datasource not found in frontend settings")

// fallbackDatasourceByUID resolves a datasource by uid — or by name, which
// dashboards on older Grafana commonly use — from /api/frontend/settings.
// Name matching is case-insensitive, mirroring the metadata API's behavior.
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
		if strings.EqualFold(name, uid) {
			rememberFallbackDatasource(ctx, ds)
			return ds.toDataSource(defaultName), nil
		}
	}
	return nil, fmt.Errorf("%w: %q", errFallbackDatasourceNotFound, uid)
}

// fallbackDatasourceByName resolves a datasource by name from
// /api/frontend/settings, which keys datasources by name. Matching is
// case-insensitive, mirroring the metadata API's behavior.
func fallbackDatasourceByName(ctx context.Context, name string) (*models.DataSource, error) {
	dss, defaultName, err := fetchFrontendSettingsDatasources(ctx)
	if err != nil {
		return nil, err
	}
	if ds, ok := dss[name]; ok {
		rememberFallbackDatasource(ctx, ds)
		return ds.toDataSource(defaultName), nil
	}
	for n, ds := range dss {
		if strings.EqualFold(n, name) {
			rememberFallbackDatasource(ctx, ds)
			return ds.toDataSource(defaultName), nil
		}
	}
	return nil, fmt.Errorf("%w: %q", errFallbackDatasourceNotFound, name)
}

// fallbackDatasourceList lists datasources from /api/frontend/settings.
func fallbackDatasourceList(ctx context.Context) (models.DataSourceList, error) {
	dss, defaultName, err := fetchFrontendSettingsDatasources(ctx)
	if err != nil {
		return nil, err
	}
	list := make(models.DataSourceList, 0, len(dss))
	for name, ds := range dss {
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
