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

var allowedMethods = map[string]bool{
	http.MethodGet:    true,
	http.MethodPost:   true,
	http.MethodPut:    true,
	http.MethodPatch:  true,
	http.MethodDelete: true,
}

type APIRequestParams struct {
	Endpoint string            `json:"endpoint" jsonschema:"required,description=The API path relative to the Grafana base URL (e.g. '/api/org'\\, '/api/dashboards/uid/abc123'). Must start with '/'."`
	Method   string            `json:"method,omitempty" jsonschema:"enum=GET,enum=POST,enum=PUT,enum=PATCH,enum=DELETE,description=HTTP method. Defaults to GET"`
	Body     string            `json:"body,omitempty" jsonschema:"description=Request body (JSON string). Used with POST\\, PUT\\, and PATCH requests."`
	Headers  map[string]string `json:"headers,omitempty" jsonschema:"description=Additional HTTP headers to include in the request."`
	JQ       string            `json:"jq,omitempty" jsonschema:"description=A jq expression to filter or transform the JSON response (e.g. '.dashboards[] | .title')."`
}

type APIRequestReadOnlyParams struct {
	Endpoint string            `json:"endpoint" jsonschema:"required,description=The API path relative to the Grafana base URL (e.g. '/api/org'\\, '/api/dashboards/uid/abc123'). Must start with '/'."`
	Method   string            `json:"method,omitempty" jsonschema:"enum=GET,description=HTTP method. Only GET is allowed in read-only mode"`
	Headers  map[string]string `json:"headers,omitempty" jsonschema:"description=Additional HTTP headers to include in the request."`
	JQ       string            `json:"jq,omitempty" jsonschema:"description=A jq expression to filter or transform the JSON response (e.g. '.dashboards[] | .title')."`
}

type APIRequestResult struct {
	Status  int               `json:"status"`
	Headers map[string]string `json:"headers,omitempty"`
	Data    any               `json:"data"`
}

func apiRequest(ctx context.Context, args APIRequestParams) (*APIRequestResult, error) {
	return doAPIRequest(ctx, args.Endpoint, args.Method, args.Body, args.Headers, args.JQ)
}

func apiRequestReadOnly(ctx context.Context, args APIRequestReadOnlyParams) (*APIRequestResult, error) {
	method := strings.ToUpper(args.Method)
	if method == "" {
		method = http.MethodGet
	}
	if method != http.MethodGet {
		return nil, fmt.Errorf("method %s is not allowed in read-only mode; only GET requests are permitted", method)
	}
	return doAPIRequest(ctx, args.Endpoint, method, "", args.Headers, args.JQ)
}

func doAPIRequest(ctx context.Context, endpoint, method, body string, headers map[string]string, jqExpr string) (*APIRequestResult, error) {
	cfg := mcpgrafana.GrafanaConfigFromContext(ctx)
	if cfg.URL == "" {
		return nil, fmt.Errorf("grafana URL is not configured")
	}

	method = strings.ToUpper(method)
	if method == "" {
		method = http.MethodGet
	}
	if !allowedMethods[method] {
		return nil, fmt.Errorf("unsupported HTTP method: %s", method)
	}

	if !strings.HasPrefix(endpoint, "/") {
		return nil, fmt.Errorf("endpoint must be a relative path starting with '/' (got %q)", endpoint)
	}

	var jqCode *gojq.Code
	if jqExpr != "" {
		query, err := gojq.Parse(jqExpr)
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
	if body != "" {
		bodyReader = strings.NewReader(body)
	}

	req, err := http.NewRequestWithContext(ctx, method, url, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := (&http.Client{Transport: transport}).Do(req)
	if err != nil {
		return nil, fmt.Errorf("do request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := readResponseBody(resp.Body, defaultResponseLimitBytes)
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
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		result.Data = string(respBody)
		return result, nil
	}

	if jqCode != nil {
		filtered, err := applyJQ(ctx, jqCode, parsed)
		if err != nil {
			return nil, fmt.Errorf("apply jq expression: %w", err)
		}
		result.Data = filtered
	} else {
		result.Data = parsed
	}

	return result, nil
}

func applyJQ(ctx context.Context, code *gojq.Code, input any) (any, error) {
	iter := code.RunWithContext(ctx, input)
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

func AddAPITools(mcp *server.MCPServer, enableWriteTools bool) {
	if enableWriteTools {
		APIRequest.Register(mcp)
	} else {
		APIRequestReadOnly.Register(mcp)
	}
}
