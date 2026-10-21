package tools

import (
	"bytes"
	"fmt"
	"io"
	"mime"
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
	// fallbackTrimPrefix is removed from the request path, right after the
	// primary base, when a request is sent to the fallback base. Grafana's
	// Loki /resources handler prepends /loki/api/v1/ to the path it is given,
	// so a Loki API path must reach it without that prefix: /proxy/.../
	// loki/api/v1/query_range and /resources/query_range are the same call.
	fallbackTrimPrefix string
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

// lokiAPIPrefix is the path prefix of every Loki HTTP API route.
const lokiAPIPrefix = "/loki/api/v1"

// newLokiDatasourceFallbackTransport is newDatasourceFallbackTransport for a
// Loki datasource whose primary is the uid proxy path and whose fallback is
// the /resources path: the fallback drops the /loki/api/v1 prefix, which
// Grafana's Loki resource handler adds back (see fallbackTrimPrefix).
func newLokiDatasourceFallbackTransport(wrapped http.RoundTripper, primaryBase, fallbackBase string) http.RoundTripper {
	return &datasourceFallbackTransport{
		wrapped:            wrapped,
		primaryBase:        primaryBase,
		fallbackBase:       fallbackBase,
		fallbackTrimPrefix: lokiAPIPrefix,
	}
}

func (t *datasourceFallbackTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	cacheKey := t.fallbackCacheKey(req)

	// Check cache: if we already know the fallback works, use it directly.
	if !t.legacy {
		if useFallback, ok := fallbackEndpoints.Load(cacheKey); ok && useFallback.(bool) {
			return t.wrapped.RoundTrip(t.fallbackRequest(req))
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
	// for which statuses are retryable in which mode, and why). Keep the
	// primary's error body: it is returned if the fallback turns out to be
	// an HTML page rather than an API answer. A read error is not fatal —
	// keep whatever was read (possibly nothing) and still try the fallback,
	// since that body only matters if the fallback is HTML.
	primaryBody, _ := io.ReadAll(io.LimitReader(resp.Body, maxPrimaryErrorBodyBytes))
	resp.Body.Close() //nolint:errcheck

	retryReq := t.fallbackRequest(req)
	if bodyBytes != nil {
		retryReq.Body = io.NopCloser(bytes.NewReader(bodyBytes))
		retryReq.ContentLength = int64(len(bodyBytes))
	}

	retryResp, retryErr := t.wrapped.RoundTrip(retryReq)
	if retryErr != nil {
		return nil, retryErr
	}

	// A datasource API never answers with HTML. A 2xx HTML response is the
	// upstream's web UI catching an unknown route — Loki, for one, serves its
	// UI's index.html for any path it does not recognise. Treat it as a failed
	// fallback: return the primary's real error and do not cache the route.
	if isHTMLResponse(retryResp) {
		retryResp.Body.Close() //nolint:errcheck
		resp.Body = io.NopCloser(bytes.NewReader(primaryBody))
		resp.ContentLength = int64(len(primaryBody))
		return resp, nil
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

// maxPrimaryErrorBodyBytes bounds how much of the primary's error response is
// kept while the fallback is tried. Error bodies are small; callers only
// surface the first KiB of them.
const maxPrimaryErrorBodyBytes = 64 << 10

// htmlSniffBytes is how much of a response body is read to detect HTML; it
// matches what http.DetectContentType considers.
const htmlSniffBytes = 512

// isHTMLResponse reports whether resp is a successful HTML response, judged by
// its Content-Type or, failing that, by its body. The header alone is not
// enough: Grafana's Loki resource handler labels every body it relays
// application/json, Loki's UI page included. Sniffing reads the start of the
// body, so resp.Body is replaced with a reader that yields the full body.
func isHTMLResponse(resp *http.Response) bool {
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return false
	}
	if mediaType, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type")); err == nil && mediaType == "text/html" {
		return true
	}
	if resp.Body == nil {
		return false
	}

	// A read error is left for the caller to hit on its own read of the body.
	head := make([]byte, htmlSniffBytes)
	n, _ := io.ReadFull(resp.Body, head)
	head = head[:n]
	resp.Body = struct {
		io.Reader
		io.Closer
	}{io.MultiReader(bytes.NewReader(head), resp.Body), resp.Body}

	mediaType, _, err := mime.ParseMediaType(http.DetectContentType(head))
	return err == nil && mediaType == "text/html"
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

// fallbackRequest returns a copy of req addressed to the fallback base.
func (t *datasourceFallbackTransport) fallbackRequest(req *http.Request) *http.Request {
	from := t.primaryBase
	if t.fallbackTrimPrefix != "" && strings.HasPrefix(req.URL.Path, from+t.fallbackTrimPrefix+"/") {
		from += t.fallbackTrimPrefix
	}
	return t.rewriteRequest(req, from, t.fallbackBase)
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
