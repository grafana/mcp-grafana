package mcpgrafana

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testClientSession implements server.ClientSession for unit tests.
type testClientSession struct {
	id string
}

func (s *testClientSession) SessionID() string                                   { return s.id }
func (s *testClientSession) NotificationChannel() chan<- mcp.JSONRPCNotification { return nil }
func (s *testClientSession) Initialize()                                         {}
func (s *testClientSession) Initialized() bool                                   { return true }

func TestSessionManager_ReaperRemovesIdleSessions(t *testing.T) {
	sm := NewSessionManager(WithSessionTTL(100 * time.Millisecond))
	defer sm.Close()

	session := &testClientSession{id: "idle-session"}
	sm.CreateSession(context.Background(), session)

	// Session should exist immediately after creation
	sm.mutex.RLock()
	_, exists := sm.sessions["idle-session"]
	sm.mutex.RUnlock()
	require.True(t, exists)

	// Wait for the session to become stale and be reaped.
	// TTL=100ms, reaper interval=50ms, so by 500ms it should be reaped.
	time.Sleep(500 * time.Millisecond)

	// Check directly without calling GetSession (which would update lastActivity)
	sm.mutex.RLock()
	_, exists = sm.sessions["idle-session"]
	sm.mutex.RUnlock()
	assert.False(t, exists, "Idle session should have been reaped")
}

func TestSessionManager_ReaperKeepsActiveSessions(t *testing.T) {
	sm := NewSessionManager(WithSessionTTL(200 * time.Millisecond))
	defer sm.Close()

	session := &testClientSession{id: "active-session"}
	sm.CreateSession(context.Background(), session)

	// Keep the session active by calling GetSession periodically
	for i := 0; i < 5; i++ {
		time.Sleep(80 * time.Millisecond)
		state, exists := sm.GetSession("active-session")
		require.True(t, exists, "Active session should not be reaped (iteration %d)", i)
		require.NotNil(t, state)
	}
}

func TestSessionManager_ReaperCleansUpProxiedClients(t *testing.T) {
	sm := NewSessionManager(WithSessionTTL(100 * time.Millisecond))
	defer sm.Close()

	session := &testClientSession{id: "cleanup-session"}
	sm.CreateSession(context.Background(), session)

	// Access the state directly to add a proxied client without updating lastActivity
	sm.mutex.RLock()
	state := sm.sessions["cleanup-session"]
	sm.mutex.RUnlock()
	state.proxiedClients["tempo_test-uid"] = &ProxiedClient{
		DatasourceUID:  "test-uid",
		DatasourceName: "Test",
		DatasourceType: "tempo",
	}

	// Wait for reaper
	time.Sleep(500 * time.Millisecond)

	sm.mutex.RLock()
	_, exists := sm.sessions["cleanup-session"]
	sm.mutex.RUnlock()
	assert.False(t, exists, "Session should have been reaped")
}

func TestSessionManager_Close(t *testing.T) {
	sm := NewSessionManager(WithSessionTTL(time.Hour)) // Long TTL so reaper won't trigger

	session1 := &testClientSession{id: "session-1"}
	session2 := &testClientSession{id: "session-2"}
	sm.CreateSession(context.Background(), session1)
	sm.CreateSession(context.Background(), session2)

	sm.Close()

	// Both sessions should be gone
	_, exists := sm.GetSession("session-1")
	assert.False(t, exists)
	_, exists = sm.GetSession("session-2")
	assert.False(t, exists)
}

func TestSessionManager_CloseIsIdempotent(t *testing.T) {
	sm := NewSessionManager(WithSessionTTL(100 * time.Millisecond))
	sm.Close()
	sm.Close() // Should not panic
}

func TestSessionManager_DisabledReaper(t *testing.T) {
	sm := NewSessionManager(WithSessionTTL(0))
	defer sm.Close()

	session := &testClientSession{id: "no-reaper-session"}
	sm.CreateSession(context.Background(), session)

	time.Sleep(50 * time.Millisecond)

	_, exists := sm.GetSession("no-reaper-session")
	assert.True(t, exists, "Session should persist when reaper is disabled")
}

func TestSessionManager_GetSessionUpdatesLastActivity(t *testing.T) {
	sm := NewSessionManager(WithSessionTTL(time.Hour))
	defer sm.Close()

	session := &testClientSession{id: "activity-session"}
	sm.CreateSession(context.Background(), session)

	// Get initial lastActivity
	sm.mutex.RLock()
	state := sm.sessions["activity-session"]
	t1 := state.lastActivity
	sm.mutex.RUnlock()

	time.Sleep(10 * time.Millisecond)

	// GetSession should update lastActivity
	sm.GetSession("activity-session")

	sm.mutex.RLock()
	t2 := state.lastActivity
	sm.mutex.RUnlock()

	assert.True(t, t2.After(t1), "lastActivity should be updated on GetSession")
}

func TestSessionManager_ConcurrentCreateAndReap(t *testing.T) {
	sm := NewSessionManager(WithSessionTTL(50 * time.Millisecond))
	defer sm.Close()

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			sess := &testClientSession{id: fmt.Sprintf("concurrent-%d", idx)}
			sm.CreateSession(context.Background(), sess)
			time.Sleep(10 * time.Millisecond)
			sm.GetSession(sess.id)
		}(i)
	}
	wg.Wait()

	// Wait for reaper to clean up
	time.Sleep(200 * time.Millisecond)
}
