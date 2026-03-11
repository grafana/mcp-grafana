package auth

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type sessionEntry struct {
	Cookie  string    `json:"cookie"`
	SavedAt time.Time `json:"saved_at"`
}

// SessionStore persists Grafana session cookies to disk, keyed by host.
type SessionStore struct {
	mu   sync.Mutex
	path string
}

// NewSessionStore creates a store backed by ~/.config/mcp-grafana/sessions.json.
func NewSessionStore() *SessionStore {
	dir := configDir()
	return &SessionStore{path: filepath.Join(dir, "sessions.json")}
}

// Load returns the saved session cookie for a Grafana URL, or "" if none.
func (s *SessionStore) Load(grafanaURL string) string {
	s.mu.Lock()
	defer s.mu.Unlock()

	host := hostFromURL(grafanaURL)
	entries := s.readAll()
	if e, ok := entries[host]; ok {
		slog.Debug("Loaded session from disk", "host", host, "saved_at", e.SavedAt)
		return e.Cookie
	}
	return ""
}

// Save persists the session cookie for a Grafana URL.
func (s *SessionStore) Save(grafanaURL, cookie string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	host := hostFromURL(grafanaURL)
	entries := s.readAll()
	entries[host] = sessionEntry{
		Cookie:  cookie,
		SavedAt: time.Now().UTC(),
	}

	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}

	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal sessions: %w", err)
	}

	if err := os.WriteFile(s.path, data, 0o600); err != nil {
		return fmt.Errorf("write sessions file: %w", err)
	}

	slog.Debug("Session saved to disk", "host", host, "path", s.path)
	return nil
}

func (s *SessionStore) readAll() map[string]sessionEntry {
	data, err := os.ReadFile(s.path)
	if err != nil {
		return make(map[string]sessionEntry)
	}
	var entries map[string]sessionEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		slog.Warn("Corrupt sessions file, starting fresh", "path", s.path, "error", err)
		return make(map[string]sessionEntry)
	}
	return entries
}

func hostFromURL(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	return u.Host
}

func configDir() string {
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "mcp-grafana")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return filepath.Join(home, ".config", "mcp-grafana")
}
