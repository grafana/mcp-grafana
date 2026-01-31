package tools

import (
	"context"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/grafana/grafana-openapi-client-go/client/search"
	"github.com/grafana/grafana-openapi-client-go/models"
	mcpgrafana "github.com/grafana/mcp-grafana"
)

var dashboardTypeStr = "dash-db"
var folderTypeStr = "dash-folder"

const (
	// DefaultSearchLimit is the default number of results to return
	DefaultSearchLimit = 100
)

type SearchDashboardsParams struct {
	Query string `json:"query,omitempty" jsonschema:"description=Search query to filter dashboards by name"`
	Limit int    `json:"limit,omitempty" jsonschema:"description=Maximum number of results to return (default: 100\\, max: 5000)"`
	Page  int    `json:"page,omitempty" jsonschema:"description=Page number for pagination (1-indexed). Default: 1"`
}

func searchDashboards(ctx context.Context, args SearchDashboardsParams) (models.HitList, error) {
	c := mcpgrafana.GrafanaClientFromContext(ctx)
	params := search.NewSearchParamsWithContext(ctx)
	if args.Query != "" {
		params.SetQuery(&args.Query)
	}
	params.SetType(&dashboardTypeStr)

	// Apply pagination
	limit := int64(args.Limit)
	if limit <= 0 {
		limit = DefaultSearchLimit
	}
	params.SetLimit(&limit)

	if args.Page > 0 {
		page := int64(args.Page)
		params.SetPage(&page)
	}

	searchResult, err := c.Search.Search(params)
	if err != nil {
		return nil, fmt.Errorf("search dashboards for %+v: %w", c, err)
	}
	return searchResult.Payload, nil
}

var SearchDashboards = mcpgrafana.MustTool(
	"search_dashboards",
	"Search and list Grafana dashboards. Returns dashboard names, UIDs, and URLs. Supports pagination with limit and page parameters.",
	searchDashboards,
	mcp.WithTitleAnnotation("Search dashboards"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
)

type SearchFoldersParams struct {
	Query string `json:"query" jsonschema:"description=The query to search for"`
}

func searchFolders(ctx context.Context, args SearchFoldersParams) (models.HitList, error) {
	c := mcpgrafana.GrafanaClientFromContext(ctx)
	params := search.NewSearchParamsWithContext(ctx)
	if args.Query != "" {
		params.SetQuery(&args.Query)
	}
	params.SetType(&folderTypeStr)
	search, err := c.Search.Search(params)
	if err != nil {
		return nil, fmt.Errorf("search folders for %+v: %w", c, err)
	}
	return search.Payload, nil
}

var SearchFolders = mcpgrafana.MustTool(
	"search_folders",
	"Search for Grafana folders by a query string. Returns matching folders with details like title, UID, and URL.",
	searchFolders,
	mcp.WithTitleAnnotation("Search folders"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
)

func AddSearchTools(mcp *server.MCPServer) {
	SearchDashboards.Register(mcp)
	SearchFolders.Register(mcp)
}
