package mcpgrafana

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

type grafanaOverrideKey struct{}

// ParseGrafanaURLOverrides validates a comma-separated, exact base-URL allowlist.
// An empty list leaves targets unrestricted when overrides are enabled.
func ParseGrafanaURLOverrides(raw string) ([]string, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	parts := strings.Split(raw, ",")
	allowed := make([]string, 0, len(parts))
	for _, part := range parts {
		candidate := strings.TrimRight(strings.TrimSpace(part), "/")
		if err := validateOverrideURL(candidate); err != nil {
			return nil, fmt.Errorf("invalid Grafana URL override allowlist entry: %w", err)
		}
		allowed = append(allowed, candidate)
	}
	return allowed, nil
}

func validateOverrideURL(raw string) error {
	if err := ValidateGrafanaURL(raw); err != nil {
		return err
	}
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || u.Opaque != "" || u.RawQuery != "" || u.Fragment != "" || u.ForceQuery {
		return fmt.Errorf("URL must be an absolute HTTP(S) base URL without query or fragment")
	}
	if _, err := url.Parse("http://" + u.Host); err != nil {
		return fmt.Errorf("invalid host: %w", err)
	}
	if !canonicalGrafanaOverridePath(u.Path) {
		return fmt.Errorf("URL path must not contain dot segments, repeated separators, backslashes, or percent signs")
	}
	return nil
}

// Reject path forms that a downstream proxy might normalize or decode into a
// different route after the base-path check. u.Path is already URL-decoded once;
// a percent sign there could introduce dot segments on a second decode.
func canonicalGrafanaOverridePath(p string) bool {
	if strings.ContainsAny(p, `\%`) || strings.Contains(p, "//") {
		return false
	}
	for _, segment := range strings.Split(p, "/") {
		if segment == "." || segment == ".." {
			return false
		}
	}
	return true
}

// GrafanaURLOverrideMiddleware authorizes request-selected URLs before any
// clients are created. A caller must supply its own Grafana service account
// token. When enabled and allowed is empty, callers may select any valid
// HTTP(S) URL; operators must restrict access to trusted callers.
// The selected URL is passed through a private context key so header-only
// calls to the extractors cannot enable it.
func GrafanaURLOverrideMiddleware(enabled bool, allowed []string, next http.Handler) http.Handler {
	set := make(map[string]struct{}, len(allowed))
	for _, u := range allowed {
		set[u] = struct{}{}
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		values := r.Header.Values(grafanaURLHeader)
		if len(values) == 0 {
			next.ServeHTTP(w, r)
			return
		}
		if len(values) != 1 || values[0] == "" {
			http.Error(w, "invalid X-Grafana-URL header", http.StatusBadRequest)
			return
		}
		if !enabled {
			http.Error(w, "X-Grafana-URL is disabled; set GRAFANA_ALLOW_URL_OVERRIDE=true", http.StatusForbidden)
			return
		}
		raw := strings.TrimRight(values[0], "/")
		if err := validateOverrideURL(raw); err != nil {
			http.Error(w, "invalid X-Grafana-URL header", http.StatusBadRequest)
			return
		}
		if len(set) > 0 {
			if _, ok := set[raw]; !ok {
				http.Error(w, "X-Grafana-URL is not allowlisted", http.StatusForbidden)
				return
			}
		}
		if apiKeyFromHeaders(r) == "" {
			http.Error(w, "X-Grafana-URL requires X-Grafana-Service-Account-Token or X-Grafana-API-Key on this request", http.StatusBadRequest)
			return
		}
		r = r.WithContext(context.WithValue(r.Context(), grafanaOverrideKey{}, raw))
		next.ServeHTTP(w, r)
	})
}

// grafanaTargetTransport prevents redirects and other secondary requests from
// escaping the selected Grafana base URL, including through auth transports
// that would otherwise reattach credentials on a redirect.
type grafanaTargetTransport struct {
	baseURL string
	next    http.RoundTripper
}

func (t *grafanaTargetTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	base, err := url.Parse(t.baseURL)
	if err != nil {
		return nil, err
	}
	u := req.URL
	if u.Scheme != base.Scheme || !strings.EqualFold(u.Host, base.Host) ||
		(req.Host != "" && !strings.EqualFold(req.Host, base.Host)) ||
		(base.Path != "" && (!canonicalGrafanaOverridePath(u.Path) ||
			(u.Path != base.Path && !strings.HasPrefix(u.Path, strings.TrimRight(base.Path, "/")+"/")))) {
		return nil, fmt.Errorf("grafana URL override blocked request outside selected target")
	}
	return t.next.RoundTrip(req)
}
