package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
)

// VerifyPKCE checks that sha256(verifier) base64url-no-pad equals challenge.
// Only the "S256" method is supported; "plain" is rejected.
func VerifyPKCE(method, challenge, verifier string) error {
	if method != "S256" {
		return fmt.Errorf("only S256 PKCE is supported, got %q", method)
	}
	sum := sha256.Sum256([]byte(verifier))
	expected := base64.RawURLEncoding.EncodeToString(sum[:])
	if subtle.ConstantTimeCompare([]byte(expected), []byte(challenge)) != 1 {
		return errors.New("code_verifier does not match code_challenge")
	}
	return nil
}

// NewAuthCode returns (plaintext, hash). The plaintext is included in the
// 302 to the client; only the hash is persisted.
func NewAuthCode() (plain, hashed string) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("rng: " + err.Error())
	}
	plain = base64.RawURLEncoding.EncodeToString(b[:])
	hashed = HashToken(plain)
	return
}

// NewToken returns (plaintext, hash) for access and refresh tokens.
func NewToken() (plain, hashed string) { return NewAuthCode() }
