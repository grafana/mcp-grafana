//go:build cloud
// +build cloud

// This file contains cloud integration tests that run against a Grafana
// instance with the AI Observability (Sigil) plugin installed, configured via
// AI_OBSERVABILITY_GRAFANA_URL and AI_OBSERVABILITY_GRAFANA_SERVICE_ACCOUNT_TOKEN
// (AI_OBSERVABILITY_GRAFANA_API_KEY is the deprecated fallback). These tests
// expect this configuration to exist and will skip if the required environment
// variables are not set. The instance is not required to contain AI
// Observability data: an empty but valid response passes. Subtests that need
// data to assert anything skip themselves when the instance is empty.
//
// CI does not set AI_OBSERVABILITY_GRAFANA_URL yet, so this test is
// intentionally manual-only for now: run it against your own Grafana instance
// with the grafana-sigil-app plugin installed.

package tools

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAIObservabilityCloudIntegration(t *testing.T) {
	ctx := createCloudTestContext(t, "AIObservability", "AI_OBSERVABILITY_GRAFANA_URL", "AI_OBSERVABILITY_GRAFANA_API_KEY")

	t.Run("list conversations", func(t *testing.T) {
		result, err := manageAIObservabilityConversations(ctx, ManageAIObservabilityConversationsParams{
			Operation: "list",
		})
		require.NoError(t, err, "Failed to list AI Observability conversations")
		require.NotNil(t, result)

		resp, ok := result.(*aiObservabilityListResponse[AIObservabilityConversation])
		require.True(t, ok, "list should return *aiObservabilityListResponse[AIObservabilityConversation]")
		for _, conv := range resp.Items {
			assert.NotEmpty(t, conv.ID, "listed conversation should have an id")
		}
	})

	t.Run("search respects page size", func(t *testing.T) {
		result, err := manageAIObservabilityConversations(ctx, ManageAIObservabilityConversationsParams{
			Operation: "search",
			Limit:     5,
		})
		require.NoError(t, err, "Failed to search AI Observability conversations")

		resp, ok := result.(*AIObservabilitySearchResponse)
		require.True(t, ok)
		assert.LessOrEqual(t, len(resp.Conversations), 5, "search should not return more than the requested page size")
	})

	t.Run("search with filter expression and explicit RFC3339 range", func(t *testing.T) {
		end := time.Now()
		start := end.Add(-7 * 24 * time.Hour)
		result, err := manageAIObservabilityConversations(ctx, ManageAIObservabilityConversationsParams{
			Operation: "search",
			Filters:   `status = "error"`,
			StartTime: start.Format(time.RFC3339),
			EndTime:   end.Format(time.RFC3339),
			Limit:     10,
		})
		// A valid filter with no matches returns an empty result, not an error.
		// A failure here means the documented filter syntax was rejected by the backend.
		require.NoError(t, err, "search with a documented filter expression should be accepted")

		resp, ok := result.(*AIObservabilitySearchResponse)
		require.True(t, ok)
		for _, conv := range resp.Conversations {
			assert.NotEmpty(t, conv.ConversationID, "search result should have a conversation_id")
		}
	})

	t.Run("search pagination cursor round-trip", func(t *testing.T) {
		// Pin an explicit window and reuse it for both calls. The cursor encodes
		// the filters and time range, so a relative "now-30d" that re-resolves on
		// the second call shifts the window and the backend rejects the cursor with
		// "cursor no longer matches current filters".
		end := time.Now()
		start := end.Add(-30 * 24 * time.Hour)
		startStr := start.Format(time.RFC3339)
		endStr := end.Format(time.RFC3339)

		first, err := manageAIObservabilityConversations(ctx, ManageAIObservabilityConversationsParams{
			Operation: "search",
			StartTime: startStr,
			EndTime:   endStr,
			Limit:     1,
		})
		require.NoError(t, err)
		resp, ok := first.(*AIObservabilitySearchResponse)
		require.True(t, ok)
		if !resp.HasMore || resp.NextCursor == "" {
			t.Log("fewer than two conversations in the window, skipping cursor round-trip")
			return
		}

		second, err := manageAIObservabilityConversations(ctx, ManageAIObservabilityConversationsParams{
			Operation: "search",
			StartTime: startStr,
			EndTime:   endStr,
			Limit:     1,
			Cursor:    resp.NextCursor,
		})
		require.NoError(t, err, "following next_cursor with identical filters should succeed")
		require.NotNil(t, second)
	})

	t.Run("drill down from search into generation and scores", func(t *testing.T) {
		result, err := manageAIObservabilityConversations(ctx, ManageAIObservabilityConversationsParams{
			Operation: "search",
			StartTime: "now-30d",
			Limit:     5,
		})
		require.NoError(t, err, "Failed to search AI Observability conversations")

		resp, ok := result.(*AIObservabilitySearchResponse)
		require.True(t, ok)
		if len(resp.Conversations) == 0 {
			t.Log("no conversations in the last 30d, skipping drill-down")
			return
		}

		convID := resp.Conversations[0].ConversationID
		require.NotEmpty(t, convID, "search result must carry a conversation_id to drill down")

		detailResult, err := manageAIObservabilityConversations(ctx, ManageAIObservabilityConversationsParams{
			Operation:      "get",
			ConversationID: convID,
		})
		require.NoError(t, err, "Failed to get conversation %s", convID)

		detail, ok := detailResult.(map[string]any)
		require.True(t, ok)
		assert.Equal(t, convID, detail["conversation_id"], "fetched conversation id should match the requested id")

		generations, ok := detail["generations"].([]any)
		if !ok || len(generations) == 0 {
			t.Log("conversation has no generations, skipping generation drill-down")
			return
		}
		generation, ok := generations[0].(map[string]any)
		require.True(t, ok)
		genID, ok := generation["generation_id"].(string)
		require.True(t, ok, "generation has no string generation_id")
		require.NotEmpty(t, genID)

		genResult, err := manageAIObservabilityGenerations(ctx, ManageAIObservabilityGenerationsParams{
			Operation:    "get",
			GenerationID: genID,
		})
		require.NoError(t, err, "Failed to get generation %s", genID)

		genDetail, ok := genResult.(map[string]any)
		require.True(t, ok)
		assert.Equal(t, genID, genDetail["generation_id"], "fetched generation id should match the requested id")

		scoresResult, err := manageAIObservabilityGenerations(ctx, ManageAIObservabilityGenerationsParams{
			Operation:    "scores",
			GenerationID: genID,
			Limit:        10,
		})
		require.NoError(t, err, "Failed to get scores for generation %s", genID)

		scores, ok := scoresResult.(*aiObservabilityListResponse[AIObservabilityScore])
		require.True(t, ok)
		assert.LessOrEqual(t, len(scores.Items), 10, "scores should not exceed the requested limit")
		for _, score := range scores.Items {
			assert.Equal(t, genID, score.GenerationID, "score should belong to the requested generation")
			assert.NotEmpty(t, score.ScoreKey, "score should have a score_key")
		}
	})
}
