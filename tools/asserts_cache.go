package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"
)

type cacheEntry[T any] struct {
	value     T
	expiresAt time.Time
}

type ttlCache[T any] struct {
	mu      sync.RWMutex
	entries map[string]cacheEntry[T]
	ttl     time.Duration
}

func newTTLCache[T any](ttl time.Duration) *ttlCache[T] {
	return &ttlCache[T]{
		entries: make(map[string]cacheEntry[T]),
		ttl:     ttl,
	}
}

func (c *ttlCache[T]) get(key string) (T, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	entry, ok := c.entries[key]
	if !ok || time.Now().After(entry.expiresAt) {
		var zero T
		return zero, false
	}
	return entry.value, true
}

func (c *ttlCache[T]) set(key string, value T) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[key] = cacheEntry[T]{
		value:     value,
		expiresAt: time.Now().Add(c.ttl),
	}
}

var (
	entityInfoCache     = newTTLCache[*graphEntityResponse](2 * time.Minute)
	graphSchemaCache    = newTTLCache[string](15 * time.Minute)
	drilldownLogCache   = newTTLCache[[]drilldownConfigEntry](15 * time.Minute)
	drilldownTraceCache = newTTLCache[[]drilldownConfigEntry](15 * time.Minute)
)

func entityCacheKey(baseURL string, orgID int64, entityType, entityName, env, site, ns string) string {
	return fmt.Sprintf("%s|%d|%s|%s|%s|%s|%s", baseURL, orgID, entityType, entityName, env, site, ns)
}

// resolveEntityInfoCached wraps resolveEntityInfo with a short-TTL cache
// to avoid redundant /v1/entity/info calls when multiple tools operate on
// the same entity within a session.
func (c *Client) resolveEntityInfoCached(ctx context.Context, entityType, entityName, env, site, namespace string) (*graphEntityResponse, error) {
	key := entityCacheKey(c.baseURL, c.orgID, entityType, entityName, env, site, namespace)

	if cached, ok := entityInfoCache.get(key); ok {
		return cached, nil
	}

	entity, err := c.resolveEntityInfo(ctx, entityType, entityName, env, site, namespace)
	if err != nil {
		return nil, err
	}

	entityInfoCache.set(key, entity)
	return entity, nil
}

// drilldownConfigEntry is the parsed representation of a single drilldown
// config from /v1/config/log-drilldown or /v1/config/trace-drilldown.
type drilldownConfigEntry struct {
	Name          string               `json:"name"`
	Priority      int                  `json:"priority"`
	MatchRules    []drilldownMatchRule `json:"entityPropertyMatchRules"`
	PromMapping   map[string]string    `json:"entityPropertyToLabelMapping,omitempty"`
	LogMapping    map[string]string    `json:"entityPropertyToLogLabelMapping,omitempty"`
	TraceMapping  map[string]string    `json:"entityPropertyToTraceLabelMapping,omitempty"`
	DefaultConfig bool                 `json:"defaultConfig"`
	DataSourceUID string               `json:"dataSourceUid,omitempty"`
}

type drilldownMatchRule struct {
	EntityProperty string `json:"entityProperty"`
	Value          string `json:"value"`
	Op             string `json:"op"`
}

// matchesEntityType checks if a drilldown config applies to the given entity type.
func (d *drilldownConfigEntry) matchesEntityType(entityType string) bool {
	if d.DefaultConfig {
		return true
	}
	for _, rule := range d.MatchRules {
		if rule.EntityProperty == "type" && rule.Op == "EQ" && rule.Value == entityType {
			return true
		}
	}
	return false
}

// fetchDrilldownConfigs fetches and caches drilldown configs for a given mode.
func fetchDrilldownConfigs(ctx context.Context, client *Client, mode string) []drilldownConfigEntry {
	var cache *ttlCache[[]drilldownConfigEntry]
	var endpoint string

	switch mode {
	case "log":
		cache = drilldownLogCache
		endpoint = "/v1/config/log-drilldown"
	case "trace":
		cache = drilldownTraceCache
		endpoint = "/v1/config/trace-drilldown"
	default:
		return nil
	}

	cacheKey := fmt.Sprintf("%s|%d", client.baseURL, client.orgID)
	if cached, ok := cache.get(cacheKey); ok {
		return cached
	}

	data, err := client.fetchAssertsDataGet(ctx, endpoint, nil)
	if err != nil {
		return nil
	}

	var configs []drilldownConfigEntry
	if err := json.Unmarshal([]byte(data), &configs); err != nil {
		return nil
	}

	cache.set(cacheKey, configs)
	return configs
}

// findDrilldownForEntity finds the best matching drilldown config for an
// entity type. Prefers specific match rules over default configs.
func findDrilldownForEntity(configs []drilldownConfigEntry, entityType string) *drilldownConfigEntry {
	var defaultConfig *drilldownConfigEntry

	for i := range configs {
		cfg := &configs[i]
		if cfg.DefaultConfig {
			if defaultConfig == nil || cfg.Priority > defaultConfig.Priority {
				defaultConfig = cfg
			}
			continue
		}
		if cfg.matchesEntityType(entityType) {
			return cfg
		}
	}

	return defaultConfig
}

// getEntityPropertyValue extracts a named property from a resolved entity.
func getEntityPropertyValue(entity *graphEntityResponse, prop string) string {
	switch prop {
	case "name":
		return entity.Name
	case "type":
		return entity.Type
	case "env":
		return entity.Env
	case "site":
		return entity.Site
	case "namespace":
		return entity.Namespace
	default:
		if entity.Properties != nil {
			if v, ok := entity.Properties[prop]; ok {
				return fmt.Sprintf("%v", v)
			}
		}
		return ""
	}
}

// buildLabelsFromMapping constructs a label selector string from a drilldown
// config's property-to-label mapping and a resolved entity.
func buildLabelsFromMapping(mapping map[string]string, entity *graphEntityResponse, separator string) string {
	if len(mapping) == 0 {
		return ""
	}

	parts := make([]string, 0, len(mapping))
	for entityProp, label := range mapping {
		value := getEntityPropertyValue(entity, entityProp)
		if value != "" {
			parts = append(parts, fmt.Sprintf(`%s="%s"`, label, value))
		}
	}
	if len(parts) == 0 {
		return ""
	}

	return "{" + strings.Join(parts, separator) + "}"
}
