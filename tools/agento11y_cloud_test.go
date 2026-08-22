//go:build cloud
// +build cloud

// This file contains cloud integration tests that run against a Grafana
// instance with the Agent Observability plugin installed, configured via
// AGENTO11Y_GRAFANA_URL and AGENTO11Y_GRAFANA_SERVICE_ACCOUNT_TOKEN
// (AGENTO11Y_GRAFANA_API_KEY is the deprecated fallback). These tests
// expect this configuration to exist and will skip if the required environment
// variables are not set. The instance is not required to contain AI
// Observability data: an empty but valid response passes. Subtests that need
// data to assert anything skip themselves when the instance is empty.
//
// CI does not set AGENTO11Y_GRAFANA_URL yet, so this test is
// intentionally manual-only for now: run it against your own Grafana instance
// with the grafana-agento11y-app plugin installed.

package tools

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAgento11yCloudIntegration(t *testing.T) {
	ctx := createCloudTestContext(t, "Agento11y", "AGENTO11Y_GRAFANA_URL", "AGENTO11Y_GRAFANA_API_KEY")

	t.Run("list conversations", func(t *testing.T) {
		result, err := agento11yRead(ctx, Agento11yReadParams{
			Operation: "list_conversations",
		})
		require.NoError(t, err, "Failed to list Agent Observability conversations")
		require.NotNil(t, result)

		resp, ok := result.(*agento11yListResponse[Agento11yConversation])
		require.True(t, ok, "list_conversations should return *agento11yListResponse[Agento11yConversation]")
		for _, conv := range resp.Items {
			assert.NotEmpty(t, conv.ID, "listed conversation should have an id")
		}
	})

	t.Run("search respects page size", func(t *testing.T) {
		result, err := agento11yRead(ctx, Agento11yReadParams{
			Operation: "search_conversations",
			Limit:     5,
		})
		require.NoError(t, err, "Failed to search Agent Observability conversations")

		resp, ok := result.(*Agento11ySearchResponse)
		require.True(t, ok)
		assert.LessOrEqual(t, len(resp.Conversations), 5, "search should not return more than the requested page size")
	})

	t.Run("search with filter expression and explicit RFC3339 range", func(t *testing.T) {
		end := time.Now()
		start := end.Add(-7 * 24 * time.Hour)
		result, err := agento11yRead(ctx, Agento11yReadParams{
			Operation: "search_conversations",
			Filters:   `status = "error"`,
			StartTime: start.Format(time.RFC3339),
			EndTime:   end.Format(time.RFC3339),
			Limit:     10,
		})
		// A valid filter with no matches returns an empty result, not an error.
		// A failure here means the documented filter syntax was rejected by the backend.
		require.NoError(t, err, "search with a documented filter expression should be accepted")

		resp, ok := result.(*Agento11ySearchResponse)
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

		first, err := agento11yRead(ctx, Agento11yReadParams{
			Operation: "search_conversations",
			StartTime: startStr,
			EndTime:   endStr,
			Limit:     1,
		})
		require.NoError(t, err)
		resp, ok := first.(*Agento11ySearchResponse)
		require.True(t, ok)
		if !resp.HasMore || resp.NextCursor == "" {
			t.Log("fewer than two conversations in the window, skipping cursor round-trip")
			return
		}

		second, err := agento11yRead(ctx, Agento11yReadParams{
			Operation: "search_conversations",
			StartTime: startStr,
			EndTime:   endStr,
			Limit:     1,
			Cursor:    resp.NextCursor,
		})
		require.NoError(t, err, "following next_cursor with identical filters should succeed")
		require.NotNil(t, second)
	})

	t.Run("drill down from search into generation and scores", func(t *testing.T) {
		result, err := agento11yRead(ctx, Agento11yReadParams{
			Operation: "search_conversations",
			StartTime: "now-30d",
			Limit:     5,
		})
		require.NoError(t, err, "Failed to search Agent Observability conversations")

		resp, ok := result.(*Agento11ySearchResponse)
		require.True(t, ok)
		if len(resp.Conversations) == 0 {
			t.Log("no conversations in the last 30d, skipping drill-down")
			return
		}

		convID := resp.Conversations[0].ConversationID
		require.NotEmpty(t, convID, "search result must carry a conversation_id to drill down")

		detailResult, err := agento11yRead(ctx, Agento11yReadParams{
			Operation:      "get_conversation",
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

		genResult, err := agento11yRead(ctx, Agento11yReadParams{
			Operation:    "get_generation",
			GenerationID: genID,
		})
		require.NoError(t, err, "Failed to get generation %s", genID)

		genDetail, ok := genResult.(map[string]any)
		require.True(t, ok)
		assert.Equal(t, genID, genDetail["generation_id"], "fetched generation id should match the requested id")

		scoresResult, err := agento11yRead(ctx, Agento11yReadParams{
			Operation:    "list_generation_scores",
			GenerationID: genID,
			Limit:        10,
		})
		require.NoError(t, err, "Failed to get scores for generation %s", genID)

		scores, ok := scoresResult.(*agento11yListResponse[Agento11yScore])
		require.True(t, ok)
		assert.LessOrEqual(t, len(scores.Items), 10, "scores should not exceed the requested limit")
		for _, score := range scores.Items {
			assert.Equal(t, genID, score.GenerationID, "score should belong to the requested generation")
			assert.NotEmpty(t, score.ScoreKey, "score should have a score_key")
		}
	})

	t.Run("list agents and drill into one", func(t *testing.T) {
		result, err := agento11yRead(ctx, Agento11yReadParams{
			Operation: "list_agents",
			Limit:     10,
		})
		require.NoError(t, err, "Failed to list Agent Observability agents")

		resp, ok := result.(*agento11yListResponse[Agento11yAgent])
		require.True(t, ok, "list_agents should return *agento11yListResponse[Agento11yAgent]")
		assert.LessOrEqual(t, len(resp.Items), 10, "list_agents should respect the requested limit")
		if len(resp.Items) == 0 {
			t.Log("no agents in the catalog on this instance, skipping agent assertions")
			return
		}
		for _, agent := range resp.Items {
			assert.NotEmpty(t, agent.LatestEffectiveVersion, "listed agent should have a latest_effective_version")
		}

		// The first name can legitimately be empty (the unnamed agent), and
		// passing it through unchanged must still address that agent.
		name := resp.Items[0].AgentName
		detailResult, err := agento11yRead(ctx, Agento11yReadParams{
			Operation: "get_agent",
			AgentName: &name,
		})
		require.NoError(t, err, "Failed to get agent %q", name)

		detail, ok := detailResult.(map[string]any)
		require.True(t, ok)
		assert.Equal(t, name, detail["agent_name"], "fetched agent name should match the requested name")
		assert.NotEmpty(t, detail["effective_version"], "agent detail should carry an effective_version")

		versionsResult, err := agento11yRead(ctx, Agento11yReadParams{
			Operation: "list_agent_versions",
			AgentName: &name,
			Limit:     10,
		})
		require.NoError(t, err, "Failed to list versions of agent %q", name)

		versions, ok := versionsResult.(*agento11yListResponse[Agento11yAgentVersion])
		require.True(t, ok, "list_agent_versions should return *agento11yListResponse[Agento11yAgentVersion]")
		assert.LessOrEqual(t, len(versions.Items), 10, "list_agent_versions should respect the requested limit")
		for _, version := range versions.Items {
			assert.NotEmpty(t, version.EffectiveVersion, "listed version should have an effective_version")
		}

		// Score aggregates need a non-blank name, and an agent with no scored
		// generations returns an empty list rather than an error.
		if strings.TrimSpace(name) == "" {
			t.Log("first catalog agent is the unnamed agent, skipping version scores")
			return
		}
		scoresResult, err := agento11yRead(ctx, Agento11yReadParams{
			Operation: "list_agent_version_scores",
			AgentName: &name,
			StartTime: time.Now().Add(-30 * 24 * time.Hour).Format(time.RFC3339),
			EndTime:   time.Now().Format(time.RFC3339),
		})
		require.NoError(t, err, "Failed to get version scores of agent %q", name)

		scores, ok := scoresResult.(*agento11yListResponse[Agento11yAgentVersionScore])
		require.True(t, ok, "list_agent_version_scores should return *agento11yListResponse[Agento11yAgentVersionScore]")
		for _, item := range scores.Items {
			assert.NotEmpty(t, item.EffectiveVersion, "score aggregate should name the version it belongs to")
			for _, evaluator := range item.Evaluators {
				assert.NotEmpty(t, evaluator.ScoreKey, "score aggregate evaluator should have a score_key")
			}
		}
	})

	t.Run("list evaluators", func(t *testing.T) {
		result, err := agento11yEvalsRead(ctx, Agento11yEvalsReadParams{
			Operation: "list_evaluators",
		})
		require.NoError(t, err, "Failed to list Agent Observability evaluators")

		resp, ok := result.(*agento11yListResponse[Agento11yEvaluatorDefinition])
		require.True(t, ok, "list_evaluators should return *agento11yListResponse[Agento11yEvaluatorDefinition]")
		if len(resp.Items) == 0 {
			t.Log("no evaluators on this instance, skipping evaluator assertions")
			return
		}
		for _, evaluator := range resp.Items {
			assert.NotEmpty(t, evaluator.EvaluatorID, "listed evaluator should have an evaluator_id")
			assert.NotEmpty(t, evaluator.Kind, "listed evaluator should have a kind")
		}

		// Drill into the first evaluator: this is the path an agent takes from a
		// failed score to the definition that produced it.
		id := resp.Items[0].EvaluatorID
		detail, err := agento11yEvalsRead(ctx, Agento11yEvalsReadParams{
			Operation:   "get_evaluator",
			EvaluatorID: id,
		})
		require.NoError(t, err, "Failed to get evaluator %s", id)

		evaluator, ok := detail.(*Agento11yEvaluatorDefinition)
		require.True(t, ok)
		assert.Equal(t, id, evaluator.EvaluatorID, "fetched evaluator id should match the requested id")
	})

	t.Run("list templates", func(t *testing.T) {
		result, err := agento11yEvalsRead(ctx, Agento11yEvalsReadParams{
			Operation: "list_templates",
			Limit:     10,
		})
		// /eval/templates only exists on stacks that configure a template store,
		// so a 404 here is a deployment property rather than a tool failure. Every
		// other error, permission and decode failures included, must fail the test.
		if err != nil && strings.Contains(err.Error(), "status 404") {
			t.Skipf("list_templates unavailable on this instance (no evaluator template store): %v", err)
		}
		require.NoError(t, err, "Failed to list Agent Observability evaluator templates")

		resp, ok := result.(*agento11yListResponse[Agento11yTemplateDefinition])
		require.True(t, ok, "list_templates should return *agento11yListResponse[Agento11yTemplateDefinition]")
		assert.LessOrEqual(t, len(resp.Items), 10, "list_templates should respect the requested limit")
		for _, template := range resp.Items {
			assert.NotEmpty(t, template.TemplateID, "listed template should have a template_id")
		}
	})

	t.Run("list judge providers", func(t *testing.T) {
		result, err := agento11yEvalsRead(ctx, Agento11yEvalsReadParams{
			Operation: "list_judge_providers",
		})
		require.NoError(t, err, "Failed to list Agent Observability judge providers")

		resp, ok := result.(*Agento11yJudgeProvidersResponse)
		require.True(t, ok, "list_judge_providers should return *Agento11yJudgeProvidersResponse")
		for _, provider := range resp.Providers {
			assert.NotEmpty(t, provider.ID, "listed judge provider should have an id")
		}
	})

	t.Run("list rules", func(t *testing.T) {
		result, err := agento11yEvalsRead(ctx, Agento11yEvalsReadParams{
			Operation: "list_rules",
		})
		require.NoError(t, err, "Failed to list Agent Observability eval rules")

		resp, ok := result.(*agento11yListResponse[Agento11yRuleDefinition])
		require.True(t, ok, "list_rules should return *agento11yListResponse[Agento11yRuleDefinition]")
		if len(resp.Items) == 0 {
			t.Log("no eval rules on this instance, skipping rule assertions")
			return
		}
		for _, rule := range resp.Items {
			assert.NotEmpty(t, rule.RuleID, "listed rule should have a rule_id")
			assert.NotEmpty(t, rule.Selector, "listed rule should have a selector")
		}

		id := resp.Items[0].RuleID
		detail, err := agento11yEvalsRead(ctx, Agento11yEvalsReadParams{
			Operation: "get_rule",
			RuleID:    id,
		})
		require.NoError(t, err, "Failed to get eval rule %s", id)

		rule, ok := detail.(*Agento11yRuleDefinition)
		require.True(t, ok)
		assert.Equal(t, id, rule.RuleID, "fetched rule id should match the requested id")
	})

	// The collection subtests are read-only on purpose: this suite runs against a
	// real stack, so bookmarking a conversation or creating a collection would
	// leave curation data behind.
	t.Run("list saved conversations", func(t *testing.T) {
		result, err := agento11yEvalsRead(ctx, Agento11yEvalsReadParams{
			Operation: "list_saved_conversations",
			Limit:     10,
		})
		require.NoError(t, err, "Failed to list Agent Observability saved conversations")

		resp, ok := result.(*Agento11ySavedConversationsResponse)
		require.True(t, ok, "list_saved_conversations should return *Agento11ySavedConversationsResponse")
		assert.LessOrEqual(t, len(resp.Items), 10, "list_saved_conversations should respect the requested limit")
		if len(resp.Items) == 0 {
			t.Log("no saved conversations on this instance, skipping saved conversation assertions")
			return
		}
		for _, saved := range resp.Items {
			assert.NotEmpty(t, saved.SavedID, "listed saved conversation should have a saved_id")
			assert.NotEmpty(t, saved.ConversationID, "listed saved conversation should have a conversation_id")
		}
		assert.GreaterOrEqual(t, resp.TotalCount, int64(len(resp.Items)), "total_count should cover at least the returned page")

		id := resp.Items[0].SavedID
		detail, err := agento11yEvalsRead(ctx, Agento11yEvalsReadParams{
			Operation: "get_saved_conversation",
			SavedID:   id,
		})
		require.NoError(t, err, "Failed to get saved conversation %s", id)

		saved, ok := detail.(*Agento11ySavedConversation)
		require.True(t, ok)
		assert.Equal(t, id, saved.SavedID, "fetched saved conversation id should match the requested id")
	})

	t.Run("list collections", func(t *testing.T) {
		result, err := agento11yEvalsRead(ctx, Agento11yEvalsReadParams{
			Operation: "list_collections",
			Limit:     10,
		})
		require.NoError(t, err, "Failed to list Agent Observability collections")

		resp, ok := result.(*agento11yListResponse[Agento11yCollection])
		require.True(t, ok, "list_collections should return *agento11yListResponse[Agento11yCollection]")
		assert.LessOrEqual(t, len(resp.Items), 10, "list_collections should respect the requested limit")
		if len(resp.Items) == 0 {
			t.Log("no collections on this instance, skipping collection assertions")
			return
		}
		for _, collection := range resp.Items {
			assert.NotEmpty(t, collection.CollectionID, "listed collection should have a collection_id")
			assert.NotEmpty(t, collection.Name, "listed collection should have a name")
		}

		id := resp.Items[0].CollectionID
		members, err := agento11yEvalsRead(ctx, Agento11yEvalsReadParams{
			Operation:    "list_collection_members",
			CollectionID: id,
			Limit:        10,
		})
		require.NoError(t, err, "Failed to list members of collection %s", id)

		memberResp, ok := members.(*agento11yListResponse[Agento11ySavedConversation])
		require.True(t, ok, "list_collection_members should return *agento11yListResponse[Agento11ySavedConversation]")
		assert.LessOrEqual(t, len(memberResp.Items), 10, "list_collection_members should respect the requested limit")
		for _, member := range memberResp.Items {
			assert.NotEmpty(t, member.SavedID, "collection member should have a saved_id")
		}
	})

	// Read-only on purpose: renaming or canceling an experiment on a real stack
	// changes data someone owns.
	t.Run("list experiments", func(t *testing.T) {
		result, err := agento11yEvalsRead(ctx, Agento11yEvalsReadParams{
			Operation: "list_experiments",
			Limit:     10,
		})
		require.NoError(t, err, "Failed to list Agent Observability experiments")

		resp, ok := result.(*agento11yListResponse[Agento11yExperiment])
		require.True(t, ok, "list_experiments should return *agento11yListResponse[Agento11yExperiment]")
		assert.LessOrEqual(t, len(resp.Items), 10, "list_experiments should respect the requested limit")
		if len(resp.Items) == 0 {
			t.Log("no experiments on this instance, skipping experiment assertions")
			return
		}
		for _, experiment := range resp.Items {
			assert.NotEmpty(t, experiment.ExperimentID, "listed experiment should have an experiment_id")
			assert.Contains(t, []string{"running", "completed", "failed", "canceled"}, experiment.Status,
				"listed experiment should carry one of the four wire statuses")
		}

		id := resp.Items[0].ExperimentID
		detail, err := agento11yEvalsRead(ctx, Agento11yEvalsReadParams{
			Operation:    "get_experiment",
			ExperimentID: id,
		})
		require.NoError(t, err, "Failed to get experiment %s", id)

		experiment, ok := detail.(*Agento11yExperiment)
		require.True(t, ok)
		assert.Equal(t, id, experiment.ExperimentID, "fetched experiment id should match the requested id")

		trials, err := agento11yEvalsRead(ctx, Agento11yEvalsReadParams{
			Operation:    "list_experiment_trials",
			ExperimentID: id,
			Limit:        10,
		})
		require.NoError(t, err, "Failed to list trials of experiment %s", id)

		trialResp, ok := trials.(*agento11yListResponse[Agento11yTestCaseTrial])
		require.True(t, ok, "list_experiment_trials should return *agento11yListResponse[Agento11yTestCaseTrial]")
		assert.LessOrEqual(t, len(trialResp.Items), 10, "list_experiment_trials should respect the requested limit")
		if len(trialResp.Items) == 0 {
			t.Log("experiment has no trials, skipping trial assertions")
			return
		}

		trialID := trialResp.Items[0].TrialID
		scores, err := agento11yEvalsRead(ctx, Agento11yEvalsReadParams{
			Operation: "list_trial_scores",
			TrialID:   trialID,
			Limit:     10,
		})
		require.NoError(t, err, "Failed to list scores of trial %s", trialID)

		scoreResp, ok := scores.(*agento11yListResponse[Agento11yEvalScore])
		require.True(t, ok, "list_trial_scores should return *agento11yListResponse[Agento11yEvalScore]")
		for _, score := range scoreResp.Items {
			assert.NotEmpty(t, score.ScoreID, "trial score should have a score_id")
		}
	})

	t.Run("get_experiment_report stays within its row limit", func(t *testing.T) {
		listed, err := agento11yEvalsRead(ctx, Agento11yEvalsReadParams{
			Operation: "list_experiments",
			Status:    "completed",
			Limit:     1,
		})
		require.NoError(t, err, "Failed to list completed Agent Observability experiments")

		resp, ok := listed.(*agento11yListResponse[Agento11yExperiment])
		require.True(t, ok)
		if len(resp.Items) == 0 {
			t.Skip("no completed experiments on this instance")
		}

		id := resp.Items[0].ExperimentID
		result, err := agento11yEvalsRead(ctx, Agento11yEvalsReadParams{
			Operation:    "get_experiment_report",
			ExperimentID: id,
			RowLimit:     5,
		})
		require.NoError(t, err, "Failed to get the report of experiment %s", id)

		report, ok := result.(*Agento11yCompactExperimentReport)
		require.True(t, ok, "get_experiment_report should return *Agento11yCompactExperimentReport")
		assert.LessOrEqual(t, len(report.Rows), 5, "get_experiment_report should respect the requested row limit")
		assert.Equal(t, len(report.Rows) < report.TotalRowCount, report.RowsTruncated,
			"rows_truncated should say whether rows were dropped")
	})

	t.Run("list experiment facets", func(t *testing.T) {
		result, err := agento11yEvalsRead(ctx, Agento11yEvalsReadParams{
			Operation: "list_experiment_facets",
		})
		require.NoError(t, err, "Failed to list Agent Observability experiment facets")

		facets, ok := result.(*Agento11yExperimentFacets)
		require.True(t, ok, "list_experiment_facets should return *Agento11yExperimentFacets")
		for _, suite := range facets.Suites {
			assert.NotEmpty(t, suite, "a reported suite facet should not be blank")
		}
	})

	// Read-only on purpose: creating a test suite or publishing a version on a
	// real stack changes what the next experiment replays.
	t.Run("list test suites", func(t *testing.T) {
		result, err := agento11yEvalsRead(ctx, Agento11yEvalsReadParams{
			Operation: "list_suites",
			Limit:     10,
		})
		require.NoError(t, err, "Failed to list Agent Observability test suites")

		resp, ok := result.(*agento11yListResponse[Agento11yTestSuite])
		require.True(t, ok, "list_suites should return *agento11yListResponse[Agento11yTestSuite]")
		assert.LessOrEqual(t, len(resp.Items), 10, "list_suites should respect the requested limit")
		if len(resp.Items) == 0 {
			t.Log("no test suites on this instance, skipping test suite assertions")
			return
		}
		for _, suite := range resp.Items {
			assert.NotEmpty(t, suite.SuiteID, "listed test suite should have a suite_id")
			assert.NotEmpty(t, suite.Name, "listed test suite should have a name")
		}

		id := resp.Items[0].SuiteID
		detail, err := agento11yEvalsRead(ctx, Agento11yEvalsReadParams{
			Operation: "get_suite",
			SuiteID:   id,
		})
		require.NoError(t, err, "Failed to get test suite %s", id)

		suite, ok := detail.(*Agento11yTestSuite)
		require.True(t, ok)
		assert.Equal(t, id, suite.SuiteID, "fetched test suite id should match the requested id")
		if len(suite.Versions) == 0 {
			t.Log("test suite has no versions, skipping version and test case assertions")
			return
		}
		for _, version := range suite.Versions {
			assert.NotEmpty(t, version.Version, "a reported version should have a version string")
			if !version.Published {
				assert.Nil(t, version.PublishedAt, "an unpublished version should have no publish time")
			}
		}

		version := suite.Versions[0].Version
		cases, err := agento11yEvalsRead(ctx, Agento11yEvalsReadParams{
			Operation: "list_test_cases",
			SuiteID:   id,
			Version:   version,
			Limit:     10,
		})
		require.NoError(t, err, "Failed to list test cases of %s version %s", id, version)

		caseResp, ok := cases.(*agento11yListResponse[Agento11yTestCase])
		require.True(t, ok, "list_test_cases should return *agento11yListResponse[Agento11yTestCase]")
		assert.LessOrEqual(t, len(caseResp.Items), 10, "list_test_cases should respect the requested limit")
		if len(caseResp.Items) == 0 {
			t.Log("test suite version has no test cases, skipping test case assertions")
			return
		}

		caseID := caseResp.Items[0].TestCaseID
		caseDetail, err := agento11yEvalsRead(ctx, Agento11yEvalsReadParams{
			Operation:  "get_test_case",
			SuiteID:    id,
			Version:    version,
			TestCaseID: caseID,
		})
		require.NoError(t, err, "Failed to get test case %s", caseID)

		testCase, ok := caseDetail.(*Agento11yTestCase)
		require.True(t, ok)
		assert.Equal(t, caseID, testCase.TestCaseID, "fetched test case id should match the requested id")
		assert.NotEmpty(t, testCase.Input, "a test case always carries an input")
	})

	t.Run("list guards", func(t *testing.T) {
		result, err := agento11yEvalsRead(ctx, Agento11yEvalsReadParams{
			Operation: "list_guards",
			Limit:     10,
		})
		require.NoError(t, err, "Failed to list Agent Observability guards")

		resp, ok := result.(*agento11yListResponse[Agento11yHookRuleDefinition])
		require.True(t, ok, "list_guards should return *agento11yListResponse[Agento11yHookRuleDefinition]")
		assert.LessOrEqual(t, len(resp.Items), 10, "list_guards should respect the requested limit")
		if len(resp.Items) == 0 {
			t.Log("no guards on this instance, skipping guard assertions")
			return
		}
		for _, guard := range resp.Items {
			assert.NotEmpty(t, guard.RuleID, "listed guard should have a rule_id")
			assert.NotEmpty(t, guard.ActionOnFail, "listed guard should have an action_on_fail")
		}
	})
}
