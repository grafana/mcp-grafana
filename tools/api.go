package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/itchyny/gojq"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	mcpgrafana "github.com/grafana/mcp-grafana"
)

const maxAPIResponseBytes = 10 * 1024 * 1024 // 10MB

var allowedMethods = map[string]bool{
	http.MethodGet:    true,
	http.MethodPost:   true,
	http.MethodPut:    true,
	http.MethodPatch:  true,
	http.MethodDelete: true,
}

var readOnlyMethods = map[string]bool{
	http.MethodGet: true,
}

type APIRequestParams struct {
	Endpoint string            `json:"endpoint" jsonschema:"required,description=The API path relative to the Grafana base URL (e.g. '/api/org'\\, '/api/dashboards/uid/abc123'). Must start with '/'."`
	Method   string            `json:"method,omitempty" jsonschema:"enum=GET,enum=POST,enum=PUT,enum=PATCH,enum=DELETE,description=HTTP method. Defaults to GET"`
	Body     string            `json:"body,omitempty" jsonschema:"description=Request body (JSON string). Used with POST\\, PUT\\, and PATCH requests."`
	Headers  map[string]string `json:"headers,omitempty" jsonschema:"description=Additional HTTP headers to include in the request."`
	JQ       string            `json:"jq,omitempty" jsonschema:"description=A jq expression to filter or transform the JSON response (e.g. '.dashboards[] | .title')."`
}

type APIRequestResult struct {
	Status  int               `json:"status"`
	Headers map[string]string `json:"headers,omitempty"`
	Data    any               `json:"data"`
}

func apiRequest(ctx context.Context, args APIRequestParams) (*APIRequestResult, error) {
	cfg := mcpgrafana.GrafanaConfigFromContext(ctx)
	if cfg.URL == "" {
		return nil, fmt.Errorf("grafana URL is not configured")
	}

	method := strings.ToUpper(args.Method)
	if method == "" {
		method = http.MethodGet
	}
	if !allowedMethods[method] {
		return nil, fmt.Errorf("unsupported HTTP method: %s", method)
	}

	endpoint := args.Endpoint
	if !strings.HasPrefix(endpoint, "/") {
		return nil, fmt.Errorf("endpoint must be a relative path starting with '/' (got %q)", endpoint)
	}

	var jqCode *gojq.Code
	if args.JQ != "" {
		query, err := gojq.Parse(args.JQ)
		if err != nil {
			return nil, fmt.Errorf("invalid jq expression: %w", err)
		}
		code, err := gojq.Compile(query)
		if err != nil {
			return nil, fmt.Errorf("compile jq expression: %w", err)
		}
		jqCode = code
	}

	transport, err := mcpgrafana.BuildTransport(&cfg, nil)
	if err != nil {
		return nil, fmt.Errorf("build transport: %w", err)
	}

	url := strings.TrimRight(cfg.URL, "/") + endpoint

	var bodyReader io.Reader
	if args.Body != "" {
		bodyReader = strings.NewReader(args.Body)
	}

	req, err := http.NewRequestWithContext(ctx, method, url, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	if args.Body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range args.Headers {
		req.Header.Set(k, v)
	}

	resp, err := (&http.Client{Transport: transport}).Do(req)
	if err != nil {
		return nil, fmt.Errorf("do request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxAPIResponseBytes))
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	result := &APIRequestResult{
		Status: resp.StatusCode,
		Headers: map[string]string{
			"Content-Type": resp.Header.Get("Content-Type"),
		},
	}

	var parsed any
	if err := json.Unmarshal(body, &parsed); err != nil {
		result.Data = string(body)
		return result, nil
	}

	if jqCode != nil {
		filtered, err := applyJQ(jqCode, parsed)
		if err != nil {
			return nil, fmt.Errorf("apply jq expression: %w", err)
		}
		result.Data = filtered
	} else {
		result.Data = parsed
	}

	return result, nil
}

func applyJQ(code *gojq.Code, input any) (any, error) {
	iter := code.Run(input)
	var results []any
	for {
		v, ok := iter.Next()
		if !ok {
			break
		}
		if err, isErr := v.(error); isErr {
			return nil, err
		}
		results = append(results, v)
	}
	if len(results) == 1 {
		return results[0], nil
	}
	return results, nil
}

var APIRequest = mcpgrafana.MustTool(
	"grafana_api_request",
	"Make an authenticated HTTP request to the Grafana API. Similar to 'gh api' for GitHub. "+
		"Supports any Grafana API endpoint with optional jq-style response filtering. "+
		"Use this for API endpoints that don't have a dedicated tool.",
	apiRequest,
	mcp.WithTitleAnnotation("Grafana API request"),
)

var APIRequestReadOnly = mcpgrafana.MustTool(
	"grafana_api_request",
	"Make an authenticated HTTP request to the Grafana API. Similar to 'gh api' for GitHub. "+
		"Supports any Grafana API endpoint with optional jq-style response filtering. "+
		"Use this for API endpoints that don't have a dedicated tool. "+
		"Only GET requests are allowed.",
	apiRequestReadOnly,
	mcp.WithTitleAnnotation("Grafana API request"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
)

func apiRequestReadOnly(ctx context.Context, args APIRequestParams) (*APIRequestResult, error) {
	method := strings.ToUpper(args.Method)
	if method == "" {
		method = http.MethodGet
	}
	if !readOnlyMethods[method] {
		return nil, fmt.Errorf("method %s is not allowed in read-only mode; only GET requests are permitted", method)
	}
	return apiRequest(ctx, args)
}

func AddAPITools(mcp *server.MCPServer, enableWriteTools bool) {
	if enableWriteTools {
		APIRequest.Register(mcp)
	} else {
		APIRequestReadOnly.Register(mcp)
	}
}
