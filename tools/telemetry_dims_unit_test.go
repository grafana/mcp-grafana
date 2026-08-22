//go:build unit

package tools

import (
	"reflect"
	"strings"
	"testing"

	"github.com/grafana/mcp-grafana/observability"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestToolMetricDimsMatchToolEnums guards observability's operation value
// allowlist (toolMetricDims) against drift: every operation a tool advertises in
// its JSON schema must survive ToolMetricDimensions unchanged. Adding an
// operation to one of these tools without extending the allowlist fails here,
// rather than silently degrading the mcp_tool_operation label to "other".
//
// The merged agento11y tools are a deliberate exception: toolMetricDims only
// ever allowlisted a subset of their operations (the agent catalog and the
// evaluator/eval-rule/saved-conversation operations were never opted in), and
// that stays true after consolidation, just renamed and split by read/write —
// see observability.go's toolMetricDims comments. For those three tools this
// test also checks the deliberately-excluded operations still collapse to
// "other", so both directions of drift are caught.
func TestToolMetricDimsMatchToolEnums(t *testing.T) {
	cases := []struct {
		toolName string
		params   any
		// allowlisted is nil for a tool whose whole schema enum is allowlisted
		// (every operation must round-trip unchanged). For a tool with a
		// deliberate partial allowlist, it names exactly the operations
		// expected to round-trip; every other schema operation must map to
		// "other".
		allowlisted []string
	}{
		{"alerting_manage_rules", ManageRulesReadWriteParams{}, nil},
		{"alerting_manage_routing", ManageRoutingParams{}, nil},
		{"agento11y_read", Agento11yReadParams{}, []string{
			"list_conversations", "search_conversations", "get_conversation",
			"get_generation", "list_generation_scores",
		}},
		{"agento11y_evals_read", Agento11yEvalsReadParams{}, []string{
			"list_experiments", "get_experiment", "get_experiment_report", "list_experiment_trials", "list_experiment_scores",
			"get_trial", "list_trial_scores", "list_trial_artifacts", "list_experiment_facets",
			"list_suites", "get_suite", "list_test_cases", "get_test_case",
		}},
		{"agento11y_evals_write", Agento11yEvalsWriteParams{}, []string{
			"update_experiment", "cancel_experiment",
			"create_suite", "update_suite", "create_draft_version", "publish_version",
			"upsert_test_case", "delete_test_case",
		}},
	}

	for _, tc := range cases {
		t.Run(tc.toolName, func(t *testing.T) {
			ops := schemaEnumValues(t, tc.params, "operation")
			require.NotEmpty(t, ops, "no enum values found on the operation field")

			allowlisted := tc.allowlisted
			fullyAllowlisted := allowlisted == nil
			allowlistedSet := make(map[string]bool, len(allowlisted))
			for _, op := range allowlisted {
				allowlistedSet[op] = true
			}

			for _, op := range ops {
				d := observability.ToolMetricDimensions(tc.toolName, map[string]any{"operation": op}, nil)
				if fullyAllowlisted || allowlistedSet[op] {
					assert.Equal(t, op, d.Operation,
						"operation %q is accepted by %s but missing from the metric value allowlist", op, tc.toolName)
				} else {
					assert.Equal(t, "other", d.Operation,
						"operation %q is not expected to be allowlisted for %s (see observability.go's toolMetricDims comment); update this test if that was deliberately changed", op, tc.toolName)
				}
			}

			// And the bound still holds for anything the tool would reject.
			d := observability.ToolMetricDimensions(tc.toolName, map[string]any{"operation": "definitely-not-an-operation"}, nil)
			assert.Equal(t, "other", d.Operation)
		})
	}
}

// schemaEnumValues returns the enum= values declared in the jsonschema tag of the
// struct field whose json name is fieldName.
func schemaEnumValues(t *testing.T, params any, fieldName string) []string {
	t.Helper()
	typ := reflect.TypeOf(params)
	for i := range typ.NumField() {
		f := typ.Field(i)
		if name, _, _ := strings.Cut(f.Tag.Get("json"), ","); name != fieldName {
			continue
		}
		var values []string
		for _, part := range strings.Split(f.Tag.Get("jsonschema"), ",") {
			if v, ok := strings.CutPrefix(part, "enum="); ok {
				values = append(values, v)
			}
		}
		return values
	}
	t.Fatalf("no field with json name %q on %T", fieldName, params)
	return nil
}
