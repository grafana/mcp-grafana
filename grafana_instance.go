package mcpgrafana

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/grafana/grafana-openapi-client-go/client"
)

// grafanaInstanceKey is the context key for GrafanaInstance.
type grafanaInstanceKey struct{}

// GrafanaInstance provides capability-aware access to Grafana APIs.
// It wraps the legacy OpenAPI client and adds support for kubernetes-style APIs
// with automatic capability detection and fallback.
type GrafanaInstance struct {
	// config holds the Grafana configuration.
	config GrafanaConfig

	// legacyClient is the existing OpenAPI client for legacy APIs.
	legacyClient *client.GrafanaHTTPAPI

	// httpClient is used for kubernetes-style API calls.
	httpClient *http.Client

	// baseURL is the Grafana instance URL (without trailing slash).
	baseURL string

	// cache is the capability cache (usually the global cache).
	cache *CapabilityCache
}

// NewGrafanaInstance creates a new GrafanaInstance with the given configuration.
func NewGrafanaInstance(config GrafanaConfig, legacyClient *client.GrafanaHTTPAPI, httpClient *http.Client) *GrafanaInstance {
	baseURL := strings.TrimRight(config.URL, "/")
	if baseURL == "" {
		baseURL = defaultGrafanaURL
	}

	return &GrafanaInstance{
		config:       config,
		legacyClient: legacyClient,
		httpClient:   httpClient,
		baseURL:      baseURL,
		cache:        globalCapabilityCache,
	}
}

// LegacyClient returns the legacy OpenAPI client.
// This is useful for tools that haven't been migrated to capability-aware APIs yet.
func (g *GrafanaInstance) LegacyClient() *client.GrafanaHTTPAPI {
	return g.legacyClient
}

// BaseURL returns the base URL of the Grafana instance.
func (g *GrafanaInstance) BaseURL() string {
	return g.baseURL
}

// Config returns the Grafana configuration.
func (g *GrafanaInstance) Config() GrafanaConfig {
	return g.config
}

// HTTPClient returns the HTTP client used for kubernetes-style API calls.
func (g *GrafanaInstance) HTTPClient() *http.Client {
	return g.httpClient
}

// DiscoverCapabilities fetches the /apis endpoint and caches the result.
// This is called automatically when needed, but can be called explicitly
// to pre-populate the cache.
func (g *GrafanaInstance) DiscoverCapabilities(ctx context.Context) error {
	entry, err := g.discoverAPIsAuthenticated(ctx)
	if err != nil {
		return err
	}

	g.cache.Set(g.baseURL, entry)

	if entry.hasKubernetesAPIs {
		slog.Debug("Discovered kubernetes-style APIs",
			"url", g.baseURL,
			"groups", len(entry.apiGroups))
	} else {
		slog.Debug("No kubernetes-style APIs available, using legacy APIs",
			"url", g.baseURL)
	}

	return nil
}

// HasKubernetesAPIs returns whether this Grafana instance supports kubernetes-style APIs.
// It fetches /apis if the capability hasn't been discovered yet.
func (g *GrafanaInstance) HasKubernetesAPIs(ctx context.Context) (bool, error) {
	entry := g.cache.Get(g.baseURL)
	if entry == nil {
		if err := g.DiscoverCapabilities(ctx); err != nil {
			return false, err
		}
		entry = g.cache.Get(g.baseURL)
	}

	if entry == nil {
		return false, nil
	}

	return entry.hasKubernetesAPIs, nil
}

// GetAPIGroupInfo returns information about a specific API group.
// Returns nil if the API group is not available or kubernetes APIs aren't supported.
func (g *GrafanaInstance) GetAPIGroupInfo(ctx context.Context, apiGroup string) (*APIGroupInfo, error) {
	entry := g.cache.Get(g.baseURL)
	if entry == nil {
		if err := g.DiscoverCapabilities(ctx); err != nil {
			return nil, err
		}
		entry = g.cache.Get(g.baseURL)
	}

	if entry == nil || !entry.hasKubernetesAPIs {
		return nil, nil
	}

	return entry.apiGroups[apiGroup], nil
}

// GetAPICapability returns the current capability setting for a specific API group.
// This determines whether to use legacy or kubernetes-style APIs.
func (g *GrafanaInstance) GetAPICapability(apiGroup string) APICapability {
	return g.cache.GetAPICapability(g.baseURL, apiGroup)
}

// SetAPICapability sets the capability for a specific API group.
// This is typically called when a 406 error is received from a legacy API.
func (g *GrafanaInstance) SetAPICapability(apiGroup string, capability APICapability) {
	g.cache.SetAPICapability(g.baseURL, apiGroup, capability)
	slog.Debug("Updated API capability",
		"url", g.baseURL,
		"apiGroup", apiGroup,
		"capability", capability)
}

// ShouldUseKubernetesAPI determines whether to use kubernetes-style APIs for the given API group.
// Returns true if we've detected that legacy APIs are not available (406 received).
func (g *GrafanaInstance) ShouldUseKubernetesAPI(apiGroup string) bool {
	capability := g.GetAPICapability(apiGroup)
	return capability == APICapabilityKubernetes
}

// GetPreferredVersion returns the preferred version for an API group.
// If a specific version was detected from a 406 error, that takes precedence.
func (g *GrafanaInstance) GetPreferredVersion(ctx context.Context, apiGroup string) (string, error) {
	info, err := g.GetAPIGroupInfo(ctx, apiGroup)
	if err != nil {
		return "", err
	}

	if info == nil {
		return "", fmt.Errorf("API group %s not available", apiGroup)
	}

	return info.PreferredVersion, nil
}

// discoverAPIsAuthenticated fetches /apis with proper authentication.
func (g *GrafanaInstance) discoverAPIsAuthenticated(ctx context.Context) (*capabilityCacheEntry, error) {
	resp, err := g.doKubernetesRequest(ctx, http.MethodGet, "/apis", nil)
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

// doKubernetesRequest performs an HTTP request to a kubernetes-style API endpoint.
func (g *GrafanaInstance) doKubernetesRequest(ctx context.Context, method, path string, body io.Reader) (*http.Response, error) {
	url := g.baseURL + path

	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	// Add authentication headers
	if g.config.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+g.config.APIKey)
	} else if g.config.BasicAuth != nil {
		password, _ := g.config.BasicAuth.Password()
		req.SetBasicAuth(g.config.BasicAuth.Username(), password)
	}

	// Add org ID header if set
	if g.config.OrgID > 0 {
		req.Header.Set(client.OrgIDHeader, fmt.Sprintf("%d", g.config.OrgID))
	}

	return g.httpClient.Do(req)
}

// KubernetesDashboard represents a dashboard in the kubernetes-style API format.
type KubernetesDashboard struct {
	Kind       string                      `json:"kind"`
	APIVersion string                      `json:"apiVersion"`
	Metadata   KubernetesDashboardMetadata `json:"metadata"`
	Spec       map[string]interface{}      `json:"spec"`
	Status     *KubernetesDashboardStatus  `json:"status,omitempty"`
}

// KubernetesDashboardMetadata contains metadata for a kubernetes-style dashboard.
type KubernetesDashboardMetadata struct {
	Name              string            `json:"name"`
	Namespace         string            `json:"namespace"`
	UID               string            `json:"uid,omitempty"`
	ResourceVersion   string            `json:"resourceVersion,omitempty"`
	CreationTimestamp string            `json:"creationTimestamp,omitempty"`
	Annotations       map[string]string `json:"annotations,omitempty"`
	Labels            map[string]string `json:"labels,omitempty"`
}

// KubernetesDashboardStatus contains status information for a kubernetes-style dashboard.
type KubernetesDashboardStatus struct {
	// Add status fields as needed
}

// GetDashboardKubernetes fetches a dashboard using the kubernetes-style API.
func (g *GrafanaInstance) GetDashboardKubernetes(ctx context.Context, uid, version, namespace string) (*KubernetesDashboard, error) {
	if namespace == "" {
		namespace = "default"
	}

	path := fmt.Sprintf("/apis/%s/%s/namespaces/%s/dashboards/%s",
		APIGroupDashboard, version, namespace, uid)

	resp, err := g.doKubernetesRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("get dashboard failed: status %d, body: %s", resp.StatusCode, string(body))
	}

	var dashboard KubernetesDashboard
	if err := json.NewDecoder(resp.Body).Decode(&dashboard); err != nil {
		return nil, fmt.Errorf("decode dashboard: %w", err)
	}

	return &dashboard, nil
}

// WithGrafanaInstance sets the GrafanaInstance in the context.
func WithGrafanaInstance(ctx context.Context, instance *GrafanaInstance) context.Context {
	return context.WithValue(ctx, grafanaInstanceKey{}, instance)
}

// GrafanaInstanceFromContext retrieves the GrafanaInstance from the context.
// Returns nil if no instance has been set.
func GrafanaInstanceFromContext(ctx context.Context) *GrafanaInstance {
	instance, ok := ctx.Value(grafanaInstanceKey{}).(*GrafanaInstance)
	if !ok {
		return nil
	}
	return instance
}
