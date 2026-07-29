package mcpgrafana

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/require"
)

// TestStreamableHTTPSessionToolStoreReclaimed is the regression guard for the
// per-session tool-registration leak.
//
// The streamable-http transport stores each session's registered tools (added
// via MCPServer.AddSessionTools) in a server-shared store keyed by session ID.
// UnregisterSession only drops the session handle; it does NOT delete that
// store entry. So a client that disconnects without sending a DELETE leaves a
// fixed amount of memory (its rewritten tool set) retained for every session
// ever created: memory that grows monotonically and is never reclaimed.
//
// The SDK's idle-session sweeper (WithSessionIdleTTL) is the fix: it runs the
// transport's full teardown, which unregisters the session AND deletes its
// per-session stores. This test enables the sweeper (as the server wiring does)
// and asserts that, after many sessions go idle, each one's tool-store entry is
// reclaimed rather than retained forever.
func TestStreamableHTTPSessionToolStoreReclaimed(t *testing.T) {
	mcpServer := server.NewMCPServer("test", "1.0")

	// Short idle TTL so the sweeper reclaims idle sessions quickly.
	const idleTTL = 200 * time.Millisecond
	streamable := server.NewStreamableHTTPServer(mcpServer,
		server.WithStateLess(false),
		server.WithSessionIdleTTL(idleTTL),
	)

	httpServer := httptest.NewServer(streamable)
	defer httpServer.Close()

	// Create several sessions and register a tool on each, exactly as the
	// per-session proxied-tools path does. Each session is then abandoned (no
	// DELETE), which is what leaks without the sweeper.
	const numSessions = 20
	sessionIDs := make([]string, 0, numSessions)
	for i := 0; i < numSessions; i++ {
		sessionID := initializeStreamableSession(t, httpServer.URL)
		require.NotEmpty(t, sessionID)
		sessionIDs = append(sessionIDs, sessionID)

		err := mcpServer.AddSessionTools(sessionID, server.ServerTool{
			Tool: mcp.NewTool("tempo_example"),
			Handler: func(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				return nil, nil
			},
		})
		require.NoError(t, err, "tools should register while the session is live")
	}

	// While sessions are live, adding tools must succeed (the entries exist).
	require.NoError(t, mcpServer.AddSessionTools(sessionIDs[0],
		server.ServerTool{Tool: mcp.NewTool("tempo_example2")}))

	// Let all sessions go idle past the TTL so the sweeper reclaims them.
	require.Eventually(t, func() bool {
		for _, id := range sessionIDs {
			// Once the sweeper has run its full teardown for a session, the
			// session handle is gone and AddSessionTools reports it as not found.
			// That teardown is the same path that deletes the per-session tool
			// store, so ErrSessionNotFound here means the store entry was freed.
			if err := mcpServer.AddSessionTools(id, server.ServerTool{Tool: mcp.NewTool("probe")}); !errors.Is(err, server.ErrSessionNotFound) {
				return false
			}
		}
		return true
	}, 5*time.Second, idleTTL/2, "all idle sessions must be swept, freeing their per-session tool stores")
}

// initializeStreamableSession performs the MCP initialize handshake against a
// streamable-http server and returns the assigned session ID.
func initializeStreamableSession(t *testing.T, url string) string {
	t.Helper()

	body, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params": map[string]any{
			"protocolVersion": mcp.LATEST_PROTOCOL_VERSION,
			"clientInfo":      map[string]any{"name": "test-client", "version": "1.0.0"},
		},
	})
	require.NoError(t, err)

	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	return resp.Header.Get("Mcp-Session-Id")
}
