package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// FileStore persists Store contents to a single encrypted file. Each mutation
// rewrites the file atomically (write-temp + rename). Single-replica only.
type FileStore struct {
	mem *MemoryStore
	enc *Encryptor

	mu   sync.RWMutex
	path string
}

type filePayload struct {
	Sessions  []Session   `json:"sessions"`
	Clients   []DCRClient `json:"clients"`
	AuthCodes []AuthCode  `json:"auth_codes"`
}

// NewFileStore opens or creates the state file at path. A missing file
// produces an empty store. A corrupt or wrong-key file returns an error.
func NewFileStore(path string, enc *Encryptor) (*FileStore, error) {
	if enc == nil {
		return nil, errors.New("file store requires an Encryptor")
	}
	fs := &FileStore{
		mem:  NewMemoryStore(),
		enc:  enc,
		path: path,
	}
	if err := fs.load(); err != nil {
		return nil, err
	}
	return fs, nil
}

func (f *FileStore) load() error {
	raw, err := os.ReadFile(f.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read state file: %w", err)
	}
	pt, err := f.enc.Open(raw)
	if err != nil {
		return fmt.Errorf("decrypt state file (wrong key or corrupt): %w", err)
	}
	var p filePayload
	if err := json.Unmarshal(pt, &p); err != nil {
		return fmt.Errorf("parse state file: %w", err)
	}

	// Re-seal inner ciphertexts under the current primary key. This makes
	// key rotation work end-to-end: when an admin runs once with
	// --token-encryption-key-previous=OLD --token-encryption-key=NEW, this
	// loop migrates every stored ciphertext from OLD to NEW. After the next
	// flush, OLD can be removed without losing any sessions.
	for i := range p.Sessions {
		if len(p.Sessions[i].UpstreamCredsCT) == 0 {
			continue
		}
		innerPt, err := f.enc.Open(p.Sessions[i].UpstreamCredsCT)
		if err != nil {
			return fmt.Errorf("rewrap session %s: %w", p.Sessions[i].Identity.String(), err)
		}
		innerCt, err := f.enc.Seal(innerPt)
		if err != nil {
			return fmt.Errorf("reseal session %s: %w", p.Sessions[i].Identity.String(), err)
		}
		p.Sessions[i].UpstreamCredsCT = innerCt
	}
	for i := range p.AuthCodes {
		if len(p.AuthCodes[i].UpstreamCredsCT) == 0 {
			continue
		}
		innerPt, err := f.enc.Open(p.AuthCodes[i].UpstreamCredsCT)
		if err != nil {
			return fmt.Errorf("rewrap auth code: %w", err)
		}
		innerCt, err := f.enc.Seal(innerPt)
		if err != nil {
			return fmt.Errorf("reseal auth code: %w", err)
		}
		p.AuthCodes[i].UpstreamCredsCT = innerCt
	}

	ctx := context.Background()
	for _, s := range p.Sessions {
		if _, err := f.mem.PutSession(ctx, s); err != nil {
			return fmt.Errorf("load session: %w", err)
		}
	}
	for _, c := range p.Clients {
		if err := f.mem.PutClient(ctx, c); err != nil {
			return fmt.Errorf("load client: %w", err)
		}
	}
	for _, ac := range p.AuthCodes {
		if err := f.mem.PutAuthCode(ctx, ac); err != nil {
			return fmt.Errorf("load auth code: %w", err)
		}
	}

	// Persist rewrapped ciphertexts immediately so the previous key can be
	// safely removed after a rotation cycle completes.
	if err := f.flush(); err != nil {
		return fmt.Errorf("post-rewrap flush: %w", err)
	}
	return nil
}

func (f *FileStore) flush() error {
	sessions, clients, codes := f.mem.Snapshot()
	p := filePayload{Sessions: sessions, Clients: clients, AuthCodes: codes}

	pt, err := json.Marshal(p)
	if err != nil {
		return err
	}
	ct, err := f.enc.Seal(pt)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(f.path), filepath.Base(f.path)+".*")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(ct); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return err
	}
	// Sync to stable storage before the rename so a crash between Close
	// and Rename can't leave the renamed file with zero-length / partial
	// contents. Without this the OS may have buffered the write and the
	// rename atomically swaps in a corrupt blob — every user has to
	// re-authenticate and re-bootstrap.
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	if err := os.Rename(tmp.Name(), f.path); err != nil {
		// Cross-device or permission errors leave the temp file behind.
		// Drop it so successive failures don't accumulate orphans.
		_ = os.Remove(tmp.Name())
		return err
	}
	return nil
}

func (f *FileStore) Close() error { return nil }

// All mutating methods delegate to the memory store and then flush.

func (f *FileStore) PutSession(ctx context.Context, s Session) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	replaced, err := f.mem.PutSession(ctx, s)
	if err != nil {
		return "", err
	}
	return replaced, f.flush()
}

func (f *FileStore) GetSessionByTokenHash(ctx context.Context, h string) (Session, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.mem.GetSessionByTokenHash(ctx, h)
}
func (f *FileStore) GetSessionByRefreshHash(ctx context.Context, h string) (Session, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.mem.GetSessionByRefreshHash(ctx, h)
}
func (f *FileStore) GetSessionByIdentity(ctx context.Context, id Identity) (Session, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.mem.GetSessionByIdentity(ctx, id)
}

func (f *FileStore) DeleteSession(ctx context.Context, h string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	deleted, err := f.mem.DeleteSession(ctx, h)
	if err != nil {
		return false, err
	}
	if !deleted {
		// Nothing changed in-memory, no need to rewrite the file.
		return false, nil
	}
	if err := f.flush(); err != nil {
		return true, err
	}
	return true, nil
}

func (f *FileStore) PutClient(ctx context.Context, c DCRClient) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.mem.PutClient(ctx, c); err != nil {
		return err
	}
	return f.flush()
}

func (f *FileStore) GetClient(ctx context.Context, id string) (DCRClient, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.mem.GetClient(ctx, id)
}

func (f *FileStore) PutAuthCode(ctx context.Context, c AuthCode) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.mem.PutAuthCode(ctx, c); err != nil {
		return err
	}
	return f.flush()
}

func (f *FileStore) PeekAuthCode(ctx context.Context, h string) (AuthCode, error) {
	// peekAuthCodePruning signals whether MemoryStore actually deleted
	// an expired entry. Flush only on a real prune — a never-existed
	// or non-expired code shouldn't burden the /token hot path with a
	// full file rewrite. And don't mask the inner ErrNotFound with a
	// flush error: callers may differentiate "not found" from "I/O
	// failure" via errors.Is, so the flush is best-effort.
	c, err, pruned := f.mem.peekAuthCodePruning(ctx, h)
	if !pruned {
		return c, err
	}
	f.mu.Lock()
	_ = f.flush()
	f.mu.Unlock()
	return c, err
}

func (f *FileStore) ConsumeAuthCode(ctx context.Context, h string) (AuthCode, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c, err := f.mem.ConsumeAuthCode(ctx, h)
	if err != nil {
		return c, err
	}
	return c, f.flush()
}
