package tools

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func resetFallbackCache() {
	fallbackEndpoints.Range(func(key, _ any) bool {
		fallbackEndpoints.Delete(key)
		return true
	})
}

// mockTransport records requests and returns canned responses based on URL path.
type mockTransport struct {
	responses map[string]*http.Response // path prefix -> response
	requests  []*http.Request
}

func (m *mockTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	m.requests = append(m.requests, req)
	for prefix, resp := range m.responses {
		if strings.Contains(req.URL.Path, prefix) {
			return resp, nil
		}
	}
	return &http.Response{StatusCode: http.StatusNotFound, Body: io.NopCloser(strings.NewReader("not found"))}, nil
}

func newMockResponse(status int) *http.Response {
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(strings.NewReader("")),
	}
}

func TestDatasourceFallbackTransport_PrimarySucceeds(t *testing.T) {
	resetFallbackCache()

	mock := &mockTransport{
		responses: map[string]*http.Response{
			"/api/datasources/uid/test-uid/resources": newMockResponse(http.StatusOK),
		},
	}

	rt := newDatasourceFallbackTransport(mock,
		"/api/datasources/uid/test-uid/resources",
		"/api/datasources/proxy/uid/test-uid",
	)

	req, _ := http.NewRequest("POST", "http://grafana.example.com/api/datasources/uid/test-uid/resources/api/v1/query", nil)
	resp, err := rt.RoundTrip(req)

	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Len(t, mock.requests, 1, "should not retry when primary succeeds")
}

func TestDatasourceFallbackTransport_FallbackOn403(t *testing.T) {
	resetFallbackCache()

	mock := &mockTransport{
		responses: map[string]*http.Response{
			"/api/datasources/uid/test-uid/resources": newMockResponse(http.StatusForbidden),
			"/api/datasources/proxy/uid/test-uid":     newMockResponse(http.StatusOK),
		},
	}

	rt := newDatasourceFallbackTransport(mock,
		"/api/datasources/uid/test-uid/resources",
		"/api/datasources/proxy/uid/test-uid",
	)

	req, _ := http.NewRequest("POST", "http://grafana.example.com/api/datasources/uid/test-uid/resources/api/v1/query", nil)
	resp, err := rt.RoundTrip(req)

	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Len(t, mock.requests, 2, "should retry with fallback on 403")
	assert.Contains(t, mock.requests[1].URL.Path, "/api/datasources/proxy/uid/test-uid/api/v1/query")
}

func TestDatasourceFallbackTransport_FallbackOn500(t *testing.T) {
	resetFallbackCache()

	mock := &mockTransport{
		responses: map[string]*http.Response{
			"/api/datasources/uid/test-uid/resources": newMockResponse(http.StatusInternalServerError),
			"/api/datasources/proxy/uid/test-uid":     newMockResponse(http.StatusOK),
		},
	}

	rt := newDatasourceFallbackTransport(mock,
		"/api/datasources/uid/test-uid/resources",
		"/api/datasources/proxy/uid/test-uid",
	)

	req, _ := http.NewRequest("POST", "http://grafana.example.com/api/datasources/uid/test-uid/resources/api/v1/query", nil)
	resp, err := rt.RoundTrip(req)

	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Len(t, mock.requests, 2, "should retry with fallback on 500")
}

func TestDatasourceFallbackTransport_CachesFallback(t *testing.T) {
	resetFallbackCache()

	mock := &mockTransport{
		responses: map[string]*http.Response{
			"/api/datasources/uid/test-uid/resources": newMockResponse(http.StatusForbidden),
			"/api/datasources/proxy/uid/test-uid":     newMockResponse(http.StatusOK),
		},
	}

	rt := newDatasourceFallbackTransport(mock,
		"/api/datasources/uid/test-uid/resources",
		"/api/datasources/proxy/uid/test-uid",
	)

	// First request: discovers fallback is needed (2 round trips).
	req1, _ := http.NewRequest("GET", "http://grafana.example.com/api/datasources/uid/test-uid/resources/api/v1/labels", nil)
	_, err := rt.RoundTrip(req1)
	require.NoError(t, err)
	assert.Len(t, mock.requests, 2)

	// Second request: uses cached fallback directly (1 round trip).
	req2, _ := http.NewRequest("GET", "http://grafana.example.com/api/datasources/uid/test-uid/resources/api/v1/query", nil)
	resp, err := rt.RoundTrip(req2)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Len(t, mock.requests, 3, "cached fallback should skip the primary attempt")
	assert.Contains(t, mock.requests[2].URL.Path, "/api/datasources/proxy/uid/test-uid/api/v1/query")
}

func TestDatasourceFallbackTransport_BothFail(t *testing.T) {
	resetFallbackCache()

	mock := &mockTransport{
		responses: map[string]*http.Response{
			"/api/datasources/uid/test-uid/resources": newMockResponse(http.StatusForbidden),
			"/api/datasources/proxy/uid/test-uid":     newMockResponse(http.StatusForbidden),
		},
	}

	rt := newDatasourceFallbackTransport(mock,
		"/api/datasources/uid/test-uid/resources",
		"/api/datasources/proxy/uid/test-uid",
	)

	req, _ := http.NewRequest("GET", "http://grafana.example.com/api/datasources/uid/test-uid/resources/api/v1/query", nil)
	resp, err := rt.RoundTrip(req)

	require.NoError(t, err)
	assert.Equal(t, http.StatusForbidden, resp.StatusCode, "should return fallback response when both fail")
	assert.Len(t, mock.requests, 2)
}

func TestDatasourceFallbackTransport_PreservesPostBody(t *testing.T) {
	resetFallbackCache()

	mock := &mockTransport{
		responses: map[string]*http.Response{
			"/api/datasources/uid/test-uid/resources": newMockResponse(http.StatusForbidden),
			"/api/datasources/proxy/uid/test-uid":     newMockResponse(http.StatusOK),
		},
	}

	rt := newDatasourceFallbackTransport(mock,
		"/api/datasources/uid/test-uid/resources",
		"/api/datasources/proxy/uid/test-uid",
	)

	body := "query=up&time=1234567890"
	req, _ := http.NewRequest("POST", "http://grafana.example.com/api/datasources/uid/test-uid/resources/api/v1/query",
		strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := rt.RoundTrip(req)

	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	require.Len(t, mock.requests, 2)

	// Verify the retry request had the body.
	retryBody, err := io.ReadAll(mock.requests[1].Body)
	require.NoError(t, err)
	assert.Equal(t, body, string(retryBody))
}

func TestDatasourceFallbackTransport_NoRetryOn4xx(t *testing.T) {
	resetFallbackCache()

	mock := &mockTransport{
		responses: map[string]*http.Response{
			"/api/datasources/uid/test-uid/resources": newMockResponse(http.StatusBadRequest),
		},
	}

	rt := newDatasourceFallbackTransport(mock,
		"/api/datasources/uid/test-uid/resources",
		"/api/datasources/proxy/uid/test-uid",
	)

	req, _ := http.NewRequest("GET", "http://grafana.example.com/api/datasources/uid/test-uid/resources/api/v1/query", nil)
	resp, err := rt.RoundTrip(req)

	require.NoError(t, err)
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.Len(t, mock.requests, 1, "should not retry on non-403/500 errors")
}

func TestDatasourceProxyPaths(t *testing.T) {
	resources, proxy := datasourceProxyPaths("my-uid-123")
	assert.Equal(t, "/api/datasources/uid/my-uid-123/resources", resources)
	assert.Equal(t, "/api/datasources/proxy/uid/my-uid-123", proxy)
}
