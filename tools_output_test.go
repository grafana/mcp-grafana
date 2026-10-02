//go:build unit

package mcpgrafana

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/invopop/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type outputInner struct {
	Label string `json:"label"`
}

type outputBase struct {
	ID string `json:"id"`
}

type outputNode struct {
	Name     string        `json:"name"`
	Children []*outputNode `json:"children"`
}

type opaqueJSON struct{ v any }

func (o opaqueJSON) MarshalJSON() ([]byte, error) { return json.Marshal(o.v) }

type outputStruct struct {
	*outputBase
	Name     string            `json:"name"`
	Inner    *outputInner      `json:"inner"`
	Optional *outputInner      `json:"optional,omitempty"`
	Tags     []string          `json:"tags"`
	Labels   map[string]string `json:"labels"`
	When     time.Time         `json:"when"`
	Opaque   opaqueJSON        `json:"opaque"`
	Tree     *outputNode       `json:"tree,omitempty"`
}

func callOutputTool[R any](t *testing.T, ret R) (*mcp.Tool, *mcp.CallToolResult) {
	t.Helper()
	type params struct{}
	tool, handler, err := ConvertTool("output_tool", "test", func(context.Context, params) (R, error) { return ret, nil })
	require.NoError(t, err)
	result, err := handler(context.Background(), newCallToolRequest("output_tool", nil))
	require.NoError(t, err)
	return tool, result
}

func schemaOf(t *testing.T, tool *mcp.Tool) map[string]any {
	t.Helper()
	require.NotNil(t, tool.OutputSchema)
	var m map[string]any
	require.NoError(t, json.Unmarshal(tool.OutputSchema.(json.RawMessage), &m))
	return m
}

func TestOutputSchema_StructIsInlinedAndMatchesText(t *testing.T) {
	tool, result := callOutputTool(t, &outputStruct{Name: "n", Inner: &outputInner{Label: "l"}, Opaque: opaqueJSON{[]int{1}}})
	schema := schemaOf(t, tool)
	assert.Equal(t, "object", schema["type"])
	assert.Contains(t, schema["properties"], "name")

	text := result.Content[0].(*mcp.TextContent).Text
	assert.JSONEq(t, text, string(result.StructuredContent.(json.RawMessage)))
	assert.Contains(t, text, `"name":"n"`)
}

func TestOutputSchema_Nullability(t *testing.T) {
	tool, _ := callOutputTool(t, outputStruct{})
	props := schemaOf(t, tool)["properties"].(map[string]any)

	// Plain values never marshal as null.
	assert.Equal(t, "string", props["name"].(map[string]any)["type"])
	// Nil slices and maps without omitempty do.
	assert.Equal(t, []any{"array", "null"}, props["tags"].(map[string]any)["type"])
	assert.Equal(t, []any{"object", "null"}, props["labels"].(map[string]any)["type"])
	// A nil struct pointer is a $ref, so null is offered alongside it.
	assert.Contains(t, props["inner"], "anyOf")
	// With omitempty a nil pointer is omitted, not null.
	assert.NotContains(t, props["optional"], "anyOf")
	// Marshallers: TextMarshaler is a string, anything else is unconstrained.
	assert.Equal(t, "string", props["when"].(map[string]any)["type"])
	assert.Contains(t, props["opaque"].(map[string]any)["type"], "null")
}

func TestOutputSchema_EmbeddedPointerFieldsAreOptional(t *testing.T) {
	tool, result := callOutputTool(t, outputStruct{})
	schema := schemaOf(t, tool)
	assert.NotContains(t, schema["required"], "id", "a nil embedded pointer omits its fields")
	assert.Contains(t, schema["required"], "name")
	assert.NotContains(t, string(result.StructuredContent.(json.RawMessage)), `"id"`)
}

func TestOutputSchema_RecursiveType(t *testing.T) {
	tool, _ := callOutputTool(t, outputStruct{Tree: &outputNode{Children: []*outputNode{{Name: "leaf"}}}})
	defs := schemaOf(t, tool)["$defs"].(map[string]any)
	assert.Contains(t, defs, "outputNode")
}

func TestOutputSchema_NonObjectsAreWrapped(t *testing.T) {
	tool, result := callOutputTool(t, []string{"a", "b"})
	schema := schemaOf(t, tool)
	assert.Equal(t, []any{"result"}, schema["required"])
	assert.JSONEq(t, `{"result":["a","b"]}`, string(result.StructuredContent.(json.RawMessage)))

	_, result = callOutputTool[[]string](t, nil)
	assert.JSONEq(t, `{"result":[]}`, string(result.StructuredContent.(json.RawMessage)))

	_, result = callOutputTool[map[string]int](t, nil)
	assert.JSONEq(t, `{"result":{}}`, string(result.StructuredContent.(json.RawMessage)))
}

func TestOutputSchema_TextOnlyTypes(t *testing.T) {
	for name, tool := range map[string]*mcp.Tool{
		"string": first(callOutputTool(t, "text")),
		"any":    first(callOutputTool[any](t, map[string]any{"a": 1})),
		"result": first(callOutputTool(t, &mcp.CallToolResult{})),
	} {
		assert.Nil(t, tool.OutputSchema, name)
	}
}

func first[A, B any](a A, _ B) A { return a }

// lyingSchema claims to be a number but marshals as an object.
type lyingSchema struct {
	X int `json:"x"`
}

func (lyingSchema) JSONSchema() *jsonschema.Schema { return &jsonschema.Schema{Type: "number"} }

func TestOutputSchema_MismatchFailsAtConstruction(t *testing.T) {
	_, err := newOutputSpec("lying", reflect.TypeFor[struct {
		L lyingSchema `json:"l"`
	}]())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "output schema does not match")
}
