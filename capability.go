package mcpgrafana

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"sync"
	"time"
)

// APICapability represents the detected API capability mode for a specific API area.
type APICapability string

const (
	// APICapabilityUnknown indicates capability detection has not been performed.
	APICapabilityUnknown APICapability = ""
	// APICapabilityLegacy indicates legacy /api endpoints should be used.
	APICapabilityLegacy APICapability = "legacy"
	// APICapabilityKubernetes indicates kubernetes-style /apis endpoints should be used.
	APICapabilityKubernetes APICapability = "kubernetes"
)

// API group names for Grafana's kubernetes-style APIs.
const (
	APIGroupDashboard   = "dashboard.grafana.app"
	APIGroupFolder      = "folder.grafana.app"
	APIGroupIAM         = "iam.grafana.app"
	APIGroupUserStorage = "userstorage.grafana.app"
	APIGroupFeatures    = "features.grafana.app"
)

// DefaultCacheTTL is the default time-to-live for capability cache entries.
const DefaultCacheTTL = 1 * time.Minute

// APIGroupList represents the response from GET /apis (Kubernetes API discovery).
type APIGroupList struct {
	Kind   string     `json:"kind"`
	Groups []APIGroup `json:"groups"`
}

// APIGroup represents a single API group in the discovery response.
type APIGroup struct {
	Name                       string             `json:"name"`
	Versions                   []GroupVersionInfo `json:"versions"`
	PreferredVersion           GroupVersionInfo   `json:"preferredVersion"`
	ServerAddressByClientCIDRs []ServerAddress    `json:"serverAddressByClientCIDRs,omitempty"`
}

// GroupVersionInfo contains version information for an API group.
type GroupVersionInfo struct {
	GroupVersion string `json:"groupVersion"`
	Version      string `json:"version"`
}

// ServerAddress contains server address information (usually not needed by clients).
type ServerAddress struct {
	ClientCIDR    string `json:"clientCIDR"`
	ServerAddress string `json:"serverAddress"`
}

// APIGroupInfo holds discovered info about a Kubernetes-style API group.
type APIGroupInfo struct {
	// Available indicates whether this API group is available.
	Available bool
	// PreferredVersion is the server's preferred version for this API group.
	PreferredVersion string
	// AllVersions contains all available versions for this API group.
	AllVersions []string
}

// capabilityCacheEntry holds cached capability information for a Grafana instance.
type capabilityCacheEntry struct {
	// hasKubernetesAPIs indicates whether this instance supports kubernetes-style APIs at all.
	hasKubernetesAPIs bool

	// apiGroups contains per-API-group info (only populated if hasKubernetesAPIs is true).
	// Key is the API group name, e.g., "dashboard.grafana.app".
	apiGroups map[string]*APIGroupInfo

	// perAPICapability tracks which capability to use for each API area.
	// This can differ from hasKubernetesAPIs because legacy APIs may still work
	// even when kubernetes APIs are available. We only switch when we get 406.
	// Key is the API group name.
	perAPICapability map[string]APICapability

	// detectedAt is when this entry was created.
	detectedAt time.Time
}

// CapabilityCache provides thread-safe caching of API capabilities keyed by Grafana URL.
// This is necessary because HTTP transports create clients per-request, but we want
// to avoid rediscovering capabilities on every request.
type CapabilityCache struct {
	entries map[string]*capabilityCacheEntry
	mu      sync.RWMutex
	ttl     time.Duration
}

// NewCapabilityCache creates a new CapabilityCache with the specified TTL.
func NewCapabilityCache(ttl time.Duration) *CapabilityCache {
	return &CapabilityCache{
		entries: make(map[string]*capabilityCacheEntry),
		ttl:     ttl,
	}
}

// globalCapabilityCache is the default cache used by all GrafanaInstance objects.
var globalCapabilityCache = NewCapabilityCache(DefaultCacheTTL)

// Get retrieves a cache entry for the given URL, or nil if not found or expired.
func (c *CapabilityCache) Get(grafanaURL string) *capabilityCacheEntry {
	c.mu.RLock()
	defer c.mu.RUnlock()

	entry, ok := c.entries[grafanaURL]
	if !ok {
		return nil
	}

	// Check if entry has expired
	if time.Since(entry.detectedAt) > c.ttl {
		return nil
	}

	return entry
}

// Set stores a cache entry for the given URL.
func (c *CapabilityCache) Set(grafanaURL string, entry *capabilityCacheEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[grafanaURL] = entry
}

// SetAPICapability updates the capability for a specific API group.
// This is used when we receive a 406 and need to switch to kubernetes APIs.
func (c *CapabilityCache) SetAPICapability(grafanaURL, apiGroup string, capability APICapability) {
	c.mu.Lock()
	defer c.mu.Unlock()

	entry, ok := c.entries[grafanaURL]
	if !ok {
		// Create a minimal entry if none exists
		entry = &capabilityCacheEntry{
			hasKubernetesAPIs: capability == APICapabilityKubernetes,
			apiGroups:         make(map[string]*APIGroupInfo),
			perAPICapability:  make(map[string]APICapability),
			detectedAt:        time.Now(),
		}
		c.entries[grafanaURL] = entry
	}

	if entry.perAPICapability == nil {
		entry.perAPICapability = make(map[string]APICapability)
	}
	entry.perAPICapability[apiGroup] = capability
}

// GetAPICapability returns the capability for a specific API group.
// Returns APICapabilityUnknown if not set.
func (c *CapabilityCache) GetAPICapability(grafanaURL, apiGroup string) APICapability {
	c.mu.RLock()
	defer c.mu.RUnlock()

	entry, ok := c.entries[grafanaURL]
	if !ok {
		return APICapabilityUnknown
	}

	if entry.perAPICapability == nil {
		return APICapabilityUnknown
	}

	return entry.perAPICapability[apiGroup]
}

// Clear removes all entries from the cache.
func (c *CapabilityCache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = make(map[string]*capabilityCacheEntry)
}

// Invalidate removes the entry for a specific URL.
func (c *CapabilityCache) Invalidate(grafanaURL string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.entries, grafanaURL)
}

// DiscoverAPIs fetches the /apis endpoint and parses the response.
// Returns a cache entry with the discovered capabilities.
// If /apis returns 404, it means kubernetes-style APIs aren't available.
func DiscoverAPIs(ctx context.Context, httpClient *http.Client, baseURL string) (*capabilityCacheEntry, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/apis", nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch /apis: %w", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	// 404 means no kubernetes-style APIs available
	if resp.StatusCode == http.StatusNotFound {
		return &capabilityCacheEntry{
			hasKubernetesAPIs: false,
			perAPICapability:  make(map[string]APICapability),
			detectedAt:        time.Now(),
		}, nil
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("unexpected status from /apis: %d, body: %s", resp.StatusCode, string(body))
	}

	var apiGroupList APIGroupList
	if err := json.NewDecoder(resp.Body).Decode(&apiGroupList); err != nil {
		return nil, fmt.Errorf("decode /apis response: %w", err)
	}

	entry := &capabilityCacheEntry{
		hasKubernetesAPIs: true,
		apiGroups:         make(map[string]*APIGroupInfo),
		perAPICapability:  make(map[string]APICapability),
		detectedAt:        time.Now(),
	}

	for _, group := range apiGroupList.Groups {
		versions := make([]string, len(group.Versions))
		for i, v := range group.Versions {
			versions[i] = v.Version
		}
		entry.apiGroups[group.Name] = &APIGroupInfo{
			Available:        true,
			PreferredVersion: group.PreferredVersion.Version,
			AllVersions:      versions,
		}
	}

	return entry, nil
}

// k8sAPIPattern matches kubernetes-style API paths in error messages.
// Groups: 1=apiGroup, 2=version, 3=namespace, 4=resource
var k8sAPIPattern = regexp.MustCompile(
	`/apis/([a-z.]+)/(v[0-9]+(?:alpha|beta)?[0-9]*)/namespaces/([^/]+)/([^/\s]+)`,
)

// ParseKubernetesAPIPath extracts API information from a kubernetes-style API path.
// This is useful for parsing 406 error messages that suggest the correct API endpoint.
func ParseKubernetesAPIPath(path string) (apiGroup, version, namespace, resource string, ok bool) {
	matches := k8sAPIPattern.FindStringSubmatch(path)
	if len(matches) >= 5 {
		return matches[1], matches[2], matches[3], matches[4], true
	}
	return "", "", "", "", false
}

// Parse406Error attempts to extract the suggested kubernetes API version from a 406 error.
// Returns the API group, version, and whether parsing was successful.
func Parse406Error(errMsg string) (apiGroup, version string, ok bool) {
	apiGroup, version, _, _, ok = ParseKubernetesAPIPath(errMsg)
	return apiGroup, version, ok
}

// GetGlobalCapabilityCache returns the global capability cache instance.
func GetGlobalCapabilityCache() *CapabilityCache {
	return globalCapabilityCache
}

// ResetGlobalCapabilityCache clears the global capability cache.
// This is primarily useful for testing.
func ResetGlobalCapabilityCache() {
	globalCapabilityCache.Clear()
}
