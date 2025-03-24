// Requires a Grafana instance running on localhost:3000,
// with alert rules configured.
// Run with `go test -tags integration`.
//go:build integration

package tools

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAlertingTools(t *testing.T) {
	const (
		rule1UID        = "test_alert_rule_1"
		rule1Title      = "Test Alert Rule 1"
		rule2UID        = "test_alert_rule_2"
		rule2Title      = "Test Alert Rule 2"
		rulePausedUID   = "test_alert_rule_paused"
		rulePausedTitle = "Test Alert Rule (Paused)"
	)

	t.Run("list alert rules", func(t *testing.T) {
		ctx := newTestContext()
		result, err := listAlertRules(ctx, ListAlertRulesParams{})
		require.NoError(t, err)

		// We should have 3 test alert rules we provisioned
		require.Equal(t, len(result), 3, "Expected 3 alert rules from provisioning")

		expectedRules := map[string]struct {
			title    string
			isPaused bool
		}{
			rule1UID:      {rule1Title, false},
			rule2UID:      {rule2Title, false},
			rulePausedUID: {rulePausedTitle, true},
		}

		for _, rule := range result {
			expected, exists := expectedRules[rule.UID]
			require.True(t, exists, "Unexpected rule with UID %s", rule.UID)
			assert.Equal(t, expected.title, *rule.Title, "Unexpected title for %s", rule.UID)
			assert.Equal(t, expected.isPaused, rule.IsPaused, "Unexpected isPaused value for %s", rule.UID)
		}
	})

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
}
