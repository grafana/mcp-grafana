// Package resources exposes Grafana objects as MCP resources, so users can
// attach them to a conversation directly (for example with @-mentions)
// instead of the model having to discover them through tools.
package resources

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	mcpgrafana "github.com/grafana/mcp-grafana/v2"
)

// Kind is a family of resources sharing one URI template. Only kinds with a
// small, bounded number of resources belong here: clients such as Claude
// Code only offer what resources/list returns, and every list fetches every
// kind. Those clients also show a resource's URI and description but not its
// name, so descriptions lead with the name.
type Kind struct {
	template *mcp.ResourceTemplate
	read     mcp.ResourceHandler
	// list returns the resources currently visible to the caller. It should
	// honour ctx: a list still running at the deadline is left out, but keeps
	// running in the background until it returns.
	list func(context.Context) ([]*mcp.Resource, error)
}

// listTimeout bounds each kind's lookup, so one slow backend can't stall
// resources/list.
var listTimeout = 5 * time.Second

// Register serves kinds on s.
//
// The resource set on an mcp.Server is shared by every caller, but which
// resources a caller can see depends on their credentials. So reads are
// served by URI templates, and resources/list is extended per request by
// middleware using the clients already in the request context.
func Register(s *mcp.Server, kinds ...Kind) {
	if len(kinds) == 0 {
		return
	}
	for _, k := range kinds {
		s.AddResourceTemplate(k.template, k.read)
	}
	s.AddReceivingMiddleware(listMiddleware(kinds))
}

// listMiddleware appends every kind's resources to the last page of
// resources/list, fetching kinds in parallel. A kind that fails is left out:
// many deployments lack some backends (IRM, for example), and that shouldn't
// hide the rest.
func listMiddleware(kinds []Kind) mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			result, err := next(ctx, method, req)
			if err != nil || method != "resources/list" {
				return result, err
			}
			r, ok := result.(*mcp.ListResourcesResult)
			if !ok {
				return result, err
			}
			// The list depends on the caller's credentials.
			r.CacheScope = "private"
			if r.NextCursor != "" {
				return r, nil
			}
			found := make([][]*mcp.Resource, len(kinds))
			var wg sync.WaitGroup
			for i, k := range kinds {
				wg.Go(func() {
					found[i] = listKind(ctx, k)
				})
			}
			wg.Wait()
			for _, resources := range found {
				r.Resources = append(r.Resources, resources...)
			}
			return r, nil
		}
	}
}

// listKind returns k's resources, or nil if listing fails, panics or misses
// the deadline, even when k.list ignores its context.
func listKind(ctx context.Context, k Kind) []*mcp.Resource {
	logger := mcpgrafana.LoggerFromContext(ctx).With("kind", k.template.Name)
	ctx, cancel := context.WithTimeout(ctx, listTimeout)
	defer cancel()
	type listed struct {
		resources []*mcp.Resource
		err       error
	}
	done := make(chan listed, 1) // buffered, so an abandoned list can still finish
	go func() {
		// This goroutine is out of reach of the recovery middleware, so a
		// panic here would take down the server.
		defer func() {
			if p := recover(); p != nil {
				logger.ErrorContext(ctx, "Listing resources panicked", "panic", p, "stack", string(debug.Stack()))
				done <- listed{err: fmt.Errorf("panic: %v", p)}
			}
		}()
		resources, err := k.list(ctx)
		done <- listed{resources, err}
	}()
	select {
	case r := <-done:
		if r.err != nil {
			logger.DebugContext(ctx, "Not listing resources", "error", r.err)
			return nil
		}
		return r.resources
	case <-ctx.Done():
		logger.DebugContext(ctx, "Not listing resources", "error", ctx.Err())
		return nil
	}
}

// jsonResource returns v as the private JSON contents of uri.
func jsonResource(uri string, v any) (*mcp.ReadResourceResult, error) {
	body, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("marshal %s: %w", uri, err)
	}
	return &mcp.ReadResourceResult{
		// The contents depend on the caller's credentials.
		Cacheable: mcp.Cacheable{CacheScope: "private"},
		Contents:  []*mcp.ResourceContents{{URI: uri, MIMEType: "application/json", Text: string(body)}},
	}, nil
}

// idFromURI returns the unescaped final segment of uri after prefix.
func idFromURI(uri, prefix string) (string, error) {
	id, err := url.PathUnescape(strings.TrimPrefix(uri, prefix))
	if err != nil {
		return "", mcp.ResourceNotFoundError(uri)
	}
	return id, nil
}
