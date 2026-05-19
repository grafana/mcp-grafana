package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/grafana/grafana-openapi-client-go/client/datasources"
	"github.com/grafana/grafana-openapi-client-go/models"
	mcpgrafana "github.com/grafana/mcp-grafana"
	datasourceschemas "github.com/grafana/mcp-grafana/tools/datasource_schemas"
)

const (
	defaultListDataSourceLimit = 50
	maxListDataSourceLimit     = 100
)

type ListDatasourcesParams struct {
	Type   string `json:"type,omitempty" jsonschema:"description=The type of datasources to search for. For example\\, 'prometheus'\\, 'loki'\\, 'tempo'\\, etc..."`
	Limit  int    `json:"limit,omitempty" jsonschema:"default=50,description=Maximum number of datasources to return (max 100)"`
	Offset int    `json:"offset,omitempty" jsonschema:"default=0,description=Number of datasources to skip for pagination"`
}

type dataSourceSummary struct {
	ID        int64  `json:"id"`
	UID       string `json:"uid"`
	Name      string `json:"name"`
	Type      string `json:"type"`
	IsDefault bool   `json:"isDefault"`
}

type ListDatasourcesResult struct {
	Datasources []dataSourceSummary `json:"datasources"`
	Total       int                 `json:"total"`   // Total count before pagination
	HasMore     bool                `json:"hasMore"` // Whether more results exist
}

func listDatasources(ctx context.Context, args ListDatasourcesParams) (*ListDatasourcesResult, error) {
	c := mcpgrafana.GrafanaClientFromContext(ctx)
	resp, err := c.Datasources.GetDataSourcesWithParams(
		datasources.NewGetDataSourcesParamsWithContext(ctx),
	)
	if err != nil {
		return nil, fmt.Errorf("list datasources: %w", err)
	}

	// Filter by type if specified
	datasources := filterDatasources(resp.Payload, args.Type)
	total := len(datasources)

	// Apply default limit if not specified
	limit := args.Limit
	if limit <= 0 {
		limit = defaultListDataSourceLimit
	}
	// Cap at maximum
	if limit > maxListDataSourceLimit {
		limit = maxListDataSourceLimit
	}

	offset := args.Offset
	if offset < 0 {
		offset = 0
	}

	// Apply pagination
	var paginated models.DataSourceList
	if offset >= len(datasources) {
		paginated = models.DataSourceList{}
	} else {
		end := offset + limit
		if end > len(datasources) {
			end = len(datasources)
		}
		paginated = datasources[offset:end]
	}

	hasMore := offset+len(paginated) < total

	return &ListDatasourcesResult{
		Datasources: summarizeDatasources(paginated),
		Total:       total,
		HasMore:     hasMore,
	}, nil
}

type CreateDatasourceParams struct {
	Name            string         `json:"name" jsonschema:"required,description=Datasource display name"`
	Type            string         `json:"type" jsonschema:"required,description=Grafana datasource plugin type\\, for example prometheus"`
	URL             string         `json:"url,omitempty" jsonschema:"description=Datasource base URL when required by the plugin"`
	Access          string         `json:"access,omitempty" jsonschema:"description=How Grafana should access the datasource (proxy or direct)"`
	Database        string         `json:"database,omitempty" jsonschema:"description=Optional database name"`
	BasicAuth       bool           `json:"basicAuth,omitempty" jsonschema:"description=Whether Grafana should use basic auth"`
	WithCredentials bool           `json:"withCredentials,omitempty" jsonschema:"description=Whether Grafana should forward credentials such as cookies"`
	IsDefault       bool           `json:"isDefault,omitempty" jsonschema:"description=Whether this should become the default datasource"`
	Fields          map[string]any `json:"fields,omitempty" jsonschema:"description=Datasource field values to provision\\, keyed by field key from the schema returned on the first call. The server uses each field's target (root or jsonData) to place values correctly in the YAML. Example: {\"url\": \"http://prometheus:9090\"\\, \"httpMethod\": \"POST\"}."`
}

type CreateDatasourceResult struct {
	Message    string             `json:"message"`
	ID         int64              `json:"id"`
	UID        string             `json:"uid"`
	Name       string             `json:"name"`
	Datasource *models.DataSource `json:"datasource,omitempty"`
	NextSteps  string             `json:"nextSteps,omitempty"`
}

type noSchemaField struct {
	Key         string `json:"key"`
	Description string `json:"description"`
	Required    bool   `json:"required,omitempty"`
}

type noSchemaGuidanceResult struct {
	Type    string          `json:"type"`
	Message string          `json:"message"`
	Fields  []noSchemaField `json:"fields"`
}

func noSchemaGuidance(pluginType string) *noSchemaGuidanceResult {
	return &noSchemaGuidanceResult{
		Type: pluginType,
		Message: "No schema is available for this datasource type. " +
			"You MUST ask the user for the value of every required field before calling create_datasource again. " +
			"For optional fields, ask only if relevant to the user's setup.",
		Fields: []noSchemaField{
			{Key: "name", Description: "Display name for the datasource.", Required: true},
			{Key: "url", Description: "Base URL of the datasource (e.g. http://localhost:8086)."},
			{Key: "database", Description: "Database name, if applicable."},
			{Key: "basicAuth", Description: "Set to true to enable basic authentication."},
			{Key: "isDefault", Description: "Set to true to make this the default datasource."},
			{Key: "withCredentials", Description: "Set to true to forward browser cookies/credentials."},
		},
	}
}

// applyFields routes Fields values to the body (root-target keys) or into the
// returned jsonData map. secureJsonData keys are never written.
func applyFields(body *models.AddDataSourceCommand, schema *datasourceschemas.DatasourceSchema, inputFields map[string]any) map[string]any {
	lookup := make(map[string]datasourceschemas.DsSchemaField, len(schema.Fields))
	for _, f := range schema.Fields {
		lookup[datasourceschemas.SchemaFieldInputKey(f)] = f
	}

	jsonData := make(map[string]any)
	for inputKey, v := range inputFields {
		f, ok := lookup[inputKey]
		if !ok || f.Target == "secureJsonData" {
			continue
		}
		if f.Target == "root" {
			switch f.Key {
			case "url":
				if s, ok := v.(string); ok {
					body.URL = s
				}
			case "basicAuth":
				if b, ok := v.(bool); ok {
					body.BasicAuth = b
				}
			case "isDefault":
				if b, ok := v.(bool); ok {
					body.IsDefault = b
				}
			case "uid":
				if s, ok := v.(string); ok {
					body.UID = s
				}
			case "access":
				if s, ok := v.(string); ok {
					body.Access = models.DsAccess(s)
				}
			case "withCredentials":
				if b, ok := v.(bool); ok {
					body.WithCredentials = b
				}
			}
		} else {
			jsonData[f.Key] = v
		}
	}
	return jsonData
}

func createDatasource(ctx context.Context, args CreateDatasourceParams) (*mcp.CallToolResult, error) {
	schema, err := datasourceschemas.LoadDatasourceSchema(args.Type)
	if err != nil {
		return nil, err
	}
	// Phase 1: return field guidance before creation.
	// With a schema: list schema fields and ask the user to fill them in.
	// Without a schema: list the explicit params and ask for name + any others
	// the user wants to set, then call again with those values.
	if schema != nil && len(args.Fields) == 0 {
		text, _ := json.Marshal(datasourceschemas.BuildSchemaGuidance(schema, "create_datasource"))
		return mcp.NewToolResultText(string(text)), nil
	}
	if schema == nil && args.Name == "" {
		text, _ := json.Marshal(noSchemaGuidance(args.Type))
		return mcp.NewToolResultText(string(text)), nil
	}

	dsAccess := args.Access
	if dsAccess == "" {
		// use grafana default
		dsAccess = "proxy"
	}

	c := mcpgrafana.GrafanaClientFromContext(ctx)
	// these are used as fallback in case the schema fails to load, that way we can still create a datasource with common, shared fields
	body := &models.AddDataSourceCommand{
		Name:            args.Name,
		Type:            args.Type,
		URL:             args.URL,
		Access:          models.DsAccess(dsAccess),
		Database:        args.Database,
		BasicAuth:       args.BasicAuth,
		IsDefault:       args.IsDefault,
		WithCredentials: args.WithCredentials,
	}
	if schema != nil {
		body.JSONData = models.JSON(applyFields(body, schema, args.Fields))
	}
	resp, err := c.Datasources.AddDataSourceWithParams(
		datasources.NewAddDataSourceParamsWithContext(ctx).WithBody(body),
	)
	if err != nil {
		return nil, fmt.Errorf("create datasource: %w", err)
	}
	p := resp.Payload
	result := &CreateDatasourceResult{
		Datasource: p.Datasource,
	}

	if p.Message != nil {
		result.Message = *p.Message
	}
	if p.ID != nil {
		result.ID = *p.ID
	}
	if p.Name != nil {
		result.Name = *p.Name
	}
	if p.Datasource != nil {
		result.UID = p.Datasource.UID
	}
	if result.UID != "" {
		grafanaURL := c.PublicURL
		if grafanaURL == "" {
			grafanaURL = mcpgrafana.GrafanaConfigFromContext(ctx).URL
		}
		configPageURL := fmt.Sprintf("%s/connections/datasources/edit/%s", grafanaURL, result.UID)
		result.NextSteps = fmt.Sprintf("Visit the datasource configuration page to finish setting it up: %s", configPageURL)
		b, err := json.Marshal(result)
		if err != nil {
			return nil, fmt.Errorf("marshal result: %w", err)
		}
		toolResult := mcp.NewToolResultText(string(b))
		toolResult.Content = append(toolResult.Content, mcp.ResourceLink{
			Type:        "resource_link",
			URI:         configPageURL,
			Name:        result.Name,
			Description: "Datasource configuration page",
		})
		return toolResult, nil
	}
	b, err := json.Marshal(result)
	if err != nil {
		return nil, fmt.Errorf("marshal result: %w", err)
	}
	return mcp.NewToolResultText(string(b)), nil
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
	"List all configured datasources in Grafana. Use this to discover available datasources and their UIDs. Supports filtering by type and pagination.",
	listDatasources,
	mcp.WithTitleAnnotation("List datasources"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
)

var CreateDatasource = mcpgrafana.MustTool(
	"create_datasource",
	"Create a new datasource in Grafana. Returns the created datasource details including its UID. If the datasource type has a known schema: first call with only the type returns field guidance — you MUST then ask the user for every required field value explicitly before calling again with the fields map. If no schema is available for the type, the datasource is created immediately from the explicit params (name, url, etc.) with no fields step. The result includes a nextSteps field and a resource link pointing to the datasource configuration page — always surface both to the user so they can complete setup (e.g. add credentials) in the Grafana UI. Does not support adding credentials or PII and should never ask for authentication options. If credentials are detected, remind the user to rotate and revoke them to keep them safe.",
	createDatasource,
	mcp.WithTitleAnnotation("Create datasource"),
	mcp.WithIdempotentHintAnnotation(false),
	mcp.WithReadOnlyHintAnnotation(false),
)

type GetDatasourceByUIDParams struct {
	UID string `json:"uid" jsonschema:"required,description=The uid of the datasource"`
}

func getDatasourceByUID(ctx context.Context, args GetDatasourceByUIDParams) (*models.DataSource, error) {
	c := mcpgrafana.GrafanaClientFromContext(ctx)
	datasource, err := c.Datasources.GetDataSourceByUIDWithParams(
		datasources.NewGetDataSourceByUIDParamsWithContext(ctx).WithUID(args.UID),
	)
	if err != nil {
		// Check if it's a 404 Not Found Error
		if strings.Contains(err.Error(), "404") {
			return nil, fmt.Errorf("datasource with UID '%s' not found. Please check if the datasource exists and is accessible", args.UID)
		}
		return nil, fmt.Errorf("get datasource by uid %s: %w", args.UID, err)
	}
	return datasource.Payload, nil
}

type GetDatasourceByNameParams struct {
	Name string `json:"name" jsonschema:"required,description=The name of the datasource"`
}

func getDatasourceByName(ctx context.Context, args GetDatasourceByNameParams) (*models.DataSource, error) {
	c := mcpgrafana.GrafanaClientFromContext(ctx)
	datasource, err := c.Datasources.GetDataSourceByNameWithParams(
		datasources.NewGetDataSourceByNameParamsWithContext(ctx).WithName(args.Name),
	)
	if err != nil {
		return nil, fmt.Errorf("get datasource by name %s: %w", args.Name, err)
	}
	return datasource.Payload, nil
}

// GetDatasourceParams accepts either a UID or Name to look up a datasource.
type GetDatasourceParams struct {
	UID  string `json:"uid,omitempty" jsonschema:"description=The UID of the datasource. If provided\\, takes priority over name."`
	Name string `json:"name,omitempty" jsonschema:"description=The name of the datasource. Used if UID is not provided."`
}

func getDatasource(ctx context.Context, args GetDatasourceParams) (*models.DataSource, error) {
	if args.UID != "" {
		return getDatasourceByUID(ctx, GetDatasourceByUIDParams{UID: args.UID})
	}
	if args.Name != "" {
		return getDatasourceByName(ctx, GetDatasourceByNameParams{Name: args.Name})
	}
	return nil, fmt.Errorf("either uid or name must be provided")
}

var GetDatasource = mcpgrafana.MustTool(
	"get_datasource",
	"Retrieves detailed information about a specific datasource by UID or name. Returns the full datasource model, including name, type, URL, access settings, JSON data, and secure JSON field status. Provide either uid or name; uid takes priority if both are given.",
	getDatasource,
	mcp.WithTitleAnnotation("Get datasource"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
)

func AddDatasourceTools(mcp *server.MCPServer, enableWrite bool) {
	ListDatasources.Register(mcp)
	GetDatasource.Register(mcp)
	// this is to make sure that we only register datasource write tools when scope grafana:write has been granted
	if enableWrite {
		CreateDatasource.Register(mcp)
	}
}
