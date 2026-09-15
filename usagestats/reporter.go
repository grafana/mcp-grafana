package usagestats

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"os"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/grafana/mcp-grafana/observability"
)

const (
	// DefaultInterval is how often a still-live MCP session reports a delta.
	DefaultInterval = 4 * time.Hour

	// intervalJitter is the fraction of DefaultInterval applied as random
	// jitter to the FIRST tick only, so a fleet of servers started together by
	// an orchestrator does not report in lockstep.
	intervalJitter = 0.10

	// sendTimeout caps a single background send.
	sendTimeout = 5 * time.Second

	// shutdownFlushTimeout caps the whole synchronous shutdown flush,
	// regardless of how many sessions it covers. Shutdown must not be held up
	// by telemetry.
	shutdownFlushTimeout = time.Second
)

// ProxiedToolName is the single pseudo-name every proxied tool call is
// recorded under, in both tools_called and tool_calls.
//
// Proxied tool names are discovered from a remote MCP server, so they are not
// authored in this repo and cannot be bounded by it. Neither the tool's real
// name nor the datasource type it came from is ever reported: the datasource
// type would name which backends an operator runs, and a single key keeps the
// map's shape independent of whatever the remote server happens to expose.
const ProxiedToolName = "proxied"

// TargetFunc resolves the Grafana target fields for a session from that
// session's context.
//
// It runs on the initialize and tool-call paths, so it must not issue network
// requests, block, or do anything else whose cost the caller would notice:
// reporting is not allowed to slow down a tool call or to add traffic to the
// operator's Grafana. It may only read what is already resolved or already
// cached, and return zero values for the rest.
type TargetFunc func(ctx context.Context) GrafanaTarget

// Config is the static half of a report: everything that is fixed for the
// process. The per-session half is gathered by the hooks.
type Config struct {
	Mode     Mode
	Endpoint string
	Logger   *slog.Logger

	// Version is the mcp-grafana version.
	Version string

	// Transport is the MCP transport name ("stdio", "sse", "streamable-http").
	Transport string

	// Flags holds the NAMES of the flags explicitly set on the command line.
	// Never their values.
	Flags []string

	// EnabledTools and DisabledTools hold tool category names.
	EnabledTools  []string
	DisabledTools []string

	LokiGuardrailMode string
	TLSEnabled        bool
	MetricsEnabled    bool
	DynamicMultiOrg   bool
	ProxiedEnabled    bool

	// NativeTools is the set of tool names this server registered itself. It
	// is the allowlist for tool_calls keys: anything outside it is recorded as
	// ProxiedToolName. A nil or empty set therefore reports every call as
	// proxied, which is the safe direction to fail in.
	//
	// The server's tool registrations are the single definition of this set;
	// it is not restated here. SetNativeTools supplies it when the server is
	// built after the Reporter, which it must be because the Reporter provides
	// the server's hooks.
	NativeTools map[string]struct{}

	// Target resolves the per-session Grafana target. A nil Target leaves the
	// Grafana fields empty.
	Target TargetFunc

	// Interval overrides DefaultInterval. Tests only.
	Interval time.Duration

	// LogOutput is where ModeLog writes. Defaults to os.Stderr: under the
	// stdio transport, stdout carries the MCP protocol and must never be
	// written to.
	LogOutput io.Writer

	// HTTPClient overrides the client used to POST reports. Tests only.
	HTTPClient *http.Client
}

// sessionCounters is the mutable per-session state. Counters are deltas: a
// flush takes and resets them, so a report that fails to send loses its delta
// rather than double-counting it into the next one.
type sessionCounters struct {
	mu sync.Mutex

	sessionID string
	startedAt time.Time

	clientName    string
	clientVersion string

	grafanaVersion string
	targetKind     string
	orgIDSet       bool
	authMethod     string

	tools map[string]ToolCount
}

// Reporter accumulates per-session usage and flushes it to the endpoint.
// A Reporter whose mode is ModeDisabled builds nothing and opens no
// connection; its Hooks are empty and its Start and Shutdown are no-ops.
type Reporter struct {
	cfg       Config
	processID string
	client    *http.Client
	logOutput io.Writer
	interval  time.Duration

	nativeTools atomic.Pointer[map[string]struct{}]

	sessions sync.Map // MCP session ID -> *sessionCounters

	startOnce sync.Once
	stopOnce  sync.Once
	stop      chan struct{}

	// inFlight tracks the background sends started by mid-life session-end and
	// interval flushes, so Shutdown does not race them.
	inFlight sync.WaitGroup
}

// New builds a Reporter. It is safe to call with ModeDisabled and use the
// result unconditionally.
func New(cfg Config) *Reporter {
	r := &Reporter{
		cfg:       cfg,
		processID: uuid.NewString(),
		client:    cfg.HTTPClient,
		logOutput: cfg.LogOutput,
		interval:  cfg.Interval,
		stop:      make(chan struct{}),
	}
	if r.cfg.Logger == nil {
		r.cfg.Logger = slog.Default()
	}
	if r.client == nil {
		r.client = &http.Client{Timeout: sendTimeout}
	}
	if r.logOutput == nil {
		r.logOutput = os.Stderr
	}
	if r.interval <= 0 {
		r.interval = DefaultInterval
	}
	if r.cfg.Endpoint == "" {
		r.cfg.Endpoint = DefaultEndpoint
	}
	r.SetNativeTools(cfg.NativeTools)
	return r
}

// SetNativeTools supplies the set of tool names this server registered, which
// bounds the keys of tool_calls. Call it once the server's tools are
// registered and before it starts serving; until then every call is reported
// as ProxiedToolName.
func (r *Reporter) SetNativeTools(names map[string]struct{}) {
	r.nativeTools.Store(&names)
}

// Enabled reports whether anything is collected at all.
//
// Only an explicitly enabling mode counts, so the zero-valued Config is inert.
// Testing for "not disabled" instead would make Config{} — and any future
// caller that forgets to set Mode — report to the live endpoint, which is the
// one direction this package must never fail in.
func (r *Reporter) Enabled() bool {
	if r == nil {
		return false
	}
	return r.cfg.Mode == ModeEnabled || r.cfg.Mode == ModeLog
}

// Disclose logs the one startup line that tells the operator usage statistics
// are being collected and how to turn them off. Nothing is persisted, so
// "once" simply means once per process start.
//
// It logs at INFO through slog, which every transport routes to stderr. Under
// stdio that stderr is the MCP client's log file rather than a terminal, so
// the operator who configured the client is the one who sees it.
func (r *Reporter) Disclose() {
	if !r.Enabled() {
		return
	}
	r.cfg.Logger.Info("Anonymous usage statistics are being reported to Grafana Labs: MCP session and tool-usage counts, server configuration by flag name, and a coarse description of the Grafana target. No Grafana URL, credentials, tool arguments or resource names are sent. Opt out with --usage-stats=disabled or "+ModeEnvVar+"=disabled, or inspect what would be sent with "+ModeEnvVar+"=log.",
		"mode", string(r.cfg.Mode),
		"docs", "https://grafana.com/docs/mcp-grafana/latest/anonymous-usage-statistics/",
	)
}

// Start launches the interval flusher. It returns immediately; the goroutine
// exits when ctx is cancelled or Shutdown is called.
func (r *Reporter) Start(ctx context.Context) {
	if !r.Enabled() {
		return
	}
	r.startOnce.Do(func() {
		go r.runInterval(ctx)
	})
}

func (r *Reporter) runInterval(ctx context.Context) {
	// Jitter the first tick only: subsequent ticks inherit the offset, so one
	// randomisation is enough to spread a fleet out permanently.
	timer := time.NewTimer(jitterDuration(r.interval))
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-r.stop:
			return
		case <-timer.C:
			r.flushAll(ctx, ReasonInterval)
			timer.Reset(r.interval)
		}
	}
}

// jitterDuration applies +/-intervalJitter to d.
func jitterDuration(d time.Duration) time.Duration {
	offset := (rand.Float64()*2 - 1) * intervalJitter
	return time.Duration(float64(d) * (1 + offset))
}

// Shutdown stops the interval flusher and flushes every still-live session
// with ReasonSessionEnd, synchronously and under a one-second cap for the
// whole operation. It is safe to call more than once.
func (r *Reporter) Shutdown() {
	if !r.Enabled() {
		return
	}
	r.stopOnce.Do(func() {
		close(r.stop)

		ctx, cancel := context.WithTimeout(context.Background(), shutdownFlushTimeout)
		defer cancel()

		// The whole flush is inside the cap, not just the HTTP sends. Building
		// an event takes each session's counter lock, and in log mode writing
		// one goes to stderr — a pipe whose reader is the MCP client and may
		// have stopped draining it. Either can block on something outside this
		// process's control, so the cap has to cover them too, which means
		// running them where a deadline can abandon them.
		done := make(chan struct{})
		go func() {
			defer close(done)
			r.flushAll(ctx, ReasonSessionEnd)
			r.inFlight.Wait()
		}()
		select {
		case <-done:
		case <-ctx.Done():
			// Abandoned, not cancelled: the goroutine may still be blocked on
			// a lock or a write. The process is on its way out, so leaving it
			// is correct — shutdown must not wait on telemetry.
		}
	})
}

// Hooks returns the MCP server hooks that collect usage. Merge them with the
// server's other hooks via observability.MergeHooks.
func (r *Reporter) Hooks() *server.Hooks {
	if !r.Enabled() {
		return &server.Hooks{}
	}
	return &server.Hooks{
		OnRegisterSession: []server.OnRegisterSessionHookFunc{
			func(ctx context.Context, session server.ClientSession) {
				r.startSession(ctx, session.SessionID())
			},
		},
		OnAfterInitialize: []server.OnAfterInitializeFunc{
			func(ctx context.Context, id any, message *mcp.InitializeRequest, result *mcp.InitializeResult) {
				if message == nil {
					return
				}
				session := server.ClientSessionFromContext(ctx)
				if session == nil {
					return
				}
				r.recordClientInfo(ctx, session.SessionID(), message.Params.ClientInfo)
			},
		},
		OnUnregisterSession: []server.OnUnregisterSessionHookFunc{
			func(ctx context.Context, session server.ClientSession) {
				r.endSession(session.SessionID())
			},
		},
		OnAfterCallTool: []server.OnAfterCallToolFunc{
			func(ctx context.Context, id any, message *mcp.CallToolRequest, result any) {
				if message == nil {
					return
				}
				r.recordToolCall(ctx, message.Params.Name, isErrorResult(result))
			},
		},
		OnError: []server.OnErrorHookFunc{
			func(ctx context.Context, id any, method mcp.MCPMethod, message any, err error) {
				// A tools/call that never reached OnAfterCallTool: the handler
				// returned an error, or the tool does not exist.
				if method != "tools/call" {
					return
				}
				req, ok := message.(*mcp.CallToolRequest)
				if !ok || req == nil {
					return
				}
				// mcp-go calls onError with a zero-valued request when the
				// tools capability is unsupported or the tools/call payload
				// fails to unmarshal. No tool was named, so there is nothing
				// to attribute; counting it would report a phantom proxied
				// call, since toolKey("") falls through to the sentinel.
				if req.Params.Name == "" {
					return
				}
				r.recordToolCall(ctx, req.Params.Name, true)
			},
		},
	}
}

// isErrorResult reports whether a tool returned an error result. A tool that
// answers with isError=true succeeded at the protocol level but failed at the
// task, which is what "which tools error" is asking about.
func isErrorResult(result any) bool {
	res, ok := result.(*mcp.CallToolResult)
	return ok && res != nil && res.IsError
}

// startSession mints this session's telemetry identity and resolves the
// Grafana target fields.
//
// The session ID is a fresh random UUID, never the MCP transport's session ID:
// that one is chosen by, and visible to, the client, and under horizontal
// scaling it is shared across processes.
func (r *Reporter) startSession(ctx context.Context, mcpSessionID string) {
	if !r.Enabled() {
		return
	}
	// An empty MCP session ID is not a session this package can account for.
	// mcp-go's stateless session ID manager generates "" (streamable_http.go
	// :2043), and a GET with no session header then stores every such client
	// under that one key (:1058), so distinct clients would share a single
	// sessionCounters and report as one session with their tool calls merged.
	// Reporting nothing for them is the honest outcome; WarnSessionCoverage
	// tells the operator at startup.
	if mcpSessionID == "" {
		return
	}
	sc := &sessionCounters{
		sessionID: uuid.NewString(),
		startedAt: time.Now(),
		tools:     map[string]ToolCount{},
	}
	sc.applyTarget(r.resolveTarget(ctx))
	r.sessions.Store(mcpSessionID, sc)
}

func (r *Reporter) recordClientInfo(ctx context.Context, mcpSessionID string, info mcp.Implementation) {
	sc, ok := r.session(mcpSessionID)
	if !ok {
		return
	}
	name := ClientName(info.Name)
	// The initialize request is the earliest point at which every transport's
	// context carries the resolved Grafana configuration, so a session that
	// never calls a tool still reports its target. Resolved before taking the
	// lock: nothing outside this package runs while a session's counters are
	// held.
	target := r.resolveTarget(ctx)

	sc.mu.Lock()
	defer sc.mu.Unlock()
	sc.clientName = name
	sc.clientVersion = ClientVersion(name, info.Version)
	sc.applyTarget(target)
}

func (r *Reporter) recordToolCall(ctx context.Context, toolName string, failed bool) {
	sc, ok := r.sessionFromContext(ctx)
	if !ok {
		return
	}
	key := r.toolKey(toolName)
	target := r.resolveTarget(ctx)

	sc.mu.Lock()
	defer sc.mu.Unlock()
	c := sc.tools[key]
	c.Calls++
	if failed {
		c.Errors++
	}
	sc.tools[key] = c
	sc.applyTarget(target)
}

// resolveTarget reads the Grafana target for a session, outside any lock.
//
// It cannot be done once at session start: the stdio transport registers its
// session before the Grafana configuration is attached to the context, and the
// Grafana version is only known once something else has fetched it. So the
// target is re-read on initialize and on each tool call, and applyTarget fills
// in each field once there is something to fill it with.
func (r *Reporter) resolveTarget(ctx context.Context) GrafanaTarget {
	if r.cfg.Target == nil {
		return GrafanaTarget{}
	}
	return r.cfg.Target(ctx)
}

// applyTarget updates the target fields that t can answer, leaving the rest as
// they were. The caller holds sc.mu.
func (sc *sessionCounters) applyTarget(t GrafanaTarget) {
	if t.URL != "" {
		sc.targetKind = TargetKind(t.URL)
		sc.orgIDSet = t.OrgIDSet
		sc.authMethod = observability.BoundedValue(t.AuthMethod, authMethods)
	}
	if v := truncateRunes(t.Version, maxVersionLen); v != "" {
		sc.grafanaVersion = v
	}
}

// toolKey maps a called tool name onto the reportable key: the name itself
// when this server registered it, else ProxiedToolName.
//
// The sentinel differs from observability.ValueOther on purpose. "other" means
// "a value outside a vocabulary this repo controls"; a proxied tool name is
// outside the vocabulary because another server chose it, which is a different
// fact and one the docs page describes separately.
func (r *Reporter) toolKey(name string) string {
	if native := r.nativeTools.Load(); native != nil {
		if _, ok := (*native)[name]; ok {
			return name
		}
	}
	return ProxiedToolName
}

func (r *Reporter) session(mcpSessionID string) (*sessionCounters, bool) {
	v, ok := r.sessions.Load(mcpSessionID)
	if !ok {
		return nil, false
	}
	sc, ok := v.(*sessionCounters)
	return sc, ok
}

func (r *Reporter) sessionFromContext(ctx context.Context) (*sessionCounters, bool) {
	session := server.ClientSessionFromContext(ctx)
	if session == nil {
		return nil, false
	}
	return r.session(session.SessionID())
}

// endSession flushes a session that ended while the server is still running.
// The send is backgrounded: OnUnregisterSession runs on the transport's
// teardown path, which must not wait on a network round trip.
func (r *Reporter) endSession(mcpSessionID string) {
	v, ok := r.sessions.LoadAndDelete(mcpSessionID)
	if !ok {
		return
	}
	sc, ok := v.(*sessionCounters)
	if !ok {
		return
	}
	event := r.buildEvent(sc, ReasonSessionEnd)
	r.inFlight.Add(1)
	go func() {
		defer r.inFlight.Done()
		ctx, cancel := context.WithTimeout(context.Background(), sendTimeout)
		defer cancel()
		r.send(ctx, event)
	}()
}

// flushAll reports every live session. Sessions are kept for a session_end
// flush; only their counters are reset.
func (r *Reporter) flushAll(ctx context.Context, reason string) {
	var events []Event
	r.sessions.Range(func(_, v any) bool {
		if ctx.Err() != nil {
			return false
		}
		if sc, ok := v.(*sessionCounters); ok {
			events = append(events, r.buildEvent(sc, reason))
		}
		return true
	})
	for _, e := range events {
		// A deadline reached partway through stops the remaining sends rather
		// than issuing them against an already-expired context. Their deltas
		// are lost, which is the documented behaviour of a flush that does not
		// land.
		if ctx.Err() != nil {
			return
		}
		r.send(ctx, e)
	}
}

// buildEvent snapshots the session's counters into an event and resets them.
// The reset happens here rather than after a successful send, so a report that
// never arrives loses its delta: totals are a floor, never a count.
func (r *Reporter) buildEvent(sc *sessionCounters, reason string) Event {
	sc.mu.Lock()
	// A flush with no tool calls reports a nil map, not an empty one, so the
	// field is omitted rather than sent as {}.
	var tools map[string]ToolCount
	if len(sc.tools) > 0 {
		tools = sc.tools
		sc.tools = map[string]ToolCount{}
	}
	e := Event{
		Service:           ServiceName,
		Version:           r.cfg.Version,
		OS:                runtime.GOOS,
		Arch:              runtime.GOARCH,
		ProcessID:         r.processID,
		SessionID:         sc.sessionID,
		ReportReason:      reason,
		SessionDurationMS: time.Since(sc.startedAt).Milliseconds(),
		ClientName:        sc.clientName,
		ClientVersion:     sc.clientVersion,
		ToolCalls:         tools,
		GrafanaVersion:    sc.grafanaVersion,
		TargetKind:        sc.targetKind,
		OrgIDSet:          sc.orgIDSet,
		AuthMethod:        sc.authMethod,
		Transport:         r.cfg.Transport,
		Flags:             joinSorted(r.cfg.Flags),
		EnabledTools:      joinSorted(r.cfg.EnabledTools),
		DisabledTools:     joinSorted(r.cfg.DisabledTools),
		LokiGuardrailMode: r.cfg.LokiGuardrailMode,
		TLSEnabled:        r.cfg.TLSEnabled,
		MetricsEnabled:    r.cfg.MetricsEnabled,
		DynamicMultiOrg:   r.cfg.DynamicMultiOrg,
		ProxiedEnabled:    r.cfg.ProxiedEnabled,
	}
	sc.mu.Unlock()

	names := make([]string, 0, len(tools))
	for name := range tools {
		names = append(names, name)
	}
	e.ToolsCalled = joinSorted(names)
	return e
}

func joinSorted(values []string) string {
	if len(values) == 0 {
		return ""
	}
	out := make([]string, 0, len(values))
	for _, v := range values {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	sort.Strings(out)
	return strings.Join(out, ",")
}

// send delivers one event. A single attempt, never retried and never queued:
// a report that does not arrive is lost on purpose, because holding reports
// would make the server's behaviour depend on the reachability of a
// telemetry endpoint.
//
// Failures are logged at debug only, and nothing is ever written to stdout —
// under the stdio transport stdout is the MCP protocol channel.
func (r *Reporter) send(ctx context.Context, e Event) {
	// Checked here as well as at every entry point: this is the only place a
	// request is issued, so it is the one place where "disabled means nothing
	// leaves" can be guaranteed rather than assumed.
	if !r.Enabled() {
		return
	}
	body, err := json.Marshal(e)
	if err != nil {
		r.cfg.Logger.Debug("failed to encode usage statistics report", "error", err)
		return
	}

	if r.cfg.Mode == ModeLog {
		if _, err := fmt.Fprintf(r.logOutput, "usage statistics (not sent): %s\n", body); err != nil {
			r.cfg.Logger.Debug("failed to write usage statistics report", "error", err)
		}
		return
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.cfg.Endpoint, bytes.NewReader(body))
	if err != nil {
		r.cfg.Logger.Debug("failed to build usage statistics request", "error", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := r.client.Do(req)
	if err != nil {
		r.cfg.Logger.Debug("failed to send usage statistics report", "error", err)
		return
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode >= http.StatusBadRequest {
		r.cfg.Logger.Debug("usage statistics report rejected", "status", resp.StatusCode)
	}
}
