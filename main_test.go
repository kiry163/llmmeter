package main

import (
	"encoding/base64"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kiry163/llmmeter/internal/api"
	"github.com/kiry163/llmmeter/internal/config"
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
