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
	body, _, err := c.fetchAgento11yWithStatus(ctx, method, urlPath, query, reqBody)
	return body, err
}

// fetchAgento11yWithStatus is fetchAgento11y plus the response status code, for
// callers that decode the body and need to tell 204 No Content apart from a
// route that answered 200 with nothing.
func (c *Client) fetchAgento11yWithStatus(ctx context.Context, method, urlPath string, query url.Values, reqBody any) ([]byte, int, error) {
	var bodyReader io.Reader
	if reqBody != nil {
		jsonData, err := json.Marshal(reqBody)
		if err != nil {
			return nil, 0, fmt.Errorf("failed to marshal request body: %w", err)
		}
		bodyReader = bytes.NewReader(jsonData)
	}

	fullURL := c.baseURL + urlPath
	if encoded := query.Encode(); encoded != "" {
		fullURL += "?" + encoded
	}

	req, err := http.NewRequestWithContext(ctx, method, fullURL, bodyReader)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to create request: %w", err)
	}
	if reqBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to execute request: %w", err)
	}
	defer func() {
		_ = resp.Body.Close() //nolint:errcheck
	}()

	body, err := readResponseBody(resp.Body, defaultResponseLimitBytes)
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, resp.StatusCode, fmt.Errorf("request failed with status %d: %s", resp.StatusCode, string(body))
	}

	return body, resp.StatusCode, nil
}

// fetchAgento11yJSON executes a request and decodes the JSON response into T.
// A 204 No Content response carries no body and yields the zero value; any other
// status without a decodable body is an error, so a route that answers 200 with
// nothing is not reported as an empty result.
func fetchAgento11yJSON[T any](ctx context.Context, c *Client, method, urlPath string, query url.Values, reqBody any) (T, error) {
	var out T
	body, status, err := c.fetchAgento11yWithStatus(ctx, method, urlPath, query, reqBody)
	if err != nil {
		return out, err
	}
	if status == http.StatusNoContent {
		return out, nil
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return out, fmt.Errorf("failed to decode %s %s response: %w", method, urlPath, err)
	}
	return out, nil
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
	mcp.WithDestructiveHintAnnotation(false),
	mcp.WithOpenWorldHintAnnotation(false),
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
	mcp.WithDestructiveHintAnnotation(false),
	mcp.WithOpenWorldHintAnnotation(false),
)

const manageAgento11yEvaluatorsDescriptionFmt = `%s

An evaluator is a scoring function (kind: llm_judge, json_schema, regex, or heuristic) that scores generations. The scores returned by agento11y_manage_generations operation 'scores' name the evaluator that produced them. Templates are versioned starting points for evaluators. Judge providers and models are the LLM backends an llm_judge evaluator can use.

Operations:
- 'list_evaluators': evaluators in this tenant (paginated)
- 'get_evaluator': one evaluator by ID, with its kind, config, and output_keys
- 'list_templates': evaluator templates, filterable by scope ('global' for built-ins, 'tenant' for locally created ones)
- 'get_template': one template with its config, output_keys, and version list
- 'list_template_versions': version history of a template, each version with its config and output_keys
- 'list_judge_providers': judge providers configured on this stack
- 'list_judge_models': judge models, optionally filtered by provider%s

Identifiers (evaluator_id, template_id) accept only letters, digits, '_', and '.'; hyphens are rejected by the API. Template operations need a stack with the evaluator template store configured and return 404 otherwise. Pagination: when a response carries next_cursor, call the same operation again with cursor set to it.

Permissions: reads need grafana-agento11y-app.data:read (Agento11y Editor or Admin). %s

When to use:
- A score from agento11y_manage_generations names an evaluator and you need to see what it checks
- Inspecting a template before deriving an evaluator from it%s

When NOT to use:
- Finding which rule scheduled an evaluator, or which guard enforces it (use agento11y_manage_eval_rules)
- Listing conversations, generations, or scores (use agento11y_manage_conversations and agento11y_manage_generations)%s`

func manageAgento11yEvaluatorsDescription(readOnly bool) string {
	if readOnly {
		return fmt.Sprintf(manageAgento11yEvaluatorsDescriptionFmt,
			"Read the evaluator catalog of Grafana Agent Observability (the grafana-agento11y-app plugin): evaluators, evaluator templates, and the judge model catalog.",
			"",
			"This variant performs no writes.",
			"",
			"\n- Creating, updating, or deleting evaluators (read-only tool)",
		)
	}
	return fmt.Sprintf(manageAgento11yEvaluatorsDescriptionFmt,
		"Manage the evaluator catalog of Grafana Agent Observability (the grafana-agento11y-app plugin): read evaluators, evaluator templates, and the judge model catalog, and create, test, or delete evaluators.",
		`
- 'upsert_evaluator': create or update an evaluator from an inline 'definition'. POST is create-or-update keyed on definition.evaluator_id; there is no separate update operation, and re-using an existing 'version' returns 409, so bump the version to change an evaluator
- 'delete_evaluator': soft-delete an evaluator by ID. Rules and guards that reference it keep the reference and silently stop producing scores, so check agento11y_manage_eval_rules first
- 'fork_template': derive a new evaluator from a template in one call. Prefer this over copying 'get_template' output into 'upsert_evaluator', which the API rejects
- 'test_evaluator': run an inline evaluator definition against one generation and return its scores without persisting anything. Useful for tuning a judge config before 'upsert_evaluator'`,
		"Every write, plus 'test_evaluator' (which persists nothing), needs grafana-agento11y-app.eval:write, granted only by the Agento11y Admin role; an Editor token gets 403.",
		`
- Tuning an llm_judge config against a real generation with 'test_evaluator' before storing it
- Creating an evaluator so a rule or guard can reference it`,
		"",
	)
}

const manageAgento11yEvalRulesDescriptionFmt = `%s

Two different resources with different runtime behavior:
- Eval rules (/eval/rules) are asynchronous. A rule selects production traffic (selector, match filters, sample_rate) and schedules its evaluator_ids to score matching generations after the fact. Rules only observe; they never change a request.
- Guards (/eval/hook-rules; there is no /eval/guards path) run inline on the request path and can deny it, redact content, or block tool calls. A guard is inert until the agent application calls the hooks endpoint (POST /eval/hooks:evaluate) itself: a stored guard on its own changes nothing.

Operations:
- 'list_rules': asynchronous eval rules in this tenant (paginated)
- 'get_rule': one eval rule by ID
- 'list_guards': guards, read from /eval/hook-rules (paginated)
- 'get_guard': one guard by ID%s

Identifiers (rule_id) accept only letters, digits, '_', and '.'; hyphens are rejected by the API. Rule selectors: user_visible_turn, all_assistant_generations, tool_call_steps, errored_generations, conversation (guards also accept 'all'). Match keys are arrays and include agent_name, agent_version, operation_name, model.provider, model.name, mode, error.type, error.category, and tags.<key>. Pagination: when a response carries next_cursor, call the same operation again with cursor set to it.

Permissions: reads need grafana-agento11y-app.data:read (Agento11y Editor or Admin). %s

When to use:
- A score names an evaluator and you need to know which rule scheduled it and on what traffic
- Auditing which guards are live and whether they warn or deny%s

When NOT to use:
- Inspecting what an evaluator checks, or the template it came from (use agento11y_manage_evaluators)
- Listing conversations, generations, or scores (use agento11y_manage_conversations and agento11y_manage_generations)%s`

func manageAgento11yEvalRulesDescription(readOnly bool) string {
	if readOnly {
		return fmt.Sprintf(manageAgento11yEvalRulesDescriptionFmt,
			"Read the evaluation rules and guards of Grafana Agent Observability (the grafana-agento11y-app plugin): the configuration that decides when evaluators run.",
			"",
			"This variant performs no writes.",
			"",
			"\n- Creating, updating, or deleting rules and guards (read-only tool)",
		)
	}
	return fmt.Sprintf(manageAgento11yEvalRulesDescriptionFmt,
		"Manage the evaluation rules and guards of Grafana Agent Observability (the grafana-agento11y-app plugin): the configuration that decides when evaluators run.",
		`
- 'create_rule': create an asynchronous eval rule from an inline 'definition'
- 'update_rule': patch an existing rule; send only the fields to change (rule_id is taken from the 'rule_id' parameter and must not appear in the definition)
- 'delete_rule': delete a rule by ID
- 'preview_rule': dry-run a selector, match, and sample_rate against recent traffic and return how many generations would match and be sampled, plus example generations. Run this before creating a rule that spends judge tokens
- 'create_guard': create an inline guard (stored as a hook rule)
- 'update_guard': full replace of a guard (PUT, not PATCH) — omitted fields reset to server defaults, so send the complete definition, normally a 'get_guard' result with your edits applied
- 'delete_guard': delete a guard by ID`,
		"Every write, plus 'preview_rule' (which persists nothing), needs grafana-agento11y-app.eval:write, granted only by the Agento11y Admin role; an Editor token gets 403.",
		`
- Binding a new evaluator to production traffic with 'create_rule', after checking the blast radius with 'preview_rule'
- Adding a guard, or promoting one from warn to deny after watching its false-positive rate`,
		"",
	)
}

const manageAgento11yEvalCollectionsDescriptionFmt = `%s

Two linked resources:
- A saved conversation (/eval/saved-conversations) is a bookmark on one conversation, keyed by a saved_id you choose. It gives that conversation a stable ID, a name, and tags a collection can reference. It does not preserve the conversation: retention deletes the bookmark and its collection memberships together with the conversation it points at.
- A collection (/eval/collections) is a named group of saved conversations, used as the source material for offline evaluation. Collections hold saved conversations, never raw conversation IDs, so a conversation must already be bookmarked before a collection accepts it.

Operations:
- 'list_saved_conversations': bookmarked conversations in this tenant, filterable by source ('telemetry' for bookmarked production traffic, 'manual' for hand-built ones). Also reports total_count for the whole filtered set, not just the page
- 'get_saved_conversation': one bookmark by ID
- 'list_collections_for_saved_conversation': the collections one bookmark belongs to (unpaginated)
- 'list_collections': collections in this tenant, each with its member_count
- 'get_collection': one collection by ID
- 'list_collection_members': the saved conversations in a collection%s

Identifiers: saved_id is caller-chosen and accepts letters, digits, '_', '.', ':', and '-' (looser than the evaluator and rule IDs, which reject hyphens). collection_id is a UUID assigned by the server when a collection is created; it cannot be chosen.

List rows are already enriched: every saved conversation in 'list_saved_conversations' and 'list_collection_members' embeds the collections it belongs to plus generation_count, total_tokens, agent_names, models, model_providers, and tags. Read those fields instead of calling 'list_collections_for_saved_conversation' per row, which is one request per result. An absent collections field means the row was not enriched; an empty array means the row genuinely belongs to no collection.

Pagination: when a response carries next_cursor, call the same operation again with cursor set to it. Echo the value back exactly; never construct or increment one. 'list_saved_conversations' returns an opaque numeric value while 'list_collections' and 'list_collection_members' return the last row ID, so a cursor from one operation passed to another fails or silently skips rows. Keep the same source filter across pages.

Permissions: reads need grafana-agento11y-app.data:read (Agento11y Editor or Admin). %s

When to use:
- Reading what is already curated: which collections exist, how large they are, and what is in them%s

When NOT to use:
- Searching or reading live conversations and generations (use agento11y_manage_conversations and agento11y_manage_generations)
- Inspecting evaluators or the rules that schedule them (use agento11y_manage_evaluators and agento11y_manage_eval_rules)%s`

func manageAgento11yEvalCollectionsDescription(readOnly bool) string {
	if readOnly {
		return fmt.Sprintf(manageAgento11yEvalCollectionsDescriptionFmt,
			"Read the curated conversations of Grafana Agent Observability (the grafana-agento11y-app plugin): saved conversations and the collections that group them.",
			"",
			"This variant performs no writes.",
			"",
			"\n- Bookmarking a conversation, or creating and filling collections (read-only tool)",
		)
	}
	return fmt.Sprintf(manageAgento11yEvalCollectionsDescriptionFmt,
		"Manage the curated conversations of Grafana Agent Observability (the grafana-agento11y-app plugin): bookmark conversations as saved conversations and group them into collections.",
		`
- 'save_conversation': bookmark a live conversation by conversation_id. saved_id is optional and defaults to 'saved-<conversation_id>'; a conversation can only be saved once, so a repeat returns 409 naming the existing saved_id
- 'delete_saved_conversation': delete a bookmark by saved_id. Idempotent, and it also removes the bookmark from every collection it belonged to, with no separate membership cleanup. On a source='manual' bookmark the backend goes further and deletes the underlying conversation and its generations, so the content itself is gone
- 'create_collection': create an empty collection from a name and optional description. The response carries the server-assigned collection_id needed by the membership operations
- 'update_collection': patch a collection's name or description. Omitted fields are left unchanged, and an explicitly empty description clears it
- 'delete_collection': delete a collection and its memberships in one transaction. Idempotent, and the saved conversations themselves are kept
- 'add_collection_members': add saved_ids to a collection. Every ID must already be a saved conversation (a missing one returns 400 naming it), and re-adding an existing member is a no-op
- 'remove_collection_member': drop one saved conversation from a collection. Idempotent, and the bookmark itself is kept`,
		"Every write needs grafana-agento11y-app.eval:write, granted only by the Agento11y Admin role; an Editor token gets 403.",
		`
- Turning a triaged failure into a regression collection: 'save_conversation', then 'create_collection' or 'add_collection_members'
- Bookmarking a conversation found via agento11y_manage_conversations so a collection can reference it by a stable ID
- Collection hygiene: renaming a collection, or removing a conversation that no longer belongs in it`,
		"",
	)
}

const manageAgento11yExperimentsDescriptionFmt = `%s

An experiment is one offline run of an agent over a test suite. Each test case in the suite produces one or more trials, each trial is scored by the experiment's evaluators, and the experiment reports a pass rate. Experiments are created by SDK runners, not from here.

Operations:
- 'list': experiments in this tenant, filterable by suite_id, status, source, created_by, tag, and a created_at or completed_at window. Each row carries the same result summary as 'get', so finding the experiment that regressed needs no second call
- 'get': one experiment with its result summary: pass rate, average final score, total cost and tokens
- 'get_report': the per-test-case breakdown, trimmed by row_limit. The test case input and expected values, the score records, and the artifact records are dropped because those fields have no size bound; each trial keeps its error message, a score_count, an artifact_count, and the IDs the drill-downs take
- 'list_trials': one experiment's trials, paginated. Prefer this over 'get_report' on a large suite. It reports no cost or token counts: only the report path fills those in
- 'list_scores': every score in one experiment, paginated
- 'get_trial': one trial in full, including the test case snapshot with its input and expected values
- 'list_trial_scores': one trial's scores, with the explanation each judge wrote
- 'list_trial_artifacts': one trial's artifact metadata, with a content_ref rather than the bytes
- 'list_facets': the distinct suites, owners, and tags across every experiment in the tenant, for building a 'list' filter. Only source, from, and to narrow it; it rejects a filter it would otherwise have to ignore%s

Size: 'get_report' is fetched whole before it is trimmed, and a response above 10 MiB fails the call rather than arriving truncated.

Pagination: when a response carries next_cursor, call the same operation again with cursor set to it, repeating the first page's filters with absolute RFC3339 times. A relative bound such as now-7d re-resolves between calls and moves the window the cursor was issued against, so it is rejected alongside a cursor.

Permissions: reads need grafana-agento11y-app.data:read (Agento11y Editor or Admin). %s

When to use:
- Finding the last experiment for a suite after a suspected regression: 'list' by suite_id, then read the pass rate off the row
- Finding which test cases an experiment failed on, then reading one failing trial in full%s

When NOT to use:
- Reading scores on live production traffic (use agento11y_manage_generations and agento11y_manage_conversations)
- Inspecting the test cases a suite defines, or editing them (use agento11y_manage_test_suites)
- Inspecting what an evaluator checks (use agento11y_manage_evaluators)%s`

func manageAgento11yExperimentsDescription(readOnly bool) string {
	if readOnly {
		return fmt.Sprintf(manageAgento11yExperimentsDescriptionFmt,
			"Read the offline experiments of Grafana Agent Observability (the grafana-agento11y-app plugin), their trials, and their scores.",
			"",
			"This variant performs no writes.",
			"",
			"\n- Renaming, retagging, or stopping an experiment (read-only tool)",
		)
	}
	return fmt.Sprintf(manageAgento11yExperimentsDescriptionFmt,
		"Manage the offline experiments of Grafana Agent Observability (the grafana-agento11y-app plugin), their trials, and their scores.",
		`
- 'update': patch an experiment's name, description, tags, or metadata. Only the experiment's created_by may patch it, so patching an experiment someone else started answers 401
- 'cancel': stop a running experiment. It checks no owner, so any caller with the write permission can stop any experiment. An experiment that already finished is left alone: the call answers 200 and returns it unchanged instead of failing, so read the status on the result rather than assume a run was stopped`,
		"Both writes need grafana-agento11y-app.eval:write, granted only by the Agento11y Admin role; an Editor token gets 403.",
		`
- Labelling an experiment after triage, so 'list' by tag finds it later
- Stopping an experiment that is burning judge tokens on a broken candidate`,
		"",
	)
}

const manageAgento11yTestSuitesDescriptionFmt = `%s

A test suite is the input side of an offline experiment: a named set of test cases that an SDK runner replays against an agent. Suites are versioned, and a test case belongs to one version rather than to the suite, so every test case operation takes both suite_id and version.

A version is either a draft or published. A draft accepts test case edits; publishing freezes it and makes it the suite's latest_version, which is the version a runner picks up. A suite has at most one draft at a time.

Operations:
- 'list_suites': the test suites in this tenant, newest first. The rows carry no version history
- 'get_suite': one suite with its full version history under versions
- 'list_test_cases': the test cases of one suite version, oldest first, paginated
- 'get_test_case': one test case in full, with its free-form input and expected values%s

Pagination: when a response carries next_cursor, call the same operation again with cursor set to it.

Permissions: reads need grafana-agento11y-app.data:read (Agento11y Editor or Admin). %s

When to use:
- Reading the test cases at the version an experiment used, after it reported a failing case%s

When NOT to use:
- Reading how a suite scored, or the trials, scores, and artifacts behind it (use agento11y_manage_experiments)
- Changing what an evaluator checks (use agento11y_manage_evaluators)%s`

func manageAgento11yTestSuitesDescription(readOnly bool) string {
	if readOnly {
		return fmt.Sprintf(manageAgento11yTestSuitesDescriptionFmt,
			"Read the test suites of Grafana Agent Observability (the grafana-agento11y-app plugin), their versions, and their test cases.",
			"",
			"This variant performs no writes.",
			"",
			"\n- Editing a suite, its versions, or its test cases (read-only tool)",
		)
	}
	return fmt.Sprintf(manageAgento11yTestSuitesDescriptionFmt,
		"Manage the test suites of Grafana Agent Observability (the grafana-agento11y-app plugin), their versions, and their test cases.",
		`
- 'create_suite': a new empty suite. It has no version yet, so follow it with 'create_draft_version'
- 'update_suite': patch a suite's name, description, or tags
- 'create_draft_version': open a new editable version. A suite that already has a draft answers 409
- 'publish_version': freeze a draft. There is no unpublish; a published version answers 409 to a second publish and to every test case edit, so changing a published suite means a new draft
- 'upsert_test_case': write a whole test case into a draft version. It replaces the stored case rather than merging into it, so a field left out is cleared; read the case with 'get_test_case' first and send it back complete
- 'delete_test_case': remove one test case from a draft version. Deleting a test case that is already gone answers 404`,
		"Every write needs grafana-agento11y-app.eval:write, granted only by the Agento11y Admin role; an Editor token gets 403.",
		`
- Adding a regression case to a suite, then publishing the draft so the next experiment picks it up
- Correcting a test case whose expected value was wrong`,
		"",
	)
}

var ManageAgento11yEvaluatorsRead = mcpgrafana.MustTool(
	"agento11y_manage_evaluators",
	manageAgento11yEvaluatorsDescription(true),
	manageAgento11yEvaluatorsRead,
	mcp.WithTitleAnnotation("Manage Agent Observability evaluators"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
	mcp.WithDestructiveHintAnnotation(false),
	mcp.WithOpenWorldHintAnnotation(false),
)

var ManageAgento11yEvalRulesRead = mcpgrafana.MustTool(
	"agento11y_manage_eval_rules",
	manageAgento11yEvalRulesDescription(true),
	manageAgento11yEvalRulesRead,
	mcp.WithTitleAnnotation("Manage Agent Observability eval rules and guards"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
	mcp.WithDestructiveHintAnnotation(false),
	mcp.WithOpenWorldHintAnnotation(false),
)

var ManageAgento11yEvalCollectionsRead = mcpgrafana.MustTool(
	"agento11y_manage_eval_collections",
	manageAgento11yEvalCollectionsDescription(true),
	manageAgento11yEvalCollectionsRead,
	mcp.WithTitleAnnotation("Manage Agent Observability saved conversations and collections"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
	mcp.WithDestructiveHintAnnotation(false),
	mcp.WithOpenWorldHintAnnotation(false),
)

var ManageAgento11yExperimentsRead = mcpgrafana.MustTool(
	"agento11y_manage_experiments",
	manageAgento11yExperimentsDescription(true),
	manageAgento11yExperimentsRead,
	mcp.WithTitleAnnotation("Manage Agent Observability experiments"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
	mcp.WithDestructiveHintAnnotation(false),
	mcp.WithOpenWorldHintAnnotation(false),
)

var ManageAgento11yTestSuitesRead = mcpgrafana.MustTool(
	"agento11y_manage_test_suites",
	manageAgento11yTestSuitesDescription(true),
	manageAgento11yTestSuitesRead,
	mcp.WithTitleAnnotation("Manage Agent Observability test suites"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
	mcp.WithDestructiveHintAnnotation(false),
	mcp.WithOpenWorldHintAnnotation(false),
)

var ManageAgento11yEvaluatorsReadWrite = mcpgrafana.MustTool(
	"agento11y_manage_evaluators",
	manageAgento11yEvaluatorsDescription(false),
	manageAgento11yEvaluatorsReadWrite,
	mcp.WithTitleAnnotation("Manage Agent Observability evaluators"),
	mcp.WithReadOnlyHintAnnotation(false),
	mcp.WithDestructiveHintAnnotation(true),
	mcp.WithOpenWorldHintAnnotation(false),
)

var ManageAgento11yEvalRulesReadWrite = mcpgrafana.MustTool(
	"agento11y_manage_eval_rules",
	manageAgento11yEvalRulesDescription(false),
	manageAgento11yEvalRulesReadWrite,
	mcp.WithTitleAnnotation("Manage Agent Observability eval rules and guards"),
	mcp.WithReadOnlyHintAnnotation(false),
	mcp.WithDestructiveHintAnnotation(true),
	mcp.WithOpenWorldHintAnnotation(false),
)

var ManageAgento11yEvalCollectionsReadWrite = mcpgrafana.MustTool(
	"agento11y_manage_eval_collections",
	manageAgento11yEvalCollectionsDescription(false),
	manageAgento11yEvalCollectionsReadWrite,
	mcp.WithTitleAnnotation("Manage Agent Observability saved conversations and collections"),
	mcp.WithReadOnlyHintAnnotation(false),
	mcp.WithDestructiveHintAnnotation(true),
	mcp.WithOpenWorldHintAnnotation(false),
)

var ManageAgento11yExperimentsReadWrite = mcpgrafana.MustTool(
	"agento11y_manage_experiments",
	manageAgento11yExperimentsDescription(false),
	manageAgento11yExperimentsReadWrite,
	mcp.WithTitleAnnotation("Manage Agent Observability experiments"),
	mcp.WithReadOnlyHintAnnotation(false),
	mcp.WithDestructiveHintAnnotation(true),
	mcp.WithOpenWorldHintAnnotation(false),
)

var ManageAgento11yTestSuitesReadWrite = mcpgrafana.MustTool(
	"agento11y_manage_test_suites",
	manageAgento11yTestSuitesDescription(false),
	manageAgento11yTestSuitesReadWrite,
	mcp.WithTitleAnnotation("Manage Agent Observability test suites"),
	mcp.WithReadOnlyHintAnnotation(false),
	mcp.WithDestructiveHintAnnotation(true),
	mcp.WithOpenWorldHintAnnotation(false),
)

func AddAgento11yTools(mcp *server.MCPServer, enableWriteTools bool) {
	ManageAgento11yConversations.Register(mcp)
	ManageAgento11yGenerations.Register(mcp)
	ManageAgento11yAgents.Register(mcp)
	if enableWriteTools {
		ManageAgento11yEvaluatorsReadWrite.Register(mcp)
		ManageAgento11yEvalRulesReadWrite.Register(mcp)
		ManageAgento11yEvalCollectionsReadWrite.Register(mcp)
		ManageAgento11yExperimentsReadWrite.Register(mcp)
		ManageAgento11yTestSuitesReadWrite.Register(mcp)
	} else {
		ManageAgento11yEvaluatorsRead.Register(mcp)
		ManageAgento11yEvalRulesRead.Register(mcp)
		ManageAgento11yEvalCollectionsRead.Register(mcp)
		ManageAgento11yExperimentsRead.Register(mcp)
		ManageAgento11yTestSuitesRead.Register(mcp)
	}
}
