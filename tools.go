package mcpgrafana

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strings"

	"github.com/invopop/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	semconv "go.opentelemetry.io/otel/semconv/v1.40.0"
	"go.opentelemetry.io/otel/trace"
)

// Tool represents a tool definition and its handler function for the MCP server.
// It encapsulates both the tool metadata (name, description, schema) and the function that executes when the tool is called.
// The simplest way to create a Tool is to use MustTool for compile-time tool creation,
// or ConvertTool if you need runtime tool creation with proper error handling.
type Tool struct {
	Tool    *mcp.Tool
	Handler mcp.ToolHandler
}

// HardError wraps an error to indicate it should propagate as a JSON-RPC protocol
// error rather than being converted to CallToolResult with IsError=true.
// Use sparingly for non-recoverable failures (e.g., missing auth).
type HardError struct {
	Err error
}

func (e *HardError) Error() string {
	return e.Err.Error()
}

func (e *HardError) Unwrap() error {
	return e.Err
}

// Register adds the Tool to the given Server.
// It is a convenience method that calls Server.AddTool with the Tool's metadata and handler,
// allowing fluent tool registration in a single statement:
//
//	mcpgrafana.MustTool(name, description, toolHandler).Register(server)
func (t *Tool) Register(s *mcp.Server) {
	s.AddTool(t.Tool, t.Handler)
}

// ToolOption configures optional metadata (annotations) on a Tool. It mirrors
// the small subset of mark3labs' mcp.ToolOption surface this codebase used,
// since the official SDK exposes annotations as plain struct fields rather
// than functional options.
type ToolOption func(*mcp.Tool)

// ensureAnnotations returns t.Annotations, allocating it if necessary.
func ensureAnnotations(t *mcp.Tool) *mcp.ToolAnnotations {
	if t.Annotations == nil {
		t.Annotations = &mcp.ToolAnnotations{}
	}
	return t.Annotations
}

// WithTitleAnnotation sets the tool's human-readable title annotation.
func WithTitleAnnotation(title string) ToolOption {
	return func(t *mcp.Tool) { ensureAnnotations(t).Title = title }
}

// WithReadOnlyHintAnnotation sets whether the tool does not modify its environment.
func WithReadOnlyHintAnnotation(value bool) ToolOption {
	return func(t *mcp.Tool) { ensureAnnotations(t).ReadOnlyHint = value }
}

// WithDestructiveHintAnnotation sets whether the tool may perform destructive updates.
func WithDestructiveHintAnnotation(value bool) ToolOption {
	return func(t *mcp.Tool) { ensureAnnotations(t).DestructiveHint = &value }
}

// WithIdempotentHintAnnotation sets whether repeated calls with the same arguments are a no-op.
func WithIdempotentHintAnnotation(value bool) ToolOption {
	return func(t *mcp.Tool) { ensureAnnotations(t).IdempotentHint = value }
}

// WithOpenWorldHintAnnotation sets whether the tool interacts with an open world of external entities.
func WithOpenWorldHintAnnotation(value bool) ToolOption {
	return func(t *mcp.Tool) { ensureAnnotations(t).OpenWorldHint = &value }
}

// MustTool creates a new Tool from the given name, description, and toolHandler.
// It panics if the tool cannot be created, making it suitable for compile-time tool definitions where creation errors indicate programming mistakes.
func MustTool[T any, R any](
	name, description string,
	toolHandler ToolHandlerFunc[T, R],
	options ...ToolOption,
) Tool {
	tool, handler, err := ConvertTool(name, description, toolHandler, options...)
	if err != nil {
		panic(err)
	}
	return Tool{Tool: tool, Handler: handler}
}

// ToolHandlerFunc is the type of a handler function for a tool.
// T is the request parameter type (must be a struct with jsonschema tags), and R is the response type which can be a string, struct, or *mcp.CallToolResult.
type ToolHandlerFunc[T any, R any] = func(ctx context.Context, request T) (R, error)

// unmarshalWithTypeCoercion unmarshals JSON data into a target struct,
// automatically coercing common LLM type mismatches:
//   - string → integer (e.g., "42" → 42)
//   - string → []string (e.g., "value" → ["value"])
//
// Fast path: tries standard json.Unmarshal first (the common case — types already match).
// Only on failure does it apply coercions and retry.
func unmarshalWithIntConversion(data []byte, target any) error {
	// Fast path: standard unmarshal covers the common case with no reflection overhead.
	if err := json.Unmarshal(data, target); err == nil {
		return nil
	}

	targetType := reflect.TypeOf(target)
	if targetType.Kind() != reflect.Pointer || targetType.Elem().Kind() != reflect.Struct {
		return json.Unmarshal(data, target)
	}

	structType := targetType.Elem()
	intFields := collectIntFieldNames(structType)
	stringSliceFields := collectStringSliceFieldNames(structType)
	if len(intFields) == 0 && len(stringSliceFields) == 0 {
		return json.Unmarshal(data, target)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	changed := false
	for name := range intFields {
		v, ok := raw[name]
		if ok && len(v) >= 2 && v[0] == '"' && v[len(v)-1] == '"' {
			raw[name] = v[1 : len(v)-1] // strip quotes: "42" → 42
			changed = true
		}
	}
	for name := range stringSliceFields {
		v, ok := raw[name]
		if ok && len(v) >= 2 && v[0] == '"' {
			raw[name] = json.RawMessage("[" + string(v) + "]")
			changed = true
		}
	}
	if !changed {
		return json.Unmarshal(data, target)
	}

	fixed, err := json.Marshal(raw)
	if err != nil {
		return err
	}
	return json.Unmarshal(fixed, target)
}

// collectIntFieldNames uses reflect.VisibleFields to walk a struct type and returns
// the set of JSON field names that map to integer (or pointer-to-integer) types.
// reflect.VisibleFields follows the same promotion rules as encoding/json, correctly
// handling embedded structs, pointer embedding, and shadowed fields.
func collectIntFieldNames(structType reflect.Type) map[string]bool {
	fields := make(map[string]bool)
	for _, f := range reflect.VisibleFields(structType) {
		if !f.IsExported() {
			continue
		}
		ft := f.Type
		if ft.Kind() == reflect.Pointer {
			ft = ft.Elem()
		}
		if !isIntegerKind(ft.Kind()) {
			continue
		}
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if name == "-" {
			continue
		}
		if name == "" {
			name = f.Name // encoding/json falls back to the Go field name when no tag name is given
		}
		fields[name] = true
	}
	return fields
}

// isIntegerKind returns true if the given Kind represents an integer type.
func isIntegerKind(kind reflect.Kind) bool {
	switch kind {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return true
	}
	return false
}

// collectStringSliceFieldNames returns the set of JSON field names that map to
// []string (or pointer-to-[]string) types. This enables coercing a bare string
// into a single-element array, which LLMs frequently send for array-typed
// parameters. A pointer is dereferenced first, matching collectIntFieldNames:
// a parameter is declared as *[]string when an omitted list and an explicitly
// empty one mean different things, and that choice must not cost it the
// coercion.
func collectStringSliceFieldNames(structType reflect.Type) map[string]bool {
	fields := make(map[string]bool)
	for _, f := range reflect.VisibleFields(structType) {
		if !f.IsExported() {
			continue
		}
		ft := f.Type
		if ft.Kind() == reflect.Pointer {
			ft = ft.Elem()
		}
		if ft.Kind() == reflect.Slice && ft.Elem().Kind() == reflect.String {
			name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
			if name == "-" {
				continue
			}
			if name == "" {
				name = f.Name
			}
			fields[name] = true
		}
	}
	return fields
}

// toolArgumentsSchema mirrors the shape mark3labs' mcp.ToolArgumentsSchema
// marshaled to, so that existing tool schemas (and their tests) are
// unaffected by the SDK migration. Required is omitted when empty, matching
// the previous behaviour.
type toolArgumentsSchema struct {
	Type                 string         `json:"type"`
	Properties           map[string]any `json:"properties"`
	Required             []string       `json:"required,omitempty"`
	AdditionalProperties any            `json:"additionalProperties,omitempty"`
}

// NewToolResultText builds a *mcp.CallToolResult carrying a single text content item.
func NewToolResultText(text string) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}}
}

// NewToolResultError builds a *mcp.CallToolResult carrying a single text content
// item and IsError set, i.e. a tool-level error surfaced to the model rather
// than a JSON-RPC protocol error.
func NewToolResultError(text string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: text}},
		IsError: true,
	}
}

// ConvertTool converts a toolHandler function to an MCP Tool and mcp.ToolHandler.
// The toolHandler must accept a context.Context and a struct with jsonschema tags for parameter documentation.
// The struct fields define the tool's input schema, while the return value can be a string, struct, or *mcp.CallToolResult.
// This function automatically generates JSON schema from the struct type and wraps the handler with OpenTelemetry instrumentation.
func ConvertTool[T any, R any](name, description string, toolHandler ToolHandlerFunc[T, R], options ...ToolOption) (*mcp.Tool, mcp.ToolHandler, error) {
	handlerValue := reflect.ValueOf(toolHandler)
	handlerType := handlerValue.Type()
	if handlerType.Kind() != reflect.Func {
		return nil, nil, errors.New("tool handler must be a function")
	}
	if handlerType.NumIn() != 2 {
		return nil, nil, errors.New("tool handler must have 2 arguments")
	}
	if handlerType.NumOut() != 2 {
		return nil, nil, errors.New("tool handler must return 2 values")
	}
	if handlerType.In(0) != reflect.TypeOf((*context.Context)(nil)).Elem() {
		return nil, nil, errors.New("tool handler first argument must be context.Context")
	}
	// We no longer check the type of the first return value
	if handlerType.Out(1).Kind() != reflect.Interface {
		return nil, nil, errors.New("tool handler second return value must be error")
	}

	argType := handlerType.In(1)
	if argType.Kind() != reflect.Struct {
		return nil, nil, errors.New("tool handler second argument must be a struct")
	}

	// Built before the handler closure so it can validate incoming argument
	// keys against the same schema that is advertised to clients.
	jsonSchema := createJSONSchemaFromHandler(toolHandler)
	properties := make(map[string]any, jsonSchema.Properties.Len())
	for pair := jsonSchema.Properties.Oldest(); pair != nil; pair = pair.Next() {
		properties[pair.Key] = pair.Value
	}

	handler := func(ctx context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		config := GrafanaConfigFromContext(ctx)

		// Extract W3C trace context from request _meta if present
		ctx = extractTraceContext(ctx, request)

		// Create span following MCP semconv: "{method} {target}" with SpanKindServer
		ctx, span := otel.Tracer("mcp-grafana").Start(ctx,
			fmt.Sprintf("tools/call %s", name),
			trace.WithSpanKind(trace.SpanKindServer),
		)
		defer span.End()

		// Add semconv attributes
		span.SetAttributes(
			semconv.GenAIToolName(name),
			attribute.String("mcp.method.name", "tools/call"),
		)
		if request.Session != nil {
			span.SetAttributes(semconv.McpSessionID(request.Session.ID()))
		}

		// Arguments arrive as raw wire bytes; unmarshaling/validating them is
		// this wrapper's responsibility (see mcp.AddTool's documentation).
		var argBytes json.RawMessage
		if request.Params != nil {
			argBytes = request.Params.Arguments
		}
		if len(argBytes) == 0 {
			argBytes = json.RawMessage("{}")
		}

		// Add arguments as span attribute only if adding args to trace attributes is enabled
		if config.IncludeArgumentsInSpans {
			span.SetAttributes(attribute.String("gen_ai.tool.call.arguments", string(argBytes)))
		}

		var rawArgs map[string]any
		if err := json.Unmarshal(argBytes, &rawArgs); err != nil {
			// Non-object argument shapes are left to the decode path below to report.
			rawArgs = nil
		}

		// Reject unknown argument keys instead of silently dropping them: a typo'd
		// optional argument (e.g. "start_rfc3339" for "start_rfc_3339") would
		// otherwise leave the field zero-valued and the tool would answer a
		// different question than the caller asked. The error is returned as a
		// tool result (not a protocol error) so LLM callers can see it and retry.
		if unknown := unknownArguments(rawArgs, properties); len(unknown) > 0 {
			span.SetStatus(codes.Error, "unknown arguments")
			return NewToolResultError(unknownArgumentsError(unknown, properties)), nil
		}

		unmarshaledArgs := reflect.New(argType).Interface()
		if err := unmarshalWithIntConversion(argBytes, unmarshaledArgs); err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, "failed to unmarshal arguments")
			return nil, fmt.Errorf("unmarshal args: %s", err)
		}

		// Need to dereference the unmarshaled arguments
		of := reflect.ValueOf(unmarshaledArgs)
		if of.Kind() != reflect.Ptr || !of.Elem().CanInterface() {
			err := errors.New("arguments must be a struct")
			span.RecordError(err)
			span.SetStatus(codes.Error, "invalid arguments structure")
			return nil, err
		}

		// Pass the instrumented context to the tool handler
		args := []reflect.Value{reflect.ValueOf(ctx), of.Elem()}

		output := handlerValue.Call(args)
		if len(output) != 2 {
			err := errors.New("tool handler must return 2 values")
			span.RecordError(err)
			span.SetStatus(codes.Error, "invalid tool handler return")
			return nil, err
		}
		if !output[0].CanInterface() {
			err := errors.New("tool handler first return value must be interfaceable")
			span.RecordError(err)
			span.SetStatus(codes.Error, "tool handler return value not interfaceable")
			return nil, err
		}

		// Handle the error return value first
		var handlerErr error
		var ok bool
		if output[1].Kind() == reflect.Interface && !output[1].IsNil() {
			handlerErr, ok = output[1].Interface().(error)
			if !ok {
				err := errors.New("tool handler second return value must be error")
				span.RecordError(err)
				span.SetStatus(codes.Error, "invalid error return type")
				return nil, err
			}
		}

		// If there's an error, record it and return
		if handlerErr != nil {
			span.RecordError(handlerErr)
			span.SetStatus(codes.Error, handlerErr.Error())
			span.SetAttributes(semconv.ErrorType(handlerErr))
			var hardErr *HardError
			if errors.As(handlerErr, &hardErr) {
				return nil, hardErr.Err
			}
			return NewToolResultError(handlerErr.Error()), nil
		}

		// Tool execution completed successfully
		span.SetStatus(codes.Ok, "tool execution completed")

		// Check if the first return value is nil (only for pointer, interface, map, etc.)
		isNilable := output[0].Kind() == reflect.Ptr ||
			output[0].Kind() == reflect.Interface ||
			output[0].Kind() == reflect.Map ||
			output[0].Kind() == reflect.Slice ||
			output[0].Kind() == reflect.Chan ||
			output[0].Kind() == reflect.Func

		if isNilable && output[0].IsNil() {
			// Return an empty text result instead of nil. A nil slice/map is a
			// valid "no results" response.
			return NewToolResultText("null"), nil
		}

		returnVal := output[0].Interface()
		returnType := output[0].Type()

		// Case 1: Already a *mcp.CallToolResult
		if callResult, ok := returnVal.(*mcp.CallToolResult); ok {
			return callResult, nil
		}

		// Case 2: An mcp.CallToolResult (not a pointer)
		if returnType.ConvertibleTo(reflect.TypeOf(mcp.CallToolResult{})) {
			callResult := returnVal.(mcp.CallToolResult)
			return &callResult, nil
		}

		// Case 3: String or *string
		if str, ok := returnVal.(string); ok {
			return NewToolResultText(str), nil
		}

		if strPtr, ok := returnVal.(*string); ok {
			if strPtr == nil {
				return NewToolResultText(""), nil
			}
			return NewToolResultText(*strPtr), nil
		}

		// Case 4: Any other type - marshal to JSON
		returnBytes, err := json.Marshal(returnVal)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal return value: %s", err)
		}

		return NewToolResultText(string(returnBytes)), nil
	}

	// Marshal the schema ourselves (rather than relying on the SDK's own
	// jsonschema-go-based inference) so we keep invopop/jsonschema and this
	// package's existing validation/coercion behaviour unchanged.
	// Tool.InputSchema accepts any value that marshals to valid JSON Schema.
	argumentsSchema := toolArgumentsSchema{
		Type:                 jsonSchema.Type,
		Properties:           properties,
		Required:             jsonSchema.Required,
		AdditionalProperties: false,
	}

	schemaBytes, err := json.Marshal(argumentsSchema)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to marshal input schema: %w", err)
	}

	// Validate that no bare boolean schemas slipped through. The Mapper on the
	// reflector handles interface{} types, but this check catches anything it
	// misses and prevents future regressions. MustTool will panic at init time
	// if this fails, making it impossible to register a tool with bare booleans.
	if err := validateNoBooleanSchemas(name, schemaBytes); err != nil {
		return nil, nil, err
	}

	t := &mcp.Tool{
		Name:        name,
		Description: description,
		InputSchema: json.RawMessage(schemaBytes),
	}
	for _, option := range options {
		option(t)
	}
	return t, handler, nil
}

// extractTraceContext checks the request's _meta for W3C trace context headers
// (traceparent/tracestate) and returns a context with the extracted span context
// so that the tool span becomes a child of the caller's trace.
func extractTraceContext(ctx context.Context, request *mcp.CallToolRequest) context.Context {
	if request.Params == nil {
		return ctx
	}
	fields := request.Params.Meta
	if len(fields) == 0 {
		return ctx
	}
	// Build a minimal carrier from _meta fields
	carrier := make(http.Header)
	if tp, ok := fields["traceparent"].(string); ok && tp != "" {
		carrier.Set("traceparent", tp)
	}
	if ts, ok := fields["tracestate"].(string); ok && ts != "" {
		carrier.Set("tracestate", ts)
	}
	if len(carrier) == 0 {
		return ctx
	}
	prop := propagation.TraceContext{}
	return prop.Extract(ctx, propagation.HeaderCarrier(carrier))
}

// Creates a full JSON schema from a user provided handler by introspecting the arguments
func createJSONSchemaFromHandler(handler any) *jsonschema.Schema {
	handlerValue := reflect.ValueOf(handler)
	handlerType := handlerValue.Type()
	argumentType := handlerType.In(1)
	inputSchema := jsonSchemaReflector.ReflectFromType(argumentType)
	return inputSchema
}

var jsonSchemaReflector = jsonschema.Reflector{
	BaseSchemaID:               "",
	Anonymous:                  true,
	AssignAnchor:               false,
	AllowAdditionalProperties:  true,
	RequiredFromJSONSchemaTags: true,
	DoNotReference:             true,
	ExpandedStruct:             true,
	FieldNameTag:               "",
	IgnoredTypes:               nil,
	Lookup:                     nil,
	// Mapper handles Go interface{}/any types which the jsonschema library
	// would otherwise emit as bare boolean `true` schemas. Some LLM providers
	// (e.g. Fireworks AI) reject bare boolean schemas. We map them to an empty
	// object schema {} instead. The non-nil Extras field prevents the library's
	// MarshalJSON from collapsing the empty schema back to `true`.
	// See: https://github.com/grafana/mcp-grafana/issues/594
	Mapper: func(t reflect.Type) *jsonschema.Schema {
		if t.Kind() == reflect.Interface {
			return &jsonschema.Schema{Extras: map[string]any{}}
		}
		return nil
	},
	Namer:            nil,
	KeyNamer:         nil,
	AdditionalFields: nil,
	CommentMap:       nil,
}

// JSON Schema keywords whose values are single sub-schemas.
var schemaValuedKeys = []string{
	"items", "additionalProperties", "not",
	"if", "then", "else",
	"contains", "propertyNames",
	"unevaluatedItems", "unevaluatedProperties",
}

// JSON Schema keywords whose values are maps of sub-schemas.
var schemaMapKeys = []string{
	"properties", "patternProperties",
	"$defs", "definitions",
}

// JSON Schema keywords whose values are arrays of sub-schemas.
var schemaArrayKeys = []string{
	"allOf", "anyOf", "oneOf", "prefixItems",
}

// validateNoBooleanSchemas checks that a marshaled JSON Schema contains no bare
// boolean values (true/false) in positions where sub-schemas are expected. Bare
// booleans typically come from Go interface{} types and break some LLM providers.
// This validation runs at tool creation time so that MustTool panics immediately
// if a tool's schema contains bare booleans, preventing silent compatibility issues.
func validateNoBooleanSchemas(toolName string, data []byte) error {
	var raw any
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	return checkSchemaNode(toolName, raw, "$")
}

// checkSchemaNode recursively walks a JSON value representing a JSON Schema and
// returns an error if any bare boolean values appear in sub-schema positions.
func checkSchemaNode(toolName string, v any, path string) error {
	obj, ok := v.(map[string]any)
	if !ok {
		return nil
	}

	// Check single-schema-valued keys
	for _, key := range schemaValuedKeys {
		if val, exists := obj[key]; exists {
			if b, ok := val.(bool); ok {
				// additionalProperties: false is valid, universally supported
				// (OpenAI structured outputs even requires it), and emitted on
				// purpose by ConvertTool. Only bare `true` — typically from
				// interface{} fields — is known to break providers.
				if key == "additionalProperties" && !b {
					continue
				}
				return fmt.Errorf(
					"tool %q has bare boolean schema (%v) at %s.%s; "+
						"this is likely caused by an interface{}/any field — "+
						"add the type to the jsonschema reflector Mapper in tools.go",
					toolName, b, path, key,
				)
			}
		}
	}

	// Check schema-map-valued keys
	for _, key := range schemaMapKeys {
		if mapVal, ok := obj[key].(map[string]any); ok {
			for k, v := range mapVal {
				if b, ok := v.(bool); ok {
					return fmt.Errorf(
						"tool %q has bare boolean schema (%v) at %s.%s.%s; "+
							"this is likely caused by an interface{}/any field — "+
							"add the type to the jsonschema reflector Mapper in tools.go",
						toolName, b, path, key, k,
					)
				}
			}
		}
	}

	// Check schema-array-valued keys
	for _, key := range schemaArrayKeys {
		if arrVal, ok := obj[key].([]any); ok {
			for i, v := range arrVal {
				if b, ok := v.(bool); ok {
					return fmt.Errorf(
						"tool %q has bare boolean schema (%v) at %s.%s[%d]; "+
							"this is likely caused by an interface{}/any field — "+
							"add the type to the jsonschema reflector Mapper in tools.go",
						toolName, b, path, key, i,
					)
				}
			}
		}
	}

	// Recurse into all nested objects and arrays
	for key, val := range obj {
		switch v := val.(type) {
		case map[string]any:
			if err := checkSchemaNode(toolName, v, path+"."+key); err != nil {
				return err
			}
		case []any:
			for _, item := range v {
				if err := checkSchemaNode(toolName, item, path+"."+key); err != nil {
					return err
				}
			}
		}
	}

	return nil
}
