package tools

import (
	"context"
	"fmt"
	"strings"
	"sync"

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

var UpdateDatasource = mcpgrafana.MustTool(
	"update_datasource",
	"Update non-secret Grafana datasource fields by UID (name, url, jsonData without secrets, default flag, etc.). Fetches the current config and merges only the fields you pass, then saves. For authentication or secrets, use Grafana UI directly. If the datasource does not exist, ask for confirmation before creating a new one.",
	updateDatasource,
	mcp.WithTitleAnnotation("Update datasource"),
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

type UpdateDatasourceParams struct {
	UID             string                 `json:"uid" jsonschema:"required,description=The UID of the datasource to update"`
	Name            *string                `json:"name,omitempty" jsonschema:"description=New display name"`
	Type            *string                `json:"type,omitempty" jsonschema:"description=Datasource plugin type (usually leave unchanged after create)"`
	URL             *string                `json:"url,omitempty" jsonschema:"description=Datasource base URL"`
	Access          *string                `json:"access,omitempty" jsonschema:"description=How Grafana reaches the datasource (proxy or direct)"`
	Database        *string                `json:"database,omitempty" jsonschema:"description=Database name when applicable"`
	User            *string                `json:"user,omitempty" jsonschema:"description=Username when applicable"`
	BasicAuth       *bool                  `json:"basicAuth,omitempty" jsonschema:"description=Whether Grafana uses basic auth to the datasource"`
	BasicAuthUser   *string                `json:"basicAuthUser,omitempty" jsonschema:"description=Basic auth username when basic auth is enabled"`
	WithCredentials *bool                  `json:"withCredentials,omitempty" jsonschema:"description=Whether Grafana forwards credentials such as cookies"`
	IsDefault       *bool                  `json:"isDefault,omitempty" jsonschema:"description=Whether this datasource should be the default"`
	JSONData        map[string]interface{} `json:"jsonData,omitempty" jsonschema:"description=Non-secret plugin settings; when set\\, replaces jsonData on the server for this datasource"`
	SecureJSONData  map[string]string      `json:"secureJsonData,omitempty" jsonschema:"description=Secret fields to set or rotate (passwords\\, tokens); only include keys you intend to change"`
}

type UpdateDatasourceResult struct {
	*models.UpdateDataSourceByUIDOKBody
	Health *DatasourceHealthResult `json:"health,omitempty"`
}

func updateDatasource(ctx context.Context, args UpdateDatasourceParams) (*UpdateDatasourceResult, error) {
	// pending add credential violation check
	c := mcpgrafana.GrafanaClientFromContext(ctx)

	current, err := c.Datasources.GetDataSourceByUID(args.UID)
	if err != nil {
		if strings.Contains(err.Error(), "404") {
			return nil, fmt.Errorf("datasource with UID '%s' not found", args.UID)
		}
		return nil, fmt.Errorf("get datasource by uid %s: %w", args.UID, err)
	}

	dsAccess := "proxy"
	if args.Access != nil && *args.Access != "" {
		// use grafana default
		dsAccess = *args.Access
	}
	ds := current.Payload
	cmd := &models.UpdateDataSourceCommand{
		Name:            ds.Name,
		Type:            ds.Type,
		Access:          models.DsAccess(dsAccess),
		URL:             ds.URL,
		Database:        ds.Database,
		User:            ds.User,
		BasicAuth:       ds.BasicAuth,
		BasicAuthUser:   ds.BasicAuthUser,
		WithCredentials: ds.WithCredentials,
		IsDefault:       ds.IsDefault,
		JSONData:        models.JSON(ds.JSONData),
		Version:         ds.Version,
	}

	if args.Name != nil {
		cmd.Name = *args.Name
	}
	if args.Type != nil {
		cmd.Type = *args.Type
	}
	if args.URL != nil {
		cmd.URL = *args.URL
	}
	if args.Access != nil {
		cmd.Access = models.DsAccess(*args.Access)
	}
	if args.Database != nil {
		cmd.Database = *args.Database
	}
	if args.User != nil {
		cmd.User = *args.User
	}
	if args.BasicAuth != nil {
		cmd.BasicAuth = *args.BasicAuth
	}
	if args.BasicAuthUser != nil {
		cmd.BasicAuthUser = *args.BasicAuthUser
	}
	if args.WithCredentials != nil {
		cmd.WithCredentials = *args.WithCredentials
	}
	if args.IsDefault != nil {
		cmd.IsDefault = *args.IsDefault
	}
	if args.JSONData != nil {
		cmd.JSONData = models.JSON(args.JSONData)
	}
	if args.SecureJSONData != nil {
		cmd.SecureJSONData = args.SecureJSONData
	}

	resp, err := c.Datasources.UpdateDataSourceByUID(args.UID, cmd)
	if err != nil {
		return nil, fmt.Errorf("update datasource %s: %w", args.UID, err)
	}

	result := &UpdateDatasourceResult{UpdateDataSourceByUIDOKBody: resp.Payload}

	health, err := checkDatasourceHealth(ctx, CheckDatasourceHealthParams{UID: args.UID})
	if err != nil {
		result.Health = &DatasourceHealthResult{UID: args.UID, Message: fmt.Sprintf("health check failed: %s", err)}
	} else {
		result.Health = health
	}

	return result, nil
}

type CheckDatasourceHealthParams struct {
	UID string `json:"uid" jsonschema:"required,description=The UID of the datasource to health-check"`
}

type DatasourceHealthResult struct {
	UID     string `json:"uid"`
	Message string `json:"message"`
}

func checkDatasourceHealth(ctx context.Context, args CheckDatasourceHealthParams) (*DatasourceHealthResult, error) {
	c := mcpgrafana.GrafanaClientFromContext(ctx)
	resp, err := c.Datasources.CheckDatasourceHealthWithUID(args.UID)
	if err != nil {
		if strings.Contains(err.Error(), "404") {
			return nil, fmt.Errorf("datasource with UID '%s' not found", args.UID)
		}
		return nil, fmt.Errorf("check datasource health %s: %w", args.UID, err)
	}
	return &DatasourceHealthResult{
		UID:     args.UID,
		Message: resp.Payload.Message,
	}, nil
}

type BulkCheckDatasourceHealthParams struct {
	Type string   `json:"type,omitempty" jsonschema:"description=Datasource plugin type to filter by (e.g. 'prometheus'\\, 'loki'). If omitted and uids is empty\\, all datasources are checked."`
	UIDs []string `json:"uids,omitempty" jsonschema:"description=Explicit list of datasource UIDs to check. When provided\\, the type filter is ignored."`
}

type DatasourceHealthCheckResult struct {
	UID     string `json:"uid"`
	Name    string `json:"name"`
	Type    string `json:"type"`
	Message string `json:"message,omitempty"`
	Error   string `json:"error,omitempty"`
}

type BulkDatasourceHealthResult struct {
	Results   []DatasourceHealthCheckResult `json:"results"`
	Total     int                           `json:"total"`
	Healthy   int                           `json:"healthy"`
	Unhealthy int                           `json:"unhealthy"`
}

func checkDatasourcesHealth(ctx context.Context, args BulkCheckDatasourceHealthParams) (*BulkDatasourceHealthResult, error) {
	c := mcpgrafana.GrafanaClientFromContext(ctx)

	resp, err := c.Datasources.GetDataSources()
	if err != nil {
		return nil, fmt.Errorf("list datasources: %w", err)
	}

	var targets models.DataSourceList
	if len(args.UIDs) > 0 {
		uidSet := make(map[string]bool, len(args.UIDs))
		for _, u := range args.UIDs {
			uidSet[u] = true
		}
		for _, ds := range resp.Payload {
			if uidSet[ds.UID] {
				targets = append(targets, ds)
			}
		}
	} else {
		targets = filterDatasources(resp.Payload, args.Type)
	}

	results := make([]DatasourceHealthCheckResult, len(targets))
	var wg sync.WaitGroup
	for i, ds := range targets {
		wg.Add(1)
		go func(i int, ds *models.DataSourceListItemDTO) {
			defer wg.Done()
			r := DatasourceHealthCheckResult{UID: ds.UID, Name: ds.Name, Type: ds.Type}
			health, err := checkDatasourceHealth(ctx, CheckDatasourceHealthParams{UID: ds.UID})
			if err != nil {
				r.Error = err.Error()
			} else {
				r.Message = health.Message
			}
			results[i] = r
		}(i, ds)
	}
	wg.Wait()

	healthy, unhealthy := 0, 0
	for _, r := range results {
		if r.Error != "" {
			unhealthy++
		} else {
			healthy++
		}
	}

	return &BulkDatasourceHealthResult{
		Results:   results,
		Total:     len(results),
		Healthy:   healthy,
		Unhealthy: unhealthy,
	}, nil
}

var CheckDatasourcesHealth = mcpgrafana.MustTool(
	"check_datasources_health",
	"Checks the health of one or multiple Grafana datasources in parallel. Filter by type (e.g. 'prometheus') to check all datasources of that type, supply a list of UIDs to check specific ones, or omit both to check every configured datasource. Returns per-datasource results and a healthy/unhealthy summary.",
	checkDatasourcesHealth,
	mcp.WithTitleAnnotation("Check datasources health"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
)

func AddDatasourceTools(mcp *server.MCPServer) {
	ListDatasources.Register(mcp)
	GetDatasource.Register(mcp)
	UpdateDatasource.Register(mcp)
	CheckDatasourcesHealth.Register(mcp)
}
