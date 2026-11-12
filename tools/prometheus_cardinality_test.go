//go:build unit

package tools

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	mcpgrafana "github.com/grafana/mcp-grafana/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPromCardinalityPolicy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"status":"success","data":{"seriesCountByMetricName":[{"name":"hot","value":1001},{"name":"small","value":1000}]}}`)
	}))
	defer srv.Close()
	for _, tc := range []struct {
		name, expr string
		scope      []string
		verdict    promGuardVerdict
		load       bool
	}{
		{"generic label", `hot{status="200"}`, nil, promGuardBlock, true},
		{"exact scope", `hot{namespace="prod"}`, nil, promGuardAllow, false},
		{"regex scope", `hot{namespace=~"prod|dev"}`, nil, promGuardBlock, true},
		{"empty scope", `hot{job=""}`, nil, promGuardBlock, true},
		{"negative scope", `hot{job!="api"}`, nil, promGuardBlock, true},
		{"empty selector", `hot{job!~".*"}`, nil, promGuardAllow, false},
		{"boundary", `small`, nil, promGuardAllow, true},
		{"absent top N", `missing`, nil, promGuardUnknown, true},
		{"regex metric", `{__name__=~"hot|small"}`, nil, promGuardUnknown, false},
		{"nameless", `{status="200"}`, nil, promGuardUnknown, false},
		{"metric matcher", `{__name__="hot"}`, nil, promGuardBlock, true},
		{"conflicting metric names", `{__name__="hot",__name__="small"}`, nil, promGuardAllow, false},
		{"conflicting metric names reversed", `{__name__="small",__name__="hot"}`, nil, promGuardAllow, false},
		{"negated exact metric name", `{__name__="hot",__name__!="hot"}`, nil, promGuardAllow, false},
		{"metric name excluded by regex", `{__name__="hot",__name__=~"small.*"}`, nil, promGuardAllow, false},
		{"metric name allowed by regex", `{__name__="hot",__name__=~"h.*"}`, nil, promGuardBlock, true},
		{"empty selector does not exempt another selector", `{__name__="hot",__name__!="hot"} or hot`, nil, promGuardBlock, true},
		{"binary per selector", `hot{job="api"} + hot{status="200"}`, nil, promGuardBlock, true},
		{"unknown does not hide known", `missing + hot`, nil, promGuardBlock, true},
		{"subquery", `max_over_time(rate(hot[5m])[1h:])`, nil, promGuardBlock, true},
		{"scalar", `vector(1)`, nil, promGuardAllow, false},
		{"parse failure", `rate(hot[$window])`, nil, promGuardUnknown, false},
		{"custom scope", `hot{tenant="one"}`, []string{"tenant"}, promGuardAllow, false},
		{"custom removes defaults", `hot{job="api"}`, []string{"tenant"}, promGuardBlock, true},
		{"metric name cannot scope", `hot`, []string{"__name__"}, promGuardBlock, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := mcpgrafana.GrafanaConfig{PrometheusGuardrailMaxMetricCardinality: 1000, PrometheusGuardrailCardinalityScopeLabels: tc.scope}
			ctx := mcpgrafana.WithGrafanaConfig(context.Background(), cfg)
			loaded := false
			d := assessPromCardinality(ctx, tc.expr, "instant", time.Time{}, time.Now(), 60, func() (*prometheusBackend, error) {
				loaded = true
				return &prometheusBackend{httpClient: srv.Client(), baseURL: srv.URL, tsdbBase: "/proxy"}, nil
			})
			assert.Equal(t, tc.verdict, d.Verdict)
			assert.Equal(t, tc.load, loaded)
			if d.Verdict == promGuardBlock {
				assert.Equal(t, promGuardReasonCardinality, d.Violations[0].Reason)
				assert.Contains(t, promGuardGuidance(d), "not the selector's matched series count")
			}
		})
	}
}

func TestPromCardinalityCacheConcurrency(t *testing.T) {
	c := newPromCardinalityCache()
	var key [32]byte
	started, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	fetch := func(ctx context.Context) (map[string]uint64, error) {
		calls.Add(1)
		close(started)
		select {
		case <-release:
			return map[string]uint64{"hot": 7}, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	var wg sync.WaitGroup
	for range 24 {
		wg.Go(func() {
			s, err := c.get(context.Background(), key, time.Minute, true, fetch)
			assert.NoError(t, err)
			assert.Equal(t, uint64(7), s["hot"])
		})
	}
	<-started
	// A canceled waiter must not cancel the shared refresh.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := c.get(ctx, key, time.Minute, true, fetch)
	require.ErrorIs(t, err, context.Canceled)
	close(release)
	wg.Wait()
	assert.Equal(t, int32(1), calls.Load())
}

func TestPromCardinalityCacheExpiryFailureAndBounds(t *testing.T) {
	c := newPromCardinalityCache()
	var key [32]byte
	calls := 0
	fetch := func(context.Context) (map[string]uint64, error) {
		calls++
		if calls > 1 {
			return nil, errors.New("offline")
		}
		return map[string]uint64{"hot": 9}, nil
	}
	_, err := c.get(context.Background(), key, time.Minute, true, fetch)
	require.NoError(t, err)
	c.entries[key].expires = time.Now().Add(-time.Second)
	stats, err := c.get(context.Background(), key, time.Minute, true, fetch)
	require.Error(t, err)
	require.Nil(t, stats)
	_, err = c.get(context.Background(), key, time.Minute, true, fetch)
	require.Error(t, err)
	assert.Equal(t, 2, calls)
	assert.WithinDuration(t, time.Now().Add(promCardinalityFailureTTL), c.entries[key].expires, time.Second)
	for i := range promCardinalityMaxEntries + 10 {
		k := [32]byte{byte(i), byte(i >> 8)}
		_, _ = c.get(context.Background(), k, time.Minute, true, func(context.Context) (map[string]uint64, error) { return nil, nil })
	}
	assert.Len(t, c.entries, promCardinalityMaxEntries)
}

func TestPromCardinalityCacheCancellationAndBypass(t *testing.T) {
	c := newPromCardinalityCache()
	var key [32]byte
	ctx, cancel := context.WithCancel(context.Background())
	_, err := c.get(ctx, key, time.Minute, true, func(ctx context.Context) (map[string]uint64, error) { cancel(); return nil, ctx.Err() })
	require.ErrorIs(t, err, context.Canceled)
	assert.Empty(t, c.entries)
	calls := 0
	for range 2 {
		_, err = c.get(context.Background(), key, time.Minute, false, func(context.Context) (map[string]uint64, error) { calls++; return nil, nil })
		require.NoError(t, err)
	}
	assert.Equal(t, 2, calls)
	assert.Empty(t, c.entries)
}

func TestPromCardinalityCacheRecoversAfterSaturation(t *testing.T) {
	c := newPromCardinalityCache()
	var key [32]byte
	ctx := context.Background()
	// Saturation fails open without starting another request.
	for range cap(c.slots) {
		c.slots <- struct{}{}
	}
	_, err := c.get(ctx, key, time.Minute, true, func(context.Context) (map[string]uint64, error) { t.Fatal("must not fetch"); return nil, nil })
	require.ErrorContains(t, err, "concurrency limit")
	<-c.slots
	// The same identity can refresh as soon as a slot is available.
	stats, err := c.get(ctx, key, time.Minute, true, func(context.Context) (map[string]uint64, error) {
		return map[string]uint64{"hot": 9}, nil
	})
	require.NoError(t, err)
	assert.Equal(t, uint64(9), stats["hot"])
}

func TestPromCardinalityCacheIdentity(t *testing.T) {
	b := &prometheusBackend{baseURL: "http://grafana", dsUID: "one", tsdbBase: "/proxy/one"}
	original := promCardinalityKey(b, mcpgrafana.GrafanaConfig{})
	for _, cfg := range []mcpgrafana.GrafanaConfig{
		{APIKey: "secret"}, {BasicAuth: url.UserPassword("user", "secret")}, {OrgID: 2},
		{AccessToken: "access"}, {IDToken: "identity"}, {ExtraHeaders: map[string]string{"X-Scope-OrgID": "tenant"}},
		{OverrideURL: "http://other"}, {SOCKS5ProxyURL: "socks5://other:1080"}, {TLSConfig: &mcpgrafana.TLSConfig{CertFile: "client.pem"}},
	} {
		assert.NotEqual(t, original, promCardinalityKey(b, cfg))
	}
	c1 := mcpgrafana.GrafanaConfig{ExtraHeaders: map[string]string{"a": "x", "b": "y"}}
	c2 := mcpgrafana.GrafanaConfig{ExtraHeaders: map[string]string{"b": "y", "a": "x"}}
	assert.Equal(t, promCardinalityKey(b, c1), promCardinalityKey(b, c2))
	b.dsUID = "other"
	assert.NotEqual(t, original, promCardinalityKey(b, mcpgrafana.GrafanaConfig{}))
}

func TestPromCardinalityHTTPFailureAndDeadline(t *testing.T) {
	for _, body := range []string{`<html>login</html>`, `{}`, `{"status":"success","data":{}}`, `{"status":"error","data":{"seriesCountByMetricName":[]}}`, `{"status":"success","data":{"seriesCountByMetricName":[{"name":"hot","value":-1}]}}`, `{"status":"success","data":{"seriesCountByMetricName":[{"name":"hot"}]}}`, `{"status":"success","data":{"seriesCountByMetricName":[{"name":"hot","value":1},{"name":"hot","value":2}]}}`} {
		t.Run(body, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, body) }))
			defer srv.Close()
			b := &prometheusBackend{httpClient: srv.Client(), baseURL: srv.URL, tsdbBase: "/proxy"}
			_, err := b.fetchCardinality(context.Background())
			require.Error(t, err)
		})
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer srv.Close()
	b := &prometheusBackend{httpClient: srv.Client(), baseURL: srv.URL, tsdbBase: "/proxy"}
	started := time.Now()
	_, err := newPromCardinalityCache().get(context.Background(), [32]byte{}, time.Minute, true, b.fetchCardinality)
	require.Error(t, err)
	assert.Less(t, time.Since(started), 3*time.Second)
}

func TestPromCardinalityActiveWaiterCancellation(t *testing.T) {
	c := newPromCardinalityCache()
	var key [32]byte
	started, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		_, _ = c.get(context.Background(), key, time.Minute, true, func(ctx context.Context) (map[string]uint64, error) {
			close(started)
			select {
			case <-release:
				return map[string]uint64{"hot": 1}, nil
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		})
	}()
	<-started
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err := c.get(ctx, key, time.Minute, true, func(context.Context) (map[string]uint64, error) { t.Fatal("waiter must not fetch"); return nil, nil })
	require.ErrorIs(t, err, context.DeadlineExceeded)
	close(release)
	<-done
	stats, err := c.get(context.Background(), key, time.Minute, true, func(context.Context) (map[string]uint64, error) {
		t.Fatal("cache must survive waiter cancellation")
		return nil, nil
	})
	require.NoError(t, err)
	assert.Equal(t, uint64(1), stats["hot"])
}

func TestPromCardinalityResponseBounds(t *testing.T) {
	for _, largeBody := range []bool{false, true} {
		t.Run(fmt.Sprint(largeBody), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if largeBody {
					_, _ = w.Write(make([]byte, promCardinalityResponseLimit+1))
					return
				}
				fmt.Fprint(w, `{"status":"success","data":{"seriesCountByMetricName":[`)
				for i := range 11 {
					if i > 0 {
						fmt.Fprint(w, ",")
					}
					fmt.Fprintf(w, `{"name":"m%d","value":1}`, i)
				}
				fmt.Fprint(w, `]}}`)
			}))
			defer srv.Close()
			b := &prometheusBackend{httpClient: srv.Client(), baseURL: srv.URL, tsdbBase: "/proxy"}
			_, err := b.fetchCardinality(context.Background())
			require.Error(t, err)
		})
	}
}
