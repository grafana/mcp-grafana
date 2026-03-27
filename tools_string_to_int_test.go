package mcpgrafana

import (
	"context"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Shared test param types used across multiple tests.

type testIntParams struct {
	Name        string `json:"name" jsonschema:"required,description=Name parameter"`
	Count       int    `json:"count" jsonschema:"description=Count parameter as int"`
	Limit       int    `json:"limit,omitempty" jsonschema:"default=10,description=Limit parameter"`
	OptionalInt *int   `json:"optionalInt,omitempty" jsonschema:"description=Optional int pointer"`
}

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

type EmbeddedIntFields struct {
	EmbeddedCount int `json:"embeddedCount" jsonschema:"description=Embedded count field"`
}

type testEmbeddedParams struct {
	Name string `json:"name" jsonschema:"required,description=Name parameter"`
	*EmbeddedIntFields
}

type embeddedIntFieldsValue struct {
	EmbeddedCount int `json:"embeddedCount" jsonschema:"description=Embedded count field"`
}

type testRegularEmbeddedParams struct {
	Name string `json:"name" jsonschema:"required,description=Name parameter"`
	embeddedIntFieldsValue
}

func testIntHandler(_ context.Context, _ testIntParams) (string, error) {
	return "success", nil
}

func TestUnmarshalWithIntConversion(t *testing.T) {
	optInt := func(n int) *int { return &n }

	tests := []struct {
		name    string
		data    string
		want    testIntParams
		wantErr string // substring to match; empty = no error expected
	}{
		{
			name: "converts string to int for int fields",
			data: `{"name":"test","count":"42","limit":"100"}`,
			want: testIntParams{Name: "test", Count: 42, Limit: 100},
		},
		{
			name: "accepts native int values",
			data: `{"name":"test","count":42,"limit":100}`,
			want: testIntParams{Name: "test", Count: 42, Limit: 100},
		},
		{
			name: "handles mixed string and int values",
			data: `{"name":"test","count":"42","limit":100}`,
			want: testIntParams{Name: "test", Count: 42, Limit: 100},
		},
		{
			name: "handles pointer int field with string",
			data: `{"name":"test","count":"42","optionalInt":"99"}`,
			want: testIntParams{Name: "test", Count: 42, OptionalInt: optInt(99)},
		},
		{
			name: "omitted optional fields stay at zero value",
			data: `{"name":"test","count":"42"}`,
			want: testIntParams{Name: "test", Count: 42},
		},
		{
			name: "handles zero values as strings",
			data: `{"name":"test","count":"0","limit":"0"}`,
			want: testIntParams{Name: "test"},
		},
		{
			name: "handles negative numbers as strings",
			data: `{"name":"test","count":"-42","limit":"-100"}`,
			want: testIntParams{Name: "test", Count: -42, Limit: -100},
		},
		{
			name: "accepts JSON null for pointer int field",
			data: `{"name":"test","count":"42","optionalInt":null}`,
			want: testIntParams{Name: "test", Count: 42},
		},
		{
			name: "handles embedded pointer-to-struct with int fields",
			// tested separately via testEmbeddedParams below
		},
		{
			name:    "rejects non-numeric string for int field",
			data:    `{"name":"test","count":"not-a-number"}`,
			wantErr: "invalid character", // stripped value is not valid JSON; fails at re-marshal
		},
		{
			name: `accepts string "null" for non-pointer int field (strips to JSON null, which zeroes the field)`,
			data: `{"name":"test","count":"null"}`,
			want: testIntParams{Name: "test", Count: 0},
		},
		{
			name:    `rejects string "true" for int field`,
			data:    `{"name":"test","count":"true"}`,
			wantErr: "cannot unmarshal",
		},
		{
			name:    `rejects string "false" for int field`,
			data:    `{"name":"test","count":"false"}`,
			wantErr: "cannot unmarshal",
		},
	}

	for _, tc := range tests {
		if tc.data == "" {
			continue // placeholder entries tested elsewhere
		}
		t.Run(tc.name, func(t *testing.T) {
			var got testIntParams
			err := unmarshalWithIntConversion([]byte(tc.data), &got)
			if tc.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}

	t.Run("handles embedded pointer-to-struct with int fields", func(t *testing.T) {
		var got testEmbeddedParams
		require.NoError(t, unmarshalWithIntConversion([]byte(`{"name":"test","embeddedCount":"42"}`), &got))
		assert.Equal(t, "test", got.Name)
		require.NotNil(t, got.EmbeddedIntFields)
		assert.Equal(t, 42, got.EmbeddedCount)
	})

	t.Run("handles regular embedded struct with int fields", func(t *testing.T) {
		var got testRegularEmbeddedParams
		require.NoError(t, unmarshalWithIntConversion([]byte(`{"name":"test","embeddedCount":"99"}`), &got))
		assert.Equal(t, "test", got.Name)
		assert.Equal(t, 99, got.EmbeddedCount)
	})

	t.Run("converts string to int for fields with no json tag (uses Go field name)", func(t *testing.T) {
		type params struct {
			Count int // no json tag at all — encoding/json uses "Count"
		}
		var got params
		require.NoError(t, unmarshalWithIntConversion([]byte(`{"Count":"42"}`), &got))
		assert.Equal(t, 42, got.Count)
	})

	t.Run("converts string to int for fields with omitempty but no tag name", func(t *testing.T) {
		type params struct {
			Count int `json:",omitempty"` // name part is empty — encoding/json uses "Count"
		}
		var got params
		require.NoError(t, unmarshalWithIntConversion([]byte(`{"Count":"42"}`), &got))
		assert.Equal(t, 42, got.Count)
	})

	t.Run("skips fields with json:\"-\" tag", func(t *testing.T) {
		type params struct {
			Ignored int    `json:"-"`
			Name    string `json:"name"`
		}
		var got params
		// "-" fields are excluded by encoding/json; they cannot be set via JSON at all
		require.NoError(t, unmarshalWithIntConversion([]byte(`{"name":"test","Ignored":"99"}`), &got))
		assert.Equal(t, "test", got.Name)
		assert.Equal(t, 0, got.Ignored)
	})

	t.Run(`accepts string "null" in embedded pointer-to-struct int fields (strips to JSON null, zeroes field)`, func(t *testing.T) {
		var got testEmbeddedParams
		require.NoError(t, unmarshalWithIntConversion([]byte(`{"name":"test","embeddedCount":"null"}`), &got))
		assert.Equal(t, "test", got.Name)
		require.NotNil(t, got.EmbeddedIntFields)
		assert.Equal(t, 0, got.EmbeddedCount)
	})
}

func TestConvertToolWithStringToIntConversion(t *testing.T) {
	_, handler, err := ConvertTool("test_int_tool", "A test tool with int params", testIntHandler)
	require.NoError(t, err)

	makeRequest := func(args map[string]any) mcp.CallToolRequest {
		return mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Name:      "test_int_tool",
				Arguments: args,
			},
		}
	}

	tests := []struct {
		name string
		args map[string]any
	}{
		{
			name: "tool handler accepts string values for int parameters",
			args: map[string]any{"name": "test", "count": "42", "limit": "100"},
		},
		{
			name: "tool handler accepts native int values",
			args: map[string]any{"name": "test", "count": 42, "limit": 100},
		},
		{
			name: "tool handler accepts mixed string and int values",
			args: map[string]any{"name": "test", "count": "42", "limit": 100},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result, err := handler(context.Background(), makeRequest(tc.args))
			require.NoError(t, err)
			require.NotNil(t, result)
			assert.False(t, result.IsError)
		})
	}
}

func TestUnmarshalWithAllIntegerTypes(t *testing.T) {
	tests := []struct {
		name    string
		data    string
		want    testAllIntTypesParams
		wantErr string
	}{
		{
			name: "converts strings to all signed/unsigned integer types",
			data: `{
				"int8Field":"127","int16Field":"32767","int32Field":"2147483647",
				"int64Field":"9223372036854775807","uintField":"42","uint8Field":"255",
				"uint16Field":"65535","uint32Field":"4294967295","uint64Field":"18446744073709551615"
			}`,
			want: testAllIntTypesParams{
				Int8Field: 127, Int16Field: 32767, Int32Field: 2147483647,
				Int64Field: 9223372036854775807, UintField: 42, Uint8Field: 255,
				Uint16Field: 65535, Uint32Field: 4294967295, Uint64Field: 18446744073709551615,
			},
		},
		{
			name: "accepts native values for all integer types",
			data: `{
				"int8Field":127,"int16Field":32767,"int32Field":2147483647,
				"int64Field":123456789012345,"uintField":42,"uint8Field":255,
				"uint16Field":65535,"uint32Field":4294967295,"uint64Field":123456789012345
			}`,
			want: testAllIntTypesParams{
				Int8Field: 127, Int16Field: 32767, Int32Field: 2147483647,
				Int64Field: 123456789012345, UintField: 42, Uint8Field: 255,
				Uint16Field: 65535, Uint32Field: 4294967295, Uint64Field: 123456789012345,
			},
		},
		{
			name: "handles pointer integer types with strings",
			data: `{
				"int8Field":"1","int16Field":"1","int32Field":"1","int64Field":"9223372036854775807",
				"uintField":"1","uint8Field":"1","uint16Field":"1","uint32Field":"4294967295","uint64Field":"1",
				"ptrInt64":"123456789","ptrUint32":"987654321"
			}`,
			want: func() testAllIntTypesParams {
				p := testAllIntTypesParams{
					Int8Field: 1, Int16Field: 1, Int32Field: 1, Int64Field: 9223372036854775807,
					UintField: 1, Uint8Field: 1, Uint16Field: 1, Uint32Field: 4294967295, Uint64Field: 1,
				}
				i64, u32 := int64(123456789), uint32(987654321)
				p.PtrInt64, p.PtrUint32 = &i64, &u32
				return p
			}(),
		},
		{
			name: "handles min values for signed types as strings",
			data: `{
				"int8Field":"-128","int16Field":"-32768","int32Field":"-2147483648",
				"int64Field":"-9223372036854775808",
				"uintField":"0","uint8Field":"0","uint16Field":"0","uint32Field":"0","uint64Field":"0"
			}`,
			want: testAllIntTypesParams{
				Int8Field: -128, Int16Field: -32768, Int32Field: -2147483648,
				Int64Field: -9223372036854775808,
			},
		},
		{
			name:    "returns error for int8 overflow",
			data:    `{"int8Field":"128","int16Field":"1","int32Field":"1","int64Field":"1","uintField":"1","uint8Field":"1","uint16Field":"1","uint32Field":"1","uint64Field":"1"}`,
			wantErr: "cannot unmarshal",
		},
		{
			name:    "returns error for int16 overflow",
			data:    `{"int8Field":"1","int16Field":"32768","int32Field":"1","int64Field":"1","uintField":"1","uint8Field":"1","uint16Field":"1","uint32Field":"1","uint64Field":"1"}`,
			wantErr: "cannot unmarshal",
		},
		{
			name:    "returns error for int32 overflow",
			data:    `{"int8Field":"1","int16Field":"1","int32Field":"2147483648","int64Field":"1","uintField":"1","uint8Field":"1","uint16Field":"1","uint32Field":"1","uint64Field":"1"}`,
			wantErr: "cannot unmarshal",
		},
		{
			name:    "returns error for uint8 overflow",
			data:    `{"int8Field":"1","int16Field":"1","int32Field":"1","int64Field":"1","uintField":"1","uint8Field":"256","uint16Field":"1","uint32Field":"1","uint64Field":"1"}`,
			wantErr: "cannot unmarshal",
		},
		{
			name:    "returns error for negative uint values",
			data:    `{"int8Field":"1","int16Field":"1","int32Field":"1","int64Field":"1","uintField":"-1","uint8Field":"1","uint16Field":"1","uint32Field":"1","uint64Field":"1"}`,
			wantErr: "cannot unmarshal",
		},
		{
			name: "handles real-world case with OrgID int64",
			data: `{"int8Field":"1","int16Field":"1","int32Field":"1","int64Field":"1","uintField":"1","uint8Field":"1","uint16Field":"1","uint32Field":"1","uint64Field":"1"}`,
			want: testAllIntTypesParams{
				Int8Field: 1, Int16Field: 1, Int32Field: 1, Int64Field: 1,
				UintField: 1, Uint8Field: 1, Uint16Field: 1, Uint32Field: 1, Uint64Field: 1,
			},
		},
		{
			name: "preserves precision for large int64 values beyond float64 precision",
			// tested separately below (requires a different struct type)
		},
		{
			name: "does not convert string fields to integers",
			// tested separately below (requires a different struct type)
		},
		{
			name: "does not convert float fields",
			// tested separately below (requires a different struct type)
		},
	}

	for _, tc := range tests {
		if tc.data == "" {
			continue // placeholder entries tested below
		}
		t.Run(tc.name, func(t *testing.T) {
			var got testAllIntTypesParams
			err := unmarshalWithIntConversion([]byte(tc.data), &got)
			if tc.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}

	t.Run("preserves precision for large int64 values beyond float64 precision", func(t *testing.T) {
		type largeIntParams struct {
			LargeInt64  int64  `json:"largeInt64"`
			LargeUint64 uint64 `json:"largeUint64"`
			Timestamp   int64  `json:"timestamp"`
		}
		tests := []struct {
			name string
			data string
			want largeIntParams
		}{
			{
				name: "as native JSON numbers",
				data: `{"largeInt64":9223372036854775807,"largeUint64":18446744073709551615,"timestamp":1709676543210}`,
				want: largeIntParams{LargeInt64: 9223372036854775807, LargeUint64: 18446744073709551615, Timestamp: 1709676543210},
			},
			{
				name: "as strings",
				data: `{"largeInt64":"9223372036854775807","largeUint64":"18446744073709551615","timestamp":"1709676543210"}`,
				want: largeIntParams{LargeInt64: 9223372036854775807, LargeUint64: 18446744073709551615, Timestamp: 1709676543210},
			},
			{
				name: "value just above float64 precision boundary (2^53+1)",
				data: `{"largeInt64":9007199254740993,"largeUint64":0,"timestamp":0}`,
				want: largeIntParams{LargeInt64: 9007199254740993},
			},
		}
		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				var got largeIntParams
				require.NoError(t, unmarshalWithIntConversion([]byte(tc.data), &got))
				assert.Equal(t, tc.want, got)
			})
		}
	})

	t.Run("does not convert string fields to integers", func(t *testing.T) {
		type params struct {
			Name    string `json:"name"`
			ZipCode string `json:"zipCode"`
			Count   int    `json:"count"`
		}
		var got params
		require.NoError(t, unmarshalWithIntConversion([]byte(`{"name":"test","zipCode":"90210","count":"42"}`), &got))
		assert.Equal(t, "90210", got.ZipCode)
		assert.Equal(t, 42, got.Count)
	})

	t.Run("does not convert float fields", func(t *testing.T) {
		type params struct {
			Price  float64 `json:"price"`
			Rating float32 `json:"rating"`
			Count  int     `json:"count"`
		}
		var got params
		require.NoError(t, unmarshalWithIntConversion([]byte(`{"price":19.99,"rating":4.5,"count":"42"}`), &got))
		assert.InDelta(t, 19.99, got.Price, 0.001)
		assert.InDelta(t, 4.5, got.Rating, 0.001)
		assert.Equal(t, 42, got.Count)
	})

	t.Run("handles embedded struct fields with string-to-int conversion", func(t *testing.T) {
		type EmbeddedParams struct {
			ID    int64 `json:"id"`
			Count int   `json:"count"`
		}
		type ParentParams struct {
			Name string `json:"name"`
			EmbeddedParams
			Limit int `json:"limit"`
		}
		tests := []struct {
			name string
			data string
			want ParentParams
		}{
			{
				name: "as strings",
				data: `{"name":"test","id":"9223372036854775807","count":"42","limit":"100"}`,
				want: ParentParams{Name: "test", EmbeddedParams: EmbeddedParams{ID: 9223372036854775807, Count: 42}, Limit: 100},
			},
			{
				name: "as native JSON numbers",
				data: `{"name":"test","id":9223372036854775807,"count":42}`,
				want: ParentParams{Name: "test", EmbeddedParams: EmbeddedParams{ID: 9223372036854775807, Count: 42}},
			},
		}
		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				var got ParentParams
				require.NoError(t, unmarshalWithIntConversion([]byte(tc.data), &got))
				assert.Equal(t, tc.want, got)
			})
		}
	})
}
