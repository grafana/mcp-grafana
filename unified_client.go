package mcpgrafana

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/grafana/grafana-openapi-client-go/client/dashboards"
	"github.com/grafana/grafana-openapi-client-go/models"
	"github.com/mark3labs/mcp-go/server"
	"golang.org/x/sync/singleflight"
)

// GrafanaAPIClient provides unified access to Grafana APIs, transparently
// routing between legacy REST and Kubernetes-style APIs based on server
// capabilities. Tools use this client without needing to know which API
// backend is in use.
type GrafanaAPIClient struct {
	legacy *GrafanaClient    // existing OpenAPI client (may be nil)
	k8s    *KubernetesClient // generic k8s client (may be nil)

	// Cached capability detection
	capsMu     sync.Mutex
	caps       *grafanaCapabilities
	capsExpiry time.Time
	capsSF     singleflight.Group
}

// grafanaCapabilities holds cached /apis discovery results.
type grafanaCapabilities struct {
	hasK8sAPIs bool
	registry   *ResourceRegistry
}

const capabilitiesTTL = 1 * time.Minute

// NewGrafanaAPIClient creates a unified client from the legacy and k8s clients.
// Either may be nil; the client will degrade gracefully.
func NewGrafanaAPIClient(legacy *GrafanaClient, k8s *KubernetesClient) *GrafanaAPIClient {
	return &GrafanaAPIClient{
		legacy: legacy,
		k8s:    k8s,
	}
}

// discoverCapabilities calls GET /apis once and caches the result.
// If k8s client is nil or /apis returns an error, returns hasK8sAPIs=false.
// Uses singleflight + detached context so a cancelled caller context
// doesn't fail the fetch for all waiters.
func (c *GrafanaAPIClient) discoverCapabilities(ctx context.Context) *grafanaCapabilities {
	// Fast path: check cache under lock.
	c.capsMu.Lock()
	if c.caps != nil && time.Now().Before(c.capsExpiry) {
		caps := c.caps
		c.capsMu.Unlock()
		return caps
	}
	c.capsMu.Unlock()

	if c.k8s == nil {
		caps := &grafanaCapabilities{hasK8sAPIs: false}
		c.capsMu.Lock()
		c.caps = caps
		c.capsExpiry = time.Now().Add(capabilitiesTTL)
		c.capsMu.Unlock()
		return caps
	}

	// Use singleflight to coalesce concurrent discovery requests.
	result, _, _ := c.capsSF.Do("discover", func() (any, error) {
		// Double-check cache inside singleflight.
		c.capsMu.Lock()
		if c.caps != nil && time.Now().Before(c.capsExpiry) {
			caps := c.caps
			c.capsMu.Unlock()
			return caps, nil
		}
		c.capsMu.Unlock()

		// Use a detached context with timeout so that a cancelled request
		// context from the first caller doesn't fail the fetch for all waiters.
		// We still propagate the GrafanaConfig values for auth.
		fetchCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		// Copy GrafanaConfig into the detached context for auth.
		cfg := GrafanaConfigFromContext(ctx)
		fetchCtx = WithGrafanaConfig(fetchCtx, cfg)

		registry, err := c.k8s.Discover(fetchCtx)
		var caps *grafanaCapabilities
		if err != nil {
			slog.Debug("k8s API discovery failed, using legacy APIs only", "error", err)
			caps = &grafanaCapabilities{hasK8sAPIs: false}
		} else {
			caps = &grafanaCapabilities{
				hasK8sAPIs: len(registry.Groups()) > 0,
				registry:   registry,
			}
		}

		c.capsMu.Lock()
		c.caps = caps
		c.capsExpiry = time.Now().Add(capabilitiesTTL)
		c.capsMu.Unlock()

		return caps, nil
	})

	return result.(*grafanaCapabilities)
}

// shouldUseK8s returns true if the given API group is available via k8s APIs.
func (c *GrafanaAPIClient) shouldUseK8s(ctx context.Context, apiGroup string) bool {
	caps := c.discoverCapabilities(ctx)
	if !caps.hasK8sAPIs || caps.registry == nil {
		return false
	}
	return caps.registry.HasGroup(apiGroup)
}

// getResourceOpts configures how getResource routes and converts a single resource fetch.
type getResourceOpts struct {
	// K8s routing info
	apiGroup  string // e.g. "dashboard.grafana.app"
	resource  string // plural, e.g. "dashboards"
	name      string
	namespace string // defaults to "default"

	// Legacy fallback
	legacyFetch func(ctx context.Context) (interface{}, error)

	// K8s -> legacy type conversion
	convert func(obj map[string]interface{}) (interface{}, error)

	// Whether to check for 406 on legacy errors
	check406 func(err error) bool
}

// getResource fetches a single resource, routing to k8s or legacy based on capabilities.
// If legacy returns 406, transparently retries via k8s.
func (c *GrafanaAPIClient) getResource(ctx context.Context, opts getResourceOpts) (interface{}, error) {
	ns := opts.namespace
	if ns == "" {
		ns = "default"
	}

	// If k8s APIs are available for this group, prefer k8s.
	if c.shouldUseK8s(ctx, opts.apiGroup) {
		return c.getResourceViaK8s(ctx, opts, ns)
	}

	// Otherwise use legacy, with optional 406 retry.
	if opts.legacyFetch == nil {
		return nil, fmt.Errorf("no legacy fetch function provided and k8s API group %q not available", opts.apiGroup)
	}

	result, err := opts.legacyFetch(ctx)
	if err != nil && opts.check406 != nil && opts.check406(err) {
		slog.Debug("Legacy API returned 406, invalidating cache and retrying via k8s API", "group", opts.apiGroup)
		// Invalidate cached capabilities: 406 means the server has switched
		// to k8s APIs since our last discovery.
		c.capsMu.Lock()
		c.caps = nil
		c.capsMu.Unlock()
		return c.getResourceViaK8s(ctx, opts, ns)
	}
	return result, err
}

// getResourceViaK8s fetches a resource via the k8s API and converts it.
func (c *GrafanaAPIClient) getResourceViaK8s(ctx context.Context, opts getResourceOpts, namespace string) (interface{}, error) {
	if c.k8s == nil {
		return nil, fmt.Errorf("k8s client not available")
	}
	if opts.convert == nil {
		return nil, fmt.Errorf("no k8s-to-legacy converter provided")
	}

	caps := c.discoverCapabilities(ctx)
	version := ""
	if caps.registry != nil {
		version = caps.registry.PreferredVersion(opts.apiGroup)
	}
	if version == "" {
		return nil, fmt.Errorf("no preferred version found for API group %q", opts.apiGroup)
	}

	desc := ResourceDescriptor{
		Group:    opts.apiGroup,
		Version:  version,
		Resource: opts.resource,
	}

	obj, err := c.k8s.Get(ctx, desc, namespace, opts.name)
	if err != nil {
		return nil, fmt.Errorf("k8s get %s/%s: %w", opts.resource, opts.name, err)
	}

	return opts.convert(obj)
}

// dashboardAPIGroup is the k8s API group for dashboards.
const dashboardAPIGroup = "dashboard.grafana.app"

// GetDashboardByUID fetches a dashboard by UID.
// Transparently uses k8s API if available, otherwise legacy.
func (c *GrafanaAPIClient) GetDashboardByUID(ctx context.Context, uid string) (*models.DashboardFullWithMeta, error) {
	result, err := c.getResource(ctx, getResourceOpts{
		apiGroup:  dashboardAPIGroup,
		resource:  "dashboards",
		name:      uid,
		namespace: "default",

		legacyFetch: func(ctx context.Context) (interface{}, error) {
			if c.legacy == nil {
				return nil, fmt.Errorf("legacy client not available")
			}
			resp, err := c.legacy.Dashboards.GetDashboardByUID(uid)
			if err != nil {
				return nil, err
			}
			return resp.Payload, nil
		},

		convert: convertK8sDashboard,

		check406: func(err error) bool {
			var notAcceptable *dashboards.GetDashboardByUIDNotAcceptable
			return errors.As(err, &notAcceptable)
		},
	})
	if err != nil {
		return nil, fmt.Errorf("get dashboard by uid %s: %w", uid, err)
	}

	dashboard, ok := result.(*models.DashboardFullWithMeta)
	if !ok {
		return nil, fmt.Errorf("unexpected result type %T", result)
	}
	return dashboard, nil
}

// convertK8sDashboard converts a k8s dashboard response to the legacy
// DashboardFullWithMeta format. It extracts `spec` as the dashboard JSON,
// `metadata.name` as UID, and `metadata.annotations["grafana.app/folder"]`
// as the folder UID.
func convertK8sDashboard(obj map[string]interface{}) (interface{}, error) {
	// Extract spec as the dashboard body.
	spec, ok := obj["spec"].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("k8s dashboard missing or invalid 'spec' field")
	}

	// Extract metadata.
	metadata, _ := obj["metadata"].(map[string]interface{})

	// Set UID from metadata.name.
	uid := ""
	if metadata != nil {
		if name, ok := metadata["name"].(string); ok {
			uid = name
			spec["uid"] = uid
		}
	}

	// Extract folder UID from annotations.
	folderUID := ""
	if metadata != nil {
		if annotations, ok := metadata["annotations"].(map[string]interface{}); ok {
			if folder, ok := annotations["grafana.app/folder"].(string); ok {
				folderUID = folder
			}
		}
	}

	// Extract title for metadata.
	title, _ := spec["title"].(string)

	result := &models.DashboardFullWithMeta{
		Dashboard: spec,
		Meta: &models.DashboardMeta{
			Slug:      uid,
			FolderUID: folderUID,
			Type:      "db",
			IsFolder:  false,
			URL:       fmt.Sprintf("/d/%s/%s", uid, title),
		},
	}

	return result, nil
}

// Context integration for GrafanaAPIClient.

type grafanaAPIClientKey struct{}

// WithGrafanaAPIClient sets the unified API client in the context.
func WithGrafanaAPIClient(ctx context.Context, c *GrafanaAPIClient) context.Context {
	return context.WithValue(ctx, grafanaAPIClientKey{}, c)
}

// GrafanaAPIClientFromContext retrieves the unified API client from the context.
// Returns nil if no client has been set.
func GrafanaAPIClientFromContext(ctx context.Context) *GrafanaAPIClient {
	c, ok := ctx.Value(grafanaAPIClientKey{}).(*GrafanaAPIClient)
	if !ok {
		return nil
	}
	return c
}

// ExtractGrafanaAPIClientFromEnv is a StdioContextFunc that creates and injects
// a unified GrafanaAPIClient into the context. It runs AFTER the legacy and k8s
// extractors, pulling both from context and combining them.
var ExtractGrafanaAPIClientFromEnv server.StdioContextFunc = func(ctx context.Context) context.Context {
	legacy := GrafanaClientFromContext(ctx)

	// Create k8s client from the config already in context.
	k8s, err := NewKubernetesClient(ctx)
	if err != nil {
		slog.Debug("Failed to create k8s client for unified API client, k8s APIs will be unavailable", "error", err)
	}

	apiClient := NewGrafanaAPIClient(legacy, k8s)
	return WithGrafanaAPIClient(ctx, apiClient)
}

// ExtractGrafanaAPIClientFromHeaders is a httpContextFunc that creates and injects
// a unified GrafanaAPIClient into the context for HTTP-based transports.
var ExtractGrafanaAPIClientFromHeaders httpContextFunc = func(ctx context.Context, req *http.Request) context.Context {
	legacy := GrafanaClientFromContext(ctx)

	k8s, err := NewKubernetesClient(ctx)
	if err != nil {
		slog.Debug("Failed to create k8s client for unified API client, k8s APIs will be unavailable", "error", err)
	}

	apiClient := NewGrafanaAPIClient(legacy, k8s)
	return WithGrafanaAPIClient(ctx, apiClient)
}
