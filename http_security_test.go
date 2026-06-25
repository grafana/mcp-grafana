package mcpgrafana

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// okHandler is the inner handler the middleware wraps. We assert that the
// middleware either calls it (allow) or short-circuits before it (deny).
func okHandler(t *testing.T) (http.Handler, *bool) {
	t.Helper()
	called := false
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}), &called
}

func TestDNSRebindingProtectionMiddleware_Host(t *testing.T) {
	cases := []struct {
		name       string
		allowed    []string
		host       string
		wantStatus int
		wantCalled bool
	}{
		{"matching host passes", []string{"localhost:8000"}, "localhost:8000", http.StatusOK, true},
		{"case-insensitive host match passes", []string{"localhost:8000"}, "LOCALHOST:8000", http.StatusOK, true},
		{"mismatched host blocked", []string{"localhost:8000"}, "evil.example:8000", http.StatusForbidden, false},
		{"loopback IP variant blocked when not allowlisted", []string{"localhost:8000"}, "127.0.0.1:8000", http.StatusForbidden, false},
		{"empty allowlist permits everything (Host check disabled)", nil, "anything.example", http.StatusOK, true},
		{"wildcard disables host validation", []string{"*"}, "rebinding.attacker.example", http.StatusOK, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			inner, called := okHandler(t)
			mw := DNSRebindingProtectionMiddleware(HostOriginPolicy{AllowedHosts: tc.allowed})

			req := httptest.NewRequest(http.MethodGet, "/sse", nil)
			req.Host = tc.host
			rr := httptest.NewRecorder()
			mw(inner).ServeHTTP(rr, req)

			assert.Equal(t, tc.wantStatus, rr.Code)
			assert.Equal(t, tc.wantCalled, *called)
		})
	}
}

func TestDNSRebindingProtectionMiddleware_Origin(t *testing.T) {
	cases := []struct {
		name       string
		allowed    []string
		origin     string
		wantStatus int
		wantCalled bool
	}{
		{"no Origin header passes (CLI client)", nil, "", http.StatusOK, true},
		{"empty allowlist rejects any Origin", nil, "http://evil.example", http.StatusForbidden, false},
		{"matching Origin passes", []string{"http://localhost:3000"}, "http://localhost:3000", http.StatusOK, true},
		{"case-insensitive Origin match passes", []string{"http://localhost:3000"}, "HTTP://LOCALHOST:3000", http.StatusOK, true},
		{"non-matching Origin blocked", []string{"http://localhost:3000"}, "http://evil.example", http.StatusForbidden, false},
		{"wildcard disables Origin validation even with Origin present", []string{"*"}, "http://evil.example", http.StatusOK, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			inner, called := okHandler(t)
			// AllowedHosts is left empty so only Origin is exercised here.
			mw := DNSRebindingProtectionMiddleware(HostOriginPolicy{AllowedOrigins: tc.allowed})

			req := httptest.NewRequest(http.MethodGet, "/sse", nil)
			if tc.origin != "" {
				req.Header.Set("Origin", tc.origin)
			}
			rr := httptest.NewRecorder()
			mw(inner).ServeHTTP(rr, req)

			assert.Equal(t, tc.wantStatus, rr.Code)
			assert.Equal(t, tc.wantCalled, *called)
		})
	}
}

// TestDNSRebindingProtectionMiddleware_RebindingScenario simulates the exact
// DNS-rebinding case from the customer report: a browser hits 127.0.0.1:8000
// but the URL bar (and therefore the Host header) is the attacker's domain.
// With the default Host allowlist derived from the bind address, the request
// must be rejected before reaching the SSE handler.
func TestDNSRebindingProtectionMiddleware_RebindingScenario(t *testing.T) {
	inner, called := okHandler(t)
	policy := HostOriginPolicy{AllowedHosts: DefaultAllowedHosts("localhost:8000")}
	mw := DNSRebindingProtectionMiddleware(policy)

	req := httptest.NewRequest(http.MethodGet, "/sse", nil)
	req.Host = "rebinding.attacker.example:8000"
	req.Header.Set("Origin", "http://rebinding.attacker.example:8000")

	rr := httptest.NewRecorder()
	mw(inner).ServeHTTP(rr, req)

	assert.Equal(t, http.StatusForbidden, rr.Code)
	assert.False(t, *called, "inner handler must not be reached for a rebinding Host")
}

func TestDefaultAllowedHosts(t *testing.T) {
	cases := []struct {
		name    string
		address string
		want    []string
	}{
		{
			name:    "localhost bind allows IPv4 and IPv6 loopback too",
			address: "localhost:8000",
			want:    []string{"localhost:8000", "127.0.0.1:8000", "[::1]:8000"},
		},
		{
			name:    "wildcard IPv4 bind allows all loopback variants",
			address: "0.0.0.0:8000",
			want:    []string{"localhost:8000", "127.0.0.1:8000", "[::1]:8000"},
		},
		{
			name:    "empty host bind allows all loopback variants",
			address: ":8000",
			want:    []string{"localhost:8000", "127.0.0.1:8000", "[::1]:8000"},
		},
		{
			name:    "explicit hostname binds only that hostname",
			address: "mcp.internal:8000",
			want:    []string{"mcp.internal:8000"},
		},
		{
			name:    "explicit IPv4 binds only that IPv4",
			address: "10.0.0.5:8000",
			want:    []string{"10.0.0.5:8000"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := DefaultAllowedHosts(tc.address)
			require.Equal(t, tc.want, got)
		})
	}
}
