package mcpgrafana

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/go-openapi/runtime"
	"github.com/go-openapi/strfmt"
	"github.com/grafana/grafana-openapi-client-go/client"
	"github.com/grafana/incident-go"
)

// ErrInvalidGrafanaURL is returned by tool handlers when the caller-supplied
// X-Grafana-URL header was malformed or used a non-HTTP(S) scheme, so the
// request could not be associated with a valid Grafana instance. It is
// surfaced via sentinel *GrafanaClient / *incident.Client values attached to
// the request context, so tool handlers receive a structured error from
// their normal API-call error path rather than nil-dereffing on a missing
// client.
//
// Detect with errors.Is(err, ErrInvalidGrafanaURL). The wrapped parse error
// is rendered with %v, not %w, so errors.As cannot unwrap the underlying
// *url.Error. This is intentional: the sentinel is the stable contract;
// callers should not depend on the inner error type.
//
// Fires only on the HTTP header-based transport path (SSE / streamable-http).
// Callers that invoke NewGrafanaClient directly with an invalid URL still
// panic. Use ValidateGrafanaURL to validate URLs before passing to
// NewGrafanaClient from library code.
var ErrInvalidGrafanaURL = errors.New("invalid X-Grafana-URL header")

// ValidateGrafanaURL returns nil if u parses as an absolute HTTP or HTTPS URL
// with a non-empty host. It is exported so library consumers can validate
// URLs before passing them to NewGrafanaClient (which panics on invalid
// input). On failure the returned error wraps ErrInvalidGrafanaURL.
//
// url.Parse alone is too lenient: it accepts relative references (/foo),
// unusual schemes (javascript:alert(1)), and URLs without a host (http://).
// ParseRequestURI plus a scheme allow-list plus a host check is the standard
// pattern for validating request-supplied URL headers.
func ValidateGrafanaURL(u string) error {
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

// validateHeaderURL returns an error if the request's X-Grafana-URL header is
// present but malformed or non-http(s). It returns nil when the header is
// absent (callers fall back to the env-configured URL downstream) or valid.
func validateHeaderURL(req *http.Request) error {
	u := strings.TrimRight(req.Header.Get(grafanaURLHeader), "/")
	if u == "" {
		return nil
	}
	return ValidateGrafanaURL(u)
}

// errorClientTransport implements runtime.ClientTransport. Every operation
// returns the configured error. Used to back sentinel Grafana clients that
// stand in for a real client when the request's X-Grafana-URL header failed
// validation.
type errorClientTransport struct {
	err error
}

// Submit satisfies runtime.ClientTransport.
func (t *errorClientTransport) Submit(*runtime.ClientOperation) (interface{}, error) {
	return nil, t.err
}

// newSentinelGrafanaClient returns a *GrafanaClient whose openapi-backed API
// methods all return err. The openapi client's generated SetTransport
// propagates the sentinel transport to every sub-resource (Dashboards,
// Datasources, ...), so any tool handler that calls a method on the returned
// client receives err from its normal error-return path.
//
// If err is nil the sentinel would silently return (nil, nil) from every API
// call, a hidden nil-panic setup for callers. Substitute ErrInvalidGrafanaURL
// so the sentinel always propagates a detectable failure.
func newSentinelGrafanaClient(err error) *GrafanaClient {
	if err == nil {
		err = ErrInvalidGrafanaURL
	}
	c := client.NewHTTPClient(strfmt.Default)
	c.SetTransport(&errorClientTransport{err: err})
	return &GrafanaClient{
		GrafanaHTTPAPI: c,
	}
}

// errorRoundTripper implements http.RoundTripper, returning the configured
// error for every request. Used to back sentinel incident-go clients, which
// expose an http.Client rather than a typed runtime.ClientTransport.
type errorRoundTripper struct {
	err error
}

// RoundTrip satisfies http.RoundTripper.
func (t *errorRoundTripper) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, t.err
}

// newSentinelIncidentClient returns a *incident.Client whose HTTP calls all
// fail with err. The URL passed to incident.NewClient is never used in
// practice because the sentinel RoundTripper short-circuits before any
// request is issued.
//
// If err is nil the sentinel would return (nil, nil) from every call. See
// newSentinelGrafanaClient for the rationale; the same substitution applies.
func newSentinelIncidentClient(err error) *incident.Client {
	if err == nil {
		err = ErrInvalidGrafanaURL
	}
	c := incident.NewClient("http://invalid.sentinel", "")
	c.HTTPClient.Transport = &errorRoundTripper{err: err}
	return c
}
