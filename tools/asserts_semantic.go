package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	mcpgrafana "github.com/grafana/mcp-grafana"
)

const (
	entityCollectionName = "kg-entities"
	defaultSearchLimit   = 10
)

type searchServiceClient struct {
	httpClient *http.Client
	baseURL    string
}

func newSearchServiceClient(ctx context.Context) (*searchServiceClient, error) {
	baseURL := os.Getenv("GRAFANA_SEARCH_SERVICE_URL")
	if baseURL == "" {
		return nil, fmt.Errorf("GRAFANA_SEARCH_SERVICE_URL not set. The semantic search tool requires the Loop search service (pgvector)")
	}

	cfg := mcpgrafana.GrafanaConfigFromContext(ctx)
	transport, err := mcpgrafana.BuildTransport(&cfg, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to build transport: %w", err)
	}
	transport = NewAuthRoundTripper(transport, cfg.AccessToken, cfg.IDToken, cfg.APIKey, cfg.BasicAuth)
	transport = mcpgrafana.NewOrgIDRoundTripper(transport, cfg.OrgID)

	return &searchServiceClient{
		httpClient: &http.Client{Transport: mcpgrafana.NewUserAgentTransport(transport)},
		baseURL:    strings.TrimRight(baseURL, "/"),
	}, nil
}

type vectorRetrieveRequest struct {
	Query  string         `json:"query"`
	Limit  int            `json:"limit"`
	Filter map[string]any `json:"filter,omitempty"`
}

type vectorRetrieveResponse struct {
	Results []vectorResult `json:"results"`
}

type vectorResult struct {
	Key      string         `json:"key"`
	Content  string         `json:"content"`
	Metadata map[string]any `json:"metadata,omitempty"`
	Distance float64        `json:"distance"`
}

func (c *searchServiceClient) retrieve(ctx context.Context, collection string, req *vectorRetrieveRequest) (*vectorRetrieveResponse, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	path := fmt.Sprintf("%s/collections/%s/vectors/retrieve", c.baseURL, collection)
	httpReq, err := http.NewRequestWithContext(ctx, "POST", path, bytes.NewBuffer(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to execute request: %w", err)
	}
	defer func() {
		_ = resp.Body.Close() //nolint:errcheck
	}()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("search service returned status %d: %s", resp.StatusCode, string(respBody))
	}

	var result vectorRetrieveResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}
	return &result, nil
}

// --- find_entities_semantic ---

type FindEntitiesSemanticParams struct {
	Query     string `json:"query" jsonschema:"required,description=Natural language query to find entities (e.g. 'the database handling payment orders'\\, 'services with high latency')"`
	Limit     int    `json:"limit,omitempty" jsonschema:"description=Max results to return (default 10)"`
	Env       string `json:"env,omitempty" jsonschema:"description=Filter by environment"`
	Namespace string `json:"namespace,omitempty" jsonschema:"description=Filter by namespace"`
}

func findEntitiesSemantic(ctx context.Context, args FindEntitiesSemanticParams) (string, error) {
	client, err := newSearchServiceClient(ctx)
	if err != nil {
		return findEntitiesFallback(ctx, args)
	}

	limit := args.Limit
	if limit <= 0 {
		limit = defaultSearchLimit
	}

	filter := make(map[string]any)
	if args.Env != "" {
		filter["env"] = args.Env
	}
	if args.Namespace != "" {
		filter["namespace"] = args.Namespace
	}

	var filterPtr map[string]any
	if len(filter) > 0 {
		filterPtr = filter
	}

	resp, err := client.retrieve(ctx, entityCollectionName, &vectorRetrieveRequest{
		Query:  args.Query,
		Limit:  limit,
		Filter: filterPtr,
	})
	if err != nil {
		return findEntitiesFallback(ctx, args)
	}

	type semanticResult struct {
		EntityType string  `json:"entityType,omitempty"`
		EntityName string  `json:"entityName,omitempty"`
		Content    string  `json:"content"`
		Score      float64 `json:"score"`
	}

	results := make([]semanticResult, 0, len(resp.Results))
	for _, r := range resp.Results {
		sr := semanticResult{
			Content: r.Content,
			Score:   1 - r.Distance,
		}
		if r.Metadata != nil {
			if v, ok := r.Metadata["entityType"].(string); ok {
				sr.EntityType = v
			}
			if v, ok := r.Metadata["entityName"].(string); ok {
				sr.EntityName = v
			}
		}
		results = append(results, sr)
	}

	output, err := json.Marshal(map[string]any{
		"query":  args.Query,
		"mode":   "semantic",
		"results": results,
	})
	if err != nil {
		return "", fmt.Errorf("failed to marshal results: %w", err)
	}
	return string(output), nil
}

// findEntitiesFallback uses deterministic name search via /v1/search when
// the semantic search service is unavailable. Less powerful but still useful
// for queries like "find the payment service".
func findEntitiesFallback(ctx context.Context, args FindEntitiesSemanticParams) (string, error) {
	assertsClient, err := newAssertsClient(ctx)
	if err != nil {
		return "", fmt.Errorf("semantic search unavailable and fallback failed: %w", err)
	}

	now := time.Now()
	matcher := entityMatcherDTO{
		PropertyMatchers: []propertyMatcherDTO{
			{Name: "name", Value: args.Query, Op: "CONTAINS"},
		},
	}

	reqBody := searchRequestDTO{
		TimeCriteria: timeCriteriaDTO{
			Start: now.Add(-24 * time.Hour).UnixMilli(),
			End:   now.UnixMilli(),
		},
		FilterCriteria: []entityMatcherDTO{matcher},
	}

	scopeVals := make(map[string][]string)
	if args.Env != "" {
		scopeVals["env"] = []string{args.Env}
	}
	if args.Namespace != "" {
		scopeVals["namespace"] = []string{args.Namespace}
	}
	if len(scopeVals) > 0 {
		reqBody.ScopeCriteria = &scopeCriteriaDTO{NameAndValues: scopeVals}
	}

	data, err := assertsClient.fetchAssertsData(ctx, "/v1/search", "POST", reqBody)
	if err != nil {
		return "", fmt.Errorf("fallback search failed: %w", err)
	}

	var resp searchResponseDTO
	if err := json.Unmarshal([]byte(data), &resp); err != nil {
		return data, nil
	}

	limit := args.Limit
	if limit <= 0 {
		limit = defaultSearchLimit
	}

	entities := resp.Data.Entities
	if len(entities) > limit {
		entities = entities[:limit]
	}

	slimEntities := make([]slimEntity, 0, len(entities))
	for i := range entities {
		slimEntities = append(slimEntities, entities[i].toSlim())
	}

	output, err := json.Marshal(map[string]any{
		"query":    args.Query,
		"mode":     "deterministic_fallback",
		"note":     "Semantic search unavailable. Results are from deterministic name matching.",
		"results":  slimEntities,
	})
	if err != nil {
		return "", fmt.Errorf("failed to marshal fallback results: %w", err)
	}
	return string(output), nil
}

