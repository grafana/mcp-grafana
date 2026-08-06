package mcpgrafana

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
)

const (
	// grafanaSessionCookie is the name of the cookie Grafana uses to identify a
	// logged-in browser session.
	grafanaSessionCookie = "grafana_session"
	// grafanaSessionExpiryCookie carries the session's expiry as a companion to
	// grafanaSessionCookie. It is not required for authentication, but Grafana's
	// frontend sends it, so it is forwarded when available.
	grafanaSessionExpiryCookie = "grafana_session_expiry"

	// browserCookieAuthEnvVar enables browser session-cookie authentication.
	browserCookieAuthEnvVar = "GRAFANA_BROWSER_COOKIE_AUTH"
	// mcpGrafanaHomeEnvVar overrides the directory used to persist the session.
	mcpGrafanaHomeEnvVar = "MCP_GRAFANA_HOME"

	// cookieValidateTimeout bounds a single validation probe against Grafana.
	cookieValidateTimeout = 15 * time.Second
)

// CookieProvider supplies Grafana session cookies for outbound requests.
//
// It is deliberately consulted per request rather than resolved once into a
// string: Grafana rotates grafana_session server-side, so a cookie captured at
// startup goes stale while the process is still running. Returning cookies from
// a live provider lets a mid-session refresh take effect without rebuilding any
// clients.
//
// Implementations must be safe for concurrent use.
type CookieProvider interface {
	// Cookies returns the cookies to attach to a request, or nil if no valid
	// session is available.
	Cookies(ctx context.Context) []*http.Cookie

	// Refresh attempts to acquire a new session, bypassing any in-memory or
	// on-disk cache. It reports whether it obtained a session that differs from
	// the one previously held, i.e. whether retrying the request is worthwhile.
	Refresh(ctx context.Context) bool
}

// cookieCandidate is a session cookie sourced from somewhere (a saved file, a
// browser cookie store) that has not necessarily been validated yet.
type cookieCandidate struct {
	// value is the raw grafana_session cookie value.
	value string
	// expiry is the optional grafana_session_expiry companion cookie value.
	expiry string
	// expires is the cookie's own expiry as a Unix timestamp, or 0 if unknown.
	expires int64
	// source names where the cookie came from, for logging ("Chrome", "saved").
	source string
}

// cookies converts the candidate into the cookies to send on a request.
func (c cookieCandidate) cookies() []*http.Cookie {
	out := []*http.Cookie{{Name: grafanaSessionCookie, Value: c.value}}
	if c.expiry != "" {
		out = append(out, &http.Cookie{Name: grafanaSessionExpiryCookie, Value: c.expiry})
	}
	return out
}

// expired reports whether the candidate's own expiry has already passed.
// A zero expires means "unknown", which is treated as not expired — validation
// against Grafana is the real test.
func (c cookieCandidate) expired(now time.Time) bool {
	return c.expires > 0 && c.expires <= now.Unix()
}

// savedSession is the on-disk representation of a validated session cookie.
type savedSession struct {
	Cookie  string `json:"cookie"`
	Expiry  string `json:"expiry,omitempty"`
	Source  string `json:"source"`
	Expires int64  `json:"expires,omitempty"`
	SavedAt int64  `json:"saved_at"`
}

// sessionFilePath returns the path used to persist the validated session
// cookie. The directory is ~/.mcp-grafana by default, overridable with
// MCP_GRAFANA_HOME so that tests (and users with unusual homes) can redirect it.
func sessionFilePath() (string, error) {
	dir := os.Getenv(mcpGrafanaHomeEnvVar)
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("could not determine home directory: %w", err)
		}
		dir = filepath.Join(home, ".mcp-grafana")
	}
	return filepath.Join(dir, "session.json"), nil
}

// loadSavedSession reads a previously validated session cookie from disk.
// A missing, unreadable, malformed, or expired file yields no candidate; none
// of those are errors worth surfacing, since the caller falls through to a
// browser scrape.
func loadSavedSession(path string, now time.Time, logger *slog.Logger) (cookieCandidate, bool) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			logger.Debug("Could not read saved Grafana session", "path", path, "error", err)
		}
		return cookieCandidate{}, false
	}

	var s savedSession
	if err := json.Unmarshal(raw, &s); err != nil {
		logger.Warn("Saved Grafana session is malformed, ignoring", "path", path, "error", err)
		return cookieCandidate{}, false
	}
	if s.Cookie == "" {
		return cookieCandidate{}, false
	}

	c := cookieCandidate{
		value:   s.Cookie,
		expiry:  s.Expiry,
		expires: s.Expires,
		source:  "saved",
	}
	if c.expired(now) {
		logger.Debug("Saved Grafana session has expired", "path", path)
		return cookieCandidate{}, false
	}
	return c, true
}

// saveSession persists a validated session cookie with owner-only permissions.
// The cookie is a live credential, so the file is created 0600 and the
// containing directory 0700.
func saveSession(path string, c cookieCandidate, now time.Time) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("could not create session directory: %w", err)
	}

	body, err := json.MarshalIndent(savedSession{
		Cookie:  c.value,
		Expiry:  c.expiry,
		Source:  c.source,
		Expires: c.expires,
		SavedAt: now.Unix(),
	}, "", "  ")
	if err != nil {
		return fmt.Errorf("could not encode session: %w", err)
	}

	// Write via a temp file in the same directory so a crash mid-write cannot
	// leave a truncated session behind. OpenFile with 0600 (rather than
	// os.WriteFile then Chmod) means the credential is never briefly readable.
	tmp, err := os.CreateTemp(filepath.Dir(path), ".session-*.json")
	if err != nil {
		return fmt.Errorf("could not create temp session file: %w", err)
	}
	tmpName := tmp.Name()
	defer func() {
		// No-op once the rename has succeeded.
		_ = os.Remove(tmpName)
	}()

	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("could not set session file permissions: %w", err)
	}
	if _, err := tmp.Write(append(body, '\n')); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("could not write session file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("could not close session file: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("could not install session file: %w", err)
	}
	return nil
}

// browserCookieProvider resolves a Grafana session cookie by checking, in
// order, a previously saved session and then the session cookies held by the
// user's installed browsers. Every candidate is validated against Grafana
// before it is used or persisted.
type browserCookieProvider struct {
	grafanaURL  string
	sessionPath string
	logger      *slog.Logger

	// scrape returns session cookie candidates for host. It is a field rather
	// than a direct call so tests can inject candidates without touching a real
	// browser cookie store.
	scrape func(host string, logger *slog.Logger) []cookieCandidate

	// validate reports whether a candidate authenticates against Grafana.
	validate func(ctx context.Context, grafanaURL string, c cookieCandidate) bool

	// now supplies the current time, injectable for expiry tests.
	now func() time.Time

	mu      sync.RWMutex
	current cookieCandidate
	haveCur bool

	// flight collapses concurrent resolve/refresh calls. A burst of tool calls
	// all failing with 401 at once must produce one browser scrape, not one per
	// in-flight request.
	flight singleflight.Group
}

// NewBrowserCookieProvider creates a CookieProvider that sources Grafana
// session cookies from the user's browsers. grafanaURL must be an absolute
// http(s) URL; it determines both the cookie domain to search and the instance
// that candidates are validated against.
func NewBrowserCookieProvider(grafanaURL string, logger *slog.Logger) (CookieProvider, error) {
	if err := ValidateGrafanaURL(grafanaURL); err != nil {
		return nil, err
	}
	path, err := sessionFilePath()
	if err != nil {
		return nil, err
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &browserCookieProvider{
		grafanaURL:  strings.TrimRight(grafanaURL, "/"),
		sessionPath: path,
		logger:      logger,
		scrape:      scrapeBrowserCookies,
		validate:    validateSessionCookie,
		now:         time.Now,
	}, nil
}

// Cookies returns the cookies for the current session, resolving one on first
// use. It never blocks on a browser scrape once a session is held.
func (p *browserCookieProvider) Cookies(ctx context.Context) []*http.Cookie {
	p.mu.RLock()
	cur, ok := p.current, p.haveCur
	p.mu.RUnlock()
	if ok {
		return cur.cookies()
	}

	c, ok := p.resolve(ctx, false)
	if !ok {
		return nil
	}
	return c.cookies()
}

// Refresh re-resolves the session from the browser, skipping the saved file
// (which is what just failed). It reports whether the new session differs from
// the one previously held.
func (p *browserCookieProvider) Refresh(ctx context.Context) bool {
	p.mu.RLock()
	prev := p.current.value
	p.mu.RUnlock()

	c, ok := p.resolve(ctx, true)
	return ok && c.value != prev
}

// Resolve acquires and validates a session cookie, returning an error if none
// could be found. It exists so startup can fail loudly with an actionable
// message rather than deferring the failure to the first tool call.
func (p *browserCookieProvider) Resolve(ctx context.Context) error {
	if _, ok := p.resolve(ctx, false); ok {
		return nil
	}
	return fmt.Errorf(
		"no valid Grafana session found for %s: no saved session, and no logged-in session in any installed browser. "+
			"Log in to %s in your browser and restart, or set GRAFANA_SERVICE_ACCOUNT_TOKEN to use a service account token instead",
		p.grafanaURL, p.grafanaURL)
}

// resolve walks the candidate tiers and returns the first that validates,
// caching and persisting it. When force is true the saved-session tier is
// skipped, since a forced resolve follows a rejected cookie.
func (p *browserCookieProvider) resolve(ctx context.Context, force bool) (cookieCandidate, bool) {
	// The singleflight key distinguishes forced from unforced resolves so a
	// refresh is never satisfied by an in-flight first-use resolve that may be
	// about to return the very cookie the caller just saw rejected.
	key := "resolve"
	if force {
		key = "refresh"
	}

	res, _, _ := p.flight.Do(key, func() (any, error) {
		// A concurrent caller may have resolved while this one queued. Only
		// short-circuit for unforced resolves; a forced one must really re-scrape.
		if !force {
			p.mu.RLock()
			cur, ok := p.current, p.haveCur
			p.mu.RUnlock()
			if ok {
				return cur, nil
			}
		}

		now := p.now()
		var candidates []cookieCandidate
		if !force {
			if saved, ok := loadSavedSession(p.sessionPath, now, p.logger); ok {
				candidates = append(candidates, saved)
			}
		}

		host := grafanaCookieHost(p.grafanaURL)
		if host != "" {
			for _, c := range p.scrape(host, p.logger) {
				if !c.expired(now) {
					candidates = append(candidates, c)
				}
			}
		}

		for _, c := range candidates {
			if !p.validate(ctx, p.grafanaURL, c) {
				p.logger.Debug("Grafana session cookie did not authenticate, trying next source", "source", c.source)
				continue
			}

			p.mu.Lock()
			p.current, p.haveCur = c, true
			p.mu.Unlock()

			if err := saveSession(p.sessionPath, c, now); err != nil {
				// A cookie that works but could not be cached is still usable;
				// the only cost is re-scraping on the next start.
				p.logger.Warn("Could not persist Grafana session", "path", p.sessionPath, "error", err)
			}
			p.logger.Info("Authenticated to Grafana with a browser session cookie", "source", c.source)
			return c, nil
		}

		return cookieCandidate{}, fmt.Errorf("no valid session")
	})

	c, ok := res.(cookieCandidate)
	return c, ok && c.value != ""
}

// grafanaCookieHost extracts the hostname whose cookie jar should be searched.
func grafanaCookieHost(grafanaURL string) string {
	u, err := url.Parse(grafanaURL)
	if err != nil {
		return ""
	}
	return u.Hostname()
}

// validateSessionCookie reports whether a candidate cookie actually
// authenticates against Grafana.
//
// The probe deliberately targets /api/user rather than /api/health: /api/health
// is unauthenticated, so a dead cookie sails through it and then fails every
// real API call. /api/user requires a live session, which is exactly the
// property being tested.
func validateSessionCookie(ctx context.Context, grafanaURL string, c cookieCandidate) bool {
	ctx, cancel := context.WithTimeout(ctx, cookieValidateTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, grafanaURL+"/api/user", nil)
	if err != nil {
		return false
	}
	req.Header.Set("Accept", "application/json")
	for _, cookie := range c.cookies() {
		req.AddCookie(cookie)
	}

	client := &http.Client{
		Timeout: cookieValidateTimeout,
		// Do not follow redirects: an unauthenticated Grafana behind some
		// proxies answers with a 302 to a login page that itself returns 200,
		// which would otherwise read as success.
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()

	return resp.StatusCode == http.StatusOK
}

// sessionRefreshRoundTripper re-acquires a rotated Grafana session cookie when
// a request is rejected, then replays the request once.
//
// It sits outside the auth layer in the middleware chain so that the replay
// passes back through AuthRoundTripper and picks up the refreshed cookie.
type sessionRefreshRoundTripper struct {
	underlying http.RoundTripper
	provider   CookieProvider
	logger     *slog.Logger
}

// NewSessionRefreshRoundTripper wraps rt so that a 401/403 triggers one session
// refresh and replay. It returns rt unchanged when no provider is configured.
func NewSessionRefreshRoundTripper(rt http.RoundTripper, provider CookieProvider, logger *slog.Logger) http.RoundTripper {
	if provider == nil {
		return rt
	}
	if rt == nil {
		rt = http.DefaultTransport
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &sessionRefreshRoundTripper{underlying: rt, provider: provider, logger: logger}
}

func (rt *sessionRefreshRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := rt.underlying.RoundTrip(req)
	if err != nil || resp == nil {
		return resp, err
	}
	if resp.StatusCode != http.StatusUnauthorized && resp.StatusCode != http.StatusForbidden {
		return resp, nil
	}

	// A request whose body has already been consumed cannot be replayed. Go
	// populates GetBody for the body types it can rewind; anything else (an
	// opaque io.Reader) is not retryable.
	if req.Body != nil && req.GetBody == nil {
		rt.logger.Debug("Grafana rejected the session but the request body is not replayable; not retrying",
			"status", resp.StatusCode, "path", req.URL.Path)
		return resp, nil
	}

	if !rt.provider.Refresh(req.Context()) {
		rt.logger.Debug("Grafana session refresh produced no new cookie; not retrying",
			"status", resp.StatusCode, "path", req.URL.Path)
		return resp, nil
	}

	retryReq := req.Clone(req.Context())
	if req.GetBody != nil {
		body, err := req.GetBody()
		if err != nil {
			rt.logger.Debug("Could not rewind request body for session retry", "error", err)
			return resp, nil
		}
		retryReq.Body = body
	}
	// The request is replayed verbatim. Any Cookie header present here came
	// from ExtraHeaders/forwarded headers, not from this session — the auth
	// layer sits inside this one and adds the session cookie to its own clone,
	// so the refreshed value is picked up without touching the caller's header.

	// The original response is being discarded; drain it so the connection can
	// be reused for the replay.
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()

	rt.logger.Info("Grafana session was refreshed after an auth failure; retrying request",
		"status", resp.StatusCode, "path", req.URL.Path)

	// The replay is not itself retried: this round tripper is the only layer
	// that retries, and it is not re-entered by the call below.
	return rt.underlying.RoundTrip(retryReq)
}

// BrowserCookieAuthEnabled reports whether browser session-cookie auth has been
// requested via GRAFANA_BROWSER_COOKIE_AUTH. Exported so cmd/mcp-grafana can
// validate the flag combination without duplicating the parsing.
func BrowserCookieAuthEnabled() bool {
	v := strings.TrimSpace(os.Getenv(browserCookieAuthEnvVar))
	return v == "1" || strings.EqualFold(v, "true") || strings.EqualFold(v, "yes")
}

// SetupBrowserCookieAuth configures browser session-cookie authentication on
// cfg when it has been requested and nothing else is already authenticating.
//
// It resolves and validates a session up front rather than lazily, so that a
// missing browser login fails at startup with an explanation instead of
// surfacing as an opaque 401 on the first tool call.
//
// Returns an error if cookie auth was requested but cannot be used; returns nil
// (leaving cfg untouched) when it was not requested or is not needed.
func SetupBrowserCookieAuth(ctx context.Context, transport string, cfg *GrafanaConfig) error {
	if !BrowserCookieAuthEnabled() {
		return nil
	}
	logger := cfg.LoggerOrDefault()

	// A session cookie identifies the person who started the process. On a
	// shared HTTP server it would be applied to requests from every caller,
	// silently authenticating all of them as that person. Refuse rather than
	// degrade quietly.
	if transport != "stdio" {
		return fmt.Errorf(
			"browser cookie authentication is only supported on the stdio transport, but the %q transport is in use: "+
				"a browser session cookie identifies the user who started the server, so on a shared HTTP server it would "+
				"authenticate every client as that user. Use GRAFANA_SERVICE_ACCOUNT_TOKEN instead", transport)
	}

	grafanaURL, apiKey := urlAndAPIKeyFromEnv(logger)
	if grafanaURL == "" {
		grafanaURL = defaultGrafanaURL
	}
	if apiKey != "" || userAndPassFromEnv() != nil {
		logger.Info("Browser cookie authentication was requested but an explicit credential is configured; using that instead")
		return nil
	}

	provider, err := NewBrowserCookieProvider(grafanaURL, logger)
	if err != nil {
		return fmt.Errorf("could not set up browser cookie authentication: %w", err)
	}
	resolver, ok := provider.(*browserCookieProvider)
	if !ok {
		return fmt.Errorf("could not set up browser cookie authentication: unexpected provider type %T", provider)
	}
	if err := resolver.Resolve(ctx); err != nil {
		return err
	}

	cfg.CookieProvider = provider
	return nil
}
