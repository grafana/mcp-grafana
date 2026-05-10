package auth

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestFileStore_PersistsAcrossInstances(t *testing.T) {
	dir := t.TempDir()
	enc := mustEnc(t, mustKey(t), nil)

	s1, err := NewFileStore(filepath.Join(dir, "auth.state"), enc)
	if err != nil {
		t.Fatal(err)
	}
	sess := newTestSession(t, "alice")
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
	_ = s1.PutSession(context.Background(), newTestSession(t, "x"))
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
		if err := s.PutSession(context.Background(), newTestSession(t, fmt.Sprintf("v%d", i))); err != nil {
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
