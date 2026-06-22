package tools

import (
	"fmt"
	"net/http"
)

// maxRedirects mirrors the cap Go's default CheckRedirect policy applies.
const maxRedirects = 10

// refuseRedirect is an http.Client.CheckRedirect callback for clients that POST
// request bodies to Grafana (the datasource proxy and /api/ds/query endpoints).
//
// Per RFC 7231, Go's http.Client downgrades a redirected POST to a GET and
// drops the request body when it follows a 301/302/303 response. A Grafana URL
// that triggers such a redirect — e.g. an http:// URL when root_url enforces
// https://, a missing/extra trailing path, or a host that 30x-es to a canonical
// name — therefore causes the datasource to receive an empty-bodied GET. The
// failure is opaque: Elasticsearch responds with a confusing 400 "request body
// or source parameter is required" rather than anything pointing at the URL.
//
// 307 and 308 redirects, by contrast, preserve the method and body, so they are
// safe to follow. Go signals the difference through req.Method: a body-dropping
// redirect changes it (e.g. POST -> GET), while a method-preserving one leaves
// it unchanged. We therefore refuse only the redirects that would silently
// corrupt the request, turning that corruption into an actionable error.
func refuseRedirect(req *http.Request, via []*http.Request) error {
	if len(via) == 0 {
		return nil
	}
	if len(via) >= maxRedirects {
		return fmt.Errorf("stopped after %d redirects", maxRedirects)
	}

	prev := via[len(via)-1]
	if req.Method == prev.Method {
		// Method preserved (307/308, or a redirected GET): no body is lost, so
		// follow the redirect as normal.
		return nil
	}

	return fmt.Errorf(
		"refusing to follow HTTP redirect from %s to %s: it changes the request method from %s to %s and drops the request body, which would silently corrupt the query; "+
			"check that your Grafana URL (GRAFANA_URL or X-Grafana-URL) uses the correct scheme and host and matches Grafana's configured root_url",
		prev.URL, req.URL, prev.Method, req.Method,
	)
}
