package auth

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
)

// Encryptor encrypts and decrypts byte strings using AES-256-GCM. It supports
// a primary key (used for new ciphertext) and an optional previous key
// (accepted for decryption during rotation).
type Encryptor struct {
	primary  cipher.AEAD
	previous cipher.AEAD // may be nil
}

// NewEncryptor builds an Encryptor. Both keys, when set, must be 32 bytes.
func NewEncryptor(primary, previous []byte) (*Encryptor, error) {
	if len(primary) != 32 {
		return nil, errors.New("primary key must be 32 bytes")
	}
	pAEAD, err := newGCM(primary)
	if err != nil {
		return nil, err
	}
	e := &Encryptor{primary: pAEAD}
	if previous != nil {
		if len(previous) != 32 {
			return nil, errors.New("previous key must be 32 bytes")
		}
		prevAEAD, err := newGCM(previous)
		if err != nil {
			return nil, err
		}
		e.previous = prevAEAD
	}
	return e, nil
}

func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// Seal encrypts plaintext with the primary key. Output layout: nonce || ciphertext.
func (e *Encryptor) Seal(plaintext []byte) ([]byte, error) {
	nonce := make([]byte, e.primary.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("nonce: %w", err)
	}
	out := e.primary.Seal(nonce, nonce, plaintext, nil)
	return out, nil
}

// Open decrypts. Tries the primary key first, then the previous key.
func (e *Encryptor) Open(ct []byte) ([]byte, error) {
	if len(ct) < e.primary.NonceSize() {
		return nil, errors.New("ciphertext too short")
	}
	nonce := ct[:e.primary.NonceSize()]
	body := ct[e.primary.NonceSize():]
	if pt, err := e.primary.Open(nil, nonce, body, nil); err == nil {
		return pt, nil
	}
	if e.previous != nil {
		if pt, err := e.previous.Open(nil, nonce, body, nil); err == nil {
			return pt, nil
		}
	}
	return nil, errors.New("decrypt: authentication failed")
}

// DecodeKey accepts a 32-byte key encoded as base64 or hex. Other lengths or
// encodings are rejected.
func DecodeKey(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, errors.New("empty key")
	}
	if b, err := hex.DecodeString(s); err == nil && len(b) == 32 {
		return b, nil
	}
	if b, err := base64.StdEncoding.DecodeString(s); err == nil && len(b) == 32 {
		return b, nil
	}
	if b, err := base64.RawStdEncoding.DecodeString(s); err == nil && len(b) == 32 {
		return b, nil
	}
	return nil, errors.New("key must be 32 bytes encoded as hex or base64")
}
