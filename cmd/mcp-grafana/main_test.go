package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/synctest"
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
	synctest.Test(t, func(t *testing.T) {
		_, _, sm := newServer("stdio", disabledTools{enabledTools: "search"}, obs, 0)
		defer sm.Close()

		session := &testClientSession{id: "should-persist"}
		sm.CreateSession(context.Background(), session)

		// Advance the fake clock well beyond any reasonable reaper interval.
		// With reaper disabled (TTL=0), the session must survive.
		time.Sleep(time.Hour)

		_, exists := sm.GetSession("should-persist")
		assert.True(t, exists, "Session should persist when idle timeout is 0 (reaper disabled)")
	})
}

func TestNewServer_SessionIdleTimeoutCustomValue(t *testing.T) {
	obs := newTestObservability(t)
	synctest.Test(t, func(t *testing.T) {
		_, _, sm := newServer("stdio", disabledTools{enabledTools: "search"}, obs, 1)
		defer sm.Close()

		session := &testClientSession{id: "custom-ttl"}
		sm.CreateSession(context.Background(), session)

		// Advance the fake clock past the 1-minute TTL.
		// The reaper runs every TTL/2 (30s), so by 2 minutes
		// it will have fired and reaped the idle session.
		time.Sleep(2 * time.Minute)

		_, exists := sm.GetSession("custom-ttl")
		assert.False(t, exists, "Session should be reaped after exceeding the 1-minute idle timeout")
	})
}

func TestProtectedResourceServerURL(t *testing.T) {
	tests := []struct {
		name   string
		addr   string
		useTLS bool
		want   string
	}{
		{name: "http", addr: "localhost:8000", want: "http://localhost:8000"},
		{name: "https", addr: "grafana.example:8443", useTLS: true, want: "https://grafana.example:8443"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, protectedResourceServerURL(tt.addr, tt.useTLS))
		})
	}
}

func TestHandleOAuthProtectedResource(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/.well-known/oauth-protected-resource", nil)
	rec := httptest.NewRecorder()

	handleOAuthProtectedResource("https://grafana.example:8443", "https://auth.example").ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))

	var got struct {
		Resource             string   `json:"resource"`
		AuthorizationServers []string `json:"authorization_servers"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Equal(t, "https://grafana.example:8443", got.Resource)
	assert.Equal(t, []string{"https://auth.example"}, got.AuthorizationServers)
}
