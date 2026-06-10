package tools

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	mcpgrafana "github.com/grafana/mcp-grafana"
	"github.com/itchyny/gojq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func apiTestContext(t *testing.T, serverURL string) context.Context {
	t.Helper()
	cfg := mcpgrafana.GrafanaConfig{URL: serverURL}
	return mcpgrafana.WithGrafanaConfig(context.Background(), cfg)
}

func TestAPIRequest_GET(t *testing.T) {
	payload := map[string]any{"name": "Main Org.", "id": float64(1)}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/org", r.URL.Path)
		assert.Equal(t, http.MethodGet, r.Method)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(payload)
	}))
	t.Cleanup(ts.Close)

	ctx := apiTestContext(t, ts.URL)
	result, err := apiRequest(ctx, APIRequestParams{Endpoint: "/api/org"})

	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, result.Status)
	assert.Equal(t, "application/json", result.Headers["Content-Type"])
	data, ok := result.Data.(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "Main Org.", data["name"])
}

func TestAPIRequest_DefaultMethodIsGET(t *testing.T) {
	var capturedMethod string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedMethod = r.Method
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(ts.Close)

	ctx := apiTestContext(t, ts.URL)
	_, err := apiRequest(ctx, APIRequestParams{Endpoint: "/api/org"})

	require.NoError(t, err)
	assert.Equal(t, http.MethodGet, capturedMethod)
}

func TestAPIRequest_POST(t *testing.T) {
	var capturedMethod string
	var capturedBody string
	var capturedContentType string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedMethod = r.Method
		capturedContentType = r.Header.Get("Content-Type")
		body, _ := io.ReadAll(r.Body)
		capturedBody = string(body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"message":"created"}`))
	}))
	t.Cleanup(ts.Close)

	ctx := apiTestContext(t, ts.URL)
	result, err := apiRequest(ctx, APIRequestParams{
		Endpoint: "/api/annotations",
		Method:   "POST",
		Body:     `{"text":"test annotation"}`,
	})

	require.NoError(t, err)
	assert.Equal(t, http.MethodPost, capturedMethod)
	assert.Equal(t, "application/json", capturedContentType)
	assert.Equal(t, `{"text":"test annotation"}`, capturedBody)
	assert.Equal(t, http.StatusOK, result.Status)
}

func TestAPIRequest_CustomHeaders(t *testing.T) {
	var capturedHeaders http.Header
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedHeaders = r.Header
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(ts.Close)

	ctx := apiTestContext(t, ts.URL)
	_, err := apiRequest(ctx, APIRequestParams{
		Endpoint: "/api/org",
		Headers:  map[string]string{"X-Custom-Header": "custom-value"},
	})

	require.NoError(t, err)
	assert.Equal(t, "custom-value", capturedHeaders.Get("X-Custom-Header"))
}

func TestAPIRequest_JQFilter(t *testing.T) {
	payload := map[string]any{
		"dashboards": []any{
			map[string]any{"title": "Dashboard 1", "uid": "abc"},
			map[string]any{"title": "Dashboard 2", "uid": "def"},
		},
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(payload)
	}))
	t.Cleanup(ts.Close)

	ctx := apiTestContext(t, ts.URL)
	result, err := apiRequest(ctx, APIRequestParams{
		Endpoint: "/api/search",
		JQ:       ".dashboards | length",
	})

	require.NoError(t, err)
	assert.Equal(t, 2, result.Data)
}

func TestAPIRequest_JQFilterMultipleResults(t *testing.T) {
	payload := map[string]any{
		"items": []any{"a", "b", "c"},
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(payload)
	}))
	t.Cleanup(ts.Close)

	ctx := apiTestContext(t, ts.URL)
	result, err := apiRequest(ctx, APIRequestParams{
		Endpoint: "/api/test",
		JQ:       ".items[]",
	})

	require.NoError(t, err)
	data, ok := result.Data.([]any)
	require.True(t, ok)
	assert.Equal(t, []any{"a", "b", "c"}, data)
}

func TestAPIRequest_InvalidJQExpression(t *testing.T) {
	ctx := apiTestContext(t, "http://localhost")
	_, err := apiRequest(ctx, APIRequestParams{
		Endpoint: "/api/org",
		JQ:       "invalid[[[",
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid jq expression")
}

func TestAPIRequest_NonJSONResponse(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("plain text response"))
	}))
	t.Cleanup(ts.Close)

	ctx := apiTestContext(t, ts.URL)
	result, err := apiRequest(ctx, APIRequestParams{Endpoint: "/api/health"})

	require.NoError(t, err)
	assert.Equal(t, "plain text response", result.Data)
}

func TestAPIRequest_RelativeURLRequired(t *testing.T) {
	ctx := apiTestContext(t, "http://localhost")
	_, err := apiRequest(ctx, APIRequestParams{Endpoint: "api/org"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "must be a relative path starting with '/'")
}

func TestAPIRequest_InvalidMethod(t *testing.T) {
	ctx := apiTestContext(t, "http://localhost")
	_, err := apiRequest(ctx, APIRequestParams{
		Endpoint: "/api/org",
		Method:   "INVALID",
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported HTTP method")
}

func TestAPIRequest_NoURLConfigured(t *testing.T) {
	ctx := mcpgrafana.WithGrafanaConfig(context.Background(), mcpgrafana.GrafanaConfig{})
	_, err := apiRequest(ctx, APIRequestParams{Endpoint: "/api/org"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "grafana URL is not configured")
}

func TestAPIRequest_AuthHeadersIncluded(t *testing.T) {
	var capturedAuth string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(ts.Close)

	cfg := mcpgrafana.GrafanaConfig{URL: ts.URL, APIKey: "glsa_test_token"}
	ctx := mcpgrafana.WithGrafanaConfig(context.Background(), cfg)
	_, err := apiRequest(ctx, APIRequestParams{Endpoint: "/api/org"})

	require.NoError(t, err)
	assert.Equal(t, "Bearer glsa_test_token", capturedAuth)
}

func TestAPIRequest_ReadOnlyRejectsNonGET(t *testing.T) {
	ctx := apiTestContext(t, "http://localhost")

	for _, method := range []string{"POST", "PUT", "PATCH", "DELETE"} {
		_, err := apiRequestReadOnly(ctx, APIRequestReadOnlyParams{
			Endpoint: "/api/org",
			Method:   method,
		})
		require.Error(t, err, "method %s should be rejected", method)
		assert.Contains(t, err.Error(), "not allowed in read-only mode")
	}
}

func TestAPIRequest_ReadOnlyAllowsGET(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(ts.Close)

	ctx := apiTestContext(t, ts.URL)
	result, err := apiRequestReadOnly(ctx, APIRequestReadOnlyParams{Endpoint: "/api/org"})

	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, result.Status)
}

func TestAPIRequest_ReadOnlyDefaultMethodIsGET(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodGet, r.Method)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(ts.Close)

	ctx := apiTestContext(t, ts.URL)
	_, err := apiRequestReadOnly(ctx, APIRequestReadOnlyParams{Endpoint: "/api/org"})
	require.NoError(t, err)
}

func TestAPIRequest_ReadOnlyParamsHasNoBodyField(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		assert.Empty(t, body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(ts.Close)

	ctx := apiTestContext(t, ts.URL)
	_, err := apiRequestReadOnly(ctx, APIRequestReadOnlyParams{Endpoint: "/api/org"})
	require.NoError(t, err)
}

func TestAPIRequest_MethodCaseInsensitive(t *testing.T) {
	var capturedMethod string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedMethod = r.Method
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(ts.Close)

	ctx := apiTestContext(t, ts.URL)
	_, err := apiRequest(ctx, APIRequestParams{
		Endpoint: "/api/org",
		Method:   "post",
	})

	require.NoError(t, err)
	assert.Equal(t, http.MethodPost, capturedMethod)
}

func TestAPIRequest_ResponseTooLarge(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// Write more than defaultResponseLimitBytes
		_, _ = w.Write(make([]byte, defaultResponseLimitBytes+1))
	}))
	t.Cleanup(ts.Close)

	ctx := apiTestContext(t, ts.URL)
	_, err := apiRequest(ctx, APIRequestParams{Endpoint: "/api/large"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "exceeds maximum size")
}

func TestAPIRequest_JQWithNonJSONResponseReturnsText(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("plain text response"))
	}))
	t.Cleanup(ts.Close)

	ctx := apiTestContext(t, ts.URL)
	result, err := apiRequest(ctx, APIRequestParams{
		Endpoint: "/api/health",
		JQ:       ".status",
	})

	require.NoError(t, err)
	assert.Equal(t, "plain text response", result.Data)
}

func TestAPIRequest_JQRespectsContextCancellation(t *testing.T) {
	// Test applyJQ directly to verify RunWithContext propagates cancellation,
	// avoiding the HTTP layer which would fail on a cancelled context first.
	query, err := gojq.Parse("while(true; . + 1)")
	require.NoError(t, err)
	code, err := gojq.Compile(query)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err = applyJQ(ctx, code, float64(1))
	require.Error(t, err)
}

func TestAPIRequest_HTTPErrorStatus(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"Not found"}`))
	}))
	t.Cleanup(ts.Close)

	ctx := apiTestContext(t, ts.URL)
	result, err := apiRequest(ctx, APIRequestParams{Endpoint: "/api/dashboards/uid/nonexistent"})

	require.NoError(t, err)
	assert.Equal(t, http.StatusNotFound, result.Status)
	data, ok := result.Data.(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "Not found", data["message"])
}
