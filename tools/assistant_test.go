package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	mcpgrafana "github.com/grafana/mcp-grafana"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// sseFrame formats a single JSON-RPC result as an SSE `data:` frame.
func sseFrame(t *testing.T, result any) string {
	t.Helper()
	payload, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      "1",
		"result":  result,
	})
	require.NoError(t, err)
	return "data: " + string(payload) + "\n\n"
}

// writeSSE writes frames to the response writer, flushing after each so a
// streaming client observes them incrementally.
func writeSSE(w http.ResponseWriter, frames ...string) {
	flusher, _ := w.(http.Flusher)
	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(http.StatusOK)
	for _, f := range frames {
		_, _ = w.Write([]byte(f))
		if flusher != nil {
			flusher.Flush()
		}
	}
}

func assistantCtx(serverURL string) context.Context {
	return mcpgrafana.WithGrafanaConfig(context.Background(), mcpgrafana.GrafanaConfig{
		URL: serverURL,
	})
}

func TestAskAssistant_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Contains(t, r.URL.Path, fmt.Sprintf(assistantPathPattern, assistantAgentID))
		assert.Equal(t, "text/event-stream", r.Header.Get("Accept"))
		assert.Equal(t, assistantAppSource, r.Header.Get("X-App-Source"))

		writeSSE(w,
			sseFrame(t, map[string]any{
				"kind":      "status-update",
				"taskId":    "task-1",
				"contextId": "ctx-abc",
				"status":    map[string]any{"state": "working"},
			}),
			sseFrame(t, map[string]any{
				"kind":      "artifact-update",
				"taskId":    "task-1",
				"contextId": "ctx-abc",
				"artifact": map[string]any{
					"name":  "step.message",
					"parts": []map[string]any{{"kind": "text", "text": "Hello,"}},
				},
			}),
			sseFrame(t, map[string]any{
				"kind":      "artifact-update",
				"taskId":    "task-1",
				"contextId": "ctx-abc",
				"artifact": map[string]any{
					"name":  "step.message",
					"parts": []map[string]any{{"kind": "text", "text": "world."}},
				},
			}),
			sseFrame(t, map[string]any{
				"kind":      "status-update",
				"taskId":    "task-1",
				"contextId": "ctx-abc",
				"status":    map[string]any{"state": "completed"},
			}),
		)
	}))
	defer server.Close()

	result, err := askAssistant(assistantCtx(server.URL), AskAssistantParams{Prompt: "hi"})
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, "Hello,\nworld.", result.Response)
	assert.Equal(t, "ctx-abc", result.ContextID)
}

func TestAskAssistant_MultiTurnPassesContextID(t *testing.T) {
	var gotContextID string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req assistantJSONRPCRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		var params assistantMessageSendParams
		require.NoError(t, json.Unmarshal(req.Params, &params))
		gotContextID = params.ContextID

		// Also verify the prompt carries the time-context block.
		require.NotEmpty(t, params.Message.Parts)
		assert.Contains(t, params.Message.Parts[0].Text, "<time_iso_utc>")

		writeSSE(w, sseFrame(t, map[string]any{
			"kind":      "task",
			"id":        "task-2",
			"contextId": "ctx-xyz",
			"status":    map[string]any{"state": "completed"},
			"artifacts": []map[string]any{{
				"name":  "step.message",
				"parts": []map[string]any{{"kind": "text", "text": "follow-up answer"}},
			}},
		}))
	}))
	defer server.Close()

	result, err := askAssistant(assistantCtx(server.URL), AskAssistantParams{
		Prompt:    "and then?",
		ContextID: "ctx-prev",
	})
	require.NoError(t, err)
	assert.Equal(t, "ctx-prev", gotContextID)
	assert.Equal(t, "follow-up answer", result.Response)
	assert.Equal(t, "ctx-xyz", result.ContextID)
}

func TestAskAssistant_TaskFailedSurfacesError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeSSE(w,
			sseFrame(t, map[string]any{
				"kind":      "artifact-update",
				"taskId":    "task-3",
				"contextId": "ctx-fail",
				"artifact": map[string]any{
					"name":  "step.message",
					"parts": []map[string]any{{"kind": "text", "text": "partial output"}},
				},
			}),
			sseFrame(t, map[string]any{
				"kind":      "status-update",
				"taskId":    "task-3",
				"contextId": "ctx-fail",
				"status":    map[string]any{"state": "failed"},
			}),
		)
	}))
	defer server.Close()

	result, err := askAssistant(assistantCtx(server.URL), AskAssistantParams{Prompt: "boom"})
	require.ErrorIs(t, err, errAssistantTaskFailed)
	// Partial output is preserved even on terminal failure.
	require.NotNil(t, result)
	assert.Equal(t, "partial output", result.Response)
	assert.Equal(t, "ctx-fail", result.ContextID)
}

func TestAskAssistant_TaskFailedErrorCarriesPartialOutput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeSSE(w,
			sseFrame(t, map[string]any{
				"kind":      "artifact-update",
				"taskId":    "task-3",
				"contextId": "ctx-fail",
				"artifact": map[string]any{
					"name":  "step.message",
					"parts": []map[string]any{{"kind": "text", "text": "partial output"}},
				},
			}),
			sseFrame(t, map[string]any{
				"kind":      "status-update",
				"taskId":    "task-3",
				"contextId": "ctx-fail",
				"status":    map[string]any{"state": "failed"},
			}),
		)
	}))
	defer server.Close()

	_, err := askAssistant(assistantCtx(server.URL), AskAssistantParams{Prompt: "boom"})
	require.ErrorIs(t, err, errAssistantTaskFailed)
	// The MCP wrapper only surfaces err.Error() on failure, so the partial
	// reply and contextId must be embedded in the error to survive.
	assert.Contains(t, err.Error(), "partial output")
	assert.Contains(t, err.Error(), "ctx-fail")
}

func TestAskAssistant_IncompleteStreamReportsError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Stream some non-terminal events, then close cleanly without ever
		// sending a terminal task state.
		writeSSE(w,
			sseFrame(t, map[string]any{
				"kind":      "status-update",
				"taskId":    "task-i",
				"contextId": "ctx-inc",
				"status":    map[string]any{"state": "working"},
			}),
			sseFrame(t, map[string]any{
				"kind":      "artifact-update",
				"taskId":    "task-i",
				"contextId": "ctx-inc",
				"artifact": map[string]any{
					"name":  "step.message",
					"parts": []map[string]any{{"kind": "text", "text": "half an answer"}},
				},
			}),
		)
	}))
	defer server.Close()

	result, err := askAssistant(assistantCtx(server.URL), AskAssistantParams{Prompt: "hi"})
	require.ErrorIs(t, err, errAssistantIncompleteStream)
	// Partial text and contextId are preserved for the caller.
	require.NotNil(t, result)
	assert.Equal(t, "half an answer", result.Response)
	assert.Equal(t, "ctx-inc", result.ContextID)
	// And they are echoed into the error so an MCP client can see them.
	assert.Contains(t, err.Error(), "half an answer")
	assert.Contains(t, err.Error(), "ctx-inc")
}

func TestAskAssistant_JSONRPCErrorFrame(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		payload, _ := json.Marshal(map[string]any{
			"jsonrpc": "2.0",
			"id":      "1",
			"error":   map[string]any{"code": -32000, "message": "backend exploded"},
		})
		_, _ = w.Write([]byte("data: " + string(payload) + "\n\n"))
	}))
	defer server.Close()

	_, err := askAssistant(assistantCtx(server.URL), AskAssistantParams{Prompt: "hi"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "backend exploded")
}

func TestAskAssistant_EmptyPrompt(t *testing.T) {
	_, err := askAssistant(assistantCtx("https://example.grafana.net"), AskAssistantParams{Prompt: "   "})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "prompt is required")
}

func TestAskAssistant_MissingURL(t *testing.T) {
	ctx := mcpgrafana.WithGrafanaConfig(context.Background(), mcpgrafana.GrafanaConfig{})
	_, err := askAssistant(ctx, AskAssistantParams{Prompt: "hi"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "grafana URL is not configured")
}

func TestAskAssistant_Non200Error(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("kaboom"))
	}))
	defer server.Close()

	_, err := askAssistant(assistantCtx(server.URL), AskAssistantParams{Prompt: "hi"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "500")
	assert.Contains(t, err.Error(), "kaboom")
}

func TestChat_RetriesTransientThenSucceeds(t *testing.T) {
	var attempts int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&attempts, 1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		writeSSE(w, sseFrame(t, map[string]any{
			"kind":      "task",
			"id":        "task-r",
			"contextId": "ctx-r",
			"status":    map[string]any{"state": "completed"},
			"artifacts": []map[string]any{{
				"name":  "step.message",
				"parts": []map[string]any{{"kind": "text", "text": "recovered"}},
			}},
		}))
	}))
	defer server.Close()

	client, err := newAssistantClient(assistantCtx(server.URL))
	require.NoError(t, err)

	body, err := buildAssistantRequest("hi", "")
	require.NoError(t, err)
	endpoint := client.url + fmt.Sprintf(assistantPathPattern, assistantAgentID)

	result, err := client.chat(context.Background(), endpoint, body)
	require.NoError(t, err)
	assert.Equal(t, "recovered", result.Response)
	assert.Equal(t, int32(2), atomic.LoadInt32(&attempts))
}

func TestChat_Timeout(t *testing.T) {
	block := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeSSE(w, sseFrame(t, map[string]any{
			"kind":      "status-update",
			"taskId":    "task-t",
			"contextId": "ctx-t",
			"status":    map[string]any{"state": "working"},
		}))
		// Hang without ever sending a terminal frame.
		<-block
	}))
	defer server.Close()
	defer close(block)

	client, err := newAssistantClient(assistantCtx(server.URL))
	require.NoError(t, err)

	body, err := buildAssistantRequest("hi", "")
	require.NoError(t, err)
	endpoint := client.url + fmt.Sprintf(assistantPathPattern, assistantAgentID)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	result, err := client.chat(ctx, endpoint, body)
	require.ErrorIs(t, err, errAssistantTimeout)
	// Context is captured even though the task never completed.
	require.NotNil(t, result)
	assert.Equal(t, "ctx-t", result.ContextID)
}

func TestFormatAssistantTimeContext(t *testing.T) {
	ts := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	got := formatAssistantTimeContext(ts)
	assert.True(t, strings.HasPrefix(got, "<context><time_iso_utc>2026-07-29T12:00:00Z</time_iso_utc>"))
	assert.Contains(t, got, "<timezone>UTC</timezone>")
}

func TestAssistantIsRetryable(t *testing.T) {
	assert.True(t, assistantIsRetryable(errAssistantTimeout))
	assert.True(t, assistantIsRetryable(&assistantHTTPError{code: http.StatusServiceUnavailable}))
	assert.True(t, assistantIsRetryable(&assistantHTTPError{code: http.StatusTooManyRequests}))
	assert.False(t, assistantIsRetryable(&assistantHTTPError{code: http.StatusInternalServerError}))
	assert.False(t, assistantIsRetryable(&assistantHTTPError{code: http.StatusBadRequest}))
	assert.False(t, assistantIsRetryable(fmt.Errorf("some other error")))
}
