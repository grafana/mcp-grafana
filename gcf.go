package mcpgrafana

import (
	"bytes"
	"encoding/json"
	"reflect"

	gcf "github.com/blackwell-systems/gcf-go"
)

// encodeGCF re-encodes a JSON tool result as a Graph Compact Format generic
// wire (https://gcformat.com), returning ok=false when the caller should keep
// the original JSON.
//
// It is deliberately conservative, so enabling GCF output can only ever help:
//
//   - Never larger: if the GCF wire is not smaller than the JSON, ok=false
//     (the never-grow guard). GCF factors repeated field names out of uniform
//     record arrays, so it wins on list results and ties on tiny or
//     high-entropy ones; the guard keeps those from regressing.
//   - Never lossy: the wire is decoded and compared back to the ORIGINAL JSON
//     bytes with number-exact, order-insensitive semantics; any encode error,
//     decode error, or mismatch yields ok=false. A tool result is never dropped
//     or altered over encoding, including integers that JSON preserves exactly
//     but a float64 round-trip would not (e.g. IDs or timestamps above 2^53).
func encodeGCF(jsonBytes []byte) (wire string, ok bool) {
	// Decode with exact numbers (int64 where representable, else float64) so the
	// value handed to gcf-go carries the same integer precision as the JSON,
	// rather than the lossy float64 that encoding/json uses for `any` by default.
	v, err := decodeExact(jsonBytes)
	if err != nil {
		return "", false
	}

	// EncodeGenericChecked never panics (it returns the numeric-domain error
	// instead), so an out-of-domain value falls back to JSON rather than failing
	// the tool call.
	wire, err = gcf.EncodeGenericChecked(v)
	if err != nil {
		return "", false
	}

	// Never-grow guard: only offer GCF when it is actually smaller.
	if len(wire) >= len(jsonBytes) {
		return "", false
	}

	// Fail-safe: require a clean, number-exact, order-insensitive round-trip back
	// to the original JSON.
	decoded, err := gcf.DecodeGeneric(wire)
	if err != nil || !jsonEqualsDecoded(jsonBytes, decoded) {
		return "", false
	}

	return wire, true
}

// jsonEqualsDecoded reports whether the gcf-decoded value represents the same
// JSON as jsonBytes, ignoring map key order and preserving exact integer values.
// Both sides are reduced to a canonical form built from a number-preserving JSON
// decode, so gcf-go's order-preserving OrderedMap and int64 decoding compare
// equal to the original, and any precision or type drift is caught.
func jsonEqualsDecoded(jsonBytes []byte, decoded any) bool {
	left, err := canonicalize(jsonBytes)
	if err != nil {
		return false
	}
	decodedBytes, err := json.Marshal(decoded)
	if err != nil {
		return false
	}
	right, err := canonicalize(decodedBytes)
	if err != nil {
		return false
	}
	return reflect.DeepEqual(left, right)
}

// canonicalize decodes JSON with json.Number (so integer text is compared
// exactly) and returns a form where maps become key-sorted [][2]any slices, so
// DeepEqual is independent of key order.
func canonicalize(b []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	return canonValue(v), nil
}

func canonValue(v any) any {
	switch t := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		// insertion sort keeps the dependency surface minimal; maps are small.
		for i := 1; i < len(keys); i++ {
			for j := i; j > 0 && keys[j] < keys[j-1]; j-- {
				keys[j], keys[j-1] = keys[j-1], keys[j]
			}
		}
		out := make([][2]any, len(keys))
		for i, k := range keys {
			out[i] = [2]any{k, canonValue(t[k])}
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i := range t {
			out[i] = canonValue(t[i])
		}
		return out
	default:
		return v
	}
}

// decodeExact decodes JSON into an `any` value where JSON integers become int64
// (when they fit) and other numbers become float64. This preserves the integer
// precision that gcf-go can encode exactly, which a plain json.Unmarshal into
// `any` would discard by using float64 for every number.
func decodeExact(b []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	return exactNumbers(v), nil
}

func exactNumbers(v any) any {
	switch t := v.(type) {
	case map[string]any:
		for k, val := range t {
			t[k] = exactNumbers(val)
		}
		return t
	case []any:
		for i := range t {
			t[i] = exactNumbers(t[i])
		}
		return t
	case json.Number:
		if i, err := t.Int64(); err == nil {
			return i
		}
		if f, err := t.Float64(); err == nil {
			return f
		}
		// Not representable as int64 or float64: keep the text. gcf-go encodes it
		// as a string, which the losslessness check will reject, so JSON is kept.
		return t.String()
	default:
		return v
	}
}
