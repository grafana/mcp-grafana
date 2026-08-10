package mcpgrafana

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRequireBearerToken(t *testing.T) {
	const token = "s3cret-caller-token"

	// Downstream handler records whether it was reached.
	var reached bool
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		w.WriteHeader(http.StatusOK)
	})
	handler := RequireBearerToken(token, slog.Default())(next)

	cases := []struct {
		name       string
		authHeader string
		wantStatus int
		wantReach  bool
	}{
		{"missing header", "", http.StatusUnauthorized, false},
		{"wrong token", "Bearer wrong", http.StatusUnauthorized, false},
		{"not bearer scheme", "Basic " + token, http.StatusUnauthorized, false},
		{"empty bearer", "Bearer ", http.StatusUnauthorized, false},
		{"valid token", "Bearer " + token, http.StatusOK, true},
		{"valid token lowercase scheme", "bearer " + token, http.StatusOK, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reached = false
			req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
			if tc.authHeader != "" {
				req.Header.Set("Authorization", tc.authHeader)
			}
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			assert.Equal(t, tc.wantStatus, rec.Code)
			assert.Equal(t, tc.wantReach, reached)
			if tc.wantStatus == http.StatusUnauthorized {
				assert.Equal(t, "Bearer", rec.Header().Get("WWW-Authenticate"))
			}
		})
	}
}

// An accidentally-empty expected token must fail closed, never allow-all.
func TestRequireBearerToken_EmptyExpectedFailsClosed(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not be reached when expected token is empty")
	})
	handler := RequireBearerToken("", slog.Default())(next)

	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req.Header.Set("Authorization", "Bearer anything")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusUnauthorized, rec.Code)
}

// Rejections must be logged for audit, but the token value must never appear in
// the logs.
func TestRequireBearerToken_LogsRejectionWithoutLeakingToken(t *testing.T) {
	const secret = "the-expected-secret-token"
	const presented = "the-wrong-presented-token"

	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not be reached for a rejected request")
	})
	handler := RequireBearerToken(secret, logger)(next)

	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req.Header.Set("Authorization", "Bearer "+presented)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusUnauthorized, rec.Code)

	logs := buf.String()
	assert.Contains(t, logs, "Rejected unauthenticated request")
	assert.Contains(t, logs, "invalid bearer token")
	assert.Contains(t, logs, "method=POST")
	assert.Contains(t, logs, "path=/mcp")
	// The secret and the presented token must never be written to logs.
	assert.NotContains(t, logs, secret)
	assert.NotContains(t, logs, presented)
}

// A successful (authenticated) request must not emit a rejection log line.
func TestRequireBearerToken_NoLogOnSuccess(t *testing.T) {
	const secret = "the-expected-secret-token"

	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := RequireBearerToken(secret, logger)(next)

	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req.Header.Set("Authorization", "Bearer "+secret)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Empty(t, buf.String(), "authenticated request should not log a rejection")
}

// After successful authentication the caller's Authorization header must be
// stripped so it can never be forwarded to Grafana or folded into a cache key.
func TestRequireBearerToken_StripsAuthorizationAfterAuth(t *testing.T) {
	const token = "s3cret-caller-token"

	var seenAuth string
	var reached bool
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		seenAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	})
	handler := RequireBearerToken(token, slog.Default())(next)

	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.True(t, reached)
	assert.Empty(t, seenAuth, "Authorization header must be stripped before reaching the downstream handler")
}

// CORS preflight must be allowed through unauthenticated so browser clients can
// negotiate; it carries no bearer token and performs no MCP operation.
func TestRequireBearerToken_AllowsOPTIONSPreflight(t *testing.T) {
	const token = "s3cret-caller-token"

	var reached bool
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		w.WriteHeader(http.StatusNoContent)
	})
	handler := RequireBearerToken(token, slog.Default())(next)

	req := httptest.NewRequest(http.MethodOptions, "/mcp", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.True(t, reached, "OPTIONS preflight should reach the downstream CORS handler")
	assert.Equal(t, http.StatusNoContent, rec.Code)
}

func TestForwardsAuthorizationHeader(t *testing.T) {
	cases := []struct {
		name string
		env  string
		want bool
	}{
		{"unset -> false", "", false},
		{"unrelated headers -> false", "Cookie, X-Session-Id", false},
		{"exact match -> true", "Authorization", true},
		{"case-insensitive match -> true", " cookie , authorization ", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("GRAFANA_FORWARD_HEADERS", tc.env)
			assert.Equal(t, tc.want, ForwardsAuthorizationHeader())
		})
	}
}

func TestIsLoopbackOnlyBind(t *testing.T) {
	cases := []struct {
		address string
		want    bool
	}{
		{"localhost:8000", true},
		{"127.0.0.1:8000", true},
		{"[::1]:8000", true},
		{"foo.localhost:8000", true},
		{"127.0.0.1", true},
		{"::1", true},
		// Public / wildcard binds.
		{"0.0.0.0:8000", false},
		{":8000", false},
		{"[::]:8000", false},
		{"192.168.1.10:8000", false},
		{"10.0.0.5:8000", false},
		{"example.com:8000", false},
		{"grafana.example.com:8000", false},
	}
	for _, tc := range cases {
		t.Run(tc.address, func(t *testing.T) {
			assert.Equal(t, tc.want, IsLoopbackOnlyBind(tc.address))
		})
	}
}
