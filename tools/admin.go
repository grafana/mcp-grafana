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

func AddAdminTools(mcp *server.MCPServer) {
	ListTeams.Register(mcp)
}
