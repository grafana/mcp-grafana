package datasource

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"

	"github.com/grafana/grafana-openapi-client-go/models"
	mcpgrafana "github.com/grafana/mcp-grafana/v2"
)

// frontendSettingsDatasource mirrors the datasource fields exposed to callers
// with query permission by GET /api/frontend/settings.
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
	resp, err := (&http.Client{Transport: transport, Timeout: timeout}).Do(req)
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
		// Built-in pseudo datasources have no positive database ID and are not
		// returned by the datasource metadata API.
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

var errFrontendSettingsDatasourceNotFound = errors.New("datasource not found in frontend settings")

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

func datasourceByUIDFromFrontendSettings(ctx context.Context, uid string) (*models.DataSource, error) {
	settings, defaultName, err := fetchFrontendSettingsDatasources(ctx)
	if err != nil {
		return nil, err
	}
	for _, ds := range settings {
		if ds.UID == uid {
			return ds.toDataSource(defaultName), nil
		}
	}
	return nil, fmt.Errorf("%w: %q", errFrontendSettingsDatasourceNotFound, uid)
}

func datasourceByNameFromFrontendSettings(ctx context.Context, name string) (*models.DataSource, error) {
	settings, defaultName, err := fetchFrontendSettingsDatasources(ctx)
	if err != nil {
		return nil, err
	}
	for settingName, ds := range settings {
		if strings.EqualFold(settingName, name) || strings.EqualFold(ds.Name, name) {
			return ds.toDataSource(defaultName), nil
		}
	}
	return nil, fmt.Errorf("%w: %q", errFrontendSettingsDatasourceNotFound, name)
}

func datasourceListFromFrontendSettings(ctx context.Context) (models.DataSourceList, error) {
	settings, defaultName, err := fetchFrontendSettingsDatasources(ctx)
	if err != nil {
		return nil, err
	}
	list := make(models.DataSourceList, 0, len(settings))
	for _, ds := range settings {
		list = append(list, &models.DataSourceListItemDTO{
			ID:        ds.ID,
			UID:       ds.UID,
			Name:      ds.Name,
			Type:      ds.Type,
			IsDefault: ds.IsDefault || ds.Name == defaultName,
		})
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })
	return list, nil
}

// WithoutBackend returns the set of UIDs whose plugin declares
// meta.backend: false in GET /api/frontend/settings.
func WithoutBackend(ctx context.Context) map[string]bool {
	settings, _, err := fetchFrontendSettingsDatasources(ctx)
	if err != nil {
		return nil
	}
	without := make(map[string]bool)
	for _, ds := range settings {
		if ds.Meta != nil && ds.Meta.Backend != nil && !*ds.Meta.Backend {
			without[ds.UID] = true
		}
	}
	return without
}
