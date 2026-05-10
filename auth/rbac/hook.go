package rbac

import (
	"context"
	"log/slog"
	"sync"

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
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.resolved == ModeAuto {
		e.resolved = ResolveAuto(perms)
		if e.cfg.Logger != nil {
			e.cfg.Logger.Info("rbac.edition_detected", "mode", string(e.resolved))
		}
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

		stop := e.cfg.Metrics.Stopwatch(mode)
		defer stop()

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
				return // shouldn't happen, but be defensive
			}
		}

		filtered := e.cfg.Gate.Filter(mode, snap, result.Tools)
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

// redactKey shortens an opaque session key for logs.
func redactKey(k string) string {
	if len(k) <= 8 {
		return "..."
	}
	return k[:8] + "..."
}
