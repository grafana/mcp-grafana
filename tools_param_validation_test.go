//go:build unit
// +build unit

package mcpgrafana

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type embeddedTimeParams struct {
	StartRFC3339 string `json:"start_rfc_3339,omitempty"`
	EndRFC3339   string `json:"end_rfc_3339,omitempty"`
}

type unknownArgsParams struct {
	embeddedTimeParams
	DataSourceUID string `json:"data_source_uid" jsonschema:"required"`
	Hidden        string `json:"-"`
}

func unknownArgsHandler(ctx context.Context, params unknownArgsParams) (string, error) {
	return "ok", nil
}

func TestUnknownArguments(t *testing.T) {
	properties := map[string]any{"data_source_uid": nil, "start_rfc_3339": nil}

	t.Run("accepts exact and case-insensitive keys", func(t *testing.T) {
		args := map[string]any{"data_source_uid": "x", "START_RFC_3339": "t"}
		assert.Empty(t, unknownArguments(args, properties))
	})

	t.Run("reports typo'd and unknown keys sorted", func(t *testing.T) {
		args := map[string]any{"data_source_uid": "x", "start_rfc3339": "t", "foo": 1}
		assert.Equal(t, []string{"foo", "start_rfc3339"}, unknownArguments(args, properties))
	})

	t.Run("non-object arguments are left to the decode path", func(t *testing.T) {
		assert.Empty(t, unknownArguments("not an object", properties))
		assert.Empty(t, unknownArguments(nil, properties))
	})

	t.Run("error message names the keys and lists valid arguments", func(t *testing.T) {
		msg := unknownArgumentsError([]string{"start_rfc3339"}, properties)
		assert.Contains(t, msg, `unknown argument "start_rfc3339"`)
		assert.Contains(t, msg, "valid arguments: data_source_uid, start_rfc_3339")
	})
}

func TestConvertToolRejectsUnknownArguments(t *testing.T) {
	_, handler, err := ConvertTool("test_tool", "A test tool", testToolHandler)
	require.NoError(t, err)
	ctx := context.Background()

	t.Run("typo'd argument returns tool error listing valid arguments", func(t *testing.T) {
		request := mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Name: "test_tool",
				Arguments: map[string]any{
					"name":    "test",
					"value":   65,
					"optionl": true,
				},
			},
		}
		result, err := handler(ctx, request)
		require.NoError(t, err)
		require.NotNil(t, result)
		assert.True(t, result.IsError)
		text, ok := result.Content[0].(mcp.TextContent)
		require.True(t, ok)
		assert.Contains(t, text.Text, `unknown argument "optionl"`)
		assert.Contains(t, text.Text, "valid arguments: name, optional, value")
	})

	t.Run("valid arguments still succeed", func(t *testing.T) {
		request := mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Name:      "test_tool",
				Arguments: map[string]any{"name": "test", "value": 65},
			},
		}
		result, err := handler(ctx, request)
		require.NoError(t, err)
		assert.False(t, result.IsError)
	})

	t.Run("empty-params tool rejects any argument", func(t *testing.T) {
		_, emptyHandler, err := ConvertTool("empty", "description", emptyToolHandler)
		require.NoError(t, err)
		request := mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Name:      "empty",
				Arguments: map[string]any{"foo": "bar"},
			},
		}
		result, err := emptyHandler(ctx, request)
		require.NoError(t, err)
		assert.True(t, result.IsError)
		text, ok := result.Content[0].(mcp.TextContent)
		require.True(t, ok)
		assert.Contains(t, text.Text, "this tool takes no arguments")
	})

	t.Run("nil arguments succeed", func(t *testing.T) {
		_, emptyHandler, err := ConvertTool("empty", "description", emptyToolHandler)
		require.NoError(t, err)
		request := mcp.CallToolRequest{
			Params: mcp.CallToolParams{Name: "empty"},
		}
		result, err := emptyHandler(ctx, request)
		require.NoError(t, err)
		assert.False(t, result.IsError)
	})
}

func TestConvertToolKnownArgumentsMatchSchema(t *testing.T) {
	// The known-args check must accept exactly what the generated schema
	// declares — including fields promoted from embedded structs — so that no
	// call valid per the advertised schema starts failing.
	tool, handler, err := ConvertTool("embedded", "description", unknownArgsHandler)
	require.NoError(t, err)

	var schema map[string]any
	require.NoError(t, json.Unmarshal(tool.RawInputSchema, &schema))
	props, ok := schema["properties"].(map[string]any)
	require.True(t, ok)
	assert.Contains(t, props, "start_rfc_3339")
	assert.Contains(t, props, "end_rfc_3339")
	assert.Contains(t, props, "data_source_uid")
	assert.NotContains(t, props, "Hidden")

	ctx := context.Background()
	args := map[string]any{"data_source_uid": "x"}
	for name := range props {
		args[name] = "y"
	}
	result, err := handler(ctx, mcp.CallToolRequest{
		Params: mcp.CallToolParams{Name: "embedded", Arguments: args},
	})
	require.NoError(t, err)
	assert.False(t, result.IsError, "every schema-declared property must pass the unknown-args check")

	result, err = handler(ctx, mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name:      "embedded",
			Arguments: map[string]any{"data_source_uid": "x", "start_rfc3339": "now"},
		},
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.True(t, result.IsError)
}
