package auth

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// sealedTestSession returns a test session whose UpstreamCredsCT is properly
// sealed with enc so that FileStore.load()'s rewrap step can open it.
func sealedTestSession(t *testing.T, id string, enc *Encryptor) Session {
	t.Helper()
	s := newTestSession(t, id)
	ct, err := enc.Seal([]byte("creds-" + id))
	if err != nil {
		t.Fatalf("seal test session creds: %v", err)
	}
	s.UpstreamCredsCT = ct
	return s
}

func TestFileStore_PersistsAcrossInstances(t *testing.T) {
	dir := t.TempDir()
	enc := mustEnc(t, mustKey(t), nil)

	s1, err := NewFileStore(filepath.Join(dir, "auth.state"), enc)
	if err != nil {
		t.Fatal(err)
	}
	sess := sealedTestSession(t, "alice", enc)
	if err := s1.PutSession(context.Background(), sess); err != nil {
		t.Fatal(err)
	}
	if err := s1.Close(); err != nil {
		t.Fatal(err)
	}

	s2, err := NewFileStore(filepath.Join(dir, "auth.state"), enc)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	got, err := s2.GetSessionByTokenHash(context.Background(), sess.TokenHash)
	if err != nil {
		t.Fatal(err)
	}
	if got.Identity != sess.Identity {
		t.Errorf("identity not persisted")
	}
}

func TestFileStore_WrongKeyFails(t *testing.T) {
	dir := t.TempDir()
	good := mustEnc(t, mustKey(t), nil)
	bad := mustEnc(t, mustKey(t), nil)

	s1, _ := NewFileStore(filepath.Join(dir, "auth.state"), good)
	_ = s1.PutSession(context.Background(), sealedTestSession(t, "x", good))
	_ = s1.Close()

	if _, err := NewFileStore(filepath.Join(dir, "auth.state"), bad); err == nil {
		t.Errorf("expected open with wrong key to fail")
	}
}

func TestFileStore_MissingFileIsEmpty(t *testing.T) {
	dir := t.TempDir()
	enc := mustEnc(t, mustKey(t), nil)
	s, err := NewFileStore(filepath.Join(dir, "does-not-exist.state"), enc)
	if err != nil {
		t.Fatalf("missing file should be a clean empty store: %v", err)
	}
	defer s.Close()
	if _, err := s.GetSessionByTokenHash(context.Background(), "x"); !errors.Is(err, ErrNotFound) {
		t.Errorf("got %v", err)
	}
}

func TestFileStore_NoLingeringTempFiles(t *testing.T) {
	dir := t.TempDir()
	enc := mustEnc(t, mustKey(t), nil)
	path := filepath.Join(dir, "auth.state")
	s, err := NewFileStore(path, enc)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	for i := 0; i < 5; i++ {
		if err := s.PutSession(context.Background(), sealedTestSession(t, fmt.Sprintf("v%d", i), enc)); err != nil {
			t.Fatal(err)
		}
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() == "auth.state" {
			continue
		}
		t.Errorf("unexpected leftover file in state dir: %s", e.Name())
	}
}

func TestFileStore_KeyRotationMigratesInnerCiphertexts(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "auth.state")

	keyA := mustKey(t)
	keyB := mustKey(t)

	// Step 1: write a session with keyA only.
	encA := mustEnc(t, keyA, nil)
	s1, err := NewFileStore(path, encA)
	if err != nil {
		t.Fatal(err)
	}
	sess := newTestSession(t, "alice")
	// Manually re-seal the test session's UpstreamCredsCT under keyA so
	// the test reflects production: the session was created at the time
	// keyA was primary.
	sess.UpstreamCredsCT, err = encA.Seal([]byte("the-sa-token"))
	if err != nil {
		t.Fatal(err)
	}
	if err := s1.PutSession(context.Background(), sess); err != nil {
		t.Fatal(err)
	}
	_ = s1.Close()

	// Step 2: open with keyB primary and keyA as previous. Load
	// migrates the inner ciphertext.
	encAB := mustEnc(t, keyB, keyA)
	s2, err := NewFileStore(path, encAB)
	if err != nil {
		t.Fatal(err)
	}
	_ = s2.Close()

	// Step 3: open with keyB only. The session should still resolve.
	encB := mustEnc(t, keyB, nil)
	s3, err := NewFileStore(path, encB)
	if err != nil {
		t.Fatalf("after rotation drain, file should still open with new key only: %v", err)
	}
	defer s3.Close()
	got, err := s3.GetSessionByTokenHash(context.Background(), sess.TokenHash)
	if err != nil {
		t.Fatal(err)
	}
	pt, err := encB.Open(got.UpstreamCredsCT)
	if err != nil {
		t.Fatalf("inner ciphertext should decrypt with keyB only after migration: %v", err)
	}
	if string(pt) != "the-sa-token" {
		t.Errorf("decrypted=%q", pt)
	}
}
