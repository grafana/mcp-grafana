package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"

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

// CreateDatasourceSpec defines one datasource within a creation request.
// All specs in a single call share the same plugin type (set on CreateDatasourceParams).
type CreateDatasourceSpec struct {
	Name            string         `json:"name" jsonschema:"required,description=Datasource display name"`
	URL             string         `json:"url,omitempty" jsonschema:"description=Datasource base URL when required by the plugin"`
	Access          string         `json:"access,omitempty" jsonschema:"description=How Grafana should access the datasource (proxy or direct)"`
	Database        string         `json:"database,omitempty" jsonschema:"description=Optional database name"`
	BasicAuth       bool           `json:"basicAuth,omitempty" jsonschema:"description=Whether Grafana should use basic auth"`
	WithCredentials bool           `json:"withCredentials,omitempty" jsonschema:"description=Whether Grafana should forward credentials such as cookies"`
	IsDefault       bool           `json:"isDefault,omitempty" jsonschema:"description=Whether this should become the default datasource"`
	Fields          map[string]any `json:"fields,omitempty" jsonschema:"description=Datasource field values keyed by field key from the schema"`
}

type CreateDatasourceParams struct {
	// Type is required on both calls: it identifies the plugin for schema guidance
	// and is applied to every spec in Datasources during creation.
	Type string `json:"type" jsonschema:"required,description=Datasource plugin type (e.g. prometheus). Required on every call."`

	// Datasources is omitted on the first (schema-guidance) call and populated on the second.
	// One entry creates a single datasource; multiple entries of the same type are created concurrently.
	Datasources []CreateDatasourceSpec `json:"datasources,omitempty" jsonschema:"description=One or more datasource configs to create. Omit on the first call to get the field schema. All entries are created concurrently and must share the type set above."`
}

type CreateDatasourceResult struct {
	Message   string                  `json:"message,omitempty"`
	ID        int64                   `json:"id,omitempty"`
	UID       string                  `json:"uid,omitempty"`
	Name      string                  `json:"name,omitempty"`
	ConfigURL string                  `json:"configURL,omitempty"`
	NextSteps string                  `json:"nextSteps,omitempty"`
	Health    *DatasourceHealthResult `json:"health,omitempty"`
	Error     string                  `json:"error,omitempty"`
}

type BulkCreateDatasourceResult struct {
	Results   []CreateDatasourceResult `json:"results"`
	Total     int                      `json:"total"`
	Succeeded int                      `json:"succeeded"`
	Failed    int                      `json:"failed"`
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
	commonFields := datasourceschemas.CommonDatasourceFields()
	lookup := make(map[string]datasourceschemas.DsSchemaField, len(commonFields)+len(schema.Fields))
	for _, f := range commonFields {
		lookup[datasourceschemas.SchemaFieldInputKey(f)] = f
	}
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
		} else if f.Section != "" {
			section, ok := jsonData[f.Section].(map[string]any)
			if !ok {
				section = make(map[string]any)
				jsonData[f.Section] = section
			}
			section[f.Key] = v
		} else {
			jsonData[f.Key] = v
		}
	}
	return jsonData
}

// executeDatasourceCreate performs the actual API call for one datasource spec and
// returns a populated CreateDatasourceResult. It is shared by the single and bulk paths.
func executeDatasourceCreate(ctx context.Context, c *mcpgrafana.GrafanaClient, dsType string, spec CreateDatasourceSpec, grafanaURL string) *CreateDatasourceResult {
	result := &CreateDatasourceResult{Name: spec.Name}

	schema, err := datasourceschemas.LoadDatasourceSchema(dsType)
	if err != nil {
		result.Error = fmt.Sprintf("load schema: %s", err)
		return result
	}

	dsAccess := spec.Access
	if dsAccess == "" {
		dsAccess = "proxy"
	}

	body := &models.AddDataSourceCommand{
		Name:            spec.Name,
		Type:            dsType,
		URL:             spec.URL,
		Access:          models.DsAccess(dsAccess),
		Database:        spec.Database,
		BasicAuth:       spec.BasicAuth,
		IsDefault:       spec.IsDefault,
		WithCredentials: spec.WithCredentials,
	}
	if schema != nil {
		body.JSONData = models.JSON(applyFields(body, schema, spec.Fields))
	}

	resp, err := c.Datasources.AddDataSourceWithParams(
		datasources.NewAddDataSourceParamsWithContext(ctx).WithBody(body),
	)
	if err != nil {
		result.Error = fmt.Sprintf("create datasource: %s", err)
		return result
	}

	p := resp.Payload
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

		if grafanaURL != "" {
			result.ConfigURL = fmt.Sprintf("%s/connections/datasources/edit/%s", grafanaURL, result.UID)
			result.NextSteps = fmt.Sprintf("Visit the datasource configuration page to finish setting it up: %s", result.ConfigURL)
		}
	}

	return result
}

func createDatasource(ctx context.Context, args CreateDatasourceParams) (*mcp.CallToolResult, error) {
	// Phase 1: no datasources yet — return field guidance for the given type so the
	// caller knows exactly which fields to populate before the creation call.
	if len(args.Datasources) == 0 {
		schema, err := datasourceschemas.LoadDatasourceSchema(args.Type)
		if err != nil {
			return nil, err
		}
		if schema != nil {
			text, _ := json.Marshal(datasourceschemas.BuildSchemaGuidance(schema, "create_datasource"))
			return mcp.NewToolResultText(string(text)), nil
		}
		text, _ := json.Marshal(noSchemaGuidance(args.Type))
		return mcp.NewToolResultText(string(text)), nil
	}

	// Phase 2: datasources provided. Since all specs share args.Type, one schema
	// check is enough — if any spec is missing fields for a type that has a schema,
	// return guidance before creating anything.
	schema, err := datasourceschemas.LoadDatasourceSchema(args.Type)
	if err != nil {
		return nil, err
	}
	if schema != nil {
		needsGuidance := false
		for _, spec := range args.Datasources {
			if spec.Fields == nil {
				needsGuidance = true
				break
			}
		}
		if needsGuidance {
			text, _ := json.Marshal(datasourceschemas.BuildSchemaGuidance(schema, "create_datasource"))
			return mcp.NewToolResultText(string(text)), nil
		}
	}

	// All specs are ready — create concurrently.
	return createDatasourcesInBulk(ctx, args.Type, args.Datasources)
}

func createDatasourcesInBulk(ctx context.Context, dsType string, specs []CreateDatasourceSpec) (*mcp.CallToolResult, error) {
	c := mcpgrafana.GrafanaClientFromContext(ctx)
	grafanaURL := c.PublicURL
	if grafanaURL == "" {
		grafanaURL = mcpgrafana.GrafanaConfigFromContext(ctx).URL
	}

	results := make([]CreateDatasourceResult, len(specs))
	sem := make(chan struct{}, 5)
	var wg sync.WaitGroup

	for i, spec := range specs {
		wg.Add(1)
		go func(i int, spec CreateDatasourceSpec) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			results[i] = *executeDatasourceCreate(ctx, c, dsType, spec, grafanaURL)
		}(i, spec)
	}
	wg.Wait()

	succeeded, failed := 0, 0
	for _, r := range results {
		if r.Error != "" {
			failed++
		} else {
			succeeded++
		}
	}

	bulkResult := BulkCreateDatasourceResult{
		Results:   results,
		Total:     len(specs),
		Succeeded: succeeded,
		Failed:    failed,
	}

	b, err := json.Marshal(bulkResult)
	if err != nil {
		return nil, fmt.Errorf("marshal bulk result: %w", err)
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
	"Create one or more datasources. If type is ambiguous, call search_plugin_information first; install the plugin if needed. "+
		"IMPORTANT: always call this tool twice for single-datasource creation. "+
		"First call: provide only the type — the tool returns a field schema. "+
		"After receiving the schema, you MUST ask the user for every required non-sensitive field value explicitly; do not infer or use defaults without user confirmation. Never ask for sensitive or secure fields (e.g. passwords, tokens, secret keys) — direct the user to enter those in the Grafana UI instead. "+
		"Second call (single): provide type plus the fields map populated with values confirmed by the user. "+
		"Second call (multiple, e.g. one datasource per region): provide the 'datasources' array where each entry has name, type, and fields — all entries are created concurrently in a single call. "+
		"Never handle credentials — remind the user to rotate any detected. Returns UID, health check, and config page links.",
	createDatasource,
	mcp.WithTitleAnnotation("Create datasource"),
	mcp.WithIdempotentHintAnnotation(false),
	mcp.WithReadOnlyHintAnnotation(false),
)

var UpdateDatasource = mcpgrafana.MustTool(
	"update_datasource",
	"Update non-secret datasource fields by UID. Omitted fields are preserved. For secrets, direct the user to the Grafana UI.",
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
	UID       string                 `json:"uid" jsonschema:"required,description=UID of the datasource to update"`
	Name      *string                `json:"name,omitempty" jsonschema:"description=Display name"`
	URL       *string                `json:"url,omitempty" jsonschema:"description=Base URL"`
	Access    *string                `json:"access,omitempty" jsonschema:"description=proxy or direct"`
	Database  *string                `json:"database,omitempty" jsonschema:"description=Database name"`
	BasicAuth *bool                  `json:"basicAuth,omitempty" jsonschema:"description=Enable basic auth"`
	IsDefault *bool                  `json:"isDefault,omitempty" jsonschema:"description=Make this the default datasource"`
	JSONData  map[string]interface{} `json:"jsonData,omitempty" jsonschema:"description=Non-secret plugin settings; replaces existing jsonData when set"`
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
	Type   string   `json:"type,omitempty" jsonschema:"description=Plugin type filter; omit to check all"`
	UIDs   []string `json:"uids,omitempty" jsonschema:"description=UIDs to check"`
	Offset int      `json:"offset,omitempty" jsonschema:"default=0,description=Number to skip for pagination"`
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
	"Check datasource health. Filter by type or UIDs; omit both to check all.",
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
