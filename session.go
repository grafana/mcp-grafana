package mcpgrafana

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric"
)

const (
	// DefaultSessionTTL is the default time-to-live for idle sessions.
	// Sessions with no activity for this duration are reaped.
	DefaultSessionTTL = 30 * time.Minute

	sessionMeterName = "mcp-grafana"
)

// sessionMetrics holds OTel instruments for session observability.
type sessionMetrics struct {
	activeSessions metric.Int64Gauge   // Current active session count
	sessionsReaped metric.Int64Counter // Total sessions removed by the reaper
}

func newSessionMetrics() sessionMetrics {
	meter := otel.GetMeterProvider().Meter(sessionMeterName)

	active, _ := meter.Int64Gauge("mcp.sessions.active",
		metric.WithDescription("Current number of active MCP sessions"),
		metric.WithUnit("{session}"),
	)
	reaped, _ := meter.Int64Counter("mcp.sessions.reaped",
		metric.WithDescription("Total number of sessions removed by the idle reaper"),
		metric.WithUnit("{session}"),
	)

	return sessionMetrics{
		activeSessions: active,
		sessionsReaped: reaped,
	}
}

// SessionState holds the state for a single client session
type SessionState struct {
	// lastActivity is the last time this session was accessed.
	// Updated on every GetSession call.
	lastActivity time.Time

	// Proxied tools state
	initOnce                sync.Once
	proxiedToolsInitialized bool
	proxiedTools            []mcp.Tool
	proxiedClients          map[string]*ProxiedClient // key: datasourceType_datasourceUID
	toolToDatasources       map[string][]string       // key: toolName, value: list of datasource keys that support it
	mutex                   sync.RWMutex
}

func newSessionState() *SessionState {
	return &SessionState{
		lastActivity:      time.Now(),
		proxiedClients:    make(map[string]*ProxiedClient),
		toolToDatasources: make(map[string][]string),
	}
}

// SessionManagerOption configures a SessionManager.
type SessionManagerOption func(*SessionManager)

// WithSessionTTL sets the TTL for idle sessions. Sessions idle longer than
// this duration are automatically reaped. A zero or negative value disables
// the reaper.
func WithSessionTTL(ttl time.Duration) SessionManagerOption {
	return func(sm *SessionManager) {
		sm.sessionTTL = ttl
	}
}

// WithSessionLogger sets the logger for the SessionManager. If not set,
// slog.Default() is used.
func WithSessionLogger(logger *slog.Logger) SessionManagerOption {
	return func(sm *SessionManager) {
		sm.logger = logger
	}
}

// SessionManager manages client sessions and their state
type SessionManager struct {
	sessions   map[string]*SessionState
	mutex      sync.RWMutex
	sessionTTL time.Duration
	stopReaper chan struct{}
	reaperDone chan struct{}
	closeOnce  sync.Once
	metrics    sessionMetrics
	logger     *slog.Logger

	// mcpServer is an optional reference to the MCP server, used to unregister
	// sessions from the SDK's internal session map when they are reaped. This
	// prevents a memory leak when sessions are registered via RegisterSession
	// in horizontal scaling scenarios (where ephemeral sessions are registered
	// so that AddSessionTools can find them).
	mcpServer *server.MCPServer
}

// SetMCPServer sets the MCP server reference for session cleanup. When set,
// the reaper will call MCPServer.UnregisterSession for reaped sessions to
// prevent a memory leak in the SDK's internal session map.
func (sm *SessionManager) SetMCPServer(s *server.MCPServer) {
	sm.mutex.Lock()
	defer sm.mutex.Unlock()
	sm.mcpServer = s
}

func NewSessionManager(opts ...SessionManagerOption) *SessionManager {
	sm := &SessionManager{
		sessions:   make(map[string]*SessionState),
		sessionTTL: DefaultSessionTTL,
		stopReaper: make(chan struct{}),
		reaperDone: make(chan struct{}),
		metrics:    newSessionMetrics(),
	}
	for _, opt := range opts {
		opt(sm)
	}
	if sm.logger == nil {
		sm.logger = slog.Default()
	}
	if sm.sessionTTL > 0 {
		go sm.runReaper()
	} else {
		close(sm.reaperDone)
	}
	return sm
}

func (sm *SessionManager) recordActiveSessionCount() {
	sm.metrics.activeSessions.Record(context.Background(), int64(len(sm.sessions)))
}

func (sm *SessionManager) CreateSession(ctx context.Context, session server.ClientSession) {
	sm.mutex.Lock()
	defer sm.mutex.Unlock()

	sessionID := session.SessionID()
	if _, exists := sm.sessions[sessionID]; !exists {
		sm.sessions[sessionID] = newSessionState()
		sm.recordActiveSessionCount()
	}
}

func (sm *SessionManager) GetSession(sessionID string) (*SessionState, bool) {
	sm.mutex.Lock()
	defer sm.mutex.Unlock()

	session, exists := sm.sessions[sessionID]
	if exists {
		session.lastActivity = time.Now()
	}
	return session, exists
}

func (sm *SessionManager) RemoveSession(ctx context.Context, session server.ClientSession) {
	sm.mutex.Lock()
	sessionID := session.SessionID()
	state, exists := sm.sessions[sessionID]
	delete(sm.sessions, sessionID)
	sm.recordActiveSessionCount()
	sm.mutex.Unlock()

	if !exists {
		return
	}

	sm.cleanupSessionState(state)
}

// cleanupSessionState closes all proxied clients in a session state.
func (sm *SessionManager) cleanupSessionState(state *SessionState) {
	state.mutex.Lock()
	defer state.mutex.Unlock()

	for key, client := range state.proxiedClients {
		if err := client.Close(); err != nil {
			sm.logger.Error("failed to close proxied client", "key", key, "error", err)
		}
	}
}

// Close stops the reaper goroutine and cleans up all remaining sessions.
// It is safe to call concurrently and multiple times.
func (sm *SessionManager) Close() {
	sm.closeOnce.Do(func() {
		close(sm.stopReaper)
		<-sm.reaperDone

		// Clean up all remaining sessions
		sm.mutex.Lock()
		sessions := make(map[string]*SessionState, len(sm.sessions))
		for k, v := range sm.sessions {
			sessions[k] = v
		}
		sm.sessions = make(map[string]*SessionState)
		sm.recordActiveSessionCount()
		sm.mutex.Unlock()

		for _, state := range sessions {
			sm.cleanupSessionState(state)
		}
		sm.logger.Debug("SessionManager closed", "cleaned_sessions", len(sessions))
	})
}

// runReaper periodically checks for and removes idle sessions.
func (sm *SessionManager) runReaper() {
	defer close(sm.reaperDone)

	interval := sm.sessionTTL / 2
	if interval < 100*time.Millisecond {
		interval = 100 * time.Millisecond
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			sm.reapStaleSessions()
		case <-sm.stopReaper:
			return
		}
	}
}

// reapStaleSessions removes sessions that have been idle longer than the TTL.
func (sm *SessionManager) reapStaleSessions() {
	now := time.Now()

	sm.mutex.Lock()
	var stale []*SessionState
	var staleIDs []string
	mcpSrv := sm.mcpServer
	for id, state := range sm.sessions {
		if now.Sub(state.lastActivity) > sm.sessionTTL {
			stale = append(stale, state)
			staleIDs = append(staleIDs, id)
			delete(sm.sessions, id)
		}
	}
	if len(stale) > 0 {
		sm.recordActiveSessionCount()
		sm.metrics.sessionsReaped.Add(context.Background(), int64(len(stale)))
	}
	sm.mutex.Unlock()

	if len(stale) > 0 {
		sm.logger.Info("Reaping stale sessions", "count", len(stale), "session_ids", staleIDs)
	}

	ctx := context.Background()
	for i, state := range stale {
		sm.cleanupSessionState(state)
		// Also unregister from MCPServer.sessions to prevent a memory leak.
		// Sessions may have been registered there via RegisterSession in the
		// OnBeforeListTools/OnBeforeCallTool hooks for horizontal scaling support.
		if mcpSrv != nil {
			mcpSrv.UnregisterSession(ctx, staleIDs[i])
		}
	}
}

// GetProxiedClient retrieves a proxied client for the given datasource
func (sm *SessionManager) GetProxiedClient(ctx context.Context, datasourceType, datasourceUID string) (*ProxiedClient, error) {
	session := server.ClientSessionFromContext(ctx)
	if session == nil {
		return nil, fmt.Errorf("session not found in context")
	}

	state, exists := sm.GetSession(session.SessionID())
	if !exists {
		return nil, fmt.Errorf("session not found")
	}

	state.mutex.RLock()
	defer state.mutex.RUnlock()

	key := datasourceType + "_" + datasourceUID
	client, exists := state.proxiedClients[key]
	if !exists {
		// List available datasources to help with debugging
		var availableUIDs []string
		for _, c := range state.proxiedClients {
			if c.DatasourceType == datasourceType {
				availableUIDs = append(availableUIDs, c.DatasourceUID)
			}
		}
		if len(availableUIDs) > 0 {
			return nil, fmt.Errorf("datasource '%s' not found. Available %s datasources: %v", datasourceUID, datasourceType, availableUIDs)
		}
		return nil, fmt.Errorf("datasource '%s' not found. No %s datasources with MCP support are configured", datasourceUID, datasourceType)
	}

	return client, nil
}
