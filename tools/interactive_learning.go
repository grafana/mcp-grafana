package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"

	mcpgrafana "github.com/grafana/mcp-grafana/v2"
)

const (
	// interactiveLearningPluginID is the plugin that serves Grafana Interactive
	// Learning, including the "My Learning" page.
	interactiveLearningPluginID = "grafana-pathfinder-app"
	interactiveLearningPath     = "/a/" + interactiveLearningPluginID

	// interactiveLearningCacheTTL bounds how long a plugin availability answer
	// is reused. Hints only appear on rare empty or dead-end results, so a
	// short TTL is enough to avoid a settings call on every such result.
	interactiveLearningCacheTTL = 5 * time.Minute
)

type interactiveLearningEntry struct {
	available bool
	expires   time.Time
}

// interactiveLearningCache maps fallbackProxyIDKey results to whether the
// Interactive Learning plugin is installed and enabled for that URL, org and
// credential.
var interactiveLearningCache sync.Map

// interactiveLearningAvailable reports whether the Interactive Learning plugin
// is installed and enabled. Any failure counts as unavailable: a hint is an
// extra, so it must never cost the caller an error or point at a missing page.
func interactiveLearningAvailable(ctx context.Context, cfg mcpgrafana.GrafanaConfig) bool {
	key := fallbackProxyIDKey(ctx, "plugin", interactiveLearningPluginID)
	if v, ok := interactiveLearningCache.Load(key); ok {
		if e := v.(interactiveLearningEntry); time.Now().Before(e.expires) {
			return e.available
		}
	}

	available := false
	body, status, err := grafanaPluginRequest(ctx, cfg, http.MethodGet, "/api/plugins/"+interactiveLearningPluginID+"/settings", nil)
	if err == nil && status == http.StatusOK {
		var settings pluginSettingsResponse
		if json.Unmarshal(body, &settings) == nil {
			available = settings.Enabled
		}
	}

	interactiveLearningCache.Store(key, interactiveLearningEntry{
		available: available,
		expires:   time.Now().Add(interactiveLearningCacheTTL),
	})
	return available
}

// interactiveLearningHint returns a pointer to Grafana's My Learning page, or
// "" when hints are disabled, no Grafana URL is configured, or Interactive
// Learning is not available on the instance. problem optionally leads the hint
// with what is missing, for example "No tempo datasource is configured", for
// callers whose result does not already say so.
func interactiveLearningHint(ctx context.Context, problem string) string {
	cfg := mcpgrafana.GrafanaConfigFromContext(ctx)
	if cfg.DisableInteractiveLearningHints || cfg.URL == "" {
		return ""
	}
	if !interactiveLearningAvailable(ctx, cfg) {
		return ""
	}
	link := strings.TrimRight(cfg.URL, "/") + interactiveLearningPath
	hint := "Grafana's My Learning page can help you to set up your instance or add new connections: " + link
	if problem != "" {
		hint = problem + ". " + hint
	}
	return hint
}
