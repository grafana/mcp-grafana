package rbac

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

func TestCache_FetchOnceCachesUntilTTL(t *testing.T) {
	var calls int32
	fetcher := func(ctx context.Context, key string) (Snapshot, error) {
		atomic.AddInt32(&calls, 1)
		return Snapshot{
			Permissions: PermissionSet{"datasources:read": {"datasources:*"}},
			BasicRole:   "Editor",
		}, nil
	}
	c := NewCache(time.Hour, fetcher)
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		s, err := c.Get(ctx, "session-1")
		if err != nil {
			t.Fatal(err)
		}
		if s.BasicRole != "Editor" {
			t.Errorf("got %+v", s)
		}
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Errorf("expected single fetch, got %d", got)
	}
}

func TestCache_RefetchAfterTTL(t *testing.T) {
	var calls int32
	fetcher := func(ctx context.Context, key string) (Snapshot, error) {
		atomic.AddInt32(&calls, 1)
		return Snapshot{}, nil
	}
	c := NewCache(50*time.Millisecond, fetcher)
	ctx := context.Background()
	_, _ = c.Get(ctx, "k")
	time.Sleep(80 * time.Millisecond)
	_, _ = c.Get(ctx, "k")
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Errorf("expected 2 fetches after TTL, got %d", got)
	}
}

func TestCache_Singleflight_CoalescesConcurrent(t *testing.T) {
	var calls int32
	fetcher := func(ctx context.Context, key string) (Snapshot, error) {
		atomic.AddInt32(&calls, 1)
		time.Sleep(50 * time.Millisecond)
		return Snapshot{BasicRole: "Viewer"}, nil
	}
	c := NewCache(time.Hour, fetcher)
	ctx := context.Background()

	done := make(chan struct{})
	for i := 0; i < 10; i++ {
		go func() {
			defer func() { done <- struct{}{} }()
			_, _ = c.Get(ctx, "k")
		}()
	}
	for i := 0; i < 10; i++ {
		<-done
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Errorf("singleflight should collapse to 1 fetch, got %d", got)
	}
}

func TestCache_FetchErrorNotCached(t *testing.T) {
	calls := 0
	fetcher := func(ctx context.Context, key string) (Snapshot, error) {
		calls++
		return Snapshot{}, errors.New("boom")
	}
	c := NewCache(time.Hour, fetcher)
	ctx := context.Background()
	_, err := c.Get(ctx, "k")
	if err == nil {
		t.Fatal("expected error")
	}
	_, _ = c.Get(ctx, "k")
	if calls != 2 {
		t.Errorf("errors should not be cached; got %d fetches", calls)
	}
}

func TestCache_Invalidate(t *testing.T) {
	calls := 0
	fetcher := func(ctx context.Context, key string) (Snapshot, error) {
		calls++
		return Snapshot{}, nil
	}
	c := NewCache(time.Hour, fetcher)
	ctx := context.Background()
	_, _ = c.Get(ctx, "k")
	c.Invalidate("k")
	_, _ = c.Get(ctx, "k")
	if calls != 2 {
		t.Errorf("invalidate should force refetch; got %d", calls)
	}
}

func TestCache_RecordsHitsAndMisses(t *testing.T) {
	// Reset OTel meter provider to a manual reader for assertion.
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	original := otel.GetMeterProvider()
	otel.SetMeterProvider(mp)
	t.Cleanup(func() { otel.SetMeterProvider(original) })

	c := NewCache(time.Hour, func(ctx context.Context, k string) (Snapshot, error) {
		return Snapshot{BasicRole: "Viewer"}, nil
	})
	c.SetMetrics(NewMetrics())
	ctx := context.Background()

	// First call: miss + flight + populate.
	_, _ = c.Get(ctx, "k")
	// Subsequent calls: hits.
	for i := 0; i < 4; i++ {
		_, _ = c.Get(ctx, "k")
	}

	var rm metricdata.ResourceMetrics
	if err := reader.Collect(ctx, &rm); err != nil {
		t.Fatal(err)
	}
	hit, miss := readCounter(t, &rm, "mcp_auth_permission_cache_hit_total"), readCounter(t, &rm, "mcp_auth_permission_cache_miss_total")
	if hit != 4 {
		t.Errorf("hit count=%d, want 4", hit)
	}
	if miss != 1 {
		t.Errorf("miss count=%d, want 1", miss)
	}
}

// readCounter sums the int64 sum data points across all attribute sets for the given metric name.
func readCounter(t *testing.T, rm *metricdata.ResourceMetrics, name string) int64 {
	t.Helper()
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != name {
				continue
			}
			sum, ok := m.Data.(metricdata.Sum[int64])
			if !ok {
				t.Fatalf("metric %s has unexpected data type %T", name, m.Data)
			}
			var total int64
			for _, dp := range sum.DataPoints {
				total += dp.Value
			}
			return total
		}
	}
	return 0
}

func TestCache_DetachedFetchSurvivesCallerCancel(t *testing.T) {
	// First Get cancels its context immediately. The fetch must NOT see
	// that cancellation — it runs on a detached context so concurrent
	// waiters arent failed by one client's disconnect.
	fetchSawCancel := atomic.Bool{}
	fetcher := func(ctx context.Context, _ string) (Snapshot, error) {
		select {
		case <-ctx.Done():
			fetchSawCancel.Store(true)
			return Snapshot{}, ctx.Err()
		case <-time.After(50 * time.Millisecond):
			return Snapshot{Permissions: PermissionSet{"a:read": []string{"*"}}}, nil
		}
	}
	c := NewCache(time.Second, fetcher)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	snap, err := c.Get(ctx, "k")
	if err != nil {
		t.Fatalf("Get returned error %v from detached fetch", err)
	}
	if fetchSawCancel.Load() {
		t.Errorf("fetcher saw cancellation from caller ctx; expected detached ctx")
	}
	if _, ok := snap.Permissions["a:read"]; !ok {
		t.Errorf("missing expected snapshot data: %+v", snap)
	}
}

func TestCache_SweepDropsExpiredEntries(t *testing.T) {
	c := NewCache(10*time.Millisecond, func(_ context.Context, _ string) (Snapshot, error) {
		return Snapshot{}, nil
	})
	now := time.Now()
	c.entries["stale"] = &entry{expiresAt: now.Add(-time.Hour)}
	c.entries["fresh"] = &entry{expiresAt: now.Add(time.Hour)}
	c.mu.Lock()
	c.sweepEntriesLocked(now)
	c.mu.Unlock()
	if _, ok := c.entries["stale"]; ok {
		t.Errorf("expired cache entry was not swept")
	}
	if _, ok := c.entries["fresh"]; !ok {
		t.Errorf("fresh cache entry was incorrectly swept")
	}
}
