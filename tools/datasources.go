package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/grafana/grafana-openapi-client-go/models"
	mcpgrafana "github.com/grafana/mcp-grafana"
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
	resp, err := c.Datasources.GetDataSources()
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
	Name            string                 `json:"name" jsonschema:"required,description=Datasource display name"`
	Type            string                 `json:"type" jsonschema:"required,description=Grafana datasource plugin type\\, for example prometheus"`
	URL             string                 `json:"url,omitempty" jsonschema:"description=Datasource base URL when required by the plugin"`
	Access          string                 `json:"access,omitempty" jsonschema:"description=How Grafana should access the datasource (proxy or direct)"`
	Database        string                 `json:"database,omitempty" jsonschema:"description=Optional database name"`
	User            string                 `json:"user,omitempty" jsonschema:"description=Optional username"`
	BasicAuth       bool                   `json:"basicAuth,omitempty" jsonschema:"description=Whether Grafana should use basic auth"`
	BasicAuthUser   string                 `json:"basicAuthUser,omitempty" jsonschema:"description=Basic auth username when basic auth is enabled"`
	WithCredentials bool                   `json:"withCredentials,omitempty" jsonschema:"description=Whether Grafana should forward credentials such as cookies"`
	IsDefault       bool                   `json:"isDefault,omitempty" jsonschema:"description=Whether this should become the default datasource"`
	JSONData        map[string]interface{} `json:"jsonData,omitempty" jsonschema:"description=Datasource-specific non-secret settings, eventually this will be dynamic"`
	SecureJSONData  map[string]string      `json:"secureJsonData,omitempty" jsonschema:"description=Datasource-specific secret settings such as passwords or tokens"`
}

type CreateDatasourceResult struct {
	Message    string       `json:"message"`
	ID         int64        `json:"id"`
	UID        string       `json:"uid"`
	Name       string       `json:"name"`
	Datasource *models.DataSource `json:"datasource,omitempty"`
}

func createDatasource(ctx context.Context, args CreateDatasourceParams) (*mcp.CallToolResult, error) {
	if reason := checkDatasourceCredentials(args); reason != "" {
		return credentialViolationResult(reason, datasourceConfigPageURL(ctx, "")), nil
	}
	c := mcpgrafana.GrafanaClientFromContext(ctx)
	body := &models.AddDataSourceCommand{
		Name:            args.Name,
		Type:            args.Type,
		URL:             args.URL,
		Access:          models.DsAccess(args.Access),
		Database:        args.Database,
		User:            args.User,
		BasicAuth:       args.BasicAuth,
		BasicAuthUser:   args.BasicAuthUser,
		WithCredentials: args.WithCredentials,
		IsDefault:       args.IsDefault,
		JSONData:        models.JSON(args.JSONData),
		SecureJSONData:  args.SecureJSONData,
	}
	resp, err := c.Datasources.AddDataSource(body)
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
	"Create a new datasource in Grafana. Returns the created datasource details including its UID.",
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

type AddAuthenticationToDatasourceParams struct {
	UID string `json:"uid,omitempty" jsonschema:"description=Datasource UID to open in Grafana (e.g. after finding the Prometheus datasource). Omit only when the user is adding a brand-new datasource in the UI."`
}

func addAuthenticationToDatasource(ctx context.Context, args AddAuthenticationToDatasourceParams) (*mcp.CallToolResult, error) {
	uid := strings.TrimSpace(args.UID)
	if uid != "" {
		if matchesSecretLike(uid) {
			return credentialViolationResult("embedded_secret_or_token", datasourceConfigPageURL(ctx, "")), nil
		}
		if matchesAuthIntent(uid) {
			return credentialViolationResult("auth_credential_instructions", datasourceConfigPageURL(ctx, "")), nil
		}
	}
	return credentialViolationResult("auth_credential_instructions", datasourceConfigPageURL(ctx, "")), nil
}

var AddAuthenticationToDatasource = mcpgrafana.MustTool(
	"add_authentication_to_datasource",
	"Use this when the user asks to add, set, configure, or rotate authentication for a Grafana datasource—passwords, API tokens, secrets, basic auth, bearer tokens, or TLS secrets—including Prometheus, Loki, or any other plugin type. Opens the Grafana UI to the datasource settings page (pass uid from list_datasources or get_datasource for an existing datasource; omit uid to open the new datasource page). Does not accept credential values through MCP. Do not use create_datasource for authentication or secrets; this server blocks those fields on create and this tool is the supported path for that intent.",
	addAuthenticationToDatasource,
	mcp.WithTitleAnnotation("Add authentication to datasource"),
	mcp.WithIdempotentHintAnnotation(false),
	mcp.WithReadOnlyHintAnnotation(false),
)

func AddDatasourceTools(mcp *server.MCPServer, enableWrite bool) {
	ListDatasources.Register(mcp)
	GetDatasource.Register(mcp)
	// this is to make sure that we only register datasource write tools when scope grafana:write has been granted
	if enableWrite {
		AddAuthenticationToDatasource.Register(mcp)
		CreateDatasource.Register(mcp)
	}
}
