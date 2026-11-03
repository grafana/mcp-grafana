package resources

import (
	"context"
	"errors"
	"net/url"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	mcpgrafana "github.com/grafana/mcp-grafana/v2"
	"github.com/grafana/mcp-grafana/v2/internal/datasource"
)

const datasourceURIPrefix = "grafana://datasources/"

// Datasources exposes each datasource visible to the caller as a
// grafana://datasources/{uid} resource.
var Datasources = Kind{
	template: &mcp.ResourceTemplate{
		URITemplate: datasourceURIPrefix + "{uid}",
		Name:        "datasource",
		Title:       "Grafana datasource",
		Description: "Full details of a Grafana datasource, including its type, URL and settings.",
		MIMEType:    "application/json",
	},
	read: readDatasource,
	list: listDatasources,
}

func readDatasource(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
	uri := req.Params.URI
	uid, err := idFromURI(uri, datasourceURIPrefix)
	if err != nil {
		return nil, err
	}
	if mcpgrafana.GrafanaClientFromContext(ctx) == nil {
		return nil, errors.New("no Grafana client is configured")
	}
	ds, err := datasource.ByUID(ctx, uid)
	if errors.Is(err, datasource.ErrNotFound) {
		return nil, mcp.ResourceNotFoundError(uri)
	}
	if err != nil {
		return nil, err
	}
	return jsonResource(uri, ds)
}

func listDatasources(ctx context.Context) ([]*mcp.Resource, error) {
	if mcpgrafana.GrafanaClientFromContext(ctx) == nil {
		return nil, nil
	}
	list, err := datasource.List(ctx)
	if err != nil {
		return nil, err
	}
	resources := make([]*mcp.Resource, 0, len(list))
	for _, ds := range list {
		desc := ds.Name + " · " + ds.Type
		if ds.IsDefault {
			desc += " (default)"
		}
		resources = append(resources, &mcp.Resource{
			URI:         datasourceURIPrefix + url.PathEscape(ds.UID),
			Name:        ds.Name,
			Description: desc,
			MIMEType:    "application/json",
		})
	}
	return resources, nil
}
