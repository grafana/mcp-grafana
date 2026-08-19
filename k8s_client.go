package mcpgrafana

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
)

// KubernetesClient is a lightweight, generic HTTP client for Grafana's
// Kubernetes-style APIs (/apis/...). It uses unstructured data
// (map[string]interface{}) so callers are not tied to specific Go types.
//
// Authentication is read from the GrafanaConfig in the request context,
// following the same priority as the rest of mcp-grafana:
//
//  1. AccessToken + IDToken (on-behalf-of)
//  2. APIKey (bearer token)
//  3. BasicAuth
type KubernetesClient struct {
	// BaseURL is the root URL of the Grafana instance (e.g. "http://localhost:3000").
	BaseURL string

	// HTTPClient is the underlying HTTP client used for requests.
	// If nil, http.DefaultClient is used.
	HTTPClient *http.Client

	// capMu guards groupVersionsCache.
	capMu sync.Mutex
	// groupVersionsCache caches the served versions per API group, discovered
	// once via GET /apis/<group>. A cached empty slice means the group is not
	// served (404). Capabilities are a property of the target Grafana instance,
	// so this is resolved once per client (i.e. once per connection) and reused;
	// transient discovery errors are not cached, so they are retried.
	groupVersionsCache map[string][]string
}

// NewKubernetesClient creates a KubernetesClient from the GrafanaConfig in ctx.
// It reuses BuildTransport so TLS, extra headers, OrgID, and user-agent are
// handled the same way as for the legacy OpenAPI client.
func NewKubernetesClient(ctx context.Context) (*KubernetesClient, error) {
	cfg := GrafanaConfigFromContext(ctx)

	baseURL := cfg.URL
	if baseURL == "" {
		baseURL = defaultGrafanaURL
	}
	baseURL = strings.TrimRight(baseURL, "/")

	transport, err := BuildTransport(&cfg, nil)
	if err != nil {
		return nil, fmt.Errorf("build transport: %w", err)
	}

	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = DefaultGrafanaClientTimeout
	}

	return &KubernetesClient{
		BaseURL: baseURL,
		HTTPClient: &http.Client{
			Transport: transport,
			Timeout:   timeout,
		},
		groupVersionsCache: make(map[string][]string),
	}, nil
}

// ResourceList is the response shape for a Kubernetes-style list request.
type ResourceList struct {
	Kind       string                   `json:"kind"`
	APIVersion string                   `json:"apiVersion"`
	Items      []map[string]interface{} `json:"items"`
	Metadata   map[string]interface{}   `json:"metadata,omitempty"`
}

// ListOptions controls the behaviour of a List call.
type ListOptions struct {
	// LabelSelector filters results by label (e.g. "app=foo").
	LabelSelector string
	// Limit caps the number of items returned.
	Limit int
	// Continue is a pagination token from a previous list response.
	Continue string
}

// Discover calls GET /apis and returns a ResourceRegistry describing
// available API groups and their versions.
func (c *KubernetesClient) Discover(ctx context.Context) (*ResourceRegistry, error) {
	body, err := c.doRequest(ctx, http.MethodGet, "/apis", nil)
	if err != nil {
		return nil, fmt.Errorf("discover /apis: %w", err)
	}

	var groupList APIGroupList
	if err := json.Unmarshal(body, &groupList); err != nil {
		return nil, fmt.Errorf("decode /apis response: %w", err)
	}

	return NewResourceRegistry(&groupList), nil
}

// validatePathSegment checks that a user-supplied path segment (namespace or
// resource name) does not contain path separators, which could lead to path
// traversal.
func validatePathSegment(kind, value string) error {
	if strings.Contains(value, "/") || strings.Contains(value, "\\") {
		return fmt.Errorf("%s %q must not contain path separators", kind, value)
	}
	return nil
}

// Get fetches a single resource by name.
// Returns the full Kubernetes-style object as unstructured data.
func (c *KubernetesClient) Get(ctx context.Context, desc ResourceDescriptor, namespace, name string) (map[string]interface{}, error) {
	if err := validatePathSegment("namespace", namespace); err != nil {
		return nil, err
	}
	if err := validatePathSegment("name", name); err != nil {
		return nil, err
	}

	path := desc.BasePath(namespace) + "/" + name

	body, err := c.doRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}

	var result map[string]interface{}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return result, nil
}

// List fetches a collection of resources.
func (c *KubernetesClient) List(ctx context.Context, desc ResourceDescriptor, namespace string, opts *ListOptions) (*ResourceList, error) {
	if err := validatePathSegment("namespace", namespace); err != nil {
		return nil, err
	}

	path := desc.BasePath(namespace)

	// Build query string from options using url.Values for proper encoding.
	if opts != nil {
		params := url.Values{}
		if opts.LabelSelector != "" {
			params.Set("labelSelector", opts.LabelSelector)
		}
		if opts.Limit > 0 {
			params.Set("limit", fmt.Sprintf("%d", opts.Limit))
		}
		if opts.Continue != "" {
			params.Set("continue", opts.Continue)
		}
		if encoded := params.Encode(); encoded != "" {
			path += "?" + encoded
		}
	}

	body, err := c.doRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}

	var list ResourceList
	if err := json.Unmarshal(body, &list); err != nil {
		return nil, fmt.Errorf("decode list response: %w", err)
	}
	return &list, nil
}

// Create creates a new resource (POST to the collection endpoint) and returns
// the created object.
func (c *KubernetesClient) Create(ctx context.Context, desc ResourceDescriptor, namespace string, obj map[string]interface{}) (map[string]interface{}, error) {
	if err := validatePathSegment("namespace", namespace); err != nil {
		return nil, err
	}

	payload, err := json.Marshal(obj)
	if err != nil {
		return nil, fmt.Errorf("encode object: %w", err)
	}

	body, err := c.doRequest(ctx, http.MethodPost, desc.BasePath(namespace), bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}

	var result map[string]interface{}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return result, nil
}

// Update replaces an existing resource (PUT to the resource endpoint) and
// returns the updated object. The supplied object must carry the current
// metadata.resourceVersion (from a prior Get) for optimistic concurrency.
func (c *KubernetesClient) Update(ctx context.Context, desc ResourceDescriptor, namespace, name string, obj map[string]interface{}) (map[string]interface{}, error) {
	if err := validatePathSegment("namespace", namespace); err != nil {
		return nil, err
	}
	if err := validatePathSegment("name", name); err != nil {
		return nil, err
	}

	payload, err := json.Marshal(obj)
	if err != nil {
		return nil, fmt.Errorf("encode object: %w", err)
	}

	path := desc.BasePath(namespace) + "/" + name
	body, err := c.doRequest(ctx, http.MethodPut, path, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}

	var result map[string]interface{}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return result, nil
}

// GroupVersions returns the API versions served for the given group, fetched
// from GET /apis/<group> and cached once per client. A non-nil empty slice
// means the group is not served (the discovery endpoint returned 404). Only
// definitive results (200 or 404) are cached; transient errors are returned
// without caching so the next call retries.
func (c *KubernetesClient) GroupVersions(ctx context.Context, group string) ([]string, error) {
	c.capMu.Lock()
	if versions, ok := c.groupVersionsCache[group]; ok {
		c.capMu.Unlock()
		return versions, nil
	}
	c.capMu.Unlock()

	body, err := c.doRequest(ctx, http.MethodGet, "/apis/"+group, nil)
	if err != nil {
		var apiErr *KubernetesAPIError
		if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusNotFound {
			// Group not served — cache the (empty) result.
			c.storeGroupVersions(group, []string{})
			return []string{}, nil
		}
		// Transient/unknown error: don't cache, let the caller retry later.
		return nil, err
	}

	var groupList struct {
		Versions []struct {
			Version string `json:"version"`
		} `json:"versions"`
	}
	if err := json.Unmarshal(body, &groupList); err != nil {
		return nil, fmt.Errorf("decode api group %q discovery: %w", group, err)
	}

	versions := make([]string, 0, len(groupList.Versions))
	for _, v := range groupList.Versions {
		versions = append(versions, v.Version)
	}
	c.storeGroupVersions(group, versions)
	return versions, nil
}

func (c *KubernetesClient) storeGroupVersions(group string, versions []string) {
	c.capMu.Lock()
	if c.groupVersionsCache == nil {
		c.groupVersionsCache = make(map[string][]string)
	}
	c.groupVersionsCache[group] = versions
	c.capMu.Unlock()
}

// SupportsGroupVersion reports whether the given group serves the given version
// (using the cached discovery from GroupVersions). On a transient discovery
// error it returns false, so callers conservatively fall back to the legacy API.
func (c *KubernetesClient) SupportsGroupVersion(ctx context.Context, group, version string) bool {
	versions, err := c.GroupVersions(ctx, group)
	if err != nil {
		return false
	}
	return slices.Contains(versions, version)
}

// KubernetesAPIError is returned when the server responds with a non-2xx status.
type KubernetesAPIError struct {
	StatusCode int
	Status     string
	Body       string
}

func (e *KubernetesAPIError) Error() string {
	return fmt.Sprintf("kubernetes API error: %s (HTTP %d): %s", e.Status, e.StatusCode, e.Body)
}

// doRequest executes an authenticated HTTP request and returns the response body.
// It injects auth headers from the GrafanaConfig in ctx.
func (c *KubernetesClient) doRequest(ctx context.Context, method, path string, reqBody io.Reader) ([]byte, error) {
	url := c.BaseURL + path

	req, err := http.NewRequestWithContext(ctx, method, url, reqBody)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	if reqBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	httpClient := c.HTTPClient
	if httpClient == nil {
		httpClient = http.DefaultClient
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	// Read the body (with a reasonable limit to avoid OOM).
	body, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20)) // 10 MB
	if err != nil {
		return nil, fmt.Errorf("read response body: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &KubernetesAPIError{
			StatusCode: resp.StatusCode,
			Status:     resp.Status,
			Body:       string(body),
		}
	}

	return body, nil
}
