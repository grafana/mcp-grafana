package auth

import (
	"net/http"
	"slices"
	"sync"
	"time"
)

// pendingFlow records what we expect to come back from /callback.
type pendingFlow struct {
	clientID            string
	redirectURI         string
	codeChallenge       string
	codeChallengeMethod string
	clientState         string
	createdAt           time.Time
}

// In-memory pending-flow registry. Keyed by upstream state value.
// State values are large random tokens; collisions are infeasible.
var (
	pendingMu sync.Mutex
	pendings  = map[string]*pendingFlow{}
)

func storePending(state string, p *pendingFlow) {
	pendingMu.Lock()
	defer pendingMu.Unlock()
	pendings[state] = p
}

func consumePending(state string) (*pendingFlow, bool) {
	pendingMu.Lock()
	defer pendingMu.Unlock()
	p, ok := pendings[state]
	if ok {
		delete(pendings, state)
	}
	return p, ok
}

// AuthorizeHandler validates the inbound /authorize request and redirects
// the user-agent to the upstream IdP.
func (s *Server) AuthorizeHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		clientID := q.Get("client_id")
		redirectURI := q.Get("redirect_uri")
		responseType := q.Get("response_type")
		challenge := q.Get("code_challenge")
		challengeMethod := q.Get("code_challenge_method")
		clientState := q.Get("state")

		// First: validate client_id and redirect_uri. Errors here are JSON
		// (we don't have a verified redirect to safely send the user back to).
		if clientID == "" || redirectURI == "" {
			httpError(w, http.StatusBadRequest, "invalid_request", "client_id and redirect_uri are required")
			return
		}
		client, err := s.Store.GetClient(r.Context(), clientID)
		if err != nil {
			httpError(w, http.StatusBadRequest, "invalid_client", "unknown client_id")
			return
		}
		if !slices.Contains(client.RedirectURIs, redirectURI) {
			httpError(w, http.StatusBadRequest, "invalid_request", "redirect_uri does not match registered URIs")
			return
		}

		// From here on, redirect_uri is verified. OAuth errors go back to the client
		// via redirect with error= and state= (RFC 6749 §4.1.2.1).
		if responseType != "code" {
			httpRedirectError(w, r, redirectURI, "unsupported_response_type", "response_type must be 'code'", clientState)
			return
		}
		if challenge == "" || challengeMethod != "S256" {
			httpRedirectError(w, r, redirectURI, "invalid_request", "PKCE code_challenge with method=S256 is required", clientState)
			return
		}

		state := stateToken()
		storePending(state, &pendingFlow{
			clientID:            clientID,
			redirectURI:         redirectURI,
			codeChallenge:       challenge,
			codeChallengeMethod: challengeMethod,
			clientState:         clientState,
			createdAt:           time.Now(),
		})

		dest := s.Upstream.AuthorizeURL(s.PublicURL+"/callback", state)
		http.Redirect(w, r, dest, http.StatusFound)
	})
}
