//go:build unit

package mcpgrafana

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	gcf "github.com/blackwell-systems/gcf-go"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// datasourceRow mirrors the uniform record shape the list tools return.
type datasourceRow struct {
	ID        int64  `json:"id"`
	UID       string `json:"uid"`
	Name      string `json:"name"`
	Type      string `json:"type"`
	IsDefault bool   `json:"isDefault"`
}

func uniformDatasources(n int) map[string]any {
	rows := make([]datasourceRow, 0, n)
	types := []string{"prometheus", "loki", "tempo", "cloudwatch"}
	for i := 0; i < n; i++ {
		rows = append(rows, datasourceRow{
			ID:        int64(i + 1),
			UID:       fmt.Sprintf("ds-%d", i),
			Name:      fmt.Sprintf("datasource %d", i),
			Type:      types[i%len(types)],
			IsDefault: i == 0,
		})
	}
	return map[string]any{"datasources": rows}
}

func TestGCFEnabled(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want bool
	}{
		{"", false},
		{"json", false},
		{"gcf", true},
		{"GCF", true},
		{"  gcf  ", true},
		{"toon", false},
	} {
		assert.Equal(t, tc.want, GrafanaConfig{OutputFormat: tc.in}.GCFEnabled(), "OutputFormat=%q", tc.in)
	}
}

func TestEncodeGCF(t *testing.T) {
	t.Run("uniform record array is smaller and lossless", func(t *testing.T) {
		jsonBytes, err := json.Marshal(uniformDatasources(30))
		require.NoError(t, err)

		wire, ok := encodeGCF(jsonBytes)
		require.True(t, ok, "GCF should win on a uniform record array")
		assert.Less(t, len(wire), len(jsonBytes), "wire must be smaller (never-grow guard)")
		assert.True(t, strings.HasPrefix(wire, "GCF profile=generic"))

		// Round-trips back to the same value.
		decoded, err := gcf.DecodeGeneric(wire)
		require.NoError(t, err)
		assert.True(t, jsonEqualsDecoded(jsonBytes, decoded))
	})

	t.Run("tiny payload falls back to JSON (never-grow)", func(t *testing.T) {
		jsonBytes, err := json.Marshal(map[string]any{"ok": true})
		require.NoError(t, err)
		_, ok := encodeGCF(jsonBytes)
		assert.False(t, ok, "GCF is not smaller on a tiny object, so keep JSON")
	})

	t.Run("single small record falls back to JSON", func(t *testing.T) {
		jsonBytes, err := json.Marshal(uniformDatasources(1))
		require.NoError(t, err)
		_, ok := encodeGCF(jsonBytes)
		assert.False(t, ok)
	})

	t.Run("invalid JSON falls back", func(t *testing.T) {
		_, ok := encodeGCF([]byte("{not json"))
		assert.False(t, ok)
	})

	t.Run("int64 above 2^53 is preserved exactly (not float-rounded)", func(t *testing.T) {
		// A default json.Unmarshal into `any` would turn this into a float64 and
		// silently round it to ...992; encodeGCF must either preserve it exactly
		// or decline, never ship the rounded value.
		rows := make([]map[string]any, 20)
		for i := range rows {
			rows[i] = map[string]any{"id": int64(9007199254740993), "n": "x"}
		}
		jsonBytes, err := json.Marshal(map[string]any{"rows": rows})
		require.NoError(t, err)

		wire, ok := encodeGCF(jsonBytes)
		require.True(t, ok, "20 uniform rows should win with GCF")
		require.NotContains(t, wire, "9.007", "must not render as a rounded float64")
		require.Contains(t, wire, "9007199254740993", "exact integer must survive")

		decoded, err := gcf.DecodeGeneric(wire)
		require.NoError(t, err)
		assert.True(t, jsonEqualsDecoded(jsonBytes, decoded), "must round-trip to the exact integer")
	})
}

// encodeGCFInvariant is the safety contract encodeGCF must satisfy for ANY
// input: it either declines (ok=false, caller keeps JSON), or it returns a wire
// that is strictly smaller than the JSON AND round-trips back to the exact same
// value. A violation means a result could be silently grown or corrupted.
func encodeGCFInvariant(t *testing.T, jsonBytes []byte) {
	t.Helper()
	wire, ok := encodeGCF(jsonBytes)
	if !ok {
		return
	}
	assert.Less(t, len(wire), len(jsonBytes), "ok=true but wire not smaller: %q", wire)

	decoded, err := gcf.DecodeGeneric(wire)
	require.NoError(t, err, "ok=true but wire does not decode: %q", wire)

	assert.True(t, jsonEqualsDecoded(jsonBytes, decoded),
		"ok=true but NOT lossless.\njson:    %s\nwire:    %q\ndecoded: %#v", jsonBytes, wire, decoded)
}

// TestEncodeGCFRobustness exercises the tricky value shapes where a wire format
// can silently corrupt: nested objects/arrays, delimiter and quote characters,
// numeric edges, nulls, empties, and strings that look like other types. For
// each, encodeGCF must either decline or be smaller-and-lossless.
func TestEncodeGCFRobustness(t *testing.T) {
	// A repeated record so the array is large enough that GCF is actually
	// attempted (small ones just decline, which is also valid).
	repeat := func(rec map[string]any, n int) []byte {
		rows := make([]map[string]any, n)
		for i := range rows {
			rows[i] = rec
		}
		b, err := json.Marshal(map[string]any{"rows": rows})
		require.NoError(t, err)
		return b
	}

	cases := map[string]map[string]any{
		"nested object":          {"id": 1, "meta": map[string]any{"team": "a", "tags": map[string]any{"env": "prod"}}},
		"nested array":           {"id": 1, "labels": []any{"prod", "team-3", "region-us"}},
		"array of objects":       {"id": 1, "ports": []any{map[string]any{"p": 80}, map[string]any{"p": 443}}},
		"pipe in value":          {"id": 1, "q": "a | b | c"},
		"comma in value":         {"id": 1, "name": "Smith, John"},
		"quote in value":         {"id": 1, "name": `he said "hi"`},
		"newline in value":       {"id": 1, "desc": "line1\nline2"},
		"leading space":          {"id": 1, "name": "  spaced  "},
		"empty string":           {"id": 1, "name": ""},
		"unicode":                {"id": 1, "name": "café ☕ 日本語"},
		"string looks like int":  {"id": 1, "code": "007"},
		"string looks like bool": {"id": 1, "flag": "true"},
		"float":                  {"id": 1, "score": 2.31},
		"negative":               {"id": -5, "delta": -0.5},
		"zero":                   {"id": 0, "v": 0},
		"large int":              {"id": 9007199254740993, "v": 1},
		"null value":             {"id": 1, "note": nil},
		"empty array field":      {"id": 1, "tags": []any{}},
		"empty object field":     {"id": 1, "meta": map[string]any{}},
		"bool mix":               {"id": 1, "a": true, "b": false},
	}
	for name, rec := range cases {
		t.Run(name, func(t *testing.T) {
			encodeGCFInvariant(t, repeat(rec, 20))
		})
	}

	// Degenerate top-level shapes.
	for name, v := range map[string]any{
		"empty array":      []any{},
		"empty object":     map[string]any{},
		"array of scalars": []any{1, 2, 3, "x", true, nil},
		"scalar string":    "just a string",
		"scalar number":    42,
		"null":             nil,
	} {
		t.Run("toplevel "+name, func(t *testing.T) {
			b, err := json.Marshal(v)
			require.NoError(t, err)
			encodeGCFInvariant(t, b)
		})
	}
}

// FuzzEncodeGCF asserts the safety invariant on arbitrary JSON: encodeGCF is
// never lossy and never grows a result. Run: go test -tags unit -run x -fuzz FuzzEncodeGCF
func FuzzEncodeGCF(f *testing.F) {
	seeds := []string{
		`{"rows":[{"id":1,"n":"a"},{"id":2,"n":"b"}]}`,
		`[{"a":1,"b":"x|y"},{"a":2,"b":"p,q"}]`,
		`{"x":[{"k":"café"},{"k":"日本"}]}`,
		`{"n":null,"e":[],"o":{}}`,
		`{"big":9007199254740993,"f":-0.5,"z":0}`,
		`"scalar"`,
		`42`,
		`[]`,
	}
	for _, s := range seeds {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, in []byte) {
		// Only consider inputs that are valid JSON and re-marshal canonically,
		// matching what the tool serializer hands to encodeGCF.
		var v any
		if err := json.Unmarshal(in, &v); err != nil {
			return
		}
		canonical, err := json.Marshal(v)
		if err != nil {
			return
		}
		wire, ok := encodeGCF(canonical)
		if !ok {
			return
		}
		if len(wire) >= len(canonical) {
			t.Fatalf("wire not smaller: json=%s wire=%q", canonical, wire)
		}
		decoded, err := gcf.DecodeGeneric(wire)
		if err != nil {
			t.Fatalf("wire does not decode: %q err=%v", wire, err)
		}
		if !jsonEqualsDecoded(canonical, decoded) {
			t.Fatalf("NOT lossless: json=%s wire=%q decoded=%#v", canonical, wire, decoded)
		}
	})
}

// gcfListHandler returns a uniform record array whose size scales with the
// requested count, so a large count wins with GCF and a small one does not.
func gcfListHandler(ctx context.Context, params testToolParams) (map[string]any, error) {
	return uniformDatasources(params.Value), nil
}

func TestMustToolGCFOutput(t *testing.T) {
	_, handler, err := ConvertTool("gcf_list", "list", gcfListHandler)
	require.NoError(t, err)

	req := mcp.CallToolRequest{Params: mcp.CallToolParams{
		Name:      "gcf_list",
		Arguments: map[string]any{"name": "x", "value": 30},
	}}

	// Default (JSON) output is unchanged.
	res, err := handler(context.Background(), req)
	require.NoError(t, err)
	jsonText := res.Content[0].(mcp.TextContent).Text
	assert.True(t, strings.HasPrefix(jsonText, "{"), "default output stays JSON")

	// With GCF enabled and a winning payload, the text block is GCF and decodes
	// back to the JSON value.
	ctx := WithGrafanaConfig(context.Background(), GrafanaConfig{OutputFormat: "gcf"})
	res, err = handler(ctx, req)
	require.NoError(t, err)
	gcfText := res.Content[0].(mcp.TextContent).Text
	require.True(t, strings.HasPrefix(gcfText, "GCF profile=generic"), "GCF output expected, got: %.40s", gcfText)
	assert.Less(t, len(gcfText), len(jsonText))

	decoded, err := gcf.DecodeGeneric(gcfText)
	require.NoError(t, err)
	assert.True(t, jsonEqualsDecoded([]byte(jsonText), decoded))

	// With GCF enabled but a payload GCF cannot shrink, it falls back to JSON.
	smallReq := mcp.CallToolRequest{Params: mcp.CallToolParams{
		Name:      "gcf_list",
		Arguments: map[string]any{"name": "x", "value": 1},
	}}
	res, err = handler(ctx, smallReq)
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(res.Content[0].(mcp.TextContent).Text, "{"), "tiny result stays JSON even with GCF on")
}
