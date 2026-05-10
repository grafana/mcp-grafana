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

	// flushMu serializes file I/O (snapshot + encrypt + write + fsync +
	// rename) across concurrent mutating methods. It does NOT guard
	// reads or in-memory mutation — the underlying MemoryStore has its
	// own RWMutex for those, so a slow flush doesn't block session
	// lookups on the auth middleware's hot path. flush() snapshots the
	// memory state under f.mem.mu.RLock briefly, then releases that
	// lock before doing the disk I/O.
	flushMu sync.Mutex
	path    string
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

// All mutating methods delegate to the memory store, then take flushMu
// to serialize the file write. Reads delegate directly to MemoryStore
// (which has its own RWMutex) so a slow flush doesn't block session
// lookups on the auth middleware's hot path.
//
// Concurrency note: a mutation is "committed" (visible to readers) the
// moment MemoryStore's write returns. The on-disk file always reflects
// some past valid state — when two flushes race, the second-to-finish
// captures both mutations, so eventual on-disk consistency is preserved.

func (f *FileStore) PutSession(ctx context.Context, s Session) (string, error) {
	replaced, err := f.mem.PutSession(ctx, s)
	if err != nil {
		return "", err
	}
	f.flushMu.Lock()
	defer f.flushMu.Unlock()
	return replaced, f.flush()
}

func (f *FileStore) GetSessionByTokenHash(ctx context.Context, h string) (Session, error) {
	return f.mem.GetSessionByTokenHash(ctx, h)
}
func (f *FileStore) GetSessionByRefreshHash(ctx context.Context, h string) (Session, error) {
	return f.mem.GetSessionByRefreshHash(ctx, h)
}
func (f *FileStore) GetSessionByIdentity(ctx context.Context, id Identity) (Session, error) {
	return f.mem.GetSessionByIdentity(ctx, id)
}

func (f *FileStore) DeleteSession(ctx context.Context, h string) (bool, error) {
	deleted, err := f.mem.DeleteSession(ctx, h)
	if err != nil {
		return false, err
	}
	if !deleted {
		// Nothing changed in-memory, no need to rewrite the file.
		return false, nil
	}
	f.flushMu.Lock()
	defer f.flushMu.Unlock()
	if err := f.flush(); err != nil {
		return true, err
	}
	return true, nil
}

func (f *FileStore) PutClient(ctx context.Context, c DCRClient) error {
	if err := f.mem.PutClient(ctx, c); err != nil {
		return err
	}
	f.flushMu.Lock()
	defer f.flushMu.Unlock()
	return f.flush()
}

func (f *FileStore) GetClient(ctx context.Context, id string) (DCRClient, error) {
	return f.mem.GetClient(ctx, id)
}

func (f *FileStore) PutAuthCode(ctx context.Context, c AuthCode) error {
	if err := f.mem.PutAuthCode(ctx, c); err != nil {
		return err
	}
	f.flushMu.Lock()
	defer f.flushMu.Unlock()
	return f.flush()
}

func (f *FileStore) PeekAuthCode(ctx context.Context, h string) (AuthCode, error) {
	// peekAuthCodePruning signals whether MemoryStore actually deleted
	// an expired entry. Flush only on a real prune under flushMu —
	// a never-existed or non-expired code shouldn't burden the /token
	// hot path with a full file rewrite, and a concurrent flush must
	// see the deletion. The flush is best-effort: errors.Is(err,
	// ErrNotFound) downstream must keep working, so we don't mask the
	// inner error with a disk error.
	c, err, pruned := f.mem.peekAuthCodePruning(ctx, h)
	if !pruned {
		return c, err
	}
	// Best-effort flush: callers differentiate ErrNotFound from I/O
	// failures, so don't mask the semantic error with the flush error.
	// The disk eviction is opportunistic — the next mutation flush
	// will capture the in-memory deletion regardless.
	f.flushMu.Lock()
	_ = f.flush()
	f.flushMu.Unlock()
	return c, err
}

func (f *FileStore) ConsumeAuthCode(ctx context.Context, h string) (AuthCode, error) {
	c, err := f.mem.ConsumeAuthCode(ctx, h)
	if err != nil {
		return c, err
	}
	f.flushMu.Lock()
	defer f.flushMu.Unlock()
	return c, f.flush()
}
