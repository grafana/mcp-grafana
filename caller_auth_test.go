package mcpgrafana

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
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

func TestRequireCallerToken(t *testing.T) {
	const token = "s3cret-caller-token"
	const header = "X-Mcp-Token"

	cases := []struct {
		name       string
		header     string
		values     []string
		wantStatus int
	}{
		{"raw token", header, []string{token}, http.StatusOK},
		{"bearer prefix", header, []string{"Bearer " + token}, http.StatusOK},
		{"lowercase bearer prefix", header, []string{"bearer " + token}, http.StatusOK},
		{"surrounding space", header, []string{"  " + token + " "}, http.StatusOK},
		{"header name case-insensitive", "x-mcp-token", []string{token}, http.StatusOK},
		{"wrong token", header, []string{"wrong"}, http.StatusUnauthorized},
		{"wrong bearer token", header, []string{"Bearer wrong"}, http.StatusUnauthorized},
		{"empty value", header, []string{""}, http.StatusUnauthorized},
		{"empty bearer", header, []string{"Bearer "}, http.StatusUnauthorized},
		{"missing header", "", nil, http.StatusUnauthorized},
		{"token in Authorization instead", "Authorization", []string{"Bearer " + token}, http.StatusUnauthorized},
		{"repeated header, first valid", header, []string{token, "other"}, http.StatusUnauthorized},
		{"repeated header, second valid", header, []string{"other", token}, http.StatusUnauthorized},
		{"repeated valid header", header, []string{token, token}, http.StatusUnauthorized},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reached := false
			handler := RequireCallerToken(header, token, slog.Default())(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				reached = true
			}))
			req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
			for _, v := range tc.values {
				req.Header.Add(tc.header, v)
			}
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			assert.Equal(t, tc.wantStatus, rec.Code)
			assert.Equal(t, tc.wantStatus == http.StatusOK, reached)
			assert.Empty(t, rec.Header().Get("WWW-Authenticate"), "Bearer challenge is for the Authorization header only")
		})
	}
}

// The default header keeps its behaviour: Bearer only, first value wins.
func TestRequireBearerToken_DefaultHeaderUnchanged(t *testing.T) {
	const token = "s3cret-caller-token"
	for _, tc := range []struct {
		name       string
		values     []string
		wantStatus int
	}{
		{"raw token", []string{token}, http.StatusUnauthorized},
		{"repeated, first valid", []string{"Bearer " + token, "Bearer other"}, http.StatusOK},
		{"repeated, first wrong", []string{"Bearer other", "Bearer " + token}, http.StatusUnauthorized},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got http.Header
			handler := RequireBearerToken(token, slog.Default())(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				got = r.Header.Clone()
			}))
			req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
			for _, v := range tc.values {
				req.Header.Add("Authorization", v)
			}
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			assert.Equal(t, tc.wantStatus, rec.Code)
			if tc.wantStatus == http.StatusOK {
				assert.Empty(t, got.Values("Authorization"), "all values stripped")
			} else {
				assert.Equal(t, "Bearer", rec.Header().Get("WWW-Authenticate"))
			}
		})
	}
}

func TestRequireCallerToken_EmptyHeaderNameFailsClosed(t *testing.T) {
	handler := RequireCallerToken("", "s3cret", slog.Default())(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("handler should not be reached")
	}))
	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req.Header.Set("Authorization", "Bearer s3cret")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestRequireCallerToken_LogsRejectionWithoutLeakingToken(t *testing.T) {
	const secret = "the-expected-secret-token"
	const presented = "the-wrong-presented-token"

	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
	handler := RequireCallerToken("X-Mcp-Token", secret, logger)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))

	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req.Header.Set("X-Mcp-Token", presented)
	handler.ServeHTTP(httptest.NewRecorder(), req)

	logs := buf.String()
	assert.Contains(t, logs, "invalid bearer token")
	assert.NotContains(t, logs, secret)
	assert.NotContains(t, logs, presented)
}

// A custom header leaves Authorization alone, so it can carry the user's own
// credential, and removes only the caller token.
func TestRequireCallerToken_StripsOnlyTheConfiguredHeader(t *testing.T) {
	const token = "s3cret-caller-token"
	var got http.Header
	handler := RequireCallerToken("X-Mcp-Token", token, slog.Default())(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
	}))

	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req.Header.Set("X-Mcp-Token", token)
	req.Header.Set("Authorization", "Bearer user-jwt")
	handler.ServeHTTP(httptest.NewRecorder(), req)

	require.NotNil(t, got)
	assert.Empty(t, got.Values("X-Mcp-Token"))
	assert.Equal(t, "Bearer user-jwt", got.Get("Authorization"))
}

// After the middleware, the caller token reaches neither Grafana nor the client
// cache key, even though the request carries it on a custom header.
func TestRequireCallerToken_TokenNeverReachesGrafanaOrCacheKey(t *testing.T) {
	const token = "s3cret-caller-token"
	const header = "X-Mcp-Token"

	var mu sync.Mutex
	var seen []http.Header
	grafana := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, r.Header.Clone())
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{}`)
	}))
	defer grafana.Close()
	t.Setenv(grafanaURLEnvVar, grafana.URL)
	t.Setenv(grafanaServiceAccountTokenEnvVar, "")
	t.Setenv(grafanaForwardHeadersEnvVar, "X-Tenant")

	cache := NewClientCache(nil)
	defer cache.Close()

	run := func(callerToken string) clientCacheKey {
		var key clientCacheKey
		handler := RequireCallerToken(header, token, slog.Default())(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			ctx := ExtractGrafanaInfoFromHeaders(WithGrafanaConfig(r.Context(), GrafanaConfig{}), r)
			ctx = extractGrafanaClientCached(cache)(ctx, r)
			client := GrafanaClientFromContext(ctx)
			require.NotNil(t, client)
			_, _ = client.Health.GetHealth()
			key = cacheKeyFromRequest(grafana.URL, GrafanaConfigFromContext(ctx).APIKey, nil, 0, r)
		}))
		req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
		req.Header.Set(header, callerToken)
		req.Header.Set(grafanaServiceAccountTokenHeader, "user-sa-token")
		req.Header.Set("X-Tenant", "t1")
		handler.ServeHTTP(httptest.NewRecorder(), req)
		return key
	}

	withBearer := run("Bearer " + token)
	raw := run(token)
	assert.Equal(t, withBearer, raw, "the caller header must not be part of the cache key")
	assert.NotContains(t, withBearer.String(), token)

	mu.Lock()
	defer mu.Unlock()
	require.NotEmpty(t, seen, "Grafana was never called")
	for _, h := range seen {
		assert.Empty(t, h.Values(header))
		for name, values := range h {
			for _, v := range values {
				assert.NotContains(t, v, token, "header %s", name)
			}
		}
	}
}

func TestForwardsHeader(t *testing.T) {
	t.Setenv(grafanaForwardHeadersEnvVar, " x-mcp-token , Cookie")
	assert.True(t, ForwardsHeader("X-Mcp-Token"))
	assert.True(t, ForwardsHeader("cookie"))
	assert.False(t, ForwardsHeader("Authorization"))
	assert.False(t, ForwardsAuthorizationHeader())
}
