package main

import (
	"context"
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
