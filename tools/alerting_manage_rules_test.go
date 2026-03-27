// Requires a Grafana instance running on localhost:3000,
// with alert rules configured.
// Run with `go test -tags integration`.
//go:build integration

package tools

import (
	"testing"
	"time"

	"github.com/go-openapi/strfmt"
	"github.com/grafana/grafana-openapi-client-go/models"
	"github.com/stretchr/testify/require"
)

const (
	rule1UID           = "test_alert_rule_1"
	rule1Title         = "Test Alert Rule 1"
	rule2UID           = "test_alert_rule_2"
	rule2Title         = "Test Alert Rule 2"
	rulePausedUID      = "test_alert_rule_paused"
	rulePausedTitle    = "Test Alert Rule (Paused)"
	ruleIDRecording    = "recording_rule_1"
	ruleRecordingTitle = "test_recording_rule"

	testRuleGroup = "Test Alert Rules"
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
		State:  "",
		Title:  rule1Title,
		Labels: rule1Labels,
	}
	rule2 = alertRuleSummary{
		UID:    rule2UID,
		State:  "",
		Title:  rule2Title,
		Labels: rule2Labels,
	}
	rulePaused = alertRuleSummary{
		UID:    rulePausedUID,
		State:  "",
		Title:  rulePausedTitle,
		Labels: rule3Labels,
	}

	ruleRecordingLabels = map[string]string{
		"team": "sre",
		"type": "test",
	}

	ruleRecordingType = alertRuleSummary{
		UID:    ruleIDRecording,
		State:  "",
		Title:  ruleRecordingTitle,
		Labels: ruleRecordingLabels,
	}

	allExpectedRules = []alertRuleSummary{rule1, rule2, rulePaused, ruleRecordingType}

	recordingRuleTypeQuery = []*AlertQuery{
		{
			RefID:         "A",
			DatasourceUID: "prometheus",
			RelativeTimeRange: &RelativeTimeRange{
				From: 600,
				To:   0,
			},
			Model: AlertQueryModel{
				Expr: "up",
			},
		},
	}
)

// Because the state depends on the evaluation of the alert rules,
// clear it and other variable runtime fields before comparing the results
// to avoid waiting for the alerts to start firing or be in the pending state.
func clearState(rules []alertRuleSummary) []alertRuleSummary {
	for i := range rules {
		rules[i].State = ""
		rules[i].Health = ""
		rules[i].FolderUID = ""
		rules[i].RuleGroup = ""
		rules[i].For = ""
		rules[i].LastEvaluation = ""
		rules[i].Annotations = nil
	}

	return rules
}

func TestManageRules_List(t *testing.T) {
	t.Run("list alert rules", func(t *testing.T) {
		ctx := newTestContext()
		result, err := manageRulesRead(ctx, ManageRulesReadParams{Operation: "list"})
		require.NoError(t, err)

		rules, ok := result.([]alertRuleSummary)
		require.True(t, ok)
		require.ElementsMatch(t, allExpectedRules, clearState(rules))
	})

	t.Run("list alert rules normalizes state", func(t *testing.T) {
		ctx := newTestContext()
		result, err := manageRulesRead(ctx, ManageRulesReadParams{
			Operation: "list",
		})
		require.NoError(t, err)

		rules, ok := result.([]alertRuleSummary)
		require.True(t, ok)
		for _, rule := range rules {
			require.NotEqual(t, "inactive", rule.State, "state should be normalized from 'inactive' to 'normal'")
		}
	})

	t.Run("list alert rules with selectors that match", func(t *testing.T) {
		ctx := newTestContext()
		result, err := manageRulesRead(ctx, ManageRulesReadParams{
			listFilterParams: listFilterParams{
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
			},
			Operation: "list",
		})
		require.NoError(t, err)

		rules, ok := result.([]alertRuleSummary)
		require.True(t, ok)
		// Only the three alerting rules carry severity=info; the recording rule does not.
		alertingRules := []alertRuleSummary{rule1, rule2, rulePaused}
		require.ElementsMatch(t, alertingRules, clearState(rules))
	})

	t.Run("list alert rules with selectors that don't match", func(t *testing.T) {
		ctx := newTestContext()
		result, err := manageRulesRead(ctx, ManageRulesReadParams{
			listFilterParams: listFilterParams{
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
			},
			Operation: "list",
		})
		require.NoError(t, err)

		rules, ok := result.([]alertRuleSummary)
		require.True(t, ok)
		require.Empty(t, rules)
	})

	t.Run("list alert rules with multiple selectors", func(t *testing.T) {
		ctx := newTestContext()
		result, err := manageRulesRead(ctx, ManageRulesReadParams{
			listFilterParams: listFilterParams{
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
			},
			Operation: "list",
		})
		require.NoError(t, err)

		rules, ok := result.([]alertRuleSummary)
		require.True(t, ok)
		require.ElementsMatch(t, []alertRuleSummary{rule2}, clearState(rules))
	})

	t.Run("list alert rules with regex matcher", func(t *testing.T) {
		ctx := newTestContext()
		result, err := manageRulesRead(ctx, ManageRulesReadParams{
			listFilterParams: listFilterParams{
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
			},
			Operation: "list",
		})
		require.NoError(t, err)

		rules, ok := result.([]alertRuleSummary)
		require.True(t, ok)
		require.ElementsMatch(t, []alertRuleSummary{rule1}, clearState(rules))
	})

	t.Run("list alert rules with not equals operator", func(t *testing.T) {
		ctx := newTestContext()
		result, err := manageRulesRead(ctx, ManageRulesReadParams{
			listFilterParams: listFilterParams{
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
			},
			Operation: "list",
		})
		require.NoError(t, err)

		rules, ok := result.([]alertRuleSummary)
		require.True(t, ok)
		require.ElementsMatch(t, allExpectedRules, clearState(rules))
	})

	t.Run("list alert rules with not matches operator", func(t *testing.T) {
		ctx := newTestContext()
		result, err := manageRulesRead(ctx, ManageRulesReadParams{
			listFilterParams: listFilterParams{
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
			},
			Operation: "list",
		})
		require.NoError(t, err)

		rules, ok := result.([]alertRuleSummary)
		require.True(t, ok)
		require.ElementsMatch(t, allExpectedRules, clearState(rules))
	})

	t.Run("list alert rules with non-existent label", func(t *testing.T) {
		ctx := newTestContext()
		result, err := manageRulesRead(ctx, ManageRulesReadParams{
			listFilterParams: listFilterParams{
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
			},
			Operation: "list",
		})
		require.NoError(t, err)

		rules, ok := result.([]alertRuleSummary)
		require.True(t, ok)
		require.Empty(t, rules)
	})

	t.Run("list alert rules with non-existent label and inequality", func(t *testing.T) {
		ctx := newTestContext()
		result, err := manageRulesRead(ctx, ManageRulesReadParams{
			listFilterParams: listFilterParams{
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
			},
			Operation: "list",
		})
		require.NoError(t, err)

		rules, ok := result.([]alertRuleSummary)
		require.True(t, ok)
		require.ElementsMatch(t, allExpectedRules, clearState(rules))
	})

	t.Run("list alert rules with a large rule_limit", func(t *testing.T) {
		ctx := newTestContext()
		result, err := manageRulesRead(ctx, ManageRulesReadParams{
			listFilterParams: listFilterParams{RuleLimit: 1000},
			Operation:        "list",
		})
		require.NoError(t, err)

		rules, ok := result.([]alertRuleSummary)
		require.True(t, ok)
		require.ElementsMatch(t, allExpectedRules, clearState(rules))
	})

	t.Run("list alert rules with negative rule_limit", func(t *testing.T) {
		ctx := newTestContext()
		_, err := manageRulesRead(ctx, ManageRulesReadParams{
			listFilterParams: listFilterParams{RuleLimit: -1},
			Operation:        "list",
		})
		require.Error(t, err)
	})

	t.Run("list with folder_uid filter", func(t *testing.T) {
		ctx := newTestContext()
		// Discover the folder UID from a known provisioned rule.
		result, err := manageRulesRead(ctx, ManageRulesReadParams{Operation: "list"})
		require.NoError(t, err)
		allRules, ok := result.([]alertRuleSummary)
		require.True(t, ok)

		var folderUID string
		for _, r := range allRules {
			if r.UID == rule1UID {
				folderUID = r.FolderUID
				break
			}
		}
		require.NotEmpty(t, folderUID)

		result, err = manageRulesRead(ctx, ManageRulesReadParams{
			Operation: "list",
			FolderUID: folderUID,
		})
		require.NoError(t, err)
		rules, ok := result.([]alertRuleSummary)
		require.True(t, ok)
		require.NotEmpty(t, rules)
		for _, r := range rules {
			require.Equal(t, folderUID, r.FolderUID)
		}
	})

	t.Run("list with folder_uid that has no alert rules returns empty", func(t *testing.T) {
		ctx := newTestContext()
		result, err := manageRulesRead(ctx, ManageRulesReadParams{
			Operation: "list",
			FolderUID: "empty-alerts-folder",
		})
		require.NoError(t, err)
		rules, ok := result.([]alertRuleSummary)
		require.True(t, ok)
		require.Empty(t, rules)
	})

	t.Run("list with non-matching rule_group", func(t *testing.T) {
		ctx := newTestContext()
		result, err := manageRulesRead(ctx, ManageRulesReadParams{
			Operation: "list",
			RuleGroup: "nonexistent-group",
		})
		require.NoError(t, err)

		rules, ok := result.([]alertRuleSummary)
		require.True(t, ok)
		require.Empty(t, rules)
	})

	t.Run("list with search_rule_name partial match", func(t *testing.T) {
		ctx := newTestContext()
		result, err := manageRulesRead(ctx, ManageRulesReadParams{
			listFilterParams: listFilterParams{SearchRuleName: "Paused"},
			Operation:        "list",
		})
		require.NoError(t, err)

		rules, ok := result.([]alertRuleSummary)
		require.True(t, ok)
		require.Len(t, rules, 1)
		require.Equal(t, rulePausedUID, rules[0].UID)
	})

	t.Run("list with search_rule_name matching nothing", func(t *testing.T) {
		ctx := newTestContext()
		result, err := manageRulesRead(ctx, ManageRulesReadParams{
			listFilterParams: listFilterParams{SearchRuleName: "zzz-no-match"},
			Operation:        "list",
		})
		require.NoError(t, err)

		rules, ok := result.([]alertRuleSummary)
		require.True(t, ok)
		require.Empty(t, rules)
	})

	t.Run("list recording rules", func(t *testing.T) {
		ctx := newTestContext()
		result, err := manageRulesRead(ctx, ManageRulesReadParams{
			listFilterParams: listFilterParams{RuleType: "recording"},
			Operation:        "list",
		})
		require.NoError(t, err)

		rules, ok := result.([]alertRuleSummary)
		require.True(t, ok)
		require.NotEmpty(t, rules)
	})

	t.Run("list with rule_limit limits results", func(t *testing.T) {
		ctx := newTestContext()
		result, err := manageRulesRead(ctx, ManageRulesReadParams{
			listFilterParams: listFilterParams{RuleLimit: 1},
			Operation:        "list",
		})
		require.NoError(t, err)

		rules, ok := result.([]alertRuleSummary)
		require.True(t, ok)
		require.Len(t, rules, 1)
	})
}

func TestManageRules_Get(t *testing.T) {
	t.Run("get running alert rule by uid", func(t *testing.T) {
		ctx := newTestContext()
		result, err := manageRulesRead(ctx, ManageRulesReadParams{
			Operation: "get",
			RuleUID:   rule1UID,
		})

		require.NoError(t, err)
		detail, ok := result.(*alertRuleDetail)
		require.True(t, ok)
		require.Equal(t, rule1UID, detail.UID)
		require.Equal(t, rule1Title, detail.Title)

		// Config fields from provisioning API
		require.NotEmpty(t, detail.FolderUID)
		require.Equal(t, testRuleGroup, detail.RuleGroup)
		require.Equal(t, "B", detail.Condition)
		require.Equal(t, "0s", detail.For)
		require.Equal(t, "NoData", detail.NoDataState)
		require.Equal(t, "Error", detail.ExecErrState)
		require.False(t, detail.IsPaused)
		require.Nil(t, detail.NotificationSettings)

		// Queries extracted from provisioned data
		require.Len(t, detail.Queries, 2)
		require.Equal(t, "A", detail.Queries[0].RefID)
		require.Equal(t, "prometheus", detail.Queries[0].DatasourceUID)
		require.Equal(t, "vector(1)", detail.Queries[0].Expression)
		require.Equal(t, "B", detail.Queries[1].RefID)
		require.Equal(t, "__expr__", detail.Queries[1].DatasourceUID)

		// Runtime state from Prometheus rules API
		require.Equal(t, "alerting", detail.Type)
		require.NotEqual(t, "inactive", detail.State, "state should be normalized")
		require.NotEmpty(t, detail.Health)
		require.NotEmpty(t, detail.LastEvaluation)
		require.Empty(t, detail.LastError)
	})

	t.Run("get paused alert rule", func(t *testing.T) {
		ctx := newTestContext()
		result, err := manageRulesRead(ctx, ManageRulesReadParams{
			Operation: "get",
			RuleUID:   rulePausedUID,
		})

		require.NoError(t, err)
		detail, ok := result.(*alertRuleDetail)
		require.True(t, ok)
		require.Equal(t, rulePausedUID, detail.UID)
		require.True(t, detail.IsPaused)
	})

	t.Run("get alert rule with empty rule_uid fails", func(t *testing.T) {
		ctx := newTestContext()
		_, err := manageRulesRead(ctx, ManageRulesReadParams{
			Operation: "get",
			RuleUID:   "",
		})

		require.Error(t, err)
	})

	t.Run("get non-existing alert rule by uid", func(t *testing.T) {
		ctx := newTestContext()
		_, err := manageRulesRead(ctx, ManageRulesReadParams{
			Operation: "get",
			RuleUID:   "some-non-existing-alert-rule-uid",
		})

		require.Error(t, err)
		require.Contains(t, err.Error(), "getAlertRuleNotFound")
	})

	t.Run("get with limit_alerts caps alert instances", func(t *testing.T) {
		ctx := newTestContext()
		result, err := manageRulesRead(ctx, ManageRulesReadParams{
			listFilterParams: listFilterParams{LimitAlerts: 1},
			Operation:        "get",
			RuleUID:          rule1UID,
		})
		require.NoError(t, err)
		detail, ok := result.(*alertRuleDetail)
		require.True(t, ok)
		require.Equal(t, rule1UID, detail.UID)
		if detail.Alerts != nil {
			require.LessOrEqual(t, len(detail.Alerts), 1)
		}
	})
}

var (
	emailType = "email"

	contactPoint1 = contactPointSummary{
		UID:  "email1",
		Name: "Email1",
		Type: &emailType,
	}
	contactPoint2 = contactPointSummary{
		UID:  "email2",
		Name: "Email2",
		Type: &emailType,
	}
	allExpectedContactPoints = []contactPointSummary{contactPoint1, contactPoint2}
)

func TestManageRouting_GetContactPoints(t *testing.T) {
	t.Run("list contact points", func(t *testing.T) {
		ctx := newTestContext()
		result, err := manageRouting(ctx, ManageRoutingParams{
			Operation: "get_contact_points",
		})
		require.NoError(t, err)

		cps, ok := result.([]contactPointSummary)
		require.True(t, ok)
		require.ElementsMatch(t, allExpectedContactPoints, cps)
	})

	t.Run("list one contact point", func(t *testing.T) {
		ctx := newTestContext()
		result, err := manageRouting(ctx, ManageRoutingParams{
			Operation: "get_contact_points",
			Limit:     1,
		})
		require.NoError(t, err)

		cps, ok := result.([]contactPointSummary)
		require.True(t, ok)
		require.Len(t, cps, 1)
	})

	t.Run("list contact points with name filter", func(t *testing.T) {
		ctx := newTestContext()
		name := "Email1"
		result, err := manageRouting(ctx, ManageRoutingParams{
			Operation: "get_contact_points",
			Name:      &name,
		})
		require.NoError(t, err)

		cps, ok := result.([]contactPointSummary)
		require.True(t, ok)
		require.Len(t, cps, 1)
		require.Equal(t, "Email1", cps[0].Name)
	})

	t.Run("list contact points with invalid limit parameter", func(t *testing.T) {
		ctx := newTestContext()
		_, err := manageRouting(ctx, ManageRoutingParams{
			Operation: "get_contact_points",
			Limit:     -1,
		})
		require.Error(t, err)
	})

	t.Run("list contact points with large limit", func(t *testing.T) {
		ctx := newTestContext()
		result, err := manageRouting(ctx, ManageRoutingParams{
			Operation: "get_contact_points",
			Limit:     1000,
		})
		require.NoError(t, err)

		cps, ok := result.([]contactPointSummary)
		require.True(t, ok)
		require.NotEmpty(t, cps)
	})

	t.Run("list contact points with non-existent name filter", func(t *testing.T) {
		ctx := newTestContext()
		name := "NonExistentAlert"
		result, err := manageRouting(ctx, ManageRoutingParams{
			Operation: "get_contact_points",
			Name:      &name,
		})
		require.NoError(t, err)

		cps, ok := result.([]contactPointSummary)
		require.True(t, ok)
		require.Empty(t, cps)
	})
}

func TestManageRules_Create(t *testing.T) {
	t.Run("create alert rule with valid parameters", func(t *testing.T) {
		ctx := newTestContext()

		sampleData := []*AlertQuery{
			{
				RefID:         "A",
				DatasourceUID: "prometheus-uid",
				RelativeTimeRange: &RelativeTimeRange{
					From: 600,
					To:   0,
				},
				Model: AlertQueryModel{
					Expr: "up",
				},
			},
			{
				RefID:         "B",
				DatasourceUID: "__expr__",
				Model: AlertQueryModel{
					Type:       "reduce",
					Expression: "A",
					Reducer:    "last",
				},
			},
			{
				RefID:         "C",
				DatasourceUID: "__expr__",
				Model: AlertQueryModel{
					Type:       "threshold",
					Expression: "B",
					Conditions: []AlertCondition{
						{Evaluator: ConditionEvaluator{Type: "gt", Params: []float64{1}}},
					},
				},
			},
		}

		testUID := "test_create_alert_rule"
		t.Cleanup(func() {
			manageRulesReadWrite(ctx, ManageRulesReadWriteParams{Operation: "delete", RuleUID: testUID}) //nolint:errcheck
		})

		result, err := manageRulesReadWrite(ctx, ManageRulesReadWriteParams{
			Operation:    "create",
			RuleUID:      testUID,
			Title:        "Test Created Alert Rule",
			RuleGroup:    "test-group",
			FolderUID:    "tests",
			Condition:    "C",
			Data:         sampleData,
			NoDataState:  "OK",
			ExecErrState: "OK",
			For:          "5m",
			Annotations: map[string]string{
				"summary": "Test alert rule created via API",
			},
			Labels: map[string]string{
				"team": "test-team",
			},
			OrgID: 1,
		})
		require.NoError(t, err)

		created, ok := result.(*models.ProvisionedAlertRule)
		require.True(t, ok)
		require.Equal(t, testUID, created.UID)
		require.Equal(t, "Test Created Alert Rule", *created.Title)
		require.Equal(t, "test-group", *created.RuleGroup)
	})

	t.Run("create alert rule with reduce and threshold expressions", func(t *testing.T) {
		ctx := newTestContext()

		data := []*AlertQuery{
			{
				RefID:         "A",
				DatasourceUID: "prometheus",
				RelativeTimeRange: &RelativeTimeRange{
					From: 600,
					To:   0,
				},
				Model: AlertQueryModel{
					Expr: "vector(1)",
				},
			},
			{
				RefID:         "B",
				DatasourceUID: "__expr__",
				Model: AlertQueryModel{
					Type:       "reduce",
					Expression: "A",
					Reducer:    "last",
				},
			},
			{
				RefID:         "C",
				DatasourceUID: "__expr__",
				Model: AlertQueryModel{
					Type:       "threshold",
					Expression: "B",
					Conditions: []AlertCondition{
						{Evaluator: ConditionEvaluator{Type: "gt", Params: []float64{0}}},
					},
				},
			},
		}

		testUID := "test_create_reduce_threshold"
		t.Cleanup(func() {
			manageRulesReadWrite(ctx, ManageRulesReadWriteParams{Operation: "delete", RuleUID: testUID}) //nolint:errcheck
		})

		result, err := manageRulesReadWrite(ctx, ManageRulesReadWriteParams{
			Operation:    "create",
			RuleUID:      testUID,
			Title:        "Test Reduce+Threshold Rule",
			RuleGroup:    "test-group",
			FolderUID:    "tests",
			Condition:    "C",
			Data:         data,
			NoDataState:  "OK",
			ExecErrState: "OK",
			For:          "5m",
			OrgID:        1,
		})
		require.NoError(t, err)

		created, ok := result.(*models.ProvisionedAlertRule)
		require.True(t, ok)
		require.Equal(t, testUID, created.UID)
		require.Equal(t, "Test Reduce+Threshold Rule", *created.Title)
		require.Len(t, created.Data, 3)
		require.Equal(t, "A", created.Data[0].RefID)
		require.Equal(t, "B", created.Data[1].RefID)
		require.Equal(t, "C", created.Data[2].RefID)
	})

	t.Run("create alert rule with math expression", func(t *testing.T) {
		ctx := newTestContext()

		data := []*AlertQuery{
			{
				RefID:         "A",
				DatasourceUID: "prometheus",
				RelativeTimeRange: &RelativeTimeRange{
					From: 600,
					To:   0,
				},
				Model: AlertQueryModel{
					Expr: "vector(1)",
				},
			},
			{
				RefID:         "B",
				DatasourceUID: "__expr__",
				Model: AlertQueryModel{
					Type:       "reduce",
					Expression: "A",
					Reducer:    "last",
				},
			},
			{
				RefID:         "C",
				DatasourceUID: "__expr__",
				Model: AlertQueryModel{
					Type:       "math",
					Expression: "$B > 0",
				},
			},
		}

		testUID := "test_create_math_expr"
		t.Cleanup(func() {
			manageRulesReadWrite(ctx, ManageRulesReadWriteParams{Operation: "delete", RuleUID: testUID}) //nolint:errcheck
		})

		result, err := manageRulesReadWrite(ctx, ManageRulesReadWriteParams{
			Operation:    "create",
			RuleUID:      testUID,
			Title:        "Test Math Expression Rule",
			RuleGroup:    "test-group",
			FolderUID:    "tests",
			Condition:    "C",
			Data:         data,
			NoDataState:  "OK",
			ExecErrState: "OK",
			For:          "5m",
			OrgID:        1,
		})
		require.NoError(t, err)

		created, ok := result.(*models.ProvisionedAlertRule)
		require.True(t, ok)
		require.Equal(t, testUID, created.UID)
		require.Equal(t, "Test Math Expression Rule", *created.Title)
		require.Len(t, created.Data, 3)
	})

	t.Run("create alert rule with auto-assigned RefIDs", func(t *testing.T) {
		ctx := newTestContext()

		data := []*AlertQuery{
			{
				DatasourceUID: "prometheus",
				RelativeTimeRange: &RelativeTimeRange{
					From: 600,
					To:   0,
				},
				Model: AlertQueryModel{
					Expr: "vector(1)",
				},
			},
			{
				DatasourceUID: "__expr__",
				Model: AlertQueryModel{
					Type:       "reduce",
					Expression: "A",
					Reducer:    "last",
				},
			},
			{
				DatasourceUID: "__expr__",
				Model: AlertQueryModel{
					Type:       "threshold",
					Expression: "B",
					Conditions: []AlertCondition{
						{Evaluator: ConditionEvaluator{Type: "gt", Params: []float64{0}}},
					},
				},
			},
		}

		testUID := "test_create_auto_refid"
		t.Cleanup(func() {
			manageRulesReadWrite(ctx, ManageRulesReadWriteParams{Operation: "delete", RuleUID: testUID}) //nolint:errcheck
		})

		result, err := manageRulesReadWrite(ctx, ManageRulesReadWriteParams{
			Operation:    "create",
			RuleUID:      testUID,
			Title:        "Test Auto RefID Rule",
			RuleGroup:    "test-group",
			FolderUID:    "tests",
			Condition:    "C",
			Data:         data,
			NoDataState:  "OK",
			ExecErrState: "OK",
			For:          "5m",
			OrgID:        1,
		})
		require.NoError(t, err)

		created, ok := result.(*models.ProvisionedAlertRule)
		require.True(t, ok)
		require.Equal(t, testUID, created.UID)
		require.Len(t, created.Data, 3)
		require.Equal(t, "A", created.Data[0].RefID)
		require.Equal(t, "B", created.Data[1].RefID)
		require.Equal(t, "C", created.Data[2].RefID)
	})

	t.Run("create recording rule type", func(t *testing.T) {
		ctx := newTestContext()

		testUID := ruleIDRecording + "_create"
		t.Cleanup(func() {
			manageRulesReadWrite(ctx, ManageRulesReadWriteParams{Operation: "delete", RuleUID: testUID}) //nolint:errcheck
		})

		recordParams := &Record{
			From:                ptrString("A"),
			Metric:              ptrString("test_recording_metric"),
			TargetDatasourceUID: "prometheus",
		}

		result, err := manageRulesReadWrite(ctx, ManageRulesReadWriteParams{
			Operation:    "create",
			RuleUID:      testUID,
			Title:        "Test Recording Rule",
			RuleGroup:    "test-group",
			FolderUID:    "tests",
			Condition:    "A",
			Data:         recordingRuleTypeQuery,
			NoDataState:  "OK",
			ExecErrState: "OK",
			For:          "5m",
			Record:       recordParams,
			Annotations: map[string]string{
				"summary": "Recording rule for test",
			},
			Labels: map[string]string{
				"team": "recording-team",
			},
			OrgID: 1,
		})
		require.NoError(t, err, "create recording rule should not return error")

		created, ok := result.(*models.ProvisionedAlertRule)
		require.True(t, ok, "result should be a *models.ProvisionedAlertRule")

		t.Run("test_response", func(t *testing.T) {
			require.Equal(t, testUID, created.UID, "UID should match the requested value")
			require.Equal(t, "Test Recording Rule", *created.Title, "title should match the requested value")
			require.Equal(t, "test-group", *created.RuleGroup, "rule_group should match the requested value")
			if created.Record == nil {
				require.Equal(t, "A", *created.Condition, "condition should match for alerting rule")
			}
			require.Len(t, created.Data, 1, "data should contain exactly one query")
			require.Equal(t, "A", created.Data[0].RefID, "data[0].refId should match")

			// Record fields
			require.NotNil(t, created.Record, "record should not be nil for recording rule")
			require.Equal(t, "A", *created.Record.From, "record.from should match the requested value")
			require.Equal(t, "test_recording_metric", *created.Record.Metric, "record.metric should match the requested value")
			require.Equal(t, "prometheus", created.Record.TargetDatasourceUID, "record.targetDatasourceUid should match the requested value")
		})

		t.Log("Recording rule created successfully with UID:", created.UID)
	})

	t.Run("create alert rule with missing required fields", func(t *testing.T) {
		ctx := newTestContext()

		_, err := manageRulesReadWrite(ctx, ManageRulesReadWriteParams{
			Operation: "create",
			Title:     "Incomplete Rule",
		})

		require.Error(t, err)
		require.Contains(t, err.Error(), "rule_group is required")
	})

	t.Run("create alert rule with empty title", func(t *testing.T) {
		ctx := newTestContext()

		_, err := manageRulesReadWrite(ctx, ManageRulesReadWriteParams{
			Operation: "create",
			Title:     "",
		})

		require.Error(t, err)
		require.Contains(t, err.Error(), "title is required")
	})
}

func TestManageRules_Update(t *testing.T) {
	t.Run("update existing alert rule", func(t *testing.T) {
		ctx := newTestContext()

		sampleData := []*AlertQuery{
			{
				RefID:         "A",
				DatasourceUID: "prometheus-uid",
				RelativeTimeRange: &RelativeTimeRange{
					From: 600,
					To:   0,
				},
				Model: AlertQueryModel{
					Expr: "up",
				},
			},
		}

		testUID := "test_update_alert_rule"
		t.Cleanup(func() {
			manageRulesReadWrite(ctx, ManageRulesReadWriteParams{Operation: "delete", RuleUID: testUID}) //nolint:errcheck
		})

		// Create the rule first
		_, err := manageRulesReadWrite(ctx, ManageRulesReadWriteParams{
			Operation:    "create",
			RuleUID:      testUID,
			Title:        "Original Title",
			RuleGroup:    "test-group",
			FolderUID:    "tests",
			Condition:    "A",
			Data:         sampleData,
			NoDataState:  "OK",
			ExecErrState: "OK",
			For:          "5m",
			OrgID:        1,
		})
		require.NoError(t, err)

		// Update basic fields
		result, err := manageRulesReadWrite(ctx, ManageRulesReadWriteParams{
			Operation:    "update",
			RuleUID:      testUID,
			Title:        "Updated Title",
			RuleGroup:    "test-group",
			FolderUID:    "tests",
			Condition:    "A",
			Data:         sampleData,
			NoDataState:  "Alerting",
			ExecErrState: "Alerting",
			For:          "10m",
			Annotations: map[string]string{
				"summary": "Updated alert rule",
			},
			Labels: map[string]string{
				"team": "updated-team",
			},
			KeepFiringFor:               "5m",
			IsPaused:                    false,
			MissingSeriesEvalsToResolve: 2,
			OrgID:                       1,
		})
		require.NoError(t, err)

		updated, ok := result.(*models.ProvisionedAlertRule)
		require.True(t, ok)
		require.Equal(t, testUID, updated.UID)
		require.Equal(t, "Updated Title", *updated.Title)
		require.Equal(t, "Alerting", *updated.NoDataState)
		require.Equal(t, "Alerting", *updated.ExecErrState)
		require.Equal(t, strfmt.Duration(5*time.Minute), updated.KeepFiringFor)
		require.False(t, updated.IsPaused)
		require.Equal(t, int64(2), updated.MissingSeriesEvalsToResolve)
	})

	t.Run("update notification settings of existing alert", func(t *testing.T) {
		ctx := newTestContext()

		sampleData := []*AlertQuery{
			{
				RefID:         "A",
				DatasourceUID: "prometheus",
				RelativeTimeRange: &RelativeTimeRange{
					From: 600,
					To:   0,
				},
				Model: AlertQueryModel{
					Expr: "up",
				},
			},
		}

		testUID := "test_update_notification_settings"
		t.Cleanup(func() {
			manageRulesReadWrite(ctx, ManageRulesReadWriteParams{Operation: "delete", RuleUID: testUID}) //nolint:errcheck
		})

		_, err := manageRulesReadWrite(ctx, ManageRulesReadWriteParams{
			Operation:    "create",
			RuleUID:      testUID,
			Title:        "Notification Settings Test Rule",
			RuleGroup:    "test-group",
			FolderUID:    "tests",
			Condition:    "A",
			Data:         sampleData,
			NoDataState:  "OK",
			ExecErrState: "OK",
			For:          "5m",
			OrgID:        1,
		})
		require.NoError(t, err)

		// Update rule with notification settings
		result, err := manageRulesReadWrite(ctx, ManageRulesReadWriteParams{
			Operation:    "update",
			RuleUID:      testUID,
			Title:        "Notification Settings Test Rule",
			RuleGroup:    "test-group",
			FolderUID:    "tests",
			Condition:    "A",
			Data:         sampleData,
			NoDataState:  "OK",
			ExecErrState: "OK",
			For:          "5m",
			NotificationSettings: &NotificationSettings{
				Receiver:          ptrString("Email1"),
				GroupBy:           []string{"alertname", "grafana_folder"},
				GroupWait:         "30s",
				GroupInterval:     "5m",
				RepeatInterval:    "4h",
				MuteTimeIntervals: []string{"weekends"},
			},
			OrgID: 1,
		})
		require.NoError(t, err)

		updated, ok := result.(*models.ProvisionedAlertRule)
		require.True(t, ok)
		require.Equal(t, testUID, updated.UID)

		// NotificationSettings
		require.NotNil(t, updated.NotificationSettings)
		require.Equal(t, "Email1", *updated.NotificationSettings.Receiver)
		require.ElementsMatch(t, []string{"alertname", "grafana_folder"}, updated.NotificationSettings.GroupBy)
		require.Equal(t, "30s", updated.NotificationSettings.GroupWait)
		require.Equal(t, "5m", updated.NotificationSettings.GroupInterval)
		require.Equal(t, "4h", updated.NotificationSettings.RepeatInterval)
		require.ElementsMatch(t, []string{"weekends"}, updated.NotificationSettings.MuteTimeIntervals)
	})

	t.Run("update an alert rule to recording type", func(t *testing.T) {
		ctx := newTestContext()

		sampleData := []*AlertQuery{
			{
				RefID:         "A",
				DatasourceUID: "prometheus",
				RelativeTimeRange: &RelativeTimeRange{
					From: 600,
					To:   0,
				},
				Model: AlertQueryModel{
					Expr: "up",
				},
			},
		}

		testUID := "test_update_record"
		t.Cleanup(func() {
			manageRulesReadWrite(ctx, ManageRulesReadWriteParams{Operation: "delete", RuleUID: testUID}) //nolint:errcheck
		})

		// Create initial rule (alerting rule)
		_, err := manageRulesReadWrite(ctx, ManageRulesReadWriteParams{
			Operation:    "create",
			RuleUID:      testUID,
			Title:        "Record Test Rule",
			RuleGroup:    "test-group",
			FolderUID:    "tests",
			Condition:    "A",
			Data:         sampleData,
			NoDataState:  "OK",
			ExecErrState: "OK",
			For:          "5m",
			OrgID:        1,
		})
		require.NoError(t, err)

		// Update rule with Record (converts to recording rule).
		// Grafana ignores NoDataState/ExecErrState for recording rules, but our
		// validator requires them, so we pass dummy values.
		result, err := manageRulesReadWrite(ctx, ManageRulesReadWriteParams{
			Operation:    "update",
			RuleUID:      testUID,
			Title:        "Record Test Rule",
			RuleGroup:    "test-group",
			FolderUID:    "tests",
			Condition:    "A",
			Data:         sampleData,
			NoDataState:  "OK",
			ExecErrState: "OK",
			For:          "5m",
			Record: &Record{
				From:                ptrString("A"),
				Metric:              ptrString("test_metric_record"),
				TargetDatasourceUID: "prometheus",
			},
			OrgID: 1,
		})
		require.NoError(t, err)

		updated, ok := result.(*models.ProvisionedAlertRule)
		require.True(t, ok)
		require.Equal(t, testUID, updated.UID)

		// Record
		require.NotNil(t, updated.Record)
		require.Equal(t, "A", *updated.Record.From)
		require.Equal(t, "test_metric_record", *updated.Record.Metric)
		require.Equal(t, "prometheus", updated.Record.TargetDatasourceUID)
	})

	t.Run("update a recoding rule query", func(t *testing.T) {
		ctx := newTestContext()

		testUID := ruleIDRecording + "_update_query"
		t.Cleanup(func() {
			manageRulesReadWrite(ctx, ManageRulesReadWriteParams{Operation: "delete", RuleUID: testUID}) //nolint:errcheck
		})

		// Create a recording rule
		_, err := manageRulesReadWrite(ctx, ManageRulesReadWriteParams{
			Operation:    "create",
			RuleUID:      testUID,
			Title:        "Recording Rule Update Query Test",
			RuleGroup:    "test-group",
			FolderUID:    "tests",
			Condition:    "A",
			Data:         recordingRuleTypeQuery,
			NoDataState:  "OK",
			ExecErrState: "OK",
			For:          "5m",
			Record: &Record{
				From:                ptrString("A"),
				Metric:              ptrString("original_recording_metric"),
				TargetDatasourceUID: "prometheus",
			},
			OrgID: 1,
		})
		require.NoError(t, err, "create recording rule should not return error")

		// Update with a new query expression and new record metric
		updatedQuery := []*AlertQuery{
			{
				RefID:         "A",
				DatasourceUID: "prometheus",
				RelativeTimeRange: &RelativeTimeRange{
					From: 900,
					To:   0,
				},
				Model: AlertQueryModel{
					Expr: "sum(rate(http_requests_total[5m]))",
				},
			},
		}

		updateResult, err := manageRulesReadWrite(ctx, ManageRulesReadWriteParams{
			Operation:    "update",
			RuleUID:      testUID,
			Title:        "Recording Rule Update Query Test - Updated",
			RuleGroup:    "test-group",
			FolderUID:    "tests",
			Condition:    "A",
			Data:         updatedQuery,
			NoDataState:  "OK",
			ExecErrState: "OK",
			For:          "10m",
			Record: &Record{
				From:                ptrString("A"),
				Metric:              ptrString("updated_recording_metric"),
				TargetDatasourceUID: "prometheus",
			},
			OrgID: 1,
		})
		require.NoError(t, err, "update recording rule should not return error")

		updated, ok := updateResult.(*models.ProvisionedAlertRule)
		require.True(t, ok, "result should be a *models.ProvisionedAlertRule")

		t.Run("test_response", func(t *testing.T) {
			require.Equal(t, testUID, updated.UID, "updated UID should match")
			require.Equal(t, "Recording Rule Update Query Test - Updated", *updated.Title, "updated title should match")
			require.Len(t, updated.Data, 1, "updated data should contain exactly one query")
			require.Equal(t, "A", updated.Data[0].RefID, "updated data[0].refId should match")

			// Record fields
			require.NotNil(t, updated.Record, "updated record should not be nil")
			require.Equal(t, "A", *updated.Record.From, "updated record.from should match")
			require.Equal(t, "updated_recording_metric", *updated.Record.Metric, "updated record.metric should match the new value")
			require.Equal(t, "prometheus", updated.Record.TargetDatasourceUID, "updated record.targetDatasourceUid should match")
		})

		t.Log("Recording rule updated successfully with new query and metric")
	})

	t.Run("update non-existent alert rule", func(t *testing.T) {
		ctx := newTestContext()

		_, err := manageRulesReadWrite(ctx, ManageRulesReadWriteParams{
			Operation:    "update",
			RuleUID:      "non-existent-uid",
			Title:        "Updated Title",
			RuleGroup:    "test-group",
			FolderUID:    "tests",
			Condition:    "A",
			Data:         []*AlertQuery{},
			NoDataState:  "OK",
			ExecErrState: "OK",
			For:          "5m",
			OrgID:        1,
		})
		require.Error(t, err)
	})

	t.Run("update alert rule with empty rule_uid", func(t *testing.T) {
		ctx := newTestContext()

		_, err := manageRulesReadWrite(ctx, ManageRulesReadWriteParams{
			Operation: "update",
			RuleUID:   "",
		})

		require.Error(t, err)
		require.Contains(t, err.Error(), "rule_uid is required")
	})

	t.Run("alert rule should have identical configuration for same state after update", func(t *testing.T) {
		ctx := newTestContext()

		sampleData := []*AlertQuery{
			{
				RefID:         "A",
				DatasourceUID: "prometheus",
				RelativeTimeRange: &RelativeTimeRange{
					From: 600,
					To:   0,
				},
				Model: AlertQueryModel{
					Expr: "up",
				},
			},
		}

		testUID := "test_identical_alert_rule"
		t.Cleanup(func() {
			manageRulesReadWrite(ctx, ManageRulesReadWriteParams{Operation: "delete", RuleUID: testUID}) //nolint:errcheck
		})

		disableProvenance := false
		initialParams := ManageRulesReadWriteParams{
			Operation:                   "create",
			RuleUID:                     testUID,
			Title:                       "Identical Test Alert",
			RuleGroup:                   "test-group",
			FolderUID:                   "tests",
			Condition:                   "A",
			Data:                        sampleData,
			NoDataState:                 "OK",
			ExecErrState:                "OK",
			For:                         "5m",
			KeepFiringFor:               "10m",
			IsPaused:                    true,
			Annotations:                 map[string]string{"summary": "test"},
			Labels:                      map[string]string{"team": "test"},
			MissingSeriesEvalsToResolve: 3,
			OrgID:                       1,
			DisableProvenance:           &disableProvenance,
			NotificationSettings: &NotificationSettings{
				Receiver: ptrString("Email1"),
				GroupBy:  []string{"alertname"},
			},
		}

		_, err := manageRulesReadWrite(ctx, initialParams)
		require.NoError(t, err, "should create the alert rule")

		updateParams := initialParams
		updateParams.Operation = "update"
		updateParams.Title = "Identical Test Alert - Updated"
		updateParams.For = "15m"
		updateParams.IsPaused = false
		updateParams.NotificationSettings.Receiver = ptrString("Email2")

		_, err = manageRulesReadWrite(ctx, updateParams)
		require.NoError(t, err, "should update the alert rule")

		fetchResult, err := manageRulesRead(ctx, ManageRulesReadParams{
			Operation: "get",
			RuleUID:   testUID,
		})
		require.NoError(t, err, "should fetch the alert rule")

		detail, ok := fetchResult.(*alertRuleDetail)
		require.True(t, ok, "result should be an *alertRuleDetail")

		require.Equal(t, updateParams.Title, detail.Title, "title should match")
		require.Equal(t, updateParams.RuleGroup, detail.RuleGroup, "rule group should match")
		require.Equal(t, updateParams.FolderUID, detail.FolderUID, "folder UID should match")
		require.Equal(t, updateParams.Condition, detail.Condition, "condition should match")
		require.Equal(t, updateParams.NoDataState, detail.NoDataState, "no data state should match")
		require.Equal(t, updateParams.ExecErrState, detail.ExecErrState, "exec err state should match")
		require.Equal(t, "15m0s", detail.For, "for duration should match")
		require.Equal(t, "10m0s", detail.KeepFiringFor, "keep firing for should match")
		require.False(t, detail.IsPaused, "is paused should match")
		require.Equal(t, int64(3), detail.MissingSeriesEvalsToResolve, "missing series evals should match")
		require.Equal(t, updateParams.Annotations, detail.Annotations, "annotations should match")
		require.Equal(t, updateParams.Labels, detail.Labels, "labels should match")
		require.NotNil(t, detail.NotificationSettings, "notification settings should not be nil")
		require.Equal(t, "Email2", *detail.NotificationSettings.Receiver, "notification receiver should match")
	})

	t.Run("recording rule should have identical configuration for same state after update", func(t *testing.T) {
		ctx := newTestContext()

		testUID := "test_identical_recording_rule"
		t.Cleanup(func() {
			manageRulesReadWrite(ctx, ManageRulesReadWriteParams{Operation: "delete", RuleUID: testUID}) //nolint:errcheck
		})

		disableProvenance := true
		initialParams := ManageRulesReadWriteParams{
			Operation:    "create",
			RuleUID:      testUID,
			Title:        "Identical Test Recording",
			RuleGroup:    "test-group",
			FolderUID:    "tests",
			Condition:    "A",
			Data:         recordingRuleTypeQuery,
			NoDataState:  "OK",
			ExecErrState: "OK",
			For:          "5m",
			Record: &Record{
				From:                ptrString("A"),
				Metric:              ptrString("test_metric"),
				TargetDatasourceUID: "prometheus",
			},
			Annotations: map[string]string{
				"summary": "recording test",
			},
			Labels: map[string]string{
				"team": "recording",
			},
			OrgID:             1,
			DisableProvenance: &disableProvenance,
		}

		_, err := manageRulesReadWrite(ctx, initialParams)
		require.NoError(t, err, "should create the recording rule")

		updateParams := initialParams
		updateParams.Operation = "update"
		updateParams.Title = "Identical Test Recording - Updated"
		updateParams.Record.Metric = ptrString("test_metric_updated")
		updateParams.Labels["team"] = "recording-updated"

		_, err = manageRulesReadWrite(ctx, updateParams)
		require.NoError(t, err, "should update the recording rule")

		fetchResult, err := manageRulesRead(ctx, ManageRulesReadParams{
			Operation: "get",
			RuleUID:   testUID,
		})
		require.NoError(t, err, "should fetch the recording rule")

		detail, ok := fetchResult.(*alertRuleDetail)
		require.True(t, ok, "result should be an *alertRuleDetail")

		require.Equal(t, updateParams.Title, detail.Title, "title should match")
		require.NotNil(t, detail.Record, "record should not be nil")
		require.Equal(t, "A", *detail.Record.From, "record.from should match")
		require.Equal(t, "test_metric_updated", *detail.Record.Metric, "record.metric should match")
		require.Equal(t, "prometheus", detail.Record.TargetDatasourceUID, "record.targetDatasourceUid should match")
		require.Equal(t, updateParams.Annotations, detail.Annotations, "annotations should match")
		require.Equal(t, updateParams.Labels, detail.Labels, "labels should match")
	})

}

func TestManageRules_Delete(t *testing.T) {
	t.Run("delete existing alert rule", func(t *testing.T) {
		ctx := newTestContext()

		sampleData := []*AlertQuery{
			{
				RefID:         "A",
				DatasourceUID: "prometheus-uid",
				RelativeTimeRange: &RelativeTimeRange{
					From: 600,
					To:   0,
				},
				Model: AlertQueryModel{
					Expr: "up",
				},
			},
		}

		testUID := "test_delete_alert_rule"
		t.Cleanup(func() {
			manageRulesReadWrite(ctx, ManageRulesReadWriteParams{Operation: "delete", RuleUID: testUID}) //nolint:errcheck
		})

		// Create the rule first
		_, err := manageRulesReadWrite(ctx, ManageRulesReadWriteParams{
			Operation:    "create",
			RuleUID:      testUID,
			Title:        "Rule to Delete",
			RuleGroup:    "test-group",
			FolderUID:    "tests",
			Condition:    "A",
			Data:         sampleData,
			NoDataState:  "OK",
			ExecErrState: "OK",
			For:          "5m",
			OrgID:        1,
		})
		require.NoError(t, err)

		// Now delete it
		result, err := manageRulesReadWrite(ctx, ManageRulesReadWriteParams{
			Operation: "delete",
			RuleUID:   testUID,
		})
		require.NoError(t, err)

		msg, ok := result.(string)
		require.True(t, ok)
		require.Contains(t, msg, "deleted successfully")
		require.Contains(t, msg, testUID)

		// Verify it's gone by trying to get it
		_, getErr := manageRulesRead(ctx, ManageRulesReadParams{
			Operation: "get",
			RuleUID:   testUID,
		})
		require.Error(t, getErr)
	})

	t.Run("delete non-existent alert rule", func(t *testing.T) {
		ctx := newTestContext()

		result, err := manageRulesReadWrite(ctx, ManageRulesReadWriteParams{
			Operation: "delete",
			RuleUID:   "non-existent-uid",
		})
		require.NoError(t, err) // DELETE is idempotent

		msg, ok := result.(string)
		require.True(t, ok)
		require.Contains(t, msg, "deleted successfully")
		require.Contains(t, msg, "non-existent-uid")
	})

	t.Run("delete alert rule with empty rule_uid", func(t *testing.T) {
		ctx := newTestContext()

		_, err := manageRulesReadWrite(ctx, ManageRulesReadWriteParams{
			Operation: "delete",
			RuleUID:   "",
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), "rule_uid is required")
	})
}

// createTestAlertQueries creates sample alert query data for testing DisableProvenance
func createTestAlertQueries() []*AlertQuery {
	return []*AlertQuery{
		{
			RefID:         "A",
			DatasourceUID: "prometheus",
			RelativeTimeRange: &RelativeTimeRange{
				From: 600,
				To:   0,
			},
			Model: AlertQueryModel{
				Expr: "vector(1)",
			},
		},
		{
			RefID:         "B",
			DatasourceUID: "__expr__",
			Model: AlertQueryModel{
				Type:       "reduce",
				Expression: "A",
				Reducer:    "last",
			},
		},
		{
			RefID:         "C",
			DatasourceUID: "__expr__",
			Model: AlertQueryModel{
				Type:       "threshold",
				Expression: "B",
				Conditions: []AlertCondition{
					{Evaluator: ConditionEvaluator{Type: "gt", Params: []float64{0}}},
				},
			},
		},
	}
}

func TestManageRules_DisableProvenance(t *testing.T) {
	t.Run("create alert rule with disable_provenance true (default)", func(t *testing.T) {
		ctx := newTestContext()

		sampleData := createTestAlertQueries()

		testUID := "test_provenance_default"
		t.Cleanup(func() {
			manageRulesReadWrite(ctx, ManageRulesReadWriteParams{Operation: "delete", RuleUID: testUID}) //nolint:errcheck
		})

		result, err := manageRulesReadWrite(ctx, ManageRulesReadWriteParams{
			Operation:    "create",
			RuleUID:      testUID,
			Title:        "Test Provenance Default",
			RuleGroup:    "test-group",
			FolderUID:    "tests",
			Condition:    "C",
			Data:         sampleData,
			NoDataState:  "OK",
			ExecErrState: "OK",
			For:          "5m",
			OrgID:        1,
			// DisableProvenance not set - should default to true
		})
		require.NoError(t, err)

		created, ok := result.(*models.ProvisionedAlertRule)
		require.True(t, ok)
		require.Equal(t, testUID, created.UID)
		require.Empty(t, created.Provenance, "expected empty provenance when disable_provenance defaults to true")
	})

	t.Run("create alert rule with disable_provenance explicitly true", func(t *testing.T) {
		ctx := newTestContext()

		sampleData := createTestAlertQueries()

		disableProvenance := true
		testUID := "test_provenance_true"
		t.Cleanup(func() {
			manageRulesReadWrite(ctx, ManageRulesReadWriteParams{Operation: "delete", RuleUID: testUID}) //nolint:errcheck
		})

		result, err := manageRulesReadWrite(ctx, ManageRulesReadWriteParams{
			Operation:         "create",
			RuleUID:           testUID,
			Title:             "Test Provenance True",
			RuleGroup:         "test-group",
			FolderUID:         "tests",
			Condition:         "C",
			Data:              sampleData,
			NoDataState:       "OK",
			ExecErrState:      "OK",
			For:               "5m",
			OrgID:             1,
			DisableProvenance: &disableProvenance,
		})
		require.NoError(t, err)

		created, ok := result.(*models.ProvisionedAlertRule)
		require.True(t, ok)
		require.Equal(t, testUID, created.UID)
		require.Empty(t, created.Provenance, "expected empty provenance when disable_provenance is true")
	})

	t.Run("create alert rule with disable_provenance false", func(t *testing.T) {
		ctx := newTestContext()

		sampleData := createTestAlertQueries()

		disableProvenance := false
		testUID := "test_provenance_false"
		t.Cleanup(func() {
			manageRulesReadWrite(ctx, ManageRulesReadWriteParams{Operation: "delete", RuleUID: testUID}) //nolint:errcheck
		})

		result, err := manageRulesReadWrite(ctx, ManageRulesReadWriteParams{
			Operation:         "create",
			RuleUID:           testUID,
			Title:             "Test Provenance False",
			RuleGroup:         "test-group",
			FolderUID:         "tests",
			Condition:         "C",
			Data:              sampleData,
			NoDataState:       "OK",
			ExecErrState:      "OK",
			For:               "5m",
			OrgID:             1,
			DisableProvenance: &disableProvenance,
		})
		require.NoError(t, err)

		created, ok := result.(*models.ProvisionedAlertRule)
		require.True(t, ok)
		require.Equal(t, testUID, created.UID)
		require.Equal(t, models.Provenance("api"), created.Provenance, "expected provenance 'api' when disable_provenance is false")
	})

	t.Run("update alert rule with disable_provenance true (default)", func(t *testing.T) {
		ctx := newTestContext()

		sampleData := createTestAlertQueries()

		testUID := "test_update_provenance_default"
		t.Cleanup(func() {
			manageRulesReadWrite(ctx, ManageRulesReadWriteParams{Operation: "delete", RuleUID: testUID}) //nolint:errcheck
		})

		_, err := manageRulesReadWrite(ctx, ManageRulesReadWriteParams{
			Operation:    "create",
			RuleUID:      testUID,
			Title:        "Test Update Provenance Default",
			RuleGroup:    "test-group",
			FolderUID:    "tests",
			Condition:    "C",
			Data:         sampleData,
			NoDataState:  "OK",
			ExecErrState: "OK",
			For:          "5m",
			OrgID:        1,
		})
		require.NoError(t, err)

		// Now update it without setting disable_provenance (should default to true)
		result, err := manageRulesReadWrite(ctx, ManageRulesReadWriteParams{
			Operation:    "update",
			RuleUID:      testUID,
			Title:        "Test Update Provenance Default - Updated",
			RuleGroup:    "test-group",
			FolderUID:    "tests",
			Condition:    "C",
			Data:         sampleData,
			NoDataState:  "OK",
			ExecErrState: "OK",
			For:          "10m",
			OrgID:        1,
		})
		require.NoError(t, err)

		updated, ok := result.(*models.ProvisionedAlertRule)
		require.True(t, ok)
		require.Equal(t, "Test Update Provenance Default - Updated", *updated.Title)
		require.Empty(t, updated.Provenance, "expected empty provenance when disable_provenance defaults to true")
	})

	t.Run("update alert rule with disable_provenance explicitly true", func(t *testing.T) {
		ctx := newTestContext()

		sampleData := createTestAlertQueries()

		testUID := "test_update_provenance"
		t.Cleanup(func() {
			manageRulesReadWrite(ctx, ManageRulesReadWriteParams{Operation: "delete", RuleUID: testUID}) //nolint:errcheck
		})

		_, err := manageRulesReadWrite(ctx, ManageRulesReadWriteParams{
			Operation:    "create",
			RuleUID:      testUID,
			Title:        "Test Update Provenance",
			RuleGroup:    "test-group",
			FolderUID:    "tests",
			Condition:    "C",
			Data:         sampleData,
			NoDataState:  "OK",
			ExecErrState: "OK",
			For:          "5m",
			OrgID:        1,
		})
		require.NoError(t, err)

		// Now update it with disable_provenance true
		disableProvenance := true
		result, err := manageRulesReadWrite(ctx, ManageRulesReadWriteParams{
			Operation:         "update",
			RuleUID:           testUID,
			Title:             "Test Update Provenance - Updated",
			RuleGroup:         "test-group",
			FolderUID:         "tests",
			Condition:         "C",
			Data:              sampleData,
			NoDataState:       "OK",
			ExecErrState:      "OK",
			For:               "10m",
			OrgID:             1,
			DisableProvenance: &disableProvenance,
		})
		require.NoError(t, err)

		updated, ok := result.(*models.ProvisionedAlertRule)
		require.True(t, ok)
		require.Equal(t, "Test Update Provenance - Updated", *updated.Title)
		require.Empty(t, updated.Provenance, "expected empty provenance when disable_provenance is true")
	})

	t.Run("update alert rule with disable_provenance false", func(t *testing.T) {
		ctx := newTestContext()

		sampleData := createTestAlertQueries()

		testUID := "test_update_provenance_false"
		t.Cleanup(func() {
			manageRulesReadWrite(ctx, ManageRulesReadWriteParams{Operation: "delete", RuleUID: testUID}) //nolint:errcheck
		})

		_, err := manageRulesReadWrite(ctx, ManageRulesReadWriteParams{
			Operation:    "create",
			RuleUID:      testUID,
			Title:        "Test Update Provenance False",
			RuleGroup:    "test-group",
			FolderUID:    "tests",
			Condition:    "C",
			Data:         sampleData,
			NoDataState:  "OK",
			ExecErrState: "OK",
			For:          "5m",
			OrgID:        1,
		})
		require.NoError(t, err)

		// Now update it with disable_provenance false (should set provenance to "api")
		disableProvenance := false
		result, err := manageRulesReadWrite(ctx, ManageRulesReadWriteParams{
			Operation:         "update",
			RuleUID:           testUID,
			Title:             "Test Update Provenance False - Updated",
			RuleGroup:         "test-group",
			FolderUID:         "tests",
			Condition:         "C",
			Data:              sampleData,
			NoDataState:       "OK",
			ExecErrState:      "OK",
			For:               "10m",
			OrgID:             1,
			DisableProvenance: &disableProvenance,
		})
		require.NoError(t, err)

		updated, ok := result.(*models.ProvisionedAlertRule)
		require.True(t, ok)
		require.Equal(t, "Test Update Provenance False - Updated", *updated.Title)
		require.Equal(t, models.Provenance("api"), updated.Provenance, "expected provenance 'api' when disable_provenance is false")
	})
}

func TestManageRules_List_Datasource(t *testing.T) {
	t.Run("list Prometheus-managed alert rules", func(t *testing.T) {
		ctx := newTestContext()
		dsUID := "prometheus"

		result, err := manageRulesRead(ctx, ManageRulesReadParams{
			Operation:     "list",
			DatasourceUID: &dsUID,
		})
		require.NoError(t, err)

		rules, ok := result.([]alertRuleSummary)
		require.True(t, ok)
		require.NotEmpty(t, rules, "Expected Prometheus to have alert rules configured")

		foundFiring := false
		for _, rule := range rules {
			require.NotEmpty(t, rule.Title)
			if rule.Title == "PrometheusTestAlertFiring" {
				foundFiring = true
				require.Equal(t, "warning", rule.Labels["severity"])
				require.Equal(t, "test", rule.Labels["environment"])
			}
		}
		require.True(t, foundFiring, "Expected to find PrometheusTestAlertFiring rule")
	})

	t.Run("list Prometheus rules with label selector", func(t *testing.T) {
		ctx := newTestContext()
		dsUID := "prometheus"

		result, err := manageRulesRead(ctx, ManageRulesReadParams{
			listFilterParams: listFilterParams{
				LabelSelectors: []Selector{
					{
						Filters: []LabelMatcher{
							{Name: "severity", Type: "=", Value: "warning"},
						},
					},
				},
			},
			Operation:     "list",
			DatasourceUID: &dsUID,
		})
		require.NoError(t, err)

		rules, ok := result.([]alertRuleSummary)
		require.True(t, ok)
		for _, rule := range rules {
			require.Equal(t, "warning", rule.Labels["severity"])
		}
	})

	t.Run("list datasource rules - invalid datasource type", func(t *testing.T) {
		ctx := newTestContext()
		dsUID := "tempo"

		_, err := manageRulesRead(ctx, ManageRulesReadParams{
			Operation:     "list",
			DatasourceUID: &dsUID,
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), "does not support ruler API")
	})

	t.Run("list datasource rules - nonexistent datasource", func(t *testing.T) {
		ctx := newTestContext()
		dsUID := "nonexistent"

		_, err := manageRulesRead(ctx, ManageRulesReadParams{
			Operation:     "list",
			DatasourceUID: &dsUID,
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), "not found")
	})

	t.Run("list Loki-managed alert rules", func(t *testing.T) {
		ctx := newTestContext()
		dsUID := "loki"

		result, err := manageRulesRead(ctx, ManageRulesReadParams{
			Operation:     "list",
			DatasourceUID: &dsUID,
		})
		if err != nil {
			t.Logf("Loki ruler query failed (this may be expected): %v", err)
		} else {
			rules, ok := result.([]alertRuleSummary)
			require.True(t, ok)
			t.Logf("Loki ruler returned %d rules", len(rules))
		}
	})
}

func TestManageRouting_GetContactPoints_Alertmanager(t *testing.T) {
	t.Run("list Alertmanager receivers", func(t *testing.T) {
		ctx := newTestContext()
		dsUID := "alertmanager"

		result, err := manageRouting(ctx, ManageRoutingParams{
			Operation:     "get_contact_points",
			DatasourceUID: &dsUID,
		})
		require.NoError(t, err)

		cps, ok := result.([]contactPointSummary)
		require.True(t, ok)
		require.NotEmpty(t, cps, "Expected Alertmanager to have receivers configured")

		receiverNames := []string{}
		for _, cp := range cps {
			receiverNames = append(receiverNames, cp.Name)
			require.Empty(t, cp.UID)
		}
		require.Contains(t, receiverNames, "test-receiver")
		require.Contains(t, receiverNames, "test-email")
		require.Contains(t, receiverNames, "test-slack")
	})

	t.Run("list Alertmanager receivers with name filter", func(t *testing.T) {
		ctx := newTestContext()
		dsUID := "alertmanager"
		name := "test-receiver"

		result, err := manageRouting(ctx, ManageRoutingParams{
			Operation:     "get_contact_points",
			DatasourceUID: &dsUID,
			Name:          &name,
		})
		require.NoError(t, err)

		cps, ok := result.([]contactPointSummary)
		require.True(t, ok)
		require.Len(t, cps, 1)
		require.Equal(t, "test-receiver", cps[0].Name)
	})

	t.Run("list contact points - invalid datasource type", func(t *testing.T) {
		ctx := newTestContext()
		dsUID := "prometheus"

		_, err := manageRouting(ctx, ManageRoutingParams{
			Operation:     "get_contact_points",
			DatasourceUID: &dsUID,
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), "is not an Alertmanager datasource")
	})

	t.Run("list contact points - nonexistent datasource", func(t *testing.T) {
		ctx := newTestContext()
		dsUID := "nonexistent"

		_, err := manageRouting(ctx, ManageRoutingParams{
			Operation:     "get_contact_points",
			DatasourceUID: &dsUID,
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), "not found")
	})
}

func TestManageRouting_GetNotificationPolicies(t *testing.T) {
	t.Run("get notification policies", func(t *testing.T) {
		ctx := newTestContext()
		result, err := manageRouting(ctx, ManageRoutingParams{
			Operation: "get_notification_policies",
		})
		require.NoError(t, err)

		route, ok := result.(*models.Route)
		require.True(t, ok)
		require.NotEmpty(t, route.Receiver, "default receiver should be set")
	})
}

func TestManageRouting_GetContactPointDetail(t *testing.T) {
	t.Run("get contact point by name", func(t *testing.T) {
		ctx := newTestContext()
		title := "Email1"
		result, err := manageRouting(ctx, ManageRoutingParams{
			Operation:         "get_contact_point",
			ContactPointTitle: &title,
		})
		require.NoError(t, err)

		cps, ok := result.([]*models.EmbeddedContactPoint)
		require.True(t, ok)
		require.Len(t, cps, 1)
		require.Equal(t, "Email1", cps[0].Name)
		require.NotNil(t, cps[0].Type)
		require.Equal(t, "email", *cps[0].Type)
	})

	t.Run("get nonexistent contact point", func(t *testing.T) {
		ctx := newTestContext()
		title := "NonExistentContactPoint"
		_, err := manageRouting(ctx, ManageRoutingParams{
			Operation:         "get_contact_point",
			ContactPointTitle: &title,
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), "not found")
	})
}

func TestManageRouting_GetTimeIntervals(t *testing.T) {
	t.Run("list time intervals", func(t *testing.T) {
		ctx := newTestContext()
		result, err := manageRouting(ctx, ManageRoutingParams{
			Operation: "get_time_intervals",
		})
		require.NoError(t, err)

		intervals, ok := result.([]muteTimingSummary)
		require.True(t, ok)
		require.NotEmpty(t, intervals, "expected provisioned 'weekends' mute timing")

		names := make([]string, len(intervals))
		for i, interval := range intervals {
			names[i] = interval.Name
		}
		require.Contains(t, names, "weekends")
	})
}

func TestManageRules_Versions(t *testing.T) {
	t.Run("get rule versions", func(t *testing.T) {
		ctx := newTestContext()
		result, err := manageRulesRead(ctx, ManageRulesReadParams{
			Operation: "versions",
			RuleUID:   rule1UID,
		})
		require.NoError(t, err)
		require.NotNil(t, result)
	})

	t.Run("get rule versions via read-write handler", func(t *testing.T) {
		ctx := newTestContext()
		result, err := manageRulesReadWrite(ctx, ManageRulesReadWriteParams{
			Operation: "versions",
			RuleUID:   rule1UID,
		})
		require.NoError(t, err)
		require.NotNil(t, result)
	})

	t.Run("get versions without rule_uid fails validation", func(t *testing.T) {
		ctx := newTestContext()
		_, err := manageRulesRead(ctx, ManageRulesReadParams{
			Operation: "versions",
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), "rule_uid is required")
	})
}

func TestManageRules_SearchFolder(t *testing.T) {
	t.Run("list with search_folder", func(t *testing.T) {
		ctx := newTestContext()
		result, err := manageRulesRead(ctx, ManageRulesReadParams{
			listFilterParams: listFilterParams{SearchFolder: "Test"},
			Operation:        "list",
		})
		require.NoError(t, err)
		// Should not error regardless of whether the folder exists
		rules, ok := result.([]alertRuleSummary)
		require.True(t, ok)
		_ = rules
	})

	t.Run("mutual exclusion validation", func(t *testing.T) {
		ctx := newTestContext()
		_, err := manageRulesRead(ctx, ManageRulesReadParams{
			listFilterParams: listFilterParams{SearchFolder: "Production"},
			Operation:        "list",
			FolderUID:        "folder-1",
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), "mutually exclusive")
	})
}
func ptrString(s string) *string { return &s }
