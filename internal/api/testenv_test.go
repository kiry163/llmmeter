package api_test

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kiry163/llmmeter/internal/api"
	"github.com/kiry163/llmmeter/internal/config"
	"github.com/kiry163/llmmeter/internal/metrics"
	"github.com/kiry163/llmmeter/internal/store"
)

// configTestEnv is a server backed by a real temporary config.yaml and
// database.
type configTestEnv struct {
	srv        *httptest.Server
	apiServer  *api.Server
	config     *config.Store
	data       *store.Store
	configPath string
	dbPath     string
}

func newConfigEnv(t *testing.T, listen string) *configTestEnv {
	t.Helper()
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	dbPath := filepath.Join(t.TempDir(), "llmmeter.db")
	cs, err := config.OpenStore(configPath, config.WithEffectiveDatabase(dbPath))
	if err != nil {
		t.Fatal(err)
	}
	data, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { data.Close() })

	now := func() time.Time { return time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC) }
	hub := metrics.New(data, now)
	srv := api.New(api.Config{
		Version:     "test",
		DBPath:      dbPath,
		Paths:       config.Paths{Config: configPath, Database: dbPath, Listen: listen},
		ConfigStore: cs,
		Store:       data,
		Hub:         hub,
		Now:         now,
	})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return &configTestEnv{
		srv:        ts,
		apiServer:  srv,
		config:     cs,
		data:       data,
		configPath: configPath,
		dbPath:     dbPath,
	}
}

// addTestUpstream adds one enabled upstream to the configuration.
func addTestUpstream(t *testing.T, cs *config.Store, id string) {
	t.Helper()
	if err := cs.Update(func(c *config.Config) error {
		c.Upstreams = append(c.Upstreams, config.Upstream{
			ID:      id,
			Name:    id,
			BaseURL: "https://" + id + ".example.com",
			APIKey:  "sk-" + id,
		})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func readConfigFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
