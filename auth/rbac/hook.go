package rbac

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// EngineConfig wires the pieces together.
type EngineConfig struct {
	Mode  Mode
	Cache *Cache
	Gate  *Gate

	// KeyFromContext returns the per-session cache key (typically the hashed
	// MCP access token from the auth middleware) and reports whether a session
	// is on the context. When false, the hook passes the result through
	// unchanged — this preserves the legacy/no-auth path.
	KeyFromContext func(ctx context.Context) (string, bool)

	Metrics *Metrics
	Logger  *slog.Logger
}

// Engine resolves the runtime mode (Auto → Enterprise/Basic) lazily on first
// successful permission fetch and applies the gate.
type Engine struct {
	cfg EngineConfig

	mu       sync.RWMutex
	resolved Mode // ModeAuto until we learn the edition; then Enterprise or Basic
}

// NewEngine builds an Engine.
func NewEngine(cfg EngineConfig) *Engine {
	return &Engine{cfg: cfg, resolved: cfg.Mode}
}

func (e *Engine) effectiveMode() Mode {
	e.mu.RLock()
	m := e.resolved
	e.mu.RUnlock()
	if m == ModeAuto {
		return ModeAuto
	}
	return m
}

func (e *Engine) recordEdition(perms PermissionSet) {
	if e.resolved != ModeAuto {
		return
	}
	// Only resolve on a non-empty permission set. An empty response is
	// ambiguous: it could be a real OSS-Basic install OR an Enterprise
	// install where the FIRST observed user happens to be a service
	// account with no granted permissions. Resolving to ModeBasic on
	// that empty signal would permanently misclassify the cluster — every
	// subsequent Admin user would also be filtered as Basic until restart.
	// Stay in ModeAuto until SOME user yields non-empty perms (→ Enterprise),
	// or the operator explicitly sets --rbac-gating=basic. The Filter
	// fail-open path keeps tools visible during the unresolved window.
	//
	// Capture the slog call's argument before releasing the lock so the
	// emission itself doesn't run while holding a write lock (slog
	// handlers may queue/block — keep them off the hot path).
	if len(perms) == 0 {
		return
	}
	e.mu.Lock()
	if e.resolved == ModeAuto {
		e.resolved = ResolveAuto(perms)
	}
	resolved := e.resolved
	e.mu.Unlock()
	if e.cfg.Logger != nil {
		e.cfg.Logger.Info("rbac.edition_detected", "mode", string(resolved))
	}
}

// HookOnAfterListTools returns the mark3labs/mcp-go hook that filters the
// result in place.
func (e *Engine) HookOnAfterListTools() server.OnAfterListToolsFunc {
	return func(ctx context.Context, _ any, _ *mcp.ListToolsRequest, result *mcp.ListToolsResult) {
		if result == nil || len(result.Tools) == 0 {
			return
		}
		mode := e.effectiveMode()
		if mode == ModeOff {
			return
		}
		key, ok := e.cfg.KeyFromContext(ctx)
		if !ok {
			return // no session: leave the tool list alone (legacy path)
		}

		snap, err := e.cfg.Cache.Get(ctx, key)
		if err != nil {
			if e.cfg.Logger != nil {
				e.cfg.Logger.Warn("auth.permission_fetch_failed",
					"session_key", redactKey(key),
					"error", err.Error())
			}
			return // fail open
		}

		if mode == ModeAuto {
			e.recordEdition(snap.Permissions)
			mode = e.effectiveMode()
			if mode == ModeOff || mode == ModeAuto {
				// recordEdition refuses to resolve on empty perms, so a
				// real OSS-Basic install (where /api/access-control/user/
				// permissions always returns {}) stays in ModeAuto until
				// the operator switches to --rbac-gating=basic. Fail open
				// here — better than misclassifying an Enterprise cluster
				// off the first restricted-SA observation.
				return
			}
		}

		// Time only the filter call — Cache.Get can include a network
		// fetch on miss (up to fetchTimeout=10s) which would dominate the
		// histogram and make it useless for tracking actual filter
		// performance. Recording happens unconditionally now that we're
		// past every early-return path; no `recorded` flag needed.
		start := time.Now()
		filtered := e.cfg.Gate.Filter(mode, snap, result.Tools)
		e.recordFilterDuration(ctx, mode, time.Since(start).Seconds())
		if e.cfg.Logger != nil {
			removed := len(result.Tools) - len(filtered)
			if removed > 0 {
				e.cfg.Logger.Debug("auth.tools_filtered",
					"session_key", redactKey(key),
					"mode", string(mode),
					"removed", removed,
					"remaining", len(filtered))
			}
		}
		result.Tools = filtered
	}
}

// ToolMiddleware returns a server.ToolHandlerMiddleware that enforces the
// same registry gates at tool-call time. Filtering tools/list keeps tool
// surfaces user-friendly, but a client that already knows tool names
// (cached, hardcoded, leaked from another session) could still call
// gated tools without this. Together with HookOnAfterListTools it gives
// defense in depth — the on-list filter is for UX, this middleware is
// for correctness.
//
// Behaviour mirrors the list-tools hook:
//   - ModeOff or no-session-on-context → pass-through.
//   - Cache fetch error → pass-through (fail open). Same rationale: an
//     RBAC outage shouldn't lock everyone out of every tool.
//   - ModeAuto resolves on first observation, same as the list path.
//
// On a deny, returns an mcp.NewToolResultError with a stable, generic
// message; the underlying tool handler is never invoked.
func (e *Engine) ToolMiddleware() server.ToolHandlerMiddleware {
	return func(next server.ToolHandlerFunc) server.ToolHandlerFunc {
		return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			mode := e.effectiveMode()
			if mode == ModeOff {
				return next(ctx, request)
			}
			key, ok := e.cfg.KeyFromContext(ctx)
			if !ok {
				return next(ctx, request) // no session: legacy / no-auth path
			}
			snap, err := e.cfg.Cache.Get(ctx, key)
			if err != nil {
				if e.cfg.Logger != nil {
					e.cfg.Logger.Warn("auth.permission_fetch_failed_call_time",
						"session_key", redactKey(key),
						"tool", request.Params.Name,
						"error", err.Error())
				}
				return next(ctx, request) // fail open
			}
			if mode == ModeAuto {
				e.recordEdition(snap.Permissions)
				mode = e.effectiveMode()
				if mode == ModeOff || mode == ModeAuto {
					return next(ctx, request)
				}
			}
			if !e.cfg.Gate.Allow(mode, snap, request.Params.Name) {
				if e.cfg.Logger != nil {
					e.cfg.Logger.Info("auth.tool_call_denied",
						"session_key", redactKey(key),
						"mode", string(mode),
						"tool", request.Params.Name)
				}
				return mcp.NewToolResultError("permission denied: tool '" + request.Params.Name + "' is not available for this session"), nil
			}
			return next(ctx, request)
		}
	}
}

// recordFilterDuration wraps the optional Metrics receiver so the hook
// stays safe even if a future Metrics method forgets its nil-receiver
// guard. Engine.cfg.Metrics is documented as optional, so an Engine
// constructed without one (e.g. TestEngine_Hook_Filters) goes through
// here as a no-op.
func (e *Engine) recordFilterDuration(ctx context.Context, mode Mode, durationSec float64) {
	if e.cfg.Metrics != nil {
		e.cfg.Metrics.FilterObserved(ctx, mode, durationSec)
	}
}

// InvalidateSessionCache drops the cached snapshot for the given session
// key. Safe to call when no entry exists. Intended for use by the auth
// package's token-rotation path so a refreshed access-token's new session
// doesn't share permissions with the previous one (and so the previous
// entry doesn't linger until TTL).
func (e *Engine) InvalidateSessionCache(key string) {
	if e == nil || e.cfg.Cache == nil {
		return
	}
	e.cfg.Cache.Invalidate(key)
}

// redactKey shortens an opaque session key for logs.
func redactKey(k string) string {
	if len(k) <= 8 {
		return "..."
	}
	return k[:8] + "..."
}
