package auth

import (
	"crypto/sha256"
	"encoding/base64"
	"testing"
)

func TestVerifyPKCE_S256_OK(t *testing.T) {
	verifier := "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	if err := VerifyPKCE("S256", challenge, verifier); err != nil {
		t.Errorf("verify: %v", err)
	}
}

func TestVerifyPKCE_S256_Mismatch(t *testing.T) {
	if err := VerifyPKCE("S256", "wrong", "verifier"); err == nil {
		t.Errorf("expected mismatch error")
	}
}

func TestVerifyPKCE_RejectsPlain(t *testing.T) {
	if err := VerifyPKCE("plain", "x", "x"); err == nil {
		t.Errorf("plain method must be rejected")
	}
	if err := VerifyPKCE("", "x", "x"); err == nil {
		t.Errorf("empty method must be rejected")
	}
}

func TestNewAuthCode_HasExpiry(t *testing.T) {
	plain, hashed := NewAuthCode()
	if plain == "" || hashed == "" || plain == hashed {
		t.Errorf("plain=%q hashed=%q", plain, hashed)
	}
	if HashToken(plain) != hashed {
		t.Errorf("hash mismatch")
	}
}
