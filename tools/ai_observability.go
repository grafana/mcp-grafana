package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	mcpgrafana "github.com/grafana/mcp-grafana"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

const (
	// aiObservabilityBasePath is the Grafana plugin-resources proxy path for the
	// AI Observability app plugin (Sigil, plugin id grafana-sigil-app).
	aiObservabilityBasePath = "/api/plugins/grafana-sigil-app/resources"

	defaultAIObservabilityPageSize = 50
)

func newAIObservabilityClient(ctx context.Context) (*Client, error) {
	cfg := mcpgrafana.GrafanaConfigFromContext(ctx)

	transport, err := mcpgrafana.BuildTransport(&cfg, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create custom transport: %w", err)
	}

	return &Client{
		httpClient: &http.Client{Transport: transport},
		baseURL:    cfg.URL + aiObservabilityBasePath,
	}, nil
}

// fetchAIObservability executes a request against the AI Observability plugin
// resources API and returns the response body, capped at defaultResponseLimitBytes.
func (c *Client) fetchAIObservability(ctx context.Context, method, urlPath string, query url.Values, reqBody any) ([]byte, error) {
	var bodyReader io.Reader
	if reqBody != nil {
		jsonData, err := json.Marshal(reqBody)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal request body: %w", err)
		}
		bodyReader = bytes.NewReader(jsonData)
	}

	fullURL := c.baseURL + urlPath
	if encoded := query.Encode(); encoded != "" {
		fullURL += "?" + encoded
	}

	req, err := http.NewRequestWithContext(ctx, method, fullURL, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	if reqBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to execute request: %w", err)
	}
	defer func() {
		_ = resp.Body.Close() //nolint:errcheck
	}()

	body, err := readResponseBody(resp.Body, defaultResponseLimitBytes)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("request failed with status %d: %s", resp.StatusCode, string(body))
	}

	return body, nil
}

// AIObservabilityConversation is a list item from GET /query/conversations.
type AIObservabilityConversation struct {
	ID                string                            `json:"id"`
	Title             string                            `json:"title,omitempty"`
	GenerationCount   int                               `json:"generation_count"`
	LastGenerationAt  time.Time                         `json:"last_generation_at"`
	CreatedAt         time.Time                         `json:"created_at"`
	UpdatedAt         time.Time                         `json:"updated_at"`
	RatingSummary     *AIObservabilityRatingSummary     `json:"rating_summary,omitempty"`
	AnnotationSummary *AIObservabilityAnnotationSummary `json:"annotation_summary,omitempty"`
}

// AIObservabilitySearchResult is an enriched result from POST
// /query/conversations/search. Has different field names than
// AIObservabilityConversation.
type AIObservabilitySearchResult struct {
	ConversationID    string                        `json:"conversation_id"`
	ConversationTitle string                        `json:"conversation_title,omitempty"`
	UserID            string                        `json:"user_id,omitempty"`
	GenerationCount   int                           `json:"generation_count"`
	FirstGenerationAt time.Time                     `json:"first_generation_at"`
	LastGenerationAt  time.Time                     `json:"last_generation_at"`
	Models            []string                      `json:"models"`
	ModelProviders    map[string]string             `json:"model_providers,omitempty"`
	Agents            []string                      `json:"agents"`
	ErrorCount        int                           `json:"error_count"`
	HasErrors         bool                          `json:"has_errors"`
	TraceIDs          []string                      `json:"trace_ids"`
	RatingSummary     *AIObservabilityRatingSummary `json:"rating_summary,omitempty"`
	AnnotationCount   int                           `json:"annotation_count"`
	EvalSummary       *AIObservabilityEvalSummary   `json:"eval_summary,omitempty"`
}

// AIObservabilityRatingSummary holds conversation rating aggregates.
type AIObservabilityRatingSummary struct {
	TotalCount    int       `json:"total_count"`
	GoodCount     int       `json:"good_count"`
	BadCount      int       `json:"bad_count"`
	LatestRating  string    `json:"latest_rating,omitempty"`
	LatestRatedAt time.Time `json:"latest_rated_at,omitzero"`
	LatestBadAt   time.Time `json:"latest_bad_at,omitzero"`
	HasBadRating  bool      `json:"has_bad_rating"`
}

// AIObservabilityAnnotationSummary holds conversation annotation aggregates.
type AIObservabilityAnnotationSummary struct {
	AnnotationCount      int       `json:"annotation_count"`
	LatestAnnotationType string    `json:"latest_annotation_type,omitempty"`
	LatestAnnotatedAt    time.Time `json:"latest_annotated_at"`
}

// AIObservabilityEvalSummary holds evaluation score aggregates.
type AIObservabilityEvalSummary struct {
	TotalScores int `json:"total_scores"`
	PassCount   int `json:"pass_count"`
	FailCount   int `json:"fail_count"`
}

// AIObservabilitySearchRequest is the request body for POST /query/conversations/search.
type AIObservabilitySearchRequest struct {
	Filters   string                          `json:"filters,omitempty"`
	TimeRange *AIObservabilitySearchTimeRange `json:"time_range,omitempty"`
	PageSize  int                             `json:"page_size,omitempty"`
	Cursor    string                          `json:"cursor,omitempty"`
}

// AIObservabilitySearchTimeRange constrains the search to a time window.
type AIObservabilitySearchTimeRange struct {
	From time.Time `json:"from"`
	To   time.Time `json:"to"`
}

// AIObservabilitySearchResponse is the response from the search endpoint.
type AIObservabilitySearchResponse struct {
	Conversations []AIObservabilitySearchResult `json:"conversations"`
	NextCursor    string                        `json:"next_cursor,omitempty"`
	HasMore       bool                          `json:"has_more"`
}

// AIObservabilityScore is a single evaluation score for a generation.
type AIObservabilityScore struct {
	ScoreID          string                      `json:"score_id"`
	GenerationID     string                      `json:"generation_id"`
	ConversationID   string                      `json:"conversation_id,omitempty"`
	EvaluatorID      string                      `json:"evaluator_id"`
	EvaluatorVersion string                      `json:"evaluator_version"`
	RuleID           string                      `json:"rule_id,omitempty"`
	RunID            string                      `json:"run_id,omitempty"`
	ScoreKey         string                      `json:"score_key"`
	ScoreType        string                      `json:"score_type"` // number, bool, string
	Value            AIObservabilityScoreValue   `json:"value"`
	Unit             string                      `json:"unit,omitempty"`
	Passed           *bool                       `json:"passed,omitempty"`
	Explanation      string                      `json:"explanation,omitempty"`
	Metadata         map[string]any              `json:"metadata,omitempty"`
	TraceID          string                      `json:"trace_id,omitempty"`
	SpanID           string                      `json:"span_id,omitempty"`
	Source           *AIObservabilityScoreSource `json:"source,omitempty"`
	CreatedAt        time.Time                   `json:"created_at"`
}

// AIObservabilityScoreValue is a union type for score values (number, bool, or string).
type AIObservabilityScoreValue struct {
	Number *float64 `json:"number,omitempty"`
	Bool   *bool    `json:"bool,omitempty"`
	String *string  `json:"string,omitempty"`
}

// AIObservabilityScoreSource identifies where a score came from.
type AIObservabilityScoreSource struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}

// aiObservabilityListResponse is the common envelope for paginated list endpoints.
type aiObservabilityListResponse[T any] struct {
	Items      []T    `json:"items"`
	NextCursor string `json:"next_cursor,omitempty"`
}

// listAIObservabilityConversations returns recent conversations. The endpoint
// is not paginated: it does not accept limit or cursor parameters and never
// returns a next_cursor.
func (c *Client) listAIObservabilityConversations(ctx context.Context) (*aiObservabilityListResponse[AIObservabilityConversation], error) {
	body, err := c.fetchAIObservability(ctx, http.MethodGet, "/query/conversations", nil, nil)
	if err != nil {
		return nil, err
	}

	var resp aiObservabilityListResponse[AIObservabilityConversation]
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("failed to decode conversations response: %w", err)
	}
	return &resp, nil
}

func (c *Client) searchAIObservabilityConversations(ctx context.Context, req AIObservabilitySearchRequest) (*AIObservabilitySearchResponse, error) {
	body, err := c.fetchAIObservability(ctx, http.MethodPost, "/query/conversations/search", nil, req)
	if err != nil {
		return nil, err
	}

	var resp AIObservabilitySearchResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("failed to decode search response: %w", err)
	}
	return &resp, nil
}

// getAIObservabilityConversation returns the full conversation detail including
// generations. Decoded as map[string]any because the nested generation objects
// vary by provider.
func (c *Client) getAIObservabilityConversation(ctx context.Context, id string) (map[string]any, error) {
	body, err := c.fetchAIObservability(ctx, http.MethodGet, "/query/conversations/"+url.PathEscape(id), nil, nil)
	if err != nil {
		return nil, err
	}

	var detail map[string]any
	if err := json.Unmarshal(body, &detail); err != nil {
		return nil, fmt.Errorf("failed to decode conversation response: %w", err)
	}
	return detail, nil
}

func (c *Client) getAIObservabilityGeneration(ctx context.Context, id string) (map[string]any, error) {
	body, err := c.fetchAIObservability(ctx, http.MethodGet, "/query/generations/"+url.PathEscape(id), nil, nil)
	if err != nil {
		return nil, err
	}

	var detail map[string]any
	if err := json.Unmarshal(body, &detail); err != nil {
		return nil, fmt.Errorf("failed to decode generation response: %w", err)
	}
	return detail, nil
}

func (c *Client) listAIObservabilityGenerationScores(ctx context.Context, id string, limit int, cursor string) (*aiObservabilityListResponse[AIObservabilityScore], error) {
	if limit <= 0 {
		limit = defaultAIObservabilityPageSize
	}
	query := url.Values{}
	query.Set("limit", strconv.Itoa(limit))
	if cursor != "" {
		query.Set("cursor", cursor)
	}

	body, err := c.fetchAIObservability(ctx, http.MethodGet, "/query/generations/"+url.PathEscape(id)+"/scores", query, nil)
	if err != nil {
		return nil, err
	}

	var resp aiObservabilityListResponse[AIObservabilityScore]
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("failed to decode scores response: %w", err)
	}
	return &resp, nil
}

// ManageAIObservabilityConversationsParams is the param struct for ai_observability_manage_conversations.
type ManageAIObservabilityConversationsParams struct {
	Operation      string `json:"operation" jsonschema:"required,enum=list,enum=search,enum=get,description=The operation to perform: 'list' for recent conversations\\, 'search' to filter conversations by expression and time range\\, 'get' to fetch one conversation with all its generations by ID"`
	ConversationID string `json:"conversation_id,omitempty" jsonschema:"description=The conversation ID (required for 'get' operation)"`
	Filters        string `json:"filters,omitempty" jsonschema:"description=Filter expression (for 'search' operation). Format: key operator value with the value in double quotes\\, multiple filters separated by spaces. See the tool description for keys and operators."`
	StartTime      string `json:"start_time,omitempty" jsonschema:"description=Start of the search time range in RFC3339 or relative format (e.g. now-6h). Defaults to now-24h (for 'search' operation)"`
	EndTime        string `json:"end_time,omitempty" jsonschema:"description=End of the search time range in RFC3339 or relative format (e.g. now). Defaults to now (for 'search' operation)"`
	Limit          int    `json:"limit,omitempty" jsonschema:"description=Maximum number of results per page (default 50) (for 'search' operation only; 'list' is not paginated)"`
	Cursor         string `json:"cursor,omitempty" jsonschema:"description=Pagination cursor from a previous response (for 'search' operation only; 'list' is not paginated). To fetch the next page set this to next_cursor and resend the same filters and start_time/end_time from the first call. Use absolute RFC3339 times for pagination; relative ranges like now-24h invalidate the cursor."`
}

func (p ManageAIObservabilityConversationsParams) validate() error {
	switch p.Operation {
	case "list":
		return nil
	case "search":
		_, err := p.toSearchRequest()
		return err
	case "get":
		if p.ConversationID == "" {
			return fmt.Errorf("conversation_id is required for 'get' operation")
		}
		return nil
	default:
		return fmt.Errorf("unknown operation %q, must be one of: list, search, get", p.Operation)
	}
}

// toSearchRequest builds the search request body. For the first page the time
// range defaults to the last 24 hours client-side (the plugin requires both
// bounds). When paginating, the backend binds the cursor to the exact filters
// and time window of the first page, so re-sending a different window (such as
// a re-resolved "now-24h" whose "now" has advanced) fails with "cursor no
// longer matches current filters". To avoid silently returning the wrong slice,
// defaults are only applied without a cursor; a cursor requires explicit bounds.
func (p ManageAIObservabilityConversationsParams) toSearchRequest() (AIObservabilitySearchRequest, error) {
	startStr, endStr := p.StartTime, p.EndTime
	if p.Cursor == "" {
		if startStr == "" {
			startStr = "now-24h"
		}
		if endStr == "" {
			endStr = "now"
		}
	} else if startStr == "" || endStr == "" {
		return AIObservabilitySearchRequest{}, fmt.Errorf("paginating with a cursor requires repeating the same start_time, end_time, and filters from the first page (use absolute RFC3339 times; relative ranges like now-24h drift between calls and invalidate the cursor)")
	}

	start, err := parseStartTime(startStr)
	if err != nil {
		return AIObservabilitySearchRequest{}, fmt.Errorf("parsing start_time: %w", err)
	}
	end, err := parseEndTime(endStr)
	if err != nil {
		return AIObservabilitySearchRequest{}, fmt.Errorf("parsing end_time: %w", err)
	}

	pageSize := p.Limit
	if pageSize <= 0 {
		pageSize = defaultAIObservabilityPageSize
	}

	return AIObservabilitySearchRequest{
		Filters:   p.Filters,
		TimeRange: &AIObservabilitySearchTimeRange{From: start, To: end},
		PageSize:  pageSize,
		Cursor:    p.Cursor,
	}, nil
}

func manageAIObservabilityConversations(ctx context.Context, args ManageAIObservabilityConversationsParams) (any, error) {
	if err := args.validate(); err != nil {
		return nil, fmt.Errorf("ai_observability_manage_conversations: %w", err)
	}

	client, err := newAIObservabilityClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create AI Observability client: %w", err)
	}

	switch args.Operation {
	case "list":
		return client.listAIObservabilityConversations(ctx)
	case "search":
		req, err := args.toSearchRequest()
		if err != nil {
			return nil, fmt.Errorf("ai_observability_manage_conversations: %w", err)
		}
		return client.searchAIObservabilityConversations(ctx, req)
	case "get":
		return client.getAIObservabilityConversation(ctx, args.ConversationID)
	default:
		return nil, fmt.Errorf("ai_observability_manage_conversations: unknown operation %q", args.Operation)
	}
}

// ManageAIObservabilityGenerationsParams is the param struct for ai_observability_manage_generations.
type ManageAIObservabilityGenerationsParams struct {
	Operation    string `json:"operation" jsonschema:"required,enum=get,enum=scores,description=The operation to perform: 'get' for the full generation detail\\, 'scores' for the evaluation scores of the generation"`
	GenerationID string `json:"generation_id" jsonschema:"required,description=The generation ID"`
	Limit        int    `json:"limit,omitempty" jsonschema:"description=Maximum number of scores per page (default 50) (for 'scores' operation)"`
	Cursor       string `json:"cursor,omitempty" jsonschema:"description=Pagination cursor from a previous response (for 'scores' operation)"`
}

func (p ManageAIObservabilityGenerationsParams) validate() error {
	switch p.Operation {
	case "get", "scores":
		if p.GenerationID == "" {
			return fmt.Errorf("generation_id is required for %q operation", p.Operation)
		}
		return nil
	default:
		return fmt.Errorf("unknown operation %q, must be one of: get, scores", p.Operation)
	}
}

func manageAIObservabilityGenerations(ctx context.Context, args ManageAIObservabilityGenerationsParams) (any, error) {
	if err := args.validate(); err != nil {
		return nil, fmt.Errorf("ai_observability_manage_generations: %w", err)
	}

	client, err := newAIObservabilityClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create AI Observability client: %w", err)
	}

	switch args.Operation {
	case "get":
		return client.getAIObservabilityGeneration(ctx, args.GenerationID)
	case "scores":
		return client.listAIObservabilityGenerationScores(ctx, args.GenerationID, args.Limit, args.Cursor)
	default:
		return nil, fmt.Errorf("ai_observability_manage_generations: unknown operation %q", args.Operation)
	}
}

var ManageAIObservabilityConversations = mcpgrafana.MustTool(
	"ai_observability_manage_conversations",
	`List, search, and fetch LLM conversations from Grafana AI Observability (the Sigil app plugin, grafana-sigil-app).

Operations:
- 'list': recent conversations (lightweight; id, title, generation count, timestamps)
- 'search': search conversations by filter expression and time range; results include models, agents, error counts, rating and eval summaries, and trace IDs
- 'get': one conversation by ID with all its generations, including full prompts and outputs (can be large)

Filter syntax for 'search': key operator value, with the value in double quotes; multiple filters are separated by spaces and combined with AND.
Filter keys (trace): model, provider, agent, agent.version, status, error.type, error.category, duration, tool.name, operation, namespace, cluster, service
Filter keys (metadata): generation_count, eval.passed, eval.evaluator_id, eval.score_key, eval.score
Operators: =, !=, >, <, >=, <=, =~ (regex)
Example: status = "error" agent = "claude-code"

Pagination ('search'): when a response has next_cursor, fetch the next page by calling 'search' again with cursor set to next_cursor and the same filters, start_time, and end_time as the first call. Use absolute RFC3339 times when paginating; relative ranges like now-24h shift between calls and the cursor will be rejected.

When to use:
- Debugging an AI application: find failing or low-rated conversations, then inspect their generations
- Reviewing evaluation results and user ratings across conversations

When NOT to use:
- Fetching a single generation or its evaluation scores (use ai_observability_manage_generations)`,
	manageAIObservabilityConversations,
	mcp.WithTitleAnnotation("Manage AI Observability conversations"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
)

var ManageAIObservabilityGenerations = mcpgrafana.MustTool(
	"ai_observability_manage_generations",
	`Fetch a single LLM generation and its evaluation scores from Grafana AI Observability (the Sigil app plugin, grafana-sigil-app).

Operations:
- 'get': full generation detail by ID, including prompt, output, model, and usage (can be large)
- 'scores': evaluation scores for a generation (evaluator, score key, score type, value, passed, explanation)

When to use:
- Drilling into one generation found via ai_observability_manage_conversations
- Checking why an evaluation passed or failed for a specific generation

When NOT to use:
- Searching or listing conversations (use ai_observability_manage_conversations)`,
	manageAIObservabilityGenerations,
	mcp.WithTitleAnnotation("Manage AI Observability generations"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
)

func AddAIObservabilityTools(mcp *server.MCPServer) {
	ManageAIObservabilityConversations.Register(mcp)
	ManageAIObservabilityGenerations.Register(mcp)
}
