package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/grafana/grafana-openapi-client-go/client/search"
	mcpgrafana "github.com/grafana/mcp-grafana"
)

type SearchDashboardsParams struct {
	Query string `json:"query" jsonschema:"description=The query to search for"`
}

func searchDashboards(ctx context.Context, args SearchDashboardsParams) (*mcp.CallToolResult, error) {
	c := mcpgrafana.GrafanaClientFromContext(ctx)
	params := search.NewSearchParamsWithContext(ctx)
	if args.Query != "" {
		params.SetQuery(&args.Query)
	}
	search, err := c.Search.Search(params)
	if err != nil {
		return nil, fmt.Errorf("search dashboards for %+v: %w", c, err)
	}
	b, err := json.Marshal(search.Payload)
	if err != nil {
		return nil, fmt.Errorf("marshal search results: %w", err)
	}
	return mcp.NewToolResultText(string(b)), nil
}

var SearchDashboards = mcpgrafana.MustTool(
	"search_dashboards",
	"Search for dashboards",
	searchDashboards,
)

func AddSearchTools(mcp *server.MCPServer) {
	SearchDashboards.Register(mcp)
}
