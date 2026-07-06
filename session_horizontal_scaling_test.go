package mcpgrafana

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockSessionWithTools implements both server.ClientSession and server.SessionWithTools
// for testing the horizontal scaling fix where AddSessionTools needs to find
// the session in MCPServer.sessions.
type mockSessionWithTools struct {
	id            string
	notifChannel  chan mcp.JSONRPCNotification
	isInitialized bool
	tools         map[string]server.ServerTool
	mu            sync.RWMutex
}

func newMockSessionWithTools(id string) *mockSessionWithTools {
	return &mockSessionWithTools{
		id:           id,
		notifChannel: make(chan mcp.JSONRPCNotification, 10),
		tools:        make(map[string]server.ServerTool),
	}
}

func (m *mockSessionWithTools) SessionID() string {
	return m.id
}

func (m *mockSessionWithTools) NotificationChannel() chan<- mcp.JSONRPCNotification {
	return m.notifChannel
}

func (m *mockSessionWithTools) Initialize() {
	m.isInitialized = true
}

func (m *mockSessionWithTools) Initialized() bool {
	return m.isInitialized
}

func (m *mockSessionWithTools) GetSessionTools() map[string]server.ServerTool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	cp := make(map[string]server.ServerTool, len(m.tools))
	for k, v := range m.tools {
		cp[k] = v
	}
	return cp
}

func (m *mockSessionWithTools) SetSessionTools(tools map[string]server.ServerTool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.tools = tools
}

// TestRegisterSessionFixesAddSessionTools verifies the core fix for issue #749:
// when an ephemeral session is registered in MCPServer.sessions via RegisterSession,
// AddSessionTools succeeds instead of returning ErrSessionNotFound.
func TestRegisterSessionFixesAddSessionTools(t *testing.T) {
	t.Run("AddSessionTools fails without RegisterSession", func(t *testing.T) {
		s := server.NewMCPServer("test", "1.0.0")
		session := newMockSessionWithTools("unregistered-session")

		err := s.AddSessionTools(session.SessionID(), server.ServerTool{
			Tool:    mcp.NewTool("test-tool"),
			Handler: func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) { return nil, nil },
		})
		assert.Error(t, err, "AddSessionTools should fail for unregistered session")
	})

	t.Run("AddSessionTools succeeds after RegisterSession", func(t *testing.T) {
		s := server.NewMCPServer("test", "1.0.0")
		session := newMockSessionWithTools("cross-pod-session")

		// Simulate what the fix does: register the ephemeral session
		err := s.RegisterSession(context.Background(), session)
		require.NoError(t, err)

		// Now AddSessionTools should succeed
		err = s.AddSessionTools(session.SessionID(), server.ServerTool{
			Tool:    mcp.NewTool("tempo_traceql-search"),
			Handler: func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) { return nil, nil },
		})
		assert.NoError(t, err, "AddSessionTools should succeed after RegisterSession")

		// Verify the tool was actually registered
		tools := session.GetSessionTools()
		assert.Contains(t, tools, "tempo_traceql-search")
	})

	t.Run("RegisterSession is idempotent for already-registered sessions", func(t *testing.T) {
		s := server.NewMCPServer("test", "1.0.0")
		session := newMockSessionWithTools("existing-session")

		// First registration
		err := s.RegisterSession(context.Background(), session)
		require.NoError(t, err)

		// Second registration should return ErrSessionExists but not panic
		err = s.RegisterSession(context.Background(), session)
		assert.Error(t, err, "Second RegisterSession should return error")

		// AddSessionTools should still work
		err = s.AddSessionTools(session.SessionID(), server.ServerTool{
			Tool:    mcp.NewTool("test-tool"),
			Handler: func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) { return nil, nil },
		})
		assert.NoError(t, err)
	})
}

// TestReaperUnregistersFromMCPServer verifies that the session reaper also
// cleans up sessions from MCPServer.sessions when SetMCPServer has been called.
func TestReaperUnregistersFromMCPServer(t *testing.T) {
	t.Run("reaper cleans up MCPServer sessions", func(t *testing.T) {
		s := server.NewMCPServer("test", "1.0.0")

		sm := NewSessionManager(
			WithSessionTTL(50 * time.Millisecond),
		)
		sm.SetMCPServer(s)
		defer sm.Close()

		session := newMockSessionWithTools("reap-me")

		// Register in both the application SessionManager and MCPServer
		sm.CreateSession(context.Background(), session)
		err := s.RegisterSession(context.Background(), session)
		require.NoError(t, err)

		// Add a tool to prove the session is registered
		err = s.AddSessionTools(session.SessionID(), server.ServerTool{
			Tool:    mcp.NewTool("test-tool"),
			Handler: func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) { return nil, nil },
		})
		require.NoError(t, err)

		// Wait for the session to become stale and be reaped
		time.Sleep(200 * time.Millisecond)

		// Session should be removed from both managers
		_, exists := sm.GetSession("reap-me")
		assert.False(t, exists, "Session should be removed from SessionManager")

		// Verify the session is gone from MCPServer too by trying to add tools
		err = s.AddSessionTools("reap-me", server.ServerTool{
			Tool:    mcp.NewTool("another-tool"),
			Handler: func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) { return nil, nil },
		})
		assert.Error(t, err, "Session should be unregistered from MCPServer after reaping")
	})

	t.Run("reaper works without MCPServer reference", func(t *testing.T) {
		sm := NewSessionManager(
			WithSessionTTL(50 * time.Millisecond),
		)
		// Deliberately NOT calling sm.SetMCPServer()
		defer sm.Close()

		session := &mockClientSession{id: "no-mcp-server"}
		sm.CreateSession(context.Background(), session)

		// Wait for reaping
		time.Sleep(200 * time.Millisecond)

		_, exists := sm.GetSession("no-mcp-server")
		assert.False(t, exists, "Session should still be reaped without MCPServer reference")
	})
}

// TestSetMCPServer verifies the SetMCPServer method.
func TestSetMCPServer(t *testing.T) {
	t.Run("SetMCPServer sets the reference", func(t *testing.T) {
		sm := NewSessionManager()
		defer sm.Close()

		s := server.NewMCPServer("test", "1.0.0")
		sm.SetMCPServer(s)

		sm.mutex.RLock()
		assert.Equal(t, s, sm.mcpServer)
		sm.mutex.RUnlock()
	})

	t.Run("SetMCPServer can be called with nil", func(t *testing.T) {
		sm := NewSessionManager()
		defer sm.Close()

		s := server.NewMCPServer("test", "1.0.0")
		sm.SetMCPServer(s)
		sm.SetMCPServer(nil)

		sm.mutex.RLock()
		assert.Nil(t, sm.mcpServer)
		sm.mutex.RUnlock()
	})
}
