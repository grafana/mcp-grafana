package mcpgrafana

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateGrafanaURL(t *testing.T) {
	cases := []struct {
		name    string
		input   string
		wantErr bool
	}{
		// Valid inputs.
		{"http with host+port", "http://localhost:3000", false},
		{"https with host", "https://grafana.example.com", false},
		{"https with host and path", "https://grafana.example.com/subpath", false},
		{"http with port and path", "http://host:8000/api/mcp", false},
		// url.ParseRequestURI lowercases the scheme, so the scheme-allow-list
		// check below (pu.Scheme != "http" && != "https") accepts uppercase
		// input without needing a separate case-fold. Documented so a future
		// refactor that adds a strings.ToLower doesn't accidentally break
		// this invariant.
		{"uppercase scheme normalized by ParseRequestURI", "HTTP://host", false},

		// Trim behavior (H1 trim consolidation).
		{"http with single trailing slash", "http://grafana.example/", false},
		{"http with multiple trailing slashes", "http://grafana.example///", false},

		// Invalid inputs.
		{"empty string", "", true},
		{"slash-only trims to empty", "/", true},
		{"plain text", "not a url", true},
		{"invalid percent encoding", "http://%gg", true},
		{"javascript scheme", "javascript:alert(1)", true},
		{"file scheme", "file:///etc/passwd", true},
		{"ftp scheme", "ftp://example.com", true},
		{"scheme-relative", "//no-scheme.example.com", true},
		{"relative path", "/relative/path", true},
		{"http with empty host", "http://", true},
		{"http with triple-slash and no host", "http:///path", true},
		{"https with empty host", "https://", true},
		{"control byte in URL", "http://host\x01", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ValidateGrafanaURL(tc.input)
			if tc.wantErr {
				require.Error(t, got, "expected an error for input %q", tc.input)
				assert.True(t, errors.Is(got, ErrInvalidGrafanaURL),
					"error must wrap ErrInvalidGrafanaURL for input %q; got %v", tc.input, got)
			} else {
				assert.NoError(t, got, "expected no error for input %q", tc.input)
			}
		})
	}
}

func TestValidateGrafanaURLMiddleware(t *testing.T) {
	cases := []struct {
		name       string
		setHeader  bool
		headerVal  string
		wantStatus int
		wantCalled bool
	}{
		{"absent header passes through", false, "", http.StatusOK, true},
		{"valid http header passes", true, "http://grafana.example", http.StatusOK, true},
		{"valid https header passes", true, "https://grafana.example.com", http.StatusOK, true},
		{"valid with trailing slash passes", true, "https://grafana.example.com/", http.StatusOK, true},
		{"malformed percent encoding rejected", true, "http://%gg", http.StatusBadRequest, false},
		{"javascript scheme rejected", true, "javascript:alert(1)", http.StatusBadRequest, false},
		{"relative path rejected", true, "/relative", http.StatusBadRequest, false},
		{"trim-to-empty slash rejected", true, "/", http.StatusBadRequest, false},
		{"empty host rejected", true, "http://", http.StatusBadRequest, false},
		{"CR-LF injection attempt rejected", true, "http://foo\r\nX-Injected: 1", http.StatusBadRequest, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			called := false
			next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				called = true
				w.WriteHeader(http.StatusOK)
			})

			req := httptest.NewRequest(http.MethodGet, "/", nil)
			if tc.setHeader {
				req.Header.Set(grafanaURLHeader, tc.headerVal)
			}
			rec := httptest.NewRecorder()
			ValidateGrafanaURLMiddleware(next).ServeHTTP(rec, req)

			assert.Equal(t, tc.wantStatus, rec.Code, "unexpected status code")
			assert.Equal(t, tc.wantCalled, called, "next handler call state mismatch")
			if tc.wantStatus == http.StatusBadRequest {
				assert.Contains(t, rec.Body.String(), "invalid X-Grafana-URL",
					"rejection body must include the operator-visible error signal")
			}
		})
	}

	t.Run("duplicate header - first value is validated", func(t *testing.T) {
		// Header.Get returns the first value. A caller that sends two
		// X-Grafana-URL headers gets validated on the first; the second is
		// ignored by standard Go http header semantics. Documented so a
		// future reader knows the behavior is intentional, not accidental.
		//
		// Use Header.Add (not the raw map) so the key is stored under its
		// canonical form (textproto.CanonicalMIMEHeaderKey). Raw map
		// assignment with the non-canonical "X-Grafana-URL" key would leave
		// the header invisible to Header.Get (which canonicalizes its
		// lookup), and this test would pass for the wrong reason.
		called := false
		next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			called = true
			w.WriteHeader(http.StatusOK)
		})
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Add(grafanaURLHeader, "http://ok.example")
		req.Header.Add(grafanaURLHeader, "javascript:alert(1)")
		rec := httptest.NewRecorder()
		ValidateGrafanaURLMiddleware(next).ServeHTTP(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code)
		assert.True(t, called)
	})

	t.Run("concurrent mixed under -race", func(t *testing.T) {
		// Middleware is stateless by construction — no data race should
		// surface. This test runs 20 concurrent requests through one
		// middleware instance under go test -race to catch any accidental
		// shared state introduced during review or future refactoring.
		next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		})
		mw := ValidateGrafanaURLMiddleware(next)

		mixed := []struct {
			header     string
			wantStatus int
		}{
			{"http://ok1.example", http.StatusOK},
			{"http://%gg", http.StatusBadRequest},
			{"https://ok2.example", http.StatusOK},
			{"javascript:alert(1)", http.StatusBadRequest},
			{"http://ok3.example:8000", http.StatusOK},
			{"/", http.StatusBadRequest},
			{"https://ok4.example/path", http.StatusOK},
			{"file:///etc/passwd", http.StatusBadRequest},
			{"http://ok5.example", http.StatusOK},
			{"http://", http.StatusBadRequest},
		}

		var wg sync.WaitGroup
		errs := make(chan string, 20)
		for i := 0; i < 20; i++ {
			tc := mixed[i%len(mixed)]
			wg.Add(1)
			go func(header string, wantStatus int) {
				defer wg.Done()
				req := httptest.NewRequest(http.MethodGet, "/", nil)
				req.Header.Set(grafanaURLHeader, header)
				rec := httptest.NewRecorder()
				mw.ServeHTTP(rec, req)
				if rec.Code != wantStatus {
					errs <- "header " + header + ": got " + http.StatusText(rec.Code) + ", want " + http.StatusText(wantStatus)
				}
			}(tc.header, tc.wantStatus)
		}
		wg.Wait()
		close(errs)
		for e := range errs {
			t.Error(e)
		}
	})
}

// Smoke coverage for ExtractGrafanaClientFromHeaders client construction.
// These two tests exercise the extractor end-to-end (header parsing -> client
// wiring -> real HTTP call against a test server) without depending on
// sentinel-specific machinery.

func TestExtractGrafanaClientFromHeaders_ValidURL(t *testing.T) {
	t.Setenv("GRAFANA_URL", "")
	t.Setenv("GRAFANA_SERVICE_ACCOUNT_TOKEN", "test-token")

	// hitCount tracks that the extractor wired a client pointed at THIS test
	// server (not defaultGrafanaURL or anything else). A request counter is
	// the cheapest durable assertion that the header-URL actually flowed
	// through to client construction.
	var hitCount int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&hitCount, 1)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"meta":{},"dashboard":{}}`))
	}))
	defer srv.Close()

	req, err := http.NewRequest(http.MethodGet, "http://example.com", nil)
	require.NoError(t, err)
	req.Header.Set(grafanaURLHeader, srv.URL)

	ctx := ExtractGrafanaClientFromHeaders(context.Background(), req)
	c := GrafanaClientFromContext(ctx)
	require.NotNil(t, c, "extractor must attach a client when header URL is valid")

	_, apiErr := c.Dashboards.GetDashboardByUID("any-uid")
	// The call reaches the test server, which returns skeletal JSON. The
	// openapi client may succeed or return a schema-mismatch error; either
	// is acceptable. What's NOT acceptable is a URL-parse error, which
	// would indicate the extractor wired garbage instead of the header URL.
	if apiErr != nil {
		assert.NotContains(t, apiErr.Error(), "parse",
			"valid-header path must not produce a URL-parse error; got %v", apiErr)
	}
	assert.Greater(t, int(atomic.LoadInt32(&hitCount)), 0,
		"extractor must wire a client that actually reaches the header-supplied URL")
}

func TestExtractGrafanaClientFromHeaders_NoHeader(t *testing.T) {
	// No X-Grafana-URL header: extractor falls back to env, and env is empty
	// so defaultGrafanaURL (http://localhost:3000) applies. Nothing is
	// listening on :3000 during tests, so the client call MUST fail with a
	// connection-level error (proving defaultGrafanaURL was used) and MUST
	// NOT fail with a URL-parse error (which would mean the extractor
	// produced garbage).
	t.Setenv("GRAFANA_URL", "")
	t.Setenv("GRAFANA_SERVICE_ACCOUNT_TOKEN", "")

	req, err := http.NewRequest(http.MethodGet, "http://example.com", nil)
	require.NoError(t, err)

	ctx := ExtractGrafanaClientFromHeaders(context.Background(), req)
	c := GrafanaClientFromContext(ctx)
	require.NotNil(t, c, "extractor must attach a client even with no header")

	_, apiErr := c.Dashboards.GetDashboardByUID("any-uid")
	require.Error(t, apiErr,
		"no-header path should fall back to defaultGrafanaURL and fail to connect")
	assert.NotContains(t, apiErr.Error(), "parse",
		"failure must be connection-level, not a URL-parse error; got %v", apiErr)
}
