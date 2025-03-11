package tools

import (
	"context"
	"fmt"

	"github.com/mark3labs/mcp-go/server"

	"github.com/grafana/grafana-openapi-client-go/client/datasources"
	mcpgrafana "github.com/grafana/mcp-grafana"
)

type ListDatasourcesParams struct{}

func listDatasources(ctx context.Context, args ListDatasourcesParams) (*datasources.GetDataSourcesOK, error) {
	c := mcpgrafana.GrafanaClientFromContext(ctx)
	datasources, err := c.Datasources.GetDataSources()
	if err != nil {
		return nil, fmt.Errorf("list datasources: %w", err)
	}
	return datasources, nil
}

var ListDatasources = mcpgrafana.MustTool(
	"list_datasources",
	"List datasources",
	listDatasources,
)

type GetDatasourceByUIDParams struct {
	UID string `json:"uid" jsonschema:"required,description=The uid of the datasource"`
}

func getDatasourceByUID(ctx context.Context, args GetDatasourceByUIDParams) (*datasources.GetDataSourceByUIDOK, error) {
	c := mcpgrafana.GrafanaClientFromContext(ctx)
	datasource, err := c.Datasources.GetDataSourceByUID(args.UID)
	if err != nil {
		return nil, fmt.Errorf("get datasource by uid %s: %w", args.UID, err)
	}
	return datasource, nil
}

var GetDatasourceByUID = mcpgrafana.MustTool(
	"get_datasource_by_uid",
	"Get datasource by uid",
	getDatasourceByUID,
)

type GetDatasourceByNameParams struct {
	Name string `json:"name" jsonschema:"required,description=The name of the datasource"`
}

func getDatasourceByName(ctx context.Context, args GetDatasourceByNameParams) (*datasources.GetDataSourceByNameOK, error) {
	c := mcpgrafana.GrafanaClientFromContext(ctx)
	datasource, err := c.Datasources.GetDataSourceByName(args.Name)
	if err != nil {
		return nil, fmt.Errorf("get datasource by name %s: %w", args.Name, err)
	}
	return datasource, nil
}

var GetDatasourceByName = mcpgrafana.MustTool(
	"get_datasource_by_name",
	"Get datasource by name",
	getDatasourceByName,
)

func AddDatasourceTools(mcp *server.MCPServer) {
	ListDatasources.Register(mcp)
	GetDatasourceByUID.Register(mcp)
	GetDatasourceByName.Register(mcp)
}
