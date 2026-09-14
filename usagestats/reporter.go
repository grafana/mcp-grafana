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
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/grafana/mcp-grafana/observability"
)

const (
	// DefaultInterval is how often a running process reports a delta.
	DefaultInterval = 4 * time.Hour

	// intervalJitter is the fraction of DefaultInterval applied as random
	// jitter to the FIRST tick only, so a fleet of servers started together by
	// an orchestrator does not report in lockstep.
	intervalJitter = 0.10

	// sendTimeout caps a single background send.
	sendTimeout = 5 * time.Second

	// shutdownFlushTimeout caps the whole synchronous shutdown flush.
	// Shutdown must not be held up by telemetry.
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

// TargetFunc resolves the Grafana target fields from a request's context.
//
// It runs on the initialize and tool-call paths, so it must not issue network
// requests, block, or do anything else whose cost the caller would notice:
// reporting is not allowed to slow down a tool call or to add traffic to the
// operator's Grafana. It may only read what is already resolved or already
// cached, and return zero values for the rest.
type TargetFunc func(ctx context.Context) GrafanaTarget

// Config is the static half of a report: everything that is fixed for the
// process. The rest is accumulated by the middleware.
type Config struct {
	Mode     Mode
	Endpoint string
	Logger   *slog.Logger

	// Version is the mcp-grafana version.
	Version string

	// Transport is the MCP transport name ("stdio", "sse", "streamable-http").
	// Clamped to that vocabulary on the wire: it is read before the flag is
	// validated, so an invalid value reaches here.
	Transport string

	// EnvSet holds the comma-joined NAMES of the configuration environment
	// variables that are set, from usagestats.EnvSet. Never their values.
	EnvSet string

	// Flags holds the NAMES of the flags explicitly set on the command line.
	// Never their values.
	Flags []string

	// EnabledTools and DisabledTools hold tool category names.
	EnabledTools  []string
	DisabledTools []string

	TLSEnabled      bool
	MetricsEnabled  bool
	DynamicMultiOrg bool
	ProxiedEnabled  bool

	// NativeTools is the set of tool names this server registered itself. It
	// is the allowlist for tool_calls keys: anything outside it is recorded as
	// ProxiedToolName. A nil or empty set therefore reports every call as
	// proxied, which is the safe direction to fail in.
	//
	// The server's tool registrations are the single definition of this set;
	// it is not restated here. SetNativeTools supplies it when the server is
	// built after the Reporter, which it must be because the Reporter provides
	// the server's middleware.
	NativeTools map[string]struct{}

	// Target resolves the Grafana target. A nil Target leaves the Grafana
	// fields empty.
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

// counters is the process's accumulated usage.
//
// Two different lifetimes live here on purpose:
//
//   - Activity (tools) is a delta. A flush takes it and resets it, so a
//     report that fails to send loses its delta rather than double-counting
//     it into the next one.
//   - Description (grafanaVersions, authMethods, targetKind) is
//     cumulative for the process. These say what the process is talking to,
//     not what it did in a window, and a window with no new initialize must
//     not forget them. Keeping the version and auth sets cumulative is also
//     what makes "the process resolved more than one value" a stable fact
//     rather than one that depends on flush timing.
type counters struct {
	mu sync.Mutex

	tools map[string]ToolCount

	grafanaVersions map[string]struct{}
	authMethods     map[string]struct{}
	targetKind      string
}

func newCounters() *counters {
	return &counters{
		tools:           map[string]ToolCount{},
		grafanaVersions: map[string]struct{}{},
		authMethods:     map[string]struct{}{},
	}
}

// Reporter accumulates this process's usage and flushes it to the endpoint.
// A Reporter whose mode is not explicitly enabling collects nothing and opens
// no connection; its middleware is a no-op and its Start and Shutdown are
// no-ops.
//
// The unit is the process rather than the MCP session because a session is not
// something every transport has. The go-sdk's middleware fires on every
// request regardless of session state, so counting at process scope sees
// every call.
type Reporter struct {
	cfg       Config
	processID string
	startedAt time.Time
	client    *http.Client
	logOutput io.Writer
	interval  time.Duration

	nativeTools atomic.Pointer[map[string]struct{}]

	// reportSeq numbers this process's reports from 1. Incremented as each
	// event is built, so a gap identifies a report that was built and lost
	// rather than one that was never made.
	reportSeq atomic.Int64
	counters  *counters

	startOnce sync.Once
	stopOnce  sync.Once
	stop      chan struct{}
}

// New builds a Reporter. It is safe to call with a disabled mode and use the
// result unconditionally.
func New(cfg Config) *Reporter {
	r := &Reporter{
		cfg:       cfg,
		processID: uuid.NewString(),
		startedAt: time.Now(),
		client:    cfg.HTTPClient,
		logOutput: cfg.LogOutput,
		interval:  cfg.Interval,
		counters:  newCounters(),
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
	r.cfg.Logger.Info("Anonymous usage statistics are being reported to Grafana Labs: per-process tool-usage counts, server configuration by flag name, and a coarse description of the Grafana target. Nothing is collected about the MCP clients that connect, and no Grafana URL, credentials, tool arguments or resource names are sent. Opt out with --usage-stats=disabled, "+ModeEnvVar+"=disabled or "+DoNotTrackEnvVar+"=1, or inspect what would be sent with "+ModeEnvVar+"=log.",
		"mode", string(r.cfg.Mode),
		"docs", "https://grafana.com/docs/grafana/latest/developer-resources/mcp/anonymous-usage-statistics/",
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
			sendCtx, cancel := context.WithTimeout(ctx, sendTimeout)
			r.flush(sendCtx, ReasonInterval)
			cancel()
			timer.Reset(r.interval)
		}
	}
}

// jitterDuration applies +/-intervalJitter to d.
func jitterDuration(d time.Duration) time.Duration {
	offset := (rand.Float64()*2 - 1) * intervalJitter
	return time.Duration(float64(d) * (1 + offset))
}

// Shutdown stops the interval flusher and sends the process's final report,
// synchronously and under a one-second cap. It is safe to call more than once.
func (r *Reporter) Shutdown() {
	if !r.Enabled() {
		return
	}
	r.stopOnce.Do(func() {
		close(r.stop)

		ctx, cancel := context.WithTimeout(context.Background(), shutdownFlushTimeout)
		defer cancel()

		// The whole flush is inside the cap, not just the HTTP send. Building
		// the event takes the counter lock, and in log mode writing it goes to
		// stderr — a pipe whose reader is the MCP client and may have stopped
		// draining it. Either can block on something outside this process's
		// control, so the cap has to cover them too, which means running them
		// where a deadline can abandon them.
		done := make(chan struct{})
		go func() {
			defer close(done)
			r.flush(ctx, ReasonShutdown)
		}()
		select {
		case <-done:
		case <-ctx.Done():
			// Abandoned, not cancelled: the goroutine may still be blocked on
			// the lock or a write. The process is on its way out, so leaving
			// it is correct — shutdown must not wait on telemetry.
		}
	})
}

// MCPMiddleware returns an mcp.Middleware that collects usage statistics.
//
// There are no session hooks: the unit is the process, and depending on
// session registration is exactly what would blind this on the streamable-http
// transport (see the Reporter doc comment).
func (r *Reporter) MCPMiddleware() mcp.Middleware {
	if !r.Enabled() {
		return func(next mcp.MethodHandler) mcp.MethodHandler { return next }
	}
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			if method == "initialize" {
				result, err := next(ctx, method, req)
				r.recordInitialize(ctx)
				return result, err
			}
			if method == "tools/call" {
				callReq, ok := req.(*mcp.CallToolRequest)
				result, err := next(ctx, method, req)
				if err != nil {
					if ok && callReq != nil && callReq.Params.Name != "" {
						r.recordToolCall(ctx, callReq.Params.Name, true)
					}
					return result, err
				}
				if ok && callReq != nil {
					r.recordToolCall(ctx, callReq.Params.Name, isErrorResult(result))
				}
				return result, err
			}
			return next(ctx, method, req)
		}
	}
}

// isErrorResult reports whether a tool returned an error result. A tool that
// answers with isError=true succeeded at the protocol level but failed at the
// task, which is what "which tools error" is asking about.
func isErrorResult(result mcp.Result) bool {
	res, ok := result.(*mcp.CallToolResult)
	return ok && res != nil && res.IsError
}

// recordInitialize folds the Grafana target into the process description. The
// initialize request is the earliest point at which every transport's context
// carries the resolved Grafana configuration, which is the only reason this
// hook exists — no property of the connecting client is recorded. Resolved
// before taking the lock: nothing outside this package runs while the counters
// are held.
func (r *Reporter) recordInitialize(ctx context.Context) {
	target := r.resolveTarget(ctx)

	c := r.counters
	c.mu.Lock()
	defer c.mu.Unlock()
	c.applyTarget(target)
}

func (r *Reporter) recordToolCall(ctx context.Context, toolName string, failed bool) {
	key := r.toolKey(toolName)
	target := r.resolveTarget(ctx)

	c := r.counters
	c.mu.Lock()
	defer c.mu.Unlock()
	tc := c.tools[key]
	tc.Calls++
	if failed {
		tc.Errors++
	}
	c.tools[key] = tc
	c.applyTarget(target)
}

// resolveTarget reads the Grafana target, outside any lock.
//
// It cannot be done once at startup: the stdio transport attaches its Grafana
// configuration to the context after the server is built, and the Grafana
// version is only known once something else has fetched it. So the target is
// re-read on initialize and on each tool call, and applyTarget accumulates
// what it can answer.
func (r *Reporter) resolveTarget(ctx context.Context) GrafanaTarget {
	if r.cfg.Target == nil {
		return GrafanaTarget{}
	}
	return r.cfg.Target(ctx)
}

// applyTarget folds one request's target into the process's description. The
// caller holds c.mu.
//
// The version and auth method accumulate as sets, because a multi-tenant HTTP
// process can resolve either differently per request; the event reports one
// only when the set holds exactly one. targetKind comes from GRAFANA_URL,
// which extractKeyGrafanaInfoFromReq never takes from a header, so it cannot
// vary within a process.
func (c *counters) applyTarget(t GrafanaTarget) {
	if t.URL != "" {
		c.targetKind = TargetKind(t.URL)
		if m := observability.BoundedValue(t.AuthMethod, authMethods); m != "" {
			c.authMethods[m] = struct{}{}
		}
	}
	if v := truncateRunes(t.Version, maxVersionLen); v != "" {
		c.grafanaVersions[v] = struct{}{}
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

// flush builds one event for the process and sends it.
func (r *Reporter) flush(ctx context.Context, reason string) {
	if !r.Enabled() {
		return
	}
	r.send(ctx, r.buildEvent(reason))
}

// buildEvent snapshots the counters into an event and resets the activity
// half. The reset happens here rather than after a successful send, so a
// report that never arrives loses its delta: totals are a floor, never a
// count.
func (r *Reporter) buildEvent(reason string) Event {
	c := r.counters

	c.mu.Lock()
	// A flush with no tool calls reports a nil map, not an empty one, so the
	// field is omitted rather than sent as {}.
	var tools map[string]ToolCount
	if len(c.tools) > 0 {
		tools = c.tools
		c.tools = map[string]ToolCount{}
	}
	e := Event{
		Service:         ServiceName,
		Version:         r.cfg.Version,
		OS:              runtime.GOOS,
		Arch:            runtime.GOARCH,
		ProcessID:       r.processID,
		ReportReason:    reason,
		ProcessUptimeMS: time.Since(r.startedAt).Milliseconds(),
		ReportSeq:       r.reportSeq.Add(1),
		ToolCalls:       tools,
		GrafanaVersion:  soleValue(c.grafanaVersions),
		TargetKind:      c.targetKind,
		AuthMethod:      soleValue(c.authMethods),
		Transport:       observability.BoundedValue(r.cfg.Transport, transports),
		Flags:           joinSorted(r.cfg.Flags),
		EnvSet:          r.cfg.EnvSet,
		EnabledTools:    joinSorted(r.cfg.EnabledTools),
		DisabledTools:   joinSorted(r.cfg.DisabledTools),
		TLSEnabled:      r.cfg.TLSEnabled,
		MetricsEnabled:  r.cfg.MetricsEnabled,
		DynamicMultiOrg: r.cfg.DynamicMultiOrg,
		ProxiedEnabled:  r.cfg.ProxiedEnabled,
	}
	c.mu.Unlock()

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
