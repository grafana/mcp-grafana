package mcpgrafana

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/go-openapi/runtime/client"
	grafana_client "github.com/grafana/grafana-openapi-client-go/client"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

func TestExtractIncidentClientFromEnv(t *testing.T) {
	t.Setenv("GRAFANA_URL", "http://my-test-url.grafana.com/")
	ctx := ExtractIncidentClientFromEnv(context.Background())

	client := IncidentClientFromContext(ctx)
	require.NotNil(t, client)
	assert.Equal(t, "http://my-test-url.grafana.com/api/plugins/grafana-irm-app/resources/api/v1/", client.RemoteHost)
}

func TestExtractIncidentClientFromHeaders(t *testing.T) {
	t.Run("no headers, no env", func(t *testing.T) {
		req, err := http.NewRequest("GET", "http://example.com", nil)
		require.NoError(t, err)
		ctx := ExtractIncidentClientFromHeaders(context.Background(), req)

		client := IncidentClientFromContext(ctx)
		require.NotNil(t, client)
		assert.Equal(t, "http://localhost:3000/api/plugins/grafana-irm-app/resources/api/v1/", client.RemoteHost)
	})

	t.Run("no headers, with env", func(t *testing.T) {
		t.Setenv("GRAFANA_URL", "http://my-test-url.grafana.com/")
		req, err := http.NewRequest("GET", "http://example.com", nil)
		require.NoError(t, err)
		ctx := ExtractIncidentClientFromHeaders(context.Background(), req)

		client := IncidentClientFromContext(ctx)
		require.NotNil(t, client)
		assert.Equal(t, "http://my-test-url.grafana.com/api/plugins/grafana-irm-app/resources/api/v1/", client.RemoteHost)
	})

	t.Run("with headers, no env", func(t *testing.T) {
		req, err := http.NewRequest("GET", "http://example.com", nil)
		req.Header.Set(grafanaURLHeader, "http://my-test-url.grafana.com")
		require.NoError(t, err)
		ctx := ExtractIncidentClientFromHeaders(context.Background(), req)

		client := IncidentClientFromContext(ctx)
		require.NotNil(t, client)
		assert.Equal(t, "http://my-test-url.grafana.com/api/plugins/grafana-irm-app/resources/api/v1/", client.RemoteHost)
	})

	t.Run("with headers, with env", func(t *testing.T) {
		t.Setenv("GRAFANA_URL", "will-not-be-used")
		req, err := http.NewRequest("GET", "http://example.com", nil)
		req.Header.Set(grafanaURLHeader, "http://my-test-url.grafana.com")
		require.NoError(t, err)
		ctx := ExtractIncidentClientFromHeaders(context.Background(), req)

		client := IncidentClientFromContext(ctx)
		require.NotNil(t, client)
		assert.Equal(t, "http://my-test-url.grafana.com/api/plugins/grafana-irm-app/resources/api/v1/", client.RemoteHost)
	})
}

func TestExtractGrafanaInfoFromHeaders(t *testing.T) {
	t.Run("no headers, no env", func(t *testing.T) {
		// Explicitly clear environment variables to ensure test isolation
		t.Setenv("GRAFANA_URL", "")
		t.Setenv("GRAFANA_API_KEY", "")
		t.Setenv("GRAFANA_SERVICE_ACCOUNT_TOKEN", "")

		req, err := http.NewRequest("GET", "http://example.com", nil)
		require.NoError(t, err)
		ctx := ExtractGrafanaInfoFromHeaders(context.Background(), req)
		config := GrafanaConfigFromContext(ctx)
		assert.Equal(t, defaultGrafanaURL, config.URL)
		assert.Equal(t, "", config.APIKey)
		assert.Nil(t, config.BasicAuth)
	})

	t.Run("no headers, with env", func(t *testing.T) {
		t.Setenv("GRAFANA_URL", "http://my-test-url.grafana.com")
		t.Setenv("GRAFANA_API_KEY", "my-test-api-key")

		req, err := http.NewRequest("GET", "http://example.com", nil)
		require.NoError(t, err)
		ctx := ExtractGrafanaInfoFromHeaders(context.Background(), req)
		config := GrafanaConfigFromContext(ctx)
		assert.Equal(t, "http://my-test-url.grafana.com", config.URL)
		assert.Equal(t, "my-test-api-key", config.APIKey)
	})

	t.Run("no headers, with service account token", func(t *testing.T) {
		t.Setenv("GRAFANA_URL", "http://my-test-url.grafana.com")
		t.Setenv("GRAFANA_SERVICE_ACCOUNT_TOKEN", "my-service-account-token")

		req, err := http.NewRequest("GET", "http://example.com", nil)
		require.NoError(t, err)
		ctx := ExtractGrafanaInfoFromHeaders(context.Background(), req)
		config := GrafanaConfigFromContext(ctx)
		assert.Equal(t, "http://my-test-url.grafana.com", config.URL)
		assert.Equal(t, "my-service-account-token", config.APIKey)
	})

	t.Run("no headers, service account token takes precedence over api key", func(t *testing.T) {
		t.Setenv("GRAFANA_URL", "http://my-test-url.grafana.com")
		t.Setenv("GRAFANA_API_KEY", "my-deprecated-api-key")
		t.Setenv("GRAFANA_SERVICE_ACCOUNT_TOKEN", "my-service-account-token")

		req, err := http.NewRequest("GET", "http://example.com", nil)
		require.NoError(t, err)
		ctx := ExtractGrafanaInfoFromHeaders(context.Background(), req)
		config := GrafanaConfigFromContext(ctx)
		assert.Equal(t, "http://my-test-url.grafana.com", config.URL)
		assert.Equal(t, "my-service-account-token", config.APIKey)
	})

	t.Run("with headers, no env", func(t *testing.T) {
		req, err := http.NewRequest("GET", "http://example.com", nil)
		require.NoError(t, err)
		req.Header.Set(grafanaURLHeader, "http://my-test-url.grafana.com")
		req.Header.Set(grafanaAPIKeyHeader, "my-test-api-key")
		ctx := ExtractGrafanaInfoFromHeaders(context.Background(), req)
		config := GrafanaConfigFromContext(ctx)
		assert.Equal(t, "http://my-test-url.grafana.com", config.URL)
		assert.Equal(t, "my-test-api-key", config.APIKey)
	})

	t.Run("with headers, with env", func(t *testing.T) {
		// Env vars should be ignored if headers are present.
		t.Setenv("GRAFANA_URL", "will-not-be-used")
		t.Setenv("GRAFANA_API_KEY", "will-not-be-used")
		t.Setenv("GRAFANA_SERVICE_ACCOUNT_TOKEN", "will-not-be-used")

		req, err := http.NewRequest("GET", "http://example.com", nil)
		require.NoError(t, err)
		req.Header.Set(grafanaURLHeader, "http://my-test-url.grafana.com")
		req.Header.Set(grafanaAPIKeyHeader, "my-test-api-key")
		ctx := ExtractGrafanaInfoFromHeaders(context.Background(), req)
		config := GrafanaConfigFromContext(ctx)
		assert.Equal(t, "http://my-test-url.grafana.com", config.URL)
		assert.Equal(t, "my-test-api-key", config.APIKey)
	})

	t.Run("with service account token header", func(t *testing.T) {
		t.Setenv("GRAFANA_URL", "")
		t.Setenv("GRAFANA_API_KEY", "")
		t.Setenv("GRAFANA_SERVICE_ACCOUNT_TOKEN", "")

		req, err := http.NewRequest("GET", "http://example.com", nil)
		require.NoError(t, err)
		req.Header.Set(grafanaURLHeader, "http://my-test-url.grafana.com")
		req.Header.Set(grafanaServiceAccountTokenHeader, "my-service-account-token")
		ctx := ExtractGrafanaInfoFromHeaders(context.Background(), req)
		config := GrafanaConfigFromContext(ctx)
		assert.Equal(t, "http://my-test-url.grafana.com", config.URL)
		assert.Equal(t, "my-service-account-token", config.APIKey)
	})

	t.Run("service account token header takes precedence over api key header", func(t *testing.T) {
		t.Setenv("GRAFANA_URL", "")
		t.Setenv("GRAFANA_API_KEY", "")
		t.Setenv("GRAFANA_SERVICE_ACCOUNT_TOKEN", "")

		req, err := http.NewRequest("GET", "http://example.com", nil)
		require.NoError(t, err)
		req.Header.Set(grafanaURLHeader, "http://my-test-url.grafana.com")
		req.Header.Set(grafanaServiceAccountTokenHeader, "my-service-account-token")
		req.Header.Set(grafanaAPIKeyHeader, "my-deprecated-api-key")
		ctx := ExtractGrafanaInfoFromHeaders(context.Background(), req)
		config := GrafanaConfigFromContext(ctx)
		assert.Equal(t, "http://my-test-url.grafana.com", config.URL)
		assert.Equal(t, "my-service-account-token", config.APIKey)
	})

	t.Run("no headers, with env", func(t *testing.T) {
		t.Setenv("GRAFANA_USERNAME", "foo")
		t.Setenv("GRAFANA_PASSWORD", "bar")

		req, err := http.NewRequest("GET", "http://example.com", nil)
		require.NoError(t, err)
		ctx := ExtractGrafanaInfoFromHeaders(context.Background(), req)
		config := GrafanaConfigFromContext(ctx)
		assert.Equal(t, "foo", config.BasicAuth.Username())
		password, _ := config.BasicAuth.Password()
		assert.Equal(t, "bar", password)
	})

	t.Run("user auth with headers, no env", func(t *testing.T) {
		req, err := http.NewRequest("GET", "http://example.com", nil)
		req.SetBasicAuth("foo", "bar")
		require.NoError(t, err)
		ctx := ExtractGrafanaInfoFromHeaders(context.Background(), req)
		config := GrafanaConfigFromContext(ctx)
		assert.Equal(t, "foo", config.BasicAuth.Username())
		password, _ := config.BasicAuth.Password()
		assert.Equal(t, "bar", password)
	})

	t.Run("user auth with headers, with env", func(t *testing.T) {
		t.Setenv("GRAFANA_USERNAME", "will-not-be-used")
		t.Setenv("GRAFANA_PASSWORD", "will-not-be-used")

		req, err := http.NewRequest("GET", "http://example.com", nil)
		req.SetBasicAuth("foo", "bar")
		require.NoError(t, err)
		ctx := ExtractGrafanaInfoFromHeaders(context.Background(), req)
		config := GrafanaConfigFromContext(ctx)
		assert.Equal(t, "foo", config.BasicAuth.Username())
		password, _ := config.BasicAuth.Password()
		assert.Equal(t, "bar", password)
	})

	t.Run("orgID from env", func(t *testing.T) {
		t.Setenv("GRAFANA_ORG_ID", "123")

		req, err := http.NewRequest("GET", "http://example.com", nil)
		require.NoError(t, err)
		ctx := ExtractGrafanaInfoFromHeaders(context.Background(), req)
		config := GrafanaConfigFromContext(ctx)
		assert.Equal(t, int64(123), config.OrgID)
	})

	t.Run("orgID from header", func(t *testing.T) {
		req, err := http.NewRequest("GET", "http://example.com", nil)
		require.NoError(t, err)
		req.Header.Set("X-Grafana-Org-Id", "456")
		ctx := ExtractGrafanaInfoFromHeaders(context.Background(), req)
		config := GrafanaConfigFromContext(ctx)
		assert.Equal(t, int64(456), config.OrgID)
	})

	t.Run("orgID header takes precedence over env", func(t *testing.T) {
		t.Setenv("GRAFANA_ORG_ID", "123")

		req, err := http.NewRequest("GET", "http://example.com", nil)
		require.NoError(t, err)
		req.Header.Set("X-Grafana-Org-Id", "456")
		ctx := ExtractGrafanaInfoFromHeaders(context.Background(), req)
		config := GrafanaConfigFromContext(ctx)
		assert.Equal(t, int64(456), config.OrgID)
	})

	t.Run("invalid orgID from env ignored", func(t *testing.T) {
		t.Setenv("GRAFANA_ORG_ID", "not-a-number")

		req, err := http.NewRequest("GET", "http://example.com", nil)
		require.NoError(t, err)
		ctx := ExtractGrafanaInfoFromHeaders(context.Background(), req)
		config := GrafanaConfigFromContext(ctx)
		assert.Equal(t, int64(0), config.OrgID)
	})

	t.Run("invalid orgID from header ignored", func(t *testing.T) {
		req, err := http.NewRequest("GET", "http://example.com", nil)
		require.NoError(t, err)
		req.Header.Set("X-Grafana-Org-Id", "invalid")
		ctx := ExtractGrafanaInfoFromHeaders(context.Background(), req)
		config := GrafanaConfigFromContext(ctx)
		assert.Equal(t, int64(0), config.OrgID)
	})
}

func TestExtractGrafanaClientPath(t *testing.T) {
	t.Run("no custom path", func(t *testing.T) {
		t.Setenv("GRAFANA_URL", "http://my-test-url.grafana.com/")
		ctx := ExtractGrafanaClientFromEnv(context.Background())

		c := GrafanaClientFromContext(ctx)
		require.NotNil(t, c)
		rt := c.Transport.(*client.Runtime)
		assert.Equal(t, "/api", rt.BasePath)
	})

	t.Run("custom path", func(t *testing.T) {
		t.Setenv("GRAFANA_URL", "http://my-test-url.grafana.com/grafana")
		ctx := ExtractGrafanaClientFromEnv(context.Background())

		c := GrafanaClientFromContext(ctx)
		require.NotNil(t, c)
		rt := c.Transport.(*client.Runtime)
		assert.Equal(t, "/grafana/api", rt.BasePath)
	})

	t.Run("custom path, trailing slash", func(t *testing.T) {
		t.Setenv("GRAFANA_URL", "http://my-test-url.grafana.com/grafana/")
		ctx := ExtractGrafanaClientFromEnv(context.Background())

		c := GrafanaClientFromContext(ctx)
		require.NotNil(t, c)
		rt := c.Transport.(*client.Runtime)
		assert.Equal(t, "/grafana/api", rt.BasePath)
	})
}

// minURL is a helper struct representing what we can extract from a constructed
// Grafana client.
type minURL struct {
	host, basePath string
}

// minURLFromClient extracts some minimal amount of URL info from a Grafana client.
func minURLFromClient(c *GrafanaClient) minURL {
	rt := c.Transport.(*client.Runtime)
	return minURL{rt.Host, rt.BasePath}
}

func TestExtractGrafanaClientFromHeaders(t *testing.T) {
	t.Run("no headers, no env", func(t *testing.T) {
		req, err := http.NewRequest("GET", "http://example.com", nil)
		require.NoError(t, err)
		ctx := ExtractGrafanaClientFromHeaders(context.Background(), req)
		c := GrafanaClientFromContext(ctx)
		url := minURLFromClient(c)
		assert.Equal(t, "localhost:3000", url.host)
		assert.Equal(t, "/api", url.basePath)
	})

	t.Run("no headers, with env", func(t *testing.T) {
		t.Setenv("GRAFANA_URL", "http://my-test-url.grafana.com")

		req, err := http.NewRequest("GET", "http://example.com", nil)
		require.NoError(t, err)
		ctx := ExtractGrafanaClientFromHeaders(context.Background(), req)
		c := GrafanaClientFromContext(ctx)
		url := minURLFromClient(c)
		assert.Equal(t, "my-test-url.grafana.com", url.host)
		assert.Equal(t, "/api", url.basePath)
	})

	t.Run("with headers, no env", func(t *testing.T) {
		req, err := http.NewRequest("GET", "http://example.com", nil)
		require.NoError(t, err)
		req.Header.Set(grafanaURLHeader, "http://my-test-url.grafana.com")
		ctx := ExtractGrafanaClientFromHeaders(context.Background(), req)
		c := GrafanaClientFromContext(ctx)
		url := minURLFromClient(c)
		assert.Equal(t, "my-test-url.grafana.com", url.host)
		assert.Equal(t, "/api", url.basePath)
	})

	t.Run("with headers, with env", func(t *testing.T) {
		// Env vars should be ignored if headers are present.
		t.Setenv("GRAFANA_URL", "will-not-be-used")

		req, err := http.NewRequest("GET", "http://example.com", nil)
		require.NoError(t, err)
		req.Header.Set(grafanaURLHeader, "http://my-test-url.grafana.com")
		ctx := ExtractGrafanaClientFromHeaders(context.Background(), req)
		c := GrafanaClientFromContext(ctx)
		url := minURLFromClient(c)
		assert.Equal(t, "my-test-url.grafana.com", url.host)
		assert.Equal(t, "/api", url.basePath)
	})
}

func TestToolTracingInstrumentation(t *testing.T) {
	// Set up in-memory span recorder
	spanRecorder := tracetest.NewSpanRecorder()
	tracerProvider := sdktrace.NewTracerProvider(
		sdktrace.WithSpanProcessor(spanRecorder),
	)
	originalProvider := otel.GetTracerProvider()
	otel.SetTracerProvider(tracerProvider)
	defer otel.SetTracerProvider(originalProvider) // Restore original provider

	t.Run("successful tool execution creates span with correct attributes", func(t *testing.T) {
		// Clear any previous spans
		spanRecorder.Reset()

		// Define a simple test tool
		type TestParams struct {
			Message string `json:"message" jsonschema:"description=Test message"`
		}

		testHandler := func(ctx context.Context, args TestParams) (string, error) {
			return "Hello " + args.Message, nil
		}

		// Create tool using MustTool (this applies our instrumentation)
		tool := MustTool("test_tool", "A test tool for tracing", testHandler)

		// Create context with argument logging enabled
		config := GrafanaConfig{
			IncludeArgumentsInSpans: true,
		}
		ctx := WithGrafanaConfig(context.Background(), config)

		// Create a mock MCP request
		request := mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Name: "test_tool",
				Arguments: map[string]interface{}{
					"message": "world",
				},
			},
		}

		// Execute the tool
		result, err := tool.Handler(ctx, request)
		require.NoError(t, err)
		require.NotNil(t, result)

		// Verify span was created
		spans := spanRecorder.Ended()
		require.Len(t, spans, 1)

		span := spans[0]
		assert.Equal(t, "tools/call test_tool", span.Name())
		assert.Equal(t, codes.Ok, span.Status().Code)

		// Check semconv attributes
		attributes := span.Attributes()
		assertHasAttribute(t, attributes, "gen_ai.tool.name", "test_tool")
		assertHasAttribute(t, attributes, "mcp.method.name", "tools/call")
		assertHasAttribute(t, attributes, "gen_ai.tool.call.arguments", `{"message":"world"}`)
	})

	t.Run("tool execution error records error on span", func(t *testing.T) {
		// Clear any previous spans
		spanRecorder.Reset()

		// Define a test tool that returns an error
		type TestParams struct {
			ShouldFail bool `json:"shouldFail" jsonschema:"description=Whether to fail"`
		}

		testHandler := func(ctx context.Context, args TestParams) (string, error) {
			if args.ShouldFail {
				return "", assert.AnError
			}
			return "success", nil
		}

		// Create tool
		tool := MustTool("failing_tool", "A tool that can fail", testHandler)

		// Create context (spans always created)
		config := GrafanaConfig{}
		ctx := WithGrafanaConfig(context.Background(), config)

		// Create a mock MCP request that will cause failure
		request := mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Name: "failing_tool",
				Arguments: map[string]interface{}{
					"shouldFail": true,
				},
			},
		}

		// Execute the tool (should fail)
		result, err := tool.Handler(ctx, request)
		assert.NoError(t, err)
		require.NotNil(t, result)
		assert.True(t, result.IsError)

		// Verify span was created and marked as error
		spans := spanRecorder.Ended()
		require.Len(t, spans, 1)

		span := spans[0]
		assert.Equal(t, "tools/call failing_tool", span.Name())
		assert.Equal(t, codes.Error, span.Status().Code)
		assert.Equal(t, assert.AnError.Error(), span.Status().Description)

		// Verify error was recorded (check events for error record)
		events := span.Events()
		hasErrorEvent := false
		for _, event := range events {
			if event.Name == "exception" {
				hasErrorEvent = true
				break
			}
		}
		assert.True(t, hasErrorEvent, "Expected error event to be recorded on span")
	})

	t.Run("spans always created for context propagation", func(t *testing.T) {
		// Clear any previous spans
		spanRecorder.Reset()

		// Define a simple test tool
		type TestParams struct {
			Message string `json:"message" jsonschema:"description=Test message"`
		}

		testHandler := func(ctx context.Context, args TestParams) (string, error) {
			return "processed", nil
		}

		// Create tool
		tool := MustTool("context_prop_tool", "A tool for context propagation", testHandler)

		// Create context with default config (no special flags)
		config := GrafanaConfig{}
		ctx := WithGrafanaConfig(context.Background(), config)

		// Create a mock MCP request
		request := mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Name: "context_prop_tool",
				Arguments: map[string]interface{}{
					"message": "test",
				},
			},
		}

		// Execute the tool (should always create spans for context propagation)
		result, err := tool.Handler(ctx, request)
		require.NoError(t, err)
		require.NotNil(t, result)

		// Verify spans ARE always created
		spans := spanRecorder.Ended()
		require.Len(t, spans, 1)

		span := spans[0]
		assert.Equal(t, "tools/call context_prop_tool", span.Name())
		assert.Equal(t, codes.Ok, span.Status().Code)
	})

	t.Run("arguments not logged by default (PII safety)", func(t *testing.T) {
		// Clear any previous spans
		spanRecorder.Reset()

		// Define a simple test tool
		type TestParams struct {
			SensitiveData string `json:"sensitiveData" jsonschema:"description=Potentially sensitive data"`
		}

		testHandler := func(ctx context.Context, args TestParams) (string, error) {
			return "processed", nil
		}

		// Create tool
		tool := MustTool("sensitive_tool", "A tool with sensitive data", testHandler)

		// Create context with argument logging disabled (default)
		config := GrafanaConfig{
			IncludeArgumentsInSpans: false, // Default: safe
		}
		ctx := WithGrafanaConfig(context.Background(), config)

		// Create a mock MCP request with potentially sensitive data
		request := mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Name: "sensitive_tool",
				Arguments: map[string]interface{}{
					"sensitiveData": "user@example.com",
				},
			},
		}

		// Execute the tool (arguments should NOT be logged by default)
		result, err := tool.Handler(ctx, request)
		require.NoError(t, err)
		require.NotNil(t, result)

		// Verify span was created
		spans := spanRecorder.Ended()
		require.Len(t, spans, 1)

		span := spans[0]
		assert.Equal(t, "tools/call sensitive_tool", span.Name())
		assert.Equal(t, codes.Ok, span.Status().Code)

		// Check that arguments are NOT logged (PII safety)
		attributes := span.Attributes()
		assertHasAttribute(t, attributes, "gen_ai.tool.name", "sensitive_tool")
		assertHasAttribute(t, attributes, "mcp.method.name", "tools/call")

		// Verify arguments are NOT present
		for _, attr := range attributes {
			assert.NotEqual(t, "gen_ai.tool.call.arguments", string(attr.Key), "Arguments should not be logged by default for PII safety")
		}
	})

	t.Run("arguments logged when argument logging enabled", func(t *testing.T) {
		// Clear any previous spans
		spanRecorder.Reset()

		// Define a simple test tool
		type TestParams struct {
			SafeData string `json:"safeData" jsonschema:"description=Non-sensitive data"`
		}

		testHandler := func(ctx context.Context, args TestParams) (string, error) {
			return "processed", nil
		}

		// Create tool
		tool := MustTool("debug_tool", "A tool for debugging", testHandler)

		// Create context with argument logging enabled
		config := GrafanaConfig{
			IncludeArgumentsInSpans: true,
		}
		ctx := WithGrafanaConfig(context.Background(), config)

		// Create a mock MCP request
		request := mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Name: "debug_tool",
				Arguments: map[string]interface{}{
					"safeData": "debug-value",
				},
			},
		}

		// Execute the tool (arguments SHOULD be logged when flag enabled)
		result, err := tool.Handler(ctx, request)
		require.NoError(t, err)
		require.NotNil(t, result)

		// Verify span was created
		spans := spanRecorder.Ended()
		require.Len(t, spans, 1)

		span := spans[0]
		assert.Equal(t, "tools/call debug_tool", span.Name())
		assert.Equal(t, codes.Ok, span.Status().Code)

		// Check that arguments ARE logged when flag enabled
		attributes := span.Attributes()
		assertHasAttribute(t, attributes, "gen_ai.tool.name", "debug_tool")
		assertHasAttribute(t, attributes, "mcp.method.name", "tools/call")
		assertHasAttribute(t, attributes, "gen_ai.tool.call.arguments", `{"safeData":"debug-value"}`)
	})
}

func TestTextMimeConsumerOverride(t *testing.T) {
	// Verify that NewGrafanaClient overrides text/plain and text/html consumers
	// with JSON consumers so that responses with incorrect content-type headers
	// (e.g. from Grafana v12 or reverse proxies) are still parsed correctly.
	// See: https://github.com/grafana/mcp-grafana/issues/635
	ctx := WithGrafanaConfig(context.Background(), GrafanaConfig{})
	c := NewGrafanaClient(ctx, "http://localhost:3000", "test-api-key", nil)
	require.NotNil(t, c)

	rt, ok := c.Transport.(*client.Runtime)
	require.True(t, ok, "expected Transport to be *client.Runtime")

	// The text/plain and text/html consumers should no longer be the default
	// TextConsumer. Verify by checking they can consume a JSON object into a
	// map (TextConsumer would fail on this).
	for _, mime := range []string{"text/plain", "text/html"} {
		consumer, exists := rt.Consumers[mime]
		require.True(t, exists, "consumer for %s should exist", mime)
		require.NotNil(t, consumer, "consumer for %s should not be nil", mime)

		// JSONConsumer can unmarshal into a map; TextConsumer cannot.
		var result map[string]interface{}
		err := consumer.Consume(
			strings.NewReader(`{"status":"ok"}`),
			&result,
		)
		assert.NoError(t, err, "consumer for %s should parse JSON", mime)
		assert.Equal(t, "ok", result["status"], "consumer for %s should return parsed value", mime)
	}
}

func TestHTTPTracingConfiguration(t *testing.T) {
	t.Run("HTTP tracing always enabled for context propagation", func(t *testing.T) {
		// Create context (HTTP tracing always enabled)
		config := GrafanaConfig{}
		ctx := WithGrafanaConfig(context.Background(), config)

		// Create Grafana client
		client := NewGrafanaClient(ctx, "http://localhost:3000", "test-api-key", nil)
		require.NotNil(t, client)

		// Verify the client was created successfully (should not panic)
		assert.NotNil(t, client.Transport)
	})

	t.Run("tracing works gracefully without OpenTelemetry configured", func(t *testing.T) {
		// No OpenTelemetry tracer provider configured

		// Create context (tracing always enabled for context propagation)
		config := GrafanaConfig{}
		ctx := WithGrafanaConfig(context.Background(), config)

		// Create Grafana client (should not panic even without OTEL configured)
		client := NewGrafanaClient(ctx, "http://localhost:3000", "test-api-key", nil)
		require.NotNil(t, client)

		// Verify the client was created successfully
		assert.NotNil(t, client.Transport)
	})
}

func TestExtractTraceContext(t *testing.T) {
	t.Run("no meta returns original context", func(t *testing.T) {
		ctx := context.Background()
		request := mcp.CallToolRequest{}
		result := extractTraceContext(ctx, request)
		assert.Equal(t, ctx, result)
	})

	t.Run("empty meta returns original context", func(t *testing.T) {
		ctx := context.Background()
		request := mcp.CallToolRequest{}
		request.Params.Meta = &mcp.Meta{}
		result := extractTraceContext(ctx, request)
		assert.Equal(t, ctx, result)
	})

	t.Run("valid traceparent extracts span context", func(t *testing.T) {
		ctx := context.Background()
		request := mcp.CallToolRequest{}
		request.Params.Meta = &mcp.Meta{
			AdditionalFields: map[string]any{
				"traceparent": "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01",
			},
		}
		result := extractTraceContext(ctx, request)
		// Should have extracted a span context
		sc := trace.SpanContextFromContext(result)
		assert.True(t, sc.IsValid())
		assert.Equal(t, "4bf92f3577b34da6a3ce929d0e0e4736", sc.TraceID().String())
		assert.Equal(t, "00f067aa0ba902b7", sc.SpanID().String())
	})

	t.Run("invalid traceparent returns context unchanged", func(t *testing.T) {
		ctx := context.Background()
		request := mcp.CallToolRequest{}
		request.Params.Meta = &mcp.Meta{
			AdditionalFields: map[string]any{
				"traceparent": "not-a-valid-traceparent",
			},
		}
		result := extractTraceContext(ctx, request)
		sc := trace.SpanContextFromContext(result)
		assert.False(t, sc.IsValid())
	})
}

// Helper function to check if an attribute exists with expected value
func assertHasAttribute(t *testing.T, attributes []attribute.KeyValue, key string, expectedValue string) {
	for _, attr := range attributes {
		if string(attr.Key) == key {
			assert.Equal(t, expectedValue, attr.Value.AsString())
			return
		}
	}
	t.Errorf("Expected attribute %s with value %s not found", key, expectedValue)
}

func TestExtraHeadersFromEnv(t *testing.T) {
	t.Run("empty env returns nil", func(t *testing.T) {
		t.Setenv("GRAFANA_EXTRA_HEADERS", "")
		headers := extraHeadersFromEnv()
		assert.Nil(t, headers)
	})

	t.Run("valid JSON", func(t *testing.T) {
		t.Setenv("GRAFANA_EXTRA_HEADERS", `{"X-Custom-Header": "custom-value", "X-Another": "another-value"}`)
		headers := extraHeadersFromEnv()
		assert.Equal(t, map[string]string{
			"X-Custom-Header": "custom-value",
			"X-Another":       "another-value",
		}, headers)
	})

	t.Run("invalid JSON returns nil", func(t *testing.T) {
		t.Setenv("GRAFANA_EXTRA_HEADERS", "not-json")
		headers := extraHeadersFromEnv()
		assert.Nil(t, headers)
	})

	t.Run("empty object", func(t *testing.T) {
		t.Setenv("GRAFANA_EXTRA_HEADERS", "{}")
		headers := extraHeadersFromEnv()
		assert.Equal(t, map[string]string{}, headers)
	})
}

func TestExtraHeadersRoundTripper(t *testing.T) {
	t.Run("adds headers to request", func(t *testing.T) {
		var capturedReq *http.Request
		mockRT := &extraHeadersMockRT{
			fn: func(req *http.Request) (*http.Response, error) {
				capturedReq = req
				return &http.Response{StatusCode: 200}, nil
			},
		}

		rt := NewExtraHeadersRoundTripper(mockRT, map[string]string{
			"X-Custom":  "value1",
			"X-Another": "value2",
		})

		req, _ := http.NewRequest("GET", "http://example.com", nil)
		_, err := rt.RoundTrip(req)
		require.NoError(t, err)

		assert.Equal(t, "value1", capturedReq.Header.Get("X-Custom"))
		assert.Equal(t, "value2", capturedReq.Header.Get("X-Another"))
	})

	t.Run("does not modify original request", func(t *testing.T) {
		mockRT := &extraHeadersMockRT{
			fn: func(req *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200}, nil
			},
		}

		rt := NewExtraHeadersRoundTripper(mockRT, map[string]string{
			"X-Custom": "value",
		})

		req, _ := http.NewRequest("GET", "http://example.com", nil)
		_, err := rt.RoundTrip(req)
		require.NoError(t, err)

		assert.Equal(t, "", req.Header.Get("X-Custom"))
	})

	t.Run("nil transport uses default", func(t *testing.T) {
		rt := NewExtraHeadersRoundTripper(nil, map[string]string{})
		assert.NotNil(t, rt.underlying)
	})
}

type capturingMockRT struct {
	fn func(*http.Request) (*http.Response, error)
}

func (m *capturingMockRT) RoundTrip(req *http.Request) (*http.Response, error) {
	return m.fn(req)
}

// Keep the old name as an alias for backwards compatibility in case anything references it.
type extraHeadersMockRT = capturingMockRT

func TestExtractGrafanaInfoWithExtraHeaders(t *testing.T) {
	t.Run("extra headers from env in ExtractGrafanaInfoFromEnv", func(t *testing.T) {
		t.Setenv("GRAFANA_EXTRA_HEADERS", `{"X-Tenant-ID": "tenant-123"}`)
		ctx := ExtractGrafanaInfoFromEnv(context.Background())
		config := GrafanaConfigFromContext(ctx)
		assert.Equal(t, map[string]string{"X-Tenant-ID": "tenant-123"}, config.ExtraHeaders)
	})

	t.Run("extra headers from env in ExtractGrafanaInfoFromHeaders", func(t *testing.T) {
		t.Setenv("GRAFANA_EXTRA_HEADERS", `{"X-Tenant-ID": "tenant-456"}`)
		req, _ := http.NewRequest("GET", "http://example.com", nil)
		ctx := ExtractGrafanaInfoFromHeaders(context.Background(), req)
		config := GrafanaConfigFromContext(ctx)
		assert.Equal(t, map[string]string{"X-Tenant-ID": "tenant-456"}, config.ExtraHeaders)
	})
}

func TestForwardHeaderNamesFromEnv(t *testing.T) {
	t.Run("empty env returns nil", func(t *testing.T) {
		t.Setenv("GRAFANA_FORWARD_HEADERS", "")
		names := forwardHeaderNamesFromEnv()
		assert.Nil(t, names)
	})

	t.Run("single header", func(t *testing.T) {
		t.Setenv("GRAFANA_FORWARD_HEADERS", "Cookie")
		names := forwardHeaderNamesFromEnv()
		assert.Equal(t, []string{"Cookie"}, names)
	})

	t.Run("multiple headers with spaces", func(t *testing.T) {
		t.Setenv("GRAFANA_FORWARD_HEADERS", " Cookie , X-Session-Id , X-Request-Id ")
		names := forwardHeaderNamesFromEnv()
		assert.Equal(t, []string{"Cookie", "X-Session-Id", "X-Request-Id"}, names)
	})

	t.Run("trailing comma ignored", func(t *testing.T) {
		t.Setenv("GRAFANA_FORWARD_HEADERS", "Cookie,")
		names := forwardHeaderNamesFromEnv()
		assert.Equal(t, []string{"Cookie"}, names)
	})
}

func TestForwardedHeadersFromRequest(t *testing.T) {
	t.Run("no env returns nil", func(t *testing.T) {
		t.Setenv("GRAFANA_FORWARD_HEADERS", "")
		req, _ := http.NewRequest("GET", "http://example.com", nil)
		req.Header.Set("Cookie", "session=abc")
		assert.Nil(t, forwardedHeadersFromRequest(req))
	})

	t.Run("header present in request", func(t *testing.T) {
		t.Setenv("GRAFANA_FORWARD_HEADERS", "Cookie")
		req, _ := http.NewRequest("GET", "http://example.com", nil)
		req.Header.Set("Cookie", "session=abc")
		forwarded := forwardedHeadersFromRequest(req)
		assert.Equal(t, map[string]string{"Cookie": "session=abc"}, forwarded)
	})

	t.Run("header missing from request returns nil", func(t *testing.T) {
		t.Setenv("GRAFANA_FORWARD_HEADERS", "Cookie")
		req, _ := http.NewRequest("GET", "http://example.com", nil)
		assert.Nil(t, forwardedHeadersFromRequest(req))
	})

	t.Run("multiple headers partial match", func(t *testing.T) {
		t.Setenv("GRAFANA_FORWARD_HEADERS", "Cookie,X-Session-Id")
		req, _ := http.NewRequest("GET", "http://example.com", nil)
		req.Header.Set("Cookie", "session=abc")
		forwarded := forwardedHeadersFromRequest(req)
		assert.Equal(t, map[string]string{"Cookie": "session=abc"}, forwarded)
	})
}

func TestMergeHeaders(t *testing.T) {
	t.Run("both nil", func(t *testing.T) {
		assert.Nil(t, mergeHeaders(nil, nil))
	})

	t.Run("base only", func(t *testing.T) {
		result := mergeHeaders(map[string]string{"A": "1"}, nil)
		assert.Equal(t, map[string]string{"A": "1"}, result)
	})

	t.Run("override only", func(t *testing.T) {
		result := mergeHeaders(nil, map[string]string{"B": "2"})
		assert.Equal(t, map[string]string{"B": "2"}, result)
	})

	t.Run("override wins on conflict", func(t *testing.T) {
		base := map[string]string{"A": "1", "B": "2"}
		override := map[string]string{"B": "override", "C": "3"}
		result := mergeHeaders(base, override)
		assert.Equal(t, map[string]string{"A": "1", "B": "override", "C": "3"}, result)
	})

	t.Run("case-insensitive header merge: override wins", func(t *testing.T) {
		base := map[string]string{"cookie": "static"}
		override := map[string]string{"Cookie": "from-request"}
		result := mergeHeaders(base, override)
		assert.Len(t, result, 1)
		assert.Equal(t, "from-request", result["Cookie"], "forwarded request value must win over extra header")
	})
}

func TestExtractGrafanaInfoFromHeadersForwardedHeaders(t *testing.T) {
	t.Run("forwarded headers merged into config", func(t *testing.T) {
		t.Setenv("GRAFANA_EXTRA_HEADERS", `{"X-Tenant-ID": "tenant-123"}`)
		t.Setenv("GRAFANA_FORWARD_HEADERS", "Cookie")
		req, _ := http.NewRequest("GET", "http://example.com", nil)
		req.Header.Set("Cookie", "session=user1")

		ctx := ExtractGrafanaInfoFromHeaders(context.Background(), req)
		config := GrafanaConfigFromContext(ctx)
		assert.Equal(t, map[string]string{
			"X-Tenant-Id": "tenant-123",
			"Cookie":      "session=user1",
		}, config.ExtraHeaders)
	})

	t.Run("forwarded header overrides extra header with same name", func(t *testing.T) {
		t.Setenv("GRAFANA_EXTRA_HEADERS", `{"Cookie": "static-cookie"}`)
		t.Setenv("GRAFANA_FORWARD_HEADERS", "Cookie")
		req, _ := http.NewRequest("GET", "http://example.com", nil)
		req.Header.Set("Cookie", "dynamic-cookie")

		ctx := ExtractGrafanaInfoFromHeaders(context.Background(), req)
		config := GrafanaConfigFromContext(ctx)
		assert.Equal(t, "dynamic-cookie", config.ExtraHeaders["Cookie"])
	})

	t.Run("no forward env uses only extra headers", func(t *testing.T) {
		t.Setenv("GRAFANA_EXTRA_HEADERS", `{"X-Tenant-ID": "tenant-789"}`)
		t.Setenv("GRAFANA_FORWARD_HEADERS", "")
		req, _ := http.NewRequest("GET", "http://example.com", nil)
		req.Header.Set("Cookie", "session=ignored")

		ctx := ExtractGrafanaInfoFromHeaders(context.Background(), req)
		config := GrafanaConfigFromContext(ctx)
		assert.Equal(t, map[string]string{"X-Tenant-ID": "tenant-789"}, config.ExtraHeaders)
	})

	t.Run("forward header not present in request", func(t *testing.T) {
		t.Setenv("GRAFANA_EXTRA_HEADERS", "")
		t.Setenv("GRAFANA_FORWARD_HEADERS", "Cookie")
		req, _ := http.NewRequest("GET", "http://example.com", nil)

		ctx := ExtractGrafanaInfoFromHeaders(context.Background(), req)
		config := GrafanaConfigFromContext(ctx)
		assert.Nil(t, config.ExtraHeaders)
	})

	t.Run("forwarded Cookie wins when extra headers has lowercase cookie", func(t *testing.T) {
		t.Setenv("GRAFANA_EXTRA_HEADERS", `{"cookie": "static"}`)
		t.Setenv("GRAFANA_FORWARD_HEADERS", "Cookie")
		req, _ := http.NewRequest("GET", "http://example.com", nil)
		req.Header.Set("Cookie", "session=user2")

		ctx := ExtractGrafanaInfoFromHeaders(context.Background(), req)
		config := GrafanaConfigFromContext(ctx)
		assert.Len(t, config.ExtraHeaders, 1)
		assert.Equal(t, "session=user2", config.ExtraHeaders["Cookie"], "incoming request must take precedence regardless of extra header key case")
	})
}

func TestOrgIDRoundTripper(t *testing.T) {
	t.Run("adds org ID header to request", func(t *testing.T) {
		var capturedReq *http.Request
		mockRT := &capturingMockRT{
			fn: func(req *http.Request) (*http.Response, error) {
				capturedReq = req
				return &http.Response{StatusCode: 200}, nil
			},
		}

		rt := NewOrgIDRoundTripper(mockRT, 123)

		req, _ := http.NewRequest("GET", "http://example.com", nil)
		_, err := rt.RoundTrip(req)
		require.NoError(t, err)

		assert.Equal(t, "123", capturedReq.Header.Get(grafana_client.OrgIDHeader))
	})

	t.Run("does not add header when org ID is zero", func(t *testing.T) {
		var capturedReq *http.Request
		mockRT := &capturingMockRT{
			fn: func(req *http.Request) (*http.Response, error) {
				capturedReq = req
				return &http.Response{StatusCode: 200}, nil
			},
		}

		rt := NewOrgIDRoundTripper(mockRT, 0)

		req, _ := http.NewRequest("GET", "http://example.com", nil)
		_, err := rt.RoundTrip(req)
		require.NoError(t, err)

		assert.Empty(t, capturedReq.Header.Get(grafana_client.OrgIDHeader))
	})

	t.Run("does not modify original request", func(t *testing.T) {
		mockRT := &capturingMockRT{
			fn: func(req *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200}, nil
			},
		}

		rt := NewOrgIDRoundTripper(mockRT, 42)

		req, _ := http.NewRequest("GET", "http://example.com", nil)
		_, err := rt.RoundTrip(req)
		require.NoError(t, err)

		assert.Empty(t, req.Header.Get(grafana_client.OrgIDHeader))
	})

	t.Run("nil transport uses default", func(t *testing.T) {
		rt := NewOrgIDRoundTripper(nil, 1)
		assert.NotNil(t, rt.underlying)
	})
}

func TestNewGrafanaClientOrgIDTransport(t *testing.T) {
	t.Run("org ID header is sent on requests when configured", func(t *testing.T) {
		var capturedHeaders http.Header
		ts := newTestHTTPServer(t, func(w http.ResponseWriter, r *http.Request) {
			capturedHeaders = r.Header.Clone()
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`[]`))
		})

		ctx := WithGrafanaConfig(context.Background(), GrafanaConfig{
			OrgID: 99,
		})
		c := NewGrafanaClient(ctx, ts.URL, "test-key", nil)
		require.NotNil(t, c)

		// Make a real request through the client
		_, _ = c.Search.Search(nil, nil)

		assert.Equal(t, "99", capturedHeaders.Get(grafana_client.OrgIDHeader))
	})

	t.Run("org ID header is not sent when org ID is zero", func(t *testing.T) {
		var capturedHeaders http.Header
		ts := newTestHTTPServer(t, func(w http.ResponseWriter, r *http.Request) {
			capturedHeaders = r.Header.Clone()
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`[]`))
		})

		ctx := WithGrafanaConfig(context.Background(), GrafanaConfig{
			OrgID: 0,
		})
		c := NewGrafanaClient(ctx, ts.URL, "test-key", nil)
		require.NotNil(t, c)

		_, _ = c.Search.Search(nil, nil)

		assert.Empty(t, capturedHeaders.Get(grafana_client.OrgIDHeader))
	})
}

func newTestHTTPServer(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)
	return ts
}

// clearPublicURLCache removes all entries from the publicURLCache for test isolation.
func clearPublicURLCache() {
	publicURLCache.Range(func(key, _ any) bool {
		publicURLCache.Delete(key)
		return true
	})
}

func TestFetchPublicURL(t *testing.T) {
	t.Cleanup(clearPublicURLCache)

	t.Run("fetches appUrl from frontend settings", func(t *testing.T) {
		ts := newTestHTTPServer(t, func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/api/frontend/settings" {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"appUrl": "https://grafana.example.com/"}`))
				return
			}
			w.WriteHeader(http.StatusNotFound)
		})

		publicURL := fetchPublicURL(context.Background(), ts.URL, "test-key", nil, nil, nil)
		assert.Equal(t, "https://grafana.example.com", publicURL)
	})

	t.Run("returns empty string when endpoint returns error", func(t *testing.T) {
		ts := newTestHTTPServer(t, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		})

		publicURL := fetchPublicURL(context.Background(), ts.URL, "test-key", nil, nil, nil)
		assert.Equal(t, "", publicURL)
	})

	t.Run("returns empty string when appUrl is empty", func(t *testing.T) {
		ts := newTestHTTPServer(t, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"appUrl": ""}`))
		})

		publicURL := fetchPublicURL(context.Background(), ts.URL, "test-key", nil, nil, nil)
		assert.Equal(t, "", publicURL)
	})

	t.Run("returns empty string for invalid JSON", func(t *testing.T) {
		ts := newTestHTTPServer(t, func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`not json`))
		})

		publicURL := fetchPublicURL(context.Background(), ts.URL, "test-key", nil, nil, nil)
		assert.Equal(t, "", publicURL)
	})

	t.Run("sends authorization header with API key", func(t *testing.T) {
		var capturedAuth string
		ts := newTestHTTPServer(t, func(w http.ResponseWriter, r *http.Request) {
			capturedAuth = r.Header.Get("Authorization")
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"appUrl": "https://grafana.example.com"}`))
		})

		fetchPublicURL(context.Background(), ts.URL, "my-token", nil, nil, nil)
		assert.Equal(t, "Bearer my-token", capturedAuth)
	})

	t.Run("sends basic auth credentials", func(t *testing.T) {
		var capturedUser, capturedPass string
		ts := newTestHTTPServer(t, func(w http.ResponseWriter, r *http.Request) {
			capturedUser, capturedPass, _ = r.BasicAuth()
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"appUrl": "https://grafana.example.com"}`))
		})

		auth := url.UserPassword("admin", "secret")
		fetchPublicURL(context.Background(), ts.URL, "", auth, nil, nil)
		assert.Equal(t, "admin", capturedUser)
		assert.Equal(t, "secret", capturedPass)
	})

	t.Run("trims trailing slash from appUrl", func(t *testing.T) {
		ts := newTestHTTPServer(t, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"appUrl": "https://grafana.example.com/"}`))
		})

		publicURL := fetchPublicURL(context.Background(), ts.URL, "", nil, nil, nil)
		assert.Equal(t, "https://grafana.example.com", publicURL)
	})

	t.Run("forwards extra headers", func(t *testing.T) {
		var capturedHeaders http.Header
		ts := newTestHTTPServer(t, func(w http.ResponseWriter, r *http.Request) {
			capturedHeaders = r.Header.Clone()
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"appUrl": "https://grafana.example.com"}`))
		})

		extraHeaders := map[string]string{
			"X-Custom-Auth":   "proxy-token-123",
			"X-Forwarded-For": "10.0.0.1",
		}
		fetchPublicURL(context.Background(), ts.URL, "", nil, nil, extraHeaders)
		assert.Equal(t, "proxy-token-123", capturedHeaders.Get("X-Custom-Auth"))
		assert.Equal(t, "10.0.0.1", capturedHeaders.Get("X-Forwarded-For"))
	})

	t.Run("caches results per grafana URL", func(t *testing.T) {
		callCount := 0
		ts := newTestHTTPServer(t, func(w http.ResponseWriter, r *http.Request) {
			callCount++
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"appUrl": "https://grafana.example.com"}`))
		})

		// First call should hit the server
		publicURL := fetchPublicURL(context.Background(), ts.URL, "test-key", nil, nil, nil)
		assert.Equal(t, "https://grafana.example.com", publicURL)
		assert.Equal(t, 1, callCount)

		// Second call should use cache
		publicURL = fetchPublicURL(context.Background(), ts.URL, "test-key", nil, nil, nil)
		assert.Equal(t, "https://grafana.example.com", publicURL)
		assert.Equal(t, 1, callCount) // no additional HTTP call
	})

	t.Run("retries on failure instead of caching errors permanently", func(t *testing.T) {
		callCount := 0
		ts := newTestHTTPServer(t, func(w http.ResponseWriter, r *http.Request) {
			callCount++
			if callCount == 1 {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"appUrl": "https://grafana.example.com"}`))
		})

		// First call fails
		publicURL := fetchPublicURL(context.Background(), ts.URL, "test-key", nil, nil, nil)
		assert.Equal(t, "", publicURL)
		assert.Equal(t, 1, callCount)

		// Second call retries and succeeds (failures are not cached)
		publicURL = fetchPublicURL(context.Background(), ts.URL, "test-key", nil, nil, nil)
		assert.Equal(t, "https://grafana.example.com", publicURL)
		assert.Equal(t, 2, callCount)

		// Third call uses cached success
		publicURL = fetchPublicURL(context.Background(), ts.URL, "test-key", nil, nil, nil)
		assert.Equal(t, "https://grafana.example.com", publicURL)
		assert.Equal(t, 2, callCount) // no additional HTTP call
	})
}

func TestNewGrafanaClientFetchesPublicURL(t *testing.T) {
	t.Cleanup(clearPublicURLCache)

	t.Run("stores public URL from frontend settings", func(t *testing.T) {
		ts := newTestHTTPServer(t, func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/api/frontend/settings" {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"appUrl": "https://public.grafana.example.com/"}`))
				return
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`[]`))
		})

		ctx := WithGrafanaConfig(context.Background(), GrafanaConfig{})
		gc := NewGrafanaClient(ctx, ts.URL, "test-key", nil)
		assert.Equal(t, "https://public.grafana.example.com", gc.PublicURL)
	})

	t.Run("public URL is empty when fetch fails", func(t *testing.T) {
		ts := newTestHTTPServer(t, func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/api/frontend/settings" {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`[]`))
		})

		ctx := WithGrafanaConfig(context.Background(), GrafanaConfig{})
		gc := NewGrafanaClient(ctx, ts.URL, "test-key", nil)
		assert.Equal(t, "", gc.PublicURL)
	})
}
