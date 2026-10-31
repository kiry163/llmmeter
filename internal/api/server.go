package api

import (
	"net/http"
	"time"

	"github.com/kiry163/llmmeter/internal/config"
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
	// Paths are the resolved configuration file, database and effective
	// listen address. They are reported by /api/bootstrap and /api/data.
	Paths config.Paths

	Repo  *settings.Repo
	Store *store.Store
	Hub   *metrics.Hub

	// ConfigStore holds the YAML configuration and the snapshots that auth and
	// the migrated handlers read. It will replace Repo once every handler has
	// moved; a nil store behaves like an unconfigured instance.
	ConfigStore *config.Store

	// Now is the clock used for date defaults and sessions; nil means
	// time.Now.
	Now func() time.Time
}

// Server implements the llmmeter HTTP API.
type Server struct {
	version     string
	listen      string
	dbPath      string
	paths       config.Paths
	repo        *settings.Repo
	store       *store.Store
	hub         *metrics.Hub
	configStore *config.Store
	now         func() time.Time
	auth        *Auth
	catalog     *catalog
}

// New returns a Server for cfg.
func New(cfg Config) *Server {
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	// Paths carries the effective listen address after flag overrides; the
	// legacy Listen field is the fallback for tests that only set that.
	listen := cfg.Listen
	if cfg.Paths.Listen != "" {
		listen = cfg.Paths.Listen
	}
	return &Server{
		version:     cfg.Version,
		listen:      cfg.Listen,
		dbPath:      cfg.DBPath,
		paths:       cfg.Paths,
		repo:        cfg.Repo,
		store:       cfg.Store,
		hub:         cfg.Hub,
		configStore: cfg.ConfigStore,
		now:         now,
		auth:        NewAuth(cfg.ConfigStore, listen, now),
		catalog:     newCatalog(cfg.ConfigStore, cfg.Store, now),
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
	mux.Handle("POST /api/auth/password", require(s.auth.Password))
	mux.Handle("GET /api/overview", require(s.overview))
	mux.Handle("GET /api/series", require(s.series))
	mux.Handle("GET /api/breakdown", require(s.breakdown))
	mux.Handle("GET /api/analysis", require(s.analysis))
	mux.Handle("GET /api/requests", require(s.requests))
	mux.Handle("GET /api/requests/{id}", require(s.request))
	mux.Handle("GET /api/live/snapshot", require(s.liveSnapshot))
	mux.Handle("GET /api/live/stream", require(s.liveStream))

	// Configuration endpoints. Setup and the pre-setup upstream probe are
	// limited to the local machine instead of requiring a session.
	mux.Handle("POST /api/setup", s.auth.RequireLoopback(http.HandlerFunc(s.setup)))
	mux.Handle("POST /api/upstream/test", s.upstreamTestRoute(s.upstreamTest))
	mux.Handle("GET /api/upstreams", require(s.listUpstreams))
	mux.Handle("POST /api/upstreams", require(s.createUpstream))
	mux.Handle("POST /api/upstreams/probe", require(s.probeUpstream))
	mux.Handle("PATCH /api/upstreams/{id}", require(s.patchUpstream))
	mux.Handle("DELETE /api/upstreams/{id}", require(s.deleteUpstream))
	mux.Handle("POST /api/upstreams/{id}/test", require(s.testUpstream))
	mux.Handle("GET /api/upstreams/{id}/secret", require(s.upstreamSecret))
	mux.Handle("GET /api/upstreams/{id}/pricing", require(s.upstreamPricing))
	mux.Handle("PUT /api/upstreams/{id}/pricing", require(s.replaceUpstreamPricing))
	mux.Handle("POST /api/upstreams/{id}/pricing/import", require(s.importUpstreamPricing))
	mux.Handle("GET /api/upstreams/{id}/catalog", require(s.upstreamCatalog))
	mux.Handle("GET /api/keys", require(s.listKeys))
	mux.Handle("POST /api/keys", require(s.createKey))
	mux.Handle("PATCH /api/keys/{keyId}", require(s.patchKey))
	mux.Handle("DELETE /api/keys/{keyId}", require(s.deleteKey))
	mux.Handle("POST /api/keys/{keyId}/rotate", require(s.rotateKey))
	mux.Handle("GET /api/keys/{keyId}/secret", require(s.keySecret))
	mux.Handle("GET /api/models", require(s.listModels))
	mux.Handle("POST /api/models", require(s.createModel))
	mux.Handle("POST /api/models/import", require(s.importModels))
	mux.Handle("PATCH /api/models/{id}", require(s.updateModel))
	mux.Handle("DELETE /api/models/{id}", require(s.deleteModel))
	mux.Handle("PUT /api/models/{id}/windows", require(s.replaceWindows))
	mux.Handle("GET /api/settings", require(s.getSettings))
	mux.Handle("PATCH /api/settings", require(s.patchSettings))
	mux.Handle("POST /api/gateway-key/rotate", require(s.rotateGatewayKey))
	mux.Handle("GET /api/general", require(s.getGeneral))
	mux.Handle("PATCH /api/general", require(s.patchGeneral))
	mux.Handle("GET /api/data", require(s.dataInfo))
	mux.Handle("POST /api/config/reload", require(s.reloadConfig))

	// Unknown /api paths still answer with the JSON error envelope.
	mux.HandleFunc("/api/", s.notFound)

	return s.auth.HostGuard(s.auth.CSRF(mux))
}

// notFound answers an unknown /api path.
func (s *Server) notFound(w http.ResponseWriter, _ *http.Request) {
	writeAPIError(w, http.StatusNotFound, codeNotFound, "no such endpoint")
}
