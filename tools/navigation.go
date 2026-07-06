package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	mcpgrafana "github.com/grafana/mcp-grafana"
)

type GenerateDeeplinkParams struct {
	ResourceType        string                       `json:"resourceType" jsonschema:"required,description=Type of resource: dashboard\\, panel\\, or explore"`
	DashboardUID        *string                      `json:"dashboardUid,omitempty" jsonschema:"description=Dashboard UID (for stored dashboards). Mutually exclusive with provisioningPreview for dashboard and panel types."`
	ProvisioningPreview *DeeplinkProvisioningPreview `json:"provisioningPreview,omitempty" jsonschema:"description=Identifies a dashboard staged on a provisioning repository branch (e.g. a git-sync PR preview). Mutually exclusive with dashboardUid for dashboard and panel types."`
	DatasourceUID       *string                      `json:"datasourceUid,omitempty" jsonschema:"description=Datasource UID (required for explore type)"`
	PanelID             *int                         `json:"panelId,omitempty" jsonschema:"description=Panel ID (required for panel type)"`
	Queries             []map[string]interface{}     `json:"queries,omitempty" jsonschema:"description=List of query objects for explore links (e.g. [{\"refId\":\"A\"\\,\"expr\":\"up\"}])"`
	QueryParams         map[string]string            `json:"queryParams,omitempty" jsonschema:"description=Additional URL query parameters (for dashboard/panel types)"`
	TimeRange           *TimeRange                   `json:"timeRange,omitempty" jsonschema:"description=Time range for the link"`
	Shorten             bool                         `json:"shorten,omitempty" jsonschema:"description=If true\\, try to shorten the generated URL to /goto/<uid>. If shortening fails\\, return the original deeplink."`
}

// DeeplinkProvisioningPreview identifies a not-yet-applied dashboard inside a
// provisioning repository. The generated URL opens Grafana's preview view for
// that file; pullRequestUrl is read by the preview banner to surface the
// upstream PR.
type DeeplinkProvisioningPreview struct {
	Repo           string `json:"repo" jsonschema:"required,description=Provisioning repository slug. List repositories via list_provisioning_repositories if unknown."`
	Path           string `json:"path" jsonschema:"required,description=Path to the dashboard file within the repository\\, relative to its root."`
	Ref            string `json:"ref,omitempty" jsonschema:"description=Branch or commit SHA. Defaults to the repository's main branch when omitted."`
	PullRequestURL string `json:"pullRequestUrl,omitempty" jsonschema:"description=Upstream pull request URL to surface in Grafana's preview banner."`
}

type TimeRange struct {
	From string `json:"from" jsonschema:"description=Start time (e.g.\\, 'now-1h')"`
	To   string `json:"to" jsonschema:"description=End time (e.g.\\, 'now')"`
}

func grafanaBaseURLFromContext(ctx context.Context) (string, error) {
	// Prefer the public URL from the Grafana client (fetched from /api/frontend/settings),
	// falling back to the configured URL if the client is not available or has no public URL.
	var baseURL string
	if gc := mcpgrafana.GrafanaClientFromContext(ctx); gc != nil && gc.PublicURL != "" {
		baseURL = gc.PublicURL
	} else {
		config := mcpgrafana.GrafanaConfigFromContext(ctx)
		baseURL = config.URL
	}

	if baseURL == "" {
		return "", fmt.Errorf("grafana url not configured. Please set GRAFANA_URL environment variable or X-Grafana-URL header")
	}

	// Validate baseURL separately from the inbound X-Grafana-URL middleware:
	// gc.PublicURL is populated by fetchPublicURL from Grafana's
	// /api/frontend/settings appUrl response, which is not covered by the
	// middleware at the HTTP transport boundary. A misconfigured Grafana can
	// therefore return a malformed appUrl that flows into deeplink construction
	// (e.g. http://%gg/d/<uid>) unless checked here.
	if err := mcpgrafana.ValidateGrafanaURL(baseURL); err != nil {
		return "", fmt.Errorf("grafana url is invalid: %w. Please set GRAFANA_URL environment variable or X-Grafana-URL header", err)
	}
	return baseURL, nil
}

func generateDeeplinkWithMode(ctx context.Context, args GenerateDeeplinkParams, allowShorten bool) (string, error) {
	baseURL, err := grafanaBaseURLFromContext(ctx)
	if err != nil {
		return "", err
	}

	var deeplink string

	switch strings.ToLower(args.ResourceType) {
	case "dashboard":
		target, err := buildDashboardTargetURL(baseURL, args.DashboardUID, args.ProvisioningPreview)
		if err != nil {
			return "", err
		}
		deeplink = target

	case "panel":
		target, err := buildDashboardTargetURL(baseURL, args.DashboardUID, args.ProvisioningPreview)
		if err != nil {
			return "", err
		}
		if args.PanelID == nil {
			return "", fmt.Errorf("panelId is required for panel links")
		}
		separator := "?"
		if strings.Contains(target, "?") {
			separator = "&"
		}
		deeplink = fmt.Sprintf("%s%sviewPanel=%d", target, separator, *args.PanelID)

	case "explore":
		if args.DatasourceUID == nil {
			return "", fmt.Errorf("datasourceUid is required for explore links")
		}

		// Build the full explore state inside `left` — Grafana Explore reads
		// datasource, queries, and range all from this single JSON object.
		exploreState := map[string]interface{}{
			"datasource": *args.DatasourceUID,
		}
		if len(args.Queries) > 0 {
			exploreState["queries"] = args.Queries
		}
		if args.TimeRange != nil {
			rangeObj := map[string]string{}
			if args.TimeRange.From != "" {
				rangeObj["from"] = toGrafanaTimeParam(args.TimeRange.From)
			}
			if args.TimeRange.To != "" {
				rangeObj["to"] = toGrafanaTimeParam(args.TimeRange.To)
			}
			if len(rangeObj) > 0 {
				exploreState["range"] = rangeObj
			}
		}

		leftJSON, err := json.Marshal(exploreState)
		if err != nil {
			return "", fmt.Errorf("failed to marshal explore state: %w", err)
		}

		params := url.Values{}
		params.Set("left", string(leftJSON))
		deeplink = fmt.Sprintf("%s/explore?%s", baseURL, params.Encode())

		// For explore, time range is already embedded in `left` — skip the
		// generic time range block below by clearing it.
		args.TimeRange = nil

	default:
		return "", fmt.Errorf("unsupported resource type: %s. Supported types are: dashboard, panel, explore", args.ResourceType)
	}

	if args.TimeRange != nil {
		separator := "?"
		if strings.Contains(deeplink, "?") {
			separator = "&"
		}
		timeParams := url.Values{}
		if args.TimeRange.From != "" {
			timeParams.Set("from", toGrafanaTimeParam(args.TimeRange.From))
		}
		if args.TimeRange.To != "" {
			timeParams.Set("to", toGrafanaTimeParam(args.TimeRange.To))
		}
		if len(timeParams) > 0 {
			deeplink = fmt.Sprintf("%s%s%s", deeplink, separator, timeParams.Encode())
		}
	}

	if len(args.QueryParams) > 0 {
		separator := "?"
		if strings.Contains(deeplink, "?") {
			separator = "&"
		}
		additionalParams := url.Values{}
		for key, value := range args.QueryParams {
			additionalParams.Set(key, value)
		}
		deeplink = fmt.Sprintf("%s%s%s", deeplink, separator, additionalParams.Encode())
	}

	if !args.Shorten {
		return deeplink, nil
	}

	if !allowShorten {
		mcpgrafana.LoggerFromContext(ctx).DebugContext(ctx,
			"generate_deeplink shorten requested while write tools are disabled; returning full URL")
		return deeplink, nil
	}

	shortURL, err := shortenURL(ctx, deeplink)
	if err != nil {
		// Compatibility-first behavior: never fail deeplink generation when
		// short-url creation is unavailable; return the long URL instead.
		mcpgrafana.LoggerFromContext(ctx).WarnContext(ctx,
			"failed to shorten generated deeplink; returning full URL",
			"error", err)
		return deeplink, nil
	}

	return shortURL, nil
}

func generateDeeplink(ctx context.Context, args GenerateDeeplinkParams) (string, error) {
	return generateDeeplinkWithMode(ctx, args, true)
}

func generateDeeplinkReadOnly(ctx context.Context, args GenerateDeeplinkParams) (string, error) {
	return generateDeeplinkWithMode(ctx, args, false)
}

func shortenURL(ctx context.Context, longURL string) (string, error) {
	publicBaseURL, err := grafanaBaseURLFromContext(ctx)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(longURL) == "" {
		return "", fmt.Errorf("url is required")
	}

	parsedURL, err := url.Parse(longURL)
	if err != nil {
		return "", fmt.Errorf("invalid url: %w", err)
	}
	path := parsedURL.RequestURI()
	if !strings.HasPrefix(path, "/") {
		return "", fmt.Errorf("url must include an absolute path")
	}

	// /api/short-urls rejects absolute paths, so strip the leading slash.
	relativePath := strings.TrimPrefix(path, "/")

	payload, err := json.Marshal(map[string]string{"path": relativePath})
	if err != nil {
		return "", fmt.Errorf("marshal short-url payload: %w", err)
	}

	cfg := mcpgrafana.GrafanaConfigFromContext(ctx)
	apiBaseURL := strings.TrimRight(cfg.URL, "/")
	if apiBaseURL == "" {
		apiBaseURL = publicBaseURL
	}
	if err := mcpgrafana.ValidateGrafanaURL(apiBaseURL); err != nil {
		return "", fmt.Errorf("grafana api url is invalid: %w", err)
	}

	transport, err := mcpgrafana.BuildTransport(&cfg, nil)
	if err != nil {
		return "", fmt.Errorf("build transport: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiBaseURL+"/api/short-urls", bytes.NewReader(payload))
	if err != nil {
		return "", fmt.Errorf("create short-url request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := (&http.Client{Transport: transport}).Do(req)
	if err != nil {
		return "", fmt.Errorf("create short url: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := readResponseBody(resp.Body, defaultResponseLimitBytes)
	if err != nil {
		return "", fmt.Errorf("read short-url response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("create short url failed with status %d: %s", resp.StatusCode, string(body))
	}

	var shortResp struct {
		URL string `json:"url"`
		UID string `json:"uid"`
	}
	if err := json.Unmarshal(body, &shortResp); err != nil {
		return "", fmt.Errorf("decode short-url response: %w", err)
	}
	if shortResp.URL == "" {
		return "", fmt.Errorf("short-url response missing url field")
	}

	normalizedURL, err := normalizeShortURLWithPublicBase(shortResp.URL, publicBaseURL)
	if err != nil {
		return "", err
	}
	return normalizedURL, nil
}

func normalizeShortURLWithPublicBase(rawShortURL, publicBaseURL string) (string, error) {
	publicBase, err := url.Parse(publicBaseURL)
	if err != nil {
		return "", fmt.Errorf("invalid public base url: %w", err)
	}

	shortURL, err := url.Parse(rawShortURL)
	if err != nil {
		return "", fmt.Errorf("short-url response contains invalid url: %w", err)
	}

	// Keep path/query/fragment from Grafana response but always use public
	// scheme/host so shortened links match deeplink host behavior.
	if shortURL.IsAbs() {
		shortURL.Scheme = publicBase.Scheme
		shortURL.Host = publicBase.Host
		return shortURL.String(), nil
	}

	if strings.HasPrefix(rawShortURL, "/") {
		return strings.TrimRight(publicBaseURL, "/") + rawShortURL, nil
	}

	return publicBase.ResolveReference(shortURL).String(), nil
}

var GenerateDeeplink = mcpgrafana.MustTool(
	"generate_deeplink",
	"Generate deeplink URLs for Grafana resources. Supports dashboards (requires dashboardUid or provisioningPreview), panels (requires dashboardUid or provisioningPreview, plus panelId), and Explore queries (requires datasourceUid and optionally queries). For dashboard and panel links, provisioningPreview points at a dashboard staged on a provisioning repository branch (e.g. a git-sync PR preview). For explore links, the time range and queries are embedded inside the Grafana explore state. Set shorten=true to also attempt a /goto/<uid> short URL; if shortening fails, the full deeplink is returned.",
	generateDeeplink,
	mcp.WithTitleAnnotation("Generate navigation deeplink"),
	mcp.WithIdempotentHintAnnotation(false),
)

var GenerateDeeplinkReadOnly = mcpgrafana.MustTool(
	"generate_deeplink",
	"Generate deeplink URLs for Grafana resources. Supports dashboards (requires dashboardUid or provisioningPreview), panels (requires dashboardUid or provisioningPreview, plus panelId), and Explore queries (requires datasourceUid and optionally queries). For dashboard and panel links, provisioningPreview points at a dashboard staged on a provisioning repository branch (e.g. a git-sync PR preview). In read-only mode, shorten=true is accepted but ignored and the full deeplink is returned.",
	generateDeeplinkReadOnly,
	mcp.WithTitleAnnotation("Generate navigation deeplink"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
)

func AddNavigationTools(mcp *server.MCPServer, enableWriteTools bool) {
	if enableWriteTools {
		GenerateDeeplink.Register(mcp)
		return
	}
	GenerateDeeplinkReadOnly.Register(mcp)
}

// buildDashboardTargetURL returns the dashboard or provisioning-preview URL
// (without time range or extra query params) for the dashboard and panel
// resource types. Exactly one of dashboardUid or preview must be supplied.
func buildDashboardTargetURL(baseURL string, dashboardUID *string, preview *DeeplinkProvisioningPreview) (string, error) {
	// Treat an empty dashboardUid the same as unset, so a non-nil but blank
	// value doesn't produce a "/d/" URL with no UID.
	hasUID := dashboardUID != nil && *dashboardUID != ""
	switch {
	case hasUID && preview != nil:
		return "", fmt.Errorf("dashboardUid and provisioningPreview are mutually exclusive; pass exactly one")
	case !hasUID && preview == nil:
		return "", fmt.Errorf("either dashboardUid or provisioningPreview must be set")
	case hasUID:
		return fmt.Sprintf("%s/d/%s", baseURL, *dashboardUID), nil
	}

	// url.PathEscape doesn't touch `..` (dots are valid path chars), so the
	// generated link could point at an unintended Grafana page without these
	// explicit checks. Shared helpers also used by rendering and the
	// provisioning file tool.
	if err := validateRepoSlug("provisioningPreview.repo", preview.Repo); err != nil {
		return "", err
	}
	if err := validateRepoPath("provisioningPreview.path", preview.Path); err != nil {
		return "", err
	}

	escapedPath := (&url.URL{Path: strings.TrimLeft(preview.Path, "/")}).EscapedPath()
	target := fmt.Sprintf("%s/dashboard/provisioning/%s/preview/%s",
		baseURL, url.PathEscape(preview.Repo), escapedPath)

	q := url.Values{}
	if preview.Ref != "" {
		q.Set("ref", preview.Ref)
	}
	if preview.PullRequestURL != "" {
		q.Set("pull_request_url", preview.PullRequestURL)
	}
	if len(q) > 0 {
		target += "?" + q.Encode()
	}
	return target, nil
}

// toGrafanaTimeParam converts a time value to a format Grafana understands
// in URL query parameters. Grafana's Scenes parseUrlParam uses hardcoded
// string length checks and only recognizes ISO 8601 at exactly 24 chars
// (with milliseconds, e.g. "2026-04-28T12:45:00.000Z"). Shorter ISO 8601
// strings like "2026-04-28T12:45:00Z" (20 chars) are silently ignored.
// This function converts RFC 3339 timestamps to epoch milliseconds, which
// is universally supported. Relative strings and epoch values pass through.
func toGrafanaTimeParam(value string) string {
	if _, err := strconv.ParseInt(value, 10, 64); err == nil {
		return value
	}
	if strings.HasPrefix(value, "now") {
		return value
	}
	if t, err := time.Parse(time.RFC3339, value); err == nil {
		return strconv.FormatInt(t.UnixMilli(), 10)
	}
	if t, err := time.Parse(time.RFC3339Nano, value); err == nil {
		return strconv.FormatInt(t.UnixMilli(), 10)
	}
	return value
}
