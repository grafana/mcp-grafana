package rbac

import (
	"context"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
)

// Snapshot is what we cache per session: the user's RBAC permission set plus
// their basic role.
type Snapshot struct {
	Permissions PermissionSet
	BasicRole   string // "Viewer" | "Editor" | "Admin" | ""
	FetchedAt   time.Time
}

// Fetcher loads a Snapshot for a session-keyed call. Implementations call
// Grafana with the per-user token bound to that session.
type Fetcher func(ctx context.Context, sessionKey string) (Snapshot, error)

// fetchTimeout bounds how long a single coalesced cache fetch may run on
// its detached context. The fetch is started by whichever Get() wins the
// singleflight race; subsequent waiters share the result. We can't use the
// caller's request context because cancelling that one would fail the fetch
// for every coalesced waiter — see TestCache_GetCoalescesConcurrentFetches.
const fetchTimeout = 10 * time.Second

// Cache is a per-session, TTL'd, singleflight-deduplicated cache of Snapshots.
type Cache struct {
	ttl     time.Duration
	fetcher Fetcher

	mu        sync.RWMutex
	entries   map[string]*entry
	lastSwept time.Time

	flight  singleflight.Group
	metrics *Metrics
}

type entry struct {
	snap      Snapshot
	expiresAt time.Time
}

// sweepEntriesLocked drops entries whose TTL has elapsed. The caller must
// hold c.mu. Runs at most once per TTL window so the amortised per-call
// cost stays O(1) under sustained traffic.
func (c *Cache) sweepEntriesLocked(now time.Time) {
	if c.ttl <= 0 {
		return
	}
	if now.Sub(c.lastSwept) < c.ttl {
		return
	}
	for k, e := range c.entries {
		if !now.Before(e.expiresAt) {
			delete(c.entries, k)
		}
	}
	c.lastSwept = now
}

// NewCache builds a cache with the given TTL. A zero ttl disables caching
// (every Get fetches).
func NewCache(ttl time.Duration, fetcher Fetcher) *Cache {
	return &Cache{
		ttl:     ttl,
		fetcher: fetcher,
		entries: make(map[string]*entry),
	}
}

// SetMetrics attaches an optional Metrics instance for cache hit/miss
// reporting. Safe to call concurrently with Get only if the caller can
// guarantee no Get is in-flight; in practice this should be called once
// at construction time.
func (c *Cache) SetMetrics(m *Metrics) {
	c.metrics = m
}

// Get returns the cached snapshot if fresh, otherwise fetches.
func (c *Cache) Get(ctx context.Context, key string) (Snapshot, error) {
	if c.ttl > 0 {
		c.mu.RLock()
		if e, ok := c.entries[key]; ok && time.Now().Before(e.expiresAt) {
			c.mu.RUnlock()
			c.metrics.CacheHit(ctx)
			return e.snap, nil
		}
		c.mu.RUnlock()
	}
	c.metrics.CacheMiss(ctx)

	v, err, _ := c.flight.Do(key, func() (any, error) {
		// Detach from the caller's context. Singleflight coalesces
		// concurrent waiters onto the goroutine that won the race; if we
		// forwarded that goroutine's request ctx and it cancelled (e.g.
		// client disconnect), every waiter would receive the cancellation
		// error and the hook would fail-open with unfiltered tool lists.
		// Bound the detached call with fetchTimeout so a hung upstream
		// can't keep the flight open indefinitely.
		fetchCtx, cancel := context.WithTimeout(context.Background(), fetchTimeout)
		defer cancel()
		snap, err := c.fetcher(fetchCtx, key)
		if err != nil {
			return Snapshot{}, err
		}
		if c.ttl > 0 {
			now := time.Now()
			c.mu.Lock()
			c.sweepEntriesLocked(now)
			c.entries[key] = &entry{snap: snap, expiresAt: now.Add(c.ttl)}
			c.mu.Unlock()
		}
		return snap, nil
	})
	if err != nil {
		return Snapshot{}, err
	}
	return v.(Snapshot), nil
}

// Invalidate removes the cached snapshot for a session. Safe to call for
// keys that were never cached.
func (c *Cache) Invalidate(key string) {
	c.mu.Lock()
	delete(c.entries, key)
	c.mu.Unlock()
}
