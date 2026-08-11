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
func TestToolMetricDimsMatchToolEnums(t *testing.T) {
	// Params struct per allowlisted multiplexer tool. Read-write variants are
	// used where they exist, since their enum is the superset.
	cases := []struct {
		toolName string
		params   any
	}{
		{"alerting_manage_rules", ManageRulesReadWriteParams{}},
		{"alerting_manage_routing", ManageRoutingParams{}},
		{"agento11y_manage_conversations", ManageAgento11yConversationsParams{}},
		{"agento11y_manage_generations", ManageAgento11yGenerationsParams{}},
	}

	for _, tc := range cases {
		t.Run(tc.toolName, func(t *testing.T) {
			ops := schemaEnumValues(t, tc.params, "operation")
			require.NotEmpty(t, ops, "no enum values found on the operation field")

			for _, op := range ops {
				d := observability.ToolMetricDimensions(tc.toolName, map[string]any{"operation": op}, nil)
				assert.Equal(t, op, d.Operation,
					"operation %q is accepted by %s but missing from the metric value allowlist", op, tc.toolName)
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
