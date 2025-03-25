// Requires a Grafana instance running on localhost:3000,
// with alert rules configured.
// Run with `go test -tags integration`.
//go:build integration

package tools

import (
	"testing"

	"github.com/stretchr/testify/require"
)

const (
	rule1UID        = "test_alert_rule_1"
	rule1Title      = "Test Alert Rule 1"
	rule2UID        = "test_alert_rule_2"
	rule2Title      = "Test Alert Rule 2"
	rulePausedUID   = "test_alert_rule_paused"
	rulePausedTitle = "Test Alert Rule (Paused)"
)

func TestAlertingTools_ListAlertRules(t *testing.T) {
	t.Run("list alert rules", func(t *testing.T) {
		ctx := newTestContext()
		result, err := listAlertRules(ctx, ListAlertRulesParams{})
		require.NoError(t, err)

		expectedRules := []alertRuleSummary{
			{UID: rule1UID, Title: rule1Title},
			{UID: rule2UID, Title: rule2Title},
			{UID: rulePausedUID, Title: rulePausedTitle},
		}
		require.ElementsMatch(t, expectedRules, result)
	})

	t.Run("list alert rules with pagination", func(t *testing.T) {
		ctx := newTestContext()

		// Get the first page with limit 1
		result1, err := listAlertRules(ctx, ListAlertRulesParams{
			Limit: 1,
			Page:  1,
		})
		require.NoError(t, err)
		require.Len(t, result1, 1)

		// Get the second page with limit 1
		result2, err := listAlertRules(ctx, ListAlertRulesParams{
			Limit: 1,
			Page:  2,
		})
		require.NoError(t, err)
		require.Len(t, result2, 1)

		// Get the third page with limit 1
		result3, err := listAlertRules(ctx, ListAlertRulesParams{
			Limit: 1,
			Page:  3,
		})
		require.NoError(t, err)
		require.Len(t, result3, 1)

		// The next page is empty
		result4, err := listAlertRules(ctx, ListAlertRulesParams{
			Limit: 1,
			Page:  4,
		})
		require.NoError(t, err)
		require.Empty(t, result4)
	})

	t.Run("list alert rules without the page and limit params", func(t *testing.T) {
		ctx := newTestContext()
		result, err := listAlertRules(ctx, ListAlertRulesParams{})
		require.NoError(t, err)
		expectedRules := []alertRuleSummary{
			{UID: rule1UID, Title: rule1Title},
			{UID: rule2UID, Title: rule2Title},
			{UID: rulePausedUID, Title: rulePausedTitle},
		}
		require.ElementsMatch(t, expectedRules, result)
	})

	t.Run("list alert rules with a limit that is larger than the number of rules", func(t *testing.T) {
		ctx := newTestContext()
		result, err := listAlertRules(ctx, ListAlertRulesParams{
			Limit: 1000,
			Page:  1,
		})
		require.NoError(t, err)
		expectedRules := []alertRuleSummary{
			{UID: rule1UID, Title: rule1Title},
			{UID: rule2UID, Title: rule2Title},
			{UID: rulePausedUID, Title: rulePausedTitle},
		}
		require.ElementsMatch(t, expectedRules, result)
	})

	t.Run("list alert rules with a page that doesn't exist", func(t *testing.T) {
		ctx := newTestContext()
		result, err := listAlertRules(ctx, ListAlertRulesParams{
			Limit: 10,
			Page:  1000,
		})
		require.NoError(t, err)
		require.Empty(t, result)
	})

	t.Run("list alert rules with invalid page parameter", func(t *testing.T) {
		ctx := newTestContext()
		result, err := listAlertRules(ctx, ListAlertRulesParams{
			Page: -1,
		})
		require.Error(t, err)
		require.Empty(t, result)
	})

	t.Run("list alert rules with invalid limit parameter", func(t *testing.T) {
		ctx := newTestContext()
		result, err := listAlertRules(ctx, ListAlertRulesParams{
			Limit: -1,
		})
		require.Error(t, err)
		require.Empty(t, result)
	})
}

func TestAlertingTools_GetAlertRuleByUID(t *testing.T) {
	t.Run("get running alert rule by uid", func(t *testing.T) {
		ctx := newTestContext()
		result, err := getAlertRuleByUID(ctx, GetAlertRuleByUIDParams{
			UID: rule1UID,
		})

		require.NoError(t, err)
		require.Equal(t, rule1UID, result.UID)
		require.NotNil(t, result.Title)
		require.Equal(t, rule1Title, *result.Title)
		require.False(t, result.IsPaused)
	})

	t.Run("get paused alert rule by uid", func(t *testing.T) {
		ctx := newTestContext()
		result, err := getAlertRuleByUID(ctx, GetAlertRuleByUIDParams{
			UID: "test_alert_rule_paused",
		})

		require.NoError(t, err)
		require.Equal(t, rulePausedUID, result.UID)
		require.NotNil(t, result.Title)
		require.Equal(t, rulePausedTitle, *result.Title)
		require.True(t, result.IsPaused)
	})

	t.Run("get alert rule with empty UID fails", func(t *testing.T) {
		ctx := newTestContext()
		result, err := getAlertRuleByUID(ctx, GetAlertRuleByUIDParams{
			UID: "",
		})

		require.Nil(t, result)
		require.Error(t, err)
	})

	t.Run("get non-existing alert rule by uid", func(t *testing.T) {
		ctx := newTestContext()
		result, err := getAlertRuleByUID(ctx, GetAlertRuleByUIDParams{
			UID: "some-non-existing-alert-rule-uid",
		})

		require.Nil(t, result)
		require.Error(t, err)
		require.Contains(t, err.Error(), "getAlertRuleNotFound")
	})
}
