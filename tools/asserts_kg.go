package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"time"

	mcpgrafana "github.com/grafana/mcp-grafana"
	"github.com/mark3labs/mcp-go/mcp"
)

// --- get_graph_schema ---

type GetGraphSchemaParams struct{}

type entityTypeDTO struct {
	EntityType           string              `json:"entityType"`
	Name                 string              `json:"name"`
	Properties           []entityPropertyDTO `json:"properties"`
	ConnectedEntityTypes []string            `json:"connectedEntityTypes"`
	Active               bool                `json:"active"`
}

type entityPropertyDTO struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

type entityTypesResponse struct {
	Entities []entityTypeDTO `json:"entities"`
}

type schemaEntityType struct {
	Type           string   `json:"type"`
	Properties     []string `json:"properties"`
	ConnectedTypes []string `json:"connectedTypes"`
}

func getGraphSchema(ctx context.Context, _ GetGraphSchemaParams) (string, error) {
	client, err := newAssertsClient(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to create Asserts client: %w", err)
	}

	cfg := mcpgrafana.GrafanaConfigFromContext(ctx)
	cacheKey := tenantCacheKey(client.baseURL, cfg.OrgID)

	if cached, ok := graphSchemaCache.get(cacheKey); ok {
		return cached, nil
	}

	data, err := client.fetchAssertsDataGet(ctx, "/v1/entity_type", nil)
	if err != nil {
		return "", fmt.Errorf("failed to fetch entity types: %w", err)
	}

	var resp entityTypesResponse
	if err := json.Unmarshal([]byte(data), &resp); err != nil {
		return "", fmt.Errorf("failed to parse entity types: %w", err)
	}

	schema := make([]schemaEntityType, 0, len(resp.Entities))
	for _, et := range resp.Entities {
		if !et.Active {
			continue
		}
		props := make([]string, 0, len(et.Properties))
		for _, p := range et.Properties {
			props = append(props, p.Name)
		}
		schema = append(schema, schemaEntityType{
			Type:           et.EntityType,
			Properties:     props,
			ConnectedTypes: et.ConnectedEntityTypes,
		})
	}

	result, err := json.Marshal(map[string]any{"entityTypes": schema})
	if err != nil {
		return "", fmt.Errorf("failed to marshal schema: %w", err)
	}

	out := string(result)
	graphSchemaCache.set(cacheKey, out)
	return out, nil
}

var GetGraphSchema = mcpgrafana.MustTool(
	"get_graph_schema",
	"Get the Knowledge Graph schema: entity types, their properties, and which types they connect to. Call this first to understand the graph structure before searching or traversing entities.",
	getGraphSchema,
	mcp.WithTitleAnnotation("Get KG schema"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
)

// --- search_entities ---

type SearchEntitiesParams struct {
	Mode          string `json:"mode,omitempty" jsonschema:"description=Search mode: 'search' (default) for structured search via /v1/search\\, 'list' for paginated listing via public API\\, 'count' for entity type counts\\, 'semantic' for natural language search via vector similarity,enum=search,enum=list,enum=count,enum=semantic"`
	EntityType    string `json:"entityType,omitempty" jsonschema:"description=Entity type (e.g. Service\\, Node\\, Pod). Required for search/list modes. Use get_graph_schema to see available types."`
	SearchText    string `json:"searchText,omitempty" jsonschema:"description=Text to search for. In search mode: matches entity names (CONTAINS). In semantic mode: natural language query (e.g. 'the database handling payment orders')."`
	Env           string `json:"env,omitempty" jsonschema:"description=Filter by environment. In list mode supports RHS filter syntax (e.g. eq:production)."`
	Site          string `json:"site,omitempty" jsonschema:"description=Filter by site"`
	Namespace     string `json:"namespace,omitempty" jsonschema:"description=Filter by namespace. In list mode supports RHS filter syntax."`
	HasAssertions bool   `json:"hasAssertions,omitempty" jsonschema:"description=Only return entities with active assertions (search mode only)"`
	Limit         int    `json:"limit,omitempty" jsonschema:"description=Max results to return (default 25 for list\\, 10 for semantic)"`
	Offset        int    `json:"offset,omitempty" jsonschema:"description=Pagination offset (list mode only)"`
	StartTime     time.Time `json:"startTime,omitempty" jsonschema:"description=Start time in RFC3339 format (required for search and count modes)"`
	EndTime       time.Time `json:"endTime,omitempty" jsonschema:"description=End time in RFC3339 format (required for search and count modes)"`
}

type searchRequestDTO struct {
	TimeCriteria   timeCriteriaDTO    `json:"timeCriteria"`
	ScopeCriteria  *scopeCriteriaDTO  `json:"scopeCriteria,omitempty"`
	FilterCriteria []entityMatcherDTO `json:"filterCriteria"`
	PageNum        int                `json:"pageNum"`
}

type timeCriteriaDTO struct {
	Start int64 `json:"start"`
	End   int64 `json:"end"`
}

type scopeCriteriaDTO struct {
	NameAndValues map[string][]string `json:"nameAndValues"`
}

type entityMatcherDTO struct {
	EntityType      string               `json:"entityType"`
	PropertyMatchers []propertyMatcherDTO `json:"propertyMatchers,omitempty"`
	HavingAssertion bool                 `json:"havingAssertion"`
}

type propertyMatcherDTO struct {
	Name  string `json:"name"`
	Value any    `json:"value"`
	Op    string `json:"op"`
}

// searchResponseDTO matches the shape of POST /v1/search when type=graph.
type searchResponseDTO struct {
	Type string `json:"type"`
	Data struct {
		PageNum                  int                    `json:"pageNum"`
		LastPage                 bool                   `json:"lastPage"`
		SearchResultsMaxLimitHit bool                   `json:"searchResultsMaxLimitHit"`
		Entities                 []graphEntityResponse  `json:"entities"`
		Edges                    []searchEdgeDTO        `json:"edges"`
	} `json:"data"`
}

type searchEdgeDTO struct {
	Source      int64  `json:"source"`
	Destination int64  `json:"destination"`
	Type        string `json:"type"`
}

type entityCountRequestDTO struct {
	TimeCriteria  timeCriteriaDTO   `json:"timeCriteria"`
	ScopeCriteria *scopeCriteriaDTO `json:"scopeCriteria,omitempty"`
}

func searchEntities(ctx context.Context, args SearchEntitiesParams) (string, error) {
	mode := args.Mode
	if mode == "" {
		mode = "search"
	}

	switch mode {
	case "search":
		return searchEntitiesDefault(ctx, args)
	case "list":
		return searchEntitiesList(ctx, args)
	case "count":
		return searchEntitiesCount(ctx, args)
	case "semantic":
		return searchEntitiesSemantic(ctx, args)
	default:
		return "", fmt.Errorf("unknown search mode: %q (valid: search, list, count, semantic)", mode)
	}
}

func searchEntitiesDefault(ctx context.Context, args SearchEntitiesParams) (string, error) {
	client, err := newAssertsClient(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to create Asserts client: %w", err)
	}

	matcher := entityMatcherDTO{
		EntityType:      args.EntityType,
		HavingAssertion: args.HasAssertions,
	}
	if args.SearchText != "" {
		matcher.PropertyMatchers = append(matcher.PropertyMatchers, propertyMatcherDTO{
			Name:  "name",
			Value: args.SearchText,
			Op:    "CONTAINS",
		})
	}

	reqBody := searchRequestDTO{
		TimeCriteria: timeCriteriaDTO{
			Start: args.StartTime.UnixMilli(),
			End:   args.EndTime.UnixMilli(),
		},
		FilterCriteria: []entityMatcherDTO{matcher},
	}

	scopeVals := make(map[string][]string)
	if args.Env != "" {
		scopeVals["env"] = []string{args.Env}
	}
	if args.Site != "" {
		scopeVals["site"] = []string{args.Site}
	}
	if args.Namespace != "" {
		scopeVals["namespace"] = []string{args.Namespace}
	}
	if len(scopeVals) > 0 {
		reqBody.ScopeCriteria = &scopeCriteriaDTO{NameAndValues: scopeVals}
	}

	data, err := client.fetchAssertsData(ctx, "/v1/search", "POST", reqBody)
	if err != nil {
		return "", fmt.Errorf("failed to search entities: %w", err)
	}

	var resp searchResponseDTO
	if err := json.Unmarshal([]byte(data), &resp); err != nil {
		return "", fmt.Errorf("failed to parse search response: %w", err)
	}

	slimEntities := make([]slimEntity, 0, len(resp.Data.Entities))
	for i := range resp.Data.Entities {
		slimEntities = append(slimEntities, resp.Data.Entities[i].toSlim())
	}

	result, err := json.Marshal(map[string]any{
		"mode":     "search",
		"entities": slimEntities,
		"edges":    resp.Data.Edges,
		"pageNum":  resp.Data.PageNum,
		"lastPage": resp.Data.LastPage,
		"limitHit": resp.Data.SearchResultsMaxLimitHit,
	})
	if err != nil {
		return "", fmt.Errorf("failed to marshal search results: %w", err)
	}
	return string(result), nil
}

func searchEntitiesList(ctx context.Context, args SearchEntitiesParams) (string, error) {
	client, err := newAssertsClient(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to create Asserts client: %w", err)
	}

	params := url.Values{}
	if args.Env != "" {
		params.Set("scope.env", args.Env)
	}
	if args.Site != "" {
		params.Set("scope.site", args.Site)
	}
	if args.Namespace != "" {
		params.Set("scope.namespace", args.Namespace)
	}
	if args.SearchText != "" {
		params.Set("name", args.SearchText)
	}
	limit := args.Limit
	if limit <= 0 {
		limit = 25
	}
	if limit > 100 {
		limit = 100
	}
	params.Set("pagination.limit", fmt.Sprintf("%d", limit))
	if args.Offset > 0 {
		params.Set("pagination.offset", fmt.Sprintf("%d", args.Offset))
	}

	path := fmt.Sprintf("/public/v1/entities/%s", url.PathEscape(args.EntityType))
	data, err := client.fetchAssertsDataGet(ctx, path, params)
	if err != nil {
		return "", fmt.Errorf("failed to list entities: %w", err)
	}

	var page struct {
		Items      []entitySummaryResponse `json:"items"`
		Pagination struct {
			Limit  int `json:"limit"`
			Offset int `json:"offset"`
		} `json:"pagination"`
	}
	if err := json.Unmarshal([]byte(data), &page); err != nil {
		return "", fmt.Errorf("failed to parse list response: %w", err)
	}

	slimItems := make([]slimEntity, 0, len(page.Items))
	for i := range page.Items {
		slimItems = append(slimItems, page.Items[i].toSlim())
	}

	result, err := json.Marshal(map[string]any{
		"mode":       "list",
		"entities":   slimItems,
		"pagination": page.Pagination,
	})
	if err != nil {
		return "", fmt.Errorf("failed to marshal entities: %w", err)
	}
	return string(result), nil
}

func searchEntitiesCount(ctx context.Context, args SearchEntitiesParams) (string, error) {
	client, err := newAssertsClient(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to create Asserts client: %w", err)
	}

	reqBody := entityCountRequestDTO{
		TimeCriteria: timeCriteriaDTO{
			Start: args.StartTime.UnixMilli(),
			End:   args.EndTime.UnixMilli(),
		},
	}

	scopeVals := make(map[string][]string)
	if args.Env != "" {
		scopeVals["env"] = []string{args.Env}
	}
	if args.Site != "" {
		scopeVals["site"] = []string{args.Site}
	}
	if args.Namespace != "" {
		scopeVals["namespace"] = []string{args.Namespace}
	}
	if len(scopeVals) > 0 {
		reqBody.ScopeCriteria = &scopeCriteriaDTO{NameAndValues: scopeVals}
	}

	data, err := client.fetchAssertsData(ctx, "/v1/entity_type/count", "POST", reqBody)
	if err != nil {
		return "", fmt.Errorf("failed to count entities: %w", err)
	}

	return data, nil
}

func searchEntitiesSemantic(ctx context.Context, args SearchEntitiesParams) (string, error) {
	limit := args.Limit
	if limit <= 0 {
		limit = 10
	}

	semanticArgs := FindEntitiesSemanticParams{
		Query:     args.SearchText,
		Limit:     limit,
		Env:       args.Env,
		Site:      args.Site,
		Namespace: args.Namespace,
	}
	return findEntitiesSemantic(ctx, semanticArgs)
}

var SearchEntities = mcpgrafana.MustTool(
	"search_entities",
	"Search the Knowledge Graph for entities. Supports four modes: 'search' (default) for structured search by type/name/scope, 'list' for paginated entity listing, 'count' for entity type counts, 'semantic' for natural language search via vector similarity. Use get_graph_schema first to understand available entity types.",
	searchEntities,
	mcp.WithTitleAnnotation("Search KG entities"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
)

// --- get_entity ---

type GetEntityParams struct {
	EntityType string `json:"entityType" jsonschema:"required,description=Entity type (e.g. Service\\, Node\\, Pod)"`
	EntityName string `json:"entityName" jsonschema:"required,description=Entity name"`
	Env        string `json:"env,omitempty" jsonschema:"description=Environment of the entity"`
	Site       string `json:"site,omitempty" jsonschema:"description=Site of the entity"`
	Namespace  string `json:"namespace,omitempty" jsonschema:"description=Namespace of the entity"`
	Detailed   bool   `json:"detailed,omitempty" jsonschema:"description=If true\\, return full entity properties. Default is slim output."`
}

func getEntity(ctx context.Context, args GetEntityParams) (string, error) {
	client, err := newAssertsClient(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to create Asserts client: %w", err)
	}

	entity, err := client.resolveEntityInfoCached(ctx, args.EntityType, args.EntityName, args.Env, args.Site, args.Namespace)
	if err != nil {
		return "", err
	}

	if args.Detailed {
		result, err := json.Marshal(entity)
		if err != nil {
			return "", fmt.Errorf("failed to marshal entity: %w", err)
		}
		return string(result), nil
	}

	slim := entity.toSlim()
	result, err := json.Marshal(slim)
	if err != nil {
		return "", fmt.Errorf("failed to marshal entity: %w", err)
	}
	return string(result), nil
}

var GetEntity = mcpgrafana.MustTool(
	"get_entity",
	"Get details for a specific Knowledge Graph entity by type and name. Returns entity properties, scope, connected types, and assertion summary.",
	getEntity,
	mcp.WithTitleAnnotation("Get KG entity"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
)

// --- get_connected_entities ---

type GetConnectedEntitiesParams struct {
	EntityType    string `json:"entityType" jsonschema:"required,description=Type of the source entity"`
	EntityName    string `json:"entityName" jsonschema:"required,description=Name of the source entity"`
	Env           string `json:"env,omitempty" jsonschema:"description=Environment of the source entity"`
	Site          string `json:"site,omitempty" jsonschema:"description=Site of the source entity"`
	Namespace     string `json:"namespace,omitempty" jsonschema:"description=Namespace of the source entity"`
	ConnectedType string `json:"connectedType,omitempty" jsonschema:"description=Filter connected entities by type"`
	Limit         int    `json:"limit,omitempty" jsonschema:"description=Max results to return (default 25\\, max 100)"`
}

func getConnectedEntities(ctx context.Context, args GetConnectedEntitiesParams) (string, error) {
	client, err := newAssertsClient(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to create Asserts client: %w", err)
	}

	entity, err := client.resolveEntityInfoCached(ctx, args.EntityType, args.EntityName, args.Env, args.Site, args.Namespace)
	if err != nil {
		return "", err
	}

	params := url.Values{}
	if args.ConnectedType != "" {
		params.Set("type", args.ConnectedType)
	}
	limit := args.Limit
	if limit <= 0 {
		limit = 25
	}
	if limit > 100 {
		limit = 100
	}
	params.Set("pagination.limit", fmt.Sprintf("%d", limit))

	path := fmt.Sprintf("/public/v1/entities/%s/%d/connected", url.PathEscape(args.EntityType), entity.ID)
	data, err := client.fetchAssertsDataGet(ctx, path, params)
	if err != nil {
		return "", fmt.Errorf("failed to fetch connected entities: %w", err)
	}

	var page struct {
		Items []entitySummaryResponse `json:"items"`
	}
	if err := json.Unmarshal([]byte(data), &page); err != nil {
		return "", fmt.Errorf("failed to parse connected entities response: %w", err)
	}

	slimItems := make([]slimEntity, 0, len(page.Items))
	for i := range page.Items {
		slimItems = append(slimItems, page.Items[i].toSlim())
	}

	result, err := json.Marshal(map[string]any{
		"source":    entity.toSlim(),
		"connected": slimItems,
	})
	if err != nil {
		return "", fmt.Errorf("failed to marshal connected entities: %w", err)
	}
	return string(result), nil
}

var GetConnectedEntities = mcpgrafana.MustTool(
	"get_connected_entities",
	"Get entities connected to a source entity in the Knowledge Graph. Use get_graph_schema first to understand which types connect to which. Chain multiple calls for multi-hop traversal.",
	getConnectedEntities,
	mcp.WithTitleAnnotation("Get connected KG entities"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
)


// --- get_assertion_summary ---

type GetAssertionSummaryParams struct {
	EntityType string    `json:"entityType" jsonschema:"required,description=Entity type"`
	EntityName string    `json:"entityName" jsonschema:"required,description=Entity name"`
	Env        string    `json:"env,omitempty" jsonschema:"description=Environment"`
	Site       string    `json:"site,omitempty" jsonschema:"description=Site"`
	Namespace  string    `json:"namespace,omitempty" jsonschema:"description=Namespace"`
	StartTime  time.Time `json:"startTime" jsonschema:"required,description=Start time in RFC3339 format"`
	EndTime    time.Time `json:"endTime" jsonschema:"required,description=End time in RFC3339 format"`
}

type assertionsSummaryRequestDTO struct {
	StartTime  int64          `json:"startTime"`
	EndTime    int64          `json:"endTime"`
	EntityKeys []entityKeyDTO `json:"entityKeys"`
}

type entityKeyDTO struct {
	Type  string            `json:"type"`
	Name  string            `json:"name"`
	Scope map[string]string `json:"scope,omitempty"`
}

func getAssertionSummary(ctx context.Context, args GetAssertionSummaryParams) (string, error) {
	client, err := newAssertsClient(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to create Asserts client: %w", err)
	}

	entityScope := make(map[string]string)
	if args.Env != "" {
		entityScope["env"] = args.Env
	}
	if args.Site != "" {
		entityScope["site"] = args.Site
	}
	if args.Namespace != "" {
		entityScope["namespace"] = args.Namespace
	}

	reqBody := assertionsSummaryRequestDTO{
		StartTime: args.StartTime.UnixMilli(),
		EndTime:   args.EndTime.UnixMilli(),
		EntityKeys: []entityKeyDTO{
			{
				Type:  args.EntityType,
				Name:  args.EntityName,
				Scope: entityScope,
			},
		},
	}

	data, err := client.fetchAssertsData(ctx, "/v1/assertions/summary", "POST", reqBody)
	if err != nil {
		return "", fmt.Errorf("failed to get assertion summary: %w", err)
	}

	return data, nil
}

var GetAssertionSummary = mcpgrafana.MustTool(
	"get_assertion_summary",
	"Get assertion counts by category and severity for a Knowledge Graph entity. Returns aggregated assertion summary without full details.",
	getAssertionSummary,
	mcp.WithTitleAnnotation("Get KG assertion summary"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
)

// --- search_rca_patterns ---

type SearchRcaPatternsParams struct {
	EntityType string    `json:"entityType" jsonschema:"required,description=Entity type"`
	EntityName string    `json:"entityName" jsonschema:"required,description=Entity name"`
	Env        string    `json:"env,omitempty" jsonschema:"description=Environment"`
	Site       string    `json:"site,omitempty" jsonschema:"description=Site"`
	Namespace  string    `json:"namespace,omitempty" jsonschema:"description=Namespace"`
	StartTime  time.Time `json:"startTime" jsonschema:"required,description=Start time in RFC3339 format"`
	EndTime    time.Time `json:"endTime" jsonschema:"required,description=End time in RFC3339 format"`
}

type rcaPatternSearchRequestDTO struct {
	EntityType string `json:"entityType"`
	EntityName string `json:"entityName"`
	Env        string `json:"env,omitempty"`
	Site       string `json:"site,omitempty"`
	Namespace  string `json:"namespace,omitempty"`
	Start      int64  `json:"start"`
	End        int64  `json:"end"`
}

func searchRcaPatterns(ctx context.Context, args SearchRcaPatternsParams) (string, error) {
	client, err := newAssertsClient(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to create Asserts client: %w", err)
	}

	reqBody := rcaPatternSearchRequestDTO{
		EntityType: args.EntityType,
		EntityName: args.EntityName,
		Env:        args.Env,
		Site:       args.Site,
		Namespace:  args.Namespace,
		Start:      args.StartTime.UnixMilli(),
		End:        args.EndTime.UnixMilli(),
	}

	data, err := client.fetchAssertsData(ctx, "/v1/patterns/search", "POST", reqBody)
	if err != nil {
		return "", fmt.Errorf("failed to search RCA patterns: %w", err)
	}

	return data, nil
}

var SearchRcaPatterns = mcpgrafana.MustTool(
	"search_rca_patterns",
	"Search for root cause analysis patterns for a Knowledge Graph entity. Returns potential root cause entities and their assertion correlation patterns.",
	searchRcaPatterns,
	mcp.WithTitleAnnotation("Search KG RCA patterns"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
)
