package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
)

// ErrNotFound is returned by Store methods when the requested record does not
// exist (or has expired and been swept).
var ErrNotFound = errors.New("auth: record not found")

// Store persists sessions, dynamic clients, and auth codes. Implementations
// must be safe for concurrent use.
type Store interface {
	PutSession(ctx context.Context, s Session) error
	GetSessionByTokenHash(ctx context.Context, tokenHash string) (Session, error)
	GetSessionByRefreshHash(ctx context.Context, refreshHash string) (Session, error)
	GetSessionByIdentity(ctx context.Context, id Identity) (Session, error)
	DeleteSession(ctx context.Context, tokenHash string) error

	PutClient(ctx context.Context, c DCRClient) error
	GetClient(ctx context.Context, clientID string) (DCRClient, error)

	PutAuthCode(ctx context.Context, c AuthCode) error
	ConsumeAuthCode(ctx context.Context, codeHash string) (AuthCode, error)
}

// HashToken returns the canonical hash used as a storage key for both
// access and refresh tokens.
func HashToken(plaintext string) string {
	sum := sha256.Sum256([]byte(plaintext))
	return hex.EncodeToString(sum[:])
}
