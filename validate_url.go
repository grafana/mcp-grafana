package mcpgrafana

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// ErrInvalidGrafanaURL is returned (wrapped) by ValidateGrafanaURL when the
// input is not an absolute HTTP or HTTPS URL with a non-empty host. Detect
// with errors.Is.
//
// The nav.go guard in tools/navigation.go:generateDeeplink wraps this
// sentinel when config.URL is malformed (e.g. coming from a bad
// /api/frontend/settings appUrl response), distinguishing that case from
// missing-URL cases for callers that discriminate via errors.Is.
var ErrInvalidGrafanaURL = errors.New("invalid Grafana URL")

// ValidateGrafanaURL returns nil if u is an absolute HTTP or HTTPS URL with a
// non-empty host. Trailing slashes are trimmed before validation so callers do
// not need to pre-normalize; this is the single canonicalization point shared
// by ValidateGrafanaURLMiddleware and the nav.go guard. On failure the
// returned error wraps ErrInvalidGrafanaURL.
//
// url.Parse alone is too lenient: it accepts relative references (/foo),
// unusual schemes (javascript:alert(1)), and URLs without a host (http://).
// ParseRequestURI plus a scheme allow-list plus a host check is the standard
// pattern for validating request-supplied URL headers.
func ValidateGrafanaURL(u string) error {
	u = strings.TrimRight(u, "/")
	if u == "" {
		return fmt.Errorf("%w: URL is empty", ErrInvalidGrafanaURL)
	}
	pu, err := url.ParseRequestURI(u)
	if err != nil {
		return fmt.Errorf("%w: %v (set a valid http:// or https:// URL)", ErrInvalidGrafanaURL, err)
	}
	if pu.Scheme != "http" && pu.Scheme != "https" {
		return fmt.Errorf("%w: scheme %q not allowed (must be http or https)", ErrInvalidGrafanaURL, pu.Scheme)
	}
	if pu.Host == "" {
		return fmt.Errorf("%w: URL has no host", ErrInvalidGrafanaURL)
	}
	return nil
}

// ValidateGrafanaURLMiddleware returns an http.Handler middleware that rejects
// requests whose X-Grafana-URL header is present but fails ValidateGrafanaURL,
// responding with 400 Bad Request. Requests without the header pass through
// unchanged (downstream extractors apply the env-variable fallback).
//
// Library consumers that wire mcp-grafana's context functions into their own
// http.Server should install this middleware to match the binary's defensive
// behavior. Consumers that call NewGrafanaClient directly (stdio or
// programmatic construction) should pre-validate the URL with
// ValidateGrafanaURL instead.
func ValidateGrafanaURLMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if raw := r.Header.Get(grafanaURLHeader); raw != "" {
			if err := ValidateGrafanaURL(raw); err != nil {
				http.Error(w, fmt.Sprintf("invalid X-Grafana-URL header: %v", err), http.StatusBadRequest)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}
