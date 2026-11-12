package mcpgrafana

import (
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
)

var errCrossOriginRedirect = errors.New("refusing cross-origin redirect from Grafana client")

// redirectGuardTransport is placed immediately before the network transport.
// It catches credentials added by our middleware, request headers, or API
// clients that authenticate independently of AuthRoundTripper.
type redirectGuardTransport struct {
	next http.RoundTripper
}

func (t *redirectGuardTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Response != nil {
		previous := req.Response.Request
		if previous == nil || !sameHTTPOrigin(previous.URL, req.URL) {
			if req.Body != nil {
				_ = req.Body.Close()
			}
			var previousURL *url.URL
			if previous != nil {
				previousURL = previous.URL
			}
			return nil, fmt.Errorf("%w (%s -> %s): set GRAFANA_URL to Grafana's final URL (see root_url), or enable GRAFANA_ALLOW_CROSS_ORIGIN_REDIRECTS in a trusted environment",
				errCrossOriginRedirect, redirectOriginForError(previousURL), redirectOriginForError(req.URL))
		}
	}
	return t.next.RoundTrip(req)
}

// redirectOriginForError excludes URL userinfo, path, query, and fragment,
// which may contain credentials or other sensitive request data.
func redirectOriginForError(u *url.URL) string {
	if u == nil {
		return "<unknown>"
	}
	return (&url.URL{Scheme: u.Scheme, Host: u.Host}).String()
}

func sameHTTPOrigin(a, b *url.URL) bool {
	aScheme, aHost, aPort, aOK := httpOriginParts(a)
	bScheme, bHost, bPort, bOK := httpOriginParts(b)
	return aOK && bOK && aScheme == bScheme && aHost == bHost && aPort == bPort
}

func httpOriginParts(u *url.URL) (scheme, host, port string, ok bool) {
	if u == nil {
		return "", "", "", false
	}
	scheme = strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" || u.Hostname() == "" {
		return "", "", "", false
	}
	rawHost := u.Hostname()
	if addr, err := netip.ParseAddr(rawHost); err == nil {
		// IPv6 zone names identify interfaces and are case-sensitive.
		host = addr.String()
	} else {
		host = strings.ToLower(rawHost)
	}
	port = u.Port()
	if port == "" {
		if scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	} else {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return "", "", "", false
		}
		port = strconv.Itoa(n)
	}
	return scheme, host, port, true
}
