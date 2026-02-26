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

	mcpgrafana "github.com/grafana/mcp-grafana"
	"github.com/mark3labs/mcp-go/mcp"
)

const (
	entityCollectionName = "kg-entities"
	defaultEmbedder      = "vertex/gemini-embedding-001"
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
		return "", err
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
		return "", fmt.Errorf("semantic search failed: %w", err)
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
			Score:   1 - r.Distance, // cosine distance → similarity
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
		"query":   args.Query,
		"results": results,
	})
	if err != nil {
		return "", fmt.Errorf("failed to marshal results: %w", err)
	}
	return string(output), nil
}

var FindEntitiesSemantic = mcpgrafana.MustTool(
	"find_entities_semantic",
	"Find Knowledge Graph entities using natural language search. Uses vector similarity to match entity descriptions. Requires GRAFANA_SEARCH_SERVICE_URL to be set. Example: 'the database handling payment orders'.",
	findEntitiesSemantic,
	mcp.WithTitleAnnotation("Find KG entities by meaning"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
)
