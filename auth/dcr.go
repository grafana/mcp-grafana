package auth

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type dcrRequest struct {
	ClientName              string   `json:"client_name"`
	RedirectURIs            []string `json:"redirect_uris"`
	TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method"`
}

type dcrResponse struct {
	ClientID                string   `json:"client_id"`
	ClientName              string   `json:"client_name,omitempty"`
	RedirectURIs            []string `json:"redirect_uris"`
	TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method"`
	GrantTypes              []string `json:"grant_types"`
	ResponseTypes           []string `json:"response_types"`
}

// DCRHandler implements RFC 7591 dynamic client registration.
// Phase 1 supports only public PKCE clients (no client_secret issuance).
func DCRHandler(store Store) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			httpError(w, http.StatusMethodNotAllowed, "invalid_request", "POST required")
			return
		}
		var req dcrRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			httpError(w, http.StatusBadRequest, "invalid_client_metadata", "request body must be JSON")
			return
		}
		if len(req.RedirectURIs) == 0 {
			httpError(w, http.StatusBadRequest, "invalid_redirect_uri", "redirect_uris is required and must be non-empty")
			return
		}
		for _, uri := range req.RedirectURIs {
			if err := validateRedirectURI(uri); err != nil {
				httpError(w, http.StatusBadRequest, "invalid_redirect_uri", err.Error())
				return
			}
		}

		var idBytes [16]byte
		if _, err := rand.Read(idBytes[:]); err != nil {
			httpError(w, http.StatusInternalServerError, "server_error", "rng failure")
			return
		}
		client := DCRClient{
			ClientID:     "mcp-" + hex.EncodeToString(idBytes[:]),
			ClientName:   req.ClientName,
			RedirectURIs: req.RedirectURIs,
			CreatedAt:    time.Now(),
		}
		if err := store.PutClient(r.Context(), client); err != nil {
			httpError(w, http.StatusInternalServerError, "server_error", "registration failed")
			return
		}

		resp := dcrResponse{
			ClientID:                client.ClientID,
			ClientName:              client.ClientName,
			RedirectURIs:            client.RedirectURIs,
			TokenEndpointAuthMethod: "none",
			GrantTypes:              []string{"authorization_code", "refresh_token"},
			ResponseTypes:           []string{"code"},
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(resp)
	})
}

// validateRedirectURI accepts https:// URIs and http://loopback URIs only.
func validateRedirectURI(s string) error {
	u, err := url.Parse(s)
	if err != nil {
		return err
	}
	if u.Fragment != "" || strings.Contains(s, "#") {
		return errFragment
	}
	if u.Scheme == "https" {
		return nil
	}
	if u.Scheme == "http" {
		host := u.Hostname()
		switch host {
		case "localhost", "127.0.0.1", "::1":
			return nil
		}
		return errPlainHTTP
	}
	return errBadScheme
}

var (
	errPlainHTTP = stringError("redirect_uri must use https or http loopback")
	errBadScheme = stringError("redirect_uri must use https or http loopback")
	errFragment  = stringError("redirect_uri must not contain a fragment")
)

type stringError string

func (e stringError) Error() string { return string(e) }

// httpError writes an OAuth 2.0 RFC 6749 §5.2 / RFC 7591 error response.
func httpError(w http.ResponseWriter, status int, errCode, desc string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"error":             errCode,
		"error_description": desc,
	})
}

// httpRedirectError redirects the user-agent back to the client's redirect_uri
// with OAuth error parameters. Used for /authorize and /callback failures
// where the user-agent has already arrived from the client's redirect.
func httpRedirectError(w http.ResponseWriter, r *http.Request, redirectURI, errCode, desc, state string) {
	u, err := url.Parse(redirectURI)
	if err != nil {
		httpError(w, http.StatusBadRequest, "invalid_redirect_uri", desc)
		return
	}
	q := u.Query()
	q.Set("error", errCode)
	if desc != "" {
		q.Set("error_description", desc)
	}
	if state != "" {
		q.Set("state", state)
	}
	u.RawQuery = q.Encode()
	http.Redirect(w, r, u.String(), http.StatusFound)
}
