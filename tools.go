package mcpgrafana

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"

	"github.com/invopop/jsonschema"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
)

// Tool is a struct that represents a tool definition and the function used
// to handle tool calls.
//
// The simplest way to create a Tool is to use `MustTool`, or `ConvertTool`
// if you wish to create tools at runtime and need to handle errors without
// panicking.
type Tool struct {
	Tool    mcp.Tool
	Handler server.ToolHandlerFunc
}

// Register adds the Tool to the given MCPServer.
//
// It is a convenience method that calls `server.MCPServer.Register` with the
// Tool's Tool and Handler fields, allowing you to add the tool in a single
// statement:
//
//	mcpgrafana.MustTool(name, description, toolHandler).Register(server)
func (t *Tool) Register(mcp *server.MCPServer) {
	mcp.AddTool(t.Tool, t.Handler)
}

// MustTool creates a new Tool from the given name, description, and toolHandler.
// It panics if the tool cannot be created.
func MustTool[T any, R any](
	name, description string,
	toolHandler ToolHandlerFunc[T, R],
	options ...mcp.ToolOption,
) Tool {
	tool, handler, err := ConvertTool(name, description, toolHandler, options...)
	if err != nil {
		panic(err)
	}
	return Tool{Tool: tool, Handler: handler}
}

// ToolHandlerFunc is the type of a handler function for a tool.
type ToolHandlerFunc[T any, R any] = func(ctx context.Context, request T) (R, error)

// ConvertTool converts a toolHandler function to a Tool and ToolHandlerFunc.
//
// The toolHandler function must have two arguments: a context.Context and a struct
// to be used as the parameters for the tool. The second argument must not be a pointer,
// should be marshalable to JSON, and the fields should have a `jsonschema` tag with the
// description of the parameter.
func ConvertTool[T any, R any](name, description string, toolHandler ToolHandlerFunc[T, R], options ...mcp.ToolOption) (mcp.Tool, server.ToolHandlerFunc, error) {
	zero := mcp.Tool{}
	handlerValue := reflect.ValueOf(toolHandler)
	handlerType := handlerValue.Type()
	if handlerType.Kind() != reflect.Func {
		return zero, nil, errors.New("tool handler must be a function")
	}
	if handlerType.NumIn() != 2 {
		return zero, nil, errors.New("tool handler must have 2 arguments")
	}
	if handlerType.NumOut() != 2 {
		return zero, nil, errors.New("tool handler must return 2 values")
	}
	if handlerType.In(0) != reflect.TypeOf((*context.Context)(nil)).Elem() {
		return zero, nil, errors.New("tool handler first argument must be context.Context")
	}
	// We no longer check the type of the first return value
	if handlerType.Out(1).Kind() != reflect.Interface {
		return zero, nil, errors.New("tool handler second return value must be error")
	}

	argType := handlerType.In(1)
	if argType.Kind() != reflect.Struct {
		return zero, nil, errors.New("tool handler second argument must be a struct")
	}

	handler := func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		// Create OpenTelemetry span for tool execution (no-op when no exporter configured)
		config := GrafanaConfigFromContext(ctx)
		ctx, span := otel.Tracer("mcp-grafana").Start(ctx, fmt.Sprintf("mcp.tool.%s", name))
		defer span.End()
		
		// Add tool metadata as span attributes
		span.SetAttributes(
			attribute.String("mcp.tool.name", name),
			attribute.String("mcp.tool.description", description),
		)

		argBytes, err := json.Marshal(request.Params.Arguments)
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, "failed to marshal arguments")
			return nil, fmt.Errorf("marshal args: %w", err)
		}
		
		   // Add arguments as span attribute only if adding args to trace attributes is enabled
		if config.IncludeArgumentsInSpans {
			span.SetAttributes(attribute.String("mcp.tool.arguments", string(argBytes)))
		}

		unmarshaledArgs := reflect.New(argType).Interface()
		if err := json.Unmarshal(argBytes, unmarshaledArgs); err != nil {
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
			return nil, handlerErr
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
			return nil, nil
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
			if str == "" {
				return nil, nil
			}
			return mcp.NewToolResultText(str), nil
		}

		if strPtr, ok := returnVal.(*string); ok {
			if strPtr == nil || *strPtr == "" {
				return nil, nil
			}
			return mcp.NewToolResultText(*strPtr), nil
		}

		// Case 4: Any other type - marshal to JSON
		returnBytes, err := json.Marshal(returnVal)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal return value: %s", err)
		}

		return mcp.NewToolResultText(string(returnBytes)), nil
	}

	jsonSchema := createJSONSchemaFromHandler(toolHandler)
	properties := make(map[string]any, jsonSchema.Properties.Len())
	for pair := jsonSchema.Properties.Oldest(); pair != nil; pair = pair.Next() {
		properties[pair.Key] = pair.Value
	}
	inputSchema := mcp.ToolInputSchema{
		Type:       jsonSchema.Type,
		Properties: properties,
		Required:   jsonSchema.Required,
	}

	t := mcp.Tool{
		Name:        name,
		Description: description,
		InputSchema: inputSchema,
	}
	for _, option := range options {
		option(&t)
	}
	return t, handler, nil
}

// Creates a full JSON schema from a user provided handler by introspecting the arguments
func createJSONSchemaFromHandler(handler any) *jsonschema.Schema {
	handlerValue := reflect.ValueOf(handler)
	handlerType := handlerValue.Type()
	argumentType := handlerType.In(1)
	inputSchema := jsonSchemaReflector.ReflectFromType(argumentType)
	return inputSchema
}

var (
	jsonSchemaReflector = jsonschema.Reflector{
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
		Mapper:                     nil,
		Namer:                      nil,
		KeyNamer:                   nil,
		AdditionalFields:           nil,
		CommentMap:                 nil,
	}
)
