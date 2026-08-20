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
// datasource proxy URL path and falls back to an alternate on 403, 404 or 500
// responses. This handles compatibility between different Grafana deployments:
//   - Azure Managed Grafana requires /api/datasources/uid/{uid}/resources
//   - AWS Managed Grafana requires /api/datasources/proxy/uid/{uid}
//   - Grafana before 9.0 has neither uid-based route; requests to them fall
//     into the numeric :id route and fail with a 500
//   - Grafana 13+ disables the deprecated numeric-id routes by default,
//     returning a 404
//
// See https://github.com/grafana/mcp-grafana/issues/524
type datasourceFallbackTransport struct {
	wrapped      http.RoundTripper
	primaryBase  string // e.g., "/api/datasources/uid/{uid}/resources"
	fallbackBase string // e.g., "/api/datasources/proxy/uid/{uid}"
	// skipCache disables the process-wide fallbackEndpoints cache. The cache
	// key is a path string with no notion of the Grafana instance, org or
	// credentials, which is fine for uid-based paths (uids rarely collide
	// across tenants) but not for the numeric-id paths used in legacy mode:
	// /api/datasources/proxy/1/... is the same string on every tenant, so a
	// Grafana 13+ tenant recording a fallback hit would pin the uid-based
	// path for a Grafana 8.x tenant, whose uid routes answer 400.
	skipCache bool
}

// fallbackEndpoints caches which datasource proxy paths need the fallback
// endpoint. Key is the primary base path plus request path suffix, value is
// true if fallback is needed.
var fallbackEndpoints sync.Map

func newDatasourceFallbackTransport(wrapped http.RoundTripper, primaryBase, fallbackBase string) http.RoundTripper {
	return &datasourceFallbackTransport{
		wrapped:      wrapped,
		primaryBase:  primaryBase,
		fallbackBase: fallbackBase,
	}
}

// newLegacyDatasourceFallbackTransport is the variant used when the
// datasource was resolved through the frontend-settings fallback (see
// datasources_fallback.go): primary is the numeric-id proxy path and the
// shared endpoint cache is skipped, because numeric-id paths collide across
// tenants (see skipCache). The cache buys nothing here anyway — the numeric
// primary is expected to succeed on the deployments that use this mode, and
// the retry only fires in the rare newer-Grafana-with-restricted-token case.
func newLegacyDatasourceFallbackTransport(wrapped http.RoundTripper, primaryBase, fallbackBase string) http.RoundTripper {
	return &datasourceFallbackTransport{
		wrapped:      wrapped,
		primaryBase:  primaryBase,
		fallbackBase: fallbackBase,
		skipCache:    true,
	}
}

func (t *datasourceFallbackTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	cacheKey := t.fallbackCacheKey(req)

	// Check cache: if we already know the fallback works, use it directly.
	if !t.skipCache {
		if useFallback, ok := fallbackEndpoints.Load(cacheKey); ok && useFallback.(bool) {
			return t.wrapped.RoundTrip(t.rewriteRequest(req, t.primaryBase, t.fallbackBase))
		}
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
		req.ContentLength = int64(len(bodyBytes))
	}

	resp, err := t.wrapped.RoundTrip(req)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusForbidden && resp.StatusCode != http.StatusNotFound && resp.StatusCode != http.StatusInternalServerError {
		return resp, nil
	}

	// Got 403, 404 or 500 — try the fallback endpoint. A missing route
	// surfaces differently per deployment: 403 on some managed platforms, 404
	// when the route is not registered (e.g. the numeric-id routes disabled by
	// default on Grafana 13+), and 500 when a uid-based path falls into the
	// numeric :id route on Grafana before 9.0. A 404 from the datasource
	// itself just costs one duplicate request, and the fallback path is only
	// cached on a 2xx response.
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

	// Only cache the fallback path when the fallback actually returned a
	// successful (2xx) response.  A 4xx from the fallback means neither path
	// is working for this particular request; caching it would silently break
	// all subsequent calls that would otherwise succeed via the primary path.
	if !t.skipCache && retryResp.StatusCode >= 200 && retryResp.StatusCode < 300 {
		fallbackEndpoints.Store(cacheKey, true)
	}

	return retryResp, nil
}

func (t *datasourceFallbackTransport) fallbackCacheKey(req *http.Request) string {
	path := req.URL.EscapedPath()
	if path == "" {
		path = req.URL.Path
	}

	suffix, ok := strings.CutPrefix(path, t.primaryBase)
	if !ok {
		return t.primaryBase + "\x00" + path
	}

	return t.primaryBase + suffix
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
