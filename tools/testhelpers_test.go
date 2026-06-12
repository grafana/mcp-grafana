package tools

import (
	"encoding/json"
	"net/http"
)

// withFrontendSettings wraps h so GET /api/frontend/settings returns a canned
// 200 reporting the given namespace (an empty namespace exercises the
// org-derived fallback), delegating everything else to h.
//
// Tools that target the app-platform (/apis/*) APIs resolve their namespace via
// mcpgrafana.GrafanaNamespace, which fetches /api/frontend/settings first and
// now errors if it cannot be reached. Mock servers in tests must therefore
// answer that endpoint; wrapping their handler with this keeps each test focused
// on the request actually under test.
//
// This lives in an untagged test file so it is available to both the
// `//go:build unit` tests and the untagged ones (e.g. dashboard_k8s_test.go).
func withFrontendSettings(namespace string, h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/frontend/settings" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"namespace": namespace})
			return
		}
		h(w, r)
	}
}
