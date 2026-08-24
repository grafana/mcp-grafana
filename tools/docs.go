package tools

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/grafana/mcp-doc-server/pkg/grafanadocs"
	mcpgrafana "github.com/grafana/mcp-grafana"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// docsIndexURL is the URL for the Grafana documentation index.
// Overridable via DOCS_INDEX_URL for testing or custom deployments.
var docsIndexURL = defaultDocsIndexURL()

func defaultDocsIndexURL() string {
	if u := os.Getenv("DOCS_INDEX_URL"); u != "" {
		return u
	}
	return grafanadocs.DefaultIndexURL
}

var (
	docsIndexMu sync.RWMutex
	docsIndex   *grafanadocs.Index

	// loadIndexFn is grafanadocs.LoadIndex in production; tests stub it.
	loadIndexFn = grafanadocs.LoadIndex

	// docsIndexLoadTimeout bounds a detached index fetch so a stalled
	// LoadIndex cannot hold docsIndexMu forever. Matches grafanadocs'
	// HTTP client timeout. Overridable in tests.
	docsIndexLoadTimeout = 60 * time.Second
)

// loadDocsIndex lazily loads the documentation index on first successful use.
// Failures are not cached: a cancelled or timed-out first request must not
// poison later calls. The fetch is detached from the caller's cancellation
// so one cancelled tool call cannot abort an in-flight load that concurrent
// callers are waiting on, but it still has its own timeout (same pattern as
// fetchPublicURL). A one-shot sync.Once is deliberately not used — see
// session.go's proxied-tool init for the same reason.
func loadDocsIndex(ctx context.Context) (*grafanadocs.Index, error) {
	docsIndexMu.RLock()
	idx := docsIndex
	docsIndexMu.RUnlock()
	if idx != nil {
		return idx, nil
	}

	docsIndexMu.Lock()
	defer docsIndexMu.Unlock()
	if docsIndex != nil {
		return docsIndex, nil
	}

	loadCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), docsIndexLoadTimeout)
	defer cancel()
	idx, err := loadIndexFn(loadCtx, docsIndexURL)
	if err != nil {
		return nil, err
	}
	docsIndex = idx
	return docsIndex, nil
}

// ResetDocsIndex clears the cached index so the next call reloads it.
// Exported for testing only.
func ResetDocsIndex() {
	docsIndexMu.Lock()
	docsIndex = nil
	docsIndexMu.Unlock()
}

// Search docs

type SearchDocsParams struct {
	Query   string `json:"query" jsonschema:"required,description=Search query for Grafana documentation"`
	Product string `json:"product,omitempty" jsonschema:"description=Filter results to a specific product (e.g. 'Grafana Tempo'\\, 'Grafana Loki')"`
	Limit   int    `json:"limit,omitempty" jsonschema:"description=Maximum results to return (default 5)"`
}

type searchDocsEntry struct {
	Title       string `json:"title"`
	URL         string `json:"url"`
	Description string `json:"description,omitempty"`
	Product     string `json:"product"`
}

type SearchDocsResult struct {
	Results []searchDocsEntry `json:"results"`
	Message string            `json:"message,omitempty"`
}

func searchDocs(ctx context.Context, args SearchDocsParams) (*SearchDocsResult, error) {
	idx, err := loadDocsIndex(ctx)
	if err != nil {
		return nil, fmt.Errorf("load docs index: %w", err)
	}

	opts := grafanadocs.SearchOpts{
		Product: args.Product,
		Limit:   args.Limit,
	}
	entries := grafanadocs.Search(idx, args.Query, opts)

	if len(entries) == 0 {
		msg := "No results found."
		if opts.Product != "" {
			msg += " Try broadening the product filter or using list_products to see available products."
		} else {
			msg += " Try different search terms."
		}
		return &SearchDocsResult{Message: msg}, nil
	}

	results := make([]searchDocsEntry, len(entries))
	for i, e := range entries {
		results[i] = searchDocsEntry{
			Title:       e.Title,
			URL:         e.URL,
			Description: e.Description,
			Product:     e.Product,
		}
	}
	return &SearchDocsResult{Results: results}, nil
}

var SearchDocsTool = mcpgrafana.MustTool(
	"search_docs",
	"Search Grafana documentation. Returns matching pages with title, URL, description, and product.",
	searchDocs,
	mcp.WithTitleAnnotation("Search Docs"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
	mcp.WithDestructiveHintAnnotation(false),
	mcp.WithOpenWorldHintAnnotation(true),
)

// Get doc

type GetDocParams struct {
	URL     string `json:"url" jsonschema:"required,description=The grafana.com/docs/ URL to fetch"`
	Section string `json:"section,omitempty" jsonschema:"description=Heading text to extract (returns only that section)"`
	Offset  int    `json:"offset,omitempty" jsonschema:"description=Line offset for paging (0-indexed)"`
	Limit   int    `json:"limit,omitempty" jsonschema:"description=Max lines to return (default ~80)"`
}

type GetDocResult struct {
	Content       string `json:"content"`
	URL           string `json:"url"`
	TotalLines    int    `json:"total_lines"`
	ReturnedRange [2]int `json:"returned_range"`
}

func getDoc(ctx context.Context, args GetDocParams) (*GetDocResult, error) {
	doc, err := grafanadocs.FetchDoc(ctx, args.URL)
	if err != nil {
		return nil, fmt.Errorf("fetch doc: %w", err)
	}

	opts := grafanadocs.ExcerptOpts{
		Section: args.Section,
		Offset:  args.Offset,
		Limit:   args.Limit,
	}
	result := grafanadocs.Excerpt(doc, opts)

	if result.Content == "" && args.Section != "" {
		return nil, fmt.Errorf("section %q not found; use get_doc_outline to see available headings", args.Section)
	}

	return &GetDocResult{
		Content:       result.Content,
		URL:           doc.URL,
		TotalLines:    result.Total,
		ReturnedRange: [2]int{result.Start, result.End},
	}, nil
}

var GetDocTool = mcpgrafana.MustTool(
	"get_doc",
	"Fetch a Grafana documentation page. Returns cleaned markdown content. Supports section extraction by heading name and offset/limit paging for bounded retrieval.",
	getDoc,
	mcp.WithTitleAnnotation("Get Doc"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
	mcp.WithDestructiveHintAnnotation(false),
	mcp.WithOpenWorldHintAnnotation(true),
)

// Get doc outline

type GetDocOutlineParams struct {
	URL string `json:"url" jsonschema:"required,description=The grafana.com/docs/ URL"`
}

type docHeading struct {
	Level int    `json:"level"`
	Text  string `json:"text"`
	Line  int    `json:"line"`
}

type GetDocOutlineResult struct {
	URL      string       `json:"url"`
	Headings []docHeading `json:"headings"`
}

func getDocOutline(ctx context.Context, args GetDocOutlineParams) (*GetDocOutlineResult, error) {
	doc, err := grafanadocs.FetchDoc(ctx, args.URL)
	if err != nil {
		return nil, fmt.Errorf("fetch doc: %w", err)
	}

	headings := grafanadocs.Outline(doc)
	result := make([]docHeading, len(headings))
	for i, h := range headings {
		result[i] = docHeading{Level: h.Level, Text: h.Text, Line: h.Line}
	}

	return &GetDocOutlineResult{URL: doc.URL, Headings: result}, nil
}

var GetDocOutlineTool = mcpgrafana.MustTool(
	"get_doc_outline",
	"Get the heading outline of a Grafana documentation page. Use this to find section names before calling get_doc with a section parameter.",
	getDocOutline,
	mcp.WithTitleAnnotation("Get Doc Outline"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
	mcp.WithDestructiveHintAnnotation(false),
	mcp.WithOpenWorldHintAnnotation(true),
)

// List products

type ListProductsParams struct{}

type listProductEntry struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

type ListProductsResult struct {
	Products []listProductEntry `json:"products"`
}

func listProducts(ctx context.Context, args ListProductsParams) (*ListProductsResult, error) {
	idx, err := loadDocsIndex(ctx)
	if err != nil {
		return nil, fmt.Errorf("load docs index: %w", err)
	}

	products := idx.Products()
	result := make([]listProductEntry, len(products))
	for i, p := range products {
		result[i] = listProductEntry{Name: p.Name, Count: p.Count}
	}

	return &ListProductsResult{Products: result}, nil
}

var ListProductsTool = mcpgrafana.MustTool(
	"list_products",
	"List all Grafana product documentation groups with their entry counts.",
	listProducts,
	mcp.WithTitleAnnotation("List Doc Products"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
	mcp.WithDestructiveHintAnnotation(false),
	mcp.WithOpenWorldHintAnnotation(true),
)

// AddDocsTools registers all documentation tools on the MCP server.
func AddDocsTools(mcp *server.MCPServer) {
	SearchDocsTool.Register(mcp)
	GetDocTool.Register(mcp)
	GetDocOutlineTool.Register(mcp)
	ListProductsTool.Register(mcp)
}
