package mcpgrafana

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"reflect"
	"slices"

	"github.com/invopop/jsonschema"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	semconv "go.opentelemetry.io/otel/semconv/v1.39.0"
	"go.opentelemetry.io/otel/trace"
)

// Tool represents a tool definition and its handler function for the MCP server.
// It encapsulates both the tool metadata (name, description, schema) and the function that executes when the tool is called.
// The simplest way to create a Tool is to use MustTool for compile-time tool creation,
// or ConvertTool if you need runtime tool creation with proper error handling.
type Tool struct {
	Tool    mcp.Tool
	Handler server.ToolHandlerFunc
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

// Register adds the Tool to the given MCPServer.
// It is a convenience method that calls server.MCPServer.AddTool with the Tool's metadata and handler,
// allowing fluent tool registration in a single statement:
// mcpgrafana.MustTool(name, description, toolHandler).Register(server)
func (t *Tool) Register(mcp *server.MCPServer) {
	mcp.AddTool(t.Tool, t.Handler)
}

// MustTool creates a new Tool from the given name, description, and toolHandler.
// It panics if the tool cannot be created, making it suitable for compile-time tool definitions where creation errors indicate programming mistakes.
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
// T is the request parameter type (must be a struct with jsonschema tags), and R is the response type which can be a string, struct, or *mcp.CallToolResult.
type ToolHandlerFunc[T any, R any] = func(ctx context.Context, request T) (R, error)

// ConvertTool converts a toolHandler function to an MCP Tool and ToolHandlerFunc.
// The toolHandler must accept a context.Context and a struct with jsonschema tags for parameter documentation.
// The struct fields define the tool's input schema, while the return value can be a string, struct, or *mcp.CallToolResult.
// This function automatically generates JSON schema from the struct type and wraps the handler with OpenTelemetry instrumentation.
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
		if session := server.ClientSessionFromContext(ctx); session != nil {
			span.SetAttributes(semconv.McpSessionID(session.SessionID()))
		}

		argBytes, err := json.Marshal(request.Params.Arguments)
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, "failed to marshal arguments")
			return nil, fmt.Errorf("marshal args: %w", err)
		}

		// Add arguments as span attribute only if adding args to trace attributes is enabled
		if config.IncludeArgumentsInSpans {
			span.SetAttributes(attribute.String("gen_ai.tool.call.arguments", string(argBytes)))
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
			span.SetAttributes(semconv.ErrorType(handlerErr))
			var hardErr *HardError
			if errors.As(handlerErr, &hardErr) {
				return nil, hardErr.Err
			}
			return &mcp.CallToolResult{
				Content: []mcp.Content{
					mcp.TextContent{
						Type: "text",
						Text: handlerErr.Error(),
					},
				},
				IsError: true,
			}, nil
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
	// Use RawInputSchema with ToolArgumentsSchema to work around a Go limitation where type aliases
	// don't inherit custom MarshalJSON methods. This ensures empty properties are included in the schema.
	argumentsSchema := mcp.ToolArgumentsSchema{
		Type:       jsonSchema.Type,
		Properties: properties,
		Required:   jsonSchema.Required,
	}

	// Marshal the schema to preserve empty properties
	schemaBytes, err := json.Marshal(argumentsSchema)
	if err != nil {
		return zero, nil, fmt.Errorf("failed to marshal input schema: %w", err)
	}

	t := mcp.Tool{
		Name:           name,
		Description:    description,
		RawInputSchema: schemaBytes,
	}
	for _, option := range options {
		option(&t)
	}
	return t, handler, nil
}

// extractTraceContext checks the request's _meta for W3C trace context headers
// (traceparent/tracestate) and returns a context with the extracted span context
// so that the tool span becomes a child of the caller's trace.
func extractTraceContext(ctx context.Context, request mcp.CallToolRequest) context.Context {
	if request.Params.Meta == nil {
		return ctx
	}
	fields := request.Params.Meta.AdditionalFields
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

// grafanaHTTPClient is a generic http client for raw requests to grafana api
type grafanaHTTPClient struct {
	Client *http.Client
	URL string
}

func newGrafanaHttpClient(cfg GrafanaConfig) (*grafanaHTTPClient, error) {
	transport, err := BuildTransport(&cfg, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create custom transport: %w", err)
	}
	transport = NewAuthRoundTripper(transport, cfg.AccessToken, cfg.IDToken, cfg.APIKey, cfg.BasicAuth)
	transport = NewOrgIDRoundTripper(transport, cfg.OrgID)
	transport = NewUserAgentTransport(transport)

	client := &http.Client{
		Transport: transport,
	}

	return &grafanaHTTPClient{
		URL:    cfg.URL,
		Client: client,
	}, nil
}

// makeRequest is a helper method to make HTTP requests and handle common response patterns
func (c *grafanaHTTPClient) makeRequest(ctx context.Context, method, path string, body []byte) ([]byte, error) {
	var req *http.Request
	var err error

	if body != nil {
		req, err = http.NewRequestWithContext(ctx, method, c.URL+path, bytes.NewBuffer(body))
		if err != nil {
			return nil, fmt.Errorf("creating request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
	} else {
		req, err = http.NewRequestWithContext(ctx, method, c.URL+path, nil)
		if err != nil {
			return nil, fmt.Errorf("creating request: %w", err)
		}
	}

	response, err := c.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("executing request: %w", err)
	}
	defer func() {
		_ = response.Body.Close() //nolint:errcheck
	}()

	// Check for non-200 status code
	if response.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(response.Body) // Read full body on error
		return nil, fmt.Errorf("API request returned status code %d: %s", response.StatusCode, string(bodyBytes))
	}

	// Read the response body with a limit to prevent memory issues
	reader := io.LimitReader(response.Body, 1024*1024*48) // 48MB limit
	buf, err := io.ReadAll(reader)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	// Check if the response is empty
	if len(buf) == 0 {
		return nil, fmt.Errorf("empty response from API")
	}

	// Trim any whitespace that might cause JSON parsing issues
	return bytes.TrimSpace(buf), nil
}

type Plugin struct {
	Name    string `json:"name"`
	ID      string `json:"id"`
	Type    string `json:"type"`
	Enabled bool   `json:"enabled"`
}
// GetEnabledPlugins returns enabled plugins accessible to the authenticated session.
func (gc *grafanaHTTPClient) GetEnabledPlugins(ctx context.Context) ([]Plugin, error) {
	response, err := gc.makeRequest(ctx, "GET", "/api/plugins?enabled=1", nil)
	if err != nil {
		return nil, err
	}
	var result []Plugin
	// TODO: apply unmarshalling with limit message when https://github.com/grafana/mcp-grafana/pull/622 is merged
	err = json.Unmarshal(response, &result)
	if err != nil {
		return nil, fmt.Errorf("unmarshalling response : %w", err)
	}
	return result, nil
}

// discoverTools discovers an authenticated session's accessible resources of the grafana instance 
// returns the discovered tools
// A discoverTools is safe to be called concurrently 
func (tm *ToolManager) discoverTools(ctx context.Context) ([]server.ServerTool, error) {
	grafana := GrafanaClientFromContext(ctx)
	discoveredSources, err := grafana.Datasources.GetDataSources()

	if err != nil {
		return nil, err
	}

	tools := make([]server.ServerTool, 0)
	categoryIncluded := map[string]bool{}

	for _, datasource := range discoveredSources.Payload {
		tools = addCategoryTools(datasource.Type, categoryIncluded, tm, tools)
	}

	cfg := GrafanaConfigFromContext(ctx)
	httpClient, err := newGrafanaHttpClient(cfg)
	if err != nil {
		return nil, err
	}

	plugins, err := httpClient.GetEnabledPlugins(ctx)
	if err != nil {
		return nil, err
	}

	enabledPlugins := map[string]bool{}

	for _, plugin := range plugins {
		enabledPlugins[plugin.ID] = true
	}

	for pluginId, categories := range tm.pluginCategories {
		tools = maybeAddPluginTools(pluginId, categories, categoryIncluded, enabledPlugins, tm, tools)
	}
	tools = slices.Clip(tools)

	return tools, nil
}

// maybeAddPluginTools appends tools with all category tools of a plugin resource
// if the plugin resource is enabled. It returns the updated slice
func maybeAddPluginTools(
	pluginID string,
	categories []string,
	categoryIncluded map[string]bool,
	enabledPlugins map[string]bool,
	tm *ToolManager,
	tools []server.ServerTool,
) []server.ServerTool {
	if !enabledPlugins[pluginID] {
		return tools
	}

	for _, category := range categories {
		tools = addCategoryTools(category, categoryIncluded, tm, tools)
	}
	return tools
}

// addCategoryTools appends tools of a category when enabled and returns the updated slice.
func addCategoryTools(category string, categoryIncluded map[string]bool, tm *ToolManager, tools []server.ServerTool) []server.ServerTool {

	if !tm.enabledTools[category] {
		slog.Info("skipping tool broadcast for excluded category", "category", category)
		return tools
	}

	if categoryIncluded[category] {
		// Skip tools for included sources
		return tools
	}

	categoryIncluded[category] = true

	toolsFc, found := tm.categoryTools[category]
	if !found {
		slog.Info("skipping tool broadcast for unidentified category", "category", category)
		return tools
	}

	dsTools := toolsFc()
	for _, tool := range dsTools {
		tools = append(tools, server.ServerTool{
			Tool:    tool.Tool,
			Handler: tool.Handler,
		})
	}

	return tools
}

// DiscoverAndRegisterToolsSession discovers an authenticated session's accessible resources of connected grafana instance
// and registers tools for a session
// A DiscoverAndRegisterToolsSession call is concurrent safe across varying or identical sessions
func (tm *ToolManager) DiscoverAndRegisterToolsSession(ctx context.Context, session server.ClientSession) {
	// Detection of connected tools disabled
	if !tm.connectedOnly {
		return
	}

	sessionID := session.SessionID()
	state, exists := tm.sm.GetSession(sessionID)
	if !exists {
		// Session exists in server context but not in our SessionManager yet
		tm.sm.CreateSession(ctx, session)
		state, exists = tm.sm.GetSession(sessionID)
		if !exists {
			slog.Error("failed to create session in SessionManager", "sessionID", sessionID)
			return
		}
	}
	// Discover datasources and register tools once per session
	state.initDiscoveryOnce.Do(func() {
		tools, err := tm.discoverTools(ctx)

		if err != nil {
			slog.Error("failed to discover MCP datasources", "error", err)
			return
		}

		if len(tools) == 0 {
			return
		}

		if err := tm.server.AddSessionTools(sessionID, tools...); err != nil {
			slog.Warn("failed to add session tools", "session", sessionID, "error", err)
		} else {
			slog.Info("Registered tools of discovered datasources for session", "sessionId", sessionID, "count", len(tools))
		}
	})
}

// DiscoverAndRegisterToolsStdio discovers resources of connected grafana instance and registers tools globally.
// Expected to be used for global tool registry when server is configured with stdio transport
func (tm *ToolManager) DiscoverAndRegisterToolsStdio(ctx context.Context) error {
	// If this is called once globally
	if !tm.connectedOnly {
		return nil
	}

	tools, err := tm.discoverTools(ctx)
	if err != nil {
		slog.Error("failed to discover MCP datasources", "error", err)
		return err
	}

	tm.server.AddTools(tools...)
	slog.Info("Registered tools of discovered datasources", "count", len(tools))
	return nil
}
