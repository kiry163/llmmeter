package main

import (
	"encoding/base64"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kiry163/llmmeter/internal/api"
	"github.com/kiry163/llmmeter/internal/config"
	"github.com/kiry163/llmmeter/internal/secure"
)

func TestValidateStartupNonLoopbackRequiresPassword(t *testing.T) {
	open := &config.Snapshot{Config: &config.Config{}}
	if err := validateStartup("0.0.0.0:8100", open); err == nil {
		t.Fatal("must refuse non-loopback without password")
	}
	secured := &config.Snapshot{Config: &config.Config{
		Auth: config.Auth{AdminPasswordHash: "$argon2id$v=19$m=65536,t=3,p=4$c2FsdA$aGFzaA"},
	}}
	if err := validateStartup("0.0.0.0:8100", secured); err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if err := validateStartup("127.0.0.1:8100", open); err != nil {
		t.Fatalf("loopback must pass: %v", err)
	}
	if err := validateStartup("0.0.0.0:8100", nil); err == nil {
		t.Fatal("nil snapshot must behave like an unconfigured instance")
	}

	for _, listen := range []string{"localhost:8100", "[::1]:8100", "127.0.0.2:8100"} {
		if err := validateStartup(listen, open); err != nil {
			t.Fatalf("%s must pass without password: %v", listen, err)
		}
	}
	for _, listen := range []string{":8100", "[::]:8100", "192.168.1.10:8100", "example.com:8100"} {
		if err := validateStartup(listen, open); err == nil {
			t.Fatalf("%s must be refused without password", listen)
		}
	}
}

func TestInitWritesConfigAndPrintsKey(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	dbPath := filepath.Join(dir, "llmmeter.db")

	var out strings.Builder
	err := runInit([]string{
		"-config", configPath,
		"-listen", "127.0.0.1:8123",
		"-database", dbPath,
		"-timezone", "Asia/Shanghai",
		"-upstream-name", "DeepSeek",
		"-upstream-url", "https://api.deepseek.com/",
		"-upstream-key", "sk-upstream",
		"-key-name", "default",
	}, strings.NewReader(""), &out)
	if err != nil {
		t.Fatalf("runInit: %v", err)
	}

	printed := strings.TrimSpace(out.String())
	const prefix = "gateway key: "
	if !strings.HasPrefix(printed, prefix) {
		t.Fatalf("stdout = %q, want gateway key line", printed)
	}
	gatewayKey := strings.TrimPrefix(printed, prefix)
	if !strings.HasPrefix(gatewayKey, "sk-local-") {
		t.Fatalf("gateway key = %q, want sk-local- prefix", gatewayKey)
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	cfg, err := config.Parse(data)
	if err != nil {
		t.Fatalf("parse config: %v", err)
	}
	if _, err := config.Build(cfg); err != nil {
		t.Fatalf("build config: %v", err)
	}
	if cfg.Server.Listen != "127.0.0.1:8123" {
		t.Fatalf("listen = %q", cfg.Server.Listen)
	}
	if cfg.Server.Database != dbPath {
		t.Fatalf("database = %q, want %q", cfg.Server.Database, dbPath)
	}
	if cfg.General.Timezone != "Asia/Shanghai" {
		t.Fatalf("timezone = %q", cfg.General.Timezone)
	}
	if len(cfg.Upstreams) != 1 {
		t.Fatalf("upstreams = %d", len(cfg.Upstreams))
	}
	up := cfg.Upstreams[0]
	if up.ID != "deepseek" || up.Name != "DeepSeek" || up.BaseURL != "https://api.deepseek.com" {
		t.Fatalf("upstream = %+v", up)
	}
	if up.APIKey != "sk-upstream" {
		t.Fatalf("upstream api key = %q", up.APIKey)
	}
	if len(cfg.GatewayKeys) != 1 {
		t.Fatalf("gateway_keys = %d", len(cfg.GatewayKeys))
	}
	key := cfg.GatewayKeys[0]
	if key.Key != gatewayKey || key.UpstreamID != up.ID || key.Name != "default" {
		t.Fatalf("gateway key = %+v, printed %q", key, gatewayKey)
	}
	if cfg.Auth.SessionSecret == "" {
		t.Fatal("session secret must be generated")
	}
	if cfg.Auth.AdminPasswordHash != "" {
		t.Fatal("no admin password was requested, so no hash must be set")
	}

	info, err := os.Stat(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("config mode = %o, want 600", perm)
	}
	if _, err := os.Stat(dbPath); !os.IsNotExist(err) {
		t.Fatalf("init must not create the database (stat err = %v)", err)
	}
}

func TestInitRefusesExistingConfig(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	const original = "version: 1\n"
	if err := os.WriteFile(configPath, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}

	err := runInit([]string{
		"-config", configPath,
		"-database", filepath.Join(dir, "llmmeter.db"),
		"-upstream-name", "DeepSeek",
		"-upstream-url", "https://api.deepseek.com",
	}, strings.NewReader(""), io.Discard)
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("existing config error = %v", err)
	}
	data, readErr := os.ReadFile(configPath)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(data) != original {
		t.Fatalf("existing config was modified: %q", data)
	}
}

func TestInitNonLoopbackRequiresAdminPassword(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")

	err := runInit([]string{
		"-config", configPath,
		"-listen", "0.0.0.0:8100",
		"-database", filepath.Join(dir, "llmmeter.db"),
		"-upstream-name", "DeepSeek",
		"-upstream-url", "https://api.deepseek.com",
	}, strings.NewReader(""), io.Discard)
	if err == nil {
		t.Fatal("non-loopback listen without an admin password must be rejected")
	}
	if _, statErr := os.Stat(configPath); !os.IsNotExist(statErr) {
		t.Fatalf("rejected init must not write a config (stat err = %v)", statErr)
	}
}

func TestInitHashesAdminPasswordFromStdin(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")

	err := runInit([]string{
		"-config", configPath,
		"-listen", "0.0.0.0:8100",
		"-database", filepath.Join(dir, "llmmeter.db"),
		"-upstream-name", "DeepSeek",
		"-upstream-url", "https://api.deepseek.com",
		"-admin-password-stdin",
	}, strings.NewReader("hunter22\n"), io.Discard)
	if err != nil {
		t.Fatalf("runInit: %v", err)
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	if !secure.VerifyPassword(cfg.Auth.AdminPasswordHash, "hunter22") {
		t.Fatal("stdin password must be stored as a verifiable hash")
	}
}

func TestEnsureSessionSecretFillsExistingConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	seed, err := config.OpenStore(path, config.WithEffectiveDatabase(filepath.Join(t.TempDir(), "llmmeter.db")))
	if err != nil {
		t.Fatal(err)
	}
	if err := seed.Update(func(c *config.Config) error {
		c.Auth.SessionSecret = ""
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	st, err := config.OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if !st.Writable() {
		t.Fatal("existing config must be writable")
	}
	if err := ensureSessionSecret(st); err != nil {
		t.Fatalf("ensureSessionSecret: %v", err)
	}

	secret := st.Snapshot().Config.Auth.SessionSecret
	raw, err := base64.StdEncoding.DecodeString(secret)
	if err != nil || len(raw) != 32 {
		t.Fatalf("session secret = %q, len(decoded)=%d, err=%v; want 32-byte base64", secret, len(raw), err)
	}
	// Auth can issue a session signed with the generated secret.
	a := api.NewAuth(st, "127.0.0.1:8100", time.Now)
	if err := a.Issue(httptest.NewRecorder()); err != nil {
		t.Fatalf("Auth cannot use generated secret: %v", err)
	}

	// The secret is persisted, not just held in memory.
	reopened, err := config.OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := reopened.Snapshot().Config.Auth.SessionSecret; got != secret {
		t.Fatalf("persisted secret = %q, want %q", got, secret)
	}
}

func TestEnsureSessionSecretKeepsExistingSecret(t *testing.T) {
	known := base64.StdEncoding.EncodeToString(make([]byte, 32))
	path := filepath.Join(t.TempDir(), "config.yaml")
	seed, err := config.OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := seed.Update(func(c *config.Config) error {
		c.Auth.SessionSecret = known
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	st, err := config.OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := ensureSessionSecret(st); err != nil {
		t.Fatalf("ensureSessionSecret: %v", err)
	}
	if got := st.Snapshot().Config.Auth.SessionSecret; got != known {
		t.Fatalf("secret = %q, want unchanged %q", got, known)
	}
}

func TestEnsureSessionSecretDoesNotWriteInSetupMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	st, err := config.OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if st.Writable() {
		t.Fatal("missing config must be in setup mode")
	}
	if err := ensureSessionSecret(st); err != nil {
		t.Fatalf("ensureSessionSecret: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("setup mode must not create a config (stat err = %v)", err)
	}
	if got := st.Snapshot().Config.Auth.SessionSecret; got != "" {
		t.Fatalf("setup mode secret = %q, want empty", got)
	}
}

func TestEnsureSessionSecretReadOnlyConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	seed, err := config.OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := seed.Update(func(c *config.Config) error {
		c.Auth.SessionSecret = ""
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	st, err := config.OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}

	if err := os.Chmod(dir, 0o500); err != nil {
		t.Skipf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	err = ensureSessionSecret(st)
	if err == nil {
		t.Skip("filesystem permissions do not block writes in this environment")
	}
	if got := st.Snapshot().Config.Auth.SessionSecret; got != "" {
		t.Fatalf("failed write must leave the secret empty, got %q", got)
	}
}
