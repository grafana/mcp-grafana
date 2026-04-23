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

type SearchDashboardsParams struct {
	Query string `json:"query" jsonschema:"description=The query to search for"`
	Limit int    `json:"limit,omitempty" jsonschema:"default=50,description=Maximum number of results to return (max 100)"`
	Page  int    `json:"page,omitempty" jsonschema:"default=1,description=Page number for pagination (1-indexed)"`
}

type dashboardSearchHit struct {
	UID         string   `json:"uid"`
	Title       string   `json:"title"`
	URL         string   `json:"url"`
	Type        string   `json:"type,omitempty"`
	FolderUID   string   `json:"folderUid,omitempty"`
	FolderTitle string   `json:"folderTitle,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	Description string   `json:"description,omitempty"`
}

type SearchDashboardsResult struct {
	Dashboards []dashboardSearchHit `json:"dashboards"`
	Total      int                  `json:"total"`   // Total count (if available)
	HasMore    bool                 `json:"hasMore"` // Whether more results exist
}

func summarizeHitList(hits models.HitList) []dashboardSearchHit {
	result := make([]dashboardSearchHit, 0, len(hits))
	for _, h := range hits {
		hit := dashboardSearchHit{
			UID:         h.UID,
			Title:       h.Title,
			URL:         h.URL,
			Type:        string(h.Type),
			FolderUID:   h.FolderUID,
			FolderTitle: h.FolderTitle,
			Description: h.Description,
		}
		if len(h.Tags) > 0 {
			hit.Tags = h.Tags
		}
		result = append(result, hit)
	}
	return result
}

func searchDashboards(ctx context.Context, args SearchDashboardsParams) (*SearchDashboardsResult, error) {
	c := mcpgrafana.GrafanaClientFromContext(ctx)
	params := search.NewSearchParamsWithContext(ctx)
	if args.Query != "" {
		params.SetQuery(&args.Query)
		params.SetType(&dashboardTypeStr)
	}

	// Apply default limit if not specified
	limit := int64(args.Limit)
	if limit <= 0 {
		limit = 50
	}
	// Cap at maximum
	if limit > 100 {
		limit = 100
	}
	params.SetLimit(&limit)

	// Apply page (1-indexed, default to 1)
	page := int64(args.Page)
	if page <= 0 {
		page = 1
	}
	params.SetPage(&page)

	searchResp, err := c.Search.Search(params)
	if err != nil {
		return nil, fmt.Errorf("search dashboards for %+v: %w", c, err)
	}

	// Determine if there are more results
	// If we got exactly limit results, there may be more
	hasMore := len(searchResp.Payload) == int(limit)

	return &SearchDashboardsResult{
		Dashboards: summarizeHitList(searchResp.Payload),
		Total:      len(searchResp.Payload), // Grafana doesn't return total count
		HasMore:    hasMore,
	}, nil
}

var SearchDashboards = mcpgrafana.MustTool(
	"search_dashboards",
	"Search for Grafana dashboards by a query string. Returns a list of matching dashboards with details like title, UID, folder, tags, and URL.",
	searchDashboards,
	mcp.WithTitleAnnotation("Search dashboards"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
)

type SearchFoldersParams struct {
	Query string `json:"query" jsonschema:"description=The query to search for"`
}

func searchFolders(ctx context.Context, args SearchFoldersParams) (*SearchDashboardsResult, error) {
	c := mcpgrafana.GrafanaClientFromContext(ctx)
	params := search.NewSearchParamsWithContext(ctx)
	if args.Query != "" {
		params.SetQuery(&args.Query)
	}
	params.SetType(&folderTypeStr)
	searchResp, err := c.Search.Search(params)
	if err != nil {
		return nil, fmt.Errorf("search folders for %+v: %w", c, err)
	}
	return &SearchDashboardsResult{
		Dashboards: summarizeHitList(searchResp.Payload),
		Total:      len(searchResp.Payload),
	}, nil
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
