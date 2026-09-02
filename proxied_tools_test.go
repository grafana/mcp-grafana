package mcpgrafana

import (
	"context"
	"errors"
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

// TestProxiedToolsRegistrationGuard covers the per-session registration guard
// that replaced the one-shot attachOnce: it must serialize concurrent hooks for
// one session into a single successful registration, and it must NOT let a
// failed/empty build consume the session's attempt (so a later hook retries).
func TestProxiedToolsRegistrationGuard(t *testing.T) {
	t.Run("concurrent hooks for one session build and register once", func(t *testing.T) {
		var builds int32
		tm, sm, srv := newToolManagerWithServer(t, func(ctx context.Context, logger *slog.Logger) (builtProxiedTools, error) {
			atomic.AddInt32(&builds, 1)
			time.Sleep(20 * time.Millisecond) // widen the window for concurrent hooks
			return builtWithTool("tempo", "uid", "tempo_example"), nil
		})

		ctx := ctxWithCreds("http://grafana", "secret", nil, 1)
		sess := newToolsCapableSession("guard-1")
		sm.CreateSession(ctx, sess)
		require.NoError(t, srv.RegisterSession(ctx, sess))

		// Fire many concurrent hook invocations for the SAME session.
		const hooks = 20
		var wg sync.WaitGroup
		wg.Add(hooks)
		for i := 0; i < hooks; i++ {
			go func() {
				defer wg.Done()
				tm.InitializeAndRegisterProxiedTools(ctx, sess)
			}()
		}
		wg.Wait()

		assert.Equal(t, int32(1), atomic.LoadInt32(&builds), "the set must be built exactly once")
		assert.Len(t, sess.GetSessionTools(), 1, "the session must be registered with its tool exactly once")

		state, ok := sm.GetSession("guard-1")
		require.True(t, ok)
		state.proxiedInitMu.Lock()
		registered := state.proxiedRegistered
		state.proxiedInitMu.Unlock()
		assert.True(t, registered, "the session must be marked registered after success")

		tm.proxiedSetsMu.Lock()
		refs := 0
		for _, s := range tm.proxiedSets {
			refs = s.refs
		}
		size := len(tm.proxiedSets)
		tm.proxiedSetsMu.Unlock()
		assert.Equal(t, 1, size, "exactly one shared set is cached")
		assert.Equal(t, 1, refs, "the session holds exactly one reference (no per-hook double-count)")
	})

	t.Run("failed build does not stick the session; a later hook retries and registers", func(t *testing.T) {
		var attempt int32
		tm, sm, srv := newToolManagerWithServer(t, func(ctx context.Context, logger *slog.Logger) (builtProxiedTools, error) {
			if atomic.AddInt32(&attempt, 1) == 1 {
				return builtProxiedTools{}, errors.New("transient discovery failure")
			}
			return builtWithTool("tempo", "uid", "tempo_example"), nil
		})

		ctx := ctxWithCreds("http://grafana", "secret", nil, 1)
		sess := newToolsCapableSession("retry-1")
		sm.CreateSession(ctx, sess)
		require.NoError(t, srv.RegisterSession(ctx, sess))

		// First hook: the build fails. The session must NOT be registered, must
		// hold no reference, and the failed set must not be cached.
		tm.InitializeAndRegisterProxiedTools(ctx, sess)

		state, ok := sm.GetSession("retry-1")
		require.True(t, ok)
		state.proxiedInitMu.Lock()
		registeredAfterFail := state.proxiedRegistered
		state.proxiedInitMu.Unlock()
		assert.False(t, registeredAfterFail, "a failed build must not mark the session registered")
		assert.Empty(t, sess.GetSessionTools(), "no tools registered after a failed build")

		tm.proxiedSetsMu.Lock()
		sizeAfterFail := len(tm.proxiedSets)
		tm.proxiedSetsMu.Unlock()
		assert.Equal(t, 0, sizeAfterFail, "the failed set must not be left cached and its ref must be released")

		// Second hook for the SAME session: the builder now succeeds. The session
		// must retry, build afresh, and end up registered with its tool.
		tm.InitializeAndRegisterProxiedTools(ctx, sess)

		assert.Equal(t, int32(2), atomic.LoadInt32(&attempt), "the second hook must retry the build")
		assert.Len(t, sess.GetSessionTools(), 1, "the session must be registered with its tool after retry")

		state.proxiedInitMu.Lock()
		registeredAfterRetry := state.proxiedRegistered
		state.proxiedInitMu.Unlock()
		assert.True(t, registeredAfterRetry, "the session must be marked registered after the successful retry")

		tm.proxiedSetsMu.Lock()
		sizeAfterRetry := len(tm.proxiedSets)
		var refs int
		for _, s := range tm.proxiedSets {
			refs = s.refs
		}
		tm.proxiedSetsMu.Unlock()
		assert.Equal(t, 1, sizeAfterRetry, "exactly one live set after the successful retry")
		assert.Equal(t, 1, refs, "no ref leak across the failed->success transition")

		// Teardown must balance the single reference: the set is released and its
		// clients closed.
		sm.RemoveSession(ctx, sess)
		tm.proxiedSetsMu.Lock()
		sizeAfterTeardown := len(tm.proxiedSets)
		tm.proxiedSetsMu.Unlock()
		assert.Equal(t, 0, sizeAfterTeardown, "teardown must release the session's reference")
	})
}

// TestProxiedToolSet_SessionAttachWaitBudget covers the fix for a session
// whose first hook invocation triggers (or attaches to) a proxiedToolSet build
// that is still running when tm.sessionAttachWaitBudget elapses. Before this
// fix, the triggering session's hook blocked for the ENTIRE build (which can
// take tens of seconds once a candidate needs retries), and if that session
// was torn down before the build finished, its later, otherwise-unreachable
// AddSessionTools call failed with "session not found". Bounding the wait
// instead lets the hook return promptly without registering anything, while
// the build keeps running in the background (on its own context, detached
// from any one caller) for whichever session asks next.
func TestProxiedToolSet_SessionAttachWaitBudget(t *testing.T) {
	// newBlockingBuild returns a testBuildFunc that signals buildStarted as soon
	// as it runs and then blocks until release is closed, so tests control
	// exactly when the build finishes instead of racing a real sleep duration.
	newBlockingBuild := func(buildStarted chan struct{}, release chan struct{}) testBuildFunc {
		return func(ctx context.Context, logger *slog.Logger) (builtProxiedTools, error) {
			close(buildStarted)
			<-release
			return builtWithTool("tempo", "uid", "tempo_example"), nil
		}
	}

	t.Run("a waiter that exceeds the budget returns without registering, and its retry picks up the completed build", func(t *testing.T) {
		buildStarted := make(chan struct{})
		release := make(chan struct{})
		tm, sm, srv := newToolManagerWithServer(t, newBlockingBuild(buildStarted, release))
		tm.sessionAttachWaitBudget = 10 * time.Millisecond

		ctx := ctxWithCreds("http://grafana", "secret", nil, 1)
		sess := newToolsCapableSession("budget-1")
		sm.CreateSession(ctx, sess)
		require.NoError(t, srv.RegisterSession(ctx, sess))

		// First hook: triggers the build, which blocks on release. The wait
		// budget elapses well before that, so this call must return promptly
		// without registering anything.
		start := time.Now()
		tm.InitializeAndRegisterProxiedTools(ctx, sess)
		elapsed := time.Since(start)

		<-buildStarted // the build did start (needsBuild path ran)
		assert.Less(t, elapsed, time.Second, "the hook must return once the wait budget elapses, not block for the whole build")
		assert.Empty(t, sess.GetSessionTools(), "no tools may be registered while the build is still running")

		state, ok := sm.GetSession("budget-1")
		require.True(t, ok)
		state.proxiedInitMu.Lock()
		registered := state.proxiedRegistered
		state.proxiedInitMu.Unlock()
		assert.False(t, registered, "a session that timed out waiting must not be marked registered")

		state.mutex.RLock()
		boundSet := state.proxiedSet
		state.mutex.RUnlock()
		require.NotNil(t, boundSet, "the session must keep its reference so the build isn't abandoned")

		// Let the build finish and publish.
		close(release)
		<-boundSet.ready

		// Second hook for the SAME session: must reuse the existing attachment
		// (not take a second reference) and register immediately since the set
		// is already published.
		tm.InitializeAndRegisterProxiedTools(ctx, sess)
		assert.Len(t, sess.GetSessionTools(), 1, "the retry must register the tool once the build has published")

		state.proxiedInitMu.Lock()
		registered = state.proxiedRegistered
		state.proxiedInitMu.Unlock()
		assert.True(t, registered, "the session must be marked registered after the retry succeeds")

		tm.proxiedSetsMu.Lock()
		var refs int
		for _, s := range tm.proxiedSets {
			refs = s.refs
		}
		tm.proxiedSetsMu.Unlock()
		assert.Equal(t, 1, refs, "the session must hold exactly one reference despite two attach attempts")
	})

	t.Run("a session torn down while waiting does not disrupt the shared build for a session that stays attached", func(t *testing.T) {
		buildStarted := make(chan struct{})
		release := make(chan struct{})
		tm, sm, srv := newToolManagerWithServer(t, newBlockingBuild(buildStarted, release))
		tm.sessionAttachWaitBudget = 10 * time.Millisecond

		ctx := ctxWithCreds("http://grafana", "secret", nil, 1)
		sessA := newToolsCapableSession("race-a")
		sessB := newToolsCapableSession("race-b")
		sm.CreateSession(ctx, sessA)
		sm.CreateSession(ctx, sessB)
		require.NoError(t, srv.RegisterSession(ctx, sessA))
		require.NoError(t, srv.RegisterSession(ctx, sessB))

		// A triggers the build and times out waiting for it.
		tm.InitializeAndRegisterProxiedTools(ctx, sessA)
		<-buildStarted
		assert.Empty(t, sessA.GetSessionTools())

		// B attaches to the SAME in-progress build (a follower, needsBuild=false)
		// and also times out waiting.
		tm.InitializeAndRegisterProxiedTools(ctx, sessB)
		assert.Empty(t, sessB.GetSessionTools())

		tm.proxiedSetsMu.Lock()
		require.Len(t, tm.proxiedSets, 1, "exactly one shared set, even though neither session has registered yet")
		var set *proxiedToolSet
		for _, s := range tm.proxiedSets {
			set = s
		}
		refsBoth := set.refs
		tm.proxiedSetsMu.Unlock()
		assert.Equal(t, 2, refsBoth, "both A and B hold a reference even though both are still (or were) waiting")

		// A is torn down while the build is still running. Because B still
		// references the set, it must survive: this is exactly the scenario that
		// used to end in "failed to add session tools: session not found" for A
		// once the build finally published -- with the bounded wait, A already
		// gave up long before this and never reaches that call at all.
		sm.RemoveSession(ctx, sessA)

		tm.proxiedSetsMu.Lock()
		_, stillCached := tm.proxiedSets[set.key]
		refsAfterA := set.refs
		tm.proxiedSetsMu.Unlock()
		assert.True(t, stillCached, "the set must survive A's teardown because B still references it")
		assert.Equal(t, 1, refsAfterA, "A's reference must be released, leaving B's")

		// Let the build finish and publish.
		close(release)
		<-set.ready

		// B's retry now finds the published set and registers.
		tm.InitializeAndRegisterProxiedTools(ctx, sessB)
		assert.Len(t, sessB.GetSessionTools(), 1, "B's retry must register the tool once the build has published")

		tm.proxiedSetsMu.Lock()
		refsFinal := set.refs
		tm.proxiedSetsMu.Unlock()
		assert.Equal(t, 1, refsFinal, "B must hold exactly one reference despite two attach attempts")

		// Teardown balances B's single reference.
		sm.RemoveSession(ctx, sessB)
		tm.proxiedSetsMu.Lock()
		sizeAfterTeardown := len(tm.proxiedSets)
		tm.proxiedSetsMu.Unlock()
		assert.Equal(t, 0, sizeAfterTeardown, "teardown must release B's reference and drop the now-unused entry")
	})

	t.Run("credentials rotating during a timed-out wait release the stale set instead of leaking it", func(t *testing.T) {
		// Key A's build blocks; key B's build returns immediately. Branching on
		// the context lets a single injected builder serve both keys.
		aStarted := make(chan struct{})
		aRelease := make(chan struct{})
		build := func(ctx context.Context, logger *slog.Logger) (builtProxiedTools, error) {
			if GrafanaConfigFromContext(ctx).URL == "http://a" {
				close(aStarted)
				<-aRelease
			}
			return builtWithTool("tempo", "uid", "tempo_example"), nil
		}
		tm, sm, srv := newToolManagerWithServer(t, build)
		tm.sessionAttachWaitBudget = 10 * time.Millisecond

		sess := newToolsCapableSession("rotate-1")
		ctxA := ctxWithCreds("http://a", "secret", nil, 1)
		sm.CreateSession(ctxA, sess)
		require.NoError(t, srv.RegisterSession(ctxA, sess))

		// First hook: attaches to key A's set and times out waiting on its
		// (still blocked) build.
		tm.InitializeAndRegisterProxiedTools(ctxA, sess)
		<-aStarted
		assert.Empty(t, sess.GetSessionTools())

		tm.proxiedSetsMu.Lock()
		require.Len(t, tm.proxiedSets, 1, "key A's set is cached")
		var setA *proxiedToolSet
		for _, s := range tm.proxiedSets {
			setA = s
		}
		refsA := setA.refs
		tm.proxiedSetsMu.Unlock()
		assert.Equal(t, 1, refsA, "the session holds A's only reference while its wait is pending")

		// Second hook for the SAME session, but with rotated credentials (key
		// B) -- e.g. a refreshed access/ID token. Before the fix, this
		// overwrote state.proxiedSet from A to B without ever releasing A's
		// reference, leaking it (and eventually its clients) forever.
		ctxB := ctxWithCreds("http://b", "secret", nil, 1)
		tm.InitializeAndRegisterProxiedTools(ctxB, sess)

		tm.proxiedSetsMu.Lock()
		refsAAfterRotation := setA.refs
		var setB *proxiedToolSet
		for _, s := range tm.proxiedSets {
			if s != setA {
				setB = s
			}
		}
		tm.proxiedSetsMu.Unlock()
		assert.Equal(t, 0, refsAAfterRotation, "the stale reference to A must be released on credential rotation, not leaked")
		require.NotNil(t, setB, "key B must have its own set")
		assert.Len(t, sess.GetSessionTools(), 1, "B's build is immediate, so the session registers on this same call")

		// Let A's now-abandoned (refs==0) build finish; the existing
		// abandoned-build path closes it without publishing.
		close(aRelease)
		<-setA.ready

		sm.RemoveSession(ctxB, sess)
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

// newTestToolManager builds a ToolManager with an injected set builder and a nil
// MCP server. Suitable for tests that never reach per-session tool registration
// (they use no-tool builds); reaching AddSessionTools would panic on the nil
// server, so use newToolManagerWithServer for registration-path tests.
func newTestToolManager(t *testing.T, ttl time.Duration, build testBuildFunc) (*ToolManager, *SessionManager) {
	t.Helper()
	sm := NewSessionManager(WithSessionTTL(ttl))
	t.Cleanup(sm.Close)
	tm := NewToolManager(sm, nil, WithProxiedTools(true))
	tm.buildSet = build
	return tm, sm
}

// newToolManagerWithServer is like newTestToolManager but wires a real
// server.MCPServer, so builds that produce tools can exercise the per-session
// AddSessionTools registration path. Sessions must be RegisterSession'd on the
// returned server and must implement server.SessionWithTools.
func newToolManagerWithServer(t *testing.T, build testBuildFunc) (*ToolManager, *SessionManager, *server.MCPServer) {
	t.Helper()
	sm := NewSessionManager(WithSessionTTL(time.Hour))
	t.Cleanup(sm.Close)
	srv := server.NewMCPServer("test", "1.0")
	tm := NewToolManager(sm, srv, WithProxiedTools(true))
	tm.buildSet = build
	return tm, sm, srv
}

// builtWith returns a build result holding a single proxied client and no tools.
func builtWith(clientType, clientUID string) builtProxiedTools {
	return builtProxiedTools{
		clients: map[string]*ProxiedClient{
			proxiedClientKey(1, clientType, clientUID): {DatasourceType: clientType, DatasourceUID: clientUID, OrgID: 1},
		},
		toolToDatasources: map[string][]string{},
		connectionOrgID:   1,
	}
}

// builtWithTool returns a build result holding a single proxied client and one
// tool, so it reaches the per-session registration path (AddSessionTools).
func builtWithTool(clientType, clientUID, toolName string) builtProxiedTools {
	b := builtWith(clientType, clientUID)
	b.tools = []mcp.Tool{mcp.NewTool(toolName)}
	return b
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

// toolsCapableSession implements server.SessionWithTools so AddSessionTools can
// register per-session tools on it. Its tool map is guarded for the concurrent
// hook test.
type toolsCapableSession struct {
	id    string
	mu    sync.RWMutex
	tools map[string]server.ServerTool
}

func newToolsCapableSession(id string) *toolsCapableSession {
	return &toolsCapableSession{id: id, tools: map[string]server.ServerTool{}}
}

var _ server.SessionWithTools = (*toolsCapableSession)(nil)

func (s *toolsCapableSession) SessionID() string                                   { return s.id }
func (s *toolsCapableSession) NotificationChannel() chan<- mcp.JSONRPCNotification { return nil }
func (s *toolsCapableSession) Initialize()                                         {}
func (s *toolsCapableSession) Initialized() bool                                   { return true }

func (s *toolsCapableSession) GetSessionTools() map[string]server.ServerTool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]server.ServerTool, len(s.tools))
	for k, v := range s.tools {
		out[k] = v
	}
	return out
}

func (s *toolsCapableSession) SetSessionTools(tools map[string]server.ServerTool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tools = tools
}
