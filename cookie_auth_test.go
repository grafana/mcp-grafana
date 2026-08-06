package mcpgrafana

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// discardLogger keeps test output readable; the provider logs at Info on success.
func discardLogger() *slog.Logger {
	return slog.New(slog.DiscardHandler)
}

// staticProvider is a CookieProvider whose value can be swapped, used to drive
// the round tripper tests without any browser or filesystem involvement.
type staticProvider struct {
	mu           sync.Mutex
	value        string
	refreshTo    string
	refreshCalls atomic.Int32
}

func (p *staticProvider) Cookies(context.Context) []*http.Cookie {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.value == "" {
		return nil
	}
	return []*http.Cookie{{Name: grafanaSessionCookie, Value: p.value}}
}

func (p *staticProvider) Refresh(context.Context) bool {
	p.refreshCalls.Add(1)
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.refreshTo == "" || p.refreshTo == p.value {
		return false
	}
	p.value = p.refreshTo
	return true
}

// newTestProvider builds a browserCookieProvider with every external
// dependency (browser stores, Grafana, the clock, the home directory) faked.
func newTestProvider(t *testing.T, scraped []cookieCandidate, valid func(cookieCandidate) bool) *browserCookieProvider {
	t.Helper()
	return &browserCookieProvider{
		grafanaURL:  "http://grafana.example.com",
		sessionPath: filepath.Join(t.TempDir(), "session.json"),
		logger:      discardLogger(),
		scrape: func(string, *slog.Logger) []cookieCandidate {
			return scraped
		},
		validate: func(_ context.Context, _ string, c cookieCandidate) bool {
			return valid(c)
		},
		now: time.Now,
	}
}

func TestCookieDomainMatches(t *testing.T) {
	tests := []struct {
		name   string
		domain string
		host   string
		want   bool
	}{
		{"exact match", "grafana.example.com", "grafana.example.com", true},
		{"leading dot", ".example.com", "grafana.example.com", true},
		{"parent domain", "example.com", "grafana.example.com", true},
		{"case insensitive", "Example.COM", "grafana.example.com", true},
		{"unrelated domain", "evil.com", "grafana.example.com", false},
		// The reason a plain strings.HasSuffix is not enough: "evilexample.com"
		// ends with "example.com" but must not match it.
		{"suffix but not a subdomain", "example.com", "evilexample.com", false},
		{"child does not match parent", "grafana.example.com", "example.com", false},
		{"empty domain", "", "grafana.example.com", false},
		{"empty host", "example.com", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, cookieDomainMatches(tt.domain, tt.host))
		})
	}
}

func TestSaveAndLoadSession(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)

	t.Run("round trips and is owner-only", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "nested", "session.json")
		c := cookieCandidate{value: "abc", expiry: "exp", expires: now.Add(time.Hour).Unix(), source: "Chrome"}
		require.NoError(t, saveSession(path, c, now))

		info, err := os.Stat(path)
		require.NoError(t, err)
		// The file holds a live credential, so it must not be group/world readable.
		assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())

		got, ok := loadSavedSession(path, now, discardLogger())
		require.True(t, ok)
		assert.Equal(t, "abc", got.value)
		assert.Equal(t, "exp", got.expiry)
		assert.Equal(t, "saved", got.source)
	})

	t.Run("expired session is not loaded", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "session.json")
		c := cookieCandidate{value: "abc", expires: now.Add(-time.Hour).Unix()}
		require.NoError(t, saveSession(path, c, now))

		_, ok := loadSavedSession(path, now, discardLogger())
		assert.False(t, ok)
	})

	t.Run("session without expiry is loaded", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "session.json")
		require.NoError(t, saveSession(path, cookieCandidate{value: "abc"}, now))

		got, ok := loadSavedSession(path, now, discardLogger())
		require.True(t, ok)
		assert.Equal(t, "abc", got.value)
	})

	t.Run("missing and malformed files yield nothing", func(t *testing.T) {
		dir := t.TempDir()
		_, ok := loadSavedSession(filepath.Join(dir, "absent.json"), now, discardLogger())
		assert.False(t, ok)

		bad := filepath.Join(dir, "bad.json")
		require.NoError(t, os.WriteFile(bad, []byte("{not json"), 0o600))
		_, ok = loadSavedSession(bad, now, discardLogger())
		assert.False(t, ok)
	})
}

func TestBrowserCookieProviderResolve(t *testing.T) {
	ctx := context.Background()

	t.Run("uses a scraped cookie and persists it", func(t *testing.T) {
		p := newTestProvider(t,
			[]cookieCandidate{{value: "good", source: "Chrome"}},
			func(cookieCandidate) bool { return true },
		)
		require.NoError(t, p.Resolve(ctx))

		cookies := p.Cookies(ctx)
		require.Len(t, cookies, 1)
		assert.Equal(t, "good", cookies[0].Value)

		saved, ok := loadSavedSession(p.sessionPath, time.Now(), discardLogger())
		require.True(t, ok, "a validated cookie should be persisted")
		assert.Equal(t, "good", saved.value)
	})

	t.Run("skips candidates that do not authenticate", func(t *testing.T) {
		p := newTestProvider(t,
			[]cookieCandidate{
				{value: "stale", source: "Chrome"},
				{value: "fresh", source: "Firefox"},
			},
			func(c cookieCandidate) bool { return c.value == "fresh" },
		)
		require.NoError(t, p.Resolve(ctx))

		cookies := p.Cookies(ctx)
		require.Len(t, cookies, 1)
		assert.Equal(t, "fresh", cookies[0].Value)
	})

	t.Run("rejected cookies are never persisted", func(t *testing.T) {
		p := newTestProvider(t,
			[]cookieCandidate{{value: "stale", source: "Chrome"}},
			func(cookieCandidate) bool { return false },
		)
		err := p.Resolve(ctx)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "no valid Grafana session")
		assert.Nil(t, p.Cookies(ctx))

		_, ok := loadSavedSession(p.sessionPath, time.Now(), discardLogger())
		assert.False(t, ok, "a cookie that failed validation must not be cached")
	})

	t.Run("prefers a valid saved session over scraping", func(t *testing.T) {
		p := newTestProvider(t,
			[]cookieCandidate{{value: "from-browser", source: "Chrome"}},
			func(cookieCandidate) bool { return true },
		)
		require.NoError(t, saveSession(p.sessionPath, cookieCandidate{value: "from-disk"}, time.Now()))

		require.NoError(t, p.Resolve(ctx))
		cookies := p.Cookies(ctx)
		require.Len(t, cookies, 1)
		assert.Equal(t, "from-disk", cookies[0].Value)
	})

	t.Run("falls through to scraping when the saved session is expired", func(t *testing.T) {
		p := newTestProvider(t,
			[]cookieCandidate{{value: "from-browser", source: "Chrome"}},
			func(cookieCandidate) bool { return true },
		)
		require.NoError(t, saveSession(p.sessionPath,
			cookieCandidate{value: "from-disk", expires: time.Now().Add(-time.Hour).Unix()}, time.Now()))

		require.NoError(t, p.Resolve(ctx))
		cookies := p.Cookies(ctx)
		require.Len(t, cookies, 1)
		assert.Equal(t, "from-browser", cookies[0].Value)
	})

	t.Run("skips scraped cookies that have already expired", func(t *testing.T) {
		p := newTestProvider(t,
			[]cookieCandidate{
				{value: "expired", source: "Chrome", expires: time.Now().Add(-time.Minute).Unix()},
				{value: "live", source: "Firefox"},
			},
			func(cookieCandidate) bool { return true },
		)
		require.NoError(t, p.Resolve(ctx))

		cookies := p.Cookies(ctx)
		require.Len(t, cookies, 1)
		assert.Equal(t, "live", cookies[0].Value)
	})

	t.Run("sends the expiry companion cookie when present", func(t *testing.T) {
		p := newTestProvider(t,
			[]cookieCandidate{{value: "sess", expiry: "12345", source: "Chrome"}},
			func(cookieCandidate) bool { return true },
		)
		require.NoError(t, p.Resolve(ctx))

		cookies := p.Cookies(ctx)
		require.Len(t, cookies, 2)
		assert.Equal(t, grafanaSessionCookie, cookies[0].Name)
		assert.Equal(t, grafanaSessionExpiryCookie, cookies[1].Name)
		assert.Equal(t, "12345", cookies[1].Value)
	})
}

func TestBrowserCookieProviderRefresh(t *testing.T) {
	ctx := context.Background()

	t.Run("reports whether the session actually changed", func(t *testing.T) {
		current := "v1"
		p := newTestProvider(t, nil, func(cookieCandidate) bool { return true })
		p.scrape = func(string, *slog.Logger) []cookieCandidate {
			return []cookieCandidate{{value: current, source: "Chrome"}}
		}

		require.NoError(t, p.Resolve(ctx))
		assert.False(t, p.Refresh(ctx), "an unchanged cookie is not worth a retry")

		current = "v2"
		assert.True(t, p.Refresh(ctx))
		assert.Equal(t, "v2", p.Cookies(ctx)[0].Value)
	})

	t.Run("ignores the saved session so a rotated cookie is picked up", func(t *testing.T) {
		p := newTestProvider(t,
			[]cookieCandidate{{value: "rotated", source: "Chrome"}},
			func(cookieCandidate) bool { return true },
		)
		require.NoError(t, saveSession(p.sessionPath, cookieCandidate{value: "on-disk"}, time.Now()))
		require.NoError(t, p.Resolve(ctx))
		require.Equal(t, "on-disk", p.Cookies(ctx)[0].Value)

		assert.True(t, p.Refresh(ctx))
		assert.Equal(t, "rotated", p.Cookies(ctx)[0].Value)
	})

	t.Run("concurrent refreshes collapse into a single scrape", func(t *testing.T) {
		var scrapes atomic.Int32
		release := make(chan struct{})

		p := newTestProvider(t, nil, func(cookieCandidate) bool { return true })
		p.scrape = func(string, *slog.Logger) []cookieCandidate {
			scrapes.Add(1)
			// Hold the scrape open so every goroutine is queued behind it;
			// without singleflight each would run its own browser scrape.
			<-release
			return []cookieCandidate{{value: "shared", source: "Chrome"}}
		}

		const goroutines = 8
		var wg sync.WaitGroup
		wg.Add(goroutines)
		for range goroutines {
			go func() {
				defer wg.Done()
				p.Refresh(ctx)
			}()
		}

		// Give the goroutines time to pile up on the singleflight key.
		time.Sleep(50 * time.Millisecond)
		close(release)
		wg.Wait()

		assert.Equal(t, int32(1), scrapes.Load(),
			"a burst of 401s must trigger one browser scrape, not one per request")
	})
}

func TestValidateSessionCookie(t *testing.T) {
	ctx := context.Background()
	candidate := cookieCandidate{value: "sess", expiry: "exp"}

	t.Run("accepts a 200 and sends both cookies", func(t *testing.T) {
		var gotPath string
		var gotCookies []string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotPath = r.URL.Path
			for _, c := range r.Cookies() {
				gotCookies = append(gotCookies, c.Name+"="+c.Value)
			}
			w.WriteHeader(http.StatusOK)
		}))
		defer srv.Close()

		assert.True(t, validateSessionCookie(ctx, srv.URL, candidate))
		// /api/health is unauthenticated, so probing it would pass with a dead
		// cookie. The probe must target an endpoint that requires a session.
		assert.Equal(t, "/api/user", gotPath)
		assert.ElementsMatch(t, []string{"grafana_session=sess", "grafana_session_expiry=exp"}, gotCookies)
	})

	t.Run("rejects a 401", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
		}))
		defer srv.Close()

		assert.False(t, validateSessionCookie(ctx, srv.URL, candidate))
	})

	t.Run("rejects a redirect to a login page", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/api/user" {
				http.Redirect(w, r, "/login", http.StatusFound)
				return
			}
			// The login page itself answers 200; following the redirect would
			// make an unauthenticated instance look authenticated.
			w.WriteHeader(http.StatusOK)
		}))
		defer srv.Close()

		assert.False(t, validateSessionCookie(ctx, srv.URL, candidate))
	})

	t.Run("rejects an unreachable instance", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
		srv.Close()

		assert.False(t, validateSessionCookie(ctx, srv.URL, candidate))
	})
}

func TestAuthRoundTripperCookiePrecedence(t *testing.T) {
	newRequest := func() *http.Request {
		req, err := http.NewRequest(http.MethodGet, "http://grafana.example.com/api/user", nil)
		require.NoError(t, err)
		return req
	}

	t.Run("cookies are used when no other credential exists", func(t *testing.T) {
		var captured *http.Request
		rt := NewAuthRoundTripper(captureTransport(&captured), "", "", "", nil,
			WithCookieProvider(&staticProvider{value: "sess"}))

		_, err := rt.RoundTrip(newRequest())
		require.NoError(t, err)
		assert.Equal(t, "grafana_session=sess", captured.Header.Get("Cookie"))
		assert.Empty(t, captured.Header.Get("Authorization"))
	})

	t.Run("an API key takes precedence over cookies", func(t *testing.T) {
		var captured *http.Request
		rt := NewAuthRoundTripper(captureTransport(&captured), "", "", "token", nil,
			WithCookieProvider(&staticProvider{value: "sess"}))

		_, err := rt.RoundTrip(newRequest())
		require.NoError(t, err)
		assert.Equal(t, "Bearer token", captured.Header.Get("Authorization"))
		assert.Empty(t, captured.Header.Get("Cookie"), "a session cookie must never shadow an explicit credential")
	})

	t.Run("basic auth takes precedence over cookies", func(t *testing.T) {
		var captured *http.Request
		rt := NewAuthRoundTripper(captureTransport(&captured), "", "", "", url.UserPassword("u", "p"),
			WithCookieProvider(&staticProvider{value: "sess"}))

		_, err := rt.RoundTrip(newRequest())
		require.NoError(t, err)
		user, pass, ok := captured.BasicAuth()
		require.True(t, ok)
		assert.Equal(t, "u", user)
		assert.Equal(t, "p", pass)
		assert.Empty(t, captured.Header.Get("Cookie"))
	})

	t.Run("an existing Cookie header is preserved alongside the session cookie", func(t *testing.T) {
		var captured *http.Request
		rt := NewAuthRoundTripper(captureTransport(&captured), "", "", "", nil,
			WithCookieProvider(&staticProvider{value: "sess"}))

		req := newRequest()
		// Simulates a cookie placed by GRAFANA_EXTRA_HEADERS. The auth layer is
		// innermost, so a Header.Set here would silently discard it.
		req.Header.Set("Cookie", "tenant=acme")

		_, err := rt.RoundTrip(req)
		require.NoError(t, err)
		got := captured.Header.Get("Cookie")
		assert.Contains(t, got, "tenant=acme")
		assert.Contains(t, got, "grafana_session=sess")
	})

	t.Run("a provider in the context overrides the constructed one", func(t *testing.T) {
		var captured *http.Request
		rt := NewAuthRoundTripper(captureTransport(&captured), "", "", "", nil,
			WithCookieProvider(&staticProvider{value: "built-in"}))

		ctx := WithGrafanaConfig(context.Background(), GrafanaConfig{
			CookieProvider: &staticProvider{value: "from-context"},
		})
		_, err := rt.RoundTrip(newRequest().WithContext(ctx))
		require.NoError(t, err)
		assert.Equal(t, "grafana_session=from-context", captured.Header.Get("Cookie"))
	})
}

// captureTransport records the request it receives and returns an empty 200.
func captureTransport(into **http.Request) http.RoundTripper {
	return roundTripFunc(func(req *http.Request) (*http.Response, error) {
		*into = req
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader("")),
			Header:     make(http.Header),
		}, nil
	})
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

// nopTransport is a comparable RoundTripper, for assertions on chain identity.
type nopTransport struct{}

func (*nopTransport) RoundTrip(*http.Request) (*http.Response, error) { return nil, nil }

func TestSessionRefreshRoundTripper(t *testing.T) {
	t.Run("refreshes and replays once on 401", func(t *testing.T) {
		var seen []string
		provider := &staticProvider{value: "stale", refreshTo: "fresh"}

		// The auth layer sits inside the refresh layer, exactly as BuildTransport
		// arranges it, so the replay is re-signed with the refreshed cookie.
		inner := NewAuthRoundTripper(roundTripFunc(func(req *http.Request) (*http.Response, error) {
			seen = append(seen, req.Header.Get("Cookie"))
			status := http.StatusUnauthorized
			if req.Header.Get("Cookie") == "grafana_session=fresh" {
				status = http.StatusOK
			}
			return &http.Response{
				StatusCode: status,
				Body:       io.NopCloser(strings.NewReader("")),
				Header:     make(http.Header),
			}, nil
		}), "", "", "", nil, WithCookieProvider(provider))

		rt := NewSessionRefreshRoundTripper(inner, provider, discardLogger())
		req, err := http.NewRequest(http.MethodGet, "http://grafana.example.com/api/user", nil)
		require.NoError(t, err)

		resp, err := rt.RoundTrip(req)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Equal(t, []string{"grafana_session=stale", "grafana_session=fresh"}, seen)
		assert.Equal(t, int32(1), provider.refreshCalls.Load())
	})

	t.Run("does not retry when the refresh yields the same cookie", func(t *testing.T) {
		var calls int
		provider := &staticProvider{value: "stale"} // refreshTo empty: Refresh reports false

		rt := NewSessionRefreshRoundTripper(roundTripFunc(func(*http.Request) (*http.Response, error) {
			calls++
			return &http.Response{
				StatusCode: http.StatusUnauthorized,
				Body:       io.NopCloser(strings.NewReader("")),
				Header:     make(http.Header),
			}, nil
		}), provider, discardLogger())

		req, err := http.NewRequest(http.MethodGet, "http://grafana.example.com/api/user", nil)
		require.NoError(t, err)

		resp, err := rt.RoundTrip(req)
		require.NoError(t, err)
		assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
		assert.Equal(t, 1, calls, "a refresh that changed nothing must not trigger a retry")
	})

	t.Run("retries at most once", func(t *testing.T) {
		var calls int
		// A provider that reports a change every time would loop forever if the
		// replay were itself retried.
		provider := &countingRefreshProvider{}

		rt := NewSessionRefreshRoundTripper(roundTripFunc(func(*http.Request) (*http.Response, error) {
			calls++
			return &http.Response{
				StatusCode: http.StatusUnauthorized,
				Body:       io.NopCloser(strings.NewReader("")),
				Header:     make(http.Header),
			}, nil
		}), provider, discardLogger())

		req, err := http.NewRequest(http.MethodGet, "http://grafana.example.com/api/user", nil)
		require.NoError(t, err)

		resp, err := rt.RoundTrip(req)
		require.NoError(t, err)
		assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
		assert.Equal(t, 2, calls, "original plus exactly one replay")
	})

	t.Run("replays a request body", func(t *testing.T) {
		var bodies []string
		provider := &staticProvider{value: "stale", refreshTo: "fresh"}

		rt := NewSessionRefreshRoundTripper(roundTripFunc(func(req *http.Request) (*http.Response, error) {
			b, _ := io.ReadAll(req.Body)
			bodies = append(bodies, string(b))
			status := http.StatusUnauthorized
			if len(bodies) > 1 {
				status = http.StatusOK
			}
			return &http.Response{
				StatusCode: status,
				Body:       io.NopCloser(strings.NewReader("")),
				Header:     make(http.Header),
			}, nil
		}), provider, discardLogger())

		req, err := http.NewRequest(http.MethodPost, "http://grafana.example.com/api/ds/query",
			strings.NewReader(`{"queries":[]}`))
		require.NoError(t, err)

		resp, err := rt.RoundTrip(req)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Equal(t, []string{`{"queries":[]}`, `{"queries":[]}`}, bodies,
			"the replay must carry the same body as the original")
	})

	t.Run("does not retry a non-replayable body", func(t *testing.T) {
		var calls int
		provider := &staticProvider{value: "stale", refreshTo: "fresh"}

		rt := NewSessionRefreshRoundTripper(roundTripFunc(func(req *http.Request) (*http.Response, error) {
			calls++
			_, _ = io.Copy(io.Discard, req.Body)
			return &http.Response{
				StatusCode: http.StatusUnauthorized,
				Body:       io.NopCloser(strings.NewReader("")),
				Header:     make(http.Header),
			}, nil
		}), provider, discardLogger())

		req, err := http.NewRequest(http.MethodPost, "http://grafana.example.com/api/ds/query", io.NopCloser(strings.NewReader("x")))
		require.NoError(t, err)
		req.GetBody = nil // an opaque reader Go cannot rewind

		resp, err := rt.RoundTrip(req)
		require.NoError(t, err)
		assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
		assert.Equal(t, 1, calls, "a body that cannot be rewound must not be replayed")
		assert.Equal(t, int32(0), provider.refreshCalls.Load())
	})

	t.Run("passes through non-auth statuses untouched", func(t *testing.T) {
		provider := &staticProvider{value: "sess", refreshTo: "other"}
		rt := NewSessionRefreshRoundTripper(roundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusInternalServerError,
				Body:       io.NopCloser(strings.NewReader("")),
				Header:     make(http.Header),
			}, nil
		}), provider, discardLogger())

		req, err := http.NewRequest(http.MethodGet, "http://grafana.example.com/api/user", nil)
		require.NoError(t, err)

		resp, err := rt.RoundTrip(req)
		require.NoError(t, err)
		assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
		assert.Equal(t, int32(0), provider.refreshCalls.Load())
	})

	t.Run("is a no-op without a provider", func(t *testing.T) {
		// Uses a comparable transport type so identity can be asserted; func
		// values are not comparable in Go.
		base := &nopTransport{}
		assert.Same(t, base, NewSessionRefreshRoundTripper(base, nil, discardLogger()),
			"with no provider the chain must be left exactly as it was")
	})
}

// countingRefreshProvider always claims the session changed, so it would drive
// an unbounded retry loop if the replay were itself retried.
type countingRefreshProvider struct{ n atomic.Int32 }

func (p *countingRefreshProvider) Cookies(context.Context) []*http.Cookie {
	return []*http.Cookie{{Name: grafanaSessionCookie, Value: "v"}}
}

func (p *countingRefreshProvider) Refresh(context.Context) bool {
	p.n.Add(1)
	return true
}

func TestBuildTransportWiresCookieAuth(t *testing.T) {
	var captured *http.Request
	provider := &staticProvider{value: "sess"}

	cfg := GrafanaConfig{CookieProvider: provider}
	transport, err := BuildTransport(&cfg, captureTransport(&captured), WithoutOtel(), WithoutUserAgent())
	require.NoError(t, err)

	req, err := http.NewRequest(http.MethodGet, "http://grafana.example.com/api/user", nil)
	require.NoError(t, err)
	_, err = transport.RoundTrip(req)
	require.NoError(t, err)

	assert.Equal(t, "grafana_session=sess", captured.Header.Get("Cookie"))
}

func TestSetupBrowserCookieAuth(t *testing.T) {
	ctx := context.Background()

	t.Run("does nothing when not enabled", func(t *testing.T) {
		t.Setenv(browserCookieAuthEnvVar, "")
		cfg := GrafanaConfig{Logger: discardLogger()}
		require.NoError(t, SetupBrowserCookieAuth(ctx, "stdio", &cfg))
		assert.Nil(t, cfg.CookieProvider)
	})

	t.Run("refuses the HTTP transports", func(t *testing.T) {
		for _, transport := range []string{"sse", "streamable-http"} {
			t.Run(transport, func(t *testing.T) {
				t.Setenv(browserCookieAuthEnvVar, "true")
				cfg := GrafanaConfig{Logger: discardLogger()}

				err := SetupBrowserCookieAuth(ctx, transport, &cfg)
				require.Error(t, err, "cookie auth on a shared server would authenticate every client as the operator")
				assert.Contains(t, err.Error(), "only supported on the stdio transport")
				assert.Nil(t, cfg.CookieProvider)
			})
		}
	})

	t.Run("defers to an explicitly configured token", func(t *testing.T) {
		t.Setenv(browserCookieAuthEnvVar, "true")
		t.Setenv(grafanaServiceAccountTokenEnvVar, "a-token")
		cfg := GrafanaConfig{Logger: discardLogger()}

		require.NoError(t, SetupBrowserCookieAuth(ctx, "stdio", &cfg))
		assert.Nil(t, cfg.CookieProvider)
	})

	t.Run("defers to explicitly configured basic auth", func(t *testing.T) {
		t.Setenv(browserCookieAuthEnvVar, "true")
		t.Setenv(grafanaUsernameEnvVar, "admin")
		t.Setenv(grafanaPasswordEnvVar, "admin")
		cfg := GrafanaConfig{Logger: discardLogger()}

		require.NoError(t, SetupBrowserCookieAuth(ctx, "stdio", &cfg))
		assert.Nil(t, cfg.CookieProvider)
	})
}

func TestBrowserCookieAuthEnabled(t *testing.T) {
	for _, tc := range []struct {
		value string
		want  bool
	}{
		{"true", true}, {"TRUE", true}, {"1", true}, {"yes", true}, {" true ", true},
		{"", false}, {"false", false}, {"0", false}, {"no", false},
	} {
		t.Run("value="+tc.value, func(t *testing.T) {
			t.Setenv(browserCookieAuthEnvVar, tc.value)
			assert.Equal(t, tc.want, BrowserCookieAuthEnabled())
		})
	}
}

func TestExtractGrafanaInfoFromHeadersClearsCookieProvider(t *testing.T) {
	// A session cookie belongs to whoever started the process. If it survived
	// into a context built from an inbound HTTP request, every caller would be
	// authenticated as that person.
	ctx := WithGrafanaConfig(context.Background(), GrafanaConfig{
		CookieProvider: &staticProvider{value: "operator-session"},
		Logger:         discardLogger(),
	})

	req, err := http.NewRequest(http.MethodGet, "http://localhost:8000/mcp", nil)
	require.NoError(t, err)

	got := GrafanaConfigFromContext(ExtractGrafanaInfoFromHeaders(ctx, req))
	assert.Nil(t, got.CookieProvider)
}

func TestSessionFilePath(t *testing.T) {
	t.Run("honours MCP_GRAFANA_HOME", func(t *testing.T) {
		dir := t.TempDir()
		t.Setenv(mcpGrafanaHomeEnvVar, dir)

		path, err := sessionFilePath()
		require.NoError(t, err)
		assert.Equal(t, filepath.Join(dir, "session.json"), path)
	})

	t.Run("defaults under the home directory", func(t *testing.T) {
		t.Setenv(mcpGrafanaHomeEnvVar, "")
		home, err := os.UserHomeDir()
		require.NoError(t, err)

		path, err := sessionFilePath()
		require.NoError(t, err)
		assert.Equal(t, filepath.Join(home, ".mcp-grafana", "session.json"), path)
	})
}

func TestNewBrowserCookieProviderRejectsBadURL(t *testing.T) {
	_, err := NewBrowserCookieProvider("not-a-url", discardLogger())
	assert.Error(t, err)
}
