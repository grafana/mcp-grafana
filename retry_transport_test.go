package mcpgrafana

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// rateLimitServer answers the first `limited` requests with status and
// Retry-After, then 200 echoing the request body.
func rateLimitServer(t *testing.T, limited int32, status int, retryAfter string) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) <= limited {
			if retryAfter != "" {
				w.Header().Set("Retry-After", retryAfter)
			}
			w.WriteHeader(status)
			return
		}
		_, _ = io.Copy(w, r.Body)
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

func TestRateLimitRetryTransport(t *testing.T) {
	client := &http.Client{Transport: &rateLimitRetryTransport{next: http.DefaultTransport}}

	t.Run("retries a 429 and replays the body", func(t *testing.T) {
		srv, calls := rateLimitServer(t, 2, http.StatusTooManyRequests, "0")
		resp, err := client.Post(srv.URL, "application/json", strings.NewReader(`{"text":"hi"}`))
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()
		body, _ := io.ReadAll(resp.Body)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Equal(t, `{"text":"hi"}`, string(body))
		assert.Equal(t, int32(3), calls.Load())
	})

	t.Run("gives up after the max retries", func(t *testing.T) {
		srv, calls := rateLimitServer(t, 10, http.StatusTooManyRequests, "0")
		resp, err := client.Get(srv.URL)
		require.NoError(t, err)
		_ = resp.Body.Close()
		assert.Equal(t, http.StatusTooManyRequests, resp.StatusCode)
		assert.Equal(t, int32(rateLimitMaxRetries+1), calls.Load())
	})

	t.Run("retries a 503 only with Retry-After", func(t *testing.T) {
		srv, calls := rateLimitServer(t, 1, http.StatusServiceUnavailable, "0")
		resp, err := client.Get(srv.URL)
		require.NoError(t, err)
		_ = resp.Body.Close()
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Equal(t, int32(2), calls.Load())

		srv, calls = rateLimitServer(t, 1, http.StatusServiceUnavailable, "")
		resp, err = client.Get(srv.URL)
		require.NoError(t, err)
		_ = resp.Body.Close()
		assert.Equal(t, http.StatusServiceUnavailable, resp.StatusCode)
		assert.Equal(t, int32(1), calls.Load())
	})

	t.Run("doesn't wait past the context deadline", func(t *testing.T) {
		srv, calls := rateLimitServer(t, 1, http.StatusTooManyRequests, "5")
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
		resp, err := client.Do(req)
		require.NoError(t, err)
		_ = resp.Body.Close()
		assert.Equal(t, http.StatusTooManyRequests, resp.StatusCode)
		assert.Equal(t, int32(1), calls.Load())
	})

	t.Run("doesn't retry a body it can't replay", func(t *testing.T) {
		srv, calls := rateLimitServer(t, 1, http.StatusTooManyRequests, "0")
		// A bare io.Reader leaves GetBody unset.
		req, _ := http.NewRequest(http.MethodPost, srv.URL, io.MultiReader(strings.NewReader("x")))
		resp, err := client.Do(req)
		require.NoError(t, err)
		_ = resp.Body.Close()
		assert.Equal(t, http.StatusTooManyRequests, resp.StatusCode)
		assert.Equal(t, int32(1), calls.Load())
	})
}

func TestBuildTransportRetriesRateLimit(t *testing.T) {
	srv, calls := rateLimitServer(t, 1, http.StatusTooManyRequests, "0")
	transport, err := BuildTransport(&GrafanaConfig{}, nil)
	require.NoError(t, err)
	resp, err := (&http.Client{Transport: transport}).Get(srv.URL)
	require.NoError(t, err)
	_ = resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, int32(2), calls.Load())
}

func TestRateLimitRetryDelay(t *testing.T) {
	now := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name       string
		status     int
		retryAfter string
		method     string
		attempt    int
		want       time.Duration
		wantRetry  bool
	}{
		{"429 seconds", 429, "3", "GET", 0, 3 * time.Second, true},
		{"429 HTTP date", 429, now.Add(4 * time.Second).Format(http.TimeFormat), "GET", 0, 4 * time.Second, true},
		{"429 date in the past", 429, now.Add(-time.Minute).Format(http.TimeFormat), "GET", 0, 0, true},
		{"429 no header backs off", 429, "", "GET", 1, 2 * rateLimitFallbackWait, true},
		{"429 garbage header backs off", 429, "soon", "GET", 0, rateLimitFallbackWait, true},
		{"429 wait too long", 429, "60", "GET", 0, 60 * time.Second, false},
		{"429 huge value", 429, "99999999999999", "GET", 0, (1 << 31) * time.Second, false},
		{"503 with header", 503, "2", "GET", 0, 2 * time.Second, true},
		{"503 without header", 503, "", "GET", 0, 0, false},
		{"503 POST with header", 503, "2", "POST", 0, 0, false},
		{"429 POST", 429, "2", "POST", 0, 2 * time.Second, true},
		{"500", 500, "1", "GET", 0, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp := &http.Response{StatusCode: tc.status, Header: http.Header{}}
			if tc.retryAfter != "" {
				resp.Header.Set("Retry-After", tc.retryAfter)
			}
			got, retry := rateLimitRetryDelay(resp, tc.method, tc.attempt, now)
			assert.Equal(t, tc.wantRetry, retry)
			if tc.wantRetry || tc.want != 0 {
				assert.Equal(t, tc.want, got)
			}
		})
	}
}
