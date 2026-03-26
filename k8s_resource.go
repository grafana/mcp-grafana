package mcpgrafana

import "fmt"

// APIGroupList represents the response from GET /apis (Kubernetes API discovery).
type APIGroupList struct {
	Kind   string     `json:"kind"`
	Groups []APIGroup `json:"groups"`
}

// APIGroup represents a single API group in the discovery response.
type APIGroup struct {
	Name             string             `json:"name"`
	Versions         []GroupVersionInfo `json:"versions"`
	PreferredVersion GroupVersionInfo   `json:"preferredVersion"`
}

// GroupVersionInfo contains version information for an API group.
type GroupVersionInfo struct {
	GroupVersion string `json:"groupVersion"`
	Version      string `json:"version"`
}

// ResourceDescriptor describes a Kubernetes-style API resource in Grafana.
// It contains enough information to construct API paths for any k8s-style resource.
type ResourceDescriptor struct {
	Group    string // e.g. "dashboard.grafana.app"
	Version  string // e.g. "v2beta1"
	Resource string // plural name, e.g. "dashboards"
}

// BasePath returns the API path prefix for this resource, including namespace.
// For example: /apis/dashboard.grafana.app/v2beta1/namespaces/default/dashboards
func (d ResourceDescriptor) BasePath(namespace string) string {
	return fmt.Sprintf("/apis/%s/%s/namespaces/%s/%s", d.Group, d.Version, namespace, d.Resource)
}

// ResourceRegistry maps API group names to their available resources and versions.
// It is built from the /apis discovery response (APIGroupList).
//
// ResourceRegistry is immutable after construction via NewResourceRegistry and
// is safe for concurrent reads from multiple goroutines without synchronization.
type ResourceRegistry struct {
	groups map[string]*ResourceGroup
}

// ResourceGroup holds information about a single API group discovered from /apis.
type ResourceGroup struct {
	Name             string
	PreferredVersion string
	AllVersions      []string
}

// NewResourceRegistry creates a ResourceRegistry from an APIGroupList.
func NewResourceRegistry(apiGroupList *APIGroupList) *ResourceRegistry {
	r := &ResourceRegistry{
		groups: make(map[string]*ResourceGroup),
	}
	if apiGroupList == nil {
		return r
	}
	for _, g := range apiGroupList.Groups {
		versions := make([]string, len(g.Versions))
		for i, v := range g.Versions {
			versions[i] = v.Version
		}
		r.groups[g.Name] = &ResourceGroup{
			Name:             g.Name,
			PreferredVersion: g.PreferredVersion.Version,
			AllVersions:      versions,
		}
	}
	return r
}

// GetGroup returns the ResourceGroup for the given API group name, or nil if not found.
func (r *ResourceRegistry) GetGroup(name string) *ResourceGroup {
	if r == nil || r.groups == nil {
		return nil
	}
	return r.groups[name]
}

// HasGroup returns true if the registry contains the given API group.
func (r *ResourceRegistry) HasGroup(name string) bool {
	return r.GetGroup(name) != nil
}

// PreferredVersion returns the preferred version for the given API group.
// Returns an empty string if the group is not found.
func (r *ResourceRegistry) PreferredVersion(group string) string {
	g := r.GetGroup(group)
	if g == nil {
		return ""
	}
	return g.PreferredVersion
}

// Groups returns a list of all known API group names.
func (r *ResourceRegistry) Groups() []string {
	if r == nil || r.groups == nil {
		return nil
	}
	names := make([]string, 0, len(r.groups))
	for name := range r.groups {
		names = append(names, name)
	}
	return names
}
