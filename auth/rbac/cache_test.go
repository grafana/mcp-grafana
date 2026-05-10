package rbac

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
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
