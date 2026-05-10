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

func (m *MemoryStore) PutSession(_ context.Context, s Session) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Enforce the documented "one session per identity" invariant: if a
	// session already exists for this identity, drop its secondary-index
	// entries before installing the replacement. Otherwise the previous
	// session's access and refresh tokens would still resolve via
	// GetSessionByTokenHash / GetSessionByRefreshHash, while
	// GetSessionByIdentity points at the new session.
	var replacedTokenHash string
	if oldHash, ok := m.sessByIdentity[s.Identity.String()]; ok && oldHash != s.TokenHash {
		replacedTokenHash = oldHash
		if old, ok := m.sessByToken[oldHash]; ok {
			delete(m.sessByToken, oldHash)
			if old.RefreshHash != "" {
				delete(m.sessByRefresh, old.RefreshHash)
			}
		}
	}
	// Same-token overwrite (e.g. doRefreshUpstream rotating the upstream
	// credential under the same MCP TokenHash): if the prior session under
	// this TokenHash had a different RefreshHash, drop the stale
	// refresh-hash → token-hash mapping so the old refresh token can no
	// longer resolve via GetSessionByRefreshHash.
	if prev, ok := m.sessByToken[s.TokenHash]; ok && prev.RefreshHash != "" && prev.RefreshHash != s.RefreshHash {
		delete(m.sessByRefresh, prev.RefreshHash)
	}

	cp := s
	m.sessByToken[s.TokenHash] = &cp
	if s.RefreshHash != "" {
		m.sessByRefresh[s.RefreshHash] = s.TokenHash
	}
	m.sessByIdentity[s.Identity.String()] = s.TokenHash
	return replacedTokenHash, nil
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

func (m *MemoryStore) DeleteSession(_ context.Context, tokenHash string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessByToken[tokenHash]
	if !ok {
		return false, nil
	}
	delete(m.sessByToken, tokenHash)
	if s.RefreshHash != "" {
		delete(m.sessByRefresh, s.RefreshHash)
	}
	delete(m.sessByIdentity, s.Identity.String())
	return true, nil
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

// Snapshot returns deep copies of every session, client, and auth code held
// in memory. FileStore.flush uses it to capture a coherent serialization
// snapshot under the memory store's own lock, keeping FileStore from
// reaching into MemoryStore's internal fields.
//
// Session and AuthCode carry []byte ciphertext fields whose backing arrays
// must NOT be shared with the live store — a future mutation that
// reassigns the slice header is safe, but in-place mutation (or a future
// optimization that overwrites the backing array) would corrupt an
// in-flight flush. cloneBytes copies the underlying bytes so the caller
// owns its own copy of every ciphertext.
func (m *MemoryStore) Snapshot() (sessions []Session, clients []DCRClient, codes []AuthCode) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	sessions = make([]Session, 0, len(m.sessByToken))
	for _, s := range m.sessByToken {
		sc := *s
		sc.UpstreamCredsCT = cloneBytes(sc.UpstreamCredsCT)
		sc.UpstreamRefreshCT = cloneBytes(sc.UpstreamRefreshCT)
		sessions = append(sessions, sc)
	}
	clients = make([]DCRClient, 0, len(m.clients))
	for _, c := range m.clients {
		clients = append(clients, c)
	}
	codes = make([]AuthCode, 0, len(m.codes))
	for _, ac := range m.codes {
		ac.UpstreamCredsCT = cloneBytes(ac.UpstreamCredsCT)
		ac.UpstreamRefreshCT = cloneBytes(ac.UpstreamRefreshCT)
		codes = append(codes, ac)
	}
	return sessions, clients, codes
}

func cloneBytes(b []byte) []byte {
	if b == nil {
		return nil
	}
	out := make([]byte, len(b))
	copy(out, b)
	return out
}

func (m *MemoryStore) PutAuthCode(_ context.Context, c AuthCode) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.codes[c.Code] = c
	return nil
}

func (m *MemoryStore) PeekAuthCode(ctx context.Context, codeHash string) (AuthCode, error) {
	c, err, _ := m.peekAuthCodePruning(ctx, codeHash)
	return c, err
}

// peekAuthCodePruning is the FileStore-friendly variant of PeekAuthCode:
// it returns (code, err, pruned) where pruned reports whether an expired
// entry was deleted under the lock. FileStore consumes the pruned flag
// to decide whether to flush — a never-existed code returns ErrNotFound
// with pruned=false, so the disk hot path on the /token endpoint isn't
// burdened by a full rewrite for unknown or replayed codes.
//
// The write lock is unconditional here: pruning is the whole point.
// Without this method (or the equivalent), handleAuthCodeGrant's
// peek-then-consume flow leaves every expired code in the map forever —
// the early return on Peek means ConsumeAuthCode (which also deletes)
// is never reached for expired entries, so the codes map (and any
// FileStore-persisted version) accumulates entries from abandoned auth
// flows.
func (m *MemoryStore) peekAuthCodePruning(_ context.Context, codeHash string) (AuthCode, error, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.codes[codeHash]
	if !ok {
		return AuthCode{}, ErrNotFound, false
	}
	if !c.ExpiresAt.IsZero() && time.Now().After(c.ExpiresAt) {
		delete(m.codes, codeHash)
		return AuthCode{}, ErrNotFound, true
	}
	return c, nil, false
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
