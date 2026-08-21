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
// datasource proxy URL path and falls back to an alternate on 401, 403 or 500
// responses. This handles compatibility between different Grafana deployments:
//   - Azure Managed Grafana requires /api/datasources/uid/{uid}/resources
//   - AWS Managed Grafana requires /api/datasources/proxy/uid/{uid}
//
// In legacy mode (datasource resolved through the frontend-settings fallback,
// see datasources_fallback.go) the retry rules differ: the primary is the
// numeric-id proxy path, and the only reason its uid-based fallback exists is
// deployments that disable the deprecated numeric routes (404, default since
// Grafana 13). Legacy mode therefore retries on 404 only — on pre-9.0 the
// uid routes answer garbage (400 "id is invalid" on 8.x, 500 on 7.x), so
// retrying a genuine 401/403/500 from the working numeric primary would mask
// the real error with a routing error.
//
// The 401 fallback also covers datasources with basic auth: the /resources
// proxy does not always forward the datasource's configured basic auth to the
// upstream (which then answers 401), whereas the legacy /proxy path does.
//
// See https://github.com/grafana/mcp-grafana/issues/524
type datasourceFallbackTransport struct {
	wrapped      http.RoundTripper
	primaryBase  string // e.g., "/api/datasources/uid/{uid}/resources"
	fallbackBase string // e.g., "/api/datasources/proxy/uid/{uid}"
	// legacy marks transports whose datasource was resolved through the
	// frontend-settings fallback. It changes two behaviors:
	//   - retries happen on 404 only (see the type comment), and
	//   - the process-wide fallbackEndpoints cache is skipped: its key is a
	//     path string with no notion of the Grafana instance, org or
	//     credentials, which is fine for uid-based paths (uids rarely collide
	//     across tenants) but not for numeric-id paths —
	//     /api/datasources/proxy/1/... is the same string on every tenant, so
	//     a Grafana 13+ tenant recording a fallback hit would pin the
	//     uid-based path for a Grafana 8.x tenant, whose uid routes answer
	//     400.
	legacy bool
}

// shouldRetry reports whether the primary response status warrants trying
// the fallback base.
func (t *datasourceFallbackTransport) shouldRetry(status int) bool {
	if t.legacy {
		return status == http.StatusNotFound
	}
	return status == http.StatusUnauthorized ||
		status == http.StatusForbidden ||
		status == http.StatusInternalServerError
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
// datasources_fallback.go): primary is the numeric-id proxy path, retries
// happen on 404 only, and the shared endpoint cache is skipped (see the
// legacy field). The numeric primary is expected to succeed on the
// deployments that use this mode; the retry only fires in the rare
// newer-Grafana-with-restricted-token case where the numeric routes are
// disabled.
func newLegacyDatasourceFallbackTransport(wrapped http.RoundTripper, primaryBase, fallbackBase string) http.RoundTripper {
	return &datasourceFallbackTransport{
		wrapped:      wrapped,
		primaryBase:  primaryBase,
		fallbackBase: fallbackBase,
		legacy:       true,
	}
}

func (t *datasourceFallbackTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	cacheKey := t.fallbackCacheKey(req)

	// Check cache: if we already know the fallback works, use it directly.
	if !t.legacy {
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

	if !t.shouldRetry(resp.StatusCode) {
		return resp, nil
	}

	// A retryable status — try the fallback endpoint (see the type comment
	// for which statuses are retryable in which mode, and why).
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
	if !t.legacy && retryResp.StatusCode >= 200 && retryResp.StatusCode < 300 {
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
