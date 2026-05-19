package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"

	"github.com/invopop/jsonschema"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/grafana/grafana-openapi-client-go/client/datasources"
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

// datasourceJSONData holds plugin-specific non-secret settings for a datasource.
// Its schema description is sourced from datasourceJSONDataSchema — swap that
// function body for an API fetch when the datasource settings API is available.
type datasourceJSONData map[string]interface{}

func (datasourceJSONData) JSONSchema() *jsonschema.Schema {
	return datasourceJSONDataSchema()
}

// datasourceJSONDataSchema returns the JSON schema for the jsonData field.
// TODO: Replace with a fetch from the Grafana datasource settings API when available.
func datasourceJSONDataSchema() *jsonschema.Schema {
	return &jsonschema.Schema{
		Type:        "object",
		Description: "Plugin-specific non-secret settings. Keys vary by datasource type; ask the user or consult the plugin docs.",
		Extras:      map[string]any{},
	}
}

type CreateDatasourceParams struct {
	Name            string             `json:"name" jsonschema:"required,description=Datasource display name"`
	Type            string             `json:"type" jsonschema:"required,description=Grafana datasource plugin type\\, for example prometheus"`
	URL             string             `json:"url,omitempty" jsonschema:"description=Datasource base URL when required by the plugin"`
	Access          string             `json:"access,omitempty" jsonschema:"description=How Grafana should access the datasource (proxy or direct)"`
	Database        string             `json:"database,omitempty" jsonschema:"description=Optional database name"`
	User            string             `json:"user,omitempty" jsonschema:"description=Optional username"`
	BasicAuth       bool               `json:"basicAuth,omitempty" jsonschema:"description=Whether Grafana should use basic auth"`
	WithCredentials bool               `json:"withCredentials,omitempty" jsonschema:"description=Whether Grafana should forward credentials such as cookies"`
	IsDefault       bool               `json:"isDefault,omitempty" jsonschema:"description=Whether this should become the default datasource"`
	JSONData        datasourceJSONData `json:"jsonData,omitempty"`
}

type CreateDatasourceResult struct {
	Message   string                  `json:"message"`
	ID        int64                   `json:"id"`
	UID       string                  `json:"uid"`
	Name      string                  `json:"name"`
	NextSteps string                  `json:"nextSteps,omitempty"`
	Health    *DatasourceHealthResult `json:"health,omitempty"`
}

func createDatasource(ctx context.Context, args CreateDatasourceParams) (*mcp.CallToolResult, error) {
	dsAccess := args.Access
	if dsAccess == "" {
		// use grafana default
		dsAccess = "proxy"
	}
	c := mcpgrafana.GrafanaClientFromContext(ctx)
	body := &models.AddDataSourceCommand{
		Name:            args.Name,
		Type:            args.Type,
		URL:             args.URL,
		Access:          models.DsAccess(dsAccess),
		Database:        args.Database,
		BasicAuth:       args.BasicAuth,
		IsDefault:       args.IsDefault,
		JSONData:        models.JSON(args.JSONData),
		WithCredentials: args.WithCredentials,
	}
	resp, err := c.Datasources.AddDataSourceWithParams(
		datasources.NewAddDataSourceParamsWithContext(ctx).WithBody(body),
	)
	if err != nil {
		return nil, fmt.Errorf("create datasource: %w", err)
	}
	p := resp.Payload
	result := &CreateDatasourceResult{}

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
		health, err := checkDatasourceHealth(ctx, CheckDatasourceHealthParams{UID: result.UID})
		if err != nil {
			result.Health = &DatasourceHealthResult{UID: result.UID, Message: fmt.Sprintf("health check failed: %s", err)}
		} else {
			result.Health = health
		}

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
	"Create a datasource. If type is ambiguous, call search_plugin_information first; install the plugin if needed. Returns UID, health check, and a config page link. Never handle credentials — remind the user to rotate any detected.",
	createDatasource,
	mcp.WithTitleAnnotation("Create datasource"),
	mcp.WithIdempotentHintAnnotation(false),
	mcp.WithReadOnlyHintAnnotation(false),
)

var UpdateDatasource = mcpgrafana.MustTool(
	"update_datasource",
	"Update non-secret datasource fields by UID. Omitted fields are preserved. Returns a health check. For secrets, instruct the user to use the Grafana UI. Confirm before creating if the datasource doesn't exist.",
	updateDatasource,
	mcp.WithTitleAnnotation("Update datasource"),
	mcp.WithIdempotentHintAnnotation(true),
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

type UpdateDatasourceParams struct {
	UID             string                 `json:"uid" jsonschema:"required,description=The UID of the datasource to update"`
	Name            *string                `json:"name,omitempty" jsonschema:"description=New display name"`
	Type            *string                `json:"type,omitempty" jsonschema:"description=Datasource plugin type (usually leave unchanged after create)"`
	URL             *string                `json:"url,omitempty" jsonschema:"description=Datasource base URL"`
	Access          *string                `json:"access,omitempty" jsonschema:"description=How Grafana reaches the datasource (proxy or direct)"`
	Database        *string                `json:"database,omitempty" jsonschema:"description=Database name when applicable"`
	BasicAuth       *bool                  `json:"basicAuth,omitempty" jsonschema:"description=Whether Grafana uses basic auth to the datasource"`
	WithCredentials *bool                  `json:"withCredentials,omitempty" jsonschema:"description=Whether Grafana forwards credentials such as cookies"`
	IsDefault       *bool                  `json:"isDefault,omitempty" jsonschema:"description=Whether this datasource should be the default"`
	JSONData        map[string]interface{} `json:"jsonData,omitempty" jsonschema:"description=Non-secret plugin settings; when set\\, replaces jsonData on the server for this datasource"`
}

type UpdateDatasourceResult struct {
	Message string                  `json:"message,omitempty"`
	Health  *DatasourceHealthResult `json:"health,omitempty"`
}

func updateDatasource(ctx context.Context, args UpdateDatasourceParams) (*UpdateDatasourceResult, error) {
	// pending add credential violation check
	c := mcpgrafana.GrafanaClientFromContext(ctx)

	current, err := c.Datasources.GetDataSourceByUIDWithParams(
		datasources.NewGetDataSourceByUIDParamsWithContext(ctx).WithUID(args.UID),
	)
	if err != nil {
		if strings.Contains(err.Error(), "404") {
			return nil, fmt.Errorf("datasource with UID '%s' not found", args.UID)
		}
		return nil, fmt.Errorf("get datasource by uid %s: %w", args.UID, err)
	}

	ds := current.Payload
	cmd := &models.UpdateDataSourceCommand{
		Name:            ds.Name,
		Type:            ds.Type,
		Access:          ds.Access,
		URL:             ds.URL,
		User:            ds.User,
		Database:        ds.Database,
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
	if args.BasicAuth != nil {
		cmd.BasicAuth = *args.BasicAuth
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

	resp, err := c.Datasources.UpdateDataSourceByUIDWithParams(
		datasources.NewUpdateDataSourceByUIDParamsWithContext(ctx).WithUID(args.UID).WithBody(cmd),
	)
	if err != nil {
		return nil, fmt.Errorf("update datasource %s: %w", args.UID, err)
	}

	result := &UpdateDatasourceResult{}
	if resp.Payload.Message != nil {
		result.Message = *resp.Payload.Message
	}

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
	Status  string `json:"status,omitempty"` // "OK", "ERROR", or "UNKNOWN"
	Message string `json:"message"`
}

type datasourcesClient struct {
	httpClient *http.Client
	baseURL    string
}

func newDatasourcesClient(ctx context.Context) (*datasourcesClient, error) {
	cfg := mcpgrafana.GrafanaConfigFromContext(ctx)
	baseURL := strings.TrimRight(cfg.URL, "/") + "/api/datasources"

	transport, err := mcpgrafana.BuildTransport(&cfg, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create transport: %w", err)
	}
	httpClient := &http.Client{Transport: transport}

	return &datasourcesClient{
		httpClient: httpClient,
		baseURL:    baseURL,
	}, nil
}

func checkDatasourceHealth(ctx context.Context, args CheckDatasourceHealthParams) (*DatasourceHealthResult, error) {
	client, err := newDatasourcesClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("check datasource health %s: %w", args.UID, err)
	}
	endpoint := client.baseURL + "/uid/" + args.UID + "/health"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request %s: %w", args.UID, err)
	}

	resp, err := client.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("check datasource health %s: %w", args.UID, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("check datasource health %s: HTTP %d: %s", args.UID, resp.StatusCode, strings.TrimSpace(string(body)))
	}

	result := &DatasourceHealthResult{UID: args.UID}
	if err := json.NewDecoder(resp.Body).Decode(result); err != nil {
		return nil, fmt.Errorf("check datasource health %s: %w", args.UID, err)
	}
	return result, nil
}

type BulkCheckDatasourceHealthParams struct {
	Type   string   `json:"type,omitempty" jsonschema:"description=Plugin type to filter (e.g. prometheus). Omit to check all."`
	UIDs   []string `json:"uids,omitempty" jsonschema:"description=UIDs to check. Takes priority over type when set."`
	Offset int      `json:"offset,omitempty" jsonschema:"default=0,description=Number to skip for pagination."`
}

type DatasourceHealthCheckResult struct {
	UID     string `json:"uid"`
	Name    string `json:"name"`
	Type    string `json:"type"`
	Status  string `json:"status,omitempty"` // "OK", "ERROR", or "UNKNOWN"
	Message string `json:"message,omitempty"`
	Error   string `json:"error,omitempty"`
}

type BulkDatasourceHealthResult struct {
	Results   []DatasourceHealthCheckResult `json:"results"`
	Total     int                           `json:"total"`   // Total matching datasources before pagination
	Checked   int                           `json:"checked"` // Number of datasources health-checked in this page
	Healthy   int                           `json:"healthy"`
	Unhealthy int                           `json:"unhealthy"`
	HasMore   bool                          `json:"hasMore"` // Whether more datasources exist beyond this page
}

func checkDatasourcesHealth(ctx context.Context, args BulkCheckDatasourceHealthParams) (*BulkDatasourceHealthResult, error) {
	c := mcpgrafana.GrafanaClientFromContext(ctx)

	resp, err := c.Datasources.GetDataSourcesWithParams(
		datasources.NewGetDataSourcesParamsWithContext(ctx),
	)
	if err != nil {
		return nil, fmt.Errorf("list datasources: %w", err)
	}

	var all models.DataSourceList
	if len(args.UIDs) > 0 {
		uidSet := make(map[string]bool, len(args.UIDs))
		for _, u := range args.UIDs {
			uidSet[u] = true
		}
		for _, ds := range resp.Payload {
			if uidSet[ds.UID] {
				all = append(all, ds)
			}
		}
	} else {
		all = filterDatasources(resp.Payload, args.Type)
	}

	limit := 10

	offset := args.Offset
	if offset < 0 {
		offset = 0
	}

	var targets models.DataSourceList
	if offset < len(all) {
		end := offset + limit
		if end > len(all) {
			end = len(all)
		}
		targets = all[offset:end]
	}

	results := make([]DatasourceHealthCheckResult, len(targets))
	sem := make(chan struct{}, 10)
	var wg sync.WaitGroup
	for i, ds := range targets {
		wg.Add(1)
		go func(i int, ds *models.DataSourceListItemDTO) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			r := DatasourceHealthCheckResult{UID: ds.UID, Name: ds.Name, Type: ds.Type}
			health, err := checkDatasourceHealth(ctx, CheckDatasourceHealthParams{UID: ds.UID})
			if err != nil {
				r.Error = err.Error()
			} else {
				r.Status = health.Status
				r.Message = health.Message
			}
			results[i] = r
		}(i, ds)
	}
	wg.Wait()

	healthy, unhealthy := 0, 0
	for _, r := range results {
		if r.Error != "" || r.Status != "OK" {
			unhealthy++
		} else {
			healthy++
		}
	}

	return &BulkDatasourceHealthResult{
		Results:   results,
		Total:     len(all),
		Checked:   len(results),
		Healthy:   healthy,
		Unhealthy: unhealthy,
		HasMore:   offset+len(results) < len(all),
	}, nil
}

var CheckDatasourcesHealth = mcpgrafana.MustTool(
	"check_datasources_health",
	"Check datasource health in parallel. Filter by type or provide UIDs; omit both to check all. Returns per-datasource status and a summary.",
	checkDatasourcesHealth,
	mcp.WithTitleAnnotation("Check datasources health"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
)

func AddDatasourceTools(mcp *server.MCPServer, enableWriteTools bool) {
	ListDatasources.Register(mcp)
	GetDatasource.Register(mcp)
	CheckDatasourcesHealth.Register(mcp)
	if enableWriteTools {
		// TODO: since these tools are more for set up / admin, when merging to main, we should move them to the admin toolset
		CreateDatasource.Register(mcp)
		UpdateDatasource.Register(mcp)
	}
}
