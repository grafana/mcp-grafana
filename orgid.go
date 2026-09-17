package mcpgrafana

import (
	"context"
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/invopop/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
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

const orgIDArgumentDescription = "Grafana org ID to target for this call, overriding the connection's default org."

func injectOrgIDProperty(properties map[string]any) {
	if _, exists := properties[OrgIDArgument]; exists {
		return
	}
	properties[OrgIDArgument] = &jsonschema.Schema{
		Type:        "integer",
		Description: orgIDArgumentDescription,
	}
}

// OrgIDOverrideMiddleware returns an mcp.Middleware that lets a single
// connection address multiple Grafana organizations. When a tools/call request
// carries an "orgId" argument, the middleware overrides GrafanaConfig.OrgID in
// the context for the duration of that call. The orgId argument is stripped from
// the request before the handler runs so it never propagates downstream.
func OrgIDOverrideMiddleware() mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			if method != "tools/call" {
				return next(ctx, method, req)
			}
			callReq, ok := req.(*mcp.CallToolRequest)
			if !ok || callReq == nil || callReq.Params == nil || len(callReq.Params.Arguments) == 0 {
				return next(ctx, method, req)
			}

			if !bytes.Contains(callReq.Params.Arguments, []byte(`"orgId"`)) {
				return next(ctx, method, req)
			}

			var args map[string]any
			if err := json.Unmarshal(callReq.Params.Arguments, &args); err != nil {
				return next(ctx, method, req)
			}

			if _, present := args[OrgIDArgument]; present {
				if orgID, ok := orgIDFromArguments(args); ok {
					if cfg := GrafanaConfigFromContext(ctx); cfg.OrgID != orgID {
						cfg.OrgID = orgID
						ctx = WithGrafanaConfig(ctx, cfg)
					}
				}
				delete(args, OrgIDArgument)
				if newArgs, err := json.Marshal(args); err == nil {
					callReq.Params.Arguments = newArgs
				}
			}

			return next(ctx, method, req)
		}
	}
}

// orgIDFromArguments extracts a positive, whole orgId from raw tool-call
// arguments, tolerating both JSON numbers and numeric strings.
func orgIDFromArguments(args map[string]any) (int64, bool) {
	raw, present := args[OrgIDArgument]
	if !present {
		return 0, false
	}

	var orgID int64
	switch v := raw.(type) {
	case float64:
		if v != math.Trunc(v) || math.IsInf(v, 0) || v >= math.MaxInt64 {
			return 0, false
		}
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

// ListUserOrgs returns the organizations the current user belongs to.
func ListUserOrgs(ctx context.Context) ([]OrgInfo, error) {
	cfg := GrafanaConfigFromContext(ctx)
	var orgs []OrgInfo
	if err := grafanaGetJSON(ctx, &cfg, "/api/user/orgs", &orgs); err != nil {
		return nil, err
	}
	return orgs, nil
}

// UserPersistedOrgID returns the org stored on the signed-in user's record.
func UserPersistedOrgID(ctx context.Context) (int64, error) {
	cfg := GrafanaConfigFromContext(ctx)
	var u struct {
		OrgID int64 `json:"orgId"`
	}
	if err := grafanaGetJSON(ctx, &cfg, "/api/user", &u); err != nil {
		return 0, err
	}
	return u.OrgID, nil
}

func resolveConnectionOrgID(ctx context.Context, logger *slog.Logger) int64 {
	cfg := GrafanaConfigFromContext(ctx)
	var org struct {
		ID int64 `json:"id"`
	}
	if err := grafanaGetJSON(ctx, &cfg, "/api/org", &org); err != nil || org.ID == 0 {
		logger.DebugContext(ctx, "could not resolve connection org from /api/org; using configured OrgID", "orgID", cfg.OrgID, "error", err)
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
	currentOrg := resolveConnectionOrgID(ctx, cfg.LoggerOrDefault())
	if currentOrg <= 0 {
		currentOrg = u.OrgID
	}
	info := UserInfo{
		Login:          u.Login,
		Email:          u.Email,
		Name:           u.Name,
		IsGrafanaAdmin: u.IsGrafanaAdmin,
		CurrentOrgID:   currentOrg,
	}
	if orgs, err := ListUserOrgs(ctx); err == nil {
		info.Orgs = orgs
	}
	return info, nil
}

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
