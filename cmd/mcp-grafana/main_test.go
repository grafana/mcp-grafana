package main

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
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
		dt := disabledTools{enabledTools: "search"}
		_, _, sm := newServer("stdio", dt, obs, 0, dt.buildInstructions())
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

func TestApplyServerInstructions(t *testing.T) {
	generated := "generated instructions"
	custom := "custom instructions\nwith multiple lines"
	customFile := filepath.Join(t.TempDir(), "instructions.md")
	require.NoError(t, os.WriteFile(customFile, []byte(custom), 0o600))
	emptyFile := filepath.Join(t.TempDir(), "empty.md")
	require.NoError(t, os.WriteFile(emptyFile, nil, 0o600))
	missingFile := filepath.Join(t.TempDir(), "missing.md")

	tests := []struct {
		name          string
		filePath      string
		mode          serverInstructionsMode
		want          string
		wantErrSubstr string
	}{
		{
			name: "no file returns generated instructions unchanged",
			want: generated,
		},
		{
			name:     "empty mode defaults to append",
			filePath: customFile,
			want:     generated + "\n\n" + custom,
		},
		{
			name:     "append mode appends custom instructions",
			filePath: customFile,
			mode:     serverInstructionsModeAppend,
			want:     generated + "\n\n" + custom,
		},
		{
			name:     "replace mode returns custom instructions only",
			filePath: customFile,
			mode:     serverInstructionsModeReplace,
			want:     custom,
		},
		{
			name:     "empty file is valid in append mode",
			filePath: emptyFile,
			mode:     serverInstructionsModeAppend,
			want:     generated,
		},
		{
			name:     "empty file is valid in replace mode",
			filePath: emptyFile,
			mode:     serverInstructionsModeReplace,
			want:     "",
		},
		{
			name:          "missing file returns error",
			filePath:      missingFile,
			mode:          serverInstructionsModeAppend,
			wantErrSubstr: "read server instructions file",
		},
		{
			name:          "invalid mode returns error",
			filePath:      customFile,
			mode:          serverInstructionsMode("overwrite"),
			wantErrSubstr: "invalid server instructions mode",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := applyServerInstructions(generated, tc.filePath, tc.mode)
			if tc.wantErrSubstr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.wantErrSubstr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestNewServer_SessionIdleTimeoutCustomValue(t *testing.T) {
	obs := newTestObservability(t)
	synctest.Test(t, func(t *testing.T) {
		dt := disabledTools{enabledTools: "search"}
		_, _, sm := newServer("stdio", dt, obs, 1, dt.buildInstructions())
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
		wantMode      serverInstructionsMode
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
			name:         "--version wins over bad instructions mode",
			showVersion:  true,
			slowLevelStr: "warn",
			wantAction:   flagActionVersion,
		},
		{
			name:         "no --version, warn slow-level",
			showVersion:  false,
			slowLevelStr: "warn",
			wantAction:   flagActionContinue,
			wantLevel:    slog.LevelWarn,
			wantMode:     serverInstructionsModeAppend,
		},
		{
			name:         "no --version, info slow-level",
			showVersion:  false,
			slowLevelStr: "info",
			wantAction:   flagActionContinue,
			wantLevel:    slog.LevelInfo,
			wantMode:     serverInstructionsModeAppend,
		},
		{
			name:         "replace server instructions mode",
			showVersion:  false,
			slowLevelStr: "warn",
			wantAction:   flagActionContinue,
			wantLevel:    slog.LevelWarn,
			wantMode:     serverInstructionsModeReplace,
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
		{
			name:          "no --version, bogus server instructions mode",
			showVersion:   false,
			slowLevelStr:  "warn",
			wantAction:    flagActionInvalidServerInstructionsMode,
			wantErr:       true,
			wantErrSubstr: []string{"append", "replace", "bogus"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			instructionsModeStr := ""
			if tc.name == "replace server instructions mode" {
				instructionsModeStr = "replace"
			}
			if tc.name == "no --version, bogus server instructions mode" || tc.name == "--version wins over bad instructions mode" {
				instructionsModeStr = "bogus"
			}

			action, level, mode, err := handleFlagsPostParse(tc.showVersion, tc.slowLevelStr, instructionsModeStr)
			assert.Equal(t, tc.wantAction, action, "unexpected action")
			if tc.wantAction == flagActionContinue {
				assert.Equal(t, tc.wantLevel, level, "unexpected level")
				assert.Equal(t, tc.wantMode, mode, "unexpected server instructions mode")
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
