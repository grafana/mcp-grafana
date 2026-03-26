package mcpgrafana

import (
	"context"
	"crypto/sha256"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"sync"

	"github.com/grafana/incident-go"
)

// clientCacheKey uniquely identifies a client by its credentials and target.
type clientCacheKey struct {
	url      string
	apiKey   string
	username string
	password string
	orgID    int64
}

// cacheKeyFromRequest builds a clientCacheKey from request-derived credentials.
func cacheKeyFromRequest(grafanaURL, apiKey string, basicAuth *url.Userinfo, orgID int64) clientCacheKey {
	key := clientCacheKey{
		url:   grafanaURL,
		apiKey: apiKey,
		orgID: orgID,
	}
	if basicAuth != nil {
		key.username = basicAuth.Username()
		key.password, _ = basicAuth.Password()
	}
	return key
}

// String returns a redacted string representation for logging.
func (k clientCacheKey) String() string {
	hasKey := k.apiKey != ""
	hasBasic := k.username != ""
	return fmt.Sprintf("url=%s apiKey=%t basicAuth=%t orgID=%d", k.url, hasKey, hasBasic, k.orgID)
}

// grafanaCacheEntry holds a cached Grafana client.
type grafanaCacheEntry struct {
	client    *GrafanaClient
	transport *http.Transport // held for cleanup
}

// incidentCacheEntry holds a cached incident client.
type incidentCacheEntry struct {
	client    *incident.Client
	transport *http.Transport // held for cleanup
}

// ClientCache caches HTTP clients keyed by credentials to avoid creating
// new transports per request. This prevents the memory leak described in
// https://github.com/grafana/mcp-grafana/issues/682.
type ClientCache struct {
	mu              sync.RWMutex
	grafanaClients  map[clientCacheKey]*grafanaCacheEntry
	incidentClients map[clientCacheKey]*incidentCacheEntry
}

// NewClientCache creates a new client cache.
func NewClientCache() *ClientCache {
	return &ClientCache{
		grafanaClients:  make(map[clientCacheKey]*grafanaCacheEntry),
		incidentClients: make(map[clientCacheKey]*incidentCacheEntry),
	}
}

// GetOrCreateGrafanaClient returns a cached Grafana client for the given key,
// or creates one using createFn if no cached client exists.
func (c *ClientCache) GetOrCreateGrafanaClient(key clientCacheKey, createFn func() *GrafanaClient) *GrafanaClient {
	// Fast path: check with read lock
	c.mu.RLock()
	if entry, ok := c.grafanaClients[key]; ok {
		c.mu.RUnlock()
		return entry.client
	}
	c.mu.RUnlock()

	// Slow path: create with write lock
	c.mu.Lock()
	defer c.mu.Unlock()

	// Double-check after acquiring write lock
	if entry, ok := c.grafanaClients[key]; ok {
		return entry.client
	}

	client := createFn()
	c.grafanaClients[key] = &grafanaCacheEntry{client: client}
	slog.Debug("Cached new Grafana client", "key", key, "cache_size", len(c.grafanaClients))
	return client
}

// GetOrCreateIncidentClient returns a cached incident client for the given key,
// or creates one using createFn if no cached client exists.
func (c *ClientCache) GetOrCreateIncidentClient(key clientCacheKey, createFn func() *incident.Client) *incident.Client {
	// Fast path: check with read lock
	c.mu.RLock()
	if entry, ok := c.incidentClients[key]; ok {
		c.mu.RUnlock()
		return entry.client
	}
	c.mu.RUnlock()

	// Slow path: create with write lock
	c.mu.Lock()
	defer c.mu.Unlock()

	// Double-check after acquiring write lock
	if entry, ok := c.incidentClients[key]; ok {
		return entry.client
	}

	client := createFn()
	c.incidentClients[key] = &incidentCacheEntry{client: client}
	slog.Debug("Cached new incident client", "key", key, "cache_size", len(c.incidentClients))
	return client
}

// Close closes all idle connections on cached transports.
func (c *ClientCache) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()

	for key, entry := range c.grafanaClients {
		if entry.transport != nil {
			entry.transport.CloseIdleConnections()
		}
		delete(c.grafanaClients, key)
	}
	for key, entry := range c.incidentClients {
		if entry.transport != nil {
			entry.transport.CloseIdleConnections()
		}
		delete(c.incidentClients, key)
	}
	slog.Debug("Client cache closed")
}

// Size returns the number of cached clients (for testing/metrics).
func (c *ClientCache) Size() (grafana, incident int) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.grafanaClients), len(c.incidentClients)
}

// hashAPIKey returns a short hash of the API key for use in logging.
// This avoids logging the full key.
func hashAPIKey(key string) string {
	if key == "" {
		return ""
	}
	h := sha256.Sum256([]byte(key))
	return fmt.Sprintf("%x", h[:4])
}

// extractGrafanaClientCached creates an httpContextFunc that uses the cache.
func extractGrafanaClientCached(cache *ClientCache) httpContextFunc {
	return func(ctx context.Context, req *http.Request) context.Context {
		config := GrafanaConfigFromContext(ctx)
		if config.OrgID == 0 {
			slog.Warn("No org ID found in request headers or environment variables, using default org. Set GRAFANA_ORG_ID or pass X-Grafana-Org-Id header to target a specific org.")
		}

		u, apiKey, basicAuth, _ := extractKeyGrafanaInfoFromReq(req)
		key := cacheKeyFromRequest(u, apiKey, basicAuth, config.OrgID)

		grafanaClient := cache.GetOrCreateGrafanaClient(key, func() *GrafanaClient {
			slog.Debug("Creating new Grafana client (cache miss)", "url", u, "api_key_hash", hashAPIKey(apiKey))
			return NewGrafanaClient(ctx, u, apiKey, basicAuth)
		})

		return WithGrafanaClient(ctx, grafanaClient)
	}
}

// extractIncidentClientCached creates an httpContextFunc that uses the cache.
func extractIncidentClientCached(cache *ClientCache) httpContextFunc {
	return func(ctx context.Context, req *http.Request) context.Context {
		grafanaURL, apiKey, _, orgID := extractKeyGrafanaInfoFromReq(req)
		key := cacheKeyFromRequest(grafanaURL, apiKey, nil, orgID)

		incidentClient := cache.GetOrCreateIncidentClient(key, func() *incident.Client {
			incidentURL := fmt.Sprintf("%s/api/plugins/grafana-irm-app/resources/api/v1/", grafanaURL)
			slog.Debug("Creating new incident client (cache miss)", "url", incidentURL)
			client := incident.NewClient(incidentURL, apiKey)

			config := GrafanaConfigFromContext(ctx)
			transport, err := BuildTransport(&config, nil)
			if err != nil {
				slog.Error("Failed to create custom transport for incident client, using default", "error", err)
			} else {
				orgIDWrapped := NewOrgIDRoundTripper(transport, orgID)
				client.HTTPClient.Transport = wrapWithUserAgent(orgIDWrapped)
			}

			return client
		})

		return WithIncidentClient(ctx, incidentClient)
	}
}
