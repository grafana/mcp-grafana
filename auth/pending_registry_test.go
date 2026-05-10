package auth

import (
	"testing"
	"time"
)

// TestPendingRegistry_StoreConsume covers the basic happy path: an entry
// stored under a key is retrievable exactly once. A second Consume must
// return ok=false (one-shot semantics).
func TestPendingRegistry_StoreConsume(t *testing.T) {
	r := newPendingRegistry[string](time.Minute)
	r.Store("k", "v")

	got, ok := r.Consume("k")
	if !ok || got != "v" {
		t.Fatalf("Consume after Store: got=%q ok=%v want v/true", got, ok)
	}
	if _, ok := r.Consume("k"); ok {
		t.Errorf("Consume must be one-shot")
	}
}

// TestPendingRegistry_ConsumeTreatsExpiredAsMissing verifies that an
// entry which has lingered past TTL is rejected by Consume — the same
// per-entry guard the /authorize and /bootstrap flows rely on so a stale
// pending that survived between sweeps doesn't satisfy a callback.
func TestPendingRegistry_ConsumeTreatsExpiredAsMissing(t *testing.T) {
	r := newPendingRegistry[string](time.Minute)
	r.entries["stale"] = &pendingEntry[string]{value: "v", createdAt: time.Now().Add(-2 * time.Minute)}

	if _, ok := r.Consume("stale"); ok {
		t.Errorf("expected expired entry to be treated as missing")
	}
}

// TestPendingRegistry_PeekTreatsExpiredAsMissing mirrors the Consume test
// for the non-consuming Peek path used by the /bootstrap GET render.
func TestPendingRegistry_PeekTreatsExpiredAsMissing(t *testing.T) {
	r := newPendingRegistry[string](time.Minute)
	r.entries["stale"] = &pendingEntry[string]{value: "v", createdAt: time.Now().Add(-2 * time.Minute)}

	if _, ok := r.Peek("stale"); ok {
		t.Errorf("expected expired entry to be treated as missing")
	}
	// Peek must also remove the stale entry so it isn't kept around for
	// the next Peek.
	if _, ok := r.entries["stale"]; ok {
		t.Errorf("expired entry should be deleted after Peek")
	}
}

// TestPendingRegistry_SweepLocked_DropsExpiredEntries verifies that
// entries past TTL are evicted by the opportunistic sweep so a flood of
// unfinished flows can't grow the map without bound.
func TestPendingRegistry_SweepLocked_DropsExpiredEntries(t *testing.T) {
	now := time.Now()
	r := newPendingRegistry[string](time.Minute)
	r.entries["fresh"] = &pendingEntry[string]{value: "v", createdAt: now}
	r.entries["expired"] = &pendingEntry[string]{value: "v", createdAt: now.Add(-2 * time.Minute)}
	// Force the sweep gate to fire on the next call.
	r.lastSwept = now.Add(-2 * time.Minute)

	r.mu.Lock()
	r.sweepLocked(now)
	_, hasFresh := r.entries["fresh"]
	_, hasExpired := r.entries["expired"]
	r.mu.Unlock()

	if hasExpired {
		t.Errorf("expected expired entry to be swept")
	}
	if !hasFresh {
		t.Errorf("fresh entry should survive the sweep")
	}
}

// TestPendingRegistry_Delete returns true on hit, false on miss.
func TestPendingRegistry_Delete(t *testing.T) {
	r := newPendingRegistry[string](time.Minute)
	r.Store("k", "v")
	if !r.Delete("k") {
		t.Errorf("Delete on existing key should return true")
	}
	if r.Delete("k") {
		t.Errorf("Delete on missing key should return false")
	}
}
