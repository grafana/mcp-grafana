package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	mcpgrafana "github.com/grafana/mcp-grafana"
)

type ListDatasourcesParams struct{}

func listDatasources(ctx context.Context, args ListDatasourcesParams) (*mcp.CallToolResult, error) {
	c := mcpgrafana.GrafanaClientFromContext(ctx)
	datasources, err := c.Datasources.GetDataSources()
	if err != nil {
		return nil, fmt.Errorf("list datasources: %w", err)
	}
	b, err := json.Marshal(datasources.Payload)
	if err != nil {
		return nil, fmt.Errorf("marshal datasources: %w", err)
	}
	return mcp.NewToolResultText(string(b)), nil
}

var ListDatasources = mcpgrafana.MustTool(
	"list_datasources",
	"List datasources",
	listDatasources,
)

type GetDatasourceByUIDParams struct {
	UID string `json:"uid" jsonschema:"required,description=The uid of the datasource"`
}

func getDatasourceByUID(ctx context.Context, args GetDatasourceByUIDParams) (*mcp.CallToolResult, error) {
	c := mcpgrafana.GrafanaClientFromContext(ctx)
	datasource, err := c.Datasources.GetDataSourceByUID(args.UID)
	if err != nil {
		return nil, fmt.Errorf("get datasource by uid %s: %w", args.UID, err)
	}
	b, err := json.Marshal(datasource.Payload)
	if err != nil {
		return nil, fmt.Errorf("marshal datasource: %w", err)
	}
	return mcp.NewToolResultText(string(b)), nil
}

var GetDatasourceByUID = mcpgrafana.MustTool(
	"get_datasource_by_uid",
	"Get datasource by uid",
	getDatasourceByUID,
)

type GetDatasourceByNameParams struct {
	Name string `json:"name" jsonschema:"required,description=The name of the datasource"`
}

func getDatasourceByName(ctx context.Context, args GetDatasourceByNameParams) (*mcp.CallToolResult, error) {
	c := mcpgrafana.GrafanaClientFromContext(ctx)
	datasource, err := c.Datasources.GetDataSourceByName(args.Name)
	if err != nil {
		return nil, fmt.Errorf("get datasource by name %s: %w", args.Name, err)
	}
	b, err := json.Marshal(datasource.Payload)
	if err != nil {
		return nil, fmt.Errorf("marshal datasource: %w", err)
	}
	return mcp.NewToolResultText(string(b)), nil
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
