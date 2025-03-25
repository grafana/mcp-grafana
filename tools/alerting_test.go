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

var (
	rule1Labels = map[string]string{
		"severity": "info",
		"type":     "test",
		"rule":     "first",
	}
	rule2Labels = map[string]string{
		"severity": "info",
		"type":     "test",
		"rule":     "second",
	}
	rule3Labels = map[string]string{
		"severity": "info",
		"type":     "test",
		"rule":     "third",
	}

	rule1 = alertRuleSummary{
		UID:    rule1UID,
		Title:  rule1Title,
		Labels: rule1Labels,
	}
	rule2 = alertRuleSummary{
		UID:    rule2UID,
		Title:  rule2Title,
		Labels: rule2Labels,
	}
	rulePaused = alertRuleSummary{
		UID:    rulePausedUID,
		Title:  rulePausedTitle,
		Labels: rule3Labels,
	}
	allExpectedRules = []alertRuleSummary{rule1, rule2, rulePaused}
)

func TestAlertingTools_ListAlertRules(t *testing.T) {
	t.Run("list alert rules", func(t *testing.T) {
		ctx := newTestContext()
		result, err := listAlertRules(ctx, ListAlertRulesParams{})
		require.NoError(t, err)

		require.ElementsMatch(t, allExpectedRules, result)
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
		require.ElementsMatch(t, allExpectedRules, result)
	})

	t.Run("list alert rules with selectors that match", func(t *testing.T) {
		ctx := newTestContext()
		result, err := listAlertRules(ctx, ListAlertRulesParams{
			LabelSelectors: []Selector{
				{
					Filters: []LabelMatcher{
						{
							Name:  "severity",
							Value: "info",
							Type:  "=",
						},
					},
				},
			},
		})
		require.NoError(t, err)
		require.ElementsMatch(t, allExpectedRules, result)
	})

	t.Run("list alert rules with selectors that don't match", func(t *testing.T) {
		ctx := newTestContext()
		result, err := listAlertRules(ctx, ListAlertRulesParams{
			LabelSelectors: []Selector{
				{
					Filters: []LabelMatcher{
						{
							Name:  "severity",
							Value: "critical",
							Type:  "=",
						},
					},
				},
			},
		})
		require.NoError(t, err)
		require.Empty(t, result)
	})

	t.Run("list alert rules with multiple selectors", func(t *testing.T) {
		ctx := newTestContext()
		result, err := listAlertRules(ctx, ListAlertRulesParams{
			LabelSelectors: []Selector{
				{
					Filters: []LabelMatcher{
						{
							Name:  "severity",
							Value: "info",
							Type:  "=",
						},
					},
				},
				{
					Filters: []LabelMatcher{
						{
							Name:  "rule",
							Value: "second",
							Type:  "=",
						},
					},
				},
			},
		})
		require.NoError(t, err)
		require.ElementsMatch(t, []alertRuleSummary{rule2}, result)
	})

	t.Run("list alert rules with regex matcher", func(t *testing.T) {
		ctx := newTestContext()
		result, err := listAlertRules(ctx, ListAlertRulesParams{
			LabelSelectors: []Selector{
				{
					Filters: []LabelMatcher{
						{
							Name:  "rule",
							Value: "fi.*",
							Type:  "=~",
						},
					},
				},
			},
		})
		require.NoError(t, err)
		require.ElementsMatch(t, []alertRuleSummary{rule1}, result)
	})

	t.Run("list alert rules with selectors and pagination", func(t *testing.T) {
		ctx := newTestContext()
		result, err := listAlertRules(ctx, ListAlertRulesParams{
			LabelSelectors: []Selector{
				{
					Filters: []LabelMatcher{
						{
							Name:  "severity",
							Value: "info",
							Type:  "=",
						},
					},
				},
			},
			Limit: 1,
			Page:  1,
		})
		require.NoError(t, err)
		require.Len(t, result, 1)
		require.ElementsMatch(t, []alertRuleSummary{rule1}, result)

		// Second page
		result, err = listAlertRules(ctx, ListAlertRulesParams{
			LabelSelectors: []Selector{
				{
					Filters: []LabelMatcher{
						{
							Name:  "severity",
							Value: "info",
							Type:  "=",
						},
					},
				},
			},
			Limit: 1,
			Page:  2,
		})
		require.NoError(t, err)
		require.Len(t, result, 1)
		require.ElementsMatch(t, []alertRuleSummary{rule2}, result)
	})

	t.Run("list alert rules with not equals operator", func(t *testing.T) {
		ctx := newTestContext()
		result, err := listAlertRules(ctx, ListAlertRulesParams{
			LabelSelectors: []Selector{
				{
					Filters: []LabelMatcher{
						{
							Name:  "severity",
							Value: "critical",
							Type:  "!=",
						},
					},
				},
			},
		})
		require.NoError(t, err)
		require.ElementsMatch(t, allExpectedRules, result)
	})

	t.Run("list alert rules with not matches operator", func(t *testing.T) {
		ctx := newTestContext()
		result, err := listAlertRules(ctx, ListAlertRulesParams{
			LabelSelectors: []Selector{
				{
					Filters: []LabelMatcher{
						{
							Name:  "severity",
							Value: "crit.*",
							Type:  "!~",
						},
					},
				},
			},
		})
		require.NoError(t, err)
		require.ElementsMatch(t, allExpectedRules, result)
	})

	t.Run("list alert rules with non-existent label", func(t *testing.T) {
		// Equality with non-existent label should return no results
		ctx := newTestContext()
		result, err := listAlertRules(ctx, ListAlertRulesParams{
			LabelSelectors: []Selector{
				{
					Filters: []LabelMatcher{
						{
							Name:  "nonexistent",
							Value: "value",
							Type:  "=",
						},
					},
				},
			},
		})
		require.NoError(t, err)
		require.Empty(t, result)
	})

	t.Run("list alert rules with non-existent label and inequality", func(t *testing.T) {
		// Inequality with non-existent label should return all results
		ctx := newTestContext()
		result, err := listAlertRules(ctx, ListAlertRulesParams{
			LabelSelectors: []Selector{
				{
					Filters: []LabelMatcher{
						{
							Name:  "nonexistent",
							Value: "value",
							Type:  "!=",
						},
					},
				},
			},
		})
		require.NoError(t, err)
		require.ElementsMatch(t, allExpectedRules, result)
	})

	t.Run("list alert rules with a limit that is larger than the number of rules", func(t *testing.T) {
		ctx := newTestContext()
		result, err := listAlertRules(ctx, ListAlertRulesParams{
			Limit: 1000,
			Page:  1,
		})
		require.NoError(t, err)
		require.ElementsMatch(t, allExpectedRules, result)
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
