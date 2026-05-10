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
//
// Sessions are unique per Identity: at any moment a given Identity has at most
// one live session. PutSession with an Identity that already has a session
// replaces the previous one; GetSessionByIdentity returns the single current
// session. Implementations must enforce this invariant.
type Store interface {
	// PutSession upserts s. If an existing session for s.Identity is replaced,
	// the old session's TokenHash is returned so the caller can atomically
	// emit a paired SessionRevoked metric / invalidate caches keyed by it.
	// Returns "" when this is a fresh install or a same-token rotation under
	// the same TokenHash. Callers must use this signal rather than a
	// pre-PutSession GetSessionByIdentity check, which is racy with the
	// middleware's expired-token DeleteSession path.
	PutSession(ctx context.Context, s Session) (replacedTokenHash string, err error)
	GetSessionByTokenHash(ctx context.Context, tokenHash string) (Session, error)
	GetSessionByRefreshHash(ctx context.Context, refreshHash string) (Session, error)
	// GetSessionByIdentity returns the single live session for id, if any.
	// Per the Store-level invariant, this is unambiguous: identities map 1-to-1
	// to sessions.
	GetSessionByIdentity(ctx context.Context, id Identity) (Session, error)
	// DeleteSession removes the session keyed by tokenHash. The bool return
	// reports whether a session was actually deleted; callers that maintain
	// per-session counters (e.g. the SessionRevoked metric) gate their
	// decrement on it so concurrent requests against the same expired
	// session don't double-count.
	DeleteSession(ctx context.Context, tokenHash string) (bool, error)

	PutClient(ctx context.Context, c DCRClient) error
	GetClient(ctx context.Context, clientID string) (DCRClient, error)

	PutAuthCode(ctx context.Context, c AuthCode) error
	// PeekAuthCode returns the code's stored payload without removing it.
	// Used by handleAuthCodeGrant to validate PKCE / client / redirect_uri
	// BEFORE the single-use consume — otherwise a malformed redemption
	// burns the code and a legitimate retry from the same client can
	// never succeed (RFC 6749 §4.1.2 only forbids reuse AFTER success).
	PeekAuthCode(ctx context.Context, codeHash string) (AuthCode, error)
	ConsumeAuthCode(ctx context.Context, codeHash string) (AuthCode, error)
}

// HashToken returns the canonical hash used as a storage key for both
// access and refresh tokens.
func HashToken(plaintext string) string {
	sum := sha256.Sum256([]byte(plaintext))
	return hex.EncodeToString(sum[:])
}
