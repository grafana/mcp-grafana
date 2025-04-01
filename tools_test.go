//go:build unit
// +build unit

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

// New handlers for different return types
func stringToolHandler(ctx context.Context, params testToolParams) (string, error) {
	if params.Name == "error" {
		return "", errors.New("test error")
	}
	if params.Name == "empty" {
		return "", nil
	}
	return params.Name + ": " + string(rune(params.Value)), nil
}

func stringPtrToolHandler(ctx context.Context, params testToolParams) (*string, error) {
	if params.Name == "error" {
		return nil, errors.New("test error")
	}
	if params.Name == "nil" {
		return nil, nil
	}
	if params.Name == "empty" {
		empty := ""
		return &empty, nil
	}
	result := params.Name + ": " + string(rune(params.Value))
	return &result, nil
}

type TestResult struct {
	Name  string `json:"name"`
	Value int    `json:"value"`
}

func structToolHandler(ctx context.Context, params testToolParams) (TestResult, error) {
	if params.Name == "error" {
		return TestResult{}, errors.New("test error")
	}
	return TestResult{
		Name:  params.Name,
		Value: params.Value,
	}, nil
}

func structPtrToolHandler(ctx context.Context, params testToolParams) (*TestResult, error) {
	if params.Name == "error" {
		return nil, errors.New("test error")
	}
	if params.Name == "nil" {
		return nil, nil
	}
	return &TestResult{
		Name:  params.Name,
		Value: params.Value,
	}, nil
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
				Name      string         `json:"name"`
				Arguments map[string]any `json:"arguments,omitempty"`
				Meta      *struct {
					ProgressToken mcp.ProgressToken `json:"progressToken,omitempty"`
				} `json:"_meta,omitempty"`
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
				Name      string         `json:"name"`
				Arguments map[string]any `json:"arguments,omitempty"`
				Meta      *struct {
					ProgressToken mcp.ProgressToken `json:"progressToken,omitempty"`
				} `json:"_meta,omitempty"`
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
				Name      string         `json:"name"`
				Arguments map[string]any `json:"arguments,omitempty"`
				Meta      *struct {
					ProgressToken mcp.ProgressToken `json:"progressToken,omitempty"`
				} `json:"_meta,omitempty"`
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

	t.Run("string return type", func(t *testing.T) {
		_, handler, err := ConvertTool("string_tool", "A string tool", stringToolHandler)
		require.NoError(t, err)

		// Test normal string return
		ctx := context.Background()
		request := mcp.CallToolRequest{
			Params: struct {
				Name      string         `json:"name"`
				Arguments map[string]any `json:"arguments,omitempty"`
				Meta      *struct {
					ProgressToken mcp.ProgressToken `json:"progressToken,omitempty"`
				} `json:"_meta,omitempty"`
			}{
				Name: "string_tool",
				Arguments: map[string]any{
					"name":  "test",
					"value": 65, // ASCII 'A'
				},
			},
		}

		result, err := handler(ctx, request)
		require.NoError(t, err)
		require.NotNil(t, result)
		require.Len(t, result.Content, 1)
		resultString, ok := result.Content[0].(mcp.TextContent)
		require.True(t, ok)
		assert.Equal(t, "test: A", resultString.Text)

		// Test empty string return
		emptyRequest := mcp.CallToolRequest{
			Params: struct {
				Name      string         `json:"name"`
				Arguments map[string]any `json:"arguments,omitempty"`
				Meta      *struct {
					ProgressToken mcp.ProgressToken `json:"progressToken,omitempty"`
				} `json:"_meta,omitempty"`
			}{
				Name: "string_tool",
				Arguments: map[string]any{
					"name":  "empty",
					"value": 65,
				},
			},
		}

		result, err = handler(ctx, emptyRequest)
		require.NoError(t, err)
		assert.Nil(t, result)

		// Test error return
		errorRequest := mcp.CallToolRequest{
			Params: struct {
				Name      string         `json:"name"`
				Arguments map[string]any `json:"arguments,omitempty"`
				Meta      *struct {
					ProgressToken mcp.ProgressToken `json:"progressToken,omitempty"`
				} `json:"_meta,omitempty"`
			}{
				Name: "string_tool",
				Arguments: map[string]any{
					"name":  "error",
					"value": 65,
				},
			},
		}

		_, err = handler(ctx, errorRequest)
		assert.Error(t, err)
		assert.Equal(t, "test error", err.Error())
	})

	t.Run("string pointer return type", func(t *testing.T) {
		_, handler, err := ConvertTool("string_ptr_tool", "A string pointer tool", stringPtrToolHandler)
		require.NoError(t, err)

		// Test normal string pointer return
		ctx := context.Background()
		request := mcp.CallToolRequest{
			Params: struct {
				Name      string         `json:"name"`
				Arguments map[string]any `json:"arguments,omitempty"`
				Meta      *struct {
					ProgressToken mcp.ProgressToken `json:"progressToken,omitempty"`
				} `json:"_meta,omitempty"`
			}{
				Name: "string_ptr_tool",
				Arguments: map[string]any{
					"name":  "test",
					"value": 65, // ASCII 'A'
				},
			},
		}

		result, err := handler(ctx, request)
		require.NoError(t, err)
		require.NotNil(t, result)
		require.Len(t, result.Content, 1)
		resultString, ok := result.Content[0].(mcp.TextContent)
		require.True(t, ok)
		assert.Equal(t, "test: A", resultString.Text)

		// Test nil string pointer return
		nilRequest := mcp.CallToolRequest{
			Params: struct {
				Name      string         `json:"name"`
				Arguments map[string]any `json:"arguments,omitempty"`
				Meta      *struct {
					ProgressToken mcp.ProgressToken `json:"progressToken,omitempty"`
				} `json:"_meta,omitempty"`
			}{
				Name: "string_ptr_tool",
				Arguments: map[string]any{
					"name":  "nil",
					"value": 65,
				},
			},
		}

		result, err = handler(ctx, nilRequest)
		require.NoError(t, err)
		assert.Nil(t, result)

		// Test empty string pointer return
		emptyRequest := mcp.CallToolRequest{
			Params: struct {
				Name      string         `json:"name"`
				Arguments map[string]any `json:"arguments,omitempty"`
				Meta      *struct {
					ProgressToken mcp.ProgressToken `json:"progressToken,omitempty"`
				} `json:"_meta,omitempty"`
			}{
				Name: "string_ptr_tool",
				Arguments: map[string]any{
					"name":  "empty",
					"value": 65,
				},
			},
		}

		result, err = handler(ctx, emptyRequest)
		require.NoError(t, err)
		assert.Nil(t, result)

		// Test error return
		errorRequest := mcp.CallToolRequest{
			Params: struct {
				Name      string         `json:"name"`
				Arguments map[string]any `json:"arguments,omitempty"`
				Meta      *struct {
					ProgressToken mcp.ProgressToken `json:"progressToken,omitempty"`
				} `json:"_meta,omitempty"`
			}{
				Name: "string_ptr_tool",
				Arguments: map[string]any{
					"name":  "error",
					"value": 65,
				},
			},
		}

		_, err = handler(ctx, errorRequest)
		assert.Error(t, err)
		assert.Equal(t, "test error", err.Error())
	})

	t.Run("struct return type", func(t *testing.T) {
		_, handler, err := ConvertTool("struct_tool", "A struct tool", structToolHandler)
		require.NoError(t, err)

		// Test normal struct return
		ctx := context.Background()
		request := mcp.CallToolRequest{
			Params: struct {
				Name      string         `json:"name"`
				Arguments map[string]any `json:"arguments,omitempty"`
				Meta      *struct {
					ProgressToken mcp.ProgressToken `json:"progressToken,omitempty"`
				} `json:"_meta,omitempty"`
			}{
				Name: "struct_tool",
				Arguments: map[string]any{
					"name":  "test",
					"value": 65, // ASCII 'A'
				},
			},
		}

		result, err := handler(ctx, request)
		require.NoError(t, err)
		require.NotNil(t, result)
		require.Len(t, result.Content, 1)
		resultString, ok := result.Content[0].(mcp.TextContent)
		require.True(t, ok)
		assert.Contains(t, resultString.Text, `"name":"test"`)
		assert.Contains(t, resultString.Text, `"value":65`)

		// Test error return
		errorRequest := mcp.CallToolRequest{
			Params: struct {
				Name      string         `json:"name"`
				Arguments map[string]any `json:"arguments,omitempty"`
				Meta      *struct {
					ProgressToken mcp.ProgressToken `json:"progressToken,omitempty"`
				} `json:"_meta,omitempty"`
			}{
				Name: "struct_tool",
				Arguments: map[string]any{
					"name":  "error",
					"value": 65,
				},
			},
		}

		_, err = handler(ctx, errorRequest)
		assert.Error(t, err)
		assert.Equal(t, "test error", err.Error())
	})

	t.Run("struct pointer return type", func(t *testing.T) {
		_, handler, err := ConvertTool("struct_ptr_tool", "A struct pointer tool", structPtrToolHandler)
		require.NoError(t, err)

		// Test normal struct pointer return
		ctx := context.Background()
		request := mcp.CallToolRequest{
			Params: struct {
				Name      string         `json:"name"`
				Arguments map[string]any `json:"arguments,omitempty"`
				Meta      *struct {
					ProgressToken mcp.ProgressToken `json:"progressToken,omitempty"`
				} `json:"_meta,omitempty"`
			}{
				Name: "struct_ptr_tool",
				Arguments: map[string]any{
					"name":  "test",
					"value": 65, // ASCII 'A'
				},
			},
		}

		result, err := handler(ctx, request)
		require.NoError(t, err)
		require.NotNil(t, result)
		require.Len(t, result.Content, 1)
		resultString, ok := result.Content[0].(mcp.TextContent)
		require.True(t, ok)
		assert.Contains(t, resultString.Text, `"name":"test"`)
		assert.Contains(t, resultString.Text, `"value":65`)

		// Test nil struct pointer return
		nilRequest := mcp.CallToolRequest{
			Params: struct {
				Name      string         `json:"name"`
				Arguments map[string]any `json:"arguments,omitempty"`
				Meta      *struct {
					ProgressToken mcp.ProgressToken `json:"progressToken,omitempty"`
				} `json:"_meta,omitempty"`
			}{
				Name: "struct_ptr_tool",
				Arguments: map[string]any{
					"name":  "nil",
					"value": 65,
				},
			},
		}

		result, err = handler(ctx, nilRequest)
		require.NoError(t, err)
		assert.Nil(t, result)

		// Test error return
		errorRequest := mcp.CallToolRequest{
			Params: struct {
				Name      string         `json:"name"`
				Arguments map[string]any `json:"arguments,omitempty"`
				Meta      *struct {
					ProgressToken mcp.ProgressToken `json:"progressToken,omitempty"`
				} `json:"_meta,omitempty"`
			}{
				Name: "struct_ptr_tool",
				Arguments: map[string]any{
					"name":  "error",
					"value": 65,
				},
			},
		}

		_, err = handler(ctx, errorRequest)
		assert.Error(t, err)
		assert.Equal(t, "test error", err.Error())
	})

	t.Run("invalid handler types", func(t *testing.T) {
		// Test wrong second argument type (not a struct)
		wrongSecondArgFunc := func(ctx context.Context, s string) (*mcp.CallToolResult, error) {
			return nil, nil
		}
		_, _, err := ConvertTool("invalid", "description", wrongSecondArgFunc)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "second argument must be a struct")
	})

	t.Run("handler execution with invalid arguments", func(t *testing.T) {
		_, handler, err := ConvertTool("test_tool", "A test tool", testToolHandler)
		require.NoError(t, err)

		// Test with invalid JSON
		invalidRequest := mcp.CallToolRequest{
			Params: struct {
				Name      string         `json:"name"`
				Arguments map[string]any `json:"arguments,omitempty"`
				Meta      *struct {
					ProgressToken mcp.ProgressToken `json:"progressToken,omitempty"`
				} `json:"_meta,omitempty"`
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
				Name      string         `json:"name"`
				Arguments map[string]any `json:"arguments,omitempty"`
				Meta      *struct {
					ProgressToken mcp.ProgressToken `json:"progressToken,omitempty"`
				} `json:"_meta,omitempty"`
			}{
				Arguments: map[string]any{
					"name":  123, // Should be a string
					"value": "not an int",
				},
			},
		}

		_, err = handler(context.Background(), mismatchRequest)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "unmarshal args")
	})
}

func TestCreateJSONSchemaFromHandler(t *testing.T) {
	schema := createJSONSchemaFromHandler(testToolHandler)

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
