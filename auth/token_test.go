package auth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func newTokenServer(t *testing.T) *Server {
	t.Helper()
	return &Server{
		PublicURL:       "https://mcp.example.com",
		Store:           NewMemoryStore(),
		Encryptor:       mustEnc(t, mustKey(t), nil),
		AccessTokenTTL:  time.Hour,
		RefreshTokenTTL: 24 * time.Hour,
	}
}

// TestToken_NilMetricsDoesNotPanic confirms that constructing a Server
// without a Metrics instance (e.g., in tests or library usage that doesn't
// call auth.New) doesn't panic when the token-grant path emits its
// SessionCreated call. The Metrics methods have nil-receiver guards; this
// test locks that invariant in.
func TestToken_NilMetricsDoesNotPanic(t *testing.T) {
	srv := &Server{
		PublicURL:       "https://mcp.example.com",
		Store:           NewMemoryStore(),
		Encryptor:       mustEnc(t, mustKey(t), nil),
		AccessTokenTTL:  time.Hour,
		RefreshTokenTTL: 24 * time.Hour,
		// Metrics intentionally nil.
	}
	ctx := context.Background()

	verifier := "the-code-verifier"
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	plainCode, hashedCode := NewAuthCode()
	credCT, _ := srv.Encryptor.Seal([]byte("sa-token"))
	_ = srv.Store.PutAuthCode(ctx, AuthCode{
		Code:                hashedCode,
		ClientID:            "cid",
		RedirectURI:         "http://localhost:1/cb",
		CodeChallenge:       challenge,
		CodeChallengeMethod: "S256",
		Identity:            Identity{Mode: ModeOAuthOIDC, ID: "alice"},
		UpstreamCredsCT:     credCT,
		ExpiresAt:           time.Now().Add(5 * time.Minute),
	})

	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", plainCode)
	form.Set("redirect_uri", "http://localhost:1/cb")
	form.Set("client_id", "cid")
	form.Set("code_verifier", verifier)
	r := httptest.NewRequest(http.MethodPost, "/token", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	// Must not panic.
	srv.TokenHandler().ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Errorf("status=%d, expected 200 even with nil Metrics", w.Code)
	}
}

func TestToken_RedeemAuthCode(t *testing.T) {
	srv := newTokenServer(t)
	ctx := context.Background()

	// Pre-stage: an auth code waiting for redemption.
	verifier := "the-code-verifier"
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])

	plainCode, hashedCode := NewAuthCode()
	credCT, _ := srv.Encryptor.Seal([]byte("sa-token"))
	_ = srv.Store.PutAuthCode(ctx, AuthCode{
		Code:                hashedCode,
		ClientID:            "cid",
		RedirectURI:         "http://localhost:1/cb",
		CodeChallenge:       challenge,
		CodeChallengeMethod: "S256",
		Identity:            Identity{Mode: ModeOAuthOIDC, ID: "alice"},
		UpstreamCredsCT:     credCT,
		ExpiresAt:           time.Now().Add(5 * time.Minute),
	})

	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", plainCode)
	form.Set("redirect_uri", "http://localhost:1/cb")
	form.Set("client_id", "cid")
	form.Set("code_verifier", verifier)
	r := httptest.NewRequest(http.MethodPost, "/token", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	srv.TokenHandler().ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body)
	}
	var resp map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["token_type"] != "Bearer" {
		t.Errorf("token_type=%v", resp["token_type"])
	}
	at, _ := resp["access_token"].(string)
	rt, _ := resp["refresh_token"].(string)
	if at == "" || rt == "" || at == rt {
		t.Errorf("at=%q rt=%q", at, rt)
	}

	// Session must exist now keyed by hashed access token.
	sess, err := srv.Store.GetSessionByTokenHash(ctx, HashToken(at))
	if err != nil {
		t.Fatal(err)
	}
	if sess.Identity.ID != "alice" {
		t.Errorf("identity=%+v", sess.Identity)
	}
}

func TestToken_PKCEMismatch(t *testing.T) {
	srv := newTokenServer(t)
	ctx := context.Background()

	plainCode, hashedCode := NewAuthCode()
	_ = srv.Store.PutAuthCode(ctx, AuthCode{
		Code:                hashedCode,
		ClientID:            "cid",
		RedirectURI:         "http://localhost:1/cb",
		CodeChallenge:       "actual-challenge",
		CodeChallengeMethod: "S256",
		ExpiresAt:           time.Now().Add(5 * time.Minute),
	})
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", plainCode)
	form.Set("redirect_uri", "http://localhost:1/cb")
	form.Set("client_id", "cid")
	form.Set("code_verifier", "wrong-verifier")
	r := httptest.NewRequest(http.MethodPost, "/token", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	srv.TokenHandler().ServeHTTP(w, r)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status=%d", w.Code)
	}
}

func TestToken_RefreshRotates(t *testing.T) {
	srv := newTokenServer(t)
	ctx := context.Background()

	// Pre-existing session.
	plainAT, hashAT := NewAuthCode()
	plainRT, hashRT := NewAuthCode()
	credCT, _ := srv.Encryptor.Seal([]byte("sa-token"))
	_ = srv.Store.PutSession(ctx, Session{
		TokenHash:        hashAT,
		RefreshHash:      hashRT,
		Identity:         Identity{Mode: ModeOAuthOIDC, ID: "alice"},
		UpstreamCredsCT:  credCT,
		ExpiresAt:        time.Now().Add(5 * time.Minute),
		RefreshExpiresAt: time.Now().Add(time.Hour),
		CreatedAt:        time.Now(),
		UpdatedAt:        time.Now(),
	})
	_ = plainAT

	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", plainRT)
	form.Set("client_id", "cid")
	r := httptest.NewRequest(http.MethodPost, "/token", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	srv.TokenHandler().ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body)
	}
	var resp map[string]string
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["refresh_token"] == plainRT {
		t.Errorf("refresh token must rotate")
	}

	// Old refresh must no longer work.
	form.Set("refresh_token", plainRT)
	r2 := httptest.NewRequest(http.MethodPost, "/token", strings.NewReader(form.Encode()))
	r2.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w2 := httptest.NewRecorder()
	srv.TokenHandler().ServeHTTP(w2, r2)
	if w2.Code != http.StatusBadRequest {
		t.Errorf("expected old refresh to be rejected, got %d", w2.Code)
	}
}

// TestToken_RefreshRejectsCrossClient confirms that a refresh token issued
// to client A cannot be exchanged by client B (RFC 6749 §10.4). A
// malicious sibling client that obtains the refresh token must not be
// able to mint fresh access tokens against it.
func TestToken_RefreshRejectsCrossClient(t *testing.T) {
	srv := newTokenServer(t)
	ctx := context.Background()

	plainRT, hashRT := NewAuthCode()
	credCT, _ := srv.Encryptor.Seal([]byte("sa-token"))
	_ = srv.Store.PutSession(ctx, Session{
		TokenHash:        HashToken("at"),
		RefreshHash:      hashRT,
		ClientID:         "client-a",
		Identity:         Identity{Mode: ModeOAuthOIDC, ID: "alice"},
		UpstreamCredsCT:  credCT,
		ExpiresAt:        time.Now().Add(5 * time.Minute),
		RefreshExpiresAt: time.Now().Add(time.Hour),
	})

	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", plainRT)
	form.Set("client_id", "client-b") // wrong client
	r := httptest.NewRequest(http.MethodPost, "/token", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	srv.TokenHandler().ServeHTTP(w, r)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected cross-client refresh to be rejected, got %d body=%s", w.Code, w.Body)
	}
	if !strings.Contains(w.Body.String(), "invalid_grant") {
		t.Errorf("expected invalid_grant in body, got %s", w.Body)
	}
}
