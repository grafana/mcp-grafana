package tools

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
)

// datasourceFallbackTransport is an http.RoundTripper that tries a primary
// datasource proxy URL path and falls back to an alternate on 403 or 500
// responses. This handles compatibility between different Grafana deployments:
//   - Azure Managed Grafana requires /api/datasources/uid/{uid}/resources
//   - AWS Managed Grafana requires /api/datasources/proxy/uid/{uid}
//
// See https://github.com/grafana/mcp-grafana/issues/524
type datasourceFallbackTransport struct {
	wrapped      http.RoundTripper
	primaryBase  string // e.g., "/api/datasources/uid/{uid}/resources"
	fallbackBase string // e.g., "/api/datasources/proxy/uid/{uid}"
}

// fallbackEndpoints caches which datasource proxy paths need the fallback
// endpoint. Key is the primary base path, value is true if fallback is needed.
var fallbackEndpoints sync.Map

func newDatasourceFallbackTransport(wrapped http.RoundTripper, primaryBase, fallbackBase string) http.RoundTripper {
	return &datasourceFallbackTransport{
		wrapped:      wrapped,
		primaryBase:  primaryBase,
		fallbackBase: fallbackBase,
	}
}

func (t *datasourceFallbackTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Check cache: if we already know the fallback works, use it directly.
	if useFallback, ok := fallbackEndpoints.Load(t.primaryBase); ok && useFallback.(bool) {
		return t.wrapped.RoundTrip(t.rewriteRequest(req, t.primaryBase, t.fallbackBase))
	}

	// Buffer the request body so we can replay it on retry.
	var bodyBytes []byte
	if req.Body != nil {
		var err error
		bodyBytes, err = io.ReadAll(req.Body)
		req.Body.Close() //nolint:errcheck
		if err != nil {
			return nil, fmt.Errorf("buffering request body for fallback: %w", err)
		}
		req.Body = io.NopCloser(bytes.NewReader(bodyBytes))
	}

	resp, err := t.wrapped.RoundTrip(req)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusForbidden && resp.StatusCode != http.StatusInternalServerError {
		return resp, nil
	}

	// Got 403 or 500 — try the fallback endpoint.
	resp.Body.Close() //nolint:errcheck

	retryReq := t.rewriteRequest(req, t.primaryBase, t.fallbackBase)
	if bodyBytes != nil {
		retryReq.Body = io.NopCloser(bytes.NewReader(bodyBytes))
		retryReq.ContentLength = int64(len(bodyBytes))
	}

	retryResp, retryErr := t.wrapped.RoundTrip(retryReq)
	if retryErr != nil {
		return nil, retryErr
	}

	// If the fallback succeeded, remember it for future requests.
	if retryResp.StatusCode != http.StatusForbidden && retryResp.StatusCode != http.StatusInternalServerError {
		fallbackEndpoints.Store(t.primaryBase, true)
	}

	return retryResp, nil
}

func (t *datasourceFallbackTransport) rewriteRequest(req *http.Request, from, to string) *http.Request {
	clone := req.Clone(req.Context())
	clone.URL.Path = strings.Replace(clone.URL.Path, from, to, 1)
	if clone.URL.RawPath != "" {
		clone.URL.RawPath = strings.Replace(clone.URL.RawPath, from, to, 1)
	}
	return clone
}

// datasourceProxyPaths returns the /resources and /proxy base paths for a
// given datasource UID.
func datasourceProxyPaths(uid string) (resourcesBase, proxyBase string) {
	resourcesBase = fmt.Sprintf("/api/datasources/uid/%s/resources", uid)
	proxyBase = fmt.Sprintf("/api/datasources/proxy/uid/%s", uid)
	return
}
