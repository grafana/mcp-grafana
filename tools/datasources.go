package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/grafana/grafana-openapi-client-go/models"
	mcpgrafana "github.com/grafana/mcp-grafana"
)

const (
	// DefaultListDatasourcesLimit is the default number of results to return
	DefaultListDatasourcesLimit = 100
)

type ListDatasourcesParams struct {
	Type  string `json:"type,omitempty" jsonschema:"description=Filter by datasource type (e.g. prometheus\\, loki\\, cloudwatch)"`
	Limit int    `json:"limit,omitempty" jsonschema:"description=Maximum number of results to return (default: 100)"`
	Page  int    `json:"page,omitempty" jsonschema:"description=Page number for pagination (1-indexed). Default: 1"`
}

type dataSourceSummary struct {
	ID        int64  `json:"id"`
	UID       string `json:"uid"`
	Name      string `json:"name"`
	Type      string `json:"type"`
	IsDefault bool   `json:"isDefault"`
}

func listDatasources(ctx context.Context, args ListDatasourcesParams) ([]dataSourceSummary, error) {
	c := mcpgrafana.GrafanaClientFromContext(ctx)
	resp, err := c.Datasources.GetDataSources()
	if err != nil {
		return nil, fmt.Errorf("list datasources: %w", err)
	}
	datasources := filterDatasources(resp.Payload, args.Type)
	summaries := summarizeDatasources(datasources)

	// Apply client-side pagination
	return applyDatasourcePagination(summaries, args.Limit, args.Page), nil
}

// applyDatasourcePagination applies pagination to the list of datasources.
func applyDatasourcePagination(items []dataSourceSummary, limit, page int) []dataSourceSummary {
	if limit <= 0 {
		limit = DefaultListDatasourcesLimit
	}
	if page <= 0 {
		page = 1
	}

	start := (page - 1) * limit
	end := start + limit

	if start >= len(items) {
		return []dataSourceSummary{}
	}
	if end > len(items) {
		end = len(items)
	}

	return items[start:end]
}

// filterDatasources returns only datasources of the specified type `t`. If `t`
// is an empty string no filtering is done.
func filterDatasources(datasources models.DataSourceList, t string) models.DataSourceList {
	if t == "" {
		return datasources
	}
	filtered := models.DataSourceList{}
	t = strings.ToLower(t)
	for _, ds := range datasources {
		if strings.Contains(strings.ToLower(ds.Type), t) {
			filtered = append(filtered, ds)
		}
	}
	return filtered
}

func summarizeDatasources(dataSources models.DataSourceList) []dataSourceSummary {
	result := make([]dataSourceSummary, 0, len(dataSources))
	for _, ds := range dataSources {
		result = append(result, dataSourceSummary{
			ID:        ds.ID,
			UID:       ds.UID,
			Name:      ds.Name,
			Type:      ds.Type,
			IsDefault: ds.IsDefault,
		})
	}
	return result
}

var ListDatasources = mcpgrafana.MustTool(
	"list_datasources",
	"List all configured datasources in Grafana. Use this to discover available Prometheus, Loki, and CloudWatch datasources and their UIDs. Supports filtering by type and pagination.",
	listDatasources,
	mcp.WithTitleAnnotation("List datasources"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
)

type GetDatasourceByUIDParams struct {
	UID string `json:"uid" jsonschema:"required,description=The uid of the datasource"`
}

func getDatasourceByUID(ctx context.Context, args GetDatasourceByUIDParams) (*models.DataSource, error) {
	c := mcpgrafana.GrafanaClientFromContext(ctx)
	datasource, err := c.Datasources.GetDataSourceByUID(args.UID)
	if err != nil {
		// Check if it's a 404 Not Found Error
		if strings.Contains(err.Error(), "404") {
			return nil, fmt.Errorf("datasource with UID '%s' not found. Please check if the datasource exists and is accessible", args.UID)
		}
		return nil, fmt.Errorf("get datasource by uid %s: %w", args.UID, err)
	}
	return datasource.Payload, nil
}

var GetDatasourceByUID = mcpgrafana.MustTool(
	"get_datasource_by_uid",
	"Retrieves detailed information about a specific datasource using its UID. Returns the full datasource model, including name, type, URL, access settings, JSON data, and secure JSON field status.",
	getDatasourceByUID,
	mcp.WithTitleAnnotation("Get datasource by UID"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
)

type GetDatasourceByNameParams struct {
	Name string `json:"name" jsonschema:"required,description=The name of the datasource"`
}

func getDatasourceByName(ctx context.Context, args GetDatasourceByNameParams) (*models.DataSource, error) {
	c := mcpgrafana.GrafanaClientFromContext(ctx)
	datasource, err := c.Datasources.GetDataSourceByName(args.Name)
	if err != nil {
		return nil, fmt.Errorf("get datasource by name %s: %w", args.Name, err)
	}
	return datasource.Payload, nil
}

var GetDatasourceByName = mcpgrafana.MustTool(
	"get_datasource_by_name",
	"Retrieves detailed information about a specific datasource using its name. Returns the full datasource model, including UID, type, URL, access settings, JSON data, and secure JSON field status.",
	getDatasourceByName,
	mcp.WithTitleAnnotation("Get datasource by name"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
)

func AddDatasourceTools(mcp *server.MCPServer) {
	ListDatasources.Register(mcp)
	GetDatasourceByUID.Register(mcp)
	GetDatasourceByName.Register(mcp)
}
