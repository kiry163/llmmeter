package config

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestStoreUpdateIsAtomicAndPerm0600(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	st, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Update(func(c *Config) error {
		c.Upstreams = []Upstream{{ID: "deepseek", Name: "DeepSeek", BaseURL: "https://api.deepseek.com", Models: nil}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %o", info.Mode().Perm())
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "deepseek") {
		t.Fatalf("file = %s", got)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() != "config.yaml" {
			t.Fatalf("leftover file %q", e.Name())
		}
	}
}

func TestStoreReloadKeepsOldConfigOnInvalidFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	st, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	before := st.Config()
	if err := os.WriteFile(path, []byte("version: nope\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := st.Reload(); err == nil {
		t.Fatal("expected reload error")
	}
	if !reflect.DeepEqual(st.Config(), before) {
		t.Fatal("config must not change on failed reload")
	}
}

func TestStoreReloadKeepsOldConfigWhenFileDeleted(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	st, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Update(func(c *Config) error {
		c.Upstreams = []Upstream{{ID: "deepseek", Name: "DeepSeek", BaseURL: "https://api.deepseek.com"}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	before := st.Config()
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := st.Reload(); err == nil {
		t.Fatal("expected reload error for a missing file")
	}
	if !reflect.DeepEqual(st.Config(), before) {
		t.Fatal("config must not change when the file is gone")
	}
}

func TestStoreReloadKeepsOldConfigOnSemanticError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	st, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Update(func(c *Config) error {
		c.Upstreams = []Upstream{{ID: "deepseek", Name: "DeepSeek", BaseURL: "https://api.deepseek.com"}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	before := st.Config()
	const doc = `version: 1
server:
  listen: "127.0.0.1:8100"
  database: "~/.cache/llmmeter/llmmeter.db"
general:
  retention_days: 1
`
	if err := os.WriteFile(path, []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := st.Reload(); err == nil {
		t.Fatal("expected reload error for an invalid retention_days")
	}
	if !reflect.DeepEqual(st.Config(), before) {
		t.Fatal("config must not change on a semantic reload failure")
	}
}

func TestOpenStoreMissingFileUsesDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	st, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if st.Writable() {
		t.Fatal("Writable must be false before the first write")
	}
	if st.Path() != path {
		t.Fatalf("Path() = %q, want %q", st.Path(), path)
	}
	if !reflect.DeepEqual(st.Config(), Default()) {
		t.Fatalf("config = %+v, want defaults", st.Config())
	}
	if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("config file must not exist yet, err = %v", err)
	}
}

func TestOpenStorePersistsResolvedDatabaseOnFirstUpdate(t *testing.T) {
	cfgHome := t.TempDir()
	cacheHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfgHome)
	t.Setenv("XDG_CACHE_HOME", cacheHome)

	paths, err := ResolvePaths("", "", "")
	if err != nil {
		t.Fatal(err)
	}
	st, err := OpenStore(paths.Config, WithEffectiveDatabase(paths.Database))
	if err != nil {
		t.Fatal(err)
	}
	if got := st.Config().Server.Database; got != paths.Database {
		t.Fatalf("in-memory database = %q, want %q", got, paths.Database)
	}
	if err := st.Update(func(c *Config) error {
		c.Upstreams = []Upstream{{ID: "deepseek", Name: "DeepSeek", BaseURL: "https://api.deepseek.com"}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(paths.Config)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), paths.Database) {
		t.Fatalf("file must persist the resolved database %q, got:\n%s", paths.Database, data)
	}
	if strings.Contains(string(data), DefaultDatabase) {
		t.Fatalf("file must not persist the default literal %q, got:\n%s", DefaultDatabase, data)
	}

	again, err := ResolvePaths("", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if again.Database != paths.Database {
		t.Fatalf("database after restart = %q, want %q", again.Database, paths.Database)
	}
}

func TestOpenStoreIgnoresEffectiveDatabaseWhenFileExists(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	fromConfig := filepath.Join(dir, "from-config.db")
	doc := "version: 1\nserver:\n  listen: 127.0.0.1:8100\n  database: " + fromConfig + "\n"
	if err := os.WriteFile(path, []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	st, err := OpenStore(path, WithEffectiveDatabase(filepath.Join(dir, "from-flag.db")))
	if err != nil {
		t.Fatal(err)
	}
	if got := st.Config().Server.Database; got != fromConfig {
		t.Fatalf("database = %q, want config value %q", got, fromConfig)
	}
}

func TestStoreUpdateCreatesFileAndFlipsWritable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	st, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Update(func(c *Config) error {
		c.General.RetentionDays = 30
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if !st.Writable() {
		t.Fatal("Writable must be true after a successful Update")
	}
	if got := st.Config().General.RetentionDays; got != 30 {
		t.Fatalf("retention_days = %d", got)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
}

func TestStoreUpdateFailureLeavesOldConfigAndFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	st, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Update(func(c *Config) error {
		c.Upstreams = []Upstream{{ID: "deepseek", Name: "DeepSeek", BaseURL: "https://api.deepseek.com"}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	beforeConfig := st.Config()
	beforeFile, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	if err := st.Update(func(c *Config) error {
		c.Version = 2
		c.Server.Listen = "127.0.0.1:9999"
		return nil
	}); err == nil {
		t.Fatal("expected validation error")
	}
	if !reflect.DeepEqual(st.Config(), beforeConfig) {
		t.Fatal("config must not change on failed update")
	}
	afterFile, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(afterFile) != string(beforeFile) {
		t.Fatal("file must not change on failed update")
	}
}

func TestStoreUpdatePropagatesMutationError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	st, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	before := st.Config()
	sentinel := errors.New("boom")
	if err := st.Update(func(c *Config) error {
		c.Version = 2
		return sentinel
	}); !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want sentinel", err)
	}
	if !reflect.DeepEqual(st.Config(), before) {
		t.Fatal("config must not change when the mutation fails")
	}
	if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("file must not be created, err = %v", err)
	}
}

func TestStoreConfigReturnsDeepCopy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	st, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	enabled := true
	keyEnabled := true
	reasoning := 0.5
	windowRate := 0.1
	if err := st.Update(func(c *Config) error {
		c.Upstreams = []Upstream{{
			ID:      "deepseek",
			Name:    "DeepSeek",
			BaseURL: "https://api.deepseek.com",
			Enabled: &enabled,
			Models: []Model{{
				Name:      "deepseek-chat",
				Input:     1,
				Output:    4,
				Reasoning: &reasoning,
				Windows: []Window{{
					Label: "night",
					Days:  []string{"mon", "tue"},
					Start: "00:00",
					End:   "08:00",
					Input: &windowRate,
				}},
			}},
		}}
		c.GatewayKeys = []GatewayKey{{
			ID:         "mac",
			Name:       "Mac",
			Key:        "sk-local-1234567890",
			UpstreamID: "deepseek",
			Enabled:    &keyEnabled,
		}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	got := st.Config()
	got.Version = 2
	got.Server.Listen = "127.0.0.1:9999"
	got.Upstreams[0].Name = "changed"
	*got.Upstreams[0].Enabled = false
	got.Upstreams[0].Models[0].Reasoning = nil
	got.Upstreams[0].Models[0].Windows[0].Days[0] = "sun"
	*got.Upstreams[0].Models[0].Windows[0].Input = 9
	*got.GatewayKeys[0].Enabled = false

	fresh := st.Config()
	if fresh.Version != 1 {
		t.Fatalf("version = %d", fresh.Version)
	}
	if fresh.Server.Listen != DefaultListen {
		t.Fatalf("listen = %q", fresh.Server.Listen)
	}
	u := fresh.Upstreams[0]
	if u.Name != "DeepSeek" || u.Enabled == nil || !*u.Enabled {
		t.Fatalf("upstream = %+v", u)
	}
	if u.Models[0].Reasoning == nil || *u.Models[0].Reasoning != reasoning {
		t.Fatalf("reasoning = %v", u.Models[0].Reasoning)
	}
	w := u.Models[0].Windows[0]
	if w.Days[0] != "mon" || w.Input == nil || *w.Input != windowRate {
		t.Fatalf("window = %+v", w)
	}
	if k := fresh.GatewayKeys[0]; k.Enabled == nil || !*k.Enabled {
		t.Fatalf("gateway key = %+v", k)
	}
}

func TestStoreReloadAppliesValidFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	st, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if st.Writable() {
		t.Fatal("Writable must be false before the file exists")
	}
	const doc = `version: 1
server:
  listen: "127.0.0.1:8100"
  database: "~/.cache/llmmeter/llmmeter.db"
upstreams:
  - id: deepseek
    name: DeepSeek
    base_url: https://api.deepseek.com
`
	if err := os.WriteFile(path, []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := st.Reload(); err != nil {
		t.Fatal(err)
	}
	if !st.Writable() {
		t.Fatal("Writable must be true after reloading an existing file")
	}
	cfg := st.Config()
	if len(cfg.Upstreams) != 1 || cfg.Upstreams[0].ID != "deepseek" {
		t.Fatalf("upstreams = %+v", cfg.Upstreams)
	}
}

func TestOpenStoreRejectsInvalidFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("version: nope\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenStore(path); err == nil {
		t.Fatal("expected OpenStore error")
	}
}

func TestOpenStoreTightensPermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	const doc = "version: 1\nserver:\n  listen: 127.0.0.1:8100\n  database: ~/.cache/llmmeter/llmmeter.db\n"
	if err := os.WriteFile(path, []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenStore(path); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %o", info.Mode().Perm())
	}
}

func TestOpenStoreCreatesConfigDir0700(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "llmmeter")
	if _, err := OpenStore(filepath.Join(dir, "config.yaml")); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Fatalf("dir mode = %o", info.Mode().Perm())
	}
}

func TestResolvePathsUsesXDGDirectories(t *testing.T) {
	cfgHome := t.TempDir()
	cacheHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfgHome)
	t.Setenv("XDG_CACHE_HOME", cacheHome)

	paths, err := ResolvePaths("", "", "")
	if err != nil {
		t.Fatal(err)
	}
	want := Paths{
		Config:   filepath.Join(cfgHome, "llmmeter", "config.yaml"),
		Database: filepath.Join(cacheHome, "llmmeter", "llmmeter.db"),
		Listen:   DefaultListen,
	}
	if paths != want {
		t.Fatalf("paths = %+v, want %+v", paths, want)
	}
	for _, dir := range []string{filepath.Join(cfgHome, "llmmeter"), filepath.Join(cacheHome, "llmmeter")} {
		info, err := os.Stat(dir)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o700 {
			t.Fatalf("%s mode = %o", dir, info.Mode().Perm())
		}
	}
	if _, err := os.Stat(paths.Config); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("config file must not be created, err = %v", err)
	}
}

func TestResolvePathsFallsBackToHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("XDG_CACHE_HOME", "")

	paths, err := ResolvePaths("", "", "")
	if err != nil {
		t.Fatal(err)
	}
	want := Paths{
		Config:   filepath.Join(home, ".config", "llmmeter", "config.yaml"),
		Database: filepath.Join(home, ".cache", "llmmeter", "llmmeter.db"),
		Listen:   DefaultListen,
	}
	if paths != want {
		t.Fatalf("paths = %+v, want %+v", paths, want)
	}
}

func TestResolvePathsConfigOverridesDefaults(t *testing.T) {
	dir := t.TempDir()
	configDir := filepath.Join(dir, "llmmeter")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(configDir, "config.yaml")
	const doc = `version: 1
server:
  listen: "127.0.0.1:9100"
  database: data/llmmeter.db
`
	if err := os.WriteFile(cfgPath, []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	paths, err := ResolvePaths("", "", "")
	if err != nil {
		t.Fatal(err)
	}
	want := Paths{
		Config:   filepath.Join(configDir, "config.yaml"),
		Database: filepath.Join(configDir, "data", "llmmeter.db"),
		Listen:   "127.0.0.1:9100",
	}
	if paths != want {
		t.Fatalf("paths = %+v, want %+v", paths, want)
	}
	if info, err := os.Stat(filepath.Join(configDir, "data")); err != nil {
		t.Fatal(err)
	} else if info.Mode().Perm() != 0o700 {
		t.Fatalf("database dir mode = %o", info.Mode().Perm())
	}
}

func TestResolvePathsXDGCacheOverridesDefaultDatabase(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	const doc = `version: 1
server:
  listen: "127.0.0.1:9100"
`
	if err := os.WriteFile(cfgPath, []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	cacheHome := filepath.Join(t.TempDir(), "cache")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", cacheHome)

	paths, err := ResolvePaths(cfgPath, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(cacheHome, "llmmeter", "llmmeter.db"); paths.Database != want {
		t.Fatalf("database = %q, want %q", paths.Database, want)
	}
	if paths.Listen != "127.0.0.1:9100" {
		t.Fatalf("listen = %q", paths.Listen)
	}
}

func TestResolvePathsFlagsOverrideConfig(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	const doc = `version: 1
server:
  listen: "127.0.0.1:9100"
  database: /from/config.db
`
	if err := os.WriteFile(cfgPath, []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	dbFlag := filepath.Join(dir, "flag.db")

	paths, err := ResolvePaths(cfgPath, dbFlag, "127.0.0.1:9200")
	if err != nil {
		t.Fatal(err)
	}
	want := Paths{Config: cfgPath, Database: dbFlag, Listen: "127.0.0.1:9200"}
	if paths != want {
		t.Fatalf("paths = %+v, want %+v", paths, want)
	}
}

func TestResolvePathsExpandsTildeDatabase(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("XDG_CACHE_HOME", "")
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	const doc = `version: 1
server:
  database: ~/data/llmmeter.db
`
	if err := os.WriteFile(cfgPath, []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}

	paths, err := ResolvePaths(cfgPath, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, "data", "llmmeter.db"); paths.Database != want {
		t.Fatalf("database = %q, want %q", paths.Database, want)
	}
}

func TestResolvePathsMissingConfigIsLegal(t *testing.T) {
	cfgHome := t.TempDir()
	cacheHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfgHome)
	t.Setenv("XDG_CACHE_HOME", cacheHome)
	cfgPath := filepath.Join(t.TempDir(), "absent", "config.yaml")

	paths, err := ResolvePaths(cfgPath, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if paths.Config != cfgPath {
		t.Fatalf("config = %q", paths.Config)
	}
	if want := filepath.Join(cacheHome, "llmmeter", "llmmeter.db"); paths.Database != want {
		t.Fatalf("database = %q, want %q", paths.Database, want)
	}
	if paths.Listen != DefaultListen {
		t.Fatalf("listen = %q", paths.Listen)
	}
}

func TestResolvePathsRejectsInvalidConfig(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte("version: [\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolvePaths(cfgPath, "", ""); err == nil {
		t.Fatal("expected error")
	}
}
