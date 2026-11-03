package mcpgrafana

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"strings"
)

// SetGrafanaURLParams are the arguments accepted by the set_grafana_url tool.
type SetGrafanaURLParams struct {
	URL string `json:"url" jsonschema:"required,description=Base URL or store name to connect to. A single-label store name such as tesco-6 is normalized to http://tesco-6/grafana; fully qualified URLs keep their scheme and path."`

	// Token replaces the service-account token used with the selected URL.
	// Omit it to make unauthenticated requests.
	Token *string `json:"token,omitempty" jsonschema:"description=Grafana service account token (or API key) to authenticate with the selected URL. Omit to make unauthenticated requests."`
}

// SetGrafanaURLResult confirms the connection selected for future tool calls.
type SetGrafanaURLResult struct {
	URL     string `json:"url"`
	Message string `json:"message"`
}

// NewSetGrafanaURLTool creates a set_grafana_url tool for a stdio server. The
// current server uses request-scoped HTTP configuration, so HTTP clients should
// use X-Grafana-URL and X-Grafana-Service-Account-Token instead.
func NewSetGrafanaURLTool(runtimeURL *RuntimeGrafanaURL) Tool {
	return MustTool(
		"set_grafana_url",
		"Call this first when the user names a store or Grafana target, before using Grafana-dependent tools. Configure the Grafana instance this stdio MCP server connects to, and optionally its auth token. Pass a store name such as tesco-6 exactly as provided; it resolves to http://tesco-6/grafana. The selected connection applies to future tool calls in this MCP session.",
		func(_ context.Context, args SetGrafanaURLParams) (*SetGrafanaURLResult, error) {
			return setGrafanaURL(runtimeURL, args)
		},
		WithTitleAnnotation("Connect to Grafana store"),
		WithReadOnlyHintAnnotation(false),
		WithDestructiveHintAnnotation(false),
		WithIdempotentHintAnnotation(true),
		WithOpenWorldHintAnnotation(true),
	).NotOrgScoped()
}

func setGrafanaURL(runtimeURL *RuntimeGrafanaURL, args SetGrafanaURLParams) (*SetGrafanaURLResult, error) {
	if runtimeURL == nil {
		return nil, fmt.Errorf("runtime Grafana URL configuration is unavailable")
	}

	url := normalizeSetGrafanaURL(args.URL)
	if err := ValidateGrafanaURL(url); err != nil {
		return nil, fmt.Errorf("invalid URL: %w", err)
	}

	var token string
	if args.Token != nil {
		token = *args.Token
	}
	runtimeURL.Set(url, token)

	return &SetGrafanaURLResult{
		URL:     url,
		Message: fmt.Sprintf("Grafana URL configured. Future tool calls will connect to %s.", url),
	}, nil
}

// normalizeSetGrafanaURL adds the conventional /grafana subpath for an
// on-premise store alias while preserving normal Grafana URLs. A store alias is
// a single-label hostname (for example, tesco-6), excluding localhost and IP
// addresses. It always uses HTTP because those stores terminate Grafana on
// their internal network. An explicit non-root path remains untouched for
// installations that use a different Grafana subpath.
func normalizeSetGrafanaURL(raw string) string {
	normalized := normalizeGrafanaURL(raw)
	parsed, err := url.Parse(normalized)
	if err != nil || !isStoreAlias(parsed.Hostname()) {
		return normalized
	}

	parsed.Scheme = "http"
	if parsed.Path == "" || parsed.Path == "/" {
		parsed.Path = "/grafana"
	}
	return strings.TrimRight(parsed.String(), "/")
}

func isStoreAlias(host string) bool {
	host = strings.ToLower(host)
	return host != "" &&
		!strings.Contains(host, ".") &&
		host != "localhost" &&
		net.ParseIP(host) == nil
}
