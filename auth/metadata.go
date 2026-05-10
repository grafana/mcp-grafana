package auth

import (
	"encoding/json"
	"net/http"
)

// ASMetadataHandler returns the RFC 8414 authorization-server metadata
// document. publicURL is the base URL (no trailing slash).
func ASMetadataHandler(publicURL string) http.Handler {
	doc := map[string]any{
		"issuer":                                publicURL,
		"authorization_endpoint":                publicURL + "/authorize",
		"token_endpoint":                        publicURL + "/token",
		"registration_endpoint":                 publicURL + "/register",
		"response_types_supported":              []string{"code"},
		"grant_types_supported":                 []string{"authorization_code", "refresh_token"},
		"code_challenge_methods_supported":      []string{"S256"},
		"token_endpoint_auth_methods_supported": []string{"none", "client_secret_basic"},
	}
	return jsonHandler(doc)
}

// ProtectedResourceMetadataHandler returns the RFC 9728 protected-resource
// metadata document the MCP client uses to discover the AS.
func ProtectedResourceMetadataHandler(publicURL string) http.Handler {
	doc := map[string]any{
		"resource":                 publicURL,
		"authorization_servers":    []string{publicURL},
		"bearer_methods_supported": []string{"header"},
	}
	return jsonHandler(doc)
}

func jsonHandler(payload any) http.Handler {
	body, _ := json.Marshal(payload)
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "public, max-age=300")
		_, _ = w.Write(body)
	})
}
