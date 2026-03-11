package auth

import (
	"bytes"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"
)

// SessionAuthTransport is an http.RoundTripper that injects a Grafana session
// cookie into every request and automatically re-authenticates via browser
// login when the session expires (HTTP 401).
type SessionAuthTransport struct {
	inner      http.RoundTripper
	grafanaURL string
	store      *SessionStore

	mu     sync.Mutex
	cookie string
}

// NewSessionAuthTransport wraps inner with session cookie injection and
// automatic 401 re-login. If an existing cookie is available from the store
// it will be used immediately.
func NewSessionAuthTransport(inner http.RoundTripper, grafanaURL string, store *SessionStore) *SessionAuthTransport {
	t := &SessionAuthTransport{
		inner:      inner,
		grafanaURL: grafanaURL,
		store:      store,
	}
	t.cookie = store.Load(grafanaURL)
	return t
}

func (t *SessionAuthTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Buffer the body so it can be replayed on 401 retry.
	bodyBytes, err := drainBody(req)
	if err != nil {
		return nil, fmt.Errorf("failed to read request body: %w", err)
	}

	cookie := t.getCookie()

	if cookie == "" {
		slog.Info("No session cookie available, triggering browser login")
		if err := t.refreshSession(""); err != nil {
			return nil, fmt.Errorf("browser login required but failed: %w", err)
		}
		cookie = t.getCookie()
	}

	setBody(req, bodyBytes)
	resp, err := t.doWithCookie(req, cookie)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode == http.StatusUnauthorized {
		slog.Info("Session expired (401), triggering browser re-login")
		_ = resp.Body.Close()

		if err := t.refreshSession(cookie); err != nil {
			return nil, fmt.Errorf("session refresh failed: %w", err)
		}

		setBody(req, bodyBytes)
		return t.doWithCookie(req, t.getCookie())
	}

	return resp, nil
}

func (t *SessionAuthTransport) doWithCookie(req *http.Request, cookie string) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.AddCookie(&http.Cookie{
		Name:  grafanaSessionName,
		Value: cookie,
	})
	// Grafana enforces CSRF protection on cookie-authenticated requests.
	// These headers bypass CSRF checks the same way the Grafana UI does.
	if clone.Header.Get("X-Grafana-Org-Id") == "" {
		clone.Header.Set("X-Grafana-Org-Id", "1")
	}
	clone.Header.Set("X-Grafana-NoDirectAccess", "true")
	if clone.Header.Get("Origin") == "" {
		clone.Header.Set("Origin", t.grafanaURL)
	}
	if clone.Header.Get("Referer") == "" {
		clone.Header.Set("Referer", t.grafanaURL+"/")
	}
	return t.inner.RoundTrip(clone)
}

func (t *SessionAuthTransport) getCookie() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.cookie
}

// refreshSession re-authenticates via browser login. staleCookie is the cookie
// value the caller observed before deciding to refresh. If another goroutine
// already refreshed while we waited for the lock, we skip the browser login.
func (t *SessionAuthTransport) refreshSession(staleCookie string) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if staleCookie != "" && t.cookie != staleCookie {
		slog.Debug("Session already refreshed by another request, skipping browser login")
		return nil
	}

	newCookie, err := BrowserLogin(t.grafanaURL)
	if err != nil {
		return err
	}

	t.cookie = newCookie
	if err := t.store.Save(t.grafanaURL, newCookie); err != nil {
		slog.Warn("Failed to persist session cookie", "error", err)
	}
	return nil
}

func drainBody(req *http.Request) ([]byte, error) {
	if req.Body == nil || req.Body == http.NoBody {
		return nil, nil
	}
	data, err := io.ReadAll(req.Body)
	_ = req.Body.Close()
	return data, err
}

func setBody(req *http.Request, body []byte) {
	if body == nil {
		req.Body = http.NoBody
		req.ContentLength = 0
		return
	}
	req.Body = io.NopCloser(bytes.NewReader(body))
	req.ContentLength = int64(len(body))
}
