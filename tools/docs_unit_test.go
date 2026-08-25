//go:build unit

package tools

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/grafana/mcp-doc-server/pkg/grafanadocs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testIndexFixture = `# Documentation

## Grafana Tempo documentation

- [Tempo overview](https://grafana.com/docs/tempo/latest/): Tempo is a distributed tracing backend.
- [Configure Tempo](https://grafana.com/docs/tempo/latest/configuration/): Configure Tempo to suit your needs.
- [Tempo query language](https://grafana.com/docs/tempo/latest/traceql/): TraceQL is a query language for traces.

## Grafana Loki documentation

- [Loki overview](https://grafana.com/docs/loki/latest/): Loki is a log aggregation system.
- [LogQL](https://grafana.com/docs/loki/latest/logql/): LogQL is a query language for logs.
`

func loadTestIndex(t *testing.T) *grafanadocs.Index {
	t.Helper()
	idx, err := grafanadocs.LoadIndexFromReader(strings.NewReader(testIndexFixture))
	require.NoError(t, err)
	return idx
}

func withTestDocsIndex(t *testing.T) {
	t.Helper()
	ResetDocsIndex()
	t.Cleanup(ResetDocsIndex)

	idx := loadTestIndex(t)
	docsIndexMu.Lock()
	docsIndex = idx
	docsIndexMu.Unlock()
}

func TestSearchDocs_FindsResults(t *testing.T) {
	withTestDocsIndex(t)

	result, err := searchDocs(context.Background(), SearchDocsParams{
		Query: "tempo configuration",
	})
	require.NoError(t, err)
	require.NotEmpty(t, result.Results)
	assert.Empty(t, result.Message)

	found := false
	for _, r := range result.Results {
		if strings.Contains(r.Title, "Configure Tempo") {
			found = true
			assert.Equal(t, "Grafana Tempo", r.Product)
			assert.Contains(t, r.URL, "grafana.com/docs/tempo")
		}
	}
	assert.True(t, found, "expected to find Configure Tempo in results")
}

func TestSearchDocs_ProductFilter(t *testing.T) {
	withTestDocsIndex(t)

	result, err := searchDocs(context.Background(), SearchDocsParams{
		Query:   "query language",
		Product: "Grafana Loki",
	})
	require.NoError(t, err)
	require.NotEmpty(t, result.Results)

	for _, r := range result.Results {
		assert.Equal(t, "Grafana Loki", r.Product)
	}
}

func TestSearchDocs_NoResults(t *testing.T) {
	withTestDocsIndex(t)

	result, err := searchDocs(context.Background(), SearchDocsParams{
		Query: "xyznonexistent",
	})
	require.NoError(t, err)
	assert.Empty(t, result.Results)
	assert.Contains(t, result.Message, "No results found")
	assert.Contains(t, result.Message, "Try different search terms")
}

func TestSearchDocs_NoResultsWithProductFilter(t *testing.T) {
	withTestDocsIndex(t)

	result, err := searchDocs(context.Background(), SearchDocsParams{
		Query:   "xyznonexistent",
		Product: "Grafana Tempo",
	})
	require.NoError(t, err)
	assert.Empty(t, result.Results)
	assert.Contains(t, result.Message, "list_products")
}

func TestListProducts_ReturnsProducts(t *testing.T) {
	withTestDocsIndex(t)

	result, err := listProducts(context.Background(), ListProductsParams{})
	require.NoError(t, err)
	require.NotEmpty(t, result.Products)

	names := make([]string, len(result.Products))
	for i, p := range result.Products {
		names[i] = p.Name
		assert.Greater(t, p.Count, 0)
	}
	assert.Contains(t, names, "Grafana Tempo")
	assert.Contains(t, names, "Grafana Loki")
}

func withStubbedDoc(t *testing.T, content string) {
	t.Helper()
	orig := fetchDocFn
	t.Cleanup(func() { fetchDocFn = orig })
	fetchDocFn = func(_ context.Context, rawURL string) (*grafanadocs.Doc, error) {
		return &grafanadocs.Doc{URL: rawURL, Content: []byte(content)}, nil
	}
}

func TestGetDoc_FetchesAndExcerpts(t *testing.T) {
	docContent := `---
title: Test Page
---
# Test Page

This is the introduction.

## Configuration

Set the following options:

- option_a: does something
- option_b: does something else

## Examples

Here is an example.
`
	withStubbedDoc(t, docContent)

	result, err := getDoc(context.Background(), GetDocParams{
		URL:   "https://grafana.com/docs/test/",
		Limit: 5,
	})
	require.NoError(t, err)
	assert.NotEmpty(t, result.Content)
	assert.Equal(t, "https://grafana.com/docs/test/", result.URL)
	assert.Greater(t, result.TotalLines, 0)
	assert.Equal(t, 1, result.ReturnedRange[0])
}

func TestGetDocOutline_ReturnsHeadings(t *testing.T) {
	docContent := `# Page Title

Intro text.

## First Section

Content.

### Subsection

More content.

## Second Section

End.
`
	withStubbedDoc(t, docContent)

	result, err := getDocOutline(context.Background(), GetDocOutlineParams{
		URL: "https://grafana.com/docs/test/",
	})
	require.NoError(t, err)
	require.NotEmpty(t, result.Headings)
	assert.Equal(t, "Page Title", result.Headings[0].Text)
	assert.Equal(t, 1, result.Headings[0].Level)
}

func TestGetDoc_PropagatesFetchError(t *testing.T) {
	orig := fetchDocFn
	t.Cleanup(func() { fetchDocFn = orig })
	fetchDocFn = func(context.Context, string) (*grafanadocs.Doc, error) {
		return nil, errors.New("network timeout")
	}

	_, err := getDoc(context.Background(), GetDocParams{
		URL: "https://grafana.com/docs/test/",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "network timeout")
}

func TestGetDocOutline_PropagatesFetchError(t *testing.T) {
	orig := fetchDocFn
	t.Cleanup(func() { fetchDocFn = orig })
	fetchDocFn = func(context.Context, string) (*grafanadocs.Doc, error) {
		return nil, errors.New("network timeout")
	}

	_, err := getDocOutline(context.Background(), GetDocOutlineParams{
		URL: "https://grafana.com/docs/test/",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "network timeout")
}

func TestGetDoc_RejectsNonGrafanaURL(t *testing.T) {
	_, err := getDoc(context.Background(), GetDocParams{
		URL: "https://example.com/docs/test/",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "rejected")
}

func TestSearchDocs_RespectLimit(t *testing.T) {
	withTestDocsIndex(t)

	result, err := searchDocs(context.Background(), SearchDocsParams{
		Query: "tempo",
		Limit: 1,
	})
	require.NoError(t, err)
	assert.LessOrEqual(t, len(result.Results), 1)
}

func TestSearchDocs_ToolDefinition(t *testing.T) {
	assert.Equal(t, "search_docs", SearchDocsTool.Tool.Name)
	assert.Contains(t, SearchDocsTool.Tool.Description, "Search Grafana documentation")
}

func TestGetDocTool_ToolDefinition(t *testing.T) {
	assert.Equal(t, "get_doc", GetDocTool.Tool.Name)
	assert.Contains(t, GetDocTool.Tool.Description, "Fetch a Grafana documentation page")
}

func TestGetDocOutlineTool_ToolDefinition(t *testing.T) {
	assert.Equal(t, "get_doc_outline", GetDocOutlineTool.Tool.Name)
	assert.Contains(t, GetDocOutlineTool.Tool.Description, "heading outline")
}

func TestListProductsTool_ToolDefinition(t *testing.T) {
	assert.Equal(t, "list_products", ListProductsTool.Tool.Name)
	assert.Contains(t, ListProductsTool.Tool.Description, "product documentation groups")
}

func TestLoadDocsIndex_RetriesAfterFailure(t *testing.T) {
	ResetDocsIndex()
	t.Cleanup(func() {
		ResetDocsIndex()
		loadIndexFn = grafanadocs.LoadIndex
	})

	idx := loadTestIndex(t)
	var calls atomic.Int32
	loadIndexFn = func(ctx context.Context, _ string) (*grafanadocs.Index, error) {
		n := calls.Add(1)
		if n == 1 {
			return nil, errors.New("transient index fetch failure")
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return idx, nil
	}

	_, err := loadDocsIndex(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "transient index fetch failure")

	got, err := loadDocsIndex(context.Background())
	require.NoError(t, err)
	require.Equal(t, idx, got)
	assert.Equal(t, int32(2), calls.Load())

	got, err = loadDocsIndex(context.Background())
	require.NoError(t, err)
	require.Equal(t, idx, got)
	assert.Equal(t, int32(2), calls.Load(), "successful load must be cached")
}

func TestLoadDocsIndex_IgnoresCallerCancellation(t *testing.T) {
	ResetDocsIndex()
	t.Cleanup(func() {
		ResetDocsIndex()
		loadIndexFn = grafanadocs.LoadIndex
	})

	idx := loadTestIndex(t)
	loadIndexFn = func(ctx context.Context, _ string) (*grafanadocs.Index, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return idx, nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	got, err := loadDocsIndex(ctx)
	require.NoError(t, err)
	require.Equal(t, idx, got)
}

func TestLoadDocsIndex_TimesOutDetachedLoad(t *testing.T) {
	ResetDocsIndex()
	oldTimeout := docsIndexLoadTimeout
	t.Cleanup(func() {
		ResetDocsIndex()
		loadIndexFn = grafanadocs.LoadIndex
		docsIndexLoadTimeout = oldTimeout
	})
	docsIndexLoadTimeout = 20 * time.Millisecond

	loadIndexFn = func(ctx context.Context, _ string) (*grafanadocs.Index, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}

	_, err := loadDocsIndex(context.Background())
	require.Error(t, err)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestLoadDocsIndex_CoalescesConcurrentLoads(t *testing.T) {
	ResetDocsIndex()
	t.Cleanup(func() {
		ResetDocsIndex()
		loadIndexFn = grafanadocs.LoadIndex
	})

	want := loadTestIndex(t)
	started := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	loadIndexFn = func(ctx context.Context, _ string) (*grafanadocs.Index, error) {
		if calls.Add(1) == 1 {
			close(started)
		}
		select {
		case <-release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		return want, nil
	}

	const n = 8
	var wg sync.WaitGroup
	wg.Add(n)
	got := make([]*grafanadocs.Index, n)
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		go func(idx int) {
			defer wg.Done()
			got[idx], errs[idx] = loadDocsIndex(context.Background())
		}(i)
	}

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("index load never started")
	}
	close(release)
	wg.Wait()

	for i := 0; i < n; i++ {
		require.NoError(t, errs[i])
		assert.Same(t, want, got[i])
	}
	assert.Equal(t, int32(1), calls.Load(), "concurrent callers must share one in-flight load")
}
