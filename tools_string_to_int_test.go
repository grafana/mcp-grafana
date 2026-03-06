package mcpgrafana

import (
	"context"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Test struct with int parameters
type testIntParams struct {
	Name        string `json:"name" jsonschema:"required,description=Name parameter"`
	Count       int    `json:"count" jsonschema:"description=Count parameter as int"`
	Limit       int    `json:"limit,omitempty" jsonschema:"default=10,description=Limit parameter"`
	OptionalInt *int   `json:"optionalInt,omitempty" jsonschema:"description=Optional int pointer"`
}

// Test handler
func testIntHandler(ctx context.Context, params testIntParams) (string, error) {
	return "success", nil
}

func TestUnmarshalWithIntConversion(t *testing.T) {
	t.Run("converts string to int for int fields", func(t *testing.T) {
		data := []byte(`{"name":"test","count":"42","limit":"100"}`)
		var params testIntParams

		err := unmarshalWithIntConversion(data, &params)
		require.NoError(t, err)

		assert.Equal(t, "test", params.Name)
		assert.Equal(t, 42, params.Count)
		assert.Equal(t, 100, params.Limit)
	})

	t.Run("accepts native int values", func(t *testing.T) {
		data := []byte(`{"name":"test","count":42,"limit":100}`)
		var params testIntParams

		err := unmarshalWithIntConversion(data, &params)
		require.NoError(t, err)

		assert.Equal(t, "test", params.Name)
		assert.Equal(t, 42, params.Count)
		assert.Equal(t, 100, params.Limit)
	})

	t.Run("handles mixed string and int values", func(t *testing.T) {
		data := []byte(`{"name":"test","count":"42","limit":100}`)
		var params testIntParams

		err := unmarshalWithIntConversion(data, &params)
		require.NoError(t, err)

		assert.Equal(t, "test", params.Name)
		assert.Equal(t, 42, params.Count)
		assert.Equal(t, 100, params.Limit)
	})

	t.Run("handles pointer int fields with string", func(t *testing.T) {
		data := []byte(`{"name":"test","count":"42","optionalInt":"99"}`)
		var params testIntParams

		err := unmarshalWithIntConversion(data, &params)
		require.NoError(t, err)

		assert.Equal(t, "test", params.Name)
		assert.Equal(t, 42, params.Count)
		require.NotNil(t, params.OptionalInt)
		assert.Equal(t, 99, *params.OptionalInt)
	})

	t.Run("handles omitted optional fields", func(t *testing.T) {
		data := []byte(`{"name":"test","count":"42"}`)
		var params testIntParams

		err := unmarshalWithIntConversion(data, &params)
		require.NoError(t, err)

		assert.Equal(t, "test", params.Name)
		assert.Equal(t, 42, params.Count)
		assert.Equal(t, 0, params.Limit) // default zero value
		assert.Nil(t, params.OptionalInt)
	})

	t.Run("returns error for invalid string to int conversion", func(t *testing.T) {
		data := []byte(`{"name":"test","count":"not-a-number"}`)
		var params testIntParams

		err := unmarshalWithIntConversion(data, &params)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to convert string")
	})

	t.Run("handles zero values as strings", func(t *testing.T) {
		data := []byte(`{"name":"test","count":"0","limit":"0"}`)
		var params testIntParams

		err := unmarshalWithIntConversion(data, &params)
		require.NoError(t, err)

		assert.Equal(t, "test", params.Name)
		assert.Equal(t, 0, params.Count)
		assert.Equal(t, 0, params.Limit)
	})

	t.Run("handles negative numbers as strings", func(t *testing.T) {
		data := []byte(`{"name":"test","count":"-42","limit":"-100"}`)
		var params testIntParams

		err := unmarshalWithIntConversion(data, &params)
		require.NoError(t, err)

		assert.Equal(t, "test", params.Name)
		assert.Equal(t, -42, params.Count)
		assert.Equal(t, -100, params.Limit)
	})
}

func TestConvertToolWithStringToIntConversion(t *testing.T) {
	t.Run("tool handler accepts string values for int parameters", func(t *testing.T) {
		_, handler, err := ConvertTool("test_int_tool", "A test tool with int params", testIntHandler)
		require.NoError(t, err)

		request := mcp.CallToolRequest{
			Params: struct {
				Name      string    `json:"name"`
				Arguments any       `json:"arguments,omitempty"`
				Meta      *mcp.Meta `json:"_meta,omitempty"`
			}{
				Name: "test_int_tool",
				Arguments: map[string]any{
					"name":  "test",
					"count": "42",  // String value
					"limit": "100", // String value
				},
			},
		}

		result, err := handler(context.Background(), request)
		require.NoError(t, err)
		require.NotNil(t, result)
		assert.False(t, result.IsError)
	})

	t.Run("tool handler accepts native int values", func(t *testing.T) {
		_, handler, err := ConvertTool("test_int_tool", "A test tool with int params", testIntHandler)
		require.NoError(t, err)

		request := mcp.CallToolRequest{
			Params: struct {
				Name      string    `json:"name"`
				Arguments any       `json:"arguments,omitempty"`
				Meta      *mcp.Meta `json:"_meta,omitempty"`
			}{
				Name: "test_int_tool",
				Arguments: map[string]any{
					"name":  "test",
					"count": 42,  // Native int
					"limit": 100, // Native int
				},
			},
		}

		result, err := handler(context.Background(), request)
		require.NoError(t, err)
		require.NotNil(t, result)
		assert.False(t, result.IsError)
	})

	t.Run("tool handler accepts mixed string and int values", func(t *testing.T) {
		_, handler, err := ConvertTool("test_int_tool", "A test tool with int params", testIntHandler)
		require.NoError(t, err)

		request := mcp.CallToolRequest{
			Params: struct {
				Name      string    `json:"name"`
				Arguments any       `json:"arguments,omitempty"`
				Meta      *mcp.Meta `json:"_meta,omitempty"`
			}{
				Name: "test_int_tool",
				Arguments: map[string]any{
					"name":  "test",
					"count": "42", // String
					"limit": 100,  // Native int
				},
			},
		}

		result, err := handler(context.Background(), request)
		require.NoError(t, err)
		require.NotNil(t, result)
		assert.False(t, result.IsError)
	})
}
