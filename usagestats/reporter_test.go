//go:build unit

package usagestats

import (
	"bytes"
	"context"
	"encoding/json"
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

// fakeSession is the minimum server.ClientSession the hooks need.
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
// server's hooks receive one.
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
	ctx, sess := sessionContext(t, "mcp-session-1")
	hooks.OnRegisterSession[0](ctx, sess)

	for range 7 {
		hooks.OnAfterCallTool[0](ctx, 1, callRequest("search_dashboards"), &mcp.CallToolResult{})
	}
	for range 2 {
		hooks.OnAfterCallTool[0](ctx, 1, callRequest("search_dashboards"), &mcp.CallToolResult{IsError: true})
	}

	r.flushAll(context.Background(), ReasonInterval)
	require.Len(t, c.received(), 1)
	first := c.received()[0]
	assert.Equal(t, ReasonInterval, first.ReportReason)
	// 9 and 2 exactly: no bucketing, no rounding.
	assert.Equal(t, ToolCount{Calls: 9, Errors: 2}, first.ToolCalls["search_dashboards"])
	assert.Equal(t, "search_dashboards", first.ToolsCalled)

	hooks.OnAfterCallTool[0](ctx, 1, callRequest("query_prometheus"), &mcp.CallToolResult{})
	r.flushAll(context.Background(), ReasonInterval)
	require.Len(t, c.received(), 2)
	second := c.received()[1]

	// The delta, not the running total: the first report's counts are gone.
	assert.Equal(t, map[string]ToolCount{"query_prometheus": {Calls: 1}}, second.ToolCalls)
	assert.Equal(t, "query_prometheus", second.ToolsCalled)

	// Same session across both reports, and the duration keeps accumulating
	// even though the counters do not.
	assert.Equal(t, first.SessionID, second.SessionID)
	assert.Equal(t, first.ProcessID, second.ProcessID)
	assert.GreaterOrEqual(t, second.SessionDurationMS, first.SessionDurationMS)
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
	ctx, sess := sessionContext(t, "mcp-session-1")
	hooks.OnRegisterSession[0](ctx, sess)
	hooks.OnAfterCallTool[0](ctx, 1, callRequest("search_dashboards"), &mcp.CallToolResult{})

	r.flushAll(context.Background(), ReasonInterval)

	r.cfg.Endpoint = c.URL
	r.flushAll(context.Background(), ReasonInterval)

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
	ctx, sess := sessionContext(t, "mcp-session-1")
	hooks.OnRegisterSession[0](ctx, sess)

	hooks.OnAfterCallTool[0](ctx, 1, callRequest("search_dashboards"), &mcp.CallToolResult{})
	hooks.OnAfterCallTool[0](ctx, 1, callRequest("tempo_traceql-search"), &mcp.CallToolResult{})
	hooks.OnAfterCallTool[0](ctx, 1, callRequest("tempo_get-trace"), &mcp.CallToolResult{IsError: true})
	hooks.OnError[0](ctx, 1, "tools/call", callRequest("loki_some-remote-tool"), assert.AnError)

	r.flushAll(context.Background(), ReasonInterval)
	require.Len(t, c.received(), 1)
	e := c.received()[0]

	assert.Equal(t, map[string]ToolCount{
		"search_dashboards": {Calls: 1},
		ProxiedToolName:     {Calls: 3, Errors: 2},
	}, e.ToolCalls)
	assert.Equal(t, "proxied,search_dashboards", e.ToolsCalled)

	body, err := json.Marshal(e)
	require.NoError(t, err)
	for _, forbidden := range []string{"traceql", "get-trace", "tempo", "some-remote-tool"} {
		assert.NotContains(t, string(body), forbidden)
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
	ctx, sess := sessionContext(t, "mcp-session-1")
	hooks.OnRegisterSession[0](ctx, sess)
	hooks.OnAfterCallTool[0](ctx, 1, callRequest("search_dashboards"), &mcp.CallToolResult{})

	r.flushAll(context.Background(), ReasonSessionEnd)

	assert.Empty(t, c.received(), "log mode must not send")
	assert.Contains(t, out.String(), "usage statistics (not sent):")

	var e Event
	_, payload, found := bytes.Cut(out.Bytes(), []byte(": "))
	require.True(t, found)
	require.NoError(t, json.Unmarshal(bytes.TrimSpace(payload), &e))
	assert.Equal(t, ToolCount{Calls: 1}, e.ToolCalls["search_dashboards"])
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
	assert.Empty(t, hooks.OnRegisterSession)
	assert.Empty(t, hooks.OnAfterCallTool)
	assert.Empty(t, hooks.OnError)

	r.Start(context.Background())
	r.Shutdown()
	assert.Empty(t, c.received())
}

func TestSessionEndFlushOnUnregister(t *testing.T) {
	c := newCollector(t)
	r := New(Config{
		Mode:        ModeEnabled,
		Endpoint:    c.URL,
		NativeTools: observability.ValueSet("search_dashboards"),
	})
	hooks := r.Hooks()
	ctx, sess := sessionContext(t, "mcp-session-1")
	hooks.OnRegisterSession[0](ctx, sess)
	hooks.OnAfterInitialize[0](ctx, 1, &mcp.InitializeRequest{
		Params: mcp.InitializeParams{ClientInfo: mcp.Implementation{Name: "Claude-Code", Version: "2.0.1"}},
	}, &mcp.InitializeResult{})
	hooks.OnAfterCallTool[0](ctx, 1, callRequest("search_dashboards"), &mcp.CallToolResult{})

	hooks.OnUnregisterSession[0](ctx, sess)
	// The mid-life flush is backgrounded; Shutdown waits for it.
	r.Shutdown()

	require.Len(t, c.received(), 1)
	e := c.received()[0]
	assert.Equal(t, ReasonSessionEnd, e.ReportReason)
	assert.Equal(t, "claude-code", e.ClientName)
	assert.Equal(t, "2.0.1", e.ClientVersion)

	// The session is gone, so the shutdown flush must not report it twice.
	r2 := c.received()
	assert.Len(t, r2, 1)
}

// TestSessionIDIsNotTheTransportSessionID: the MCP session ID is chosen by the
// client and shared across processes under horizontal scaling, so the reported
// identity is minted here instead.
func TestSessionIDIsNotTheTransportSessionID(t *testing.T) {
	c := newCollector(t)
	r := New(Config{Mode: ModeEnabled, Endpoint: c.URL})
	hooks := r.Hooks()
	ctx, sess := sessionContext(t, "client-chosen-session-id")
	hooks.OnRegisterSession[0](ctx, sess)

	r.flushAll(context.Background(), ReasonInterval)
	require.Len(t, c.received(), 1)
	assert.NotEqual(t, "client-chosen-session-id", c.received()[0].SessionID)
	assert.NotEmpty(t, c.received()[0].SessionID)
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
	ctx, sess := sessionContext(t, "mcp-session-1")
	hooks.OnRegisterSession[0](ctx, sess)

	r.flushAll(context.Background(), ReasonInterval)
	require.Len(t, c.received(), 1)
	e := c.received()[0]

	assert.Equal(t, TargetKindCloud, e.TargetKind)
	assert.Equal(t, "12.1.0", e.GrafanaVersion)
	assert.True(t, e.OrgIDSet)
	assert.Equal(t, AuthMethodServiceAccountToken, e.AuthMethod)

	body, err := json.Marshal(e)
	require.NoError(t, err)
	assert.NotContains(t, string(body), "secret-stack-name")
	assert.NotContains(t, string(body), "grafana.net")
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
	ctx, sess := sessionContext(t, "mcp-session-1")
	hooks.OnRegisterSession[0](ctx, sess)

	r.flushAll(context.Background(), ReasonInterval)
	require.Len(t, c.received(), 1)
	assert.Equal(t, observability.ValueOther, c.received()[0].AuthMethod)
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
	hooks := r.Hooks()
	ctx, sess := sessionContext(t, "mcp-session-1")
	hooks.OnRegisterSession[0](ctx, sess)

	r.flushAll(context.Background(), ReasonInterval)
	require.Len(t, c.received(), 1)
	e := c.received()[0]

	assert.Equal(t, ServiceName, e.Service)
	assert.Equal(t, "v1.2.3", e.Version)
	assert.Equal(t, "streamable-http", e.Transport)
	assert.Equal(t, "instructions-append,server-name,transport", e.Flags)
	assert.Equal(t, "alerting,search", e.EnabledTools)
	assert.Equal(t, "oncall", e.DisabledTools)
}

// TestShutdownFlushIsCapped: shutdown must not wait on a telemetry endpoint
// that accepts the connection and then stalls.
func TestShutdownFlushIsCapped(t *testing.T) {
	release := make(chan struct{})
	stalled := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
	}))
	t.Cleanup(func() {
		close(release)
		stalled.Close()
	})

	r := New(Config{Mode: ModeEnabled, Endpoint: stalled.URL})
	hooks := r.Hooks()
	ctx, sess := sessionContext(t, "mcp-session-1")
	hooks.OnRegisterSession[0](ctx, sess)

	start := time.Now()
	r.Shutdown()
	assert.Less(t, time.Since(start), 3*time.Second, "shutdown flush should give up after about a second")
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

// TestEmptyFieldsAreOmittedNotSentEmpty: an unrecognised client and a session
// that called no tools must read as NULL at the receiver, not as an empty
// string and an empty object. Otherwise "which sessions called no tools" and
// "which clients were unrecognised" are unanswerable.
func TestEmptyFieldsAreOmittedNotSentEmpty(t *testing.T) {
	c := newCollector(t)
	r := New(Config{Mode: ModeEnabled, Endpoint: c.URL})
	hooks := r.Hooks()
	ctx, sess := sessionContext(t, "mcp-session-1")
	hooks.OnRegisterSession[0](ctx, sess)
	hooks.OnAfterInitialize[0](ctx, 1, &mcp.InitializeRequest{
		Params: mcp.InitializeParams{ClientInfo: mcp.Implementation{Name: "some-internal-agent", Version: "9.9.9"}},
	}, &mcp.InitializeResult{})

	r.flushAll(context.Background(), ReasonSessionEnd)
	require.Len(t, c.rawBodies(), 1)
	body := c.rawBodies()[0]

	assert.NotContains(t, body, "client_version")
	assert.NotContains(t, body, "tool_calls")
	assert.NotContains(t, body, "tools_called")
	// The clamped name is still reported: "an unrecognised client connected"
	// is the fact the field exists to record.
	assert.Contains(t, body, `"client_name":"other"`)
}

// TestEmptyToolCallsOmittedOnEveryFlush: the counters are reset to an
// initialised empty map after a flush, so the second flush must still omit
// rather than send {}.
func TestEmptyToolCallsOmittedOnEveryFlush(t *testing.T) {
	c := newCollector(t)
	r := New(Config{
		Mode:        ModeEnabled,
		Endpoint:    c.URL,
		NativeTools: observability.ValueSet("search_dashboards"),
	})
	hooks := r.Hooks()
	ctx, sess := sessionContext(t, "mcp-session-1")
	hooks.OnRegisterSession[0](ctx, sess)
	hooks.OnAfterCallTool[0](ctx, 1, callRequest("search_dashboards"), &mcp.CallToolResult{})

	r.flushAll(context.Background(), ReasonInterval)
	r.flushAll(context.Background(), ReasonInterval)

	require.Len(t, c.rawBodies(), 2)
	assert.Contains(t, c.rawBodies()[0], "search_dashboards")
	assert.NotContains(t, c.rawBodies()[1], "tool_calls")
	assert.NotContains(t, c.rawBodies()[1], "tools_called")
}

// TestGrafanaVersionIsLengthCapped: the instance's reported version is as much
// outside this binary's control as the client's, and forks set it freely.
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
	ctx, sess := sessionContext(t, "mcp-session-1")
	hooks.OnRegisterSession[0](ctx, sess)

	r.flushAll(context.Background(), ReasonInterval)
	require.Len(t, c.received(), 1)
	assert.Len(t, c.received()[0].GrafanaVersion, maxVersionLen)
}

// TestTargetFuncIsNotCalledUnderTheSessionLock: a Target implementation that
// blocks must not be able to block a concurrent flush, because the shutdown
// flush is on the process's exit path.
func TestTargetFuncIsNotCalledUnderTheSessionLock(t *testing.T) {
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
	ctx, sess := sessionContext(t, "mcp-session-1")

	go hooks.OnRegisterSession[0](ctx, sess)
	<-entered

	// The session is not yet stored, so flush sees nothing; the point is that
	// it returns rather than blocking behind the stalled Target call.
	done := make(chan struct{})
	go func() {
		defer close(done)
		r.flushAll(context.Background(), ReasonInterval)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("flush blocked behind the Target call")
	}
	close(release)
}
