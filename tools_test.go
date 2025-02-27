package mcpgrafana

import (
	"context"
	"errors"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type testToolParams struct {
	Name     string `json:"name" jsonschema:"required,description=The name parameter"`
	Value    int    `json:"value" jsonschema:"required,description=The value parameter"`
	Optional bool   `json:"optional,omitempty" jsonschema:"description=An optional parameter"`
}

func testToolHandler(ctx context.Context, params testToolParams) (*mcp.CallToolResult, error) {
	if params.Name == "error" {
		return nil, errors.New("test error")
	}
	return mcp.NewToolResultText(params.Name + ": " + string(rune(params.Value))), nil
}

type emptyToolParams struct{}

func emptyToolHandler(ctx context.Context, params emptyToolParams) (*mcp.CallToolResult, error) {
	return mcp.NewToolResultText("empty"), nil
}

func TestConvertTool(t *testing.T) {
	t.Run("valid handler conversion", func(t *testing.T) {
		tool, handler, err := ConvertTool("test_tool", "A test tool", testToolHandler)

		require.NoError(t, err)
		require.NotNil(t, tool)
		require.NotNil(t, handler)

		// Check tool properties
		assert.Equal(t, "test_tool", tool.Name)
		assert.Equal(t, "A test tool", tool.Description)

		// Check schema properties
		assert.Equal(t, "object", tool.InputSchema.Type)
		assert.Contains(t, tool.InputSchema.Properties, "name")
		assert.Contains(t, tool.InputSchema.Properties, "value")
		assert.Contains(t, tool.InputSchema.Properties, "optional")

		// Test handler execution
		ctx := context.Background()
		request := mcp.CallToolRequest{
			Params: struct {
				Name      string         "json:\"name\""
				Arguments map[string]any "json:\"arguments,omitempty\""
				Meta      *struct {
					ProgressToken mcp.ProgressToken "json:\"progressToken,omitempty\""
				} "json:\"_meta,omitempty\""
			}{
				Name: "test_tool",
				Arguments: map[string]any{
					"name":  "test",
					"value": 65, // ASCII 'A'
				},
			},
		}

		result, err := handler(ctx, request)
		require.NoError(t, err)
		require.Len(t, result.Content, 1)
		resultString, ok := result.Content[0].(mcp.TextContent)
		require.True(t, ok)
		assert.Equal(t, "test: A", resultString.Text)

		// Test error handling
		errorRequest := mcp.CallToolRequest{
			Params: struct {
				Name      string         "json:\"name\""
				Arguments map[string]any "json:\"arguments,omitempty\""
				Meta      *struct {
					ProgressToken mcp.ProgressToken "json:\"progressToken,omitempty\""
				} "json:\"_meta,omitempty\""
			}{
				Name: "test_tool",
				Arguments: map[string]any{
					"name":  "error",
					"value": 66,
				},
			},
		}

		_, err = handler(ctx, errorRequest)
		assert.Error(t, err)
		assert.Equal(t, "test error", err.Error())
	})

	t.Run("empty handler params", func(t *testing.T) {
		tool, handler, err := ConvertTool("empty", "description", emptyToolHandler)

		require.NoError(t, err)
		require.NotNil(t, tool)
		require.NotNil(t, handler)

		// Check tool properties
		assert.Equal(t, "empty", tool.Name)
		assert.Equal(t, "description", tool.Description)

		// Check schema properties
		assert.Equal(t, "object", tool.InputSchema.Type)
		assert.Len(t, tool.InputSchema.Properties, 0)

		// Test handler execution
		ctx := context.Background()
		request := mcp.CallToolRequest{
			Params: struct {
				Name      string         "json:\"name\""
				Arguments map[string]any "json:\"arguments,omitempty\""
				Meta      *struct {
					ProgressToken mcp.ProgressToken "json:\"progressToken,omitempty\""
				} "json:\"_meta,omitempty\""
			}{
				Name: "empty",
			},
		}
		result, err := handler(ctx, request)
		require.NoError(t, err)
		require.Len(t, result.Content, 1)
		resultString, ok := result.Content[0].(mcp.TextContent)
		require.True(t, ok)
		assert.Equal(t, "empty", resultString.Text)
	})

	t.Run("invalid handler types", func(t *testing.T) {
		// Test non-function handler
		_, _, err := ConvertTool("invalid", "description", "not a function")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "must be a function")

		// Test wrong number of arguments
		wrongArgsFunc := func(ctx context.Context) (*mcp.CallToolResult, error) {
			return nil, nil
		}
		_, _, err = ConvertTool("invalid", "description", wrongArgsFunc)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "must have 2 arguments")

		// Test wrong number of return values
		wrongReturnFunc := func(ctx context.Context, params testToolParams) *mcp.CallToolResult {
			return nil
		}
		_, _, err = ConvertTool("invalid", "description", wrongReturnFunc)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "must return 2 values")

		// Test wrong first argument type
		wrongFirstArgFunc := func(s string, params testToolParams) (*mcp.CallToolResult, error) {
			return nil, nil
		}
		_, _, err = ConvertTool("invalid", "description", wrongFirstArgFunc)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "first argument must be context.Context")

		// Test wrong first return value type
		wrongFirstReturnFunc := func(ctx context.Context, params testToolParams) (string, error) {
			return "", nil
		}
		_, _, err = ConvertTool("invalid", "description", wrongFirstReturnFunc)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "first return value must be mcp.CallToolResult")

		// Test wrong second argument type (not a struct)
		wrongSecondArgFunc := func(ctx context.Context, s string) (*mcp.CallToolResult, error) {
			return nil, nil
		}
		_, _, err = ConvertTool("invalid", "description", wrongSecondArgFunc)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "second argument must be a struct")
	})

	t.Run("handler execution with invalid arguments", func(t *testing.T) {
		_, handler, err := ConvertTool("test_tool", "A test tool", testToolHandler)
		require.NoError(t, err)

		// Test with invalid JSON
		invalidRequest := mcp.CallToolRequest{
			Params: struct {
				Name      string         "json:\"name\""
				Arguments map[string]any "json:\"arguments,omitempty\""
				Meta      *struct {
					ProgressToken mcp.ProgressToken "json:\"progressToken,omitempty\""
				} "json:\"_meta,omitempty\""
			}{
				Arguments: map[string]any{
					"name": make(chan int), // Channels can't be marshaled to JSON
				},
			},
		}

		_, err = handler(context.Background(), invalidRequest)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "marshal args")

		// Test with type mismatch
		mismatchRequest := mcp.CallToolRequest{
			Params: struct {
				Name      string         "json:\"name\""
				Arguments map[string]any "json:\"arguments,omitempty\""
				Meta      *struct {
					ProgressToken mcp.ProgressToken "json:\"progressToken,omitempty\""
				} "json:\"_meta,omitempty\""
			}{
				Arguments: map[string]any{
					"name":  123, // Should be a string
					"value": "not an int",
				},
			},
		}

		result, err := handler(context.Background(), mismatchRequest)
		assert.Nil(t, err) // Error is returned in the result, not as an error
		assert.Contains(t, result.Content[0].(mcp.TextContent).Text, "unmarshal args")
	})
}

func TestCreateJsonSchemaFromHandler(t *testing.T) {
	schema := createJsonSchemaFromHandler(testToolHandler)

	assert.Equal(t, "object", schema.Type)
	assert.Len(t, schema.Required, 2) // name and value are required, optional is not

	// Check properties
	nameProperty, ok := schema.Properties.Get("name")
	assert.True(t, ok)
	assert.Equal(t, "string", nameProperty.Type)
	assert.Equal(t, "The name parameter", nameProperty.Description)

	valueProperty, ok := schema.Properties.Get("value")
	assert.True(t, ok)
	assert.Equal(t, "integer", valueProperty.Type)
	assert.Equal(t, "The value parameter", valueProperty.Description)

	optionalProperty, ok := schema.Properties.Get("optional")
	assert.True(t, ok)
	assert.Equal(t, "boolean", optionalProperty.Type)
	assert.Equal(t, "An optional parameter", optionalProperty.Description)
}
