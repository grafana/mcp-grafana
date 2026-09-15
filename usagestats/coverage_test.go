//go:build unit

package usagestats

import (
	"bytes"
	"context"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSessionCoverageWarnings(t *testing.T) {
	// stdio and sse register a real session per connection, so there is
	// nothing to caveat.
	assert.Empty(t, SessionCoverageWarnings(TransportStdio, false))
	assert.Empty(t, SessionCoverageWarnings(TransportSSE, false))

	// Stateful streamable-http still misses clients on the protocol version
	// that removed sessions.
	stateful := SessionCoverageWarnings(TransportStreamableHTTP, false)
	require.Len(t, stateful, 1)
	assert.Contains(t, stateful[0], "under-report")
	assert.Contains(t, stateful[0], modernProtocolVersion)

	// Stateless collects nothing whatsoever, which is the louder of the two.
	stateless := SessionCoverageWarnings(TransportStreamableHTTP, true)
	require.Len(t, stateless, 2)
	assert.Contains(t, stateless[0], "no usage statistics will be collected at all")
}

func TestWarnSessionCoverageIsSilentWhenDisabled(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	New(Config{Mode: ModeDisabled, Logger: logger}).WarnSessionCoverage(TransportStreamableHTTP, true)
	assert.Empty(t, buf.String(), "nothing to caveat when nothing is collected by choice")

	New(Config{Mode: ModeEnabled, Logger: logger}).WarnSessionCoverage(TransportStreamableHTTP, true)
	assert.Contains(t, buf.String(), "no usage statistics will be collected at all")
}

// TestEmptySessionIDIsNotTracked: mcp-go's stateless session ID manager
// generates "", and a GET with no session header stores every such client
// under that one key. Tracking it would merge distinct clients into one
// reported session with their tool calls summed together.
func TestEmptySessionIDIsNotTracked(t *testing.T) {
	c := newCollector(t)
	r := New(Config{Mode: ModeEnabled, Endpoint: c.URL})
	hooks := r.Hooks()
	ctx, sess := sessionContext(t, "")

	hooks.OnRegisterSession[0](ctx, sess)
	hooks.OnAfterCallTool[0](ctx, 1, callRequest("search_dashboards"), nil)
	hooks.OnUnregisterSession[0](ctx, sess)
	r.Shutdown()

	assert.Empty(t, c.received(), "an unattributable session must not be reported")

	// And it left no state behind that a later flush could pick up.
	_, ok := r.session("")
	assert.False(t, ok)
}

// TestDistinctSessionsDoNotShareCounters guards the property the empty-ID
// check protects: two registered sessions must never merge.
func TestDistinctSessionsDoNotShareCounters(t *testing.T) {
	c := newCollector(t)
	r := New(Config{Mode: ModeEnabled, Endpoint: c.URL, NativeTools: map[string]struct{}{"search_dashboards": {}}})
	hooks := r.Hooks()

	ctxA, sessA := sessionContext(t, "session-a")
	ctxB, sessB := sessionContext(t, "session-b")
	hooks.OnRegisterSession[0](ctxA, sessA)
	hooks.OnRegisterSession[0](ctxB, sessB)
	hooks.OnAfterCallTool[0](ctxA, 1, callRequest("search_dashboards"), nil)

	r.flushAll(context.Background(), ReasonInterval)
	events := c.received()
	require.Len(t, events, 2)
	assert.NotEqual(t, events[0].SessionID, events[1].SessionID)

	var withCalls int
	for _, e := range events {
		if e.ToolCalls["search_dashboards"].Calls == 1 {
			withCalls++
		}
	}
	assert.Equal(t, 1, withCalls, "the call belongs to exactly one session")
}
