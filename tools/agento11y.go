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

// AddAgento11yTools registers agento11y_read (always), and agento11y_evals_read
// plus, when write tools are enabled, agento11y_evals_write. agento11y_read has
// no write counterpart: agents, conversations, and generations are all derived
// from ingested telemetry, with no mutating API behind any of them.
func AddAgento11yTools(mcp *server.MCPServer, enableWriteTools bool) {
	Agento11yRead.Register(mcp)
	Agento11yEvalsRead.Register(mcp)
	if enableWriteTools {
		Agento11yEvalsWrite.Register(mcp)
	}
}
