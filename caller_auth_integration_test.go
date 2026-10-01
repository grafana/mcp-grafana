package mcpgrafana

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testCallerToken = "integration-caller-token"

const initializeBody = `{"jsonrpc":"2.0","id":0,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"test","version":"0"}}}`

func TestCallerAuth_StreamableHTTP_EndToEnd(t *testing.T) {
	mcpServer := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0"}, nil)
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return mcpServer }, &mcp.StreamableHTTPOptions{
		Stateless: true,
	})

	mux := http.NewServeMux()
	mux.Handle("/mcp", RequireBearerToken(testCallerToken, slog.Default())(handler))
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	post := func(t *testing.T, authHeader, sessionID string) *http.Response {
		t.Helper()
		req, err := http.NewRequest(http.MethodPost, ts.URL+"/mcp", bytes.NewBufferString(initializeBody))
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		if authHeader != "" {
			req.Header.Set("Authorization", authHeader)
		}
		if sessionID != "" {
			req.Header.Set("Mcp-Session-Id", sessionID)
		}
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		t.Cleanup(func() { _ = resp.Body.Close() })
		return resp
	}

	t.Run("no token cannot call tools -> 401", func(t *testing.T) {
		resp := post(t, "", "")
		assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	})

	t.Run("wrong token -> 401", func(t *testing.T) {
		resp := post(t, "Bearer nope", "")
		assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	})

	t.Run("self-minted session id does not bypass auth -> 401", func(t *testing.T) {
		resp := post(t, "", "made-up-session-id")
		assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	})

	t.Run("valid token initializes (not 401)", func(t *testing.T) {
		resp := post(t, "Bearer "+testCallerToken, "")
		assert.NotEqual(t, http.StatusUnauthorized, resp.StatusCode)
	})

	t.Run("OPTIONS preflight passes without a token", func(t *testing.T) {
		req, err := http.NewRequest(http.MethodOptions, ts.URL+"/mcp", nil)
		require.NoError(t, err)
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		t.Cleanup(func() { _ = resp.Body.Close() })
		assert.NotEqual(t, http.StatusUnauthorized, resp.StatusCode)
	})
}

func TestCallerAuth_SSE_EndToEnd(t *testing.T) {
	mcpServer := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0"}, nil)
	sse := mcp.NewSSEHandler(func(*http.Request) *mcp.Server { return mcpServer }, nil)
	ts := httptest.NewServer(RequireBearerToken(testCallerToken, slog.Default())(sse))
	t.Cleanup(ts.Close)

	get := func(t *testing.T, authHeader string) (*http.Response, error) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		t.Cleanup(cancel)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/sse", nil)
		require.NoError(t, err)
		if authHeader != "" {
			req.Header.Set("Authorization", authHeader)
		}
		return http.DefaultClient.Do(req)
	}

	t.Run("no token -> 401", func(t *testing.T) {
		resp, err := get(t, "")
		require.NoError(t, err)
		t.Cleanup(func() { _ = resp.Body.Close() })
		assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	})

	t.Run("valid token opens the stream (not 401)", func(t *testing.T) {
		resp, err := get(t, "Bearer "+testCallerToken)
		if err != nil && !errors.Is(err, context.DeadlineExceeded) {
			require.NoError(t, err)
		}
		require.NotNil(t, resp)
		t.Cleanup(func() { _ = resp.Body.Close() })
		assert.Equal(t, http.StatusOK, resp.StatusCode)
	})
}
