package mcpgrafana

import (
	"context"
	"log/slog"
	"sync"
	"time"

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

type sessionMetrics struct {
	activeSessions metric.Int64Gauge
	sessionsReaped metric.Int64Counter
}

func newSessionMetrics(mp metric.MeterProvider) sessionMetrics {
	if mp == nil {
		mp = otel.GetMeterProvider()
	}
	meter := mp.Meter(sessionMeterName)

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

// SessionState holds the state for a single client session.
type SessionState struct {
	lastActivity time.Time
}

func newSessionState() *SessionState {
	return &SessionState{
		lastActivity: time.Now(),
	}
}

// SessionManagerOption configures a SessionManager.
type SessionManagerOption func(*SessionManager)

// WithSessionTTL sets the TTL for idle sessions.
func WithSessionTTL(ttl time.Duration) SessionManagerOption {
	return func(sm *SessionManager) {
		sm.sessionTTL = ttl
	}
}

// WithSessionLogger sets the logger for the SessionManager.
func WithSessionLogger(logger *slog.Logger) SessionManagerOption {
	return func(sm *SessionManager) {
		sm.logger = logger
	}
}

// WithSessionMeterProvider sets the metric.MeterProvider for the SessionManager.
func WithSessionMeterProvider(mp metric.MeterProvider) SessionManagerOption {
	return func(sm *SessionManager) {
		sm.meterProvider = mp
	}
}

// SessionManager manages client sessions and their state.
type SessionManager struct {
	sessions      map[string]*SessionState
	mutex         sync.RWMutex
	sessionTTL    time.Duration
	stopReaper    chan struct{}
	reaperDone    chan struct{}
	closeOnce     sync.Once
	metrics       sessionMetrics
	meterProvider metric.MeterProvider
	logger        *slog.Logger

	mcpServer *server.MCPServer
}

// SetMCPServer sets the MCP server reference for session cleanup.
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
	}
	for _, opt := range opts {
		opt(sm)
	}
	sm.metrics = newSessionMetrics(sm.meterProvider)
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
	delete(sm.sessions, sessionID)
	sm.recordActiveSessionCount()
	sm.mutex.Unlock()
}

// Close stops the reaper goroutine and cleans up all remaining sessions.
func (sm *SessionManager) Close() {
	sm.closeOnce.Do(func() {
		close(sm.stopReaper)
		<-sm.reaperDone

		sm.mutex.Lock()
		count := len(sm.sessions)
		sm.sessions = make(map[string]*SessionState)
		sm.recordActiveSessionCount()
		sm.mutex.Unlock()

		sm.logger.Debug("SessionManager closed", "cleaned_sessions", count)
	})
}

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

func (sm *SessionManager) reapStaleSessions() {
	now := time.Now()

	sm.mutex.Lock()
	var staleIDs []string
	mcpSrv := sm.mcpServer
	for id, state := range sm.sessions {
		if now.Sub(state.lastActivity) > sm.sessionTTL {
			staleIDs = append(staleIDs, id)
			delete(sm.sessions, id)
		}
	}
	if len(staleIDs) > 0 {
		sm.recordActiveSessionCount()
		sm.metrics.sessionsReaped.Add(context.Background(), int64(len(staleIDs)))
	}
	sm.mutex.Unlock()

	if len(staleIDs) > 0 {
		sm.logger.Info("Reaping stale sessions", "count", len(staleIDs), "session_ids", staleIDs)
	}

	ctx := context.Background()
	for _, id := range staleIDs {
		if mcpSrv != nil {
			mcpSrv.UnregisterSession(ctx, id)
		}
	}
}
