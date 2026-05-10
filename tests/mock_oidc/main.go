// Package main is a tiny OIDC IdP for integration tests. It accepts any
// /authorize request, redirects back with a fixed code, and returns a signed
// ID token from /token. Not for production use.
package main

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"flag"
	"log"
	"math/big"
	"net/http"
	"net/url"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
)

var (
	addr     = flag.String("addr", ":9999", "address")
	clientID = flag.String("client-id", "mcp", "expected client_id")
	subject  = flag.String("sub", "alice", "subject claim")
	email    = flag.String("email", "alice@example.com", "email claim")
)

func main() {
	flag.Parse()

	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		log.Fatal(err)
	}
	kid := "mock-1"

	mux := http.NewServeMux()
	// addr is expected as ":NNNN" or "host:NNNN"; either way "localhost+addr"
	// is wrong when addr already includes a host. Tests run with ":NNNN".
	issuer := "http://localhost" + *addr

	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer":                                issuer,
			"authorization_endpoint":                issuer + "/authorize",
			"token_endpoint":                        issuer + "/token",
			"jwks_uri":                              issuer + "/jwks",
			"id_token_signing_alg_values_supported": []string{"RS256"},
			"response_types_supported":              []string{"code"},
			"subject_types_supported":               []string{"public"},
		})
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"keys": []map[string]any{{
				"kty": "RSA",
				"alg": "RS256",
				"use": "sig",
				"kid": kid,
				"n":   base64.RawURLEncoding.EncodeToString(priv.N.Bytes()),
				"e":   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(priv.E)).Bytes()),
			}},
		})
	})
	mux.HandleFunc("/authorize", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		redirect := q.Get("redirect_uri")
		state := q.Get("state")
		u, err := url.Parse(redirect)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		qq := u.Query()
		qq.Set("code", "mock-code")
		if state != "" {
			qq.Set("state", state)
		}
		u.RawQuery = qq.Encode()
		http.Redirect(w, r, u.String(), http.StatusFound)
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.Form.Get("code") != "mock-code" {
			http.Error(w, "bad code", http.StatusBadRequest)
			return
		}
		signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: priv}, (&jose.SignerOptions{}).WithType("JWT").WithHeader("kid", kid))
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		now := time.Now()
		claims := map[string]any{
			"iss":   issuer,
			"sub":   *subject,
			"aud":   *clientID,
			"iat":   now.Unix(),
			"exp":   now.Add(10 * time.Minute).Unix(),
			"email": *email,
			"nonce": r.Form.Get("nonce"), // echo it back; go-oidc validates if expected
		}
		idToken, err := jwt.Signed(signer).Claims(claims).Serialize()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "ignored",
			"token_type":   "Bearer",
			"expires_in":   600,
			"id_token":     idToken,
		})
	})

	log.Printf("mock OIDC listening on %s", *addr)
	log.Fatal(http.ListenAndServe(*addr, mux))
}
