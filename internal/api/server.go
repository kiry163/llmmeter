package api

import (
	"net/http"
	"time"

	"github.com/kiry163/llmmeter/internal/metrics"
	"github.com/kiry163/llmmeter/internal/settings"
	"github.com/kiry163/llmmeter/internal/store"
)

// Config is the construction input of a Server.
type Config struct {
	// Version is the build version reported by /api/health and /api/bootstrap.
	Version string
	// Listen is the configured listen address reported by /api/bootstrap.
	Listen string
	// DBPath is the SQLite file path reported by the settings endpoint.
	DBPath string

	Repo  *settings.Repo
	Store *store.Store
	Hub   *metrics.Hub

	// Now is the clock used for date defaults and sessions; nil means
	// time.Now.
	Now func() time.Time
}

// Server implements the llmmeter HTTP API.
type Server struct {
	version string
	listen  string
	dbPath  string
	repo    *settings.Repo
	store   *store.Store
	hub     *metrics.Hub
	now     func() time.Time
	auth    *Auth
}

// New returns a Server for cfg.
func New(cfg Config) *Server {
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	return &Server{
		version: cfg.Version,
		listen:  cfg.Listen,
		dbPath:  cfg.DBPath,
		repo:    cfg.Repo,
		store:   cfg.Store,
		hub:     cfg.Hub,
		now:     now,
		auth:    NewAuth(cfg.Repo, now),
	}
}

// Handler returns the HTTP handler with every /api route registered. The whole
// tree is wrapped in the CSRF check; mutating handlers rely on it for origin
// enforcement.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	// Public endpoints.
	mux.HandleFunc("GET /api/health", s.health)
	mux.HandleFunc("GET /api/bootstrap", s.bootstrap)
	mux.HandleFunc("GET /api/me", s.me)
	mux.HandleFunc("POST /api/auth/login", s.auth.Login)
	mux.HandleFunc("POST /api/auth/logout", s.auth.Logout)

	// Protected endpoints.
	require := func(h http.HandlerFunc) http.Handler { return s.auth.Require(h) }
	mux.Handle("GET /api/overview", require(s.overview))
	mux.Handle("GET /api/series", require(s.series))
	mux.Handle("GET /api/breakdown", require(s.breakdown))
	mux.Handle("GET /api/requests", require(s.requests))
	mux.Handle("GET /api/requests/{id}", require(s.request))
	mux.Handle("GET /api/live/snapshot", require(s.liveSnapshot))
	mux.Handle("GET /api/live/stream", require(s.liveStream))

	// Configuration endpoints. Setup and the pre-setup upstream probe are
	// limited to the local machine instead of requiring a session.
	mux.Handle("POST /api/setup", s.auth.RequireLoopback(http.HandlerFunc(s.setup)))
	mux.Handle("POST /api/upstream/test", s.upstreamTestRoute(s.upstreamTest))
	mux.Handle("GET /api/models", require(s.listModels))
	mux.Handle("POST /api/models", require(s.createModel))
	mux.Handle("PATCH /api/models/{id}", require(s.updateModel))
	mux.Handle("DELETE /api/models/{id}", require(s.deleteModel))
	mux.Handle("PUT /api/models/{id}/windows", require(s.replaceWindows))
	mux.Handle("GET /api/settings", require(s.getSettings))
	mux.Handle("PATCH /api/settings", require(s.patchSettings))
	mux.Handle("POST /api/gateway-key/rotate", require(s.rotateGatewayKey))

	// Unknown /api paths still answer with the JSON error envelope.
	mux.HandleFunc("/api/", s.notFound)

	return s.auth.CSRF(mux)
}

// notFound answers an unknown /api path.
func (s *Server) notFound(w http.ResponseWriter, _ *http.Request) {
	writeAPIError(w, http.StatusNotFound, codeNotFound, "no such endpoint")
}
