package auth

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"net/url"
	"sync"
	"time"
)

// pendingBootstrap holds the data we'll need after the user submits their
// SA token at /bootstrap. Keyed by an opaque "flow" token in the URL.
type pendingBootstrap struct {
	identity            Identity
	clientID            string
	redirectURI         string
	clientState         string
	codeChallenge       string
	codeChallengeMethod string
	createdAt           time.Time
}

// bootstrapTTL bounds how long a /bootstrap-side pending entry stays in
// memory before it's swept away. Matches the existing 15-minute
// freshness check in processBootstrap.
const bootstrapTTL = 15 * time.Minute

var (
	bootstrapMu        sync.Mutex
	bootstrapPendings  = map[string]*pendingBootstrap{}
	bootstrapLastSwept time.Time
)

// sweepBootstrapLocked drops entries older than bootstrapTTL. The caller
// must hold bootstrapMu. Runs at most once per bootstrapTTL window for
// amortised O(1) cost under sustained traffic.
func sweepBootstrapLocked(now time.Time) {
	if now.Sub(bootstrapLastSwept) < bootstrapTTL {
		return
	}
	cutoff := now.Add(-bootstrapTTL)
	for k, p := range bootstrapPendings {
		if p.createdAt.Before(cutoff) {
			delete(bootstrapPendings, k)
		}
	}
	bootstrapLastSwept = now
}

func storeBootstrap(token string, p *pendingBootstrap) {
	bootstrapMu.Lock()
	defer bootstrapMu.Unlock()
	sweepBootstrapLocked(time.Now())
	bootstrapPendings[token] = p
}

func consumeBootstrap(token string) (*pendingBootstrap, bool) {
	bootstrapMu.Lock()
	defer bootstrapMu.Unlock()
	sweepBootstrapLocked(time.Now())
	p, ok := bootstrapPendings[token]
	if !ok {
		return nil, false
	}
	delete(bootstrapPendings, token)
	if time.Since(p.createdAt) > bootstrapTTL {
		return nil, false
	}
	return p, true
}

// peekBootstrap returns a snapshot of the pending entry without removing it.
// Used by /bootstrap GET and POST to read the entry under the mutex without
// consuming it (consumption happens after token validation).
func peekBootstrap(token string) (pendingBootstrap, bool) {
	bootstrapMu.Lock()
	defer bootstrapMu.Unlock()
	sweepBootstrapLocked(time.Now())
	p, ok := bootstrapPendings[token]
	if !ok {
		return pendingBootstrap{}, false
	}
	if time.Since(p.createdAt) > bootstrapTTL {
		delete(bootstrapPendings, token)
		return pendingBootstrap{}, false
	}
	return *p, true
}

// CallbackHandler handles the upstream IdP callback (Mode C: OIDC code).
func (s *Server) CallbackHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		state := r.URL.Query().Get("state")
		pf, ok := consumePending(state)
		if !ok {
			httpError(w, http.StatusBadRequest, "invalid_request", "unknown or expired state")
			return
		}

		identity, cred, hasCred, err := s.Upstream.HandleCallback(r.Context(), r.URL.Query())
		if err != nil {
			httpRedirectError(w, r, pf.redirectURI, "access_denied", err.Error(), pf.clientState)
			return
		}

		// Mode A would land here with hasCred=true. Mode C never does.
		if hasCred {
			s.completeAuthCode(w, r, pf, identity, cred)
			return
		}

		// Mode C: do we already have an SA token for this identity?
		existing, err := s.Store.GetSessionByIdentity(r.Context(), identity)
		if err == nil && len(existing.UpstreamCredsCT) > 0 {
			s.completeAuthCode(w, r, pf, identity, existing.UpstreamCredsCT)
			return
		}

		// First login (or token previously cleared): redirect to /bootstrap.
		var fb [16]byte
		_, _ = rand.Read(fb[:])
		flowToken := hex.EncodeToString(fb[:])
		storeBootstrap(flowToken, &pendingBootstrap{
			identity:            identity,
			clientID:            pf.clientID,
			redirectURI:         pf.redirectURI,
			clientState:         pf.clientState,
			codeChallenge:       pf.codeChallenge,
			codeChallengeMethod: pf.codeChallengeMethod,
			createdAt:           time.Now(),
		})

		bs := url.URL{Path: "/bootstrap"}
		q := bs.Query()
		q.Set("flow", flowToken)
		bs.RawQuery = q.Encode()
		http.Redirect(w, r, bs.String(), http.StatusFound)
	})
}

// completeAuthCode mints a one-shot auth code, persists it, and 302s the
// user-agent back to the MCP client's redirect_uri with code+state.
func (s *Server) completeAuthCode(w http.ResponseWriter, r *http.Request, pf *pendingFlow, identity Identity, credCT []byte) {
	plain, hashed := NewAuthCode()
	ttl := s.AuthCodeTTL
	if ttl == 0 {
		ttl = 5 * time.Minute
	}
	if err := s.Store.PutAuthCode(r.Context(), AuthCode{
		Code:                hashed,
		ClientID:            pf.clientID,
		RedirectURI:         pf.redirectURI,
		CodeChallenge:       pf.codeChallenge,
		CodeChallengeMethod: pf.codeChallengeMethod,
		Identity:            identity,
		UpstreamCredsCT:     credCT,
		ExpiresAt:           time.Now().Add(ttl),
	}); err != nil {
		httpRedirectError(w, r, pf.redirectURI, "server_error", err.Error(), pf.clientState)
		return
	}
	u, err := url.Parse(pf.redirectURI)
	if err != nil {
		httpError(w, http.StatusBadRequest, "invalid_redirect_uri", err.Error())
		return
	}
	q := u.Query()
	q.Set("code", plain)
	if pf.clientState != "" {
		q.Set("state", pf.clientState)
	}
	u.RawQuery = q.Encode()
	http.Redirect(w, r, u.String(), http.StatusFound)
}
