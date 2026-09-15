//go:build unit

package usagestats

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/grafana/mcp-grafana/observability"
)

// fakeSession is the minimum server.ClientSession a context needs.
type fakeSession struct{ id string }

func (f *fakeSession) Initialize()       {}
func (f *fakeSession) Initialized() bool { return true }
func (f *fakeSession) NotificationChannel() chan<- mcp.JSONRPCNotification {
	return make(chan mcp.JSONRPCNotification, 1)
}
func (f *fakeSession) SessionID() string { return f.id }

// collector is a test endpoint that records the events posted to it.
type collector struct {
	*httptest.Server
	mu     sync.Mutex
	events []Event
	bodies [][]byte
}

func newCollector(t *testing.T) *collector {
	t.Helper()
	c := &collector{}
	c.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		var e Event
		if err := json.Unmarshal(body, &e); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		c.mu.Lock()
		c.events = append(c.events, e)
		c.bodies = append(c.bodies, body)
		c.mu.Unlock()
	}))
	t.Cleanup(c.Close)
	return c
}

func (c *collector) received() []Event {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]Event(nil), c.events...)
}

// rawBodies returns the payloads exactly as they went over the wire, so a test
// can assert on which keys are present rather than on decoded zero values.
func (c *collector) rawBodies() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]string, 0, len(c.bodies))
	for _, b := range c.bodies {
		out = append(out, string(b))
	}
	return out
}

// sessionContext returns a context carrying a client session, as the MCP
// server's hooks receive one on the legacy path.
func sessionContext(t *testing.T, id string) (context.Context, server.ClientSession) {
	t.Helper()
	srv := server.NewMCPServer("test", "v0")
	sess := &fakeSession{id: id}
	return srv.WithContext(context.Background(), sess), sess
}

func callRequest(name string) *mcp.CallToolRequest {
	req := &mcp.CallToolRequest{}
	req.Params.Name = name
	return req
}

func initRequest(clientName, clientVersion string) *mcp.InitializeRequest {
	return &mcp.InitializeRequest{
		Params: mcp.InitializeParams{
			ClientInfo: mcp.Implementation{Name: clientName, Version: clientVersion},
		},
	}
}

// TestToolCountsAreExactAndResetOnFlush pins two properties at once: counts
// are raw numbers rather than buckets, and each flush reports the delta since
// the previous one rather than a running total.
func TestToolCountsAreExactAndResetOnFlush(t *testing.T) {
	c := newCollector(t)
	r := New(Config{
		Mode:        ModeEnabled,
		Endpoint:    c.URL,
		NativeTools: observability.ValueSet("search_dashboards", "query_prometheus"),
	})
	hooks := r.Hooks()
	ctx, _ := sessionContext(t, "mcp-session-1")

	for range 7 {
		hooks.OnAfterCallTool[0](ctx, 1, callRequest("search_dashboards"), &mcp.CallToolResult{})
	}
	for range 2 {
		hooks.OnAfterCallTool[0](ctx, 1, callRequest("search_dashboards"), &mcp.CallToolResult{IsError: true})
	}

	r.flush(context.Background(), ReasonInterval)
	require.Len(t, c.received(), 1)
	first := c.received()[0]
	assert.Equal(t, ReasonInterval, first.ReportReason)
	// 9 and 2 exactly: no bucketing, no rounding.
	assert.Equal(t, ToolCount{Calls: 9, Errors: 2}, first.ToolCalls["search_dashboards"])
	assert.Equal(t, "search_dashboards", first.ToolsCalled)

	hooks.OnAfterCallTool[0](ctx, 1, callRequest("query_prometheus"), &mcp.CallToolResult{})
	r.flush(context.Background(), ReasonInterval)
	require.Len(t, c.received(), 2)
	second := c.received()[1]

	// The delta, not the running total: the first report's counts are gone.
	assert.Equal(t, map[string]ToolCount{"query_prometheus": {Calls: 1}}, second.ToolCalls)
	assert.Equal(t, "query_prometheus", second.ToolsCalled)

	// Same process across both reports, and uptime keeps accumulating even
	// though the counters do not.
	assert.Equal(t, first.ProcessID, second.ProcessID)
	assert.GreaterOrEqual(t, second.ProcessUptimeMS, first.ProcessUptimeMS)
}

// TestCountsAggregateAcrossSessionsAndSessionlessRequests is the point of the
// per-process unit: calls are counted whether or not a session was registered,
// which is what keeps protocol version 2026-07-28 clients visible.
func TestCountsAggregateAcrossSessionsAndSessionlessRequests(t *testing.T) {
	c := newCollector(t)
	r := New(Config{
		Mode:        ModeEnabled,
		Endpoint:    c.URL,
		NativeTools: observability.ValueSet("search_dashboards"),
	})
	hooks := r.Hooks()

	ctxA, _ := sessionContext(t, "session-a")
	ctxB, _ := sessionContext(t, "session-b")
	// No session in context at all, as on the sessionless modern path.
	ctxNone := context.Background()

	hooks.OnAfterCallTool[0](ctxA, 1, callRequest("search_dashboards"), &mcp.CallToolResult{})
	hooks.OnAfterCallTool[0](ctxB, 1, callRequest("search_dashboards"), &mcp.CallToolResult{})
	hooks.OnAfterCallTool[0](ctxNone, 1, callRequest("search_dashboards"), &mcp.CallToolResult{})

	r.flush(context.Background(), ReasonInterval)
	require.Len(t, c.received(), 1)
	assert.Equal(t, ToolCount{Calls: 3}, c.received()[0].ToolCalls["search_dashboards"])
}

// TestClientsSeenIsASortedSet: clients_seen says which kinds of client
// connected, never how many of each, and resets with the other activity.
func TestClientsSeenIsASortedSet(t *testing.T) {
	c := newCollector(t)
	r := New(Config{Mode: ModeEnabled, Endpoint: c.URL})
	hooks := r.Hooks()
	ctx := context.Background()

	hooks.OnAfterInitialize[0](ctx, 1, initRequest("Cursor", "1.2.3"), &mcp.InitializeResult{})
	hooks.OnAfterInitialize[0](ctx, 1, initRequest("cursor", "1.2.4"), &mcp.InitializeResult{})
	hooks.OnAfterInitialize[0](ctx, 1, initRequest("claude-code", "2.0.1"), &mcp.InitializeResult{})
	hooks.OnAfterInitialize[0](ctx, 1, initRequest("my-internal-agent", "9.9"), &mcp.InitializeResult{})

	r.flush(context.Background(), ReasonInterval)
	require.Len(t, c.received(), 1)
	assert.Equal(t, "claude-code,cursor,other", c.received()[0].ClientsSeen)

	// No version travels with the set, under any key.
	assert.NotContains(t, c.rawBodies()[0], "1.2.3")
	assert.NotContains(t, c.rawBodies()[0], "client_version")

	// It is a delta like the counters: a window with no initialize omits it.
	r.flush(context.Background(), ReasonInterval)
	require.Len(t, c.rawBodies(), 2)
	assert.NotContains(t, c.rawBodies()[1], "clients_seen")
}

// TestConflictingPerRequestValuesAreOmitted: a multi-tenant process can
// resolve a different Grafana version or auth method per request. Absence then
// means "no single value", rather than a "mixed" sentinel that would sit in
// the same column as real values.
func TestConflictingPerRequestValuesAreOmitted(t *testing.T) {
	c := newCollector(t)
	var target GrafanaTarget
	r := New(Config{
		Mode:     ModeEnabled,
		Endpoint: c.URL,
		Target:   func(context.Context) GrafanaTarget { return target },
	})
	hooks := r.Hooks()
	ctx := context.Background()

	target = GrafanaTarget{URL: "https://one.grafana.net", Version: "12.1.0", AuthMethod: AuthMethodServiceAccountToken}
	hooks.OnAfterCallTool[0](ctx, 1, callRequest("x"), &mcp.CallToolResult{})

	// One distinct value each, so both travel.
	r.flush(context.Background(), ReasonInterval)
	require.Len(t, c.received(), 1)
	assert.Equal(t, "12.1.0", c.received()[0].GrafanaVersion)
	assert.Equal(t, AuthMethodServiceAccountToken, c.received()[0].AuthMethod)

	// A second tenant resolves differently.
	target = GrafanaTarget{URL: "https://one.grafana.net", Version: "12.2.0", AuthMethod: AuthMethodBasicAuth}
	hooks.OnAfterCallTool[0](ctx, 1, callRequest("x"), &mcp.CallToolResult{})

	r.flush(context.Background(), ReasonInterval)
	require.Len(t, c.rawBodies(), 2)
	assert.Empty(t, c.received()[1].GrafanaVersion)
	assert.Empty(t, c.received()[1].AuthMethod)
	assert.NotContains(t, c.rawBodies()[1], "grafana_version")
	assert.NotContains(t, c.rawBodies()[1], "auth_method")
	// And no invented sentinel took their place.
	assert.NotContains(t, c.rawBodies()[1], "mixed")

	// target_kind comes from GRAFANA_URL, which is never read from a header,
	// so it is not subject to the rule.
	assert.Equal(t, TargetKindCloud, c.received()[1].TargetKind)
}

// TestFailedFlushLosesItsDelta is the stated consequence of resetting on send
// rather than on acknowledgement: totals are a floor, never a count.
func TestFailedFlushLosesItsDelta(t *testing.T) {
	c := newCollector(t)
	r := New(Config{
		Mode:        ModeEnabled,
		Endpoint:    "http://127.0.0.1:1/unreachable",
		NativeTools: observability.ValueSet("search_dashboards"),
	})
	hooks := r.Hooks()
	ctx := context.Background()
	hooks.OnAfterCallTool[0](ctx, 1, callRequest("search_dashboards"), &mcp.CallToolResult{})

	r.flush(context.Background(), ReasonInterval)

	r.cfg.Endpoint = c.URL
	r.flush(context.Background(), ReasonInterval)

	require.Len(t, c.received(), 1)
	assert.Empty(t, c.received()[0].ToolCalls, "the lost report's delta must not be re-reported")
}

// TestProxiedToolsCollapseToOneKey covers the privacy rule that matters most
// here: a proxied tool's name is chosen by a remote MCP server, so it can
// never reach the wire, and neither can the datasource type it is prefixed
// with.
func TestProxiedToolsCollapseToOneKey(t *testing.T) {
	c := newCollector(t)
	r := New(Config{
		Mode:        ModeEnabled,
		Endpoint:    c.URL,
		NativeTools: observability.ValueSet("search_dashboards"),
	})
	hooks := r.Hooks()
	ctx := context.Background()

	hooks.OnAfterCallTool[0](ctx, 1, callRequest("search_dashboards"), &mcp.CallToolResult{})
	hooks.OnAfterCallTool[0](ctx, 1, callRequest("tempo_traceql-search"), &mcp.CallToolResult{})
	hooks.OnAfterCallTool[0](ctx, 1, callRequest("tempo_get-trace"), &mcp.CallToolResult{IsError: true})
	hooks.OnError[0](ctx, 1, "tools/call", callRequest("loki_some-remote-tool"), assert.AnError)

	r.flush(context.Background(), ReasonInterval)
	require.Len(t, c.received(), 1)
	e := c.received()[0]

	assert.Equal(t, map[string]ToolCount{
		"search_dashboards": {Calls: 1},
		ProxiedToolName:     {Calls: 3, Errors: 2},
	}, e.ToolCalls)
	assert.Equal(t, "proxied,search_dashboards", e.ToolsCalled)

	for _, forbidden := range []string{"traceql", "get-trace", "tempo", "some-remote-tool"} {
		assert.NotContains(t, c.rawBodies()[0], forbidden)
	}
}

// TestNativeToolsSetBeforeRegistrationFailsSafe: with no allowlist supplied
// yet, every call reports as proxied rather than by name.
func TestNativeToolsSetBeforeRegistrationFailsSafe(t *testing.T) {
	r := New(Config{Mode: ModeEnabled})
	assert.Equal(t, ProxiedToolName, r.toolKey("search_dashboards"))

	r.SetNativeTools(observability.ValueSet("search_dashboards"))
	assert.Equal(t, "search_dashboards", r.toolKey("search_dashboards"))
	assert.Equal(t, ProxiedToolName, r.toolKey("search_dashboardz"))
}

// TestOnErrorWithoutAToolNameRecordsNothing: mcp-go hands onError a
// zero-valued request when the tools capability is unsupported or the payload
// fails to unmarshal. toolKey("") returns the proxied sentinel, so counting it
// would invent a proxied call that never happened.
func TestOnErrorWithoutAToolNameRecordsNothing(t *testing.T) {
	c := newCollector(t)
	r := New(Config{Mode: ModeEnabled, Endpoint: c.URL})
	hooks := r.Hooks()
	ctx := context.Background()

	hooks.OnError[0](ctx, 1, "tools/call", &mcp.CallToolRequest{}, assert.AnError)
	hooks.OnError[0](ctx, 1, "tools/list", callRequest("search_dashboards"), assert.AnError)

	r.flush(context.Background(), ReasonInterval)
	require.Len(t, c.received(), 1)
	assert.Empty(t, c.received()[0].ToolCalls)
	assert.Empty(t, c.received()[0].ToolsCalled)
}

func TestLogModeWritesToStderrAndSendsNothing(t *testing.T) {
	c := newCollector(t)
	var out bytes.Buffer
	r := New(Config{
		Mode:        ModeLog,
		Endpoint:    c.URL,
		LogOutput:   &out,
		NativeTools: observability.ValueSet("search_dashboards"),
	})
	hooks := r.Hooks()
	hooks.OnAfterCallTool[0](context.Background(), 1, callRequest("search_dashboards"), &mcp.CallToolResult{})

	r.flush(context.Background(), ReasonShutdown)

	assert.Empty(t, c.received(), "log mode must not send")
	assert.Contains(t, out.String(), "usage statistics (not sent):")

	var e Event
	_, payload, found := bytes.Cut(out.Bytes(), []byte(": "))
	require.True(t, found)
	require.NoError(t, json.Unmarshal(bytes.TrimSpace(payload), &e))
	assert.Equal(t, ToolCount{Calls: 1}, e.ToolCalls["search_dashboards"])
	assert.Equal(t, ReasonShutdown, e.ReportReason)
}

// TestLogModeDefaultsToStderr: stdout is the MCP protocol channel under the
// stdio transport, so the inspection output must never land there.
func TestLogModeDefaultsToStderr(t *testing.T) {
	r := New(Config{Mode: ModeLog})
	assert.Same(t, os.Stderr, r.logOutput)
}

func TestDisabledReporterCollectsNothing(t *testing.T) {
	c := newCollector(t)
	r := New(Config{Mode: ModeDisabled, Endpoint: c.URL})

	assert.False(t, r.Enabled())
	hooks := r.Hooks()
	assert.Empty(t, hooks.OnAfterInitialize)
	assert.Empty(t, hooks.OnAfterCallTool)
	assert.Empty(t, hooks.OnError)

	r.Start(context.Background())
	r.Shutdown()
	assert.Empty(t, c.received())
}

// countingTransport records every request attempt without letting one out.
type countingTransport struct {
	mu sync.Mutex
	n  int
}

func (c *countingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	c.mu.Lock()
	c.n++
	c.mu.Unlock()
	return nil, errors.New("blocked by test")
}

func (c *countingTransport) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.n
}

// TestZeroConfigLifecycleSendsNothing walks the whole lifecycle on a
// zero-valued Config and asserts not one request was even attempted.
//
// This is a regression test for a real leak: Enabled() used to be "mode is not
// disabled", so usagestats.New(usagestats.Config{}) in the cmd tests was
// enabled, New filled in DefaultEndpoint, and a torn-down server POSTed a
// usage report to the production endpoint on every `make test-unit` run.
func TestZeroConfigLifecycleSendsNothing(t *testing.T) {
	ct := &countingTransport{}
	r := New(Config{HTTPClient: &http.Client{Transport: ct}})
	require.False(t, r.Enabled())

	hooks := r.Hooks()
	assert.Empty(t, hooks.OnAfterInitialize)
	assert.Empty(t, hooks.OnAfterCallTool)
	assert.Empty(t, hooks.OnError)

	// Even driven directly, past the empty hooks, nothing must leave.
	r.recordToolCall(context.Background(), "search_dashboards", false)
	r.flush(context.Background(), ReasonInterval)
	r.Start(context.Background())
	r.Shutdown()

	assert.Zero(t, ct.count(), "a disabled reporter must not attempt a request")
}

func TestShutdownReportsWithTheShutdownReason(t *testing.T) {
	c := newCollector(t)
	r := New(Config{
		Mode:        ModeEnabled,
		Endpoint:    c.URL,
		NativeTools: observability.ValueSet("search_dashboards"),
	})
	hooks := r.Hooks()
	hooks.OnAfterCallTool[0](context.Background(), 1, callRequest("search_dashboards"), &mcp.CallToolResult{})

	r.Shutdown()
	r.Shutdown() // idempotent: must not send twice

	require.Len(t, c.received(), 1)
	assert.Equal(t, ReasonShutdown, c.received()[0].ReportReason)
	assert.Equal(t, ToolCount{Calls: 1}, c.received()[0].ToolCalls["search_dashboards"])
}

// TestGrafanaURLNeverReachesTheWire: the target URL is supplied only so it can
// be classified as cloud or self-hosted.
func TestGrafanaURLNeverReachesTheWire(t *testing.T) {
	c := newCollector(t)
	r := New(Config{
		Mode:     ModeEnabled,
		Endpoint: c.URL,
		Target: func(context.Context) GrafanaTarget {
			return GrafanaTarget{
				URL:        "https://secret-stack-name.grafana.net",
				Version:    "12.1.0",
				OrgIDSet:   true,
				AuthMethod: AuthMethodServiceAccountToken,
			}
		},
	})
	hooks := r.Hooks()
	hooks.OnAfterInitialize[0](context.Background(), 1, initRequest("cursor", "1"), &mcp.InitializeResult{})

	r.flush(context.Background(), ReasonInterval)
	require.Len(t, c.received(), 1)
	e := c.received()[0]

	assert.Equal(t, TargetKindCloud, e.TargetKind)
	assert.Equal(t, "12.1.0", e.GrafanaVersion)
	assert.True(t, e.OrgIDSet)
	assert.Equal(t, AuthMethodServiceAccountToken, e.AuthMethod)

	assert.NotContains(t, c.rawBodies()[0], "secret-stack-name")
	assert.NotContains(t, c.rawBodies()[0], "grafana.net")
}

// TestAuthMethodOutsideTheVocabularyIsClamped: the vocabulary is shared with
// observability's metric-label allowlist, sentinel included.
func TestAuthMethodOutsideTheVocabularyIsClamped(t *testing.T) {
	c := newCollector(t)
	r := New(Config{
		Mode:     ModeEnabled,
		Endpoint: c.URL,
		Target: func(context.Context) GrafanaTarget {
			return GrafanaTarget{URL: "http://localhost:3000", AuthMethod: "some-new-scheme"}
		},
	})
	hooks := r.Hooks()
	hooks.OnAfterCallTool[0](context.Background(), 1, callRequest("x"), &mcp.CallToolResult{})

	r.flush(context.Background(), ReasonInterval)
	require.Len(t, c.received(), 1)
	assert.Equal(t, observability.ValueOther, c.received()[0].AuthMethod)
}

// TestGrafanaVersionIsLengthCapped: the instance's reported version is outside
// this binary's control, and forks set it freely.
func TestGrafanaVersionIsLengthCapped(t *testing.T) {
	c := newCollector(t)
	r := New(Config{
		Mode:     ModeEnabled,
		Endpoint: c.URL,
		Target: func(context.Context) GrafanaTarget {
			return GrafanaTarget{
				URL:     "http://localhost:3000",
				Version: "12.1.0-" + strings.Repeat("x", 4096),
			}
		},
	})
	hooks := r.Hooks()
	hooks.OnAfterCallTool[0](context.Background(), 1, callRequest("x"), &mcp.CallToolResult{})

	r.flush(context.Background(), ReasonInterval)
	require.Len(t, c.received(), 1)
	assert.Len(t, c.received()[0].GrafanaVersion, maxVersionLen)
}

func TestConfigFieldsAreNamesNotValues(t *testing.T) {
	c := newCollector(t)
	r := New(Config{
		Mode:          ModeEnabled,
		Endpoint:      c.URL,
		Version:       "v1.2.3",
		Transport:     "streamable-http",
		Flags:         []string{"transport", "server-name", "instructions-append"},
		EnabledTools:  []string{"search", "alerting"},
		DisabledTools: []string{"oncall"},
	})

	r.flush(context.Background(), ReasonInterval)
	require.Len(t, c.received(), 1)
	e := c.received()[0]

	assert.Equal(t, ServiceName, e.Service)
	assert.Equal(t, "v1.2.3", e.Version)
	assert.Equal(t, "streamable-http", e.Transport)
	assert.Equal(t, "instructions-append,server-name,transport", e.Flags)
	assert.Equal(t, "alerting,search", e.EnabledTools)
	assert.Equal(t, "oncall", e.DisabledTools)
}

// TestEmptyFieldsAreOmittedNotSentEmpty: a window with no activity must read
// as NULL at the receiver, not as empty strings and an empty object.
func TestEmptyFieldsAreOmittedNotSentEmpty(t *testing.T) {
	c := newCollector(t)
	r := New(Config{Mode: ModeEnabled, Endpoint: c.URL})

	r.flush(context.Background(), ReasonInterval)
	require.Len(t, c.rawBodies(), 1)
	body := c.rawBodies()[0]

	for _, absent := range []string{"clients_seen", "tool_calls", "tools_called", "grafana_version", "auth_method", "target_kind"} {
		assert.NotContains(t, body, absent)
	}
	// The envelope is always present, so an idle process is still countable.
	assert.Contains(t, body, `"report_reason":"interval"`)
	assert.Contains(t, body, `"process_id":`)
	assert.Contains(t, body, `"process_uptime_ms":`)
}

// TestShutdownIsCappedWhenLogOutputBlocks: in log mode the event goes to
// stderr, which under stdio is a pipe the MCP client owns. A client that has
// stopped reading it must not be able to hold the process open.
func TestShutdownIsCappedWhenLogOutputBlocks(t *testing.T) {
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })

	r := New(Config{Mode: ModeLog, LogOutput: blockingWriter{release}})

	start := time.Now()
	r.Shutdown()
	assert.Less(t, time.Since(start), 3*time.Second, "a blocked stderr must not hold up shutdown")
}

// blockingWriter blocks in Write until its channel is closed.
type blockingWriter struct{ release chan struct{} }

func (b blockingWriter) Write(p []byte) (int, error) {
	<-b.release
	return len(p), nil
}

// TestShutdownIsCappedWhenTheCounterLockIsHeld: buildEvent takes the counter
// lock, so a stuck holder must not extend shutdown either.
func TestShutdownIsCappedWhenTheCounterLockIsHeld(t *testing.T) {
	c := newCollector(t)
	r := New(Config{Mode: ModeEnabled, Endpoint: c.URL})

	r.counters.mu.Lock()
	t.Cleanup(r.counters.mu.Unlock)

	start := time.Now()
	r.Shutdown()
	assert.Less(t, time.Since(start), 3*time.Second, "shutdown must not wait on a held counter lock")
}

// TestShutdownIsCappedWhenTheEndpointStalls: a telemetry endpoint that accepts
// the connection and never answers must not delay the process exit.
func TestShutdownIsCappedWhenTheEndpointStalls(t *testing.T) {
	release := make(chan struct{})
	stalled := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
	}))
	t.Cleanup(func() {
		close(release)
		stalled.Close()
	})

	r := New(Config{Mode: ModeEnabled, Endpoint: stalled.URL})

	start := time.Now()
	r.Shutdown()
	assert.Less(t, time.Since(start), 3*time.Second, "shutdown flush should give up after about a second")
}

// TestTargetFuncIsNotCalledUnderTheCounterLock: a Target implementation that
// blocks must not be able to block a concurrent flush, because the shutdown
// flush is on the process's exit path.
func TestTargetFuncIsNotCalledUnderTheCounterLock(t *testing.T) {
	c := newCollector(t)
	release := make(chan struct{})
	entered := make(chan struct{}, 1)
	r := New(Config{
		Mode:     ModeEnabled,
		Endpoint: c.URL,
		Target: func(context.Context) GrafanaTarget {
			select {
			case entered <- struct{}{}:
			default:
			}
			<-release
			return GrafanaTarget{URL: "http://localhost:3000"}
		},
	})
	hooks := r.Hooks()

	go hooks.OnAfterCallTool[0](context.Background(), 1, callRequest("x"), &mcp.CallToolResult{})
	<-entered

	done := make(chan struct{})
	go func() {
		defer close(done)
		r.flush(context.Background(), ReasonInterval)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("flush blocked behind the Target call")
	}
	close(release)
}

func TestJitterStaysWithinTenPercent(t *testing.T) {
	base := float64(DefaultInterval)
	lo := time.Duration(base * (1 - intervalJitter))
	hi := time.Duration(base * (1 + intervalJitter))
	for range 200 {
		d := jitterDuration(DefaultInterval)
		assert.GreaterOrEqual(t, d, lo)
		assert.LessOrEqual(t, d, hi)
	}
}

// TestConcurrentRecordingIsRaceFree drives the hooks from many goroutines
// while flushing, which is what the process actually does: one shared counter
// set behind requests served in parallel.
func TestConcurrentRecordingIsRaceFree(t *testing.T) {
	c := newCollector(t)
	r := New(Config{
		Mode:        ModeEnabled,
		Endpoint:    c.URL,
		NativeTools: observability.ValueSet("search_dashboards"),
	})
	hooks := r.Hooks()

	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 50 {
				hooks.OnAfterCallTool[0](context.Background(), 1, callRequest("search_dashboards"), &mcp.CallToolResult{})
				hooks.OnAfterInitialize[0](context.Background(), 1, initRequest("cursor", "1"), &mcp.InitializeResult{})
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for range 5 {
			r.flush(context.Background(), ReasonInterval)
		}
	}()
	wg.Wait()
	r.flush(context.Background(), ReasonInterval)

	// Every call lands in exactly one report, so the deltas must total 400.
	var total int64
	for _, e := range c.received() {
		total += e.ToolCalls["search_dashboards"].Calls
	}
	assert.Equal(t, int64(400), total, "no call may be lost or double-counted across flushes")
}
