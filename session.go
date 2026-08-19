package mcpgrafana

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

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

	// Proxied tools state.
	//
	// Rather than discovering, connecting, and rewriting proxied tools per
	// session (which made proxied clients and tools scale with the number of
	// live sessions), a session attaches to a shared, credential-keyed
	// proxiedToolSet owned by the ToolManager. proxiedSet is a reference to that
	// shared set; teardown releases the reference by that pointer (not by key),
	// so the reference is balanced even for a set already dropped from the cache.
	// proxiedSetReleased makes that release idempotent per session.
	//
	// proxiedInitMu serializes attach/build/register for this one session and
	// proxiedRegistered records that it succeeded. A one-shot sync.Once is
	// deliberately NOT used: a failed or empty build must NOT consume the
	// session's attempt, or that session would be stuck without proxied tools
	// forever even though the cache treats the failure as transient and lets
	// other sessions rebuild. Leaving proxiedRegistered false on failure lets the
	// next call's touch-or-create path retry.
	proxiedInitMu      sync.Mutex
	proxiedRegistered  bool
	proxiedSet         *proxiedToolSet
	proxiedSetReleased bool
	mutex              sync.RWMutex
}

func newSessionState() *SessionState {
	return &SessionState{
		lastActivity: time.Now(),
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

// SessionManager manages client sessions and their state.
//
// Unlike mark3labs, the official go-sdk exposes no session-disconnect hook
// (ServerSessionOptions.onClose exists but is unexported, and ServerOptions
// has no general connect/disconnect callback), so cleanup here relies
// entirely on the idle reaper rather than an immediate on-disconnect
// notification paired with a reaper backstop. A session's proxied tool set
// reference (and its underlying remote MCP connections) is therefore held
// for up to sessionTTL after a client actually disconnects, not released
// the instant it does.
type SessionManager struct {
	sessions   map[string]*SessionState
	mutex      sync.RWMutex
	sessionTTL time.Duration
	stopReaper chan struct{}
	reaperDone chan struct{}
	closeOnce  sync.Once
	metrics    sessionMetrics
	logger     *slog.Logger

	// toolManager is an optional reference to the ToolManager, used on session
	// teardown to release the session's reference to its shared proxied tool
	// set (closing the underlying clients only when the last session detaches).
	toolManager *ToolManager
}

// SetToolManager sets the ToolManager reference for session cleanup. When set,
// tearing down a session releases its reference to the shared proxied tool set,
// so the set's clients are closed once the last session using them is gone.
func (sm *SessionManager) SetToolManager(tm *ToolManager) {
	sm.mutex.Lock()
	defer sm.mutex.Unlock()
	sm.toolManager = tm
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

// TouchOrCreateSession records activity for sessionID, creating its state if
// this is the first time it's been seen. It is the equivalent of mark3labs'
// OnRegisterSession hook, called lazily on the first call for a session
// (typically from GrafanaContextMiddleware) rather than at connection time,
// since the official SDK has no dedicated session-registration hook.
func (sm *SessionManager) TouchOrCreateSession(sessionID string) *SessionState {
	sm.mutex.Lock()
	defer sm.mutex.Unlock()

	state, exists := sm.sessions[sessionID]
	if !exists {
		state = newSessionState()
		sm.sessions[sessionID] = state
		sm.recordActiveSessionCount()
	} else {
		state.lastActivity = time.Now()
	}
	return state
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

// sessionRegistered reports whether sessionID is still tracked and maps to the
// exact state pointer given. It is used to detect a teardown (reap) that raced
// an attach: if the session was removed (or replaced by a newer state) since
// the state was obtained, the caller must release the reference it took,
// because no future reap will fire for this untracked state.
func (sm *SessionManager) sessionRegistered(sessionID string, state *SessionState) bool {
	sm.mutex.RLock()
	defer sm.mutex.RUnlock()
	current, exists := sm.sessions[sessionID]
	return exists && current == state
}

// cleanupSessionState releases the session's reference to its shared proxied
// tool set and drops its registered *mcp.Server (if any). The set's clients
// are closed only when the last referencing session is torn down; other live
// sessions sharing the same credentials keep them open.
func (sm *SessionManager) cleanupSessionState(sessionID string, state *SessionState) {
	sm.mutex.RLock()
	tm := sm.toolManager
	sm.mutex.RUnlock()

	if tm != nil {
		tm.releaseSessionProxiedToolSet(state)
		tm.UnregisterSessionServer(sessionID)
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

		for id, state := range sessions {
			sm.cleanupSessionState(id, state)
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
// This is the SessionManager's only cleanup path (see the type doc): the
// official SDK gives no earlier signal that a session has disconnected.
func (sm *SessionManager) reapStaleSessions() {
	now := time.Now()

	sm.mutex.Lock()
	var stale []*SessionState
	var staleIDs []string
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

	for i, state := range stale {
		sm.cleanupSessionState(staleIDs[i], state)
	}
}

// GetProxiedClient retrieves a proxied client for the given datasource and
// registers an in-flight call against its shared set, so the client cannot be
// Closed by a concurrent teardown (the reaper) while the returned client is
// still in use. The caller MUST invoke the returned release func (deferred)
// once the call completes; only then may the set be torn down. On error the
// release func is nil and must not be called.
func (sm *SessionManager) GetProxiedClient(sessionID, datasourceType, datasourceUID string) (*ProxiedClient, func(), error) {
	state, exists := sm.GetSession(sessionID)
	if !exists {
		return nil, nil, fmt.Errorf("session not found")
	}

	state.mutex.RLock()
	set := state.proxiedSet
	state.mutex.RUnlock()

	sm.mutex.RLock()
	tm := sm.toolManager
	sm.mutex.RUnlock()

	if set == nil || tm == nil {
		return nil, nil, fmt.Errorf("datasource '%s' not found. No %s datasources with MCP support are configured", datasourceUID, datasourceType)
	}

	// acquireProxiedClientForCall looks up the client and takes the in-flight
	// count under the set lock, so it cannot race a last-ref release/Close.
	return tm.acquireProxiedClientForCall(set, datasourceType, datasourceUID)
}
