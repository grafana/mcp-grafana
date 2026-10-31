package tools

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/go-openapi/runtime"
	"github.com/go-openapi/strfmt"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/grafana/grafana-openapi-client-go/client/search"
	"github.com/grafana/grafana-openapi-client-go/models"
	mcpgrafana "github.com/grafana/mcp-grafana/v2"
)

var dashboardTypeStr = "dash-db"
var folderTypeStr = "dash-folder"

// folderListPageSize is the page size used when listing every folder to
// build folder paths.
const folderListPageSize = 1000

type SearchDashboardsParams struct {
	Query      string   `json:"query,omitempty" jsonschema:"description=Optional query. Omit to filter by folderUid\\, folderPath\\, tag\\, or starred"`
	FolderUID  string   `json:"folderUid,omitempty" jsonschema:"description=Optional immediate folder UID (not nested)"`
	FolderPath string   `json:"folderPath,omitempty" jsonschema:"description=Optional folder path like Ops/Production. Includes subfolders. Case-insensitive"`
	Tag        []string `json:"tag,omitempty" jsonschema:"description=Optional tags to match (AND)"`
	Starred    bool     `json:"starred,omitempty" jsonschema:"description=If true\\, only starred dashboards"`
	Limit      int      `json:"limit,omitempty" jsonschema:"default=50,description=Maximum number of results to return (max 100)"`
	Page       int      `json:"page,omitempty" jsonschema:"default=1,description=Page number for pagination (1-indexed)"`
}

type dashboardSearchHit struct {
	UID         string   `json:"uid"`
	Title       string   `json:"title"`
	URL         string   `json:"url"`
	Type        string   `json:"type,omitempty"`
	FolderUID   string   `json:"folderUid,omitempty"`
	FolderTitle string   `json:"folderTitle,omitempty"`
	FolderPath  string   `json:"folderPath,omitempty"`
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

// folderNode is one folder of the folder tree, keyed by UID in folderTree.
type folderNode struct {
	title     string
	parentUID string
}

type folderTree map[string]folderNode

// listFolderTree lists every folder the caller can see, at every nesting
// level. Grafana's folder search returns each folder with its parent, so the
// request count does not depend on the depth of the tree.
func listFolderTree(ctx context.Context) (folderTree, error) {
	c := mcpgrafana.GrafanaClientFromContext(ctx)
	tree := folderTree{}
	limit := int64(folderListPageSize)
	for page := int64(1); ; page++ {
		params := search.NewSearchParamsWithContext(ctx)
		params.SetType(&folderTypeStr)
		params.SetLimit(&limit)
		params.SetPage(&page)
		resp, err := c.Search.Search(params)
		if err != nil {
			return nil, fmt.Errorf("list folders (page %d): %w", page, err)
		}
		for _, h := range resp.Payload {
			tree[h.UID] = folderNode{title: h.Title, parentUID: h.FolderUID}
		}
		if len(resp.Payload) < folderListPageSize {
			return tree, nil
		}
	}
}

// path returns the folder titles from the root down to uid, joined by "/".
// It returns "" if the chain is incomplete (a parent the caller cannot see)
// or loops, so a partial path is never mistaken for a root path.
func (t folderTree) path(uid string) string {
	var titles []string
	seen := map[string]bool{}
	for uid != "" {
		node, ok := t[uid]
		if !ok || seen[uid] {
			return ""
		}
		seen[uid] = true
		titles = append(titles, node.title)
		uid = node.parentUID
	}
	slices.Reverse(titles)
	return strings.Join(titles, "/")
}

// subtree returns the UIDs of every folder at path, and of all folders below
// them. Paths match on whole segments, case-insensitively. Several folders can
// share a path, and all of them match.
func (t folderTree) subtree(path string) []string {
	want := strings.ToLower(path)
	var uids []string
	for uid := range t {
		p := strings.ToLower(t.path(uid))
		if p != "" && (p == want || strings.HasPrefix(p, want+"/")) {
			uids = append(uids, uid)
		}
	}
	slices.Sort(uids)
	return uids
}

// withRepeatedFolderUIDs sends uids as repeated folderUIDs query parameters.
// The generated client joins several values with commas, which Grafana reads
// as a single UID that matches nothing.
func withRepeatedFolderUIDs(uids []string) search.ClientOption {
	return func(op *runtime.ClientOperation) {
		inner := op.Params
		op.Params = runtime.ClientRequestWriterFunc(func(r runtime.ClientRequest, reg strfmt.Registry) error {
			if err := inner.WriteToRequest(r, reg); err != nil {
				return err
			}
			return r.SetQueryParam("folderUIDs", uids...)
		})
	}
}

func normalizeFolderPath(path string) string {
	return strings.Trim(strings.TrimSpace(path), "/")
}

// addFolderPaths sets FolderPath on each hit. tree may be nil, in which case it
// is listed only if a hit is in a folder. Paths are best effort: if listing
// fails, the hits keep an empty FolderPath and the search still succeeds.
func addFolderPaths(ctx context.Context, hits []dashboardSearchHit, tree folderTree) {
	if tree == nil {
		if !slices.ContainsFunc(hits, func(h dashboardSearchHit) bool { return h.FolderUID != "" }) {
			return
		}
		var err error
		if tree, err = listFolderTree(ctx); err != nil {
			mcpgrafana.LoggerFromContext(ctx).WarnContext(ctx, "failed to list folders for folder paths", "error", err)
			return
		}
	}
	for i := range hits {
		hits[i].FolderPath = tree.path(hits[i].FolderUID)
	}
}

func searchDashboards(ctx context.Context, args SearchDashboardsParams) (*SearchDashboardsResult, error) {
	folderPath := normalizeFolderPath(args.FolderPath)
	if folderPath != "" && args.FolderUID != "" {
		return nil, fmt.Errorf("set either folderUid or folderPath, not both")
	}

	c := mcpgrafana.GrafanaClientFromContext(ctx)
	params := search.NewSearchParamsWithContext(ctx)
	params.SetType(&dashboardTypeStr)
	if args.Query != "" {
		params.SetQuery(&args.Query)
	}
	if args.FolderUID != "" {
		params.SetFolderUIDs([]string{args.FolderUID})
	}

	// Grafana's folderUIDs filter only matches the immediate folder, so a
	// folderPath is expanded to the folder and all its subfolders first.
	var tree folderTree
	var opts []search.ClientOption
	if folderPath != "" {
		var err error
		if tree, err = listFolderTree(ctx); err != nil {
			return nil, err
		}
		uids := tree.subtree(folderPath)
		if len(uids) == 0 {
			return &SearchDashboardsResult{Dashboards: []dashboardSearchHit{}}, nil
		}
		opts = append(opts, withRepeatedFolderUIDs(uids))
	}
	if len(args.Tag) > 0 {
		params.SetTag(args.Tag)
	}
	if args.Starred {
		starred := true
		params.SetStarred(&starred)
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

	searchResp, err := c.Search.Search(params, opts...)
	if err != nil {
		return nil, fmt.Errorf("search dashboards for %+v: %w", c, err)
	}

	// Determine if there are more results
	// If we got exactly limit results, there may be more
	hasMore := len(searchResp.Payload) == int(limit)

	hits := summarizeHitList(searchResp.Payload)
	addFolderPaths(ctx, hits, tree)

	return &SearchDashboardsResult{
		Dashboards: hits,
		Total:      len(searchResp.Payload), // Grafana doesn't return total count
		HasMore:    hasMore,
	}, nil
}

var SearchDashboards = mcpgrafana.MustTool(
	"search_dashboards",
	"Search Grafana dashboards by query and/or folderUid, folderPath, tag, and starred. Returns matching dashboards with title, UID, folder, folder path, tags, and URL.",
	searchDashboards,
	mcpgrafana.WithTitleAnnotation("Search dashboards"),
	mcpgrafana.WithIdempotentHintAnnotation(true),
	mcpgrafana.WithReadOnlyHintAnnotation(true),
	mcpgrafana.WithDestructiveHintAnnotation(false),
	mcpgrafana.WithOpenWorldHintAnnotation(false),
	mcpgrafana.RequiresPermissions("dashboards:read", "folders:read"),
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
	mcpgrafana.WithTitleAnnotation("Search folders"),
	mcpgrafana.WithIdempotentHintAnnotation(true),
	mcpgrafana.WithReadOnlyHintAnnotation(true),
	mcpgrafana.WithDestructiveHintAnnotation(false),
	mcpgrafana.WithOpenWorldHintAnnotation(false),
	mcpgrafana.RequiresPermissions("folders:read"),
)

func AddSearchTools(s *mcp.Server) {
	SearchDashboards.Register(s)
	SearchFolders.Register(s)
}
