//go:build unit
// +build unit

package mcpgrafana

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/go-openapi/runtime/client"
	"github.com/grafana/authlib/authn"
	grafana_client "github.com/grafana/grafana-openapi-client-go/client"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
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
func minURLFromClient(c *grafana_client.GrafanaHTTPAPI) minURL {
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
			Params: struct {
				Name      string    `json:"name"`
				Arguments any       `json:"arguments,omitempty"`
				Meta      *mcp.Meta `json:"_meta,omitempty"`
			}{
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
		assert.Equal(t, "mcp.tool.test_tool", span.Name())
		assert.Equal(t, codes.Ok, span.Status().Code)

		// Check attributes
		attributes := span.Attributes()
		assertHasAttribute(t, attributes, "mcp.tool.name", "test_tool")
		assertHasAttribute(t, attributes, "mcp.tool.description", "A test tool for tracing")
		assertHasAttribute(t, attributes, "mcp.tool.arguments", `{"message":"world"}`)
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
			Params: struct {
				Name      string    `json:"name"`
				Arguments any       `json:"arguments,omitempty"`
				Meta      *mcp.Meta `json:"_meta,omitempty"`
			}{
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
		assert.Equal(t, "mcp.tool.failing_tool", span.Name())
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
			Params: struct {
				Name      string    `json:"name"`
				Arguments any       `json:"arguments,omitempty"`
				Meta      *mcp.Meta `json:"_meta,omitempty"`
			}{
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
		assert.Equal(t, "mcp.tool.context_prop_tool", span.Name())
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
			Params: struct {
				Name      string    `json:"name"`
				Arguments any       `json:"arguments,omitempty"`
				Meta      *mcp.Meta `json:"_meta,omitempty"`
			}{
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
		assert.Equal(t, "mcp.tool.sensitive_tool", span.Name())
		assert.Equal(t, codes.Ok, span.Status().Code)

		// Check that arguments are NOT logged (PII safety)
		attributes := span.Attributes()
		assertHasAttribute(t, attributes, "mcp.tool.name", "sensitive_tool")
		assertHasAttribute(t, attributes, "mcp.tool.description", "A tool with sensitive data")

		// Verify arguments are NOT present
		for _, attr := range attributes {
			assert.NotEqual(t, "mcp.tool.arguments", string(attr.Key), "Arguments should not be logged by default for PII safety")
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
			Params: struct {
				Name      string    `json:"name"`
				Arguments any       `json:"arguments,omitempty"`
				Meta      *mcp.Meta `json:"_meta,omitempty"`
			}{
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
		assert.Equal(t, "mcp.tool.debug_tool", span.Name())
		assert.Equal(t, codes.Ok, span.Status().Code)

		// Check that arguments ARE logged when flag enabled
		attributes := span.Attributes()
		assertHasAttribute(t, attributes, "mcp.tool.name", "debug_tool")
		assertHasAttribute(t, attributes, "mcp.tool.description", "A tool for debugging")
		assertHasAttribute(t, attributes, "mcp.tool.arguments", `{"safeData":"debug-value"}`)
	})
}

func TestHTTPTracingConfiguration(t *testing.T) {
	t.Run("HTTP tracing always enabled for context propagation", func(t *testing.T) {
		// Create context (HTTP tracing always enabled)
		config := GrafanaConfig{}
		ctx := WithGrafanaConfig(context.Background(), config)

		// Create Grafana client
		client := NewGrafanaClient(ctx, "http://localhost:3000", "test-api-key", nil, 0)
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
		client := NewGrafanaClient(ctx, "http://localhost:3000", "test-api-key", nil, 0)
		require.NotNil(t, client)

		// Verify the client was created successfully
		assert.NotNil(t, client.Transport)
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

type extraHeadersMockRT struct {
	fn func(*http.Request) (*http.Response, error)
}

func (m *extraHeadersMockRT) RoundTrip(req *http.Request) (*http.Response, error) {
	return m.fn(req)
}

func TestCloudAccessPolicyTokenFromEnv(t *testing.T) {
	t.Run("token set in env", func(t *testing.T) {
		t.Setenv("GRAFANA_CLOUD_ACCESS_POLICY_TOKEN", "glc_test-token-123")
		ctx := ExtractGrafanaInfoFromEnv(context.Background())
		config := GrafanaConfigFromContext(ctx)
		assert.Equal(t, "glc_test-token-123", config.CloudAccessPolicyToken)
	})

	t.Run("token not set", func(t *testing.T) {
		t.Setenv("GRAFANA_CLOUD_ACCESS_POLICY_TOKEN", "")
		ctx := ExtractGrafanaInfoFromEnv(context.Background())
		config := GrafanaConfigFromContext(ctx)
		assert.Equal(t, "", config.CloudAccessPolicyToken)
	})
}

func TestCloudAccessPolicyTokenFromHeaders(t *testing.T) {
	t.Run("token from header", func(t *testing.T) {
		req, err := http.NewRequest("GET", "http://example.com", nil)
		require.NoError(t, err)
		req.Header.Set(grafanaCloudAccessPolicyTokenHeader, "glc_header-token")
		ctx := ExtractGrafanaInfoFromHeaders(context.Background(), req)
		config := GrafanaConfigFromContext(ctx)
		assert.Equal(t, "glc_header-token", config.CloudAccessPolicyToken)
	})

	t.Run("token from env when no header", func(t *testing.T) {
		t.Setenv("GRAFANA_CLOUD_ACCESS_POLICY_TOKEN", "glc_env-token")
		req, err := http.NewRequest("GET", "http://example.com", nil)
		require.NoError(t, err)
		ctx := ExtractGrafanaInfoFromHeaders(context.Background(), req)
		config := GrafanaConfigFromContext(ctx)
		assert.Equal(t, "glc_env-token", config.CloudAccessPolicyToken)
	})

	t.Run("header takes precedence over env", func(t *testing.T) {
		t.Setenv("GRAFANA_CLOUD_ACCESS_POLICY_TOKEN", "glc_env-token")
		req, err := http.NewRequest("GET", "http://example.com", nil)
		require.NoError(t, err)
		req.Header.Set(grafanaCloudAccessPolicyTokenHeader, "glc_header-token")
		ctx := ExtractGrafanaInfoFromHeaders(context.Background(), req)
		config := GrafanaConfigFromContext(ctx)
		assert.Equal(t, "glc_header-token", config.CloudAccessPolicyToken)
	})
}

func TestTokenExchangeTransport(t *testing.T) {
	t.Run("exchanges token and sets X-Access-Token header via BuildTransport", func(t *testing.T) {
		var capturedReq *http.Request
		mockRT := &extraHeadersMockRT{
			fn: func(req *http.Request) (*http.Response, error) {
				capturedReq = req
				return &http.Response{StatusCode: 200}, nil
			},
		}

		cfg := &GrafanaConfig{
			CloudAccessPolicyToken: "glc_my-token",
			TokenExchanger:         authn.NewStaticTokenExchanger("exchanged-access-token"),
			TokenExchangeNamespace: "stack-123",
		}
		transport, err := BuildTransport(cfg, mockRT)
		require.NoError(t, err)

		req, _ := http.NewRequest("GET", "http://example.com", nil)
		_, err = transport.RoundTrip(req)
		require.NoError(t, err)

		// The exchanged token (not the raw CAP token) should be in X-Access-Token
		assert.Equal(t, "exchanged-access-token", capturedReq.Header.Get("X-Access-Token"))
	})

	t.Run("no X-Access-Token when no exchanger configured", func(t *testing.T) {
		var capturedReq *http.Request
		mockRT := &extraHeadersMockRT{
			fn: func(req *http.Request) (*http.Response, error) {
				capturedReq = req
				return &http.Response{StatusCode: 200}, nil
			},
		}

		cfg := &GrafanaConfig{}
		transport, err := BuildTransport(cfg, mockRT)
		require.NoError(t, err)

		req, _ := http.NewRequest("GET", "http://example.com", nil)
		_, err = transport.RoundTrip(req)
		require.NoError(t, err)

		assert.Equal(t, "", capturedReq.Header.Get("X-Access-Token"))
	})

	t.Run("skipped when on-behalf-of auth is active", func(t *testing.T) {
		var capturedReq *http.Request
		mockRT := &extraHeadersMockRT{
			fn: func(req *http.Request) (*http.Response, error) {
				capturedReq = req
				return &http.Response{StatusCode: 200}, nil
			},
		}

		cfg := &GrafanaConfig{
			CloudAccessPolicyToken: "glc_my-token",
			TokenExchanger:         authn.NewStaticTokenExchanger("exchanged-access-token"),
			TokenExchangeNamespace: "stack-123",
			AccessToken:            "obo-access-token",
		}
		transport, err := BuildTransport(cfg, mockRT)
		require.NoError(t, err)

		req, _ := http.NewRequest("GET", "http://example.com", nil)
		_, err = transport.RoundTrip(req)
		require.NoError(t, err)

		// Token exchange should NOT happen when OBO auth is active
		assert.Equal(t, "", capturedReq.Header.Get("X-Access-Token"))
	})

	t.Run("works alongside extra headers", func(t *testing.T) {
		var capturedReq *http.Request
		mockRT := &extraHeadersMockRT{
			fn: func(req *http.Request) (*http.Response, error) {
				capturedReq = req
				return &http.Response{StatusCode: 200}, nil
			},
		}

		cfg := &GrafanaConfig{
			CloudAccessPolicyToken: "glc_my-token",
			TokenExchanger:         authn.NewStaticTokenExchanger("exchanged-access-token"),
			TokenExchangeNamespace: "stack-123",
			ExtraHeaders:           map[string]string{"X-Custom": "value"},
		}
		transport, err := BuildTransport(cfg, mockRT)
		require.NoError(t, err)

		req, _ := http.NewRequest("GET", "http://example.com", nil)
		_, err = transport.RoundTrip(req)
		require.NoError(t, err)

		assert.Equal(t, "exchanged-access-token", capturedReq.Header.Get("X-Access-Token"))
		assert.Equal(t, "value", capturedReq.Header.Get("X-Custom"))
	})

	t.Run("exchange error is propagated", func(t *testing.T) {
		mockRT := &extraHeadersMockRT{
			fn: func(req *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200}, nil
			},
		}

		cfg := &GrafanaConfig{
			TokenExchanger:         &failingTokenExchanger{err: errors.New("auth service unavailable")},
			TokenExchangeNamespace: "stack-123",
		}
		transport, err := BuildTransport(cfg, mockRT)
		require.NoError(t, err)

		req, _ := http.NewRequest("GET", "http://example.com", nil)
		_, err = transport.RoundTrip(req)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "token exchange failed")
		assert.Contains(t, err.Error(), "auth service unavailable")
	})

	t.Run("no exchanger created when exchange URL missing", func(t *testing.T) {
		// newTokenExchanger returns nil when exchange URL is empty
		exchanger := newTokenExchanger("glc_my-token", "")
		assert.Nil(t, exchanger)
	})

	t.Run("no exchanger created when CAP token missing", func(t *testing.T) {
		exchanger := newTokenExchanger("", "https://auth-api.grafana.net/v1/sign-access-token")
		assert.Nil(t, exchanger)
	})
}

// failingTokenExchanger is a test helper that always returns an error.
type failingTokenExchanger struct {
	err error
}

func (f *failingTokenExchanger) Exchange(_ context.Context, _ authn.TokenExchangeRequest) (*authn.TokenExchangeResponse, error) {
	return nil, f.err
}

// Ensure failingTokenExchanger implements authn.TokenExchanger at compile time.
var _ authn.TokenExchanger = (*failingTokenExchanger)(nil)

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
