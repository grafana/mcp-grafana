package auth

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"time"
)

// samlValidator returns the upstream as a SAMLValidator if it implements
// the interface; otherwise nil. Used by the SAML route handlers to fail
// closed when called against a non-SAML upstream.
func (s *Server) samlValidator() SAMLValidator {
	if v, ok := s.Upstream.(SAMLValidator); ok {
		return v
	}
	return nil
}

// SAMLMetadataHandler serves the SP entity metadata XML at /saml/metadata.
// IdPs use this to register mcp-grafana as a service provider.
func (s *Server) SAMLMetadataHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		v := s.samlValidator()
		if v == nil {
			http.NotFound(w, r)
			return
		}
		body, err := v.MetadataXML()
		if err != nil {
			httpError(w, http.StatusInternalServerError, "server_error", "metadata generation failed")
			return
		}
		w.Header().Set("Content-Type", "application/samlmetadata+xml")
		w.Header().Set("Cache-Control", "public, max-age=300")
		_, _ = w.Write(body)
	})
}

// SAMLACSHandler validates a POSTed SAMLResponse, extracts identity, and
// runs the same callback flow as Mode C: if the user already has an SA
// token on file, redirect back to the MCP client; otherwise, redirect to
// /bootstrap.
func (s *Server) SAMLACSHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		v := s.samlValidator()
		if v == nil {
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			httpError(w, http.StatusMethodNotAllowed, "invalid_request", "POST required")
			return
		}

		result, err := v.ValidateAssertion(r)
		if err != nil {
			s.logger().Warn("auth.saml_assertion_rejected", "error", err.Error())
			httpError(w, http.StatusBadRequest, "invalid_request", "invalid SAML assertion")
			return
		}

		// Look up the pendingFlow by RelayState (acts like OAuth state).
		pf, ok := consumePending(result.RelayState)
		if !ok {
			httpError(w, http.StatusBadRequest, "invalid_request", "unknown or expired RelayState")
			return
		}

		// Run the Mode-C-equivalent flow: existing session shortcut, else
		// redirect to /bootstrap.
		existing, err := s.Store.GetSessionByIdentity(r.Context(), result.Identity)
		if err == nil && len(existing.UpstreamCredsCT) > 0 {
			s.completeAuthCode(w, r, pf, result.Identity, existing.UpstreamCredsCT, existing.UpstreamRefreshCT, existing.UpstreamExpiresAt)
			return
		}

		// First login — redirect to /bootstrap.
		var fb [16]byte
		_, _ = rand.Read(fb[:])
		flowToken := hex.EncodeToString(fb[:])
		storeBootstrap(flowToken, &pendingBootstrap{
			identity:            result.Identity,
			clientID:            pf.clientID,
			redirectURI:         pf.redirectURI,
			clientState:         pf.clientState,
			codeChallenge:       pf.codeChallenge,
			codeChallengeMethod: pf.codeChallengeMethod,
			createdAt:           time.Now(),
		})

		http.Redirect(w, r, samlBootstrapURL(flowToken), http.StatusFound)
	})
}

// samlBootstrapURL returns the /bootstrap?flow=... URL.
func samlBootstrapURL(flow string) string {
	return "/bootstrap?flow=" + flow
}

// SAMLSLSHandler handles SAML Single Logout requests from the IdP.
// On success, deletes the user's sessions and redirects the user-agent
// back to the IdP's SLO endpoint with a signed LogoutResponse.
func (s *Server) SAMLSLSHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		v := s.samlValidator()
		if v == nil {
			http.NotFound(w, r)
			return
		}

		identity, redirectURL, err := v.BuildLogoutResponseURL(r)
		if err != nil {
			s.logger().Warn("auth.saml_logout_rejected", "error", err.Error())
			httpError(w, http.StatusBadRequest, "invalid_request", "invalid logout request")
			return
		}

		// Drop all sessions matching this identity.
		if existing, err := s.Store.GetSessionByIdentity(r.Context(), identity); err == nil {
			_ = s.Store.DeleteSession(r.Context(), existing.TokenHash)
			if s.RBAC != nil {
				s.RBAC.InvalidateSessionCache(existing.TokenHash)
			}
		}

		s.logger().Info("auth.session_revoked", "user_id", identity.String(), "reason", "saml_slo")
		http.Redirect(w, r, redirectURL, http.StatusFound)
	})
}
