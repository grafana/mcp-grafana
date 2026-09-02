package mcpgrafana

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestProxiedToolSetKeyNotLoggedInClear guards that logging a proxiedToolSetKey
// emits the redacted form and never the raw secret values. slog does not honor
// fmt.Stringer for Any values, so the key implements slog.LogValuer.
func TestProxiedToolSetKeyNotLoggedInClear(t *testing.T) {
	// JSONHandler reflects an Any value's struct fields (unlike TextHandler,
	// which formats via fmt and would honor String()), so it is the handler that
	// actually leaks without a slog.LogValuer. Use it so this test truly guards.
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))

	key := proxiedToolSetKeyFromContext(WithGrafanaConfig(context.Background(), GrafanaConfig{
		URL:         "http://grafana",
		APIKey:      "super-secret-api-key",
		AccessToken: "super-secret-access-token",
		IDToken:     "super-secret-id-token",
	}))

	logger.Info("built proxied tool set", "key", key)

	out := buf.String()
	for _, secret := range []string{"super-secret-api-key", "super-secret-access-token", "super-secret-id-token"} {
		assert.False(t, strings.Contains(out, secret), "secret %q must not appear in logs; got: %s", secret, out)
	}
	assert.True(t, strings.Contains(out, "apiKey=true"), "redacted form should report presence, not value")
}

// TestTeardownReleaseToZeroDuringBuild is the real-overlap regression guard for
// the "build then publish" fix. It forces the dangerous interleaving the naive
// publish-then-build had: the builder signals it has STARTED and blocks; the
// last (only) session is torn down to refs==0 while the build is still running;
// then the builder finishes and returns its freshly-built clients.
//
// Two things must hold:
//   - No `fatal error: concurrent map iteration and map write`. Because the
//     build mutates only local maps and publishes them under proxiedSetsMu only
//     if refs>0, teardown (which iterates set.clients under the same lock) can
//     never observe a half-written map. Run under -race to shake this out.
//   - The freshly-built clients are closed (abandoned path) and the set ends at
//     refs==0, closed, unpublished (built==false), and removed from the cache.
//     Nothing leaks and nothing is used after close.
func TestTeardownReleaseToZeroDuringBuild(t *testing.T) {
	buildStarted := make(chan struct{})
	releaseBuild := make(chan struct{})
	var closes int32

	tm, sm := newTestToolManager(t, time.Hour, func(ctx context.Context, logger *slog.Logger) (builtProxiedTools, error) {
		close(buildStarted)
		<-releaseBuild // block here while the test tears the session down
		return builtProxiedTools{
			clients: map[string]*ProxiedClient{
				proxiedClientKey(1, "tempo", "uid"): newCloseCountingClient(1, "tempo", "uid", &closes),
			},
			toolToDatasources: map[string][]string{},
		}, nil
	})

	ctx := ctxWithCreds("http://grafana", "secret", nil, 1)
	sess := &mockClientSession{id: "midbuild"}
	sm.CreateSession(ctx, sess)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		tm.InitializeAndRegisterProxiedTools(ctx, sess)
	}()

	<-buildStarted // build is in flight; the session is already bound to the set

	// Grab the set the session is bound to so we can assert on it afterward.
	state, ok := sm.GetSession("midbuild")
	require.True(t, ok)
	state.mutex.RLock()
	set := state.proxiedSet
	state.mutex.RUnlock()
	require.NotNil(t, set, "attach must bind the set before the build runs")

	// Tear the session down to refs==0 WHILE the build is still blocked. This is
	// the concurrent teardown-vs-build window; releaseProxiedToolSet iterates
	// set.clients (empty placeholder) under proxiedSetsMu here.
	sm.RemoveSession(ctx, sess)

	// Now let the build finish. Its results must NOT be published (refs==0); the
	// abandoned path must close the freshly-built clients instead.
	close(releaseBuild)
	wg.Wait()

	tm.proxiedSetsMu.Lock()
	built := set.built
	closed := set.closed
	refs := set.refs
	cacheSize := len(tm.proxiedSets)
	tm.proxiedSetsMu.Unlock()

	assert.False(t, built, "live clients must NOT be published after teardown to zero")
	assert.True(t, closed, "the abandoned set must be marked closed")
	assert.Equal(t, 0, refs, "the reference taken before the build must be released")
	assert.Equal(t, 0, cacheSize, "the abandoned entry must be removed from the cache")
	assert.Equal(t, int32(1), atomic.LoadInt32(&closes), "the freshly-built client must be Closed exactly once")
}

// TestTeardownBetweenAttachAndBind guards the pre-bind ref-leak window. The old
// code took refs++ under proxiedSetsMu, then bound state.proxiedSet under
// state.mutex in a SEPARATE critical section; a teardown in that gap saw
// state.proxiedSet==nil and no-oped, leaking the ref forever.
//
// attachProxiedToolSet now binds the set under state.mutex while still holding
// proxiedSetsMu, and teardown must take proxiedSetsMu to decrement, so teardown
// cannot run until attach has fully bound. This test hammers attach and teardown
// concurrently for many sessions; every ref taken must be releasable, so the
// cache must drain to empty (no orphaned entry whose ref no teardown can find).
func TestTeardownBetweenAttachAndBind(t *testing.T) {
	// A build that yields so its execution overlaps concurrent teardown. Under
	// -race this also shakes out any lock-free write of shared set fields during
	// the build (the previous round's crash): the builder here returns local maps
	// only, so there must be no data race between build and teardown-iterate.
	build := func(ctx context.Context, logger *slog.Logger) (builtProxiedTools, error) {
		runtime.Gosched()
		return builtWith("tempo", "uid"), nil
	}
	tm, sm := newTestToolManager(t, time.Hour, build)

	ctx := ctxWithCreds("http://grafana", "secret", nil, 1)

	const n = 50
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		id := "race-" + string(rune('a'+i%26)) + "-" + string(rune('0'+i/26))
		sess := &mockClientSession{id: id}
		sm.CreateSession(ctx, sess)

		wg.Add(2)
		// Attach.
		go func() {
			defer wg.Done()
			tm.InitializeAndRegisterProxiedTools(ctx, sess)
		}()
		// Teardown, racing the attach: it may land before, during, or after the
		// attach/bind. Whenever it lands, the session's ref must be releasable.
		go func() {
			defer wg.Done()
			sm.RemoveSession(ctx, sess)
		}()
	}
	wg.Wait()

	// Any session that attached after its teardown ran keeps a live set; drain
	// those by removing again (idempotent for already-removed sessions).
	for i := 0; i < n; i++ {
		id := "race-" + string(rune('a'+i%26)) + "-" + string(rune('0'+i/26))
		sm.RemoveSession(ctx, &mockClientSession{id: id})
	}

	require.Eventually(t, func() bool {
		tm.proxiedSetsMu.Lock()
		defer tm.proxiedSetsMu.Unlock()
		return len(tm.proxiedSets) == 0
	}, 2*time.Second, 10*time.Millisecond, "every attached reference must be releasable; no entry may be orphaned")
}

// TestFailedBuildNotCached guards against a poisoned cache: a build that fails
// (discovery error / cancellation) must not leave an entry cached, so the next
// session for the same key rebuilds and can succeed.
func TestFailedBuildNotCached(t *testing.T) {
	var attempt int32
	tm, sm := newTestToolManager(t, time.Hour, func(ctx context.Context, logger *slog.Logger) (builtProxiedTools, error) {
		if atomic.AddInt32(&attempt, 1) == 1 {
			return builtProxiedTools{}, errors.New("transient discovery failure")
		}
		return builtWith("tempo", "uid"), nil
	})

	ctx := ctxWithCreds("http://grafana", "secret", nil, 1)

	// First session: build fails. The entry must not remain cached.
	sess1 := &mockClientSession{id: "fail-1"}
	sm.CreateSession(ctx, sess1)
	tm.InitializeAndRegisterProxiedTools(ctx, sess1)

	tm.proxiedSetsMu.Lock()
	sizeAfterFailure := len(tm.proxiedSets)
	tm.proxiedSetsMu.Unlock()
	assert.Equal(t, 0, sizeAfterFailure, "a failed build must not be left cached")

	// Second session, same credentials: must rebuild (attempt 2) and succeed.
	sess2 := &mockClientSession{id: "fail-2"}
	sm.CreateSession(ctx, sess2)
	tm.InitializeAndRegisterProxiedTools(ctx, sess2)

	tm.proxiedSetsMu.Lock()
	sizeAfterRetry := len(tm.proxiedSets)
	var refs int
	for _, s := range tm.proxiedSets {
		refs = s.refs
	}
	tm.proxiedSetsMu.Unlock()

	assert.Equal(t, int32(2), atomic.LoadInt32(&attempt), "the next session must retry the build")
	assert.Equal(t, 1, sizeAfterRetry, "the successful rebuild must be cached")
	assert.Equal(t, 1, refs, "only the second session references the rebuilt set")
}

// TestEmptyBuildCachedAndReused verifies that a SUCCESSFUL build that finds no
// MCP datasources is a stable "this instance has no proxied tools" result: the
// empty set is cached (not de-cached), the session is registered so it does not
// retry, and a second session for the same key reuses the cached empty set
// WITHOUT re-running discovery. This is the regression guard for empty builds
// re-running full discovery on every hook on no-MCP-datasource instances.
func TestEmptyBuildCachedAndReused(t *testing.T) {
	var discoveries int32
	tm, sm := newTestToolManager(t, time.Hour, func(ctx context.Context, logger *slog.Logger) (builtProxiedTools, error) {
		atomic.AddInt32(&discoveries, 1)
		// Successful discovery, but this instance exposes no MCP datasources.
		return builtProxiedTools{clients: map[string]*ProxiedClient{}, toolToDatasources: map[string][]string{}}, nil
	})

	ctx := ctxWithCreds("http://grafana", "secret", nil, 1)

	// First session: builds once, gets an empty set, registers, keeps its ref.
	sess1 := &mockClientSession{id: "empty-1"}
	sm.CreateSession(ctx, sess1)
	tm.InitializeAndRegisterProxiedTools(ctx, sess1)

	tm.proxiedSetsMu.Lock()
	sizeAfterEmpty := len(tm.proxiedSets)
	var refsAfter1 int
	for _, s := range tm.proxiedSets {
		refsAfter1 = s.refs
	}
	tm.proxiedSetsMu.Unlock()
	assert.Equal(t, 1, sizeAfterEmpty, "a successful empty build must be cached")
	assert.Equal(t, 1, refsAfter1, "the first session must keep a reference to the empty set")

	state1, ok := sm.GetSession("empty-1")
	require.True(t, ok)
	state1.proxiedInitMu.Lock()
	registered1 := state1.proxiedRegistered
	state1.proxiedInitMu.Unlock()
	assert.True(t, registered1, "the session must be registered on an empty-but-successful build")

	// Repeated hooks for the SAME session must not re-run discovery.
	tm.InitializeAndRegisterProxiedTools(ctx, sess1)
	tm.InitializeAndRegisterProxiedTools(ctx, sess1)
	assert.Equal(t, int32(1), atomic.LoadInt32(&discoveries), "repeated hooks for one session must not re-run discovery")

	// A second session with the SAME credentials must reuse the cached empty set,
	// not trigger a fresh discovery.
	sess2 := &mockClientSession{id: "empty-2"}
	sm.CreateSession(ctx, sess2)
	tm.InitializeAndRegisterProxiedTools(ctx, sess2)

	tm.proxiedSetsMu.Lock()
	sizeAfterReuse := len(tm.proxiedSets)
	var refsAfter2 int
	for _, s := range tm.proxiedSets {
		refsAfter2 = s.refs
	}
	tm.proxiedSetsMu.Unlock()
	assert.Equal(t, int32(1), atomic.LoadInt32(&discoveries), "a second same-key session must reuse the cached empty set (no re-discovery)")
	assert.Equal(t, 1, sizeAfterReuse, "the cached empty set must still be the only entry")
	assert.Equal(t, 2, refsAfter2, "both sessions must reference the shared empty set")

	// Both sessions must reference the very same underlying set pointer.
	s1set := sessionSet(t, sm, "empty-1")
	s2set := sessionSet(t, sm, "empty-2")
	assert.Same(t, s1set, s2set, "same-credential sessions must share one empty set")

	// Teardown must balance both references and remove the entry.
	sm.RemoveSession(ctx, sess1)
	sm.RemoveSession(ctx, sess2)
	tm.proxiedSetsMu.Lock()
	sizeAfterTeardown := len(tm.proxiedSets)
	tm.proxiedSetsMu.Unlock()
	assert.Equal(t, 0, sizeAfterTeardown, "teardown must release both references to the empty set")
}

// sessionSet returns the shared proxiedToolSet bound to a session, for identity
// assertions.
func sessionSet(t *testing.T, sm *SessionManager, id string) *proxiedToolSet {
	t.Helper()
	state, ok := sm.GetSession(id)
	require.True(t, ok)
	state.mutex.RLock()
	defer state.mutex.RUnlock()
	return state.proxiedSet
}

// TestBuildPanicUnblocksWaitersAndDeCaches guards that a panic in the builder
// still closes ready (so concurrent waiters do not block forever) and removes
// the entry from the cache (so a later session can rebuild).
func TestBuildPanicUnblocksWaitersAndDeCaches(t *testing.T) {
	var attempt int32
	tm, sm := newTestToolManager(t, time.Hour, func(ctx context.Context, logger *slog.Logger) (builtProxiedTools, error) {
		if atomic.AddInt32(&attempt, 1) == 1 {
			panic("boom during build")
		}
		return builtWith("tempo", "uid"), nil
	})

	ctx := ctxWithCreds("http://grafana", "secret", nil, 1)

	// The first attach triggers the panicking build. runProxiedToolSetBuild
	// recovers it, so InitializeAndRegisterProxiedTools must return (not panic)
	// and must not leave the session's goroutine blocked on <-ready.
	done := make(chan struct{})
	go func() {
		defer close(done)
		sess := &mockClientSession{id: "panic-1"}
		sm.CreateSession(ctx, sess)
		tm.InitializeAndRegisterProxiedTools(ctx, sess)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("attach blocked after a build panic: ready was not closed")
	}

	tm.proxiedSetsMu.Lock()
	sizeAfterPanic := len(tm.proxiedSets)
	tm.proxiedSetsMu.Unlock()
	assert.Equal(t, 0, sizeAfterPanic, "a panicked build must be de-cached so a later session rebuilds")

	// A later session with the same key must rebuild successfully.
	sess2 := &mockClientSession{id: "panic-2"}
	sm.CreateSession(ctx, sess2)
	tm.InitializeAndRegisterProxiedTools(ctx, sess2)

	tm.proxiedSetsMu.Lock()
	sizeAfterRetry := len(tm.proxiedSets)
	tm.proxiedSetsMu.Unlock()
	assert.Equal(t, int32(2), atomic.LoadInt32(&attempt), "the next session must retry after a build panic")
	assert.Equal(t, 1, sizeAfterRetry, "the rebuilt set must be cached")
}

// TestInFlightCallBlocksClose guards that a proxied client is not Closed while a
// tool call is in flight against it, even if the last session is torn down
// concurrently. The set is closed only once the in-flight call releases.
func TestInFlightCallBlocksClose(t *testing.T) {
	var closes int32
	tm, sm := newTestToolManager(t, time.Hour, func(ctx context.Context, logger *slog.Logger) (builtProxiedTools, error) {
		return builtProxiedTools{
			clients: map[string]*ProxiedClient{
				proxiedClientKey(1, "tempo", "uid"): newCloseCountingClient(1, "tempo", "uid", &closes),
			},
			toolToDatasources: map[string][]string{},
		}, nil
	})

	ctx := ctxWithCreds("http://grafana", "secret", nil, 1)
	sess := &mockClientSession{id: "inflight"}
	sm.CreateSession(ctx, sess)
	tm.InitializeAndRegisterProxiedTools(ctx, sess)

	state, ok := sm.GetSession("inflight")
	require.True(t, ok)
	state.mutex.RLock()
	set := state.proxiedSet
	state.mutex.RUnlock()
	require.NotNil(t, set)

	// Acquire a client for a call, then start last-session teardown concurrently.
	client, release, err := tm.acquireProxiedClientForCall(set, 1, "tempo", "uid")
	require.NoError(t, err)
	require.NotNil(t, client)

	teardownDone := make(chan struct{})
	go func() {
		defer close(teardownDone)
		sm.RemoveSession(ctx, sess) // drops the last ref while the call is in flight
	}()

	// Wait for teardown to drop the session ref. The client must NOT be Closed
	// yet, because a call is in flight.
	require.Eventually(t, func() bool {
		tm.proxiedSetsMu.Lock()
		defer tm.proxiedSetsMu.Unlock()
		return set.refs == 0
	}, 2*time.Second, 10*time.Millisecond, "teardown should have dropped the session reference")

	tm.proxiedSetsMu.Lock()
	closedWhileInFlight := set.closed
	inFlight := set.inFlight
	tm.proxiedSetsMu.Unlock()
	assert.False(t, closedWhileInFlight, "the set must not be closed while a call is in flight")
	assert.Equal(t, 1, inFlight, "the in-flight call must still be counted")
	assert.Equal(t, int32(0), atomic.LoadInt32(&closes), "no client may be Closed during an in-flight call")

	// The acquired client is the live one and stays usable while held.
	assert.NotNil(t, client)

	// The in-flight call completes and releases; only now may the set close.
	release()
	<-teardownDone

	tm.proxiedSetsMu.Lock()
	closedAfter := set.closed
	tm.proxiedSetsMu.Unlock()
	assert.True(t, closedAfter, "the set must close once the in-flight call releases and no session references it")
	assert.Equal(t, int32(1), atomic.LoadInt32(&closes), "the client must be Closed exactly once after the call returns")
}

// TestFailedBuildClosesClients guards that when a build errors AFTER connecting
// some clients, those freshly-built clients are closed rather than leaked. The
// failed set is de-cached and not published, so nothing else can close them.
func TestFailedBuildClosesClients(t *testing.T) {
	var closes int32
	tm, sm := newTestToolManager(t, time.Hour, func(ctx context.Context, logger *slog.Logger) (builtProxiedTools, error) {
		// A builder that connected a client, then hit an error.
		return builtProxiedTools{
			clients: map[string]*ProxiedClient{
				proxiedClientKey(1, "tempo", "uid"): newCloseCountingClient(1, "tempo", "uid", &closes),
			},
			toolToDatasources: map[string][]string{},
		}, errors.New("failed after connecting a client")
	})

	ctx := ctxWithCreds("http://grafana", "secret", nil, 1)
	sess := &mockClientSession{id: "failclose"}
	sm.CreateSession(ctx, sess)
	tm.InitializeAndRegisterProxiedTools(ctx, sess)

	tm.proxiedSetsMu.Lock()
	size := len(tm.proxiedSets)
	tm.proxiedSetsMu.Unlock()
	assert.Equal(t, 0, size, "a failed build must not be cached")
	assert.Equal(t, int32(1), atomic.LoadInt32(&closes), "clients connected before the error must be Closed, not leaked")
}

// newCloseCountingClient returns a ProxiedClient whose Close increments closes.
// Its underlying Client is nil, so Close only runs the counter (all these tests
// need to observe). The counter lets us assert "closed exactly once".
func newCloseCountingClient(orgID int64, dsType, dsUID string, closes *int32) *ProxiedClient {
	return &ProxiedClient{
		DatasourceType: dsType,
		DatasourceUID:  dsUID,
		OrgID:          orgID,
		closeHook:      func() { atomic.AddInt32(closes, 1) },
	}
}
