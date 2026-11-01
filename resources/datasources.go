// Package resources exposes Grafana objects as MCP resources, so users can
// attach them to a conversation directly (for example with @-mentions)
// instead of the model having to discover them through tools.
package resources

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	mcpgrafana "github.com/grafana/mcp-grafana/v2"
	"github.com/grafana/mcp-grafana/v2/internal/datasource"
)

const datasourceURIPrefix = "grafana://datasources/"

// AddDatasources exposes each datasource visible to the caller as a
// grafana://datasources/{uid} resource.
//
// The resource set on an mcp.Server is shared by every caller, but which
// datasources a caller can see depends on their credentials. So reads are
// served by a URI template, and resources/list is extended per request by
// middleware using the Grafana client already in the request context.
func AddDatasources(s *mcp.Server) {
	s.AddResourceTemplate(&mcp.ResourceTemplate{
		URITemplate: datasourceURIPrefix + "{uid}",
		Name:        "datasource",
		Title:       "Grafana datasource",
		Description: "Full details of a Grafana datasource, including its type, URL and settings.",
		MIMEType:    "application/json",
	}, readDatasource)
	s.AddReceivingMiddleware(listDatasourcesMiddleware)
}

func readDatasource(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
	uri := req.Params.URI
	uid, err := url.PathUnescape(strings.TrimPrefix(uri, datasourceURIPrefix))
	if err != nil {
		return nil, mcp.ResourceNotFoundError(uri)
	}
	ds, err := datasource.ByUID(ctx, uid)
	if err != nil {
		return nil, err
	}
	body, err := json.Marshal(ds)
	if err != nil {
		return nil, fmt.Errorf("marshal datasource: %w", err)
	}
	return &mcp.ReadResourceResult{
		// The datasource depends on the caller's credentials.
		Cacheable: mcp.Cacheable{CacheScope: "private"},
		Contents:  []*mcp.ResourceContents{{URI: uri, MIMEType: "application/json", Text: string(body)}},
	}, nil
}

// listDatasourcesMiddleware appends the caller's datasources to the last page
// of resources/list. If they can't be fetched, the static resources are still
// listed: a Grafana error shouldn't hide the rest of the server's resources.
func listDatasourcesMiddleware(next mcp.MethodHandler) mcp.MethodHandler {
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
		if r.NextCursor != "" || mcpgrafana.GrafanaClientFromContext(ctx) == nil {
			return r, nil
		}
		list, err := datasource.List(ctx)
		if err != nil {
			mcpgrafana.LoggerFromContext(ctx).WarnContext(ctx, "Not listing datasource resources", "error", err)
			return r, nil
		}
		for _, ds := range list {
			desc := ds.Type + " datasource"
			if ds.IsDefault {
				desc += " (default)"
			}
			r.Resources = append(r.Resources, &mcp.Resource{
				URI:         datasourceURIPrefix + url.PathEscape(ds.UID),
				Name:        ds.Name,
				Description: desc,
				MIMEType:    "application/json",
			})
		}
		return r, nil
	}
}
