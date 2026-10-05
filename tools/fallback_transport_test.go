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

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
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

func TestDatasourceFallbackTransport_FallbackOn401(t *testing.T) {
	resetFallbackCache()

	mock := &mockTransport{
		responses: map[string]*http.Response{
			"/api/datasources/uid/test-uid/resources": newMockResponse(http.StatusUnauthorized),
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
	assert.Len(t, mock.requests, 2, "should retry with fallback on 401")
	assert.Contains(t, mock.requests[1].URL.Path, "/api/datasources/proxy/uid/test-uid/api/v1/query")
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

func TestDatasourceFallbackTransport_CachesFallbackPerRequestPath(t *testing.T) {
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

	// Same request path: uses cached fallback directly (1 round trip).
	req2, _ := http.NewRequest("GET", "http://grafana.example.com/api/datasources/uid/test-uid/resources/api/v1/labels", nil)
	resp, err := rt.RoundTrip(req2)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Len(t, mock.requests, 3, "same path should use cached fallback")
	assert.Contains(t, mock.requests[2].URL.Path, "/api/datasources/proxy/uid/test-uid/api/v1/labels")

	// Different request path: tries primary first because /resources and /proxy
	// compatibility can differ between datasource API endpoints.
	req3, _ := http.NewRequest("GET", "http://grafana.example.com/api/datasources/uid/test-uid/resources/api/v1/query", nil)
	resp, err = rt.RoundTrip(req3)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Len(t, mock.requests, 5, "different path should not inherit fallback cache")
	assert.Contains(t, mock.requests[3].URL.Path, "/api/datasources/uid/test-uid/resources/api/v1/query")
	assert.Contains(t, mock.requests[4].URL.Path, "/api/datasources/proxy/uid/test-uid/api/v1/query")
}

func TestDatasourceFallbackTransport_LokiPatternsDoesNotInheritFallback(t *testing.T) {
	resetFallbackCache()

	var requests []*http.Request
	mock := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		requests = append(requests, req)

		switch {
		case strings.Contains(req.URL.Path, "/api/datasources/proxy/uid/test-uid/api/v1/labels"):
			return newMockResponse(http.StatusForbidden), nil
		case strings.Contains(req.URL.Path, "/api/datasources/uid/test-uid/resources/api/v1/labels"):
			return newMockResponse(http.StatusOK), nil
		case strings.Contains(req.URL.Path, "/api/datasources/proxy/uid/test-uid/loki/api/v1/patterns"):
			return newMockResponse(http.StatusOK), nil
		case strings.Contains(req.URL.Path, "/api/datasources/uid/test-uid/resources/loki/api/v1/patterns"):
			return newMockResponse(http.StatusNotFound), nil
		default:
			return newMockResponse(http.StatusNotFound), nil
		}
	})

	rt := newDatasourceFallbackTransport(mock,
		"/api/datasources/proxy/uid/test-uid",
		"/api/datasources/uid/test-uid/resources",
	)

	// First request: discovers fallback is needed for labels.
	req1, _ := http.NewRequest("GET", "http://grafana.example.com/api/datasources/proxy/uid/test-uid/api/v1/labels", nil)
	resp, err := rt.RoundTrip(req1)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	require.Len(t, requests, 2)

	// A different Loki path must still try the primary path. Caching the
	// fallback only by datasource base would incorrectly route this directly to
	// /resources, which is not equivalent for the patterns endpoint.
	req2, _ := http.NewRequest("GET", "http://grafana.example.com/api/datasources/proxy/uid/test-uid/loki/api/v1/patterns", nil)
	resp, err = rt.RoundTrip(req2)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	require.Len(t, requests, 3)
	assert.Contains(t, requests[2].URL.Path, "/api/datasources/proxy/uid/test-uid/loki/api/v1/patterns")
}

func TestDatasourceFallbackTransport_HTMLFallbackIsNotSuccess(t *testing.T) {
	resetFallbackCache()

	// Reproduces a Loki datasource behind Grafana: a transient 500 on the
	// proxy path sends the request to /resources, which Loki answers with its
	// UI's index.html and a 200. That page must not be returned as the
	// response, nor pin every later request for this path to /resources.
	var requests []*http.Request
	mock := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		requests = append(requests, req)

		if strings.Contains(req.URL.Path, "/api/datasources/uid/test-uid/resources") {
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"text/html; charset=utf-8"}},
				Body:       io.NopCloser(strings.NewReader("<!DOCTYPE html><title>Loki UI</title>")),
			}, nil
		}
		return &http.Response{
			StatusCode: http.StatusInternalServerError,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"message":"query timed out"}`)),
		}, nil
	})

	rt := newDatasourceFallbackTransport(mock,
		"/api/datasources/proxy/uid/test-uid",
		"/api/datasources/uid/test-uid/resources",
	)

	req1, _ := http.NewRequest("GET", "http://grafana.example.com/api/datasources/proxy/uid/test-uid/loki/api/v1/query_range", nil)
	resp, err := rt.RoundTrip(req1)
	require.NoError(t, err)
	require.Len(t, requests, 2, "should still try the fallback on 500")

	// The caller gets the primary's real error, not the HTML page.
	assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Equal(t, `{"message":"query timed out"}`, string(body))

	// The HTML answer was not cached: the next request tries the primary again.
	req2, _ := http.NewRequest("GET", "http://grafana.example.com/api/datasources/proxy/uid/test-uid/loki/api/v1/query_range", nil)
	_, err = rt.RoundTrip(req2)
	require.NoError(t, err)
	require.Len(t, requests, 4)
	assert.Contains(t, requests[2].URL.Path, "/api/datasources/proxy/uid/test-uid/loki/api/v1/query_range")
}

// errAfterReader yields data, then fails, like a connection reset mid-body.
type errAfterReader struct {
	data string
	done bool
}

func (r *errAfterReader) Read(p []byte) (int, error) {
	if r.done {
		return 0, io.ErrUnexpectedEOF
	}
	r.done = true
	return copy(p, r.data), nil
}

func TestDatasourceFallbackTransport_PrimaryBodyReadErrorStillFallsBack(t *testing.T) {
	resetFallbackCache()

	var requests []*http.Request
	mock := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		requests = append(requests, req)

		if strings.Contains(req.URL.Path, "/api/datasources/uid/test-uid/resources") {
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(`{"status":"success"}`)),
			}, nil
		}
		return &http.Response{
			StatusCode: http.StatusInternalServerError,
			Body:       io.NopCloser(&errAfterReader{data: `{"message":"par`}),
		}, nil
	})

	rt := newDatasourceFallbackTransport(mock,
		"/api/datasources/proxy/uid/test-uid",
		"/api/datasources/uid/test-uid/resources",
	)

	// A primary whose error body cannot be read must not stop the fallback
	// from being tried, nor its good answer from being returned and cached.
	req1, _ := http.NewRequest("GET", "http://grafana.example.com/api/datasources/proxy/uid/test-uid/api/v1/labels", nil)
	resp, err := rt.RoundTrip(req1)
	require.NoError(t, err)
	require.Len(t, requests, 2)
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	req2, _ := http.NewRequest("GET", "http://grafana.example.com/api/datasources/proxy/uid/test-uid/api/v1/labels", nil)
	_, err = rt.RoundTrip(req2)
	require.NoError(t, err)
	require.Len(t, requests, 3)
	assert.Contains(t, requests[2].URL.Path, "/api/datasources/uid/test-uid/resources/api/v1/labels")
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
	assert.Len(t, mock.requests, 1, "should not retry on 4xx errors other than 401/403")
}

func TestDatasourceProxyPaths(t *testing.T) {
	resources, proxy := datasourceProxyPaths("my-uid-123")
	assert.Equal(t, "/api/datasources/uid/my-uid-123/resources", resources)
	assert.Equal(t, "/api/datasources/proxy/uid/my-uid-123", proxy)
}
