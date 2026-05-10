package auth

import (
	"log/slog"
	"net/http"
	"sync"
	"time"
)

// Server bundles everything the auth HTTP handlers need.
type Server struct {
	PublicURL string
	// GrafanaURL is the operator-configured Grafana base URL. Middleware
	// pins it onto the request's GrafanaConfig so a client cannot redirect
	// the decrypted per-session API key at an attacker-controlled host via
	// the X-Grafana-URL header.
	GrafanaURL string
	// TrustForwardedHeaders gates whether X-Forwarded-For / X-Real-IP /
	// X-Forwarded-Proto from inbound requests are honoured for rate-limit
	// bucketing and HTTPS detection respectively. Default false: only
	// r.RemoteAddr and the actual TLS state are used. Set to true ONLY
	// when a header-stripping reverse proxy fronts mcp-grafana — without
	// that, attackers can spoof these headers per request to bypass per-IP
	// limits and the auth-endpoint HTTPS guard.
	TrustForwardedHeaders bool
	Store                 Store
	Upstream              Upstream
	Encryptor             *Encryptor
	Logger                *slog.Logger
	Metrics               *Metrics

	AccessTokenTTL  time.Duration
	RefreshTokenTTL time.Duration
	AuthCodeTTL     time.Duration

	// pendingsOnce lazy-initializes the per-Server pending-flow registries
	// (authzReg, bootstrapReg) on first use. Lazy init keeps &Server{...}
	// struct literals working in tests while still giving each Server its
	// own state — the previous package-level globals leaked state across
	// Server instances and across tests in the same binary.
	pendingsOnce sync.Once
	authzReg     *pendingRegistry[*pendingFlow]
	bootstrapReg *pendingRegistry[*pendingBootstrap]
}

func (s *Server) logger() *slog.Logger {
	if s.Logger != nil {
		return s.Logger
	}
	return slog.Default()
}

// RegisterRoutes mounts the auth endpoints on mux. grafanaURL is forwarded to
// the bootstrap handler so it can validate pasted tokens against the right
// Grafana instance, and stamped onto s.GrafanaURL so Middleware can pin it
// against X-Grafana-URL header overrides. allowInsecure disables the HTTPS
// guard; use only for dev.
func (s *Server) RegisterRoutes(mux *http.ServeMux, grafanaURL string, allowInsecure bool) {
	s.GrafanaURL = grafanaURL
	authLim := NewIPLimiter(10, time.Minute, s.TrustForwardedHeaders)
	bootLim := NewIPLimiter(3, time.Minute, s.TrustForwardedHeaders)
	dcrLim := NewIPLimiter(5, time.Minute, s.TrustForwardedHeaders)
	guard := RequireHTTPS(allowInsecure, s.TrustForwardedHeaders)

	wrap := func(h http.Handler) http.Handler { return guard(h) }

	mux.Handle("/.well-known/oauth-authorization-server", wrap(ASMetadataHandler(s.PublicURL)))
	mux.Handle("/.well-known/oauth-protected-resource", wrap(ProtectedResourceMetadataHandler(s.PublicURL)))
	mux.Handle("/register", wrap(dcrLim.Wrap(DCRHandler(s.Store))))
	mux.Handle("/authorize", wrap(authLim.Wrap(s.AuthorizeHandler())))
	mux.Handle("/callback", wrap(authLim.Wrap(s.CallbackHandler())))
	mux.Handle("/token", wrap(authLim.Wrap(s.TokenHandler())))
	mux.Handle("/bootstrap", wrap(bootLim.Wrap(s.BootstrapHandler(grafanaURL))))
}

// New constructs a Server from a validated Config plus an Encryptor and
// upstream. Sets sensible defaults for TTLs.
func New(cfg Config, store Store, enc *Encryptor, upstream Upstream, logger *slog.Logger) *Server {
	return &Server{
		PublicURL:             cfg.PublicURL,
		TrustForwardedHeaders: cfg.TrustForwardedHeaders,
		Store:                 store,
		Upstream:              upstream,
		Encryptor:             enc,
		Logger:                logger,
		Metrics:               NewMetrics(),
		AccessTokenTTL:        time.Hour,
		RefreshTokenTTL:       30 * 24 * time.Hour,
		AuthCodeTTL:           5 * time.Minute,
	}
}
