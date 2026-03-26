package main

import (
	"context"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/grafana/mcp-grafana/observability"
)

// testClientSession implements server.ClientSession for unit tests.
type testClientSession struct {
	id string
}

func (s *testClientSession) SessionID() string                                   { return s.id }
func (s *testClientSession) NotificationChannel() chan<- mcp.JSONRPCNotification { return nil }
func (s *testClientSession) Initialize()                                         {}
func (s *testClientSession) Initialized() bool                                   { return true }

func newTestObservability(t *testing.T) *observability.Observability {
	t.Helper()
	obs, err := observability.Setup(observability.Config{})
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = obs.Shutdown(context.Background())
	})
	return obs
}

func TestNewServer_SessionIdleTimeoutZeroDisablesReaping(t *testing.T) {
	obs := newTestObservability(t)
	_, _, sm := newServer("stdio", disabledTools{enabledTools: "search"}, obs, 0)
	defer sm.Close()

	session := &testClientSession{id: "should-persist"}
	sm.CreateSession(context.Background(), session)

	// With reaper disabled, the session must survive well beyond any
	// reasonable reaper interval.
	time.Sleep(200 * time.Millisecond)

	_, exists := sm.GetSession("should-persist")
	assert.True(t, exists, "Session should persist when idle timeout is 0 (reaper disabled)")
}

func TestNewServer_SessionIdleTimeoutCustomValue(t *testing.T) {
	obs := newTestObservability(t)
	// Use 1 minute; we can't wait a full minute in a unit test, but we can
	// verify the session is NOT reaped within a short window (proving the
	// TTL is at least longer than the default 100ms test TTLs).
	_, _, sm := newServer("stdio", disabledTools{enabledTools: "search"}, obs, 1)
	defer sm.Close()

	session := &testClientSession{id: "custom-ttl"}
	sm.CreateSession(context.Background(), session)

	time.Sleep(200 * time.Millisecond)

	_, exists := sm.GetSession("custom-ttl")
	assert.True(t, exists, "Session should still exist with a 1-minute idle timeout after only 200ms")
}
