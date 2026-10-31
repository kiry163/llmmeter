package api

import (
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/kiry163/llmmeter/internal/config"
	"github.com/kiry163/llmmeter/internal/store"
)

// bootstrapResponse is the unauthenticated first-load payload. It carries no
// secrets: only the state needed to pick between login and the app.
type bootstrapResponse struct {
	Version      string `json:"version"`
	AuthRequired bool   `json:"authRequired"`
	Listen       string `json:"listen"`
	Timezone     string `json:"timezone"`
	ConfigPath   string `json:"configPath"`
	DBPath       string `json:"dbPath"`
	EarliestDay  string `json:"earliestDay"`
}

func (s *Server) bootstrap(w http.ResponseWriter, _ *http.Request) {
	snap := s.configSnapshot()
	writeJSON(w, http.StatusOK, bootstrapResponse{
		Version:      s.version,
		AuthRequired: snap != nil && snap.Config.Auth.AdminPasswordHash != "",
		Listen:       s.effectiveListen(),
		Timezone:     s.configLocation().String(),
		ConfigPath:   s.configPath(),
		DBPath:       s.databasePath(),
		EarliestDay:  s.earliestDay(),
	})
}

// dataInfo describes the process data locations and health counters.
type dataInfo struct {
	ConfigPath      string `json:"configPath"`
	DBPath          string `json:"dbPath"`
	DBBytes         int64  `json:"dbBytes"`
	WALBytes        int64  `json:"walBytes"`
	SchemaVersion   int    `json:"schemaVersion"`
	Version         string `json:"version"`
	RetentionDays   int    `json:"retentionDays"`
	Timezone        string `json:"timezone"`
	EffectiveListen string `json:"effectiveListen"`
	WriteQueue      int    `json:"writeQueue"`
	EarliestDay     string `json:"earliestDay"`
}

func (s *Server) dataInfo(w http.ResponseWriter, _ *http.Request) {
	dbPath := s.databasePath()
	retention := 0
	if snap := s.configSnapshot(); snap != nil {
		retention = snap.Config.General.RetentionDays
	}
	queue := 0
	if s.store != nil {
		queue = s.store.QueueDepth()
	}
	writeJSON(w, http.StatusOK, dataInfo{
		ConfigPath:      s.configPath(),
		DBPath:          dbPath,
		DBBytes:         fileBytes(dbPath),
		WALBytes:        fileBytes(dbPath + "-wal"),
		SchemaVersion:   store.UserVersion,
		Version:         s.version,
		RetentionDays:   retention,
		Timezone:        s.configLocation().String(),
		EffectiveListen: s.effectiveListen(),
		WriteQueue:      queue,
		EarliestDay:     s.earliestDay(),
	})
}

// configSnapshot returns the current immutable configuration snapshot. A
// server built without a configuration store behaves like an unconfigured
// instance.
func (s *Server) configSnapshot() *config.Snapshot {
	if s.configStore == nil {
		return nil
	}
	return s.configStore.Snapshot()
}

// configLocation returns the configured timezone, defaulting to UTC.
func (s *Server) configLocation() *time.Location {
	if snap := s.configSnapshot(); snap != nil && snap.Location != nil {
		return snap.Location
	}
	return time.UTC
}

// configPath returns the resolved configuration file path.
func (s *Server) configPath() string {
	if s.paths.Config != "" {
		return s.paths.Config
	}
	if s.configStore != nil {
		return s.configStore.Path()
	}
	return ""
}

// databasePath returns the resolved database path.
func (s *Server) databasePath() string {
	if s.paths.Database != "" {
		return s.paths.Database
	}
	return s.dbPath
}

// effectiveListen returns the listen address after flag overrides: the
// resolved paths win over the raw configuration value.
func (s *Server) effectiveListen() string {
	if s.paths.Listen != "" {
		return s.paths.Listen
	}
	if snap := s.configSnapshot(); snap != nil && snap.Config.Server.Listen != "" {
		return snap.Config.Server.Listen
	}
	return s.listen
}

// earliestDay returns the first local day with traffic, or "" when the
// database has none. Read failures are logged and reported as empty.
func (s *Server) earliestDay() string {
	if s.store == nil {
		return ""
	}
	day, err := s.store.EarliestDay()
	if err != nil {
		slog.Error("api: earliest day", "error", sanitizeLogError(err))
		return ""
	}
	return day
}

// fileBytes returns the size of a regular file, or 0 when it is missing or not
// a regular file.
func fileBytes(path string) int64 {
	if path == "" {
		return 0
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return 0
	}
	return info.Size()
}
