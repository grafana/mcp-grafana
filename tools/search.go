package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/grafana/grafana-openapi-client-go/client/search"
	mcpgrafana "github.com/grafana/mcp-grafana"
)

type SearchDashboardsParams struct {
}

func SearchDashboardsHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	c := mcpgrafana.GrafanaClientFromContext(ctx)
	params := search.NewSearchParamsWithContext(ctx)
	if q, ok := request.Params.Arguments["query"]; ok {
		if q, ok := q.(string); ok {
			params.SetQuery(&q)
		}
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

var SearchDashboardsTool = mcp.NewTool("search_dashboards",
	mcp.WithDescription("Search for dashboards"),
	mcp.WithString("query",
		mcp.Description("Query string"),
		mcp.Required(),
	),
)
