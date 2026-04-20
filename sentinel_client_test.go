package mcpgrafana

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/grafana/incident-go"
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
		{"uppercase scheme is normalized by ParseRequestURI", "HTTP://host", false},

		// Invalid inputs.
		{"empty string", "", true},
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

func TestValidateHeaderURL(t *testing.T) {
	cases := []struct {
		name    string
		setHdr  bool
		value   string
		wantErr bool
	}{
		{"absent header (no Set call)", false, "", false},
		{"empty header", true, "", false},
		{"empty header with trailing slash", true, "/", false}, // TrimRight("/", "/") = ""
		{"valid http", true, "http://localhost:3000", false},
		{"valid https with trailing slash", true, "https://grafana.example.com/", false},
		{"valid with nested path", true, "https://example.com/subpath/api", false},
		{"malformed percent encoding", true, "http://%gg", true},
		{"non-http scheme", true, "file:///etc/passwd", true},
		{"no host", true, "http://", true},
		{"relative path", true, "/relative", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req, err := http.NewRequest(http.MethodGet, "http://example.com", nil)
			require.NoError(t, err)
			if tc.setHdr {
				req.Header.Set(grafanaURLHeader, tc.value)
			}
			got := validateHeaderURL(req)
			if tc.wantErr {
				require.Error(t, got)
				assert.True(t, errors.Is(got, ErrInvalidGrafanaURL),
					"error must wrap ErrInvalidGrafanaURL; got %v", got)
			} else {
				assert.NoError(t, got)
			}
		})
	}
}

func TestSentinelGrafanaClient_PublicURLIsEmpty(t *testing.T) {
	// navigation.go and proxied_tools.go read c.PublicURL or check gc != nil
	// patterns. The sentinel must present as non-nil with empty PublicURL so
	// conditional-access branches short-circuit cleanly rather than trying
	// to dereference a partially-populated struct.
	c := newSentinelGrafanaClient(errors.New("boom"))
	require.NotNil(t, c)
	assert.Empty(t, c.PublicURL,
		"sentinel PublicURL must be empty so `gc != nil && gc.PublicURL != \"\"` checks short-circuit")
}

func TestSentinelGrafanaClient_ReturnsError(t *testing.T) {
	t.Run("API method returns the configured error", func(t *testing.T) {
		want := errors.New("boom")
		c := newSentinelGrafanaClient(want)

		// Any sub-resource call should route through the sentinel transport.
		// GetDashboardByUID is representative: it's a simple GET that doesn't
		// depend on request-body serialization, so any error from Submit
		// surfaces directly as the returned error.
		_, err := c.Dashboards.GetDashboardByUID("any-uid")
		require.Error(t, err)
		assert.True(t, errors.Is(err, want),
			"sub-resource call must surface the sentinel error; got %v", err)
	})

	t.Run("nil err is substituted with ErrInvalidGrafanaURL", func(t *testing.T) {
		// Guards against a footgun: if a future caller passes nil, the
		// sentinel must still produce a detectable error rather than
		// silently returning (nil, nil) from every API call.
		c := newSentinelGrafanaClient(nil)
		_, err := c.Dashboards.GetDashboardByUID("any-uid")
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrInvalidGrafanaURL),
			"nil-err sentinel must substitute ErrInvalidGrafanaURL; got %v", err)
	})
}

func TestSentinelIncidentClient_ReturnsError(t *testing.T) {
	t.Run("HTTP request returns the configured error", func(t *testing.T) {
		want := errors.New("boom")
		c := newSentinelIncidentClient(want)

		// Exercise HTTPClient directly: the sentinel RoundTripper
		// short-circuits any request before it reaches a real server.
		req, err := http.NewRequest(http.MethodGet, "http://any.example/api/v1/x", nil)
		require.NoError(t, err)
		_, err = c.HTTPClient.Do(req)
		require.Error(t, err)
		assert.True(t, errors.Is(err, want),
			"HTTP call must surface the sentinel error; got %v", err)
	})

	t.Run("typed incident service method returns the configured error", func(t *testing.T) {
		// Mirror the pattern in tools/incident.go:20-21 — wrap the client
		// in incident.NewIncidentsService and call a typed method. Proves
		// the sentinel RoundTripper intercepts at the layer tool handlers
		// actually use, not just raw HTTPClient.Do.
		want := errors.New("boom")
		c := newSentinelIncidentClient(want)
		is := incident.NewIncidentsService(c)
		_, err := is.QueryIncidentPreviews(context.Background(),
			incident.QueryIncidentPreviewsRequest{
				Query: incident.IncidentPreviewsQuery{Limit: 10},
			})
		require.Error(t, err)
		assert.True(t, errors.Is(err, want),
			"typed incident service method must surface the sentinel error; got %v", err)
	})

	t.Run("nil err is substituted with ErrInvalidGrafanaURL", func(t *testing.T) {
		c := newSentinelIncidentClient(nil)
		req, err := http.NewRequest(http.MethodGet, "http://any.example/api/v1/x", nil)
		require.NoError(t, err)
		_, err = c.HTTPClient.Do(req)
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrInvalidGrafanaURL),
			"nil-err sentinel must substitute ErrInvalidGrafanaURL; got %v", err)
	})
}

func TestExtractGrafanaClientFromHeaders_InvalidURL(t *testing.T) {
	// extractKeyGrafanaInfoFromReq pulls GRAFANA_URL from env as a fallback;
	// neutralize that so a bad header is the only input.
	t.Setenv("GRAFANA_URL", "")
	t.Setenv("GRAFANA_SERVICE_ACCOUNT_TOKEN", "")

	cases := []struct {
		name   string
		header string
	}{
		{"malformed percent encoding", "http://%gg"},
		{"non-http scheme", "file:///etc/passwd"},
		{"empty host", "http://"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req, err := http.NewRequest(http.MethodGet, "http://example.com", nil)
			require.NoError(t, err)
			req.Header.Set(grafanaURLHeader, tc.header)

			ctx := ExtractGrafanaClientFromHeaders(context.Background(), req)
			c := GrafanaClientFromContext(ctx)
			require.NotNil(t, c, "client must be attached even on validation failure")

			// Sentinel surfaces ErrInvalidGrafanaURL from any API call.
			_, apiErr := c.Dashboards.GetDashboardByUID("any-uid")
			require.Error(t, apiErr)
			assert.True(t, errors.Is(apiErr, ErrInvalidGrafanaURL),
				"sentinel client must surface ErrInvalidGrafanaURL; got %v", apiErr)
		})
	}
}

func TestExtractGrafanaClientFromHeaders_ValidURL(t *testing.T) {
	// When the header is valid, the extractor must attach a real client,
	// not a sentinel. We can't easily invoke the real client without a
	// Grafana instance, but we can assert that the client is NOT returning
	// ErrInvalidGrafanaURL on a direct call (which would prove it's a
	// sentinel).
	t.Setenv("GRAFANA_URL", "")
	t.Setenv("GRAFANA_SERVICE_ACCOUNT_TOKEN", "test-token")

	// Use an httptest server so the request actually goes somewhere and
	// produces a real HTTP error we can distinguish from the sentinel error.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"meta":{},"dashboard":{}}`))
	}))
	defer srv.Close()

	req, err := http.NewRequest(http.MethodGet, "http://example.com", nil)
	require.NoError(t, err)
	req.Header.Set(grafanaURLHeader, srv.URL)

	ctx := ExtractGrafanaClientFromHeaders(context.Background(), req)
	c := GrafanaClientFromContext(ctx)
	require.NotNil(t, c)

	_, apiErr := c.Dashboards.GetDashboardByUID("any-uid")
	// Real client may succeed or return a parse/shape error from the canned
	// response, but must not return the sentinel error.
	if apiErr != nil {
		assert.False(t, errors.Is(apiErr, ErrInvalidGrafanaURL),
			"valid URL path must attach a real client, not a sentinel; got sentinel error")
	}
}

func TestExtractGrafanaClientFromHeaders_NoHeader(t *testing.T) {
	// No X-Grafana-URL header: falls back to env URL (defaultGrafanaURL if
	// env is empty). Must NOT be a sentinel.
	t.Setenv("GRAFANA_URL", "")
	t.Setenv("GRAFANA_SERVICE_ACCOUNT_TOKEN", "")

	req, err := http.NewRequest(http.MethodGet, "http://example.com", nil)
	require.NoError(t, err)

	ctx := ExtractGrafanaClientFromHeaders(context.Background(), req)
	c := GrafanaClientFromContext(ctx)
	require.NotNil(t, c)

	// Calling the real client will fail with a connection error
	// (defaultGrafanaURL points at localhost:3000), but not with the
	// sentinel error.
	_, apiErr := c.Dashboards.GetDashboardByUID("any-uid")
	if apiErr != nil {
		assert.False(t, errors.Is(apiErr, ErrInvalidGrafanaURL),
			"no-header path must attach a real client, not a sentinel; got sentinel error")
	}
}

func TestExtractIncidentClientFromHeaders_InvalidURL(t *testing.T) {
	t.Setenv("GRAFANA_URL", "")
	t.Setenv("GRAFANA_SERVICE_ACCOUNT_TOKEN", "")

	req, err := http.NewRequest(http.MethodGet, "http://example.com", nil)
	require.NoError(t, err)
	req.Header.Set(grafanaURLHeader, "http://%gg")

	ctx := ExtractIncidentClientFromHeaders(context.Background(), req)
	c := IncidentClientFromContext(ctx)
	require.NotNil(t, c, "incident client must be attached even on validation failure")

	apiReq, err := http.NewRequest(http.MethodGet, "http://any.example/api/v1/x", nil)
	require.NoError(t, err)
	_, err = c.HTTPClient.Do(apiReq)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrInvalidGrafanaURL),
		"sentinel incident client must surface ErrInvalidGrafanaURL; got %v", err)
}

func TestExtractGrafanaClientCached_DoesNotPoisonCacheWithSentinel(t *testing.T) {
	// A request with a malformed X-Grafana-URL must not create a cache
	// entry. Subsequent requests with a valid URL must get a real client,
	// not a cached sentinel.
	t.Setenv("GRAFANA_URL", "")
	t.Setenv("GRAFANA_SERVICE_ACCOUNT_TOKEN", "test-token")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"meta":{},"dashboard":{}}`))
	}))
	defer srv.Close()

	cache := NewClientCache()
	defer cache.Close()
	extractor := extractGrafanaClientCached(cache)

	// First: malformed URL.
	badReq, err := http.NewRequest(http.MethodGet, "http://example.com", nil)
	require.NoError(t, err)
	badReq.Header.Set(grafanaURLHeader, "http://%gg")
	badCtx := extractor(context.Background(), badReq)
	badClient := GrafanaClientFromContext(badCtx)
	require.NotNil(t, badClient)

	grafanaCount, _ := cache.Size()
	assert.Zero(t, grafanaCount, "sentinel must not be cached")

	// Then: valid URL.
	goodReq, err := http.NewRequest(http.MethodGet, "http://example.com", nil)
	require.NoError(t, err)
	goodReq.Header.Set(grafanaURLHeader, srv.URL)
	goodCtx := extractor(context.Background(), goodReq)
	goodClient := GrafanaClientFromContext(goodCtx)
	require.NotNil(t, goodClient)

	// Valid client must NOT be a sentinel.
	_, apiErr := goodClient.Dashboards.GetDashboardByUID("any-uid")
	if apiErr != nil {
		assert.False(t, errors.Is(apiErr, ErrInvalidGrafanaURL),
			"valid URL path must attach a real client after a cache miss from a bad-URL request; got sentinel error")
	}
}

func TestExtractGrafanaClientCached_ConcurrentMixedURLs(t *testing.T) {
	// Race-detector sanity check: N goroutines fire requests through the
	// cached extractor, alternating valid and invalid headers. No races,
	// no nil clients, sentinels on bad requests only.
	t.Setenv("GRAFANA_URL", "")
	t.Setenv("GRAFANA_SERVICE_ACCOUNT_TOKEN", "test-token")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cache := NewClientCache()
	defer cache.Close()
	extractor := extractGrafanaClientCached(cache)

	const n = 50
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		bad := i%2 == 0
		go func() {
			defer wg.Done()
			req, err := http.NewRequest(http.MethodGet, "http://example.com", nil)
			if err != nil {
				t.Errorf("new request: %v", err)
				return
			}
			if bad {
				req.Header.Set(grafanaURLHeader, "http://%gg")
			} else {
				req.Header.Set(grafanaURLHeader, srv.URL)
			}
			ctx := extractor(context.Background(), req)
			c := GrafanaClientFromContext(ctx)
			if c == nil {
				t.Errorf("nil client")
				return
			}
			_, apiErr := c.Dashboards.GetDashboardByUID("any-uid")
			if bad {
				if !errors.Is(apiErr, ErrInvalidGrafanaURL) {
					t.Errorf("bad URL path must return sentinel error, got %v", apiErr)
				}
			} else {
				if apiErr != nil && errors.Is(apiErr, ErrInvalidGrafanaURL) {
					t.Errorf("valid URL path must not return sentinel error")
				}
			}
		}()
	}
	wg.Wait()
}

func TestErrInvalidGrafanaURL_ActionableMessageText(t *testing.T) {
	// Lock in the error message text the MCP client eventually sees. This
	// is a regression guard: if someone rewords the hint, this test flags
	// it so we notice and decide intentionally.
	err := ValidateGrafanaURL("http://%gg")
	require.Error(t, err)
	msg := err.Error()
	assert.True(t, strings.Contains(msg, "invalid X-Grafana-URL header"),
		"error text must contain the sentinel prefix; got %q", msg)
	assert.True(t, strings.Contains(msg, "http://") || strings.Contains(msg, "https://"),
		"error text should include an actionable hint naming http(s); got %q", msg)
}
