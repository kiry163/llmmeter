package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestEnsureFileCreatesValidTemplate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "deeper", "config.yaml")
	paths := Paths{
		Config:   path,
		Database: filepath.Join(dir, "data", "llmmeter.db"),
		Listen:   "127.0.0.1:8123",
	}

	created, err := EnsureFile(path, paths)
	if err != nil {
		t.Fatalf("EnsureFile: %v", err)
	}
	if !created {
		t.Fatal("EnsureFile must report a newly created file")
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("config mode = %o, want 600", perm)
	}
	parent, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if perm := parent.Mode().Perm(); perm != 0o700 {
		t.Fatalf("config directory mode = %o, want 700", perm)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := Parse(data)
	if err != nil {
		t.Fatalf("template must parse: %v\n%s", err, data)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("template must validate: %v\n%s", err, data)
	}
	if cfg.Version != 1 {
		t.Fatalf("version = %d", cfg.Version)
	}
	if cfg.Server.Listen != paths.Listen || cfg.Server.Database != paths.Database {
		t.Fatalf("server = %+v", cfg.Server)
	}
	if cfg.General.Timezone != time.Local.String() || cfg.General.RetentionDays != 90 {
		t.Fatalf("general = %+v", cfg.General)
	}
	if cfg.Auth.AdminPasswordHash != "" || cfg.Auth.SessionSecret != "" {
		t.Fatalf("auth = %+v", cfg.Auth)
	}
	if len(cfg.Upstreams) != 0 || len(cfg.GatewayKeys) != 0 {
		t.Fatalf("template must start empty: %+v %+v", cfg.Upstreams, cfg.GatewayKeys)
	}
	for _, want := range []string{"upstreams: []", "gateway_keys: []", "Add the first upstream", "Add the first key"} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("template missing %q:\n%s", want, data)
		}
	}
}

func TestEnsureFileIsNoOpWhenFileExists(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	const original = "version: 1\nserver:\n  listen: 127.0.0.1:9000\n  database: /tmp/x.db\ngeneral:\n  retention_days: 30\n"
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}

	created, err := EnsureFile(path, Paths{Config: path, Database: "/tmp/y.db", Listen: "127.0.0.1:8100"})
	if err != nil {
		t.Fatalf("EnsureFile: %v", err)
	}
	if created {
		t.Fatal("an existing file must not be reported as created")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != original {
		t.Fatalf("existing config was modified:\n%s", data)
	}
}
