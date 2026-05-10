package auth

import (
	"context"
	"errors"
	"testing"
	"time"
)

func newTestSession(t *testing.T, id string) Session {
	t.Helper()
	return Session{
		TokenHash:        HashToken("tok-" + id),
		RefreshHash:      HashToken("ref-" + id),
		ExpiresAt:        time.Now().Add(time.Hour),
		RefreshExpiresAt: time.Now().Add(30 * 24 * time.Hour),
		Identity:         Identity{Mode: ModeOAuthOIDC, ID: id},
		UpstreamCredsCT:  []byte("ct-" + id),
		CreatedAt:        time.Now(),
		UpdatedAt:        time.Now(),
	}
}

func TestMemoryStore_SessionRoundTrip(t *testing.T) {
	s := NewMemoryStore()
	ctx := context.Background()
	sess := newTestSession(t, "alice")

	if err := s.PutSession(ctx, sess); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetSessionByTokenHash(ctx, sess.TokenHash)
	if err != nil {
		t.Fatal(err)
	}
	if got.Identity != sess.Identity {
		t.Errorf("identity mismatch: %+v vs %+v", got.Identity, sess.Identity)
	}
	got, err = s.GetSessionByRefreshHash(ctx, sess.RefreshHash)
	if err != nil {
		t.Fatal(err)
	}
	if got.TokenHash != sess.TokenHash {
		t.Errorf("refresh-lookup returned wrong session")
	}
	got, err = s.GetSessionByIdentity(ctx, sess.Identity)
	if err != nil {
		t.Fatal(err)
	}
	if got.TokenHash != sess.TokenHash {
		t.Errorf("identity-lookup returned wrong session")
	}
}

func TestMemoryStore_DeleteSession(t *testing.T) {
	s := NewMemoryStore()
	ctx := context.Background()
	sess := newTestSession(t, "alice")
	_ = s.PutSession(ctx, sess)
	if err := s.DeleteSession(ctx, sess.TokenHash); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetSessionByTokenHash(ctx, sess.TokenHash); !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
	if _, err := s.GetSessionByIdentity(ctx, sess.Identity); !errors.Is(err, ErrNotFound) {
		t.Errorf("identity index should be cleared too")
	}
}

func TestMemoryStore_AuthCodeOneShot(t *testing.T) {
	s := NewMemoryStore()
	ctx := context.Background()
	codeHash := HashToken("ac-1")
	c := AuthCode{
		Code:                codeHash,
		ClientID:            "cid",
		RedirectURI:         "http://localhost/cb",
		CodeChallenge:       "challenge",
		CodeChallengeMethod: "S256",
		Identity:            Identity{Mode: ModeOAuthOIDC, ID: "alice"},
		ExpiresAt:           time.Now().Add(5 * time.Minute),
	}
	if err := s.PutAuthCode(ctx, c); err != nil {
		t.Fatal(err)
	}
	got, err := s.ConsumeAuthCode(ctx, codeHash)
	if err != nil {
		t.Fatal(err)
	}
	if got.ClientID != "cid" {
		t.Errorf("got %+v", got)
	}
	if _, err := s.ConsumeAuthCode(ctx, codeHash); !errors.Is(err, ErrNotFound) {
		t.Errorf("second consume must fail with ErrNotFound, got %v", err)
	}
}

func TestMemoryStore_AuthCodeExpired(t *testing.T) {
	s := NewMemoryStore()
	ctx := context.Background()
	c := AuthCode{
		Code:      HashToken("expired"),
		ExpiresAt: time.Now().Add(-time.Second),
	}
	_ = s.PutAuthCode(ctx, c)
	if _, err := s.ConsumeAuthCode(ctx, c.Code); !errors.Is(err, ErrNotFound) {
		t.Errorf("expired code should be ErrNotFound, got %v", err)
	}
}

func TestMemoryStore_ClientCRUD(t *testing.T) {
	s := NewMemoryStore()
	ctx := context.Background()
	c := DCRClient{ClientID: "cid-1", ClientName: "demo"}
	_ = s.PutClient(ctx, c)
	got, err := s.GetClient(ctx, "cid-1")
	if err != nil || got.ClientName != "demo" {
		t.Errorf("got=%+v err=%v", got, err)
	}
	if _, err := s.GetClient(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}
