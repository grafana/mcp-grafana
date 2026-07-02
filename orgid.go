package mcpgrafana

import (
	"context"
	"strconv"

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
