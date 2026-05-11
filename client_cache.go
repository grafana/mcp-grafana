package mcpgrafana

import (
	"context"
	"crypto/sha256"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/grafana/incident-go"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"golang.org/x/sync/singleflight"
)

// defaultClientCacheMaxEntries caps the per-type (grafana / incident)
// map size. Without a cap, per-user auth (one APIKey per user) lets
// the cache grow without bound over the process lifetime — a soft
// memory leak in long-running deployments. 1000 fits typical
// deployments comfortably; operators with much larger user
// populations can build their own ClientCache via NewClientCacheSized.
const defaultClientCacheMaxEntries = 1000

const clientCacheMeterName = "mcp-grafana"

// clientCacheKey uniquely identifies a client by its credentials, target, and forwarded headers.
type clientCacheKey struct {
	url              string
	apiKey           string
	username         string
	password         string
	orgID            int64
	forwardedHeaders string // sorted, serialized forwarded headers for cache differentiation
}

// cacheKeyFromRequest builds a clientCacheKey from request-derived credentials and forwarded headers.
func cacheKeyFromRequest(grafanaURL, apiKey string, basicAuth *url.Userinfo, orgID int64, req *http.Request) clientCacheKey {
	key := clientCacheKey{
		url:    grafanaURL,
		apiKey: apiKey,
		orgID:  orgID,
	}
	if basicAuth != nil {
		key.username = basicAuth.Username()
		key.password, _ = basicAuth.Password()
	}
	if req != nil {
		headers := forwardedHeadersFromRequest(req)
		if len(headers) > 0 {
			names := make([]string, 0, len(headers))
			for k := range headers {
				names = append(names, k)
			}
			sort.Strings(names)
			var sb strings.Builder
			for _, k := range names {
				sb.WriteString(k)
				sb.WriteByte('=')
				sb.WriteString(headers[k])
				sb.WriteByte(',')
			}
			key.forwardedHeaders = sb.String()
		}
	}
	return key
}

// String returns a redacted string representation for logging.
func (k clientCacheKey) String() string {
	hasKey := k.apiKey != ""
	hasBasic := k.username != ""
	return fmt.Sprintf("url=%s apiKey=%t basicAuth=%t orgID=%d forwardedHeaders=%s", k.url, hasKey, hasBasic, k.orgID, k.forwardedHeaders)
}

// clientCacheMetrics holds OTel instruments for cache observability.
type clientCacheMetrics struct {
	lookups metric.Int64Counter // Total lookups (hits + misses)
	hits    metric.Int64Counter // Cache hits
	misses  metric.Int64Counter // Cache misses (new client created)
	size    metric.Int64Gauge   // Current number of cached clients
}

func newClientCacheMetrics() clientCacheMetrics {
	meter := otel.GetMeterProvider().Meter(clientCacheMeterName)

	lookups, _ := meter.Int64Counter("mcp.client_cache.lookups",
		metric.WithDescription("Total number of client cache lookups"),
		metric.WithUnit("{lookup}"),
	)
	hits, _ := meter.Int64Counter("mcp.client_cache.hits",
		metric.WithDescription("Number of client cache hits (existing client reused)"),
		metric.WithUnit("{hit}"),
	)
	misses, _ := meter.Int64Counter("mcp.client_cache.misses",
		metric.WithDescription("Number of client cache misses (new client created)"),
		metric.WithUnit("{miss}"),
	)
	size, _ := meter.Int64Gauge("mcp.client_cache.size",
		metric.WithDescription("Current number of cached clients"),
		metric.WithUnit("{client}"),
	)

	return clientCacheMetrics{
		lookups: lookups,
		hits:    hits,
		misses:  misses,
		size:    size,
	}
}

var (
	attrClientTypeGrafana  = attribute.String("client.type", "grafana")
	attrClientTypeIncident = attribute.String("client.type", "incident")
)

// ClientCache caches HTTP clients keyed by credentials to avoid creating
// new transports per request. This prevents the memory leak described in
// https://github.com/grafana/mcp-grafana/issues/682.
//
// Eviction: each per-type map is capped at maxEntries. When a new entry
// would push past the cap, the least-recently-used entry (the one whose
// lastUsed timestamp is oldest) is dropped. Without this cap, per-user
// auth (one distinct APIKey per user) would grow the cache linearly
// with the unique-user count over the process lifetime — a soft leak.
type ClientCache struct {
	mu              sync.RWMutex
	grafanaClients  map[clientCacheKey]*grafanaCacheEntry
	incidentClients map[clientCacheKey]*incidentCacheEntry
	maxEntries      int
	metrics         clientCacheMetrics
	sfGrafana       singleflight.Group
	sfIncident      singleflight.Group
	logger          *slog.Logger
}

type grafanaCacheEntry struct {
	client   *GrafanaClient
	lastUsed time.Time
}

type incidentCacheEntry struct {
	client   *incident.Client
	lastUsed time.Time
}

// NewClientCache creates a new client cache with the default per-type
// max-entries cap.
func NewClientCache(logger *slog.Logger) *ClientCache {
	return NewClientCacheSized(logger, defaultClientCacheMaxEntries)
}

// NewClientCacheSized creates a new client cache with a custom per-type
// max-entries cap. <=0 means unbounded (legacy behaviour).
func NewClientCacheSized(logger *slog.Logger, maxEntries int) *ClientCache {
	if logger == nil {
		logger = slog.Default()
	}
	return &ClientCache{
		grafanaClients:  make(map[clientCacheKey]*grafanaCacheEntry),
		incidentClients: make(map[clientCacheKey]*incidentCacheEntry),
		maxEntries:      maxEntries,
		metrics:         newClientCacheMetrics(),
		logger:          logger,
	}
}

// GetOrCreateGrafanaClient returns a cached Grafana client for the given key,
// or creates one using createFn if no cached client exists.
// The createFn is called outside the cache lock via singleflight to avoid
// blocking concurrent cache reads during slow client creation (e.g. network I/O).
func (c *ClientCache) GetOrCreateGrafanaClient(key clientCacheKey, createFn func() *GrafanaClient) *GrafanaClient {
	ctx := context.Background()
	typeAttr := metric.WithAttributes(attrClientTypeGrafana)
	c.metrics.lookups.Add(ctx, 1, typeAttr)

	// Fast path: check with read lock. Promote lastUsed under the
	// write lock if found, so eviction reflects actual usage.
	c.mu.RLock()
	if entry, ok := c.grafanaClients[key]; ok {
		client := entry.client
		c.mu.RUnlock()
		c.metrics.hits.Add(ctx, 1, typeAttr)
		c.touchGrafana(key)
		return client
	}
	c.mu.RUnlock()

	// Slow path: use singleflight to create outside the lock,
	// deduplicating concurrent requests for the same key.
	// Use fmt.Sprintf("%v", key) for the singleflight key to include actual
	// credential values (the struct fields), not the redacted String() output.
	sfKey := fmt.Sprintf("%v", key)
	val, _, _ := c.sfGrafana.Do(sfKey, func() (any, error) {
		// Double-check after winning the singleflight race
		c.mu.RLock()
		if entry, ok := c.grafanaClients[key]; ok {
			c.mu.RUnlock()
			return entry.client, nil
		}
		c.mu.RUnlock()

		// Create the client without holding any lock
		client := createFn()

		// Store the result
		c.mu.Lock()
		c.evictGrafanaIfFullLocked()
		c.grafanaClients[key] = &grafanaCacheEntry{client: client, lastUsed: time.Now()}
		c.metrics.misses.Add(ctx, 1, typeAttr)
		c.metrics.size.Record(ctx, int64(len(c.grafanaClients)), typeAttr)
		c.logger.Debug("Cached new Grafana client", "key", key, "cache_size", len(c.grafanaClients))
		c.mu.Unlock()

		return client, nil
	})

	return val.(*GrafanaClient)
}

func (c *ClientCache) touchGrafana(key clientCacheKey) {
	c.mu.Lock()
	if entry, ok := c.grafanaClients[key]; ok {
		entry.lastUsed = time.Now()
	}
	c.mu.Unlock()
}

// evictGrafanaIfFullLocked drops the LRU entry from grafanaClients when
// the cap would be exceeded. Caller must hold c.mu write-locked.
func (c *ClientCache) evictGrafanaIfFullLocked() {
	if c.maxEntries <= 0 || len(c.grafanaClients) < c.maxEntries {
		return
	}
	var oldestKey clientCacheKey
	var oldestTime time.Time
	first := true
	for k, e := range c.grafanaClients {
		if first || e.lastUsed.Before(oldestTime) {
			oldestKey = k
			oldestTime = e.lastUsed
			first = false
		}
	}
	delete(c.grafanaClients, oldestKey)
	c.logger.Debug("Evicted LRU Grafana client", "key", oldestKey, "last_used", oldestTime)
}

// GetOrCreateIncidentClient returns a cached incident client for the given key,
// or creates one using createFn if no cached client exists.
// The createFn is called outside the cache lock via singleflight to avoid
// blocking concurrent cache reads during slow client creation.
func (c *ClientCache) GetOrCreateIncidentClient(key clientCacheKey, createFn func() *incident.Client) *incident.Client {
	ctx := context.Background()
	typeAttr := metric.WithAttributes(attrClientTypeIncident)
	c.metrics.lookups.Add(ctx, 1, typeAttr)

	// Fast path: check with read lock. Promote lastUsed under the
	// write lock if found.
	c.mu.RLock()
	if entry, ok := c.incidentClients[key]; ok {
		client := entry.client
		c.mu.RUnlock()
		c.metrics.hits.Add(ctx, 1, typeAttr)
		c.touchIncident(key)
		return client
	}
	c.mu.RUnlock()

	// Slow path: use singleflight to create outside the lock
	sfKey := fmt.Sprintf("%v", key)
	val, _, _ := c.sfIncident.Do(sfKey, func() (any, error) {
		c.mu.RLock()
		if entry, ok := c.incidentClients[key]; ok {
			c.mu.RUnlock()
			return entry.client, nil
		}
		c.mu.RUnlock()

		client := createFn()

		c.mu.Lock()
		c.evictIncidentIfFullLocked()
		c.incidentClients[key] = &incidentCacheEntry{client: client, lastUsed: time.Now()}
		c.metrics.misses.Add(ctx, 1, typeAttr)
		c.metrics.size.Record(ctx, int64(len(c.incidentClients)), typeAttr)
		c.logger.Debug("Cached new incident client", "key", key, "cache_size", len(c.incidentClients))
		c.mu.Unlock()

		return client, nil
	})

	return val.(*incident.Client)
}

func (c *ClientCache) touchIncident(key clientCacheKey) {
	c.mu.Lock()
	if entry, ok := c.incidentClients[key]; ok {
		entry.lastUsed = time.Now()
	}
	c.mu.Unlock()
}

// evictIncidentIfFullLocked drops the LRU entry from incidentClients
// when the cap would be exceeded. Closes the evicted client's idle
// connections before dropping. Caller must hold c.mu write-locked.
func (c *ClientCache) evictIncidentIfFullLocked() {
	if c.maxEntries <= 0 || len(c.incidentClients) < c.maxEntries {
		return
	}
	var oldestKey clientCacheKey
	var oldestTime time.Time
	first := true
	for k, e := range c.incidentClients {
		if first || e.lastUsed.Before(oldestTime) {
			oldestKey = k
			oldestTime = e.lastUsed
			first = false
		}
	}
	if entry, ok := c.incidentClients[oldestKey]; ok && entry.client != nil && entry.client.HTTPClient != nil {
		entry.client.HTTPClient.CloseIdleConnections()
	}
	delete(c.incidentClients, oldestKey)
	c.logger.Debug("Evicted LRU incident client", "key", oldestKey, "last_used", oldestTime)
}

// Close cleans up cached clients. For incident clients, idle connections
// are closed via the underlying HTTP transport. Grafana clients use a
// go-openapi runtime whose transport is set via reflection, so we clear
// the map and let the GC reclaim resources.
func (c *ClientCache) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()

	for key, entry := range c.incidentClients {
		if entry.client != nil && entry.client.HTTPClient != nil {
			entry.client.HTTPClient.CloseIdleConnections()
		}
		delete(c.incidentClients, key)
	}
	for key := range c.grafanaClients {
		delete(c.grafanaClients, key)
	}

	ctx := context.Background()
	c.metrics.size.Record(ctx, 0, metric.WithAttributes(attrClientTypeGrafana))
	c.metrics.size.Record(ctx, 0, metric.WithAttributes(attrClientTypeIncident))
	c.logger.Debug("Client cache closed")
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
		logger := config.LoggerOrDefault()
		if config.OrgID == 0 {
			logger.Warn("No org ID found in request headers or environment variables, using default org. Set GRAFANA_ORG_ID or pass X-Grafana-Org-Id header to target a specific org.")
		}

		u, apiKey, basicAuth, _ := extractKeyGrafanaInfoFromReq(req, logger)
		// Per-user auth (auth.Middleware) pre-sets config.URL and
		// config.APIKey on the context. Those win over header/env so
		// the cached client is keyed by — and created with — the
		// session-derived bearer rather than the shared env token.
		if config.URL != "" {
			u = config.URL
		}
		if config.APIKey != "" {
			apiKey = config.APIKey
		}
		if config.BasicAuth != nil {
			basicAuth = config.BasicAuth
		}
		key := cacheKeyFromRequest(u, apiKey, basicAuth, config.OrgID, req)

		grafanaClient := cache.GetOrCreateGrafanaClient(key, func() *GrafanaClient {
			logger.Debug("Creating new Grafana client (cache miss)", "url", u, "api_key_hash", hashAPIKey(apiKey))
			return NewGrafanaClient(ctx, u, apiKey, basicAuth)
		})

		return WithGrafanaClient(ctx, grafanaClient)
	}
}

// extractIncidentClientCached creates an httpContextFunc that uses the cache.
func extractIncidentClientCached(cache *ClientCache) httpContextFunc {
	return func(ctx context.Context, req *http.Request) context.Context {
		config := GrafanaConfigFromContext(ctx)
		logger := config.LoggerOrDefault()

		grafanaURL, apiKey, _, orgID := extractKeyGrafanaInfoFromReq(req, logger)
		// Per-user auth (auth.Middleware) pre-sets config.URL and
		// config.APIKey on the context. Those win over header/env so
		// the cached incident client is keyed by — and created with —
		// the session-derived bearer rather than the shared env token.
		if config.URL != "" {
			grafanaURL = config.URL
		}
		if config.APIKey != "" {
			apiKey = config.APIKey
		}
		key := cacheKeyFromRequest(grafanaURL, apiKey, nil, orgID, req)

		incidentClient := cache.GetOrCreateIncidentClient(key, func() *incident.Client {
			incidentURL := fmt.Sprintf("%s/api/plugins/grafana-irm-app/resources/api/v1/", grafanaURL)
			logger.Debug("Creating new incident client (cache miss)", "url", incidentURL)
			client := incident.NewClient(incidentURL, apiKey)

			config.OrgID = orgID
			transport, err := BuildTransport(&config, nil, WithoutAuth())
			if err != nil {
				logger.Error("Failed to create custom transport for incident client, using default", "error", err)
			} else {
				client.HTTPClient.Transport = transport
			}

			return client
		})

		return WithIncidentClient(ctx, incidentClient)
	}
}
