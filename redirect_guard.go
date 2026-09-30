package mcpgrafana

import (
	"errors"
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
			return nil, errCrossOriginRedirect
		}
	}
	return t.next.RoundTrip(req)
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
