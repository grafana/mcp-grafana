package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/grafana/mcp-grafana/observability"
)

// testClientSession implements server.ClientSession for unit tests.
type testClientSession struct {
	id string
}

func (s *testClientSession) SessionID() string                                   { return s.id }
func (s *testClientSession) NotificationChannel() chan<- mcp.JSONRPCNotification { return nil }
func (s *testClientSession) Initialize()                                         {}
func (s *testClientSession) Initialized() bool                                   { return true }

func newTestObservability(t *testing.T) *observability.Observability {
	t.Helper()
	obs, err := observability.Setup(observability.Config{})
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = obs.Shutdown(context.Background())
	})
	return obs
}

func TestNewServer_SessionIdleTimeoutZeroDisablesReaping(t *testing.T) {
	obs := newTestObservability(t)
	synctest.Test(t, func(t *testing.T) {
		_, _, sm := newServer("stdio", disabledTools{enabledTools: "search"}, obs, 0)
		defer sm.Close()

		session := &testClientSession{id: "should-persist"}
		sm.CreateSession(context.Background(), session)

		// Advance the fake clock well beyond any reasonable reaper interval.
		// With reaper disabled (TTL=0), the session must survive.
		time.Sleep(time.Hour)

		_, exists := sm.GetSession("should-persist")
		assert.True(t, exists, "Session should persist when idle timeout is 0 (reaper disabled)")
	})
}

func TestBuildInstructions_ReflectsEnabledCategories(t *testing.T) {
	tests := []struct {
		name            string
		enabledTools    string
		disableFlags    map[string]bool
		wantContains    []string
		wantNotContains []string
	}{
		{
			name:         "all defaults include Loki and Prometheus",
			enabledTools: "search,datasource,incident,prometheus,loki,alerting,dashboard,folder,oncall,asserts,sift,pyroscope,navigation,annotations,rendering",
			wantContains: []string{
				"Prometheus:",
				"Loki:",
				"Alerting:",
				"Available Capabilities:",
			},
			wantNotContains: []string{
				"ClickHouse:",
				"No tool categories are currently enabled.",
			},
		},
		{
			name:         "disabled category excluded from instructions",
			enabledTools: "search,datasource,prometheus,loki",
			disableFlags: map[string]bool{"loki": true},
			wantContains: []string{
				"Prometheus:",
			},
			wantNotContains: []string{
				"Loki:",
			},
		},
		{
			name:         "category not in enabled list excluded",
			enabledTools: "search,datasource",
			wantContains: []string{
				"Search:",
				"Datasources:",
			},
			wantNotContains: []string{
				"Prometheus:",
				"Loki:",
				"Alerting:",
			},
		},
		{
			name:         "empty enabled list shows no capabilities",
			enabledTools: "",
			disableFlags: map[string]bool{"proxied": true},
			wantContains: []string{
				"No tool categories are currently enabled.",
			},
			wantNotContains: []string{
				"Available Capabilities:",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dt := disabledTools{enabledTools: tc.enabledTools}
			if tc.disableFlags != nil {
				if tc.disableFlags["loki"] {
					dt.loki = true
				}
				if tc.disableFlags["prometheus"] {
					dt.prometheus = true
				}
				if tc.disableFlags["proxied"] {
					dt.proxied = true
				}
			}

			instructions := dt.buildInstructions()

			for _, want := range tc.wantContains {
				assert.Contains(t, instructions, want, "instructions should contain %q", want)
			}
			for _, notWant := range tc.wantNotContains {
				assert.NotContains(t, instructions, notWant, "instructions should not contain %q", notWant)
			}
		})
	}
}

func TestBuildInstructions_TimestampNote(t *testing.T) {
	// The timestamp note should always be present regardless of enabled categories.
	dt := disabledTools{enabledTools: "search"}
	instructions := dt.buildInstructions()
	assert.Contains(t, instructions, "Timestamp parameters without a timezone offset are interpreted as UTC")
}

func TestNewServer_SessionIdleTimeoutCustomValue(t *testing.T) {
	obs := newTestObservability(t)
	synctest.Test(t, func(t *testing.T) {
		_, _, sm := newServer("stdio", disabledTools{enabledTools: "search"}, obs, 1)
		defer sm.Close()

		session := &testClientSession{id: "custom-ttl"}
		sm.CreateSession(context.Background(), session)

		// Advance the fake clock past the 1-minute TTL.
		// The reaper runs every TTL/2 (30s), so by 2 minutes
		// it will have fired and reaped the idle session.
		time.Sleep(2 * time.Minute)

		_, exists := sm.GetSession("custom-ttl")
		assert.False(t, exists, "Session should be reaped after exceeding the 1-minute idle timeout")
	})
}

func TestParseSlowRequestLogLevel(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantLevel slog.Level
		wantErr   bool
	}{
		{name: "lowercase info", input: "info", wantLevel: slog.LevelInfo},
		{name: "lowercase warn", input: "warn", wantLevel: slog.LevelWarn},
		{name: "uppercase INFO", input: "INFO", wantLevel: slog.LevelInfo},
		{name: "mixed case Warn", input: "Warn", wantLevel: slog.LevelWarn},
		{name: "empty string rejected", input: "", wantErr: true},
		{name: "debug rejected", input: "debug", wantErr: true},
		{name: "error rejected", input: "error", wantErr: true},
		{name: "typo rejected", input: "wurn", wantErr: true},
		// Documents intentional strictness: no whitespace trimming. CLI
		// usage won't hit this, but env-var or config-file plumbing that
		// carries trailing/leading whitespace must fail-fast, not silently
		// round-trip through ToLower into a default.
		{name: "whitespace not trimmed", input: " info", wantErr: true},
		{name: "trailing newline not trimmed", input: "warn\n", wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseSlowRequestLogLevel(tc.input)
			if tc.wantErr {
				require.Error(t, err, "expected error for input %q", tc.input)
				return
			}
			require.NoError(t, err, "unexpected error for input %q", tc.input)
			assert.Equal(t, tc.wantLevel, got, "unexpected level for input %q", tc.input)
		})
	}
}

func TestVersionOutput(t *testing.T) {
	t.Run("without ldflags returns non-empty version", func(t *testing.T) {
		bin := t.TempDir() + "/mcp-grafana"
		build := exec.Command("go", "build", "-o", bin, ".")
		out, err := build.CombinedOutput()
		require.NoError(t, err, "go build failed: %s", out)

		got, err := exec.Command(bin, "--version").Output()
		require.NoError(t, err)
		assert.NotEmpty(t, strings.TrimSpace(string(got)))
	})

	t.Run("ldflags version takes precedence", func(t *testing.T) {
		bin := t.TempDir() + "/mcp-grafana"
		build := exec.Command("go", "build", "-ldflags", "-X github.com/grafana/mcp-grafana.version=v1.2.3", "-o", bin, ".")
		out, err := build.CombinedOutput()
		require.NoError(t, err, "go build failed: %s", out)

		got, err := exec.Command(bin, "--version").Output()
		require.NoError(t, err)
		assert.Equal(t, "v1.2.3", strings.TrimSpace(string(got)))
	})
}

// TestHandleFlagsPostParse locks in the precedence invariant that --version
// short-circuits before --slow-request-log-level validation. Regression guard
// for the Bugbot finding on the initial #756 revision where
// `./mcp-grafana --version --slow-request-log-level=bogus` exited 2 instead
// of printing the version.
func TestHandleFlagsPostParse(t *testing.T) {
	tests := []struct {
		name          string
		showVersion   bool
		slowLevelStr  string
		wantAction    flagAction
		wantLevel     slog.Level
		wantErr       bool
		wantErrSubstr []string
	}{
		{
			name:         "bare --version",
			showVersion:  true,
			slowLevelStr: "warn",
			wantAction:   flagActionVersion,
		},
		{
			// The regression guard. --version must print regardless of other
			// flags' values, even when --slow-request-log-level would fail
			// validation on its own.
			name:         "--version wins over bad slow-level",
			showVersion:  true,
			slowLevelStr: "bogus",
			wantAction:   flagActionVersion,
		},
		{
			name:         "no --version, warn slow-level",
			showVersion:  false,
			slowLevelStr: "warn",
			wantAction:   flagActionContinue,
			wantLevel:    slog.LevelWarn,
		},
		{
			name:         "no --version, info slow-level",
			showVersion:  false,
			slowLevelStr: "info",
			wantAction:   flagActionContinue,
			wantLevel:    slog.LevelInfo,
		},
		{
			name:          "no --version, bogus slow-level",
			showVersion:   false,
			slowLevelStr:  "bogus",
			wantAction:    flagActionInvalidSlowLevel,
			wantErr:       true,
			wantErrSubstr: []string{"must be", "bogus"},
		},
		{
			name:          "no --version, empty slow-level",
			showVersion:   false,
			slowLevelStr:  "",
			wantAction:    flagActionInvalidSlowLevel,
			wantErr:       true,
			wantErrSubstr: []string{"must be"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			action, level, err := handleFlagsPostParse(tc.showVersion, tc.slowLevelStr)
			assert.Equal(t, tc.wantAction, action, "unexpected action")
			if tc.wantAction == flagActionContinue {
				assert.Equal(t, tc.wantLevel, level, "unexpected level")
			}
			if tc.wantErr {
				require.Error(t, err, "expected an error")
				for _, sub := range tc.wantErrSubstr {
					assert.Contains(t, err.Error(), sub,
						"error message should contain %q; got %q", sub, err.Error())
				}
			} else {
				assert.NoError(t, err, "expected no error")
			}
		})
	}
}

func TestSplitAndTrim(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []string
	}{
		{"empty string", "", nil},
		{"single value", "a", []string{"a"}},
		{"comma separated", "a,b,c", []string{"a", "b", "c"}},
		{"whitespace trimmed", " a , b , c ", []string{"a", "b", "c"}},
		{"empty entries skipped", "a,,b, ,c", []string{"a", "b", "c"}},
		{"only commas yields nil", ",,, , ,", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, splitAndTrim(tc.in))
		})
	}
}

func TestHTTPSecurityConfigPolicy(t *testing.T) {
	cases := []struct {
		name           string
		allowedHosts   string
		allowedOrigins string
		address        string
		wantHosts      []string
		wantOrigins    []string
	}{
		{
			name:        "unset --allowed-hosts falls back to defaults",
			address:     "localhost:8000",
			wantHosts:   []string{"localhost:8000", "127.0.0.1:8000", "[::1]:8000"},
			wantOrigins: nil,
		},
		{
			// Regression guard: a malformed value that splits to empty must
			// NOT silently disable Host validation.
			name:         "comma-only --allowed-hosts falls back to defaults",
			allowedHosts: ",,, ,",
			address:      "localhost:8000",
			wantHosts:    []string{"localhost:8000", "127.0.0.1:8000", "[::1]:8000"},
			wantOrigins:  nil,
		},
		{
			name:         "explicit --allowed-hosts overrides defaults",
			allowedHosts: "mcp.example:8000, other.example:8000",
			address:      "localhost:8000",
			wantHosts:    []string{"mcp.example:8000", "other.example:8000"},
		},
		{
			name:           "origins pass through",
			allowedOrigins: "https://app.example",
			address:        "localhost:8000",
			wantHosts:      []string{"localhost:8000", "127.0.0.1:8000", "[::1]:8000"},
			wantOrigins:    []string{"https://app.example"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hsc := httpSecurityConfig{allowedHosts: tc.allowedHosts, allowedOrigins: tc.allowedOrigins}
			got := hsc.policy(tc.address)
			assert.Equal(t, tc.wantHosts, got.AllowedHosts)
			assert.Equal(t, tc.wantOrigins, got.AllowedOrigins)
		})
	}
}

// TestSSEServerSuppressesWildcardCORS pins the load-bearing assumption behind
// corsOrigins(): that passing any non-empty AllowedOrigins through
// WithSSECORS makes mcp-go's corsConfig.enabled() return true, suppressing
// the historical Access-Control-Allow-Origin: * default on /sse.
//
// The control sub-test boots an SSE server without our opt-in and asserts the
// wildcard IS emitted, documenting the regression scenario. If a future
// mcp-go bump removes the historical default, the control fails and we know
// the sentinel workaround can be removed.
func TestSSEServerSuppressesWildcardCORS(t *testing.T) {
	hitSSE := func(t *testing.T, opts ...server.SSEOption) http.Header {
		t.Helper()
		mcpServer := server.NewMCPServer("test", "0")
		sse := server.NewSSEServer(mcpServer, opts...)
		ts := httptest.NewServer(sse)
		t.Cleanup(ts.Close)

		// Abort as soon as we have headers — SSE keeps the stream open.
		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/sse", nil)
		require.NoError(t, err)
		resp, err := http.DefaultClient.Do(req)
		if err != nil && !errors.Is(err, context.DeadlineExceeded) {
			require.NoError(t, err)
		}
		require.NotNil(t, resp)
		t.Cleanup(func() { _ = resp.Body.Close() })
		return resp.Header
	}

	t.Run("control: mcp-go emits the wildcard by default", func(t *testing.T) {
		h := hitSSE(t)
		assert.Equal(t, "*", h.Get("Access-Control-Allow-Origin"),
			"mcp-go's historical default changed — sentinel workaround in corsOrigins() may be removable")
	})

	t.Run("opt-in via corsOrigins sentinel suppresses the wildcard", func(t *testing.T) {
		hsc := httpSecurityConfig{}
		h := hitSSE(t, server.WithSSECORS(server.WithCORSAllowedOrigins(hsc.corsOrigins()...)))
		assert.Empty(t, h.Get("Access-Control-Allow-Origin"),
			"sentinel did not suppress wildcard — mcp-go CORS contract may have changed")
	})
}

func TestHTTPSecurityConfigCORSOrigins(t *testing.T) {
	cases := []struct {
		name           string
		allowedOrigins string
		want           []string
	}{
		{
			// The sentinel keeps mcp-go's corsConfig.enabled() true so its
			// SSE default of Access-Control-Allow-Origin: * is suppressed.
			name: "unset returns the .invalid sentinel",
			want: []string{"https://mcp-grafana.invalid"},
		},
		{
			name:           "comma-only returns the sentinel",
			allowedOrigins: ", ,",
			want:           []string{"https://mcp-grafana.invalid"},
		},
		{
			name:           "explicit origins pass through lowercased",
			allowedOrigins: "HTTPS://App.Example, https://other.example",
			want:           []string{"https://app.example", "https://other.example"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hsc := httpSecurityConfig{allowedOrigins: tc.allowedOrigins}
			assert.Equal(t, tc.want, hsc.corsOrigins())
		})
	}
}
