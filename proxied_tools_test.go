package mcpgrafana

import (
	"context"
	"log/slog"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSessionManagerConcurrency(t *testing.T) {
	t.Run("concurrent session creation is safe", func(t *testing.T) {
		sm := NewSessionManager()
		defer sm.Close()
		var wg sync.WaitGroup

		// Create many sessions concurrently
		const numSessions = 100
		wg.Add(numSessions)

		for i := 0; i < numSessions; i++ {
			go func(id int) {
				defer wg.Done()
				sessionID := "session-" + string(rune('a'+id%26)) + "-" + string(rune('0'+id/26))
				mockSession := &mockClientSession{id: sessionID}
				sm.CreateSession(context.Background(), mockSession)
			}(i)
		}

		wg.Wait()

		// Verify all sessions were created
		sm.mutex.RLock()
		count := len(sm.sessions)
		sm.mutex.RUnlock()

		assert.Equal(t, numSessions, count, "All sessions should be created")
	})

	t.Run("concurrent get and remove is safe", func(t *testing.T) {
		sm := NewSessionManager()
		defer sm.Close()

		// Pre-populate sessions
		for i := 0; i < 50; i++ {
			sessionID := "session-" + string(rune('a'+i%26))
			mockSession := &mockClientSession{id: sessionID}
			sm.CreateSession(context.Background(), mockSession)
		}

		var wg sync.WaitGroup

		// Readers
		for i := 0; i < 50; i++ {
			wg.Add(1)
			go func(id int) {
				defer wg.Done()
				sessionID := "session-" + string(rune('a'+id%26))
				_, _ = sm.GetSession(sessionID)
			}(i)
		}

		// Writers (removers)
		for i := 0; i < 25; i++ {
			wg.Add(1)
			go func(id int) {
				defer wg.Done()
				sessionID := "session-" + string(rune('a'+id%26))
				mockSession := &mockClientSession{id: sessionID}
				sm.RemoveSession(context.Background(), mockSession)
			}(i)
		}

		wg.Wait()

		// Test passed if no race conditions occurred
	})
}

func TestAttachOncePattern(t *testing.T) {
	t.Run("verify sync.Once guarantees single execution", func(t *testing.T) {
		var once sync.Once
		var counter int32
		var wg sync.WaitGroup

		// Simulate the attachOnce guard used in InitializeAndRegisterProxiedTools.
		attach := func() {
			atomic.AddInt32(&counter, 1)
			// Simulate expensive attach work
			time.Sleep(50 * time.Millisecond)
		}

		// Launch many concurrent calls
		for i := 0; i < 1000; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				once.Do(attach)
			}()
		}

		wg.Wait()

		assert.Equal(t, int32(1), atomic.LoadInt32(&counter),
			"sync.Once should guarantee the attach runs exactly once")
	})
}

func TestSessionStateLifecycle(t *testing.T) {
	t.Run("create and get session", func(t *testing.T) {
		sm := NewSessionManager()
		defer sm.Close()

		mockSession := &mockClientSession{id: "test-session-123"}
		sm.CreateSession(context.Background(), mockSession)

		state, exists := sm.GetSession("test-session-123")
		assert.True(t, exists)
		require.NotNil(t, state)
		assert.Nil(t, state.proxiedSet)
	})

	t.Run("multiple sessions maintain separate state", func(t *testing.T) {
		sm := NewSessionManager()
		defer sm.Close()

		session1 := &mockClientSession{id: "session-1"}
		session2 := &mockClientSession{id: "session-2"}

		sm.CreateSession(context.Background(), session1)
		sm.CreateSession(context.Background(), session2)

		state1, _ := sm.GetSession("session-1")
		state2, _ := sm.GetSession("session-2")

		state1.mutex.Lock()
		state1.proxiedSet = &proxiedToolSet{}
		state1.mutex.Unlock()

		state1.mutex.RLock()
		set1 := state1.proxiedSet
		state1.mutex.RUnlock()
		state2.mutex.RLock()
		set2 := state2.proxiedSet
		state2.mutex.RUnlock()

		assert.NotNil(t, set1)
		assert.Nil(t, set2)
		assert.NotSame(t, state1, state2)
	})
}

// testBuildFunc matches ToolManager.buildSet: it returns the built results in
// local maps (never mutating shared state), so tests exercise the shared cache
// and its reference counting without real discovery or network I/O.
type testBuildFunc func(ctx context.Context, logger *slog.Logger) (builtProxiedTools, error)

// newTestToolManager builds a ToolManager with an injected set builder.
func newTestToolManager(t *testing.T, ttl time.Duration, build testBuildFunc) (*ToolManager, *SessionManager) {
	t.Helper()
	sm := NewSessionManager(WithSessionTTL(ttl))
	t.Cleanup(sm.Close)
	tm := NewToolManager(sm, nil, WithProxiedTools(true))
	tm.buildSet = build
	sm.SetToolManager(tm)
	return tm, sm
}

// builtWith returns a build result holding a single proxied client.
func builtWith(clientType, clientUID string) builtProxiedTools {
	return builtProxiedTools{
		clients: map[string]*ProxiedClient{
			clientType + "_" + clientUID: {DatasourceType: clientType, DatasourceUID: clientUID},
		},
		toolToDatasources: map[string][]string{},
	}
}

// setWith returns a builder that produces a single proxied client.
func setWith(clientType, clientUID string) testBuildFunc {
	return func(ctx context.Context, logger *slog.Logger) (builtProxiedTools, error) {
		return builtWith(clientType, clientUID), nil
	}
}

// ctxWithCreds returns a context carrying the given credential-bearing config.
func ctxWithCreds(grafanaURL, apiKey string, basicAuth *url.Userinfo, orgID int64) context.Context {
	return WithGrafanaConfig(context.Background(), GrafanaConfig{
		URL:       grafanaURL,
		APIKey:    apiKey,
		BasicAuth: basicAuth,
		OrgID:     orgID,
	})
}

// withExtraHeaders returns a copy of cfg with the given ExtraHeaders set.
func withExtraHeaders(cfg GrafanaConfig, headers map[string]string) GrafanaConfig {
	cfg.ExtraHeaders = headers
	return cfg
}

// TestProxiedToolSetSharing is the regression guard for the per-session
// duplication that caused proxied clients and tools to scale with the number of
// live sessions (linear memory growth, OOM under session churn). It verifies:
//   - multiple sessions with identical credentials share exactly ONE
//     proxiedToolSet (cache size stays 1);
//   - distinct credentials produce distinct entries;
//   - reaping/closing the last session for a key closes its clients exactly
//     once and removes the entry, while earlier detaches leave them open.
func TestProxiedToolSetSharing(t *testing.T) {
	t.Run("identical credentials share exactly one set", func(t *testing.T) {
		var builds int32
		tm, sm := newTestToolManager(t, time.Hour, func(ctx context.Context, logger *slog.Logger) (builtProxiedTools, error) {
			atomic.AddInt32(&builds, 1)
			return builtWith("tempo", "uid"), nil
		})

		ctx := ctxWithCreds("http://grafana", "secret", nil, 1)

		// Many concurrent sessions with identical credentials must all resolve to
		// the same shared set: before the fix each of these built and stored its
		// own copy of the clients and tools, so heap grew linearly with this count.
		const numSessions = 25
		var wg sync.WaitGroup
		wg.Add(numSessions)
		for i := 0; i < numSessions; i++ {
			go func(id int) {
				defer wg.Done()
				sess := &mockClientSession{id: "shared-" + string(rune('a'+id%26)) + "-" + string(rune('0'+id/26))}
				sm.CreateSession(ctx, sess)
				tm.InitializeAndRegisterProxiedTools(ctx, sess)
			}(i)
		}
		wg.Wait()

		tm.proxiedSetsMu.Lock()
		cacheSize := len(tm.proxiedSets)
		var refs int
		for _, s := range tm.proxiedSets {
			refs = s.refs
		}
		tm.proxiedSetsMu.Unlock()

		assert.Equal(t, 1, cacheSize, "identical credentials must produce exactly one shared set")
		assert.Equal(t, int32(1), atomic.LoadInt32(&builds), "the set must be built at most once per key")
		assert.Equal(t, numSessions, refs, "every session must hold a reference to the shared set")

		// All sessions must reference the very same underlying set pointer.
		s1, _ := sm.GetSession("shared-a-0")
		s2, _ := sm.GetSession("shared-b-0")
		require.NotNil(t, s1)
		require.NotNil(t, s2)
		s1.mutex.RLock()
		set1 := s1.proxiedSet
		s1.mutex.RUnlock()
		s2.mutex.RLock()
		set2 := s2.proxiedSet
		s2.mutex.RUnlock()
		assert.Same(t, set1, set2, "sessions with identical credentials must share the same set")
	})

	t.Run("distinct credentials produce distinct sets", func(t *testing.T) {
		tm, sm := newTestToolManager(t, time.Hour, setWith("tempo", "uid"))

		// Different URL, apiKey, orgID, basic-auth user, and basic-auth password
		// must each key to a distinct set. Also verify that identical credentials
		// (the repeated first entry) do not add a second entry.
		ctxs := []context.Context{
			ctxWithCreds("http://a", "k1", nil, 1),
			ctxWithCreds("http://a", "k1", nil, 1), // duplicate of the first
			ctxWithCreds("http://b", "k1", nil, 1),
			ctxWithCreds("http://a", "k2", nil, 1),
			ctxWithCreds("http://a", "k1", nil, 2),
			ctxWithCreds("http://a", "", url.UserPassword("u1", "p1"), 1),
			ctxWithCreds("http://a", "", url.UserPassword("u2", "p1"), 1),
			ctxWithCreds("http://a", "", url.UserPassword("u1", "p2"), 1),
		}

		for i, ctx := range ctxs {
			sess := &mockClientSession{id: "distinct-" + string(rune('a'+i))}
			sm.CreateSession(ctx, sess)
			tm.InitializeAndRegisterProxiedTools(ctx, sess)
		}

		tm.proxiedSetsMu.Lock()
		cacheSize := len(tm.proxiedSets)
		tm.proxiedSetsMu.Unlock()

		// 8 contexts, one is a duplicate, so 7 distinct keys.
		assert.Equal(t, 7, cacheSize, "each distinct credential set must produce its own entry")
	})

	t.Run("access and id tokens are part of the key", func(t *testing.T) {
		tm, sm := newTestToolManager(t, time.Hour, setWith("tempo", "uid"))

		base := GrafanaConfig{URL: "http://grafana", APIKey: "shared", OrgID: 1}

		cfgA := base
		cfgA.AccessToken = "userA-access"
		cfgA.IDToken = "userA-id"

		cfgB := base
		cfgB.AccessToken = "userB-access"
		cfgB.IDToken = "userB-id"

		for i, cfg := range []GrafanaConfig{cfgA, cfgB} {
			ctx := WithGrafanaConfig(context.Background(), cfg)
			sess := &mockClientSession{id: "token-" + string(rune('a'+i))}
			sm.CreateSession(ctx, sess)
			tm.InitializeAndRegisterProxiedTools(ctx, sess)
		}

		tm.proxiedSetsMu.Lock()
		cacheSize := len(tm.proxiedSets)
		tm.proxiedSetsMu.Unlock()

		assert.Equal(t, 2, cacheSize, "per-user access/id tokens must not collide on a shared instance")
	})

	t.Run("TLS material and extra headers are part of the key", func(t *testing.T) {
		tm, sm := newTestToolManager(t, time.Hour, setWith("tempo", "uid"))

		base := GrafanaConfig{URL: "http://grafana", APIKey: "shared", OrgID: 1}

		// Each of these differs from base only in a field that changes the built
		// client (TLS client identity, CA, skip-verify, or forwarded/extra
		// headers). Sharing one client across them would bleed one session's TLS
		// cert or headers into another's requests, so each must key distinctly.
		mkTLS := func(mutate func(*TLSConfig)) GrafanaConfig {
			cfg := base
			cfg.TLSConfig = &TLSConfig{}
			mutate(cfg.TLSConfig)
			return cfg
		}

		cfgs := []GrafanaConfig{
			base, // no TLS, no extra headers
			mkTLS(func(t *TLSConfig) { t.CertFile = "/a/client-a.pem"; t.KeyFile = "/a/client-a.key" }),
			mkTLS(func(t *TLSConfig) { t.CertFile = "/b/client-b.pem"; t.KeyFile = "/b/client-b.key" }),
			mkTLS(func(t *TLSConfig) { t.CAFile = "/ca/roots-a.pem" }),
			mkTLS(func(t *TLSConfig) { t.CAFile = "/ca/roots-b.pem" }),
			mkTLS(func(t *TLSConfig) { t.SkipVerify = true }),
			withExtraHeaders(base, map[string]string{"X-Webauth-User": "alice"}),
			withExtraHeaders(base, map[string]string{"X-Webauth-User": "bob"}),
		}
		// Append a duplicate of the alice-headers config to prove identical
		// header sets collapse to one entry.
		cfgs = append(cfgs, withExtraHeaders(base, map[string]string{"X-Webauth-User": "alice"}))

		for i, cfg := range cfgs {
			ctx := WithGrafanaConfig(context.Background(), cfg)
			sess := &mockClientSession{id: "tlskey-" + string(rune('a'+i))}
			sm.CreateSession(ctx, sess)
			tm.InitializeAndRegisterProxiedTools(ctx, sess)
		}

		tm.proxiedSetsMu.Lock()
		cacheSize := len(tm.proxiedSets)
		tm.proxiedSetsMu.Unlock()

		// 9 configs, one duplicate (alice headers), so 8 distinct keys.
		assert.Equal(t, 8, cacheSize, "TLS material and extra headers must each key distinctly (no client bleed)")
	})

	t.Run("ambiguous header maps do not collide", func(t *testing.T) {
		tm, sm := newTestToolManager(t, time.Hour, setWith("tempo", "uid"))

		base := GrafanaConfig{URL: "http://grafana", APIKey: "shared", OrgID: 1}

		// These two header maps serialize to the same string under a naive
		// name=value,name=value join, but are genuinely different and must key
		// distinctly. A collision would share one built client (header bleed).
		a := withExtraHeaders(base, map[string]string{"H": "a,b=c"})
		b := withExtraHeaders(base, map[string]string{"H": "a", "b": "c"})

		for i, cfg := range []GrafanaConfig{a, b} {
			ctx := WithGrafanaConfig(context.Background(), cfg)
			sess := &mockClientSession{id: "ambig-" + string(rune('a'+i))}
			sm.CreateSession(ctx, sess)
			tm.InitializeAndRegisterProxiedTools(ctx, sess)
		}

		tm.proxiedSetsMu.Lock()
		cacheSize := len(tm.proxiedSets)
		tm.proxiedSetsMu.Unlock()
		assert.Equal(t, 2, cacheSize, "distinct header maps must not collide under key serialization")
	})

	t.Run("timeout is part of the key", func(t *testing.T) {
		tm, sm := newTestToolManager(t, time.Hour, setWith("tempo", "uid"))

		base := GrafanaConfig{URL: "http://grafana", APIKey: "shared", OrgID: 1}
		a := base
		a.Timeout = 5 * time.Second
		b := base
		b.Timeout = 30 * time.Second

		for i, cfg := range []GrafanaConfig{a, b} {
			ctx := WithGrafanaConfig(context.Background(), cfg)
			sess := &mockClientSession{id: "timeout-" + string(rune('a'+i))}
			sm.CreateSession(ctx, sess)
			tm.InitializeAndRegisterProxiedTools(ctx, sess)
		}

		tm.proxiedSetsMu.Lock()
		cacheSize := len(tm.proxiedSets)
		tm.proxiedSetsMu.Unlock()
		assert.Equal(t, 2, cacheSize, "different Timeout values bake different transports, so must key distinctly")
	})

	t.Run("entry is removed only when the last session detaches", func(t *testing.T) {
		tm, sm := newTestToolManager(t, time.Hour, setWith("tempo", "uid"))

		ctx := ctxWithCreds("http://grafana", "secret", nil, 1)

		sessA := &mockClientSession{id: "life-a"}
		sessB := &mockClientSession{id: "life-b"}
		sm.CreateSession(ctx, sessA)
		sm.CreateSession(ctx, sessB)
		tm.InitializeAndRegisterProxiedTools(ctx, sessA)
		tm.InitializeAndRegisterProxiedTools(ctx, sessB)

		tm.proxiedSetsMu.Lock()
		require.Len(t, tm.proxiedSets, 1)
		var key proxiedToolSetKey
		for k, s := range tm.proxiedSets {
			key = k
			assert.Equal(t, 2, s.refs, "both sessions must reference the set")
		}
		tm.proxiedSetsMu.Unlock()

		// First session goes away: the shared set must survive because the other
		// session still uses it (closing here would break the live session).
		sm.RemoveSession(ctx, sessA)
		tm.proxiedSetsMu.Lock()
		s, ok := tm.proxiedSets[key]
		require.True(t, ok, "entry must survive while other sessions reference it")
		assert.Equal(t, 1, s.refs)
		tm.proxiedSetsMu.Unlock()

		// Last session goes away: the entry (and its clients) is torn down. The
		// entry is deleted in a single branch reached exactly once per key, so
		// its clients are closed exactly once.
		sm.RemoveSession(ctx, sessB)
		tm.proxiedSetsMu.Lock()
		_, ok = tm.proxiedSets[key]
		tm.proxiedSetsMu.Unlock()
		assert.False(t, ok, "entry must be removed once the last session detaches")
	})

	t.Run("release is idempotent per session", func(t *testing.T) {
		tm, sm := newTestToolManager(t, time.Hour, setWith("tempo", "uid"))

		ctx := ctxWithCreds("http://grafana", "secret", nil, 1)
		sess := &mockClientSession{id: "idem"}
		sm.CreateSession(ctx, sess)
		tm.InitializeAndRegisterProxiedTools(ctx, sess)

		state, ok := sm.GetSession("idem")
		require.True(t, ok)

		// Two releases of the same session must decrement refs exactly once, so
		// the entry is removed and stays removed. A second decrement would drive
		// refs below zero and could close another key's clients.
		tm.releaseSessionProxiedToolSet(state)
		tm.releaseSessionProxiedToolSet(state)

		tm.proxiedSetsMu.Lock()
		cacheSize := len(tm.proxiedSets)
		tm.proxiedSetsMu.Unlock()
		assert.Equal(t, 0, cacheSize, "the entry must be removed after the single owning session releases it")
	})
}

func TestReaperReleasesSharedSet(t *testing.T) {
	tm, sm := newTestToolManager(t, 100*time.Millisecond, setWith("tempo", "uid"))

	ctx := ctxWithCreds("http://grafana", "secret", nil, 1)
	sess := &mockClientSession{id: "reaped"}
	sm.CreateSession(ctx, sess)
	tm.InitializeAndRegisterProxiedTools(ctx, sess)

	tm.proxiedSetsMu.Lock()
	require.Len(t, tm.proxiedSets, 1)
	tm.proxiedSetsMu.Unlock()

	// Reaping the idle session must release the shared set (dropping refs to zero
	// and removing the entry), so idle-session churn cannot leak sets.
	require.Eventually(t, func() bool {
		tm.proxiedSetsMu.Lock()
		defer tm.proxiedSetsMu.Unlock()
		return len(tm.proxiedSets) == 0
	}, 2*time.Second, 25*time.Millisecond, "reaping the last session must release the shared set")
}

// mockClientSession implements server.ClientSession for testing
type mockClientSession struct {
	id            string
	notifChannel  chan mcp.JSONRPCNotification
	isInitialized bool
}

var _ server.ClientSession = (*mockClientSession)(nil)

func (m *mockClientSession) SessionID() string {
	return m.id
}

func (m *mockClientSession) NotificationChannel() chan<- mcp.JSONRPCNotification {
	if m.notifChannel == nil {
		m.notifChannel = make(chan mcp.JSONRPCNotification, 10)
	}
	return m.notifChannel
}

func (m *mockClientSession) Initialize() {
	m.isInitialized = true
}

func (m *mockClientSession) Initialized() bool {
	return m.isInitialized
}
