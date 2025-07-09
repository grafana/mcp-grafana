package tools

import (
	"context"
	"fmt"

	"github.com/mark3labs/mcp-go/server"

	"github.com/grafana/grafana-openapi-client-go/client/teams"
	"github.com/grafana/grafana-openapi-client-go/models"
	mcpgrafana "github.com/grafana/mcp-grafana"
)

type ListTeamsParams struct {
	Query string `json:"query" jsonschema:"description=The query to search for teams. Can be left empty to fetch all teams"`
}

func listTeams(ctx context.Context, args ListTeamsParams) (*models.SearchTeamQueryResult, error) {
	c := mcpgrafana.GrafanaClientFromContext(ctx)
	params := teams.NewSearchTeamsParamsWithContext(ctx)
	if args.Query != "" {
		params.SetQuery(&args.Query)
	}
	search, err := c.Teams.SearchTeams(params)
	if err != nil {
		return nil, fmt.Errorf("search teams for %+v: %w", c, err)
	}
	return search.Payload, nil
}

var ListTeams = mcpgrafana.MustTool(
	"list_teams",
	"Search for Grafana teams by a query string. Returns a list of matching teams with details like name, ID, and URL.",
	listTeams,
)

type ListUsersByOrgParams struct{}

func listUsersByOrg(ctx context.Context, args ListUsersByOrgParams) ([]*models.OrgUserDTO, error) {
	c := mcpgrafana.GrafanaClientFromContext(ctx)

	search, err := c.Org.GetOrgUsersForCurrentOrg()
	if err != nil {
		return nil, fmt.Errorf("search users: %w", err)
	}
	return search.Payload, nil
}

var ListUsersByOrg = mcpgrafana.MustTool(
	"list_users_by_org",
	"List users by organization. Returns a list of users with details like userid, email, role etc.",
	listUsersByOrg,
)

func AddAdminTools(mcp *server.MCPServer) {
	ListTeams.Register(mcp)
	ListUsersByOrg.Register(mcp)
}
