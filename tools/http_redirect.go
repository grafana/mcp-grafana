package tools

import (
	"fmt"
	"net/http"
)

// refuseRedirect is an http.Client.CheckRedirect callback for clients that POST
// request bodies to Grafana (the datasource proxy and /api/ds/query endpoints).
//
// Per RFC 7231, Go's http.Client silently downgrades a redirected POST to a GET
// and drops the request body when it follows a 301/302/303 response. A Grafana
// URL that triggers a redirect — e.g. an http:// URL when root_url enforces
// https://, a missing/extra trailing path, or a host that 30x-es to a canonical
// name — therefore causes the datasource to receive an empty-bodied GET. The
// failure is opaque: Elasticsearch responds with a confusing 400 "request body
// or source parameter is required" rather than anything pointing at the URL.
//
// Refusing to follow the redirect turns that silent corruption into an
// actionable error naming both URLs.
func refuseRedirect(req *http.Request, via []*http.Request) error {
	orig := req.URL
	if len(via) > 0 {
		orig = via[0].URL
	}
	return fmt.Errorf(
		"refusing to follow HTTP redirect from %s to %s: following it would drop the request body and silently corrupt the query; "+
			"check that your Grafana URL (GRAFANA_URL or X-Grafana-URL) uses the correct scheme and host and matches Grafana's configured root_url",
		orig, req.URL,
	)
}
