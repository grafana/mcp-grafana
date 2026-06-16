package tools

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRefuseRedirectMSearch verifies that a client configured with
// refuseRedirect surfaces a clear error instead of silently following a
// redirect that would downgrade the POST to a body-less GET (the failure mode
// behind https://github.com/grafana/mcp-grafana/issues/938).
func TestRefuseRedirectMSearch(t *testing.T) {
	var gotMethods []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethods = append(gotMethods, r.Method)
		// Redirect to a canonical path, the way a root_url / scheme mismatch
		// would. A naive client follows this as a GET and drops the body.
		http.Redirect(w, r, "/canonical/_msearch", http.StatusMovedPermanently)
	}))
	defer srv.Close()

	client := &http.Client{CheckRedirect: refuseRedirect}
	searchQuery := esSearchQuery{query: "*", size: 10, timeField: defaultTimeField}.build()

	_, err := executeMSearch(context.Background(), client, srv.URL+"/_msearch", "logs-*", searchQuery, defaultTimeField)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "refusing to follow HTTP redirect")
	assert.Contains(t, err.Error(), "GRAFANA_URL")
	// The redirect must not have been followed, so only the original request hit the server.
	assert.Equal(t, []string{http.MethodPost}, gotMethods)
}

// TestRefuseRedirectFollows307 confirms that a 307 redirect — which preserves
// the request method and body — is followed rather than refused, so deployments
// behind proxies/load balancers that issue 307/308 keep working.
func TestRefuseRedirectFollows307(t *testing.T) {
	var (
		gotMethods []string
		gotBodyLen int
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethods = append(gotMethods, r.Method)
		if r.URL.Path == "/_msearch" {
			http.Redirect(w, r, "/final/_msearch", http.StatusTemporaryRedirect)
			return
		}
		// Final hop: the body must have survived the redirect.
		body, _ := io.ReadAll(r.Body)
		gotBodyLen = len(body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"took":1,"responses":[{"took":1,"hits":{"total":{"value":0,"relation":"eq"},"hits":[]}}]}`))
	}))
	defer srv.Close()

	client := &http.Client{CheckRedirect: refuseRedirect, Timeout: 5 * time.Second}
	searchQuery := esSearchQuery{query: "*", size: 10, timeField: defaultTimeField}.build()

	docs, err := executeMSearch(context.Background(), client, srv.URL+"/_msearch", "logs-*", searchQuery, defaultTimeField)
	require.NoError(t, err)
	assert.Empty(t, docs)
	// Both hops used POST (method preserved) and the body reached the final hop.
	assert.Equal(t, []string{http.MethodPost, http.MethodPost}, gotMethods)
	assert.Positive(t, gotBodyLen, "request body should survive a 307 redirect")
}

// TestRefuseRedirectAllowsNonRedirect confirms the CheckRedirect hook is inert
// when the server responds without a redirect.
func TestRefuseRedirectAllowsNonRedirect(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"took":1,"responses":[{"took":1,"hits":{"total":{"value":0,"relation":"eq"},"hits":[]}}]}`))
	}))
	defer srv.Close()

	client := &http.Client{CheckRedirect: refuseRedirect, Timeout: 5 * time.Second}
	searchQuery := esSearchQuery{query: "*", size: 10, timeField: defaultTimeField}.build()

	docs, err := executeMSearch(context.Background(), client, srv.URL+"/_msearch", "logs-*", searchQuery, defaultTimeField)
	require.NoError(t, err)
	assert.Empty(t, docs)
}
