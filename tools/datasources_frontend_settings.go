package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	mcpgrafana "github.com/grafana/mcp-grafana/v2"
)

// frontendSettingsDatasource mirrors the fields used to identify datasource
// plugins that do not have a backend component.
type frontendSettingsDatasource struct {
	UID  string                          `json:"uid"`
	Meta *frontendSettingsDatasourceMeta `json:"meta,omitempty"`
}

type frontendSettingsDatasourceMeta struct {
	Backend *bool `json:"backend,omitempty"`
}

func fetchFrontendSettingsDatasources(ctx context.Context) (map[string]frontendSettingsDatasource, error) {
	cfg := mcpgrafana.GrafanaConfigFromContext(ctx)
	settingsURL := strings.TrimRight(cfg.URL, "/") + "/api/frontend/settings"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, settingsURL, nil)
	if err != nil {
		return nil, fmt.Errorf("creating frontend settings request: %w", err)
	}
	transport, err := mcpgrafana.BuildTransport(&cfg, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create transport: %w", err)
	}
	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = mcpgrafana.DefaultGrafanaClientTimeout
	}
	resp, err := (&http.Client{Transport: transport, Timeout: timeout}).Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching frontend settings: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("frontend settings returned status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var settings struct {
		Datasources map[string]frontendSettingsDatasource `json:"datasources"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&settings); err != nil {
		return nil, fmt.Errorf("decoding frontend settings: %w", err)
	}
	return settings.Datasources, nil
}
