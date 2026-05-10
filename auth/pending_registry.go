package auth

import (
	"sync"
	"time"
)

// pendingRegistry is a per-instance TTL-keyed in-memory registry. The same
// type backs the OAuth /authorize → /callback handshake, the /bootstrap
// flow-token map, and each upstream's per-state pending map (OIDC PKCE
// verifiers, Grafana PKCE verifiers, SAML RequestIDs). Sharing one
// implementation keeps the sweep-on-write cadence and per-entry TTL guards
// identical across every call site, and moving the maps off package
// globals onto their owning struct removes a source of cross-test
// contamination.
type pendingRegistry[T any] struct {
	ttl time.Duration

	mu        sync.Mutex
	entries   map[string]*pendingEntry[T]
	lastSwept time.Time
}

type pendingEntry[T any] struct {
	value     T
	createdAt time.Time
}

func newPendingRegistry[T any](ttl time.Duration) *pendingRegistry[T] {
	return &pendingRegistry[T]{
		ttl:     ttl,
		entries: make(map[string]*pendingEntry[T]),
	}
}

// Store inserts or replaces an entry under key. Sweeps on write so the
// amortised per-call cost stays O(1) under sustained traffic.
func (r *pendingRegistry[T]) Store(key string, value T) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sweepLocked(time.Now())
	r.entries[key] = &pendingEntry[T]{value: value, createdAt: time.Now()}
}

// Consume removes the entry at key and returns its value. Returns
// zero/false when the key is missing OR the entry aged past TTL between
// sweep cycles; callers treat both cases as "unknown or expired".
func (r *pendingRegistry[T]) Consume(key string) (T, bool) {
	var zero T
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sweepLocked(time.Now())
	e, ok := r.entries[key]
	if !ok {
		return zero, false
	}
	delete(r.entries, key)
	if time.Since(e.createdAt) > r.ttl {
		return zero, false
	}
	return e.value, true
}

// Peek returns the value at key without removing it. Returns zero/false
// when the key is missing or aged past TTL — past-TTL entries are also
// dropped from the map under the same lock.
func (r *pendingRegistry[T]) Peek(key string) (T, bool) {
	var zero T
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sweepLocked(time.Now())
	e, ok := r.entries[key]
	if !ok {
		return zero, false
	}
	if time.Since(e.createdAt) > r.ttl {
		delete(r.entries, key)
		return zero, false
	}
	return e.value, true
}

// Delete removes an entry. Returns true if an entry was present. Used by
// rollback paths (e.g. SAMLUpstream.AuthorizeURL when the redirect-URL
// build step fails after the entry was already stored).
func (r *pendingRegistry[T]) Delete(key string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.entries[key]; !ok {
		return false
	}
	delete(r.entries, key)
	return true
}

// sweepLocked drops entries older than ttl. The caller must hold r.mu.
// Runs at most once per ttl window so the amortised cost stays O(1).
func (r *pendingRegistry[T]) sweepLocked(now time.Time) {
	if now.Sub(r.lastSwept) < r.ttl {
		return
	}
	cutoff := now.Add(-r.ttl)
	for k, e := range r.entries {
		if e.createdAt.Before(cutoff) {
			delete(r.entries, k)
		}
	}
	r.lastSwept = now
}
