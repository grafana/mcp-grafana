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

// Cache is a per-session, TTL'd, singleflight-deduplicated cache of Snapshots.
type Cache struct {
	ttl     time.Duration
	fetcher Fetcher

	mu      sync.RWMutex
	entries map[string]*entry

	flight singleflight.Group
}

type entry struct {
	snap      Snapshot
	expiresAt time.Time
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

// Get returns the cached snapshot if fresh, otherwise fetches.
func (c *Cache) Get(ctx context.Context, key string) (Snapshot, error) {
	if c.ttl > 0 {
		c.mu.RLock()
		if e, ok := c.entries[key]; ok && time.Now().Before(e.expiresAt) {
			c.mu.RUnlock()
			return e.snap, nil
		}
		c.mu.RUnlock()
	}

	v, err, _ := c.flight.Do(key, func() (any, error) {
		snap, err := c.fetcher(ctx, key)
		if err != nil {
			return Snapshot{}, err
		}
		if c.ttl > 0 {
			c.mu.Lock()
			c.entries[key] = &entry{snap: snap, expiresAt: time.Now().Add(c.ttl)}
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
