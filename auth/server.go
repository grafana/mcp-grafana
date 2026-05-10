package auth

import (
	"log/slog"
	"time"
)

// Server bundles everything the auth HTTP handlers need.
type Server struct {
	PublicURL string
	Store     Store
	Upstream  Upstream
	Encryptor *Encryptor
	Logger    *slog.Logger

	// Tunables, defaulted by RegisterRoutes.
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
