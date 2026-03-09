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

// Test struct with all integer types
type testAllIntTypesParams struct {
	Int8Field   int8    `json:"int8Field" jsonschema:"description=Int8 field"`
	Int16Field  int16   `json:"int16Field" jsonschema:"description=Int16 field"`
	Int32Field  int32   `json:"int32Field" jsonschema:"description=Int32 field"`
	Int64Field  int64   `json:"int64Field" jsonschema:"description=Int64 field"`
	UintField   uint    `json:"uintField" jsonschema:"description=Uint field"`
	Uint8Field  uint8   `json:"uint8Field" jsonschema:"description=Uint8 field"`
	Uint16Field uint16  `json:"uint16Field" jsonschema:"description=Uint16 field"`
	Uint32Field uint32  `json:"uint32Field" jsonschema:"description=Uint32 field"`
	Uint64Field uint64  `json:"uint64Field" jsonschema:"description=Uint64 field"`
	PtrInt64    *int64  `json:"ptrInt64,omitempty" jsonschema:"description=Pointer to int64"`
	PtrUint32   *uint32 `json:"ptrUint32,omitempty" jsonschema:"description=Pointer to uint32"`
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
		// Standard json.Unmarshal error for invalid syntax
		assert.Contains(t, err.Error(), "invalid")
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

func TestUnmarshalWithAllIntegerTypes(t *testing.T) {
	t.Run("converts strings to all signed integer types", func(t *testing.T) {
		data := []byte(`{
			"int8Field": "127",
			"int16Field": "32767",
			"int32Field": "2147483647",
			"int64Field": "9223372036854775807",
			"uintField": "42",
			"uint8Field": "255",
			"uint16Field": "65535",
			"uint32Field": "4294967295",
			"uint64Field": "18446744073709551615"
		}`)
		var params testAllIntTypesParams

		err := unmarshalWithIntConversion(data, &params)
		require.NoError(t, err)

		assert.Equal(t, int8(127), params.Int8Field)
		assert.Equal(t, int16(32767), params.Int16Field)
		assert.Equal(t, int32(2147483647), params.Int32Field)
		assert.Equal(t, int64(9223372036854775807), params.Int64Field)
		assert.Equal(t, uint(42), params.UintField)
		assert.Equal(t, uint8(255), params.Uint8Field)
		assert.Equal(t, uint16(65535), params.Uint16Field)
		assert.Equal(t, uint32(4294967295), params.Uint32Field)
		assert.Equal(t, uint64(18446744073709551615), params.Uint64Field)
	})

	t.Run("accepts native values for all integer types", func(t *testing.T) {
		data := []byte(`{
			"int8Field": 127,
			"int16Field": 32767,
			"int32Field": 2147483647,
			"int64Field": 123456789012345,
			"uintField": 42,
			"uint8Field": 255,
			"uint16Field": 65535,
			"uint32Field": 4294967295,
			"uint64Field": 123456789012345
		}`)
		var params testAllIntTypesParams

		err := unmarshalWithIntConversion(data, &params)
		require.NoError(t, err)

		assert.Equal(t, int8(127), params.Int8Field)
		assert.Equal(t, int16(32767), params.Int16Field)
		assert.Equal(t, int32(2147483647), params.Int32Field)
		assert.Equal(t, int64(123456789012345), params.Int64Field)
		assert.Equal(t, uint(42), params.UintField)
		assert.Equal(t, uint8(255), params.Uint8Field)
		assert.Equal(t, uint16(65535), params.Uint16Field)
		assert.Equal(t, uint32(4294967295), params.Uint32Field)
		assert.Equal(t, uint64(123456789012345), params.Uint64Field)
	})

	t.Run("handles pointer integer types with strings", func(t *testing.T) {
		data := []byte(`{
			"int8Field": "1",
			"int16Field": "1",
			"int32Field": "1",
			"int64Field": "9223372036854775807",
			"uintField": "1",
			"uint8Field": "1",
			"uint16Field": "1",
			"uint32Field": "4294967295",
			"uint64Field": "1",
			"ptrInt64": "123456789",
			"ptrUint32": "987654321"
		}`)
		var params testAllIntTypesParams

		err := unmarshalWithIntConversion(data, &params)
		require.NoError(t, err)

		require.NotNil(t, params.PtrInt64)
		assert.Equal(t, int64(123456789), *params.PtrInt64)
		require.NotNil(t, params.PtrUint32)
		assert.Equal(t, uint32(987654321), *params.PtrUint32)
	})

	t.Run("handles negative values for signed types", func(t *testing.T) {
		data := []byte(`{
			"int8Field": "-128",
			"int16Field": "-32768",
			"int32Field": "-2147483648",
			"int64Field": "-9223372036854775808",
			"uintField": "0",
			"uint8Field": "0",
			"uint16Field": "0",
			"uint32Field": "0",
			"uint64Field": "0"
		}`)
		var params testAllIntTypesParams

		err := unmarshalWithIntConversion(data, &params)
		require.NoError(t, err)

		assert.Equal(t, int8(-128), params.Int8Field)
		assert.Equal(t, int16(-32768), params.Int16Field)
		assert.Equal(t, int32(-2147483648), params.Int32Field)
		assert.Equal(t, int64(-9223372036854775808), params.Int64Field)
	})

	t.Run("returns error for int8 overflow", func(t *testing.T) {
		data := []byte(`{"int8Field": "128", "int16Field": "1", "int32Field": "1", "int64Field": "1", "uintField": "1", "uint8Field": "1", "uint16Field": "1", "uint32Field": "1", "uint64Field": "1"}`)
		var params testAllIntTypesParams

		err := unmarshalWithIntConversion(data, &params)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "cannot unmarshal")
	})

	t.Run("returns error for int16 overflow", func(t *testing.T) {
		data := []byte(`{"int8Field": "1", "int16Field": "32768", "int32Field": "1", "int64Field": "1", "uintField": "1", "uint8Field": "1", "uint16Field": "1", "uint32Field": "1", "uint64Field": "1"}`)
		var params testAllIntTypesParams

		err := unmarshalWithIntConversion(data, &params)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "cannot unmarshal")
	})

	t.Run("returns error for int32 overflow", func(t *testing.T) {
		data := []byte(`{"int8Field": "1", "int16Field": "1", "int32Field": "2147483648", "int64Field": "1", "uintField": "1", "uint8Field": "1", "uint16Field": "1", "uint32Field": "1", "uint64Field": "1"}`)
		var params testAllIntTypesParams

		err := unmarshalWithIntConversion(data, &params)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "cannot unmarshal")
	})

	t.Run("returns error for uint8 overflow", func(t *testing.T) {
		data := []byte(`{"int8Field": "1", "int16Field": "1", "int32Field": "1", "int64Field": "1", "uintField": "1", "uint8Field": "256", "uint16Field": "1", "uint32Field": "1", "uint64Field": "1"}`)
		var params testAllIntTypesParams

		err := unmarshalWithIntConversion(data, &params)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "cannot unmarshal")
	})

	t.Run("returns error for negative uint values", func(t *testing.T) {
		data := []byte(`{"int8Field": "1", "int16Field": "1", "int32Field": "1", "int64Field": "1", "uintField": "-1", "uint8Field": "1", "uint16Field": "1", "uint32Field": "1", "uint64Field": "1"}`)
		var params testAllIntTypesParams

		err := unmarshalWithIntConversion(data, &params)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "cannot unmarshal")
	})

	t.Run("handles real-world case with OrgID int64", func(t *testing.T) {
		// Simulates the CreateAlertRuleParams/UpdateAlertRuleParams use case
		type alertParams struct {
			Title string `json:"title"`
			OrgID int64  `json:"orgID"`
		}

		// Test with string value (which MCP clients might send)
		data := []byte(`{"title": "Test Alert", "orgID": "1"}`)
		var params alertParams

		err := unmarshalWithIntConversion(data, &params)
		require.NoError(t, err)

		assert.Equal(t, "Test Alert", params.Title)
		assert.Equal(t, int64(1), params.OrgID)
	})

	t.Run("preserves precision for large int64 values beyond float64 precision", func(t *testing.T) {
		// Test values larger than 2^53 (9007199254740992)
		// These would lose precision if converted through float64
		type largeIntParams struct {
			LargeInt64  int64  `json:"largeInt64"`
			LargeUint64 uint64 `json:"largeUint64"`
			Timestamp   int64  `json:"timestamp"` // epoch milliseconds
		}

		// Values that would be corrupted by float64 round-trip:
		// - 9223372036854775807 is max int64
		// - 18446744073709551615 is max uint64
		// - 1709676543210 is a realistic epoch millisecond timestamp
		data := []byte(`{
			"largeInt64": 9223372036854775807,
			"largeUint64": 18446744073709551615,
			"timestamp": 1709676543210
		}`)
		var params largeIntParams

		err := unmarshalWithIntConversion(data, &params)
		require.NoError(t, err)

		// Verify exact values without precision loss
		assert.Equal(t, int64(9223372036854775807), params.LargeInt64)
		assert.Equal(t, uint64(18446744073709551615), params.LargeUint64)
		assert.Equal(t, int64(1709676543210), params.Timestamp)
	})

	t.Run("preserves precision for large int64 values as strings", func(t *testing.T) {
		type largeIntParams struct {
			LargeInt64  int64  `json:"largeInt64"`
			LargeUint64 uint64 `json:"largeUint64"`
		}

		// Test with string representations
		data := []byte(`{
			"largeInt64": "9223372036854775807",
			"largeUint64": "18446744073709551615"
		}`)
		var params largeIntParams

		err := unmarshalWithIntConversion(data, &params)
		require.NoError(t, err)

		assert.Equal(t, int64(9223372036854775807), params.LargeInt64)
		assert.Equal(t, uint64(18446744073709551615), params.LargeUint64)
	})

	t.Run("preserves precision for values just above float64 precision boundary", func(t *testing.T) {
		// 2^53 + 1 = 9007199254740993
		// This is the smallest integer that would lose precision in float64
		type precisionParams struct {
			Value int64 `json:"value"`
		}

		data := []byte(`{"value": 9007199254740993}`)
		var params precisionParams

		err := unmarshalWithIntConversion(data, &params)
		require.NoError(t, err)

		// This would fail with the old implementation using raw json.Unmarshal
		// because float64 cannot represent 9007199254740993 exactly
		assert.Equal(t, int64(9007199254740993), params.Value)
	})

	t.Run("handles embedded struct fields with string-to-int conversion", func(t *testing.T) {
		// Test that embedded (anonymous) struct fields are processed correctly
		type EmbeddedParams struct {
			ID    int64 `json:"id"`
			Count int   `json:"count"`
		}

		type ParentParams struct {
			Name string `json:"name"`
			EmbeddedParams
			Limit int `json:"limit"`
		}

		// Test with string values in embedded struct fields
		data := []byte(`{"name": "test", "id": "9223372036854775807", "count": "42", "limit": "100"}`)
		var params ParentParams

		err := unmarshalWithIntConversion(data, &params)
		require.NoError(t, err)

		assert.Equal(t, "test", params.Name)
		assert.Equal(t, int64(9223372036854775807), params.ID)
		assert.Equal(t, 42, params.Count)
		assert.Equal(t, 100, params.Limit)
	})

	t.Run("handles embedded struct fields with native int values", func(t *testing.T) {
		type EmbeddedParams struct {
			ID    int64 `json:"id"`
			Count int   `json:"count"`
		}

		type ParentParams struct {
			Name string `json:"name"`
			EmbeddedParams
		}

		// Test with native JSON numbers in embedded struct fields
		data := []byte(`{"name": "test", "id": 9223372036854775807, "count": 42}`)
		var params ParentParams

		err := unmarshalWithIntConversion(data, &params)
		require.NoError(t, err)

		assert.Equal(t, "test", params.Name)
		assert.Equal(t, int64(9223372036854775807), params.ID)
		assert.Equal(t, 42, params.Count)
	})

	t.Run("does not convert string fields to integers", func(t *testing.T) {
		// Verify that string fields containing numeric strings are not converted
		type ParamsWithStrings struct {
			Name    string `json:"name"`
			ZipCode string `json:"zipCode"`
			Count   int    `json:"count"`
		}

		data := []byte(`{"name": "test", "zipCode": "90210", "count": "42"}`)
		var params ParamsWithStrings

		err := unmarshalWithIntConversion(data, &params)
		require.NoError(t, err)

		assert.Equal(t, "test", params.Name)
		assert.Equal(t, "90210", params.ZipCode) // Should remain a string
		assert.Equal(t, 42, params.Count)        // Should be converted to int
	})

	t.Run("does not convert float fields", func(t *testing.T) {
		// Verify that float fields are not affected by integer conversion logic
		type ParamsWithFloats struct {
			Name   string  `json:"name"`
			Price  float64 `json:"price"`
			Rating float32 `json:"rating"`
			Count  int     `json:"count"`
		}

		data := []byte(`{"name": "test", "price": 19.99, "rating": 4.5, "count": "42"}`)
		var params ParamsWithFloats

		err := unmarshalWithIntConversion(data, &params)
		require.NoError(t, err)

		assert.Equal(t, "test", params.Name)
		assert.InDelta(t, 19.99, params.Price, 0.001)
		assert.InDelta(t, 4.5, params.Rating, 0.001)
		assert.Equal(t, 42, params.Count)
	})
}
