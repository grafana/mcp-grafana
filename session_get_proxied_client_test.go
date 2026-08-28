package mcpgrafana

import (
	"context"
	"testing"

	"github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNewToolManager_WiresSetToolManager is the regression guard for a caller
// wiring bug: NewToolManager did not back-wire itself onto the SessionManager
// it was given, so SessionManager.GetProxiedClient (the path every
// session-mode proxied tool call goes through) always saw a nil toolManager
// and failed as if no datasources had been discovered, no matter how well
// discovery/build/registration went. This reproduced live in
// grafana-assistant-app: its NewMCPServer called sm.SetMCPServer(s) but never
// sm.SetToolManager(tm), so every proxied tool call failed identically
// regardless of session, replica, or credentials, while tools/list kept
// listing the tools fine (a different code path that never needed
// sm.toolManager). NewToolManager now wires this itself so a caller that only
// calls NewToolManager can't forget the step.
func TestNewToolManager_WiresSetToolManager(t *testing.T) {
	tm, sm, srv := newToolManagerWithServer(t, setWith("tempo", "uid-1"))

	ctx := ctxWithCreds("http://grafana", "secret", nil, 1)
	sess := newToolsCapableSession("sess-1")
	sm.CreateSession(ctx, sess)
	require.NoError(t, srv.RegisterSession(ctx, sess))

	tm.InitializeAndRegisterProxiedTools(ctx, sess)

	callCtx := srv.WithContext(ctx, sess)
	client, release, err := sm.GetProxiedClient(callCtx, 0, "tempo", "uid-1")
	require.NoError(t, err, "GetProxiedClient must succeed once NewToolManager has wired itself onto sm")
	require.NotNil(t, client)
	assert.Equal(t, "uid-1", client.DatasourceUID)
	if release != nil {
		release()
	}
}

// TestSessionManager_GetProxiedClient_NoToolManagerWired covers the other side
// of the same bug: a SessionManager that was never given a ToolManager at all
// must fail with a distinct, diagnosable error rather than the generic "no
// datasources configured" message, which looks identical to a legitimate
// empty discovery result and is what made the wiring bug so hard to spot from
// logs/error text alone.
func TestSessionManager_GetProxiedClient_NoToolManagerWired(t *testing.T) {
	sm := NewSessionManager()
	t.Cleanup(sm.Close)

	srv := server.NewMCPServer("test", "1.0")

	ctx := context.Background()
	sess := newToolsCapableSession("sess-1")
	sm.CreateSession(ctx, sess)

	callCtx := srv.WithContext(ctx, sess)
	_, _, err := sm.GetProxiedClient(callCtx, 0, "tempo", "uid-1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "wiring error", "the error must call out the missing ToolManager wiring, not look like a discovery/permissions problem")
}
