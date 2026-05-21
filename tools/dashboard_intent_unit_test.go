package tools

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProjectIntent_FullBlock(t *testing.T) {
	// A representative dashboard-level intent block — every field populated,
	// camelCase as it appears in the dashboard JSON, with snake_case
	// provenance keys (which pass through unchanged from the assistant API).
	raw := map[string]interface{}{
		"schemaVersion": float64(1),
		"purpose":       "Track checkout p99 latency.",
		"owner":         "@checkout-team",
		"expectedBehavior": map[string]interface{}{
			"normalRange":    "p99 < 250ms",
			"alertThreshold": "p99 > 500ms for 5m",
			"notes":          "Watch for deploy regressions.",
		},
		"failureModes": []interface{}{
			map[string]interface{}{"tag": "db-slow", "description": "DB latency spike."},
			map[string]interface{}{"tag": "pod-oom"},
		},
		"relatedSlos": []interface{}{
			map[string]interface{}{"name": "Checkout availability", "target": "99.9%", "url": "https://slo/x"},
		},
		"runbooks": []interface{}{
			map[string]interface{}{"title": "Checkout runbook", "url": "https://wiki/checkout"},
		},
		"provenance": map[string]interface{}{
			"purpose":                            "author-written",
			"expected_behavior.normal_range":     "author-written",
			"expected_behavior.alert_threshold":  "lifted-from-alert",
			"failure_modes":                      "assistant-unconfirmed",
		},
		"lastVerifiedAt": "2026-05-21T11:00:00Z",
	}

	intent, err := projectIntent(raw, "dash-1", nil)
	require.NoError(t, err)
	assert.Equal(t, "dash-1", intent.DashboardUID)
	assert.Nil(t, intent.PanelID)
	assert.Equal(t, 1, intent.SchemaVersion)
	assert.Equal(t, "Track checkout p99 latency.", intent.Purpose)
	assert.Equal(t, "@checkout-team", intent.Owner)
	require.NotNil(t, intent.ExpectedBehavior)
	assert.Equal(t, "p99 < 250ms", intent.ExpectedBehavior.NormalRange)
	assert.Equal(t, "p99 > 500ms for 5m", intent.ExpectedBehavior.AlertThreshold)
	require.Len(t, intent.FailureModes, 2)
	assert.Equal(t, "db-slow", intent.FailureModes[0].Tag)
	assert.Equal(t, "DB latency spike.", intent.FailureModes[0].Description)
	assert.Equal(t, "pod-oom", intent.FailureModes[1].Tag)
	require.Len(t, intent.RelatedSLOs, 1)
	assert.Equal(t, "99.9%", intent.RelatedSLOs[0].Target)
	require.Len(t, intent.Runbooks, 1)
	assert.Equal(t, "https://wiki/checkout", intent.Runbooks[0].URL)
	assert.Equal(t, "lifted-from-alert", intent.Provenance["expected_behavior.alert_threshold"])
	require.NotNil(t, intent.LastVerifiedAt)
}

func TestProjectIntent_DefaultsSchemaVersion(t *testing.T) {
	// Older blocks may have been written without a schemaVersion; the
	// projector should default to the current version so downstream
	// consumers don't need to special-case missing values.
	raw := map[string]interface{}{
		"purpose": "stale block, no schemaVersion",
	}
	intent, err := projectIntent(raw, "dash-1", nil)
	require.NoError(t, err)
	assert.Equal(t, dashboardIntentSchemaVersionCurrent, intent.SchemaVersion)
}

func TestProjectIntent_PanelLevelSetsPanelID(t *testing.T) {
	raw := map[string]interface{}{"purpose": "panel"}
	pid := 42
	intent, err := projectIntent(raw, "dash-1", &pid)
	require.NoError(t, err)
	require.NotNil(t, intent.PanelID)
	assert.Equal(t, 42, *intent.PanelID)
}

func TestProjectIntent_RejectsNonObject(t *testing.T) {
	_, err := projectIntent("not an object", "dash-1", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not an object")
}

func TestProjectIntent_SkipsMalformedEntries(t *testing.T) {
	// Defensive: a hand-edited dashboard JSON could carry a failure mode
	// without a tag, or a runbook without a title. The projector should
	// drop those entries silently rather than surface a half-populated
	// record that downstream consumers can't reason about.
	raw := map[string]interface{}{
		"failureModes": []interface{}{
			map[string]interface{}{"tag": ""},
			map[string]interface{}{"tag": "valid"},
			"not-an-object",
		},
		"runbooks": []interface{}{
			map[string]interface{}{"title": "", "url": "https://x"},
			map[string]interface{}{"title": "ok"},
		},
		"provenance": map[string]interface{}{
			"purpose": "  ", // whitespace-only — drop
			"owner":   "author-written",
		},
	}

	intent, err := projectIntent(raw, "dash-1", nil)
	require.NoError(t, err)
	require.Len(t, intent.FailureModes, 1)
	assert.Equal(t, "valid", intent.FailureModes[0].Tag)
	require.Len(t, intent.Runbooks, 1)
	assert.Equal(t, "ok", intent.Runbooks[0].Title)
	assert.Equal(t, map[string]string{"owner": "author-written"}, intent.Provenance)
}

func TestListDashboardIntent_WalksPanels(t *testing.T) {
	// Inline walk of the projector against a synthetic dashboard map —
	// gives us coverage of the panel walking logic without needing a
	// live Grafana. The full HTTP wiring is exercised by the dashboard
	// integration tests.
	db := map[string]interface{}{
		"intent": map[string]interface{}{
			"purpose": "dashboard-level",
		},
		"panels": []interface{}{
			map[string]interface{}{
				"id":     float64(1),
				"intent": map[string]interface{}{"purpose": "p1"},
			},
			map[string]interface{}{
				"id": float64(2),
				// no intent block
			},
			map[string]interface{}{
				"id":   float64(3),
				"type": "row",
				"panels": []interface{}{
					map[string]interface{}{
						"id":     float64(4),
						"intent": map[string]interface{}{"purpose": "nested"},
					},
				},
			},
		},
	}

	bundle := &DashboardIntentBundle{DashboardUID: "dash-1", Panels: []DashboardIntent{}}
	if raw, ok := db["intent"]; ok && raw != nil {
		di, err := projectIntent(raw, "dash-1", nil)
		require.NoError(t, err)
		bundle.Dashboard = di
	}
	for _, panel := range collectAllPanels(db) {
		raw, ok := panel["intent"]
		if !ok || raw == nil {
			continue
		}
		pid := safeInt(panel, "id")
		if pid == 0 {
			continue
		}
		panelID := pid
		di, err := projectIntent(raw, "dash-1", &panelID)
		require.NoError(t, err)
		bundle.Panels = append(bundle.Panels, *di)
	}

	require.NotNil(t, bundle.Dashboard)
	assert.Equal(t, "dashboard-level", bundle.Dashboard.Purpose)
	// Both the top-level panel (id=1) and the row-nested panel (id=4)
	// should appear; the panel with no intent (id=2) is skipped.
	require.Len(t, bundle.Panels, 2)
	assert.Equal(t, 1, *bundle.Panels[0].PanelID)
	assert.Equal(t, "p1", bundle.Panels[0].Purpose)
	assert.Equal(t, 4, *bundle.Panels[1].PanelID)
	assert.Equal(t, "nested", bundle.Panels[1].Purpose)
}
