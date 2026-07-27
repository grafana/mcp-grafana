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
	// agento11yBasePath is the Grafana plugin-resources proxy path for the
	// Agent Observability app plugin (plugin id grafana-agento11y-app).
	agento11yBasePath = "/api/plugins/grafana-agento11y-app/resources"

	defaultAgento11yPageSize = 50
)

func newAgento11yClient(ctx context.Context) (*Client, error) {
	cfg := mcpgrafana.GrafanaConfigFromContext(ctx)

	transport, err := mcpgrafana.BuildTransport(&cfg, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create custom transport: %w", err)
	}

	return &Client{
		httpClient: &http.Client{Transport: transport},
		baseURL:    cfg.URL + agento11yBasePath,
	}, nil
}

// fetchAgento11y executes a request against the Agent Observability plugin
// resources API and returns the response body; bodies larger than
// defaultResponseLimitBytes are rejected with an error.
func (c *Client) fetchAgento11y(ctx context.Context, method, urlPath string, query url.Values, reqBody any) ([]byte, error) {
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

// Agento11yConversation is a list item from GET /query/conversations.
type Agento11yConversation struct {
	ID                string                      `json:"id"`
	Title             string                      `json:"title,omitempty"`
	GenerationCount   int                         `json:"generation_count"`
	LastGenerationAt  time.Time                   `json:"last_generation_at"`
	CreatedAt         time.Time                   `json:"created_at"`
	UpdatedAt         time.Time                   `json:"updated_at"`
	RatingSummary     *Agento11yRatingSummary     `json:"rating_summary,omitempty"`
	AnnotationSummary *Agento11yAnnotationSummary `json:"annotation_summary,omitempty"`
}

// Agento11ySearchResult is an enriched result from POST
// /query/conversations/search. Has different field names than
// Agento11yConversation.
type Agento11ySearchResult struct {
	ConversationID    string                  `json:"conversation_id"`
	ConversationTitle string                  `json:"conversation_title,omitempty"`
	UserID            string                  `json:"user_id,omitempty"`
	GenerationCount   int                     `json:"generation_count"`
	FirstGenerationAt time.Time               `json:"first_generation_at"`
	LastGenerationAt  time.Time               `json:"last_generation_at"`
	Models            []string                `json:"models"`
	ModelProviders    map[string]string       `json:"model_providers,omitempty"`
	Agents            []string                `json:"agents"`
	ErrorCount        int                     `json:"error_count"`
	HasErrors         bool                    `json:"has_errors"`
	TraceIDs          []string                `json:"trace_ids"`
	RatingSummary     *Agento11yRatingSummary `json:"rating_summary,omitempty"`
	AnnotationCount   int                     `json:"annotation_count"`
	EvalSummary       *Agento11yEvalSummary   `json:"eval_summary,omitempty"`
}

// Agento11yRatingSummary holds conversation rating aggregates.
type Agento11yRatingSummary struct {
	TotalCount    int       `json:"total_count"`
	GoodCount     int       `json:"good_count"`
	BadCount      int       `json:"bad_count"`
	LatestRating  string    `json:"latest_rating,omitempty"`
	LatestRatedAt time.Time `json:"latest_rated_at,omitzero"`
	LatestBadAt   time.Time `json:"latest_bad_at,omitzero"`
	HasBadRating  bool      `json:"has_bad_rating"`
}

// Agento11yAnnotationSummary holds conversation annotation aggregates.
type Agento11yAnnotationSummary struct {
	AnnotationCount      int       `json:"annotation_count"`
	LatestAnnotationType string    `json:"latest_annotation_type,omitempty"`
	LatestAnnotatedAt    time.Time `json:"latest_annotated_at"`
}

// Agento11yEvalSummary holds evaluation score aggregates.
type Agento11yEvalSummary struct {
	TotalScores int `json:"total_scores"`
	PassCount   int `json:"pass_count"`
	FailCount   int `json:"fail_count"`
}

// Agento11ySearchRequest is the request body for POST /query/conversations/search.
type Agento11ySearchRequest struct {
	Filters   string                    `json:"filters,omitempty"`
	TimeRange *Agento11ySearchTimeRange `json:"time_range,omitempty"`
	PageSize  int                       `json:"page_size,omitempty"`
	Cursor    string                    `json:"cursor,omitempty"`
}

// Agento11ySearchTimeRange constrains the search to a time window.
type Agento11ySearchTimeRange struct {
	From time.Time `json:"from"`
	To   time.Time `json:"to"`
}

// Agento11ySearchResponse is the response from the search endpoint.
type Agento11ySearchResponse struct {
	Conversations []Agento11ySearchResult `json:"conversations"`
	NextCursor    string                  `json:"next_cursor,omitempty"`
	HasMore       bool                    `json:"has_more"`
}

// Agento11yScore is a single evaluation score for a generation.
type Agento11yScore struct {
	ScoreID          string                `json:"score_id"`
	GenerationID     string                `json:"generation_id"`
	ConversationID   string                `json:"conversation_id,omitempty"`
	EvaluatorID      string                `json:"evaluator_id"`
	EvaluatorVersion string                `json:"evaluator_version"`
	RuleID           string                `json:"rule_id,omitempty"`
	ExperimentID     string                `json:"experiment_id,omitempty"`
	ScoreKey         string                `json:"score_key"`
	ScoreType        string                `json:"score_type"` // number, bool, string
	Value            Agento11yScoreValue   `json:"value"`
	Unit             string                `json:"unit,omitempty"`
	Passed           *bool                 `json:"passed,omitempty"`
	Explanation      string                `json:"explanation,omitempty"`
	Metadata         map[string]any        `json:"metadata,omitempty"`
	TraceID          string                `json:"trace_id,omitempty"`
	SpanID           string                `json:"span_id,omitempty"`
	Source           *Agento11yScoreSource `json:"source,omitempty"`
	CreatedAt        time.Time             `json:"created_at"`
}

// Agento11yScoreValue is a union type for score values (number, bool, or string).
type Agento11yScoreValue struct {
	Number *float64 `json:"number,omitempty"`
	Bool   *bool    `json:"bool,omitempty"`
	String *string  `json:"string,omitempty"`
}

// Agento11yScoreSource identifies where a score came from.
type Agento11yScoreSource struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}

// agento11yListResponse is the common envelope for paginated list endpoints.
type agento11yListResponse[T any] struct {
	Items      []T    `json:"items"`
	NextCursor string `json:"next_cursor,omitempty"`
}

// listAgento11yConversations returns a page of recent conversations.
func (c *Client) listAgento11yConversations(ctx context.Context, limit int, cursor string) (*agento11yListResponse[Agento11yConversation], error) {
	if limit <= 0 {
		limit = defaultAgento11yPageSize
	}
	query := url.Values{}
	query.Set("limit", strconv.Itoa(limit))
	if cursor != "" {
		query.Set("cursor", cursor)
	}

	body, err := c.fetchAgento11y(ctx, http.MethodGet, "/query/conversations", query, nil)
	if err != nil {
		return nil, err
	}

	var resp agento11yListResponse[Agento11yConversation]
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("failed to decode conversations response: %w", err)
	}
	return &resp, nil
}

func (c *Client) searchAgento11yConversations(ctx context.Context, req Agento11ySearchRequest) (*Agento11ySearchResponse, error) {
	body, err := c.fetchAgento11y(ctx, http.MethodPost, "/query/conversations/search", nil, req)
	if err != nil {
		return nil, err
	}

	var resp Agento11ySearchResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("failed to decode search response: %w", err)
	}
	return &resp, nil
}

// getAgento11yDetail returns the full conversation or generation detail.
// Decoded as map[string]any because the nested generation objects vary by
// provider.
func (c *Client) getAgento11yDetail(ctx context.Context, urlPath, what string) (map[string]any, error) {
	body, err := c.fetchAgento11y(ctx, http.MethodGet, urlPath, nil, nil)
	if err != nil {
		return nil, err
	}

	var detail map[string]any
	if err := json.Unmarshal(body, &detail); err != nil {
		return nil, fmt.Errorf("failed to decode %s response: %w", what, err)
	}
	return detail, nil
}

func (c *Client) listAgento11yGenerationScores(ctx context.Context, id string, limit int, cursor string) (*agento11yListResponse[Agento11yScore], error) {
	if limit <= 0 {
		limit = defaultAgento11yPageSize
	}
	query := url.Values{}
	query.Set("limit", strconv.Itoa(limit))
	if cursor != "" {
		query.Set("cursor", cursor)
	}

	body, err := c.fetchAgento11y(ctx, http.MethodGet, "/query/generations/"+url.PathEscape(id)+"/scores", query, nil)
	if err != nil {
		return nil, err
	}

	var resp agento11yListResponse[Agento11yScore]
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("failed to decode scores response: %w", err)
	}
	return &resp, nil
}

// ManageAgento11yConversationsParams is the param struct for agento11y_manage_conversations.
type ManageAgento11yConversationsParams struct {
	Operation      string `json:"operation" jsonschema:"required,enum=list,enum=search,enum=get,description=The operation to perform: 'list' for recent conversations\\, 'search' to filter conversations by expression and time range\\, 'get' to fetch one conversation with all its generations by ID"`
	ConversationID string `json:"conversation_id,omitempty" jsonschema:"description=The conversation ID (required for 'get' operation)"`
	Filters        string `json:"filters,omitempty" jsonschema:"description=Filter expression (for 'search' operation). Format: key operator value with the value in double quotes\\, multiple filters separated by spaces. See the tool description for keys and operators."`
	StartTime      string `json:"start_time,omitempty" jsonschema:"description=Start of the search time range in RFC3339 or relative format (e.g. now-6h). Defaults to now-24h (for 'search' operation)"`
	EndTime        string `json:"end_time,omitempty" jsonschema:"description=End of the search time range in RFC3339 or relative format (e.g. now). Defaults to now (for 'search' operation)"`
	Limit          int    `json:"limit,omitempty" jsonschema:"description=Maximum number of results per page (default 50) (for 'list' and 'search' operations)"`
	Cursor         string `json:"cursor,omitempty" jsonschema:"description=Pagination cursor from a previous response (for 'list' and 'search' operations). To fetch the next page set this to next_cursor. For 'search' also resend the same filters and start_time/end_time from the first call and use absolute RFC3339 times; relative ranges like now-24h invalidate the cursor."`
}

func (p ManageAgento11yConversationsParams) validate() error {
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
// longer matches current filters". Rather than let drifting defaults trigger
// that confusing backend error, defaults are only applied without a cursor;
// a cursor requires explicit bounds and fails client-side otherwise.
func (p ManageAgento11yConversationsParams) toSearchRequest() (Agento11ySearchRequest, error) {
	startStr, endStr := p.StartTime, p.EndTime
	if p.Cursor == "" {
		if startStr == "" {
			startStr = "now-24h"
		}
		if endStr == "" {
			endStr = "now"
		}
	} else if startStr == "" || endStr == "" {
		return Agento11ySearchRequest{}, fmt.Errorf("paginating with a cursor requires repeating the same start_time, end_time, and filters from the first page (use absolute RFC3339 times; relative ranges like now-24h drift between calls and invalidate the cursor)")
	}

	start, err := parseStartTime(startStr)
	if err != nil {
		return Agento11ySearchRequest{}, fmt.Errorf("parsing start_time: %w", err)
	}
	end, err := parseEndTime(endStr)
	if err != nil {
		return Agento11ySearchRequest{}, fmt.Errorf("parsing end_time: %w", err)
	}

	pageSize := p.Limit
	if pageSize <= 0 {
		pageSize = defaultAgento11yPageSize
	}

	return Agento11ySearchRequest{
		Filters:   p.Filters,
		TimeRange: &Agento11ySearchTimeRange{From: start, To: end},
		PageSize:  pageSize,
		Cursor:    p.Cursor,
	}, nil
}

func manageAgento11yConversations(ctx context.Context, args ManageAgento11yConversationsParams) (any, error) {
	if err := args.validate(); err != nil {
		return nil, fmt.Errorf("agento11y_manage_conversations: %w", err)
	}

	client, err := newAgento11yClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create Agent Observability client: %w", err)
	}

	switch args.Operation {
	case "list":
		return client.listAgento11yConversations(ctx, args.Limit, args.Cursor)
	case "search":
		req, err := args.toSearchRequest()
		if err != nil {
			return nil, fmt.Errorf("agento11y_manage_conversations: %w", err)
		}
		return client.searchAgento11yConversations(ctx, req)
	case "get":
		return client.getAgento11yDetail(ctx, "/query/conversations/"+url.PathEscape(args.ConversationID), "conversation")
	default:
		return nil, fmt.Errorf("agento11y_manage_conversations: unknown operation %q", args.Operation)
	}
}

// ManageAgento11yGenerationsParams is the param struct for agento11y_manage_generations.
type ManageAgento11yGenerationsParams struct {
	Operation    string `json:"operation" jsonschema:"required,enum=get,enum=scores,description=The operation to perform: 'get' for the full generation detail\\, 'scores' for the evaluation scores of the generation"`
	GenerationID string `json:"generation_id" jsonschema:"required,description=The generation ID"`
	Limit        int    `json:"limit,omitempty" jsonschema:"description=Maximum number of scores per page (default 50) (for 'scores' operation)"`
	Cursor       string `json:"cursor,omitempty" jsonschema:"description=Pagination cursor from a previous response (for 'scores' operation)"`
}

func (p ManageAgento11yGenerationsParams) validate() error {
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

func manageAgento11yGenerations(ctx context.Context, args ManageAgento11yGenerationsParams) (any, error) {
	if err := args.validate(); err != nil {
		return nil, fmt.Errorf("agento11y_manage_generations: %w", err)
	}

	client, err := newAgento11yClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create Agent Observability client: %w", err)
	}

	switch args.Operation {
	case "get":
		return client.getAgento11yDetail(ctx, "/query/generations/"+url.PathEscape(args.GenerationID), "generation")
	case "scores":
		return client.listAgento11yGenerationScores(ctx, args.GenerationID, args.Limit, args.Cursor)
	default:
		return nil, fmt.Errorf("agento11y_manage_generations: unknown operation %q", args.Operation)
	}
}

var ManageAgento11yConversations = mcpgrafana.MustTool(
	"agento11y_manage_conversations",
	`List, search, and fetch LLM conversations from Grafana Agent Observability (the grafana-agento11y-app plugin).

Operations:
- 'list': recent conversations (lightweight; id, title, generation count, timestamps), paginated via limit and cursor
- 'search': search conversations by filter expression and time range; results include models, agents, error counts, rating and eval summaries, and trace IDs
- 'get': one conversation by ID with all its generations, including full prompts and outputs (can be large)

Filter syntax for 'search': key operator value, with the value in double quotes; multiple filters are separated by spaces and combined with AND.
Filter keys (trace): model, provider, agent, agent.version, status, error.type, error.category, duration, tool.name, operation, namespace, cluster, service
Filter keys (metadata): generation_count, eval.passed, eval.evaluator_id, eval.score_key, eval.score
Operators: =, !=, >, <, >=, <=, =~ (regex)
Example: status = "error" agent = "claude-code"

Pagination: when a response has next_cursor, fetch the next page by calling the same operation again with cursor set to next_cursor. For 'search', also repeat the same filters, start_time, and end_time as the first call, using absolute RFC3339 times; relative ranges like now-24h shift between calls and the cursor will be rejected.

When to use:
- Debugging an AI application: find failing or low-rated conversations, then inspect their generations
- Reviewing evaluation results and user ratings across conversations

When NOT to use:
- Fetching a single generation or its evaluation scores (use agento11y_manage_generations)`,
	manageAgento11yConversations,
	mcp.WithTitleAnnotation("Manage Agent Observability conversations"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
)

var ManageAgento11yGenerations = mcpgrafana.MustTool(
	"agento11y_manage_generations",
	`Fetch a single LLM generation and its evaluation scores from Grafana Agent Observability (the grafana-agento11y-app plugin).

Operations:
- 'get': full generation detail by ID, including prompt, output, model, and usage (can be large)
- 'scores': evaluation scores for a generation (evaluator, score key, score type, value, passed, explanation)

When to use:
- Drilling into one generation found via agento11y_manage_conversations
- Checking why an evaluation passed or failed for a specific generation

When NOT to use:
- Searching or listing conversations (use agento11y_manage_conversations)`,
	manageAgento11yGenerations,
	mcp.WithTitleAnnotation("Manage Agent Observability generations"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
)

func AddAgento11yTools(mcp *server.MCPServer) {
	ManageAgento11yConversations.Register(mcp)
	ManageAgento11yGenerations.Register(mcp)
}
