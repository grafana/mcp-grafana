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

	mu   sync.Mutex
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
	ctx := context.Background()
	for _, s := range p.Sessions {
		_ = f.mem.PutSession(ctx, s)
	}
	for _, c := range p.Clients {
		_ = f.mem.PutClient(ctx, c)
	}
	for _, ac := range p.AuthCodes {
		_ = f.mem.PutAuthCode(ctx, ac)
	}
	return nil
}

func (f *FileStore) flush() error {
	f.mem.mu.RLock()
	p := filePayload{}
	for _, s := range f.mem.sessByToken {
		p.Sessions = append(p.Sessions, *s)
	}
	for _, c := range f.mem.clients {
		p.Clients = append(p.Clients, c)
	}
	for _, ac := range f.mem.codes {
		p.AuthCodes = append(p.AuthCodes, ac)
	}
	f.mem.mu.RUnlock()

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
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), f.path)
}

func (f *FileStore) Close() error { return nil }

// All mutating methods delegate to the memory store and then flush.

func (f *FileStore) PutSession(ctx context.Context, s Session) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.mem.PutSession(ctx, s); err != nil {
		return err
	}
	return f.flush()
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

func (f *FileStore) DeleteSession(ctx context.Context, h string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.mem.DeleteSession(ctx, h); err != nil {
		return err
	}
	return f.flush()
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

func (f *FileStore) ConsumeAuthCode(ctx context.Context, h string) (AuthCode, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c, err := f.mem.ConsumeAuthCode(ctx, h)
	if err != nil {
		return c, err
	}
	return c, f.flush()
}
