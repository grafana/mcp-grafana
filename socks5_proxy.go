package mcpgrafana

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"
)

// parseSOCKS5ProxyURL parses and validates a SOCKS5 proxy URL for use with
// http.Transport.Proxy. Only the socks5 and socks5h schemes are accepted
// (net/http treats them identically: hostname resolution is delegated to the
// proxy). The URL must consist of scheme, optional userinfo, host, and
// optional port only.
//
// Error messages never include the raw input: proxy URLs may embed
// credentials, and these errors end up in logs and stderr.
func parseSOCKS5ProxyURL(raw string) (*url.URL, error) {
	if raw == "" {
		return nil, fmt.Errorf("SOCKS5 proxy URL is empty")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("SOCKS5 proxy URL is not a valid URL")
	}
	if u.Scheme != "socks5" && u.Scheme != "socks5h" {
		return nil, fmt.Errorf("SOCKS5 proxy URL must use the socks5 or socks5h scheme, got %q", u.Scheme)
	}
	if u.Opaque != "" {
		return nil, fmt.Errorf("SOCKS5 proxy URL must be of the form socks5://host:port")
	}
	if u.Hostname() == "" {
		return nil, fmt.Errorf("SOCKS5 proxy URL must include a host")
	}
	if port := u.Port(); port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return nil, fmt.Errorf("SOCKS5 proxy URL port must be between 1 and 65535")
		}
	}
	if u.Path != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return nil, fmt.Errorf("SOCKS5 proxy URL must not contain a path, query, or fragment")
	}
	return u, nil
}

// failClosedRoundTripper fails every request with the stored error.
type failClosedRoundTripper struct {
	err error
}

func (t *failClosedRoundTripper) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, t.err
}

// failClosedTransport returns an http.RoundTripper that fails every request
// with err. Call sites that fall back to a default transport when
// BuildTransport fails must use it instead when a SOCKS5 proxy is configured,
// so that a misconfigured transport never silently bypasses the proxy.
func failClosedTransport(err error) http.RoundTripper {
	return &failClosedRoundTripper{err: err}
}

// ValidateSOCKS5ProxyURL reports whether raw is a valid SOCKS5 proxy URL as
// accepted by GrafanaConfig.SOCKS5ProxyURL. It is intended for early
// validation at startup so that misconfiguration fails fast instead of
// surfacing later when the first Grafana client is built.
func ValidateSOCKS5ProxyURL(raw string) error {
	_, err := parseSOCKS5ProxyURL(raw)
	return err
}

// clientTransport builds the standard middleware transport for cfg (see
// BuildTransport) and applies the SOCKS5 fail-closed policy in one place, so
// every caller that assigns a transport to an http.Client treats a
// misconfiguration identically instead of re-implementing the decision.
//
// The returned RoundTripper is always safe to assign to an http.Client:
//
//   - build succeeded                        → the built transport, ok=true
//   - build failed, SOCKS5 proxy configured   → a fail-closed transport that
//     rejects every request (so traffic can never silently bypass the proxy),
//     ok=true
//   - build failed, no proxy configured        → nil, ok=false (the caller
//     should keep its existing/default transport)
//
// Build failures are logged via cfg's logger.
func (cfg *GrafanaConfig) clientTransport(base http.RoundTripper, opts ...TransportOption) (http.RoundTripper, bool) {
	transport, err := BuildTransport(cfg, base, opts...)
	if err == nil {
		return transport, true
	}
	logger := cfg.LoggerOrDefault()
	if cfg.SOCKS5ProxyURL != "" {
		logger.Error("Failed to build HTTP transport, failing closed because a SOCKS5 proxy is configured", "error", err)
		return failClosedTransport(err), true
	}
	logger.Error("Failed to build HTTP transport, falling back to the default transport", "error", err)
	return nil, false
}

// ProxyOnlyTransport returns an http.RoundTripper that routes requests through
// the configured SOCKS5 proxy, if any, but applies no other middleware. Use it
// for requests to third-party services (e.g. the grafana.com plugin catalog)
// that must honour the proxy in an egress-restricted network but must never
// receive Grafana credentials, forwarded headers, or org identifiers.
//
// The returned RoundTripper is always safe to use:
//
//   - no proxy configured         → http.DefaultTransport
//   - proxy configured & valid    → a clone of http.DefaultTransport with Proxy set
//   - proxy configured & invalid  → a fail-closed transport that rejects every
//     request, so the request never bypasses the proxy
func (cfg *GrafanaConfig) ProxyOnlyTransport() http.RoundTripper {
	if cfg.SOCKS5ProxyURL == "" {
		return http.DefaultTransport
	}
	proxyURL, err := parseSOCKS5ProxyURL(cfg.SOCKS5ProxyURL)
	if err != nil {
		cfg.LoggerOrDefault().Error("Failed to parse SOCKS5 proxy URL, failing closed", "error", err)
		return failClosedTransport(err)
	}
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.Proxy = http.ProxyURL(proxyURL)
	return t
}
