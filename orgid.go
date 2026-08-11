package mcpgrafana

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/invopop/jsonschema"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// DynamicMultiOrgEnabled controls per-call org selection: when true, tools
// advertise the optional orgId argument and OrgIDOverrideMiddleware is wired in.
// It is set from the --dynamic-multi-org flag before tools are registered.
// Startup-time multi-org (GRAFANA_ORG_ID / the X-Grafana-Org-Id header on the
// connection) works regardless of this flag.
var DynamicMultiOrgEnabled bool

// OrgIDArgument is the name of the optional per-call tool argument that selects
// which Grafana organization a tool call targets. When dynamic multi-org is
// enabled it is advertised on every native tool's input schema (see
// injectOrgIDProperty) and consumed by OrgIDOverrideMiddleware.
const OrgIDArgument = "orgId"

// orgIDArgumentDescription documents the orgId argument advertised on every tool.
const orgIDArgumentDescription = "Grafana org ID to target for this call, overriding the connection's default org."

// injectOrgIDProperty advertises the optional orgId argument on a tool's
// reflected property set (unless the tool already declares it), so
// OrgIDOverrideMiddleware has something for clients to populate. Keeping it here,
// beside the middleware that reads it, leaves ConvertTool free of orgId
// specifics.
func injectOrgIDProperty(properties map[string]any) {
	if _, exists := properties[OrgIDArgument]; exists {
		return
	}
	properties[OrgIDArgument] = &jsonschema.Schema{
		Type:        "integer",
		Description: orgIDArgumentDescription,
	}
}

// OrgIDOverrideMiddleware returns a tool-handler middleware that lets a single
// connection address multiple Grafana organizations. When a tool call carries
// an "orgId" argument, the middleware overrides GrafanaConfig.OrgID in the
// context for the duration of that call. Because the outgoing X-Grafana-Org-Id
// header (OrgIDRoundTripper) and the resolved app-platform namespace
// (GrafanaNamespace) both read OrgID from the context at call time, this single
// override redirects both the legacy /api/* and the /apis/* requests to the
// requested org consistently.
//
// The override can only reach organizations the underlying credential is a
// member of — Grafana still enforces authorization, and a service-account token
// remains bound to its single org. An absent, non-numeric, or non-positive
// value leaves the connection-level OrgID untouched.
//
// The orgId argument is stripped from the request before the handler runs so it
// never propagates downstream — in particular, proxied tools forward all
// arguments to upstream datasource MCP servers, which must not receive a
// Grafana-only orgId.
func OrgIDOverrideMiddleware(next server.ToolHandlerFunc) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if args := request.GetArguments(); args != nil {
			if _, present := args[OrgIDArgument]; present {
				if orgID, ok := orgIDFromArguments(args); ok {
					if cfg := GrafanaConfigFromContext(ctx); cfg.OrgID != orgID {
						cfg.OrgID = orgID
						ctx = WithGrafanaConfig(ctx, cfg)
					}
				}
				// Strip it regardless of validity so it never reaches a handler
				// (GetArguments returns the live map, so deletion propagates).
				delete(args, OrgIDArgument)
			}
		}
		return next(ctx, request)
	}
}

// orgIDFromArguments extracts a positive orgId from raw tool-call arguments,
// tolerating both JSON numbers and numeric strings (some clients send integer
// arguments as strings). It returns ok=false when the argument is absent,
// unparseable, or not positive.
func orgIDFromArguments(args map[string]any) (int64, bool) {
	raw, present := args[OrgIDArgument]
	if !present {
		return 0, false
	}

	var orgID int64
	switch v := raw.(type) {
	case float64:
		orgID = int64(v)
	case int64:
		orgID = v
	case int:
		orgID = int64(v)
	case string:
		parsed, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return 0, false
		}
		orgID = parsed
	default:
		return 0, false
	}

	if orgID <= 0 {
		return 0, false
	}
	return orgID, true
}

// OrgInfo describes an organization the current user is a member of.
type OrgInfo struct {
	OrgID int64  `json:"orgId"`
	Name  string `json:"name"`
	Role  string `json:"role"`
}

// ListUserOrgs returns the organizations the current user belongs to
// (GET /api/user/orgs) — the set of values OrgIDArgument can usefully name. It
// returns an error for identities that cannot enumerate orgs (e.g.
// service-account tokens, which are single-org).
func ListUserOrgs(ctx context.Context) ([]OrgInfo, error) {
	cfg := GrafanaConfigFromContext(ctx)
	var orgs []OrgInfo
	if err := grafanaGetJSON(ctx, &cfg, "/api/user/orgs", &orgs); err != nil {
		return nil, err
	}
	return orgs, nil
}

// resolveDefaultOrgID returns the org the connection targets when no orgId is
// given: the current org reported by /api/org, falling back to the configured
// OrgID (0 when unset, which Grafana treats as the identity's active org).
func resolveDefaultOrgID(ctx context.Context, logger *slog.Logger) int64 {
	cfg := GrafanaConfigFromContext(ctx)
	var org struct {
		ID int64 `json:"id"`
	}
	if err := grafanaGetJSON(ctx, &cfg, "/api/org", &org); err != nil || org.ID == 0 {
		logger.DebugContext(ctx, "could not resolve default org from /api/org; using configured OrgID", "orgID", cfg.OrgID, "error", err)
		return cfg.OrgID
	}
	return org.ID
}

// UserInfo describes the signed-in identity for the current request.
type UserInfo struct {
	Login          string    `json:"login,omitempty"`
	Email          string    `json:"email,omitempty"`
	Name           string    `json:"name,omitempty"`
	IsGrafanaAdmin bool      `json:"isGrafanaAdmin"`
	CurrentOrgID   int64     `json:"currentOrgId"`
	Orgs           []OrgInfo `json:"orgs"`
}

// CurrentUserInfo returns the signed-in user's identity (GET /api/user) plus the
// organizations the credential can access (GET /api/user/orgs). Org membership
// is best-effort: it is empty for identities that can't enumerate orgs (e.g.
// service-account tokens), which remain scoped to their single CurrentOrgID.
func CurrentUserInfo(ctx context.Context) (UserInfo, error) {
	cfg := GrafanaConfigFromContext(ctx)
	var u struct {
		Login          string `json:"login"`
		Email          string `json:"email"`
		Name           string `json:"name"`
		IsGrafanaAdmin bool   `json:"isGrafanaAdmin"`
		OrgID          int64  `json:"orgId"`
	}
	if err := grafanaGetJSON(ctx, &cfg, "/api/user", &u); err != nil {
		return UserInfo{}, err
	}
	info := UserInfo{
		Login:          u.Login,
		Email:          u.Email,
		Name:           u.Name,
		IsGrafanaAdmin: u.IsGrafanaAdmin,
		CurrentOrgID:   u.OrgID,
	}
	if orgs, err := ListUserOrgs(ctx); err == nil {
		info.Orgs = orgs
	}
	return info, nil
}

// grafanaGetJSON performs an authenticated GET against the Grafana API and
// decodes a JSON response, using the same transport chain as the rest of the
// server (auth, OrgID header, TLS, etc.).
func grafanaGetJSON(ctx context.Context, cfg *GrafanaConfig, path string, out any) error {
	transport, err := BuildTransport(cfg, nil)
	if err != nil {
		return fmt.Errorf("build transport: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(cfg.URL, "/")+path, nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := (&http.Client{Transport: transport, Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return fmt.Errorf("do request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: HTTP %d", path, resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}
