package auth

import (
	"context"
	"sync"
	"time"
)

// MemoryStore is an in-memory Store. Suitable for single-replica deployments
// and tests; data is lost on process restart.
type MemoryStore struct {
	mu sync.RWMutex

	sessByToken    map[string]*Session
	sessByRefresh  map[string]string // refreshHash -> tokenHash
	sessByIdentity map[string]string // identity.String() -> tokenHash

	clients map[string]DCRClient

	codes map[string]AuthCode
}

// NewMemoryStore returns an empty MemoryStore.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		sessByToken:    make(map[string]*Session),
		sessByRefresh:  make(map[string]string),
		sessByIdentity: make(map[string]string),
		clients:        make(map[string]DCRClient),
		codes:          make(map[string]AuthCode),
	}
}

func (m *MemoryStore) PutSession(_ context.Context, s Session) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := s
	m.sessByToken[s.TokenHash] = &cp
	if s.RefreshHash != "" {
		m.sessByRefresh[s.RefreshHash] = s.TokenHash
	}
	m.sessByIdentity[s.Identity.String()] = s.TokenHash
	return nil
}

func (m *MemoryStore) GetSessionByTokenHash(_ context.Context, h string) (Session, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	s, ok := m.sessByToken[h]
	if !ok {
		return Session{}, ErrNotFound
	}
	return *s, nil
}

func (m *MemoryStore) GetSessionByRefreshHash(_ context.Context, h string) (Session, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	tok, ok := m.sessByRefresh[h]
	if !ok {
		return Session{}, ErrNotFound
	}
	s, ok := m.sessByToken[tok]
	if !ok {
		return Session{}, ErrNotFound
	}
	return *s, nil
}

func (m *MemoryStore) GetSessionByIdentity(_ context.Context, id Identity) (Session, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	tok, ok := m.sessByIdentity[id.String()]
	if !ok {
		return Session{}, ErrNotFound
	}
	s, ok := m.sessByToken[tok]
	if !ok {
		return Session{}, ErrNotFound
	}
	return *s, nil
}

func (m *MemoryStore) DeleteSession(_ context.Context, tokenHash string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessByToken[tokenHash]
	if !ok {
		return nil
	}
	delete(m.sessByToken, tokenHash)
	if s.RefreshHash != "" {
		delete(m.sessByRefresh, s.RefreshHash)
	}
	delete(m.sessByIdentity, s.Identity.String())
	return nil
}

func (m *MemoryStore) PutClient(_ context.Context, c DCRClient) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.clients[c.ClientID] = c
	return nil
}

func (m *MemoryStore) GetClient(_ context.Context, id string) (DCRClient, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	c, ok := m.clients[id]
	if !ok {
		return DCRClient{}, ErrNotFound
	}
	return c, nil
}

func (m *MemoryStore) PutAuthCode(_ context.Context, c AuthCode) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.codes[c.Code] = c
	return nil
}

func (m *MemoryStore) ConsumeAuthCode(_ context.Context, codeHash string) (AuthCode, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.codes[codeHash]
	if !ok {
		return AuthCode{}, ErrNotFound
	}
	delete(m.codes, codeHash)
	if !c.ExpiresAt.IsZero() && time.Now().After(c.ExpiresAt) {
		return AuthCode{}, ErrNotFound
	}
	return c, nil
}
