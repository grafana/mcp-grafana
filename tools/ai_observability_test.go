//go:build unit
// +build unit

package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	mcpgrafana "github.com/grafana/mcp-grafana"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupMockAIObservabilityServer(handler http.HandlerFunc) (*httptest.Server, context.Context) {
	server := httptest.NewServer(handler)
	config := mcpgrafana.GrafanaConfig{
		URL:    server.URL,
		APIKey: "test-api-key",
	}
	ctx := mcpgrafana.WithGrafanaConfig(context.Background(), config)
	return server, ctx
}

func TestAIObservabilityManageConversations(t *testing.T) {
	testCases := []struct {
		name        string
		params      ManageAIObservabilityConversationsParams
		handler     func(t *testing.T, w http.ResponseWriter, r *http.Request) // nil: server must not be called
		wantErr     string
		checkResult func(t *testing.T, result any)
	}{
		{
			name:   "list recent conversations",
			params: ManageAIObservabilityConversationsParams{Operation: "list"},
			handler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				require.Equal(t, http.MethodGet, r.Method)
				require.Equal(t, "/api/plugins/grafana-sigil-app/resources/query/conversations", r.URL.Path)
				require.Equal(t, "Bearer test-api-key", r.Header.Get("Authorization"))
				require.Empty(t, r.URL.RawQuery)

				w.Header().Set("Content-Type", "application/json")
				_, err := w.Write([]byte(`{"items":[{
					"id": "conv-1",
					"title": "Hello",
					"generation_count": 2,
					"annotation_summary": {"annotation_count": 3, "latest_annotation_type": "note", "latest_annotated_at": "2025-04-23T10:00:00Z"}
				}]}`))
				require.NoError(t, err)
			},
			checkResult: func(t *testing.T, result any) {
				resp, ok := result.(*aiObservabilityListResponse[AIObservabilityConversation])
				require.True(t, ok)
				require.Len(t, resp.Items, 1)
				assert.Equal(t, "conv-1", resp.Items[0].ID)
				assert.Equal(t, "Hello", resp.Items[0].Title)
				assert.Equal(t, 2, resp.Items[0].GenerationCount)
				require.NotNil(t, resp.Items[0].AnnotationSummary)
				assert.Equal(t, 3, resp.Items[0].AnnotationSummary.AnnotationCount)
				assert.Equal(t, "note", resp.Items[0].AnnotationSummary.LatestAnnotationType)
			},
		},
		{
			name:   "list does not send limit and cursor",
			params: ManageAIObservabilityConversationsParams{Operation: "list", Limit: 10, Cursor: "abc"},
			handler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				require.Empty(t, r.URL.RawQuery)

				w.Header().Set("Content-Type", "application/json")
				_, err := w.Write([]byte(`{"items":[]}`))
				require.NoError(t, err)
			},
		},
		{
			name: "search with filters and concrete time range",
			params: ManageAIObservabilityConversationsParams{
				Operation: "search",
				Filters:   `status = "error"`,
				StartTime: "2025-04-23T10:00:00Z",
				EndTime:   "2025-04-23T11:00:00Z",
				Limit:     25,
				Cursor:    "cursor-1",
			},
			handler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				require.Equal(t, http.MethodPost, r.Method)
				require.Equal(t, "/api/plugins/grafana-sigil-app/resources/query/conversations/search", r.URL.Path)
				require.Equal(t, "Bearer test-api-key", r.Header.Get("Authorization"))
				require.Equal(t, "application/json", r.Header.Get("Content-Type"))

				var req AIObservabilitySearchRequest
				require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
				assert.Equal(t, `status = "error"`, req.Filters)
				assert.Equal(t, 25, req.PageSize)
				assert.Equal(t, "cursor-1", req.Cursor)
				require.NotNil(t, req.TimeRange)
				assert.True(t, req.TimeRange.From.Equal(time.Date(2025, 4, 23, 10, 0, 0, 0, time.UTC)))
				assert.True(t, req.TimeRange.To.Equal(time.Date(2025, 4, 23, 11, 0, 0, 0, time.UTC)))

				w.Header().Set("Content-Type", "application/json")
				_, err := w.Write([]byte(`{
					"conversations": [{
						"conversation_id": "conv-1",
						"conversation_title": "Broken run",
						"generation_count": 3,
						"models": ["claude-opus-4-6"],
						"agents": ["claude-code"],
						"error_count": 2,
						"has_errors": true,
						"trace_ids": ["trace-1"],
						"rating_summary": {"total_count": 1, "good_count": 0, "bad_count": 1, "latest_rated_at": "2025-04-23T10:30:00Z", "latest_bad_at": "2025-04-23T10:30:00Z", "has_bad_rating": true},
						"annotation_count": 0,
						"eval_summary": {"total_scores": 4, "pass_count": 3, "fail_count": 1}
					}],
					"next_cursor": "next-tok",
					"has_more": true
				}`))
				require.NoError(t, err)
			},
			checkResult: func(t *testing.T, result any) {
				resp, ok := result.(*AIObservabilitySearchResponse)
				require.True(t, ok)
				require.Len(t, resp.Conversations, 1)
				conv := resp.Conversations[0]
				assert.Equal(t, "conv-1", conv.ConversationID)
				assert.Equal(t, 2, conv.ErrorCount)
				assert.True(t, conv.HasErrors)
				require.NotNil(t, conv.RatingSummary)
				assert.True(t, conv.RatingSummary.HasBadRating)
				assert.True(t, conv.RatingSummary.LatestRatedAt.Equal(time.Date(2025, 4, 23, 10, 30, 0, 0, time.UTC)))
				assert.True(t, conv.RatingSummary.LatestBadAt.Equal(time.Date(2025, 4, 23, 10, 30, 0, 0, time.UTC)))
				require.NotNil(t, conv.EvalSummary)
				assert.Equal(t, 1, conv.EvalSummary.FailCount)
				cursor, cursorContext, err := decodeAIObservabilitySearchCursor(resp.NextCursor)
				require.NoError(t, err)
				assert.Equal(t, "next-tok", cursor)
				require.NotNil(t, cursorContext)
				assert.Equal(t, `status = "error"`, cursorContext.Filters)
				assert.Equal(t, 25, cursorContext.PageSize)
				require.NotNil(t, cursorContext.TimeRange)
				assert.True(t, cursorContext.TimeRange.From.Equal(time.Date(2025, 4, 23, 10, 0, 0, 0, time.UTC)))
				assert.True(t, cursorContext.TimeRange.To.Equal(time.Date(2025, 4, 23, 11, 0, 0, 0, time.UTC)))
				assert.True(t, resp.HasMore)
			},
		},
		{
			name:   "search defaults to last 24 hours",
			params: ManageAIObservabilityConversationsParams{Operation: "search"},
			handler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				var req AIObservabilitySearchRequest
				require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
				require.NotNil(t, req.TimeRange)
				assert.WithinDuration(t, time.Now(), req.TimeRange.To, time.Minute)
				assert.WithinDuration(t, time.Now().Add(-24*time.Hour), req.TimeRange.From, time.Minute)
				assert.Equal(t, 50, req.PageSize)

				w.Header().Set("Content-Type", "application/json")
				_, err := w.Write([]byte(`{"conversations":[],"has_more":false}`))
				require.NoError(t, err)
			},
		},
		{
			name:   "get conversation detail",
			params: ManageAIObservabilityConversationsParams{Operation: "get", ConversationID: "conv-123"},
			handler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				require.Equal(t, http.MethodGet, r.Method)
				require.Equal(t, "/api/plugins/grafana-sigil-app/resources/query/conversations/conv-123", r.URL.Path)

				w.Header().Set("Content-Type", "application/json")
				_, err := w.Write([]byte(`{"id":"conv-123","generations":[{"id":"gen-1"}]}`))
				require.NoError(t, err)
			},
			checkResult: func(t *testing.T, result any) {
				detail, ok := result.(map[string]any)
				require.True(t, ok)
				assert.Equal(t, "conv-123", detail["id"])
				assert.Len(t, detail["generations"], 1)
			},
		},
		{
			name:    "get without conversation_id",
			params:  ManageAIObservabilityConversationsParams{Operation: "get"},
			wantErr: "conversation_id is required",
		},
		{
			name:    "unknown operation",
			params:  ManageAIObservabilityConversationsParams{Operation: "delete"},
			wantErr: "unknown operation",
		},
		{
			name:    "search with invalid start time",
			params:  ManageAIObservabilityConversationsParams{Operation: "search", StartTime: "not-a-date"},
			wantErr: "parsing start_time",
		},
		{
			name:   "upstream error is returned",
			params: ManageAIObservabilityConversationsParams{Operation: "list"},
			handler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusForbidden)
				_, err := w.Write([]byte(`{"error":"missing grafana-sigil-app.conversations:read"}`))
				require.NoError(t, err)
			},
			wantErr: "request failed with status 403",
		},
		{
			name:   "oversized response is rejected",
			params: ManageAIObservabilityConversationsParams{Operation: "get", ConversationID: "conv-big"},
			handler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				_, err := w.Write(make([]byte, defaultResponseLimitBytes+1))
				require.NoError(t, err)
			},
			wantErr: "exceeds maximum size",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			server, ctx := setupMockAIObservabilityServer(func(w http.ResponseWriter, r *http.Request) {
				if tc.handler == nil {
					t.Error("server should not be called for validation failures")
					return
				}
				tc.handler(t, w, r)
			})
			defer server.Close()

			result, err := manageAIObservabilityConversations(ctx, tc.params)
			if tc.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.wantErr)
				return
			}
			require.NoError(t, err)
			if tc.checkResult != nil {
				tc.checkResult(t, result)
			}
		})
	}
}

func TestAIObservabilitySearchCursorPreservesContext(t *testing.T) {
	start := time.Date(2025, 4, 23, 10, 0, 0, 0, time.UTC)
	end := time.Date(2025, 4, 23, 11, 0, 0, 0, time.UTC)
	var nextCursor string
	requestNumber := 0

	server, ctx := setupMockAIObservabilityServer(func(w http.ResponseWriter, r *http.Request) {
		requestNumber++
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/api/plugins/grafana-sigil-app/resources/query/conversations/search", r.URL.Path)

		var req AIObservabilitySearchRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		assert.Equal(t, `status = "error"`, req.Filters)
		assert.Equal(t, 1, req.PageSize)
		require.NotNil(t, req.TimeRange)
		assert.True(t, req.TimeRange.From.Equal(start))
		assert.True(t, req.TimeRange.To.Equal(end))

		w.Header().Set("Content-Type", "application/json")
		switch requestNumber {
		case 1:
			assert.Empty(t, req.Cursor)
			_, err := w.Write([]byte(`{"conversations":[],"next_cursor":"backend-cursor-1","has_more":true}`))
			require.NoError(t, err)
		case 2:
			assert.Equal(t, "backend-cursor-1", req.Cursor)
			_, err := w.Write([]byte(`{"conversations":[],"has_more":false}`))
			require.NoError(t, err)
		default:
			t.Fatalf("unexpected request %d", requestNumber)
		}
	})
	defer server.Close()

	first, err := manageAIObservabilityConversations(ctx, ManageAIObservabilityConversationsParams{
		Operation: "search",
		Filters:   `status = "error"`,
		StartTime: start.Format(time.RFC3339),
		EndTime:   end.Format(time.RFC3339),
		Limit:     1,
	})
	require.NoError(t, err)
	firstResp, ok := first.(*AIObservabilitySearchResponse)
	require.True(t, ok)
	require.NotEmpty(t, firstResp.NextCursor)
	nextCursor = firstResp.NextCursor

	_, err = manageAIObservabilityConversations(ctx, ManageAIObservabilityConversationsParams{
		Operation: "search",
		Cursor:    nextCursor,
	})
	require.NoError(t, err)
	assert.Equal(t, 2, requestNumber)
}

func TestAIObservabilityManageGenerations(t *testing.T) {
	testCases := []struct {
		name        string
		params      ManageAIObservabilityGenerationsParams
		handler     func(t *testing.T, w http.ResponseWriter, r *http.Request) // nil: server must not be called
		wantErr     string
		checkResult func(t *testing.T, result any)
	}{
		{
			name:   "get generation detail",
			params: ManageAIObservabilityGenerationsParams{Operation: "get", GenerationID: "gen-123"},
			handler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				require.Equal(t, http.MethodGet, r.Method)
				require.Equal(t, "/api/plugins/grafana-sigil-app/resources/query/generations/gen-123", r.URL.Path)
				require.Equal(t, "Bearer test-api-key", r.Header.Get("Authorization"))

				w.Header().Set("Content-Type", "application/json")
				_, err := w.Write([]byte(`{"id":"gen-123","model":{"name":"claude-opus-4-6"},"status":"error"}`))
				require.NoError(t, err)
			},
			checkResult: func(t *testing.T, result any) {
				detail, ok := result.(map[string]any)
				require.True(t, ok)
				assert.Equal(t, "gen-123", detail["id"])
				assert.Equal(t, "error", detail["status"])
			},
		},
		{
			name:   "get generation scores",
			params: ManageAIObservabilityGenerationsParams{Operation: "scores", GenerationID: "gen-123"},
			handler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				require.Equal(t, http.MethodGet, r.Method)
				require.Equal(t, "/api/plugins/grafana-sigil-app/resources/query/generations/gen-123/scores", r.URL.Path)
				require.Equal(t, "50", r.URL.Query().Get("limit"))

				w.Header().Set("Content-Type", "application/json")
				_, err := w.Write([]byte(`{
					"items": [{
						"score_id": "score-1",
						"generation_id": "gen-123",
						"evaluator_id": "eval-1",
						"evaluator_version": "v1",
						"score_key": "helpfulness",
						"score_type": "number",
						"value": {"number": 0.9},
						"passed": true,
						"explanation": "response addressed the question"
					}],
					"next_cursor": "score-tok"
				}`))
				require.NoError(t, err)
			},
			checkResult: func(t *testing.T, result any) {
				resp, ok := result.(*aiObservabilityListResponse[AIObservabilityScore])
				require.True(t, ok)
				require.Len(t, resp.Items, 1)
				score := resp.Items[0]
				assert.Equal(t, "eval-1", score.EvaluatorID)
				assert.Equal(t, "helpfulness", score.ScoreKey)
				assert.Equal(t, "number", score.ScoreType)
				require.NotNil(t, score.Value.Number)
				assert.Equal(t, 0.9, *score.Value.Number)
				require.NotNil(t, score.Passed)
				assert.True(t, *score.Passed)
				assert.Equal(t, "response addressed the question", score.Explanation)
				assert.Equal(t, "score-tok", resp.NextCursor)
			},
		},
		{
			name:   "scores passes limit and cursor through",
			params: ManageAIObservabilityGenerationsParams{Operation: "scores", GenerationID: "gen-123", Limit: 5, Cursor: "tok-2"},
			handler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				require.Equal(t, "5", r.URL.Query().Get("limit"))
				require.Equal(t, "tok-2", r.URL.Query().Get("cursor"))

				w.Header().Set("Content-Type", "application/json")
				_, err := w.Write([]byte(`{"items":[]}`))
				require.NoError(t, err)
			},
		},
		{
			name:    "get without generation_id",
			params:  ManageAIObservabilityGenerationsParams{Operation: "get"},
			wantErr: "generation_id is required",
		},
		{
			name:    "scores without generation_id",
			params:  ManageAIObservabilityGenerationsParams{Operation: "scores"},
			wantErr: "generation_id is required",
		},
		{
			name:    "unknown operation",
			params:  ManageAIObservabilityGenerationsParams{Operation: "list", GenerationID: "gen-123"},
			wantErr: "unknown operation",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			server, ctx := setupMockAIObservabilityServer(func(w http.ResponseWriter, r *http.Request) {
				if tc.handler == nil {
					t.Error("server should not be called for validation failures")
					return
				}
				tc.handler(t, w, r)
			})
			defer server.Close()

			result, err := manageAIObservabilityGenerations(ctx, tc.params)
			if tc.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.wantErr)
				return
			}
			require.NoError(t, err)
			if tc.checkResult != nil {
				tc.checkResult(t, result)
			}
		})
	}
}
