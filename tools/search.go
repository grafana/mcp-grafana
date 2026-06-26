package tools

import (
	"context"
	"fmt"
	"strings"

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

type dashboardSearchHitWithPath struct {
	dashboardSearchHit
	FolderPath string `json:"folderPath,omitempty"`
}

type SearchDashboardsDeepParams struct {
	Query      string   `json:"query" jsonschema:"description=Query matched case-insensitively against title\\, description\\, tags\\, folder title\\, and reconstructed folder path. Empty string returns all dashboards."`
	FolderPath string   `json:"folderPath,omitempty" jsonschema:"description=Optional. Limit results to dashboards inside this folder path (e.g. '/Ops/Production'). Includes all subfolders. Case insensitive."`
	Tags       []string `json:"tags,omitempty" jsonschema:"description=Optional. Limit results to dashboards that have all of these tags (AND logic). Case insensitive."`
}

type SearchDashboardsDeepResult struct {
	Dashboards []dashboardSearchHitWithPath `json:"dashboards"`
	Total      int                          `json:"total"`
}

func fetchAllFolders(ctx context.Context) (map[string]*models.Hit, error) {
	c := mcpgrafana.GrafanaClientFromContext(ctx)
	folderMap := make(map[string]*models.Hit)
	page := int64(1)
	limit := int64(500)
	for {
		params := search.NewSearchParamsWithContext(ctx)
		params.SetType(&folderTypeStr)
		params.SetLimit(&limit)
		params.SetPage(&page)
		resp, err := c.Search.Search(params)
		if err != nil {
			return nil, fmt.Errorf("fetch folders page %d: %w", page, err)
		}
		for _, hit := range resp.Payload {
			folderMap[hit.UID] = hit
		}
		if len(resp.Payload) == 0 {
			break
		}
		page++
	}
	return folderMap, nil
}

func fetchAllDashboards(ctx context.Context) ([]*models.Hit, error) {
	c := mcpgrafana.GrafanaClientFromContext(ctx)
	var dashboards []*models.Hit
	page := int64(1)
	limit := int64(500)
	for {
		params := search.NewSearchParamsWithContext(ctx)
		params.SetType(&dashboardTypeStr)
		params.SetLimit(&limit)
		params.SetPage(&page)
		resp, err := c.Search.Search(params)
		if err != nil {
			return nil, fmt.Errorf("fetch dashboards page %d: %w", page, err)
		}
		dashboards = append(dashboards, resp.Payload...)
		if len(resp.Payload) == 0 {
			break
		}
		page++
	}
	return dashboards, nil
}

// buildFolderPath walks up the folder tree via folderMap and returns a slash-separated path.
func buildFolderPath(folderUID string, folderMap map[string]*models.Hit) string {
	if folderUID == "" {
		return ""
	}
	var parts []string
	uid := folderUID
	for uid != "" {
		folder, ok := folderMap[uid]
		if !ok {
			break
		}
		parts = append([]string{folder.Title}, parts...)
		uid = folder.FolderUID
	}
	if len(parts) == 0 {
		return ""
	}
	return "/" + strings.Join(parts, "/")
}

func hasAllTags(dashboardTags []string, filterTags []string) bool {
	for _, ft := range filterTags {
		found := false
		for _, dt := range dashboardTags {
			if strings.ToLower(dt) == ft {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func matchesDashboardQuery(query string, hit dashboardSearchHitWithPath) bool {
	if strings.Contains(strings.ToLower(hit.Title), query) {
		return true
	}
	if strings.Contains(strings.ToLower(hit.Description), query) {
		return true
	}
	if strings.Contains(strings.ToLower(hit.FolderTitle), query) {
		return true
	}
	if strings.Contains(strings.ToLower(hit.FolderPath), query) {
		return true
	}
	for _, tag := range hit.Tags {
		if strings.Contains(strings.ToLower(tag), query) {
			return true
		}
	}
	return false
}

func searchDashboardsDeep(ctx context.Context, args SearchDashboardsDeepParams) (*SearchDashboardsDeepResult, error) {
	folderMap, err := fetchAllFolders(ctx)
	if err != nil {
		return nil, err
	}
	dashboards, err := fetchAllDashboards(ctx)
	if err != nil {
		return nil, err
	}

	query := strings.ToLower(args.Query)
	folderFilter := ""
	if args.FolderPath != "" {
		folderFilter = strings.TrimRight(strings.ToLower(args.FolderPath), "/")
	}
	tagFilter := make([]string, len(args.Tags))
	for i, t := range args.Tags {
		tagFilter[i] = strings.ToLower(t)
	}
	var results []dashboardSearchHitWithPath
	for _, d := range dashboards {
		folderPath := buildFolderPath(d.FolderUID, folderMap)
		hit := dashboardSearchHitWithPath{
			dashboardSearchHit: dashboardSearchHit{
				UID:         d.UID,
				Title:       d.Title,
				URL:         d.URL,
				Type:        string(d.Type),
				FolderUID:   d.FolderUID,
				FolderTitle: d.FolderTitle,
				Tags:        d.Tags,
				Description: d.Description,
			},
			FolderPath: folderPath,
		}
		if folderFilter != "" && !strings.HasPrefix(strings.TrimRight(strings.ToLower(folderPath), "/")+"/", folderFilter+"/") {
			continue
		}
		if !hasAllTags(hit.Tags, tagFilter) {
			continue
		}
		if query == "" || matchesDashboardQuery(query, hit) {
			results = append(results, hit)
		}
	}
	return &SearchDashboardsDeepResult{
		Dashboards: results,
		Total:      len(results),
	}, nil
}

var SearchDashboardsDeep = mcpgrafana.MustTool(
	"search_dashboards_deep",
	"Search for Grafana dashboards by a query string. Every attribute of the dashboard, tags and the folderPath will be checked if it contains the case insensitive query string. Returns a list of matching dashboards with details like title, UID, folder, tags, URL and folderPath.",
	searchDashboardsDeep,
	mcp.WithTitleAnnotation("Search dashboards deep"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
)

func AddSearchTools(mcp *server.MCPServer) {
	SearchDashboards.Register(mcp)
	SearchFolders.Register(mcp)
	SearchDashboardsDeep.Register(mcp)
}
