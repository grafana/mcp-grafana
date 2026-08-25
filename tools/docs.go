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
	"golang.org/x/sync/singleflight"
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
	docsIndexMu     sync.RWMutex
	docsIndex       *grafanadocs.Index
	docsIndexFlight singleflight.Group

	// loadIndexFn is grafanadocs.LoadIndex in production; tests stub it.
	loadIndexFn = grafanadocs.LoadIndex

	// fetchDocFn is grafanadocs.FetchDoc in production; tests stub it.
	fetchDocFn = grafanadocs.FetchDoc

	// docsIndexLoadTimeout bounds a detached index fetch so a stalled
	// LoadIndex cannot run forever. Matches grafanadocs' HTTP client
	// timeout. Overridable in tests.
	docsIndexLoadTimeout = 60 * time.Second
)

// loadDocsIndex lazily loads the documentation index on first successful use.
// Failures are not cached: a cancelled or timed-out first request must not
// poison later calls. Concurrent first loads are coalesced with singleflight
// so the network fetch runs outside docsIndexMu (same pattern as
// fetchPublicURL and ClientCache). The fetch is detached from the caller's
// cancellation so one cancelled tool call cannot abort an in-flight load
// that concurrent callers are waiting on, but it still has its own timeout.
func loadDocsIndex(ctx context.Context) (*grafanadocs.Index, error) {
	docsIndexMu.RLock()
	idx := docsIndex
	docsIndexMu.RUnlock()
	if idx != nil {
		return idx, nil
	}

	v, err, _ := docsIndexFlight.Do(docsIndexURL, func() (any, error) {
		docsIndexMu.RLock()
		idx := docsIndex
		docsIndexMu.RUnlock()
		if idx != nil {
			return idx, nil
		}

		loadCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), docsIndexLoadTimeout)
		defer cancel()
		idx, err := loadIndexFn(loadCtx, docsIndexURL)
		if err != nil {
			return nil, err
		}

		docsIndexMu.Lock()
		docsIndex = idx
		docsIndexMu.Unlock()
		return idx, nil
	})
	if err != nil {
		return nil, err
	}
	return v.(*grafanadocs.Index), nil
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
	Query   string `json:"query,omitempty" jsonschema:"description=Search query for Grafana documentation. Omit to list all available product groups."`
	Product string `json:"product,omitempty" jsonschema:"description=Filter results to a specific product (e.g. 'Grafana Tempo'\\, 'Grafana Loki')"`
	Limit   int    `json:"limit,omitempty" jsonschema:"description=Maximum results to return (default 5)"`
}

type searchDocsEntry struct {
	Title       string `json:"title"`
	URL         string `json:"url"`
	Description string `json:"description,omitempty"`
	Product     string `json:"product"`
}

type listProductEntry struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

type SearchDocsResult struct {
	Results  []searchDocsEntry  `json:"results,omitempty"`
	Products []listProductEntry `json:"products,omitempty"`
	Message  string             `json:"message,omitempty"`
}

func searchDocs(ctx context.Context, args SearchDocsParams) (*SearchDocsResult, error) {
	idx, err := loadDocsIndex(ctx)
	if err != nil {
		return nil, fmt.Errorf("load docs index: %w", err)
	}

	if args.Query == "" {
		products := idx.Products()
		result := make([]listProductEntry, len(products))
		for i, p := range products {
			result[i] = listProductEntry{Name: p.Name, Count: p.Count}
		}
		return &SearchDocsResult{Products: result}, nil
	}

	opts := grafanadocs.SearchOpts{
		Product: args.Product,
		Limit:   args.Limit,
	}
	entries := grafanadocs.Search(idx, args.Query, opts)

	if len(entries) == 0 {
		msg := "No results found."
		if opts.Product != "" {
			msg += " Try broadening the product filter or call search_docs with no query to see available products."
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
	"Search Grafana documentation. Returns matching pages with title, URL, description, and product. Call with no query to list available product groups.",
	searchDocs,
	mcp.WithTitleAnnotation("Search Docs"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
	mcp.WithDestructiveHintAnnotation(false),
	mcp.WithOpenWorldHintAnnotation(true),
)

// Get doc

type GetDocParams struct {
	URL         string `json:"url" jsonschema:"required,description=The grafana.com/docs/ URL to fetch"`
	OutlineOnly bool   `json:"outline_only,omitempty" jsonschema:"description=Return only the heading outline (use to discover section names before fetching content)"`
	Section     string `json:"section,omitempty" jsonschema:"description=Heading text to extract (returns only that section)"`
	Offset      int    `json:"offset,omitempty" jsonschema:"description=Line offset for paging (0-indexed)"`
	Limit       int    `json:"limit,omitempty" jsonschema:"description=Max lines to return (default ~80)"`
}

type docHeading struct {
	Level int    `json:"level"`
	Text  string `json:"text"`
	Line  int    `json:"line"`
}

type GetDocResult struct {
	URL           string       `json:"url"`
	Headings      []docHeading `json:"headings,omitempty"`
	Content       string       `json:"content,omitempty"`
	TotalLines    int          `json:"total_lines,omitempty"`
	ReturnedRange [2]int       `json:"returned_range,omitempty"`
}

func getDoc(ctx context.Context, args GetDocParams) (*GetDocResult, error) {
	doc, err := fetchDocFn(ctx, args.URL)
	if err != nil {
		return nil, fmt.Errorf("fetch doc: %w", err)
	}

	if args.OutlineOnly {
		headings := grafanadocs.Outline(doc)
		result := make([]docHeading, len(headings))
		for i, h := range headings {
			result[i] = docHeading{Level: h.Level, Text: h.Text, Line: h.Line}
		}
		return &GetDocResult{URL: doc.URL, Headings: result}, nil
	}

	opts := grafanadocs.ExcerptOpts{
		Section: args.Section,
		Offset:  args.Offset,
		Limit:   args.Limit,
	}
	excerpt := grafanadocs.Excerpt(doc, opts)

	if excerpt.Content == "" && args.Section != "" {
		return nil, fmt.Errorf("section %q not found; call get_doc with outline_only=true to see available headings", args.Section)
	}

	return &GetDocResult{
		URL:           doc.URL,
		Content:       excerpt.Content,
		TotalLines:    excerpt.Total,
		ReturnedRange: [2]int{excerpt.Start, excerpt.End},
	}, nil
}

var GetDocTool = mcpgrafana.MustTool(
	"get_doc",
	"Fetch a Grafana documentation page. Set outline_only=true to get the heading structure first, then call again with a section name for bounded retrieval.",
	getDoc,
	mcp.WithTitleAnnotation("Get Doc"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
	mcp.WithDestructiveHintAnnotation(false),
	mcp.WithOpenWorldHintAnnotation(true),
)

// AddDocsTools registers all documentation tools on the MCP server.
func AddDocsTools(mcp *server.MCPServer) {
	SearchDocsTool.Register(mcp)
	GetDocTool.Register(mcp)
}
