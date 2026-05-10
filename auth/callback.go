package auth

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"net/url"
	"time"
)

// pendingBootstrap holds the data we'll need after the user submits their
// SA token at /bootstrap. Keyed by an opaque "flow" token in the URL.
// TTL enforcement happens in pendingRegistry via pendingEntry.createdAt;
// no per-value timestamp is needed here.
type pendingBootstrap struct {
	identity            Identity
	clientID            string
	redirectURI         string
	clientState         string
	codeChallenge       string
	codeChallengeMethod string
	// attempts counts rejected token pastes against this flow. Bumped under
	// the registry mutex via pendingRegistry.Modify so concurrent POSTs
	// can't race past the cap. See maxBootstrapAttempts in bootstrap.go.
	attempts int
}

// bootstrapTTL bounds how long a /bootstrap-side pending entry stays in
// memory before it's swept away. Matches the existing 15-minute
// freshness check in processBootstrap.
const bootstrapTTL = 15 * time.Minute

// authzPendings returns the per-Server /authorize → /callback registry,
// initializing it on first use. Lazy init keeps &Server{...} struct
// literals (used widely in tests) working without an explicit constructor
// step while still giving each Server its own state — eliminates the
// cross-Server / cross-test pollution the package-level globals used to
// have.
func (s *Server) authzPendings() *pendingRegistry[*pendingFlow] {
	s.pendingsOnce.Do(s.initPendings)
	return s.authzReg
}

// bootstrapPendings returns the per-Server /bootstrap registry, lazy
// initialized on first use. Same rationale as authzPendings.
func (s *Server) bootstrapPendings() *pendingRegistry[*pendingBootstrap] {
	s.pendingsOnce.Do(s.initPendings)
	return s.bootstrapReg
}

func (s *Server) initPendings() {
	s.authzReg = newPendingRegistry[*pendingFlow](pendingFlowTTL)
	s.bootstrapReg = newPendingRegistry[*pendingBootstrap](bootstrapTTL)
}

// peekBootstrap returns a snapshot of the pending entry without removing it.
// Used by /bootstrap GET and POST to read the entry under the mutex without
// consuming it (consumption happens after token validation).
func (s *Server) peekBootstrap(token string) (pendingBootstrap, bool) {
	p, ok := s.bootstrapPendings().Peek(token)
	if !ok {
		return pendingBootstrap{}, false
	}
	return *p, true
}

// CallbackHandler handles the upstream IdP callback (Mode C: OIDC code).
func (s *Server) CallbackHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		state := r.URL.Query().Get("state")
		pf, ok := s.authzPendings().Consume(state)
		if !ok {
			httpError(w, http.StatusBadRequest, "invalid_request", "unknown or expired state")
			return
		}

		result, err := s.Upstream.HandleCallback(r.Context(), r.URL.Query())
		if err != nil {
			// Log the underlying upstream error for operators; redirect the
			// user-agent back to the MCP client with a generic
			// error_description to avoid leaking internal hostnames or
			// upstream token-exchange details to external clients.
			s.logger().Warn("auth.upstream_callback_failed", "error", err.Error())
			httpRedirectError(w, r, pf.redirectURI, "access_denied", "upstream authentication failed", pf.clientState)
			return
		}

		// Mode A lands here with HasCred=true. Encrypt the credential pair before
		// passing it through to the auth-code shortcut.
		if result.HasCred {
			credCT, err := s.Encryptor.Seal(result.UpstreamCreds)
			if err != nil {
				// Log the cipher-level detail locally; redirect the
				// user-agent back to the MCP client with a generic
				// error_description so internal crypto state doesn't
				// reach external clients (matches the pattern above).
				s.logger().Error("auth.callback_encrypt_failed", "error", err.Error())
				httpRedirectError(w, r, pf.redirectURI, "server_error", "credential encryption failed", pf.clientState)
				return
			}
			var refreshCT []byte
			if len(result.UpstreamRefresh) > 0 {
				refreshCT, err = s.Encryptor.Seal(result.UpstreamRefresh)
				if err != nil {
					s.logger().Error("auth.callback_encrypt_failed", "error", err.Error())
					httpRedirectError(w, r, pf.redirectURI, "server_error", "credential encryption failed", pf.clientState)
					return
				}
			}
			s.completeAuthCode(w, r, pf, result.Identity, credCT, refreshCT, result.UpstreamExpiresAt)
			return
		}

		// Mode C (and SAML, in phase 4): existing SA token shortcut, else
		// redirect to /bootstrap.
		s.resolveIdentityOrBootstrap(w, r, pf, result.Identity)
	})
}

// resolveIdentityOrBootstrap completes the auth code if a session already
// exists for this identity (Mode C / SAML existing-user shortcut), or
// stores a pendingBootstrap and 302s to /bootstrap if not. Shared between
// the OAuth /callback handler and the SAML /saml/acs handler so future
// changes (audit logging, identity post-processing) stay consistent.
func (s *Server) resolveIdentityOrBootstrap(w http.ResponseWriter, r *http.Request, pf *pendingFlow, identity Identity) {
	existing, err := s.Store.GetSessionByIdentity(r.Context(), identity)
	if err == nil && len(existing.UpstreamCredsCT) > 0 {
		s.completeAuthCode(w, r, pf, identity, existing.UpstreamCredsCT, existing.UpstreamRefreshCT, existing.UpstreamExpiresAt)
		return
	}

	var fb [16]byte
	if _, err := rand.Read(fb[:]); err != nil {
		panic("rng: " + err.Error())
	}
	flowToken := hex.EncodeToString(fb[:])
	s.bootstrapPendings().Store(flowToken, &pendingBootstrap{
		identity:            identity,
		clientID:            pf.clientID,
		redirectURI:         pf.redirectURI,
		clientState:         pf.clientState,
		codeChallenge:       pf.codeChallenge,
		codeChallengeMethod: pf.codeChallengeMethod,
	})

	bs := url.URL{Path: "/bootstrap"}
	q := bs.Query()
	q.Set("flow", flowToken)
	bs.RawQuery = q.Encode()
	http.Redirect(w, r, bs.String(), http.StatusFound)
}

// completeAuthCode mints a one-shot auth code, persists it, and 302s the
// user-agent back to the MCP client's redirect_uri with code+state.
func (s *Server) completeAuthCode(w http.ResponseWriter, r *http.Request, pf *pendingFlow, identity Identity, credCT, refreshCT []byte, upstreamExpiresAt time.Time) {
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
		UpstreamRefreshCT:   refreshCT,
		UpstreamExpiresAt:   upstreamExpiresAt,
		ExpiresAt:           time.Now().Add(ttl),
	}); err != nil {
		// Log the storage detail locally; redirect with a generic
		// description so internal storage state (file paths, DB
		// internals) doesn't reach the client's URL — and from there
		// browser history, proxy logs, and the redirect target's
		// access logs. Matches the upstream-error sanitization above.
		s.logger().Error("auth.authcode_persist_failed", "user_id", identity.String(), "error", err.Error())
		httpRedirectError(w, r, pf.redirectURI, "server_error", "auth code persist failed", pf.clientState)
		return
	}
	u, err := url.Parse(pf.redirectURI)
	if err != nil {
		// pf.redirectURI was validated against the client's registered
		// URIs at /authorize time, so this branch shouldn't fire — but
		// if a parser change ever does trip it, log internally rather
		// than echo the err string back to the user.
		s.logger().Error("auth.redirect_uri_parse_failed", "redirect_uri", pf.redirectURI, "error", err.Error())
		httpError(w, http.StatusBadRequest, "invalid_redirect_uri", "registered redirect_uri did not parse")
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
