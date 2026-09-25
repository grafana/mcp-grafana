package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	mcpgrafana "github.com/grafana/mcp-grafana/v2"
	"github.com/grafana/mcp-grafana/v2/observability"
	"github.com/grafana/mcp-grafana/v2/usagestats"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestObservability(t *testing.T) *observability.Observability {
	t.Helper()
	obs, err := observability.Setup(observability.Config{})
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = obs.Shutdown(context.Background())
	})
	return obs
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
			enabledTools: "search,datasource,incident,prometheus,loki,alerting,dashboard,folder,oncall,asserts,pyroscope,navigation,annotations,rendering",
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
			disableFlags: map[string]bool{"tempo": true},
			wantContains: []string{
				"No tool categories are currently enabled.",
			},
			wantNotContains: []string{
				"Available Capabilities:",
			},
		},
		{
			name:         "agento11y excluded unless opted in",
			enabledTools: "search,datasource,incident,prometheus,loki,alerting,dashboard,folder,oncall,asserts,pyroscope,navigation,tempo,annotations,rendering,plugin,api,config,provisioning",
			wantContains: []string{
				"Search:",
			},
			wantNotContains: []string{
				"Agent Observability:",
			},
		},
		{
			name:         "agento11y included when opted in",
			enabledTools: "search,agento11y",
			wantContains: []string{
				"Agent Observability:",
			},
		},
		{
			name:         "agento11y disable flag overrides enabled list",
			enabledTools: "search,agento11y",
			disableFlags: map[string]bool{"agento11y": true},
			wantContains: []string{
				"Search:",
			},
			wantNotContains: []string{
				"Agent Observability:",
			},
		},
		{
			name:         "assistant excluded unless opted in",
			enabledTools: "search,datasource,incident,prometheus,loki,alerting,dashboard,folder,oncall,asserts,pyroscope,navigation,tempo,annotations,rendering,plugin,api,config,provisioning",
			wantContains: []string{
				"Search:",
			},
			wantNotContains: []string{
				"Assistant:",
			},
		},
		{
			name:         "assistant included when opted in",
			enabledTools: "search,assistant",
			wantContains: []string{
				"Assistant:",
			},
		},
		{
			name:         "assistant disable flag overrides enabled list",
			enabledTools: "search,assistant",
			disableFlags: map[string]bool{"assistant": true},
			wantContains: []string{
				"Search:",
			},
			wantNotContains: []string{
				"Assistant:",
			},
		},
		{
			name:         "query-only categories excluded when query disabled",
			enabledTools: "search,elasticsearch,quickwit,influxdb,runpanelquery",
			disableFlags: map[string]bool{"query": true},
			wantContains: []string{
				"Search:",
			},
			wantNotContains: []string{
				"Elasticsearch and OpenSearch:",
				"Quickwit:",
				"InfluxDB:",
				"Run Panel Query:",
			},
		},
		{
			name:         "partially gated categories describe what remains when query disabled",
			enabledTools: "prometheus,loki,sql",
			disableFlags: map[string]bool{"query": true},
			wantContains: []string{
				"Prometheus: Retrieve metric metadata",
				"Loki: Retrieve log metadata",
				"SQL: List tables and describe table schemas",
				"Query execution is disabled.",
			},
			wantNotContains: []string{
				"Run PromQL queries",
				"Run LogQL queries",
			},
		},
		{
			name:         "raw-SQL categories reflect read-only mode",
			enabledTools: "sql,influxdb",
			disableFlags: map[string]bool{"write": true},
			wantContains: []string{
				"SQL: List tables and describe table schemas",
				"Query execution is disabled.",
			},
			wantNotContains: []string{
				"InfluxDB:",
			},
		},
		{
			name:         "raw-SQL categories restored by enable-query",
			enabledTools: "sql,influxdb",
			disableFlags: map[string]bool{"write": true, "enableQuery": true},
			wantContains: []string{
				"SQL: Query supported SQL datasources",
				"InfluxDB:",
			},
			wantNotContains: []string{
				"Query execution is disabled.",
			},
		},
		{
			name:         "query categories described normally by default",
			enabledTools: "prometheus,elasticsearch",
			wantContains: []string{
				"Run PromQL queries",
				"Elasticsearch and OpenSearch:",
			},
			wantNotContains: []string{
				"Query execution is disabled.",
			},
		},
		{
			name:         "assistant excluded when write disabled",
			enabledTools: "search,assistant",
			disableFlags: map[string]bool{"write": true},
			wantContains: []string{
				"Search:",
			},
			wantNotContains: []string{
				"Assistant:",
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
				if tc.disableFlags["tempo"] {
					dt.tempo = true
				}
				if tc.disableFlags["agento11y"] {
					dt.agento11y = true
				}
				if tc.disableFlags["assistant"] {
					dt.assistant = true
				}
				if tc.disableFlags["write"] {
					dt.write = true
				}
				if tc.disableFlags["query"] {
					dt.query = true
				}
				if tc.disableFlags["enableQuery"] {
					dt.enableQuery = true
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

func TestAppendInstructions(t *testing.T) {
	base := "This server provides access to your Grafana instance."

	t.Run("empty extra is a no-op", func(t *testing.T) {
		assert.Equal(t, base, appendInstructions(base, ""))
	})
	t.Run("whitespace-only extra is a no-op", func(t *testing.T) {
		assert.Equal(t, base, appendInstructions(base, "   \n  "))
	})
	t.Run("non-empty extra is appended with separating newlines", func(t *testing.T) {
		got := appendInstructions(base, "Log access is restricted; see https://example.tld")
		assert.Equal(t, base+"\nLog access is restricted; see https://example.tld\n", got)
	})
	t.Run("surrounding whitespace is trimmed", func(t *testing.T) {
		got := appendInstructions(base, "  NOTE  ")
		assert.Equal(t, base+"\nNOTE\n", got)
	})
}

func TestNormalizeEnabledTools(t *testing.T) {
	tests := []struct {
		name         string
		enabledTools string
		disableSQL   bool
		disableTempo bool
		wantTools    string
		wantSQLOff   bool
		wantTempoOff bool
	}{
		{
			name:         "clickhouse alias becomes sql",
			enabledTools: "search,clickhouse",
			wantTools:    "search,sql",
		},
		{
			name:         "snowflake alias becomes sql",
			enabledTools: "search,snowflake",
			wantTools:    "search,sql",
		},
		{
			name:         "athena alias becomes sql",
			enabledTools: "search,athena",
			wantTools:    "search,sql",
		},
		{
			name:         "multiple aliases deduplicated",
			enabledTools: "clickhouse,snowflake,athena",
			wantTools:    "sql",
		},
		{
			name:         "alias overrides disable-sql",
			enabledTools: "search,clickhouse",
			disableSQL:   true,
			wantTools:    "search,sql",
		},
		{
			name:         "sql without alias preserves disable flag",
			enabledTools: "search,sql",
			disableSQL:   true,
			wantTools:    "search,sql",
			wantSQLOff:   true,
		},
		{
			name:         "no aliases no change",
			enabledTools: "search,prometheus",
			wantTools:    "search,prometheus",
		},
		{
			name:         "alias coexists with sql",
			enabledTools: "sql,clickhouse",
			wantTools:    "sql",
		},
		{
			name:         "proxied alias becomes tempo",
			enabledTools: "search,proxied",
			wantTools:    "search,tempo",
		},
		{
			name:         "proxied alias preserves disable-tempo",
			enabledTools: "search,proxied",
			disableTempo: true,
			wantTools:    "search,tempo",
			wantTempoOff: true,
		},
		{
			name:         "tempo without alias preserves disable flag",
			enabledTools: "search,tempo",
			disableTempo: true,
			wantTools:    "search,tempo",
			wantTempoOff: true,
		},
		{
			name:         "proxied coexists with tempo",
			enabledTools: "tempo,proxied",
			wantTools:    "tempo",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dt := disabledTools{enabledTools: tc.enabledTools, sql: tc.disableSQL, tempo: tc.disableTempo}
			dt.normalizeEnabledTools()
			assert.Equal(t, tc.wantTools, dt.enabledTools)
			assert.Equal(t, tc.wantSQLOff, dt.sql, "sql disabled flag")
			assert.Equal(t, tc.wantTempoOff, dt.tempo, "tempo disabled flag")
		})
	}
}

func TestBuildInstructions_SQLAliasBackCompat(t *testing.T) {
	dt := disabledTools{enabledTools: "clickhouse"}
	instructions := dt.buildInstructions()
	assert.Contains(t, instructions, "SQL: Query supported SQL datasources")
}

func TestBuildInstructions_ProxiedAliasBackCompat(t *testing.T) {
	dt := disabledTools{enabledTools: "proxied"}
	instructions := dt.buildInstructions()
	assert.Contains(t, instructions, "Tempo: Search traces")
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

// testBinaryPath returns a per-test output path for `go build -o`. Windows
// refuses to exec a binary without the .exe suffix, so the suffix follows
// the host OS rather than being hardcoded.
func testBinaryPath(t *testing.T) string {
	t.Helper()
	name := "mcp-grafana"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return filepath.Join(t.TempDir(), name)
}

func TestVersionOutput(t *testing.T) {
	t.Run("without ldflags returns non-empty version", func(t *testing.T) {
		bin := testBinaryPath(t)
		build := exec.Command("go", "build", "-o", bin, ".")
		out, err := build.CombinedOutput()
		require.NoError(t, err, "go build failed: %s", out)

		got, err := exec.Command(bin, "--version").Output()
		require.NoError(t, err)
		assert.NotEmpty(t, strings.TrimSpace(string(got)))
	})

	t.Run("ldflags version takes precedence", func(t *testing.T) {
		bin := testBinaryPath(t)
		build := exec.Command("go", "build", "-ldflags", "-X github.com/grafana/mcp-grafana/v2.version=v1.2.3", "-o", bin, ".")
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

// TestApplyLokiGuardrailEnv locks in the flag-over-env precedence: env vars
// only fill in guardrail settings for flags not set on the command line,
// including a flag explicitly set to its default value.
func TestApplyLokiGuardrailEnv(t *testing.T) {
	// Flag defaults as registered in addFlags.
	defaults := grafanaConfig{
		lokiGuardrailMode:     "off",
		lokiGuardrailMaxBytes: 100 << 30,
		lokiGuardrailMaxRange: 24 * time.Hour,
	}

	tests := []struct {
		name          string
		env           map[string]string
		setFlags      map[string]bool
		wantMode      string
		wantMaxBytes  int64
		wantMaxRange  time.Duration
		wantErrSubstr string
	}{
		{
			name: "env-only applies to all three settings",
			env: map[string]string{
				"GRAFANA_LOKI_GUARDRAIL_MODE":      "enforce",
				"GRAFANA_LOKI_GUARDRAIL_MAX_BYTES": "1073741824",
				"GRAFANA_LOKI_GUARDRAIL_MAX_RANGE": "6h",
			},
			wantMode:     "enforce",
			wantMaxBytes: 1 << 30,
			wantMaxRange: 6 * time.Hour,
		},
		{
			name: "flag-set wins over env",
			env: map[string]string{
				"GRAFANA_LOKI_GUARDRAIL_MODE":      "enforce",
				"GRAFANA_LOKI_GUARDRAIL_MAX_BYTES": "1073741824",
				"GRAFANA_LOKI_GUARDRAIL_MAX_RANGE": "6h",
			},
			setFlags: map[string]bool{
				"loki-guardrail-mode":      true,
				"loki-guardrail-max-bytes": true,
				"loki-guardrail-max-range": true,
			},
			// Values stay at the flag defaults: an explicit
			// --loki-guardrail-mode=off must not be overridden by env even
			// though it equals the default.
			wantMode:     defaults.lokiGuardrailMode,
			wantMaxBytes: defaults.lokiGuardrailMaxBytes,
			wantMaxRange: defaults.lokiGuardrailMaxRange,
		},
		{
			name: "flag-set is per setting",
			env: map[string]string{
				"GRAFANA_LOKI_GUARDRAIL_MODE":      "shadow",
				"GRAFANA_LOKI_GUARDRAIL_MAX_RANGE": "6h",
			},
			setFlags:     map[string]bool{"loki-guardrail-max-range": true},
			wantMode:     "shadow",
			wantMaxBytes: defaults.lokiGuardrailMaxBytes,
			wantMaxRange: defaults.lokiGuardrailMaxRange,
		},
		{
			name:         "empty env ignored",
			env:          map[string]string{"GRAFANA_LOKI_GUARDRAIL_MODE": ""},
			wantMode:     defaults.lokiGuardrailMode,
			wantMaxBytes: defaults.lokiGuardrailMaxBytes,
			wantMaxRange: defaults.lokiGuardrailMaxRange,
		},
		{
			name:          "invalid MAX_BYTES errors",
			env:           map[string]string{"GRAFANA_LOKI_GUARDRAIL_MAX_BYTES": "10GiB"},
			wantErrSubstr: "GRAFANA_LOKI_GUARDRAIL_MAX_BYTES",
		},
		{
			name:          "invalid MAX_RANGE errors",
			env:           map[string]string{"GRAFANA_LOKI_GUARDRAIL_MAX_RANGE": "1fortnight"},
			wantErrSubstr: "GRAFANA_LOKI_GUARDRAIL_MAX_RANGE",
		},
	}

	envVars := []string{
		"GRAFANA_LOKI_GUARDRAIL_MODE",
		"GRAFANA_LOKI_GUARDRAIL_MAX_BYTES",
		"GRAFANA_LOKI_GUARDRAIL_MAX_RANGE",
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			for _, k := range envVars {
				t.Setenv(k, tc.env[k])
			}
			gc := defaults
			err := gc.applyLokiGuardrailEnv(tc.setFlags)
			if tc.wantErrSubstr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.wantErrSubstr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.wantMode, gc.lokiGuardrailMode)
			assert.Equal(t, tc.wantMaxBytes, gc.lokiGuardrailMaxBytes)
			assert.Equal(t, tc.wantMaxRange, gc.lokiGuardrailMaxRange)
		})
	}
}

// TestValidateLokiGuardrail covers the startup validation extracted from
// main: unknown modes and negative limits must be rejected.
func TestValidateLokiGuardrail(t *testing.T) {
	tests := []struct {
		name          string
		gc            grafanaConfig
		wantErrSubstr string
	}{
		{name: "off is valid", gc: grafanaConfig{lokiGuardrailMode: "off"}},
		{name: "shadow with limits is valid", gc: grafanaConfig{lokiGuardrailMode: "shadow", lokiGuardrailMaxBytes: 100 << 30, lokiGuardrailMaxRange: 24 * time.Hour}},
		{name: "zero limits disable checks", gc: grafanaConfig{lokiGuardrailMode: "enforce"}},
		{name: "unknown mode rejected", gc: grafanaConfig{lokiGuardrailMode: "Enforce"}, wantErrSubstr: "invalid Loki guardrail mode"},
		{name: "negative max bytes rejected", gc: grafanaConfig{lokiGuardrailMode: "enforce", lokiGuardrailMaxBytes: -1}, wantErrSubstr: "GRAFANA_LOKI_GUARDRAIL_MAX_BYTES"},
		{name: "negative max range rejected", gc: grafanaConfig{lokiGuardrailMode: "enforce", lokiGuardrailMaxRange: -time.Hour}, wantErrSubstr: "GRAFANA_LOKI_GUARDRAIL_MAX_RANGE"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.gc.validateLokiGuardrail()
			if tc.wantErrSubstr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.wantErrSubstr)
				return
			}
			assert.NoError(t, err)
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

// Exercise the binary so the test covers both the SDK's loopback check and
// our Host/Origin middleware, including how the CLI wires them together.
func TestHTTPAllowedHostsLoopbackProxy(t *testing.T) {
	bin := testBinaryPath(t)
	build := exec.CommandContext(t.Context(), "go", "build", "-o", bin, ".")
	out, err := build.CombinedOutput()
	require.NoError(t, err, "go build failed: %s", out)

	// Keep the test independent of the developer's Grafana and OTel settings.
	var env []string
	for _, value := range os.Environ() {
		if !strings.HasPrefix(value, "GRAFANA_") && !strings.HasPrefix(value, "MCP_GRAFANA_") && !strings.HasPrefix(value, "OTEL_") {
			env = append(env, value)
		}
	}
	const token = "loopback-proxy-test-token"
	env = append(env, "GRAFANA_URL=http://127.0.0.1:1", "GRAFANA_ORG_ID=1", "MCP_GRAFANA_SERVER_TOKEN="+token)

	cases := []struct {
		name       string
		flags      []string
		allowLocal bool
		allowProxy bool
		allowOther bool
	}{
		{name: "unset", allowLocal: true},
		{name: "empty", flags: []string{"--allowed-hosts="}, allowLocal: true},
		{name: "separators", flags: []string{"--allowed-hosts= , , "}, allowLocal: true},
		{name: "explicit", flags: []string{"--allowed-hosts= mcp.example.com, other.example.com "}, allowProxy: true},
		{name: "wildcard", flags: []string{"--allowed-hosts=*"}, allowLocal: true, allowProxy: true, allowOther: true},
	}
	for _, transport := range []string{"sse", "streamable-http"} {
		t.Run(transport, func(t *testing.T) {
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					listener, err := net.Listen("tcp", "127.0.0.1:0")
					require.NoError(t, err)
					port := strconv.Itoa(listener.Addr().(*net.TCPAddr).Port)
					require.NoError(t, listener.Close())
					addr := "127.0.0.1:" + port
					args := []string{"--transport=" + transport, "--address=0.0.0.0:" + port, "--enabled-tools=", "--disable-tempo"}
					args = append(args, tc.flags...)
					cmd := exec.CommandContext(t.Context(), bin, args...)
					cmd.Env = env
					var logs bytes.Buffer
					cmd.Stdout, cmd.Stderr = &logs, &logs
					require.NoError(t, cmd.Start())
					t.Cleanup(func() {
						_ = cmd.Process.Kill()
						_ = cmd.Wait()
						if t.Failed() {
							t.Log(logs.String())
						}
					})
					require.Eventually(t, func() bool {
						conn, err := net.DialTimeout("tcp", addr, 100*time.Millisecond)
						if err != nil {
							return false
						}
						_ = conn.Close()
						return true
					}, 5*time.Second, 10*time.Millisecond)

					client := &http.Client{Timeout: 3 * time.Second}
					t.Cleanup(client.CloseIdleConnections)
					request := func(t *testing.T, host, origin, auth string, wantStatus int) {
						t.Helper()
						method, path, body := http.MethodGet, "/sse", ""
						if transport == "streamable-http" {
							method, path = http.MethodPost, "/mcp"
							body = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"test","version":"1"}}}`
						}
						req, err := http.NewRequestWithContext(t.Context(), method, "http://"+addr+path, strings.NewReader(body))
						require.NoError(t, err)
						req.Host = host
						req.Header.Set("Content-Type", "application/json")
						req.Header.Set("Accept", "application/json, text/event-stream")
						if origin != "" {
							req.Header.Set("Origin", origin)
						}
						if auth != "" {
							req.Header.Set("Authorization", auth)
						}
						resp, err := client.Do(req)
						require.NoError(t, err)
						defer func() { _ = resp.Body.Close() }()
						require.Equal(t, wantStatus, resp.StatusCode)
						if wantStatus != http.StatusOK {
							return
						}
						if transport == "sse" {
							line, err := bufio.NewReader(resp.Body).ReadString('\n')
							require.NoError(t, err)
							assert.Equal(t, "event: endpoint\n", line)
						} else {
							var result struct {
								Result mcp.InitializeResult `json:"result"`
							}
							require.NoError(t, json.NewDecoder(resp.Body).Decode(&result))
							assert.NotEmpty(t, result.Result.ProtocolVersion)
						}
					}
					for _, host := range []struct {
						name    string
						allowed bool
					}{
						{"localhost:" + port, tc.allowLocal},
						{"mcp.example.com", tc.allowProxy},
						{"other.example.com", tc.allowProxy},
						{"unlisted.example.com", tc.allowOther},
					} {
						t.Run(host.name, func(t *testing.T) {
							status := http.StatusForbidden
							if host.allowed {
								status = http.StatusOK
							}
							request(t, host.name, "", "Bearer "+token, status)
						})
					}
					allowedHost := "localhost:" + port
					if tc.allowProxy {
						allowedHost = "mcp.example.com"
					}
					t.Run("reject origin", func(t *testing.T) {
						request(t, allowedHost, "https://untrusted.example.com", "Bearer "+token, http.StatusForbidden)
					})
					t.Run("require token", func(t *testing.T) {
						request(t, allowedHost, "", "", http.StatusUnauthorized)
					})
					t.Run("reject wrong token", func(t *testing.T) {
						request(t, allowedHost, "", "Bearer wrong-token", http.StatusUnauthorized)
					})
				})
			}
		})
	}
}

// A browser client on an allowed origin must be able to send the Grafana
// selection headers the server documents, and responses vary by Origin.
func TestCORSMiddlewarePreflight(t *testing.T) {
	h := corsMiddleware([]string{"https://app.example"}, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("preflight must not reach the MCP handler")
	}))
	req := httptest.NewRequest(http.MethodOptions, "/mcp", nil)
	req.Header.Set("Origin", "https://app.example")
	req.Header.Set("Access-Control-Request-Method", http.MethodPost)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNoContent, rec.Code)
	assert.Equal(t, "https://app.example", rec.Header().Get("Access-Control-Allow-Origin"))
	assert.Equal(t, "Origin", rec.Header().Get("Vary"))
	allowed := strings.Split(rec.Header().Get("Access-Control-Allow-Headers"), ", ")
	for _, name := range []string{"X-Grafana-URL", "X-Grafana-Service-Account-Token", "X-Grafana-API-Key", "X-Grafana-Org-Id", "Mcp-Session-Id", "Last-Event-ID"} {
		assert.Contains(t, allowed, name)
	}
}

func TestHTTPSecurityConfigCORSOrigins(t *testing.T) {
	cases := []struct {
		name           string
		allowedOrigins string
		want           []string
	}{
		{
			name: "unset returns nil",
			want: nil,
		},
		{
			name:           "comma-only returns nil",
			allowedOrigins: ", ,",
			want:           nil,
		},
		{
			name:           "explicit origins pass through trimmed",
			allowedOrigins: "HTTPS://App.Example, https://other.example",
			want:           []string{"HTTPS://App.Example", "https://other.example"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hsc := httpSecurityConfig{allowedOrigins: tc.allowedOrigins}
			assert.Equal(t, tc.want, hsc.corsOrigins())
		})
	}
}

// connectTestClient creates an in-memory client session to a server for testing.
func connectTestClient(t *testing.T, s *mcp.Server) *mcp.ClientSession {
	t.Helper()
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	go func() { _ = s.Run(context.Background(), serverTransport) }()
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0"}, nil)
	session, err := client.Connect(context.Background(), clientTransport, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Close() })
	return session
}

func getServerNameFromInitialize(t *testing.T, s *mcp.Server) string {
	t.Helper()
	session := connectTestClient(t, s)
	return session.InitializeResult().ServerInfo.Name
}

func TestResolveServerName(t *testing.T) {
	tests := []struct {
		name              string
		flagValue         string
		flagExplicitlySet bool
		envValue          string
		want              string
	}{
		{
			name:              "no flag no env returns default",
			flagValue:         defaultServerName,
			flagExplicitlySet: false,
			envValue:          "",
			want:              defaultServerName,
		},
		{
			name:              "flag set wins over nothing",
			flagValue:         "my-custom-server",
			flagExplicitlySet: true,
			envValue:          "",
			want:              "my-custom-server",
		},
		{
			name:              "env set and flag not explicitly set returns env",
			flagValue:         defaultServerName,
			flagExplicitlySet: false,
			envValue:          "env-server",
			want:              "env-server",
		},
		{
			name:              "both set flag wins",
			flagValue:         "flag-server",
			flagExplicitlySet: true,
			envValue:          "env-server",
			want:              "flag-server",
		},
		{
			name:              "flag explicitly set to default overrides env",
			flagValue:         defaultServerName,
			flagExplicitlySet: true,
			envValue:          "env-server",
			want:              defaultServerName,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := resolveServerName(tc.flagValue, tc.flagExplicitlySet, tc.envValue, defaultServerName)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestValidateServerName(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{"default", "mcp-grafana", false},
		{"typical multi-instance", "grafana-project-a", false},
		{"dot-separated", "mcp-grafana.staging", false},
		{"mixed case underscores digits", "My_Custom_Server_v2", false},
		{"minimum length", "a", false},
		{"maximum length", strings.Repeat("X", 128), false},
		{"starts with letter then digits", "g123", false},
		{"starts with digit", "1server", false},

		{"empty string", "", true},
		{"whitespace only", " ", true},
		{"contains space", "my server", true},
		{"contains tab", "name\t", true},
		{"contains newline", "name\n", true},
		{"starts with hyphen", "-starts-hyphen", true},
		{"starts with dot", ".dotfile", true},
		{"starts with underscore", "_leading_underscore", true},
		{"non-ASCII unicode", "café-server", true},
		{"cyrillic", "сервер", true},
		{"ANSI escape", "name\x1b[31m", true},
		{"null byte", "name\x00", true},
		{"shell metacharacter semicolon", "server;rm -rf /", true},
		{"shell command substitution", "$(whoami)", true},
		{"forward slash", "name/path", true},
		{"backslash", "name\\path", true},
		{"colon", "name:colon", true},
		{"zero-width character", "name\u200dzwj", true},
		{"RTL override", "name\u202ertl", true},
		{"exceeds max length", strings.Repeat("a", 129), true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validateServerName(tc.input)
			if tc.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestNewServer_DefaultServerName(t *testing.T) {
	obs := newTestObservability(t)
	s := newServer(defaultServerName, disabledTools{enabledTools: "search"}, obs, usagestats.New(usagestats.Config{Mode: usagestats.ModeDisabled}), "")

	name := getServerNameFromInitialize(t, s)
	assert.Equal(t, "mcp-grafana", name)
}

func TestNewServer_CustomServerName(t *testing.T) {
	obs := newTestObservability(t)
	s := newServer("my-custom-server", disabledTools{enabledTools: "search"}, obs, usagestats.New(usagestats.Config{Mode: usagestats.ModeDisabled}), "")

	name := getServerNameFromInitialize(t, s)
	assert.Equal(t, "my-custom-server", name)
}

func TestNewServer_MultiInstanceDistinctNames(t *testing.T) {
	obs := newTestObservability(t)

	sAlpha := newServer("instance-alpha", disabledTools{enabledTools: "search"}, obs, usagestats.New(usagestats.Config{Mode: usagestats.ModeDisabled}), "")

	sBeta := newServer("instance-beta", disabledTools{enabledTools: "search"}, obs, usagestats.New(usagestats.Config{Mode: usagestats.ModeDisabled}), "")

	nameAlpha := getServerNameFromInitialize(t, sAlpha)
	nameBeta := getServerNameFromInitialize(t, sBeta)

	assert.Equal(t, "instance-alpha", nameAlpha)
	assert.Equal(t, "instance-beta", nameBeta)
	assert.NotEqual(t, nameAlpha, nameBeta)
}

func TestCustomServerName_DoesNotAffectUserAgent(t *testing.T) {
	obs := newTestObservability(t)
	s := newServer("my-custom-instance", disabledTools{enabledTools: "search"}, obs, usagestats.New(usagestats.Config{Mode: usagestats.ModeDisabled}), "")

	name := getServerNameFromInitialize(t, s)
	assert.Equal(t, "my-custom-instance", name)

	ua := mcpgrafana.UserAgent()
	assert.Contains(t, ua, "mcp-grafana/")
	assert.NotContains(t, ua, "my-custom-instance")
}

func TestValidateServerName_ErrorMessages(t *testing.T) {
	tests := []struct {
		name           string
		input          string
		wantSubstrings []string
	}{
		{
			name:           "empty string mentions empty",
			input:          "",
			wantSubstrings: []string{"must not be empty"},
		},
		{
			name:           "too long mentions length",
			input:          strings.Repeat("a", 129),
			wantSubstrings: []string{"too long", "129", "128"},
		},
		{
			name:           "invalid chars includes name and pattern",
			input:          "my server",
			wantSubstrings: []string{"my server", "invalid characters"},
		},
		{
			name:           "leading hyphen includes name",
			input:          "-bad",
			wantSubstrings: []string{"-bad", "invalid characters"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validateServerName(tc.input)
			require.Error(t, err)
			for _, sub := range tc.wantSubstrings {
				assert.Contains(t, err.Error(), sub)
			}
		})
	}
}

func TestCallerAuthConfigResolveToken(t *testing.T) {
	t.Run("flag takes precedence and is trimmed", func(t *testing.T) {
		t.Setenv(serverAuthTokenEnvVar, "from-env")
		ca := callerAuthConfig{token: "  from-flag  "}
		assert.Equal(t, "from-flag", ca.resolveToken())
	})

	t.Run("falls back to env when flag empty", func(t *testing.T) {
		t.Setenv(serverAuthTokenEnvVar, "  from-env  ")
		ca := callerAuthConfig{}
		assert.Equal(t, "from-env", ca.resolveToken())
	})

	t.Run("empty when neither set", func(t *testing.T) {
		t.Setenv(serverAuthTokenEnvVar, "")
		ca := callerAuthConfig{}
		assert.Empty(t, ca.resolveToken())
	})
}

func TestCheckCallerAuthPolicy(t *testing.T) {
	cases := []struct {
		name      string
		address   string
		token     string
		wantLevel string // "INFO" or "WARN"
		wantMsg   string // substring expected in the emitted log line
	}{
		// A caller token authenticates every request, so any bind is fine.
		{"token set, public bind", "0.0.0.0:8000", "tok", "INFO", "Caller authentication enabled"},
		{"token set, loopback bind", "localhost:8000", "tok", "INFO", "Caller authentication enabled"},

		// No token on a loopback bind: only local processes can connect, so this
		// warns about the missing token rather than about public exposure.
		{"no token, loopback", "127.0.0.1:8000", "", "WARN", "bound to a loopback address"},

		// No token on a reachable bind: logged at ERROR (highest --log-level, so
		// the exposure can't be filtered out); starts today (backward compatible).
		{"no token, public bind", "0.0.0.0:8000", "", "ERROR", "startup error in a future release"},
		{"no token, wildcard port", ":8000", "", "ERROR", "startup error in a future release"},
		{"no token, routable IP", "192.168.1.5:8000", "", "ERROR", "startup error in a future release"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
			checkCallerAuthPolicy("streamable-http", tc.address, tc.token, logger)
			out := buf.String()
			assert.Contains(t, out, tc.wantMsg)
			assert.Contains(t, out, `"level":"`+tc.wantLevel+`"`)
		})
	}
}

// TestSOCKS5ProxyFromEnv locks in GRAFANA_SOCKS5_PROXY handling: unset or
// empty leaves the proxy disabled, a valid URL is returned verbatim, and an
// invalid URL is a startup error that does not leak the raw value (it may
// contain proxy credentials).
func TestSOCKS5ProxyFromEnv(t *testing.T) {
	t.Run("unset env leaves proxy empty", func(t *testing.T) {
		t.Setenv("GRAFANA_SOCKS5_PROXY", "")
		raw, err := socks5ProxyFromEnv()
		require.NoError(t, err)
		assert.Empty(t, raw)
	})

	t.Run("valid URL is returned verbatim", func(t *testing.T) {
		t.Setenv("GRAFANA_SOCKS5_PROXY", "socks5://127.0.0.1:1080")
		raw, err := socks5ProxyFromEnv()
		require.NoError(t, err)
		assert.Equal(t, "socks5://127.0.0.1:1080", raw)
	})

	t.Run("invalid URL is an error naming the env var but not the value", func(t *testing.T) {
		t.Setenv("GRAFANA_SOCKS5_PROXY", "http://user:secretpw@proxy.example.com:1080")
		_, err := socks5ProxyFromEnv()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "GRAFANA_SOCKS5_PROXY")
		assert.NotContains(t, err.Error(), "secretpw")
	})
}

// safeQueryToolNames execute a query the query language cannot use to mutate
// data. --disable-query removes them; --disable-write must not.
var safeQueryToolNames = []string{
	"query_prometheus",
	"query_prometheus_histogram",
	"query_loki_logs",
	"query_loki_patterns",
	"query_elasticsearch",
	"query_quickwit",
	"query_graphite",
	"query_graphite_density",
	"query_cloudwatch",
	"query_pyroscope",
	"run_panel_query",
}

// mutatingQueryToolNames pass raw SQL or InfluxQL through unfiltered, so they
// can write when the datasource credentials permit it. Both --disable-query and
// --disable-write remove them; --enable-query overrides the latter.
var mutatingQueryToolNames = []string{
	"query_sql",
	"query_influxdb",
}

// queryToolNames are every tool that executes a query against a datasource.
var queryToolNames = append(append([]string{}, safeQueryToolNames...), mutatingQueryToolNames...)

// metadataToolNames are discovery tools that live in the same categories as the
// query tools and must survive --disable-query.
var metadataToolNames = []string{
	"list_prometheus_metric_names",
	"list_prometheus_label_names",
	"list_prometheus_label_values",
	"list_prometheus_metric_metadata",
	"list_loki_label_names",
	"list_loki_label_values",
	// Both send a selector to the datasource but read the index rather than
	// returning log content, so query gating does not apply to them.
	"query_loki_stats",
	"analyze_loki_labels",
	"list_sql_databases",
	"list_sql_tables",
	"describe_sql_table",
	"list_graphite_metrics",
	"list_graphite_tags",
	"list_cloudwatch_namespaces",
	"list_cloudwatch_metrics",
	"list_cloudwatch_dimensions",
	"list_cloudwatch_dimension_values",
	"list_pyroscope_label_names",
	"list_pyroscope_label_values",
	"list_pyroscope_profile_types",
}

// registerAllCategories registers every known tool category on a fresh server
// and returns the set of advertised tool names.
func registerAllCategories(t *testing.T, dt disabledTools) map[string]bool {
	t.Helper()

	categories := make([]string, 0, len(dt.toolEntries()))
	for _, e := range dt.toolEntries() {
		categories = append(categories, e.category)
	}
	dt.enabledTools = strings.Join(categories, ",")

	srv := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0"}, nil)
	dt.processTools(srv)

	session := connectTestClient(t, srv)
	result, err := session.ListTools(context.Background(), nil)
	require.NoError(t, err)

	names := make(map[string]bool, len(result.Tools))
	for _, tool := range result.Tools {
		names[tool.Name] = true
	}
	return names
}

func TestProcessTools_QueryToolsRegisteredByDefault(t *testing.T) {
	names := registerAllCategories(t, disabledTools{})
	for _, name := range append(append([]string{}, queryToolNames...), metadataToolNames...) {
		assert.True(t, names[name], "%s should be registered by default", name)
	}
}

func TestProcessTools_DisableQueryRemovesOnlyQueryTools(t *testing.T) {
	names := registerAllCategories(t, disabledTools{query: true})
	for _, name := range queryToolNames {
		assert.False(t, names[name], "%s should not be registered with --disable-query", name)
	}
	for _, name := range metadataToolNames {
		assert.True(t, names[name], "%s should survive --disable-query", name)
	}
	// --disable-query is independent of --disable-write: write tools stay.
	assert.True(t, names["update_dashboard"], "write tools should be unaffected by --disable-query")
}

func TestProcessTools_DisableWriteRemovesMutatingQueryTools(t *testing.T) {
	names := registerAllCategories(t, disabledTools{write: true})
	for _, name := range safeQueryToolNames {
		assert.True(t, names[name], "%s cannot mutate data and should survive --disable-write", name)
	}
	for _, name := range mutatingQueryToolNames {
		assert.False(t, names[name], "%s passes raw SQL through and should be gone with --disable-write", name)
	}
	assert.False(t, names["update_dashboard"], "write tools should be gone with --disable-write")
	for _, name := range metadataToolNames {
		assert.True(t, names[name], "%s should survive --disable-write", name)
	}
}

func TestProcessTools_EnableQueryOverridesDisableWrite(t *testing.T) {
	names := registerAllCategories(t, disabledTools{write: true, enableQuery: true})
	for _, name := range queryToolNames {
		assert.True(t, names[name], "%s should be restored by --enable-query", name)
	}
	// The override is scoped to query execution: real write tools stay gone.
	assert.False(t, names["update_dashboard"], "--enable-query must not re-enable write tools")
	assert.False(t, names["create_folder"], "--enable-query must not re-enable write tools")
	assert.False(t, names["create_annotation"], "--enable-query must not re-enable write tools")
}

func TestProcessTools_EnableQueryAloneChangesNothing(t *testing.T) {
	defaults := registerAllCategories(t, disabledTools{})
	names := registerAllCategories(t, disabledTools{enableQuery: true})
	assert.Equal(t, defaults, names, "--enable-query on its own should be a no-op")
}

func TestProcessTools_DisableQueryBeatsEnableQuery(t *testing.T) {
	names := registerAllCategories(t, disabledTools{query: true, enableQuery: true})
	for _, name := range queryToolNames {
		assert.False(t, names[name], "%s should be gone: --disable-query wins over --enable-query", name)
	}
	for _, name := range metadataToolNames {
		assert.True(t, names[name], "%s should survive --disable-query", name)
	}
}

func TestProcessTools_BothDisableFlags(t *testing.T) {
	names := registerAllCategories(t, disabledTools{write: true, query: true})
	for _, name := range queryToolNames {
		assert.False(t, names[name], "%s should be gone with both flags set", name)
	}
	assert.False(t, names["update_dashboard"], "write tools should be gone with both flags set")
	for _, name := range metadataToolNames {
		assert.True(t, names[name], "%s should survive both flags", name)
	}
}

func TestWriteToolOverridden_TrimsWhitespaceAroundNames(t *testing.T) {
	dt := &disabledTools{write: true, writeToolOverrides: "query_sql, query_influxdb"}
	assert.True(t, dt.writeToolOverridden("query_influxdb"), "trailing name after a space-separated comma should still match")

	dt = &disabledTools{write: true, writeToolOverrides: " query_sql"}
	assert.True(t, dt.writeToolOverridden("query_sql"), "leading whitespace before a name should still match")
}

func TestProcessTools_EnableWriteToolsAloneChangesNothing(t *testing.T) {
	defaults := registerAllCategories(t, disabledTools{})
	names := registerAllCategories(t, disabledTools{writeToolOverrides: "query_sql,query_influxdb"})
	assert.Equal(t, defaults, names, "--enable-write-tools on its own should be a no-op")
}

// --enable-query is documented as a shorthand for naming the four raw-SQL
// query tools in --enable-write-tools; this pins that equivalence.
func TestProcessTools_EnableQueryIsAliasForEnableWriteTools(t *testing.T) {
	viaEnableQuery := registerAllCategories(t, disabledTools{write: true, enableQuery: true})
	viaWriteTools := registerAllCategories(t, disabledTools{
		write:              true,
		writeToolOverrides: "query_sql,query_influxdb",
	})
	assert.Equal(t, viaEnableQuery, viaWriteTools)
}

func getPath(h http.Handler, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

func TestRegisterOps_DefaultKeepsHealthzOnMainMux(t *testing.T) {
	main := http.NewServeMux()
	side := registerOps(main, newTestObservability(t), "", observability.Config{})

	assert.Empty(t, side, "no extra listener without --healthz-address or --metrics-address")
	rec := getPath(main, "/healthz")
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "ok", rec.Body.String())
}

func TestRegisterOps_MetricsOnMainMux(t *testing.T) {
	obs, err := observability.Setup(observability.Config{MetricsEnabled: true})
	require.NoError(t, err)
	t.Cleanup(func() { _ = obs.Shutdown(context.Background()) })

	main := http.NewServeMux()
	side := registerOps(main, obs, "", observability.Config{MetricsEnabled: true})

	assert.Empty(t, side, "--metrics with empty --metrics-address must not start a side listener")
	assert.Equal(t, http.StatusOK, getPath(main, "/healthz").Code)
	assert.Equal(t, http.StatusOK, getPath(main, "/metrics").Code)
}

func TestRegisterOps_HealthzAddressMovesItOffMainMux(t *testing.T) {
	main := http.NewServeMux()
	side := registerOps(main, newTestObservability(t), ":8080", observability.Config{})

	assert.Equal(t, http.StatusNotFound, getPath(main, "/healthz").Code,
		"/healthz must leave the MCP listener, not be served on both")
	require.Len(t, side, 1)
	require.Contains(t, side, ":8080")
	rec := getPath(side[":8080"], "/healthz")
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "ok", rec.Body.String())
}

func TestRegisterOps_SharedAddressUsesOneListener(t *testing.T) {
	obs, err := observability.Setup(observability.Config{MetricsEnabled: true})
	require.NoError(t, err)
	t.Cleanup(func() { _ = obs.Shutdown(context.Background()) })

	main := http.NewServeMux()
	side := registerOps(main, obs, ":9090", observability.Config{MetricsEnabled: true, MetricsAddress: ":9090"})

	require.Len(t, side, 1, "one bind address must not produce two listeners")
	assert.Equal(t, http.StatusOK, getPath(side[":9090"], "/healthz").Code)
	assert.Equal(t, http.StatusOK, getPath(side[":9090"], "/metrics").Code)
	assert.Equal(t, http.StatusNotFound, getPath(main, "/healthz").Code)
	assert.Equal(t, http.StatusNotFound, getPath(main, "/metrics").Code)
}

func TestRegisterOps_DistinctAddressesStaySplit(t *testing.T) {
	obs, err := observability.Setup(observability.Config{MetricsEnabled: true})
	require.NoError(t, err)
	t.Cleanup(func() { _ = obs.Shutdown(context.Background()) })

	main := http.NewServeMux()
	side := registerOps(main, obs, ":8080", observability.Config{MetricsEnabled: true, MetricsAddress: ":9090"})

	require.Len(t, side, 2)
	assert.Equal(t, http.StatusOK, getPath(side[":8080"], "/healthz").Code)
	assert.Equal(t, http.StatusNotFound, getPath(side[":8080"], "/metrics").Code)
	assert.Equal(t, http.StatusOK, getPath(side[":9090"], "/metrics").Code)
	assert.Equal(t, http.StatusNotFound, getPath(side[":9090"], "/healthz").Code)
}

// Metrics stay opt-in: --healthz-address alone must not expose /metrics.
func TestRegisterOps_HealthzAddressDoesNotEnableMetrics(t *testing.T) {
	main := http.NewServeMux()
	side := registerOps(main, newTestObservability(t), ":8080", observability.Config{})

	require.Len(t, side, 1)
	assert.Equal(t, http.StatusNotFound, getPath(side[":8080"], "/metrics").Code)
	assert.Equal(t, http.StatusNotFound, getPath(main, "/metrics").Code)
}

// TestNewServer_InvalidArgumentTypeReturnsToolErrorNotProtocolError verifies
// that a tool call with a JSON type mismatch (e.g. a number where the schema
// declares a string) surfaces as a structured tool result (IsError: true)
// that an agent can act on, rather than escaping as a raw JSON-RPC internal
// error (-32603) with a bare Go unmarshal message. See issue #830.
func TestNewServer_InvalidArgumentTypeReturnsToolErrorNotProtocolError(t *testing.T) {
	obs := newTestObservability(t)
	s := newServer(defaultServerName, disabledTools{enabledTools: "datasource"}, obs, usagestats.New(usagestats.Config{Mode: usagestats.ModeDisabled}), "")

	session := connectTestClient(t, s)

	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "list_datasources",
		Arguments: map[string]any{"type": 42},
	})

	require.NoError(t, err, "a schema type mismatch must not surface as a JSON-RPC protocol error")
	require.NotNil(t, result)
	assert.True(t, result.IsError, "a schema type mismatch must surface as a structured tool error")
}

func TestCategoryReport(t *testing.T) {
	dt := disabledTools{enabledTools: "search,alerting,oncall,not-a-real-category", oncall: true, dashboard: true}
	enabled, disabled := dt.categoryReport()

	assert.ElementsMatch(t, []string{"search", "alerting"}, enabled)
	// Every category a --disable-* flag turned off, whether or not
	// --enabled-tools named it. An unrecognised category name from
	// --enabled-tools appears in neither list.
	assert.ElementsMatch(t, []string{"dashboard", "oncall"}, disabled)
}

func TestNativeToolNamesFromRegistrations(t *testing.T) {
	dt := disabledTools{enabledTools: "search"}
	names := nativeToolNames(dt)
	assert.Contains(t, names, "search_dashboards")
	assert.NotEmpty(t, names)
}

// TestCategoryReportNormalisesAliases: --enabled-tools=clickhouse is rewritten
// to sql and clears --disable-sql, so the report must say sql is enabled
// rather than repeating the flags as typed.
func TestCategoryReportNormalisesAliases(t *testing.T) {
	dt := disabledTools{enabledTools: "search,clickhouse", sql: true}
	dt.normalizeEnabledTools()
	enabled, disabled := dt.categoryReport()

	assert.Contains(t, enabled, "sql")
	assert.NotContains(t, disabled, "sql")
	assert.NotContains(t, enabled, "clickhouse")
}

// TestCategoryReportHonoursWriteAndQueryGates: a category can survive
// --enabled-tools and still register no tool, because --disable-write empties
// assistant and --disable-query empties the query-only categories.
// categoryReport reported those as enabled while the server exposed nothing,
// which made tool availability look unrelated to tool usage. It shares
// categoryRegistersTools with buildInstructions so the two cannot drift again.
func TestCategoryReportHonoursWriteAndQueryGates(t *testing.T) {
	t.Run("assistant is empty without write tools", func(t *testing.T) {
		dt := disabledTools{enabledTools: "assistant", write: true}
		enabled, disabled := dt.categoryReport()

		assert.NotContains(t, enabled, "assistant")
		// Not disabled either: no --disable-assistant was passed. The two
		// lists are deliberately not complements.
		assert.NotContains(t, disabled, "assistant")
	})

	t.Run("assistant is reported when write tools are enabled", func(t *testing.T) {
		dt := disabledTools{enabledTools: "assistant"}
		enabled, _ := dt.categoryReport()

		assert.Contains(t, enabled, "assistant")
	})

	t.Run("query-only categories are empty without query tools", func(t *testing.T) {
		dt := disabledTools{enabledTools: strings.Join(queryOnlyCategories, ",") + ",search", query: true}
		enabled, _ := dt.categoryReport()

		for _, category := range queryOnlyCategories {
			assert.NotContains(t, enabled, category, "%s registers nothing with --disable-query", category)
		}
		// A category that is not query-only still registers its other tools.
		assert.Contains(t, enabled, "search")
	})

	t.Run("agrees with the capabilities advertised to the agent", func(t *testing.T) {
		dt := disabledTools{enabledTools: "assistant,search", write: true}
		enabled, _ := dt.categoryReport()
		instructions := (&disabledTools{enabledTools: "assistant,search", write: true}).buildInstructions()

		assert.NotContains(t, enabled, "assistant")
		assert.NotContains(t, instructions, "Assistant")
	})
}

func TestEffectiveTLSEnabled(t *testing.T) {
	withCert := tlsConfig{certFile: "/tmp/c.pem", keyFile: "/tmp/k.pem"}

	assert.True(t, effectiveTLSEnabled("streamable-http", withCert))
	assert.True(t, effectiveTLSEnabled("sse", withCert))
	assert.False(t, effectiveTLSEnabled("stdio", withCert))
	assert.False(t, effectiveTLSEnabled("streamable-http", tlsConfig{}))
}

func TestEffectiveMetricsEnabled(t *testing.T) {
	assert.True(t, effectiveMetricsEnabled("streamable-http", true))
	assert.True(t, effectiveMetricsEnabled("sse", true))
	// registerOps is never called for stdio, so nothing serves /metrics.
	assert.False(t, effectiveMetricsEnabled("stdio", true))
	assert.False(t, effectiveMetricsEnabled("streamable-http", false))
}

// TestListToolsResult_ReturnsTools verifies that tools/list returns the
// expected tools via the go-sdk. The mark3labs-specific resultType/cacheScope/
// ttlMs fields (#1140) are handled natively by the go-sdk's Cacheable struct.
func TestListToolsResult_ReturnsTools(t *testing.T) {
	obs := newTestObservability(t)
	s := newServer(defaultServerName, disabledTools{enabledTools: "search"}, obs, usagestats.New(usagestats.Config{Mode: usagestats.ModeDisabled}), "")

	session := connectTestClient(t, s)
	result, err := session.ListTools(context.Background(), nil)
	require.NoError(t, err)
	require.NotEmpty(t, result.Tools, "tools/list should return registered tools")
}

func TestRecoveryMiddleware_CatchesPanic(t *testing.T) {
	s := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0"}, nil)
	mcp.AddTool(s, &mcp.Tool{Name: "panicking_tool"}, func(_ context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
		panic("boom")
	})
	s.AddReceivingMiddleware(recoveryMiddleware())

	session := connectTestClient(t, s)

	_, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "panicking_tool"})
	require.Error(t, err, "a panicking tool must return an error")
	assert.Contains(t, err.Error(), "internal error")
	assert.NotContains(t, err.Error(), "boom", "panic details must not leak to the client")
}

// Regression test for https://github.com/grafana/mcp-grafana/issues/1021:
// nothing under --base-path was routed to the SSE handler at all. /healthz and /metrics are
// internal-only endpoints and stay mounted at the server root regardless of
// --base-path.
func TestHTTPMuxHonoursBasePath(t *testing.T) {
	const mcpBody = "mcp"
	mcpHandler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(mcpBody))
	})
	// Real observability: the ops endpoints come from registerOps, the same
	// call the transports make, so the test cannot pass on a mount the server
	// does not actually build.
	metricsCfg := observability.Config{MetricsEnabled: true}
	obs, err := observability.Setup(metricsCfg)
	require.NoError(t, err)
	t.Cleanup(func() { _ = obs.Shutdown(context.Background()) })

	get := func(t *testing.T, mux *http.ServeMux, path string) (int, string) {
		t.Helper()
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		return rec.Code, rec.Body.String()
	}
	assertBody := func(t *testing.T, mux *http.ServeMux, path, want string) {
		t.Helper()
		code, body := get(t, mux, path)
		assert.Equal(t, http.StatusOK, code, "GET %s", path)
		assert.Equal(t, want, body, "GET %s", path)
	}

	t.Run("sse", func(t *testing.T) {
		cases := []struct {
			name     string
			basePath string
			// paths the MCP (SSE) handler must serve
			mcpPaths []string
			// paths that must not reach the MCP handler
			unroutedPaths []string
		}{
			{
				name:     "no base path",
				basePath: "",
				mcpPaths: []string{"/sse"},
			},
			{
				name:          "base path without trailing slash",
				basePath:      "/my-custom-base",
				mcpPaths:      []string{"/my-custom-base/sse"},
				unroutedPaths: []string{"/sse"},
			},
			{
				name:          "base path with trailing slash",
				basePath:      "/my-custom-base/",
				mcpPaths:      []string{"/my-custom-base/sse"},
				unroutedPaths: []string{"/sse"},
			},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				mux := newSSEMux(mcpHandler, normalizeBasePath(tc.basePath), "", nil)
				require.Empty(t, registerOps(mux, obs, "", metricsCfg),
					"ops share the MCP listener when no ops address is set")
				for _, p := range tc.mcpPaths {
					assertBody(t, mux, p, mcpBody)
				}
				assertBody(t, mux, "/healthz", "ok")
				if code, _ := get(t, mux, "/metrics"); code != http.StatusOK {
					t.Fatalf("GET /metrics = %d, want 200", code)
				}
				// The base path is a prefix, not an alias: the MCP endpoints
				// must not stay reachable at the server root as well.
				for _, p := range tc.unroutedPaths {
					code, _ := get(t, mux, p)
					assert.Equal(t, http.StatusNotFound, code, "GET %s", p)
				}
				// /healthz and /metrics are internal-only and must never answer
				// under --base-path.
				if base := normalizeBasePath(tc.basePath); base != "" {
					code, _ := get(t, mux, base+"/healthz")
					assert.Equal(t, http.StatusNotFound, code, "GET %s/healthz", base)
					code, _ = get(t, mux, base+"/metrics")
					assert.Equal(t, http.StatusNotFound, code, "GET %s/metrics", base)
				}
			})
		}
	})

	t.Run("streamable-http", func(t *testing.T) {
		cases := []struct {
			name         string
			basePath     string
			endpointPath string
			mcpPath      string
		}{
			{
				name:         "no base path",
				endpointPath: "/mcp",
				mcpPath:      "/mcp",
			},
			{
				name:         "base path prefixes the endpoint",
				basePath:     "/my-custom-base",
				endpointPath: "/mcp",
				mcpPath:      "/my-custom-base/mcp",
			},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				mux := newStreamableHTTPMux(mcpHandler, streamableEndpointPath(tc.basePath, tc.endpointPath), "", nil)
				require.Empty(t, registerOps(mux, obs, "", metricsCfg),
					"ops share the MCP listener when no ops address is set")
				assertBody(t, mux, tc.mcpPath, mcpBody)
				assertBody(t, mux, "/healthz", "ok")
				if code, _ := get(t, mux, "/metrics"); code != http.StatusOK {
					t.Fatalf("GET /metrics = %d, want 200", code)
				}
				// /healthz and /metrics must never answer under --base-path.
				if base := normalizeBasePath(tc.basePath); base != "" {
					code, _ := get(t, mux, base+"/healthz")
					assert.Equal(t, http.StatusNotFound, code, "GET %s/healthz", base)
					code, _ = get(t, mux, base+"/metrics")
					assert.Equal(t, http.StatusNotFound, code, "GET %s/metrics", base)
				}
			})
		}
	})

	t.Run("metrics are absent when disabled", func(t *testing.T) {
		mux := newSSEMux(mcpHandler, normalizeBasePath("/my-custom-base"), "", nil)
		require.Empty(t, registerOps(mux, newTestObservability(t), "", observability.Config{}))
		code, _ := get(t, mux, "/metrics")
		assert.Equal(t, http.StatusNotFound, code, "GET /metrics with metrics disabled")
	})
}

// The mount pattern is what can break, not the raw flag: --endpoint-path is
// joined with --base-path and cleaned on the way to the mux, so a value that
// does not read as an operational path can still resolve to one. ServeMux
// panics both on a duplicate pattern and on a pattern it cannot parse, and it
// quietly reinterprets a '{' segment as a wildcard, so none of the three may
// reach the mount.
func TestValidateMountFlags(t *testing.T) {
	metricsOnListener := observability.Config{MetricsEnabled: true}

	cases := []struct {
		name           string
		basePath       string
		endpointPath   string
		healthzAddress string
		obs            observability.Config
		wantPath       string
		wantErr        string
		// wildcard marks a pattern ServeMux accepts but reads as a wildcard
		// segment rather than the literal path the operator typed.
		wildcard bool
	}{
		{name: "default", endpointPath: "/mcp", wantPath: "/mcp"},
		{name: "under a base path", basePath: "/my-base", endpointPath: "/mcp", wantPath: "/my-base/mcp"},
		{name: "healthz", endpointPath: "/healthz", wantPath: "/healthz", wantErr: "operational endpoint"},
		{name: "healthz without a leading slash", endpointPath: "healthz", wantPath: "/healthz", wantErr: "operational endpoint"},
		// A trailing slash makes it a subtree mount, which sits beside the
		// exact /healthz registerOps makes rather than colliding with it.
		{name: "healthz with a trailing slash", endpointPath: "/healthz/", wantPath: "/healthz/"},
		{name: "trailing slash is preserved", endpointPath: "/mcp/", wantPath: "/mcp/"},
		{name: "trailing slash under a base path", basePath: "/my-base", endpointPath: "/mcp/", wantPath: "/my-base/mcp/"},
		{name: "empty", endpointPath: "", wantPath: "/", wantErr: "cannot be empty"},
		// --endpoint-path "/" is a subtree of the base path, so this mounts
		// "/healthz/" beside the operational "/healthz" rather than over it.
		{name: "healthz contributed by the base path", basePath: "/healthz", endpointPath: "/", wantPath: "/healthz/"},
		{name: "base path and endpoint path spelling healthz together", basePath: "/health", endpointPath: "../healthz", wantPath: "/healthz", wantErr: "operational endpoint"},
		{name: "nested under an operational path is fine", endpointPath: "/healthz/mcp", wantPath: "/healthz/mcp"},
		// --healthz-address moves /healthz to its own listener, which frees
		// the path on this one — the same rule --metrics-address follows.
		{name: "healthz served on its own address", endpointPath: "/healthz", healthzAddress: ":8080", wantPath: "/healthz"},
		{name: "operational path as a prefix is fine", endpointPath: "/healthz-mcp", wantPath: "/healthz-mcp"},

		// /metrics is only mounted when the metrics handler shares this
		// listener, so the path is only taken then.
		{name: "metrics with metrics on the listener", endpointPath: "/metrics", obs: metricsOnListener, wantPath: "/metrics", wantErr: "operational endpoint"},
		{name: "metrics reached by traversal", endpointPath: "/foo/../metrics", obs: metricsOnListener, wantPath: "/metrics", wantErr: "operational endpoint"},
		{name: "metrics with metrics disabled", endpointPath: "/metrics", wantPath: "/metrics"},
		{name: "metrics with metrics on their own address", endpointPath: "/metrics", obs: observability.Config{MetricsEnabled: true, MetricsAddress: ":9090"}, wantPath: "/metrics"},

		// ServeMux pattern syntax: a space or tab starts a method, '{' a
		// wildcard segment.
		{name: "space in the base path", basePath: "/my base", endpointPath: "/mcp", wantPath: "/my base/mcp", wantErr: "not a route path"},
		{name: "tab in the endpoint path", endpointPath: "/m\tcp", wantPath: "/m\tcp", wantErr: "not a route path"},
		{name: "wildcard segment", endpointPath: "/{mcp}", wantPath: "/{mcp}", wantErr: "not a route path", wildcard: true},
	}

	noop := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := streamableEndpointPath(normalizeBasePath(tc.basePath), tc.endpointPath)
			assert.Equal(t, tc.wantPath, got)

			err := validateMountFlags("streamable-http", tc.basePath, tc.endpointPath, tc.healthzAddress, tc.obs)

			// The verdict has to match what actually happens at mount time:
			// the MCP mount and registerOps, exactly as the transport builds
			// them.
			o, setupErr := observability.Setup(tc.obs)
			require.NoError(t, setupErr)
			t.Cleanup(func() { _ = o.Shutdown(context.Background()) })
			mount := func() {
				mux := newStreamableHTTPMux(noop, got, "", nil)
				registerOps(mux, o, tc.healthzAddress, tc.obs)
			}

			if tc.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.wantErr)
				if tc.endpointPath == "" {
					// "/" is a pattern ServeMux takes: the objection is that it
					// answers every unclaimed path on the listener, not that it
					// panics. Prove it is a catch-all rather than a crash.
					mux := newStreamableHTTPMux(noop, got, "", nil)
					registerOps(mux, o, tc.healthzAddress, tc.obs)
					rec := httptest.NewRecorder()
					mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/anything", nil))
					assert.Equal(t, http.StatusOK, rec.Code, "an empty endpoint path mounts a catch-all")
					return
				}
				if tc.wildcard {
					// ServeMux takes this pattern, but not as the path that was
					// typed: it matches any single segment instead.
					mcp := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
						_, _ = w.Write([]byte("mcp"))
					})
					mux := newStreamableHTTPMux(mcp, got, "", nil)
					registerOps(mux, o, tc.healthzAddress, tc.obs)
					rec := httptest.NewRecorder()
					mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/not-the-endpoint", nil))
					assert.Equal(t, "mcp", rec.Body.String(), "GET /not-the-endpoint reached the MCP handler through the wildcard")
					return
				}
				assert.Panics(t, mount, "mounting %q should panic", got)
				return
			}

			assert.NoError(t, err)
			assert.NotPanics(t, mount)

			if strings.HasSuffix(got, "/") {
				// The slash is the whole point: clients pointed at the URL with
				// it, and at the bare path, must both still land on MCP.
				mcp := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					_, _ = w.Write([]byte("mcp"))
				})
				mux := newStreamableHTTPMux(mcp, got, "", nil)
				registerOps(mux, o, tc.healthzAddress, tc.obs)
				rec := httptest.NewRecorder()
				mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, got, nil))
				assert.Equal(t, "mcp", rec.Body.String(), "POST %s must reach the MCP handler", got)
				// The bare path redirects into the subtree — unless an
				// operational endpoint already holds it exactly, which is the
				// one case where the root mount legitimately wins.
				bare := strings.TrimSuffix(got, "/")
				if !slices.Contains(operationalMounts(tc.healthzAddress, tc.obs), bare) {
					rec = httptest.NewRecorder()
					mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, bare, nil))
					assert.Equal(t, http.StatusTemporaryRedirect, rec.Code,
						"POST %s must redirect into the subtree, method intact", bare)
				}
			}
		})
	}
}

// --endpoint-path is only mounted by the streamable-http transport, so a value
// that would be rejected there is nobody's problem on the others.
// normalizeBasePath cleans "..", so a prefix can vanish on the way to the
// mux. Silently serving MCP at the server root is not what the operator who
// wrote a prefix asked for, so it is refused instead.
func TestValidateMountFlags_BasePathTraversingToRoot(t *testing.T) {
	for _, transport := range []string{"sse", "streamable-http"} {
		t.Run(transport, func(t *testing.T) {
			for _, base := range []string{"..", ".", "/foo/../.."} {
				err := validateMountFlags(transport, base, "/mcp", "", observability.Config{})
				require.Error(t, err, "--base-path %q", base)
				assert.Contains(t, err.Error(), "resolves to the server root")
			}
			// An unset flag means the same thing and stays legal.
			assert.NoError(t, validateMountFlags(transport, "", "/mcp", "", observability.Config{}))
			assert.NoError(t, validateMountFlags(transport, "/", "/mcp", "", observability.Config{}))
		})
	}
}

func TestValidateMountFlags_EndpointPathIgnoredByOtherTransports(t *testing.T) {
	for _, transport := range []string{"stdio", "sse"} {
		t.Run(transport, func(t *testing.T) {
			assert.NoError(t, validateMountFlags(transport, "", "/healthz", "", observability.Config{MetricsEnabled: true}))
		})
	}
}

// The SSE handler is mounted at <base>/sse, so it cannot collide with an
// operational endpoint — but it still has to parse as a pattern.
func TestValidateMountFlags_SSEBasePath(t *testing.T) {
	assert.NoError(t, validateMountFlags("sse", "/healthz", "", "", observability.Config{MetricsEnabled: true}),
		"--base-path /healthz mounts /healthz/sse, which does not collide with /healthz")

	err := validateMountFlags("sse", "/my base", "", "", observability.Config{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--base-path")
	assert.Contains(t, err.Error(), "not a route path")

	// The check has to run on the pattern newSSEMux really mounts. Both derive
	// it from the raw flag through normalizeBasePath, and the error quotes what
	// was checked — so a base path whose raw and normalized forms differ pins
	// the two together. Drop the normalization on either side and this reads
	// "/my base//sse".
	err = validateMountFlags("sse", "/my base/", "", "", observability.Config{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), strconv.Quote(sseEndpointPath(normalizeBasePath("/my base/"))))
}
