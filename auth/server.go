package auth

import (
	"log/slog"
	"net/http"
	"time"
)

// Server bundles everything the auth HTTP handlers need.
type Server struct {
	PublicURL string
	Store     Store
	Upstream  Upstream
	Encryptor *Encryptor
	Logger    *slog.Logger

	AccessTokenTTL  time.Duration
	RefreshTokenTTL time.Duration
	AuthCodeTTL     time.Duration
	PendingFlowTTL  time.Duration
}

func (s *Server) logger() *slog.Logger {
	if s.Logger != nil {
		return s.Logger
	}
	return slog.Default()
}

// RegisterRoutes mounts the auth endpoints on mux. grafanaURL is forwarded to
// the bootstrap handler so it can validate pasted tokens against the right
// Grafana instance.
func (s *Server) RegisterRoutes(mux *http.ServeMux, grafanaURL string) {
	authLim := NewIPLimiter(10, time.Minute)
	bootLim := NewIPLimiter(3, time.Minute)
	dcrLim := NewIPLimiter(5, time.Minute)

	mux.Handle("/.well-known/oauth-authorization-server", ASMetadataHandler(s.PublicURL))
	mux.Handle("/.well-known/oauth-protected-resource", ProtectedResourceMetadataHandler(s.PublicURL))
	mux.Handle("/register", dcrLim.Wrap(DCRHandler(s.Store)))
	mux.Handle("/authorize", authLim.Wrap(s.AuthorizeHandler()))
	mux.Handle("/callback", authLim.Wrap(s.CallbackHandler()))
	mux.Handle("/token", authLim.Wrap(s.TokenHandler()))
	mux.Handle("/bootstrap", bootLim.Wrap(s.BootstrapHandler(grafanaURL)))
}

// New constructs a Server from a validated Config plus an Encryptor and
// upstream. Sets sensible defaults for TTLs.
func New(cfg Config, store Store, enc *Encryptor, upstream Upstream, logger *slog.Logger) *Server {
	return &Server{
		PublicURL:       cfg.PublicURL,
		Store:           store,
		Upstream:        upstream,
		Encryptor:       enc,
		Logger:          logger,
		AccessTokenTTL:  time.Hour,
		RefreshTokenTTL: 30 * 24 * time.Hour,
		AuthCodeTTL:     5 * time.Minute,
		PendingFlowTTL:  15 * time.Minute,
	}
}
