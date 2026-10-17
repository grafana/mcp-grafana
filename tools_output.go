package mcpgrafana

import (
	"context"
	"encoding"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	gjsonschema "github.com/google/jsonschema-go/jsonschema"
	"github.com/invopop/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"go.opentelemetry.io/otel/trace"
)

// outputSpec describes the structured output of a tool: the advertised
// outputSchema, the resolved form used to check results against it, and
// whether results are wrapped as {"result": v} because they are not JSON
// objects themselves (MCP requires structuredContent to be an object).
type outputSpec struct {
	schema   json.RawMessage
	resolved *gjsonschema.Resolved
	wrap     bool
	elem     reflect.Type // non-nil when R is a pointer; used to replace a typed nil
}

var (
	callToolResultType = reflect.TypeFor[mcp.CallToolResult]()
	jsonMarshalerType  = reflect.TypeFor[json.Marshaler]()
	textMarshalerType  = reflect.TypeFor[encoding.TextMarshaler]()
	customSchemaType   = reflect.TypeFor[interface{ JSONSchema() *jsonschema.Schema }]()
)

// newOutputSpec builds the structured output description for a handler
// returning t. It returns nil for types with no useful schema — strings,
// interfaces and *mcp.CallToolResult — whose results stay text-only.
func newOutputSpec(name string, t reflect.Type) (*outputSpec, error) {
	base := t
	if base.Kind() == reflect.Pointer {
		base = base.Elem()
	}
	if base.Kind() == reflect.Interface || base.Kind() == reflect.String || base == callToolResultType {
		return nil, nil
	}

	var root map[string]any
	if err := remarshal(outputSchemaReflector.ReflectFromType(t), &root); err != nil {
		return nil, fmt.Errorf("tool %q: reflect output schema: %w", name, err)
	}
	delete(root, "$schema")
	delete(root, "$id")
	defs, _ := root["$defs"].(map[string]any)
	delete(root, "$defs")

	// A struct reflects as {"$ref": "#/$defs/T"}. Inline it so the schema's
	// root is the object itself, which is what clients expect to see.
	wrap := true
	if ref, ok := root["$ref"].(string); ok && len(root) == 1 {
		defName := strings.TrimPrefix(ref, "#/$defs/")
		if def, ok := defs[defName].(map[string]any); ok && def["type"] == "object" {
			root = def
			delete(defs, defName)
			// Recursive types still reference their own definition.
			if b, _ := json.Marshal(map[string]any{"r": root, "d": defs}); strings.Contains(string(b), `"`+ref+`"`) {
				var copied map[string]any
				_ = remarshal(def, &copied)
				defs[defName] = copied
			}
			wrap = false
		}
	}
	// Nil pointers, slices and maps in fields without omitempty marshal as
	// null, which reflection does not account for.
	markNullableFields(t, root, defs, map[string]bool{})
	if wrap {
		root = map[string]any{
			"type":       "object",
			"properties": map[string]any{"result": root},
			"required":   []any{"result"},
		}
	}
	if len(defs) > 0 {
		root["$defs"] = defs
	}

	schema, err := json.Marshal(root)
	if err != nil {
		return nil, fmt.Errorf("tool %q: marshal output schema: %w", name, err)
	}
	if err := validateNoBooleanSchemas(name, schema); err != nil {
		return nil, err
	}
	var s gjsonschema.Schema
	if err := json.Unmarshal(schema, &s); err != nil {
		return nil, fmt.Errorf("tool %q: parse output schema: %w", name, err)
	}
	resolved, err := s.Resolve(nil)
	if err != nil {
		return nil, fmt.Errorf("tool %q: resolve output schema: %w", name, err)
	}

	spec := &outputSpec{schema: schema, resolved: resolved, wrap: wrap}
	if t.Kind() == reflect.Pointer {
		spec.elem = t.Elem()
	}
	// Check the schema against values populated to each depth in turn, so
	// every level is seen both set and nil. A schema that disagrees with how
	// the type marshals then fails at startup (and in every test run) rather
	// than in a client.
	for maxDepth := range sampleDepth + 1 {
		b, err := spec.encode(sampleValue(t, 0, maxDepth))
		if err != nil {
			continue // custom marshallers may reject synthetic data
		}
		if err := spec.validate(b); err != nil {
			return nil, fmt.Errorf("tool %q: output schema does not match %s: %w", name, t, err)
		}
	}
	return spec, nil
}

// sampleDepth bounds how deep sampleValue populates, which also stops it
// looping on recursive types.
const sampleDepth = 6

// sampleValue returns a value of t with every pointer set, and every slice
// and string-keyed map holding one element, down to maxDepth; below that
// values are left zero (nil).
func sampleValue(t reflect.Type, depth, maxDepth int) reflect.Value {
	v := reflect.New(t).Elem()
	if depth >= maxDepth {
		return v
	}
	switch t.Kind() {
	case reflect.Pointer:
		p := reflect.New(t.Elem())
		p.Elem().Set(sampleValue(t.Elem(), depth+1, maxDepth))
		v.Set(p)
	case reflect.Slice:
		s := reflect.MakeSlice(t, 1, 1)
		s.Index(0).Set(sampleValue(t.Elem(), depth+1, maxDepth))
		v.Set(s)
	case reflect.Map:
		if t.Key().Kind() == reflect.String {
			m := reflect.MakeMap(t)
			m.SetMapIndex(reflect.ValueOf("k").Convert(t.Key()), sampleValue(t.Elem(), depth+1, maxDepth))
			v.Set(m)
		}
	case reflect.Struct:
		for i := range t.NumField() {
			if f := v.Field(i); f.CanSet() {
				f.Set(sampleValue(t.Field(i).Type, depth+1, maxDepth))
			}
		}
	}
	return v
}

// result builds the tool result for a handler's return value: the JSON as
// structuredContent, and the same JSON as text for clients that only read
// content. A result that does not match the advertised schema is still
// returned, since most clients can use it, but is logged so the schema can be
// fixed: strict clients (the TypeScript and Python SDKs) will reject it.
func (o *outputSpec) result(ctx context.Context, name string, v reflect.Value) (*mcp.CallToolResult, error) {
	b, err := o.encode(v)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal return value: %s", err)
	}
	if err := o.validate(b); err != nil {
		LoggerFromContext(ctx).WarnContext(ctx, "tool result does not match its output schema", "tool", name, "error", err)
		trace.SpanFromContext(ctx).AddEvent("output schema mismatch")
	}
	return &mcp.CallToolResult{
		Content:           []mcp.Content{&mcp.TextContent{Text: string(b)}},
		StructuredContent: json.RawMessage(b),
	}, nil
}

// encode marshals a handler's return value as its structuredContent.
func (o *outputSpec) encode(v reflect.Value) ([]byte, error) {
	if v.Kind() == reflect.Pointer && v.IsNil() && o.elem != nil {
		v = reflect.New(o.elem).Elem()
	}
	out := v.Interface()
	if o.wrap {
		// Return [] / {} rather than null so callers can iterate directly.
		switch {
		case v.Kind() == reflect.Slice && v.IsNil():
			out = reflect.MakeSlice(v.Type(), 0, 0).Interface()
		case v.Kind() == reflect.Map && v.IsNil():
			out = reflect.MakeMap(v.Type()).Interface()
		}
		out = map[string]any{"result": out}
	}
	return json.Marshal(out)
}

func (o *outputSpec) validate(b []byte) error {
	var instance any
	if err := json.Unmarshal(b, &instance); err != nil {
		return err
	}
	return o.resolved.Validate(instance)
}

// outputSchemaReflector differs from the input reflector in that fields
// without omitempty are required (encoding/json always emits them) and
// definitions are referenced, so recursive types such as notification policy
// routes terminate.
var outputSchemaReflector = jsonschema.Reflector{
	AllowAdditionalProperties: true,
	Mapper:                    outputTypeMapper,
}

// outputTypeMapper describes types whose JSON is not their struct shape.
func outputTypeMapper(t reflect.Type) *jsonschema.Schema {
	if t.Implements(customSchemaType) || reflect.PointerTo(t).Implements(customSchemaType) {
		return nil
	}
	implements := func(i reflect.Type) bool { return t.Implements(i) || reflect.PointerTo(t).Implements(i) }
	switch {
	case implements(textMarshalerType):
		// time.Time, strfmt.DateTime and friends marshal as strings.
		return &jsonschema.Schema{Type: "string"}
	case implements(jsonMarshalerType), t.Kind() == reflect.Interface:
		// A custom marshaller can emit anything; say so rather than guess.
		return anyJSONSchema()
	}
	return nil
}

// anyJSONSchema is a schema accepting every JSON value. It is spelled out as
// a type list rather than `true` because some clients reject bare boolean
// schemas (see validateNoBooleanSchemas).
func anyJSONSchema() *jsonschema.Schema {
	return &jsonschema.Schema{Extras: map[string]any{
		"type": []string{"string", "number", "integer", "boolean", "object", "array", "null"},
	}}
}

// markNullableFields walks Go type t alongside its schema node and allows
// null on every field encoding/json can emit as null: a pointer, slice, map or
// interface without omitempty. seen holds visited $defs names, so recursive
// types terminate.
func markNullableFields(t reflect.Type, node map[string]any, defs map[string]any, seen map[string]bool) {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if ref, ok := node["$ref"].(string); ok {
		name := strings.TrimPrefix(ref, "#/$defs/")
		if seen[name] {
			return
		}
		seen[name] = true
		def, ok := defs[name].(map[string]any)
		if !ok {
			return
		}
		node = def
	}
	switch t.Kind() {
	case reflect.Slice, reflect.Array:
		markNullableElem(t.Elem(), node, "items", defs, seen)
	case reflect.Map:
		markNullableElem(t.Elem(), node, "additionalProperties", defs, seen)
	case reflect.Struct:
		if _, ok := node["properties"].(map[string]any); !ok {
			return // described by the Mapper, not by its fields
		}
		markNullableStructFields(t, node, false, defs, seen)
	}
}

// viaPointer reports that t is embedded through a pointer: when that pointer
// is nil, encoding/json omits all of t's fields, so none can be required.
// markNullableElem handles the element schema of a slice or map, at node[key].
func markNullableElem(elem reflect.Type, node map[string]any, key string, defs map[string]any, seen map[string]bool) {
	schema, ok := node[key].(map[string]any)
	if !ok {
		return
	}
	markNullableFields(elem, schema, defs, seen)
	if isNilable(elem) {
		node[key] = nullable(schema)
	}
}

func isNilable(t reflect.Type) bool {
	switch t.Kind() {
	case reflect.Pointer, reflect.Slice, reflect.Map, reflect.Interface:
		return true
	}
	return false
}

func markNullableStructFields(t reflect.Type, node map[string]any, viaPointer bool, defs map[string]any, seen map[string]bool) {
	props := node["properties"].(map[string]any)
	for i := range t.NumField() {
		f := t.Field(i)
		tag := f.Tag.Get("json")
		if tag == "-" || (!f.IsExported() && !f.Anonymous) {
			continue
		}
		name, opts, _ := strings.Cut(tag, ",")
		if f.Anonymous && name == "" {
			ft := f.Type
			for ft.Kind() == reflect.Pointer {
				ft = ft.Elem()
			}
			if ft.Kind() == reflect.Struct {
				markNullableStructFields(ft, node, viaPointer || f.Type.Kind() == reflect.Pointer, defs, seen)
				continue
			}
		}
		if name == "" {
			name = f.Name
		}
		prop, ok := props[name].(map[string]any)
		if !ok {
			continue
		}
		markNullableFields(f.Type, prop, defs, seen)
		if viaPointer {
			unrequire(node, name)
		}
		omitted := strings.Contains(","+opts+",", ",omitempty,") || strings.Contains(","+opts+",", ",omitzero,")
		if isNilable(f.Type) && !omitted {
			props[name] = nullable(prop)
		}
	}
}

func unrequire(node map[string]any, name string) {
	required, _ := node["required"].([]any)
	for i, r := range required {
		if r == name {
			node["required"] = append(required[:i:i], required[i+1:]...)
			return
		}
	}
}

// nullable returns schema with null added to its accepted types. A $ref
// cannot carry a type of its own, so it becomes anyOf [$ref, null].
func nullable(schema map[string]any) map[string]any {
	if _, ok := schema["$ref"]; ok {
		return map[string]any{"anyOf": []any{schema, map[string]any{"type": "null"}}}
	}
	makeNullable(schema)
	return schema
}

// makeNullable adds null to a schema node's accepted types. Nodes without a
// type (a $ref, a composition) are left alone: their targets carry it.
func makeNullable(node map[string]any) {
	switch typ := node["type"].(type) {
	case string:
		if typ != "null" {
			node["type"] = []any{typ, "null"}
		}
	case []any:
		for _, t := range typ {
			if t == "null" {
				return
			}
		}
		node["type"] = append(typ, "null")
	default:
		return
	}
	if enum, ok := node["enum"].([]any); ok {
		node["enum"] = append(enum, nil)
	}
}

func remarshal(from, to any) error {
	b, err := json.Marshal(from)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, to)
}
