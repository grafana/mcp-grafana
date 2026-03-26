package mcpgrafana

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
)

// GrafanaCapabilities holds the result of API capability detection for a Grafana instance.
// It determines whether to use legacy or Kubernetes-style APIs.
type GrafanaCapabilities struct {
	// HasKubernetesAPIs is true if the Grafana instance supports /apis at all.
	HasKubernetesAPIs bool
	// Registry contains discovered API groups and versions. Nil if no k8s APIs.
	Registry *ResourceRegistry
}

// DiscoverCapabilities calls GET /apis on the Grafana instance and returns
// what API groups are available. If /apis returns 404, the instance only
// supports legacy APIs.
func (c *KubernetesClient) DiscoverCapabilities(ctx context.Context) (*GrafanaCapabilities, error) {
	registry, err := c.Discover(ctx)
	if err != nil {
		var apiErr *KubernetesAPIError
		if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusNotFound {
			slog.DebugContext(ctx, "Grafana instance does not support Kubernetes APIs (404 from /apis)")
			return &GrafanaCapabilities{HasKubernetesAPIs: false}, nil
		}
		return nil, err
	}

	slog.DebugContext(ctx, "Discovered Kubernetes API capabilities", "groups", registry.Groups())
	return &GrafanaCapabilities{
		HasKubernetesAPIs: true,
		Registry:          registry,
	}, nil
}

// ShouldUseKubernetesAPI checks whether a specific API group is available
// via Kubernetes-style APIs on this Grafana instance.
func (caps *GrafanaCapabilities) ShouldUseKubernetesAPI(apiGroup string) bool {
	if caps == nil || !caps.HasKubernetesAPIs || caps.Registry == nil {
		return false
	}
	return caps.Registry.HasGroup(apiGroup)
}
