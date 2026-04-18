package main

import (
	"context"
	"log/slog"
	"testing"
	"testing/synctest"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
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
