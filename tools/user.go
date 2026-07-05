package tools

import (
	"context"
	"fmt"

	mcpgrafana "github.com/grafana/mcp-grafana"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// UserInfoParams takes no arguments.
type UserInfoParams struct{}

// userInfoResult is the user_info tool output: the signed-in identity and the
// organizations it can access, plus a usage note explaining how to target a
// specific org given the server's configuration.
type userInfoResult struct {
	mcpgrafana.UserInfo
	Usage string `json:"usage,omitempty"`
}

func getUserInfo(ctx context.Context, _ UserInfoParams) (*userInfoResult, error) {
	info, err := mcpgrafana.CurrentUserInfo(ctx)
	if err != nil {
		return nil, fmt.Errorf("get user info: %w", err)
	}
	result := &userInfoResult{UserInfo: info}
	// Only explain org switching when there's actually more than one org to
	// switch between; with a single (default) org the note is just noise.
	if len(info.Orgs) > 1 {
		result.Usage = multiOrgUsage(info.CurrentOrgID)
	}
	return result, nil
}

// multiOrgUsage explains how to direct calls at a particular org, depending on
// whether per-call org selection is enabled.
func multiOrgUsage(currentOrgID int64) string {
	if mcpgrafana.DynamicMultiOrgEnabled {
		return fmt.Sprintf("Per-call organization selection is enabled: pass orgId=<id> (one of the ids in 'orgs') to any tool to target that organization for a single call. Calls without orgId use org %d.", currentOrgID)
	}
	return fmt.Sprintf("This connection targets org %d for every call. To work in a different organization, either set GRAFANA_ORG_ID=<id> (or send the X-Grafana-Org-Id header) for that org and reconnect, or start mcp-grafana with --dynamic-multi-org to select an org per call via an orgId argument.", currentOrgID)
}

var UserInfoTool = mcpgrafana.MustTool(
	"user_info",
	"Get information about the current Grafana identity: login, email, name, whether it is a Grafana (server) admin, the current organization, and the organizations it can access (with roles). Call this to discover which orgId values are valid before targeting a specific organization, and to understand the identity's capabilities.",
	getUserInfo,
	mcp.WithTitleAnnotation("Get current user info"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
)

func AddUserTools(s *server.MCPServer) {
	UserInfoTool.Register(s)
}
