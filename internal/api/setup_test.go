package api_test

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kiry163/llmmeter/internal/api"
	"github.com/kiry163/llmmeter/internal/config"
	"github.com/kiry163/llmmeter/internal/metrics"
	"github.com/kiry163/llmmeter/internal/secure"
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

func TestSetupWritesConfigAndReturnsGatewayKey(t *testing.T) {
	env := newConfigEnv(t, "127.0.0.1:8100")
	body := `{
		"upstreamName": "DeepSeek",
		"baseUrl": "https://api.deepseek.com",
		"apiKey": "sk-upstream-secret",
		"timezone": "Asia/Shanghai",
		"adminPassword": "hunter22",
		"keyName": "Default",
		"models": [{"name":"deepseek-chat","input":1.0,"output":4.0,"cacheRead":0.02,"cacheWrite":0,"reasoning":4.0,"windows":[]}]
	}`
	rec := doPOST(t, env.srv, "/api/setup", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("setup = %d %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		UpstreamID string `json:"upstreamId"`
		KeyID      string `json:"keyId"`
		GatewayKey string `json:"gatewayKey"`
		ConfigPath string `json:"configPath"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.UpstreamID != "deepseek" || resp.KeyID != "default" {
		t.Fatalf("ids = %+v", resp)
	}
	if !strings.HasPrefix(resp.GatewayKey, "sk-local-") {
		t.Fatalf("gateway key = %q", resp.GatewayKey)
	}
	if resp.ConfigPath != env.configPath {
		t.Fatalf("config path = %q, want %q", resp.ConfigPath, env.configPath)
	}
	if strings.Contains(rec.Body.String(), "sk-upstream-secret") {
		t.Fatalf("setup echoed upstream key: %s", rec.Body.String())
	}

	// The file holds the upstream, the key and the generated auth material.
	raw := readConfigFile(t, env.configPath)
	for _, want := range []string{
		"name: DeepSeek",
		"base_url: https://api.deepseek.com",
		"gateway_keys:",
		resp.GatewayKey,
		"session_secret:",
		"admin_password_hash: $argon2id$",
		"name: deepseek-chat",
	} {
		if !strings.Contains(raw, want) {
			t.Fatalf("config.yaml missing %q:\n%s", want, raw)
		}
	}

	snap := env.config.Snapshot()
	if len(snap.Config.Upstreams) != 1 || snap.Config.Upstreams[0].ID != "deepseek" {
		t.Fatalf("upstreams = %+v", snap.Config.Upstreams)
	}
	if snap.Config.General.Timezone != "Asia/Shanghai" {
		t.Fatalf("timezone = %q", snap.Config.General.Timezone)
	}
	binding, ok := snap.LookupKey(resp.GatewayKey)
	if !ok || binding.KeyID != "default" || binding.UpstreamID != "deepseek" {
		t.Fatalf("binding = %+v, ok = %v", binding, ok)
	}
	if snap.Config.Auth.SessionSecret == "" {
		t.Fatal("session secret not generated")
	}
}

func TestSetupRejectsWhenUpstreamsExist(t *testing.T) {
	env := newConfigEnv(t, "127.0.0.1:8100")
	addTestUpstream(t, env.config, "existing")

	rec := doPOST(t, env.srv, "/api/setup",
		`{"upstreamName":"Other","baseUrl":"https://other.example.com","apiKey":"sk-other"}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("setup = %d %s", rec.Code, rec.Body.String())
	}
	if got := len(env.config.Snapshot().Config.Upstreams); got != 1 {
		t.Fatalf("upstreams after rejected setup = %d", got)
	}
}

func TestSetupLoopbackOnly(t *testing.T) {
	env := newConfigEnv(t, "127.0.0.1:8100")
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.RemoteAddr = "203.0.113.9:4321"
		env.apiServer.Handler().ServeHTTP(w, r)
	}))
	t.Cleanup(remote.Close)

	rec := doPOST(t, remote, "/api/setup",
		`{"upstreamName":"Remote","baseUrl":"https://remote.example.com","apiKey":"sk-remote"}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("remote setup = %d %s", rec.Code, rec.Body.String())
	}
	if len(env.config.Snapshot().Config.Upstreams) != 0 {
		t.Fatal("remote setup wrote configuration")
	}
}

func TestSetupRejectsMissingPasswordOffLoopback(t *testing.T) {
	env := newConfigEnv(t, "0.0.0.0:8100")
	rec := doPOST(t, env.srv, "/api/setup",
		`{"upstreamName":"Public","baseUrl":"https://public.example.com","apiKey":"sk-public"}`)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), `"adminPassword"`) {
		t.Fatalf("setup = %d %s", rec.Code, rec.Body.String())
	}
	if len(env.config.Snapshot().Config.Upstreams) != 0 {
		t.Fatal("rejected setup wrote configuration")
	}
}

func TestSetupValidatesBodyFields(t *testing.T) {
	env := newConfigEnv(t, "127.0.0.1:8100")
	rec := doPOST(t, env.srv, "/api/setup",
		`{"upstreamName":"","baseUrl":"not-a-url","timezone":"Nowhere/Nope"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("setup = %d %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, field := range []string{`"upstreamName"`, `"baseUrl"`, `"timezone"`} {
		if !strings.Contains(body, field) {
			t.Fatalf("missing field %s: %s", field, body)
		}
	}
	if len(env.config.Snapshot().Config.Upstreams) != 0 {
		t.Fatal("invalid setup wrote configuration")
	}
}

func TestSetupPreservesExistingAdminPasswordAndSecret(t *testing.T) {
	env := newConfigEnv(t, "127.0.0.1:8100")
	hash, err := secure.HashPassword("oldpass1")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := secure.RandomSecret()
	if err != nil {
		t.Fatal(err)
	}
	secret := base64.StdEncoding.EncodeToString(raw)
	if err := env.config.Update(func(c *config.Config) error {
		c.Auth.AdminPasswordHash = hash
		c.Auth.SessionSecret = secret
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	client := newSessionClient(t, env.srv, "oldpass1")
	rec := doAs(t, client, env.srv, "POST", "/api/setup",
		`{"upstreamName":"DeepSeek","baseUrl":"https://api.deepseek.com"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("setup = %d %s", rec.Code, rec.Body.String())
	}
	auth := env.config.Snapshot().Config.Auth
	if auth.AdminPasswordHash != hash || !secure.VerifyPassword(auth.AdminPasswordHash, "oldpass1") {
		t.Fatal("setup cleared or replaced the existing admin password")
	}
	if auth.SessionSecret != secret {
		t.Fatal("setup rotated the existing session secret")
	}
	rawFile := readConfigFile(t, env.configPath)
	if !strings.Contains(rawFile, secret) || !strings.Contains(rawFile, hash) {
		t.Fatalf("config.yaml did not preserve auth material:\n%s", rawFile)
	}
}

func TestSetupPersistsEffectiveServerPaths(t *testing.T) {
	env := newConfigEnv(t, "127.0.0.1:9999")
	if got := env.config.Snapshot().Config.Server.Listen; got == "127.0.0.1:9999" {
		t.Fatalf("fixture base listen unexpectedly already effective: %q", got)
	}
	rec := doPOST(t, env.srv, "/api/setup",
		`{"upstreamName":"DeepSeek","baseUrl":"https://api.deepseek.com"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("setup = %d %s", rec.Code, rec.Body.String())
	}
	cfg := env.config.Snapshot().Config
	if cfg.Server.Listen != "127.0.0.1:9999" {
		t.Fatalf("listen = %q", cfg.Server.Listen)
	}
	if cfg.Server.Database != env.dbPath {
		t.Fatalf("database = %q, want %q", cfg.Server.Database, env.dbPath)
	}
	raw := readConfigFile(t, env.configPath)
	if !strings.Contains(raw, "listen: 127.0.0.1:9999") {
		t.Fatalf("config.yaml missing effective listen:\n%s", raw)
	}
	if !strings.Contains(raw, "database: "+env.dbPath) {
		t.Fatalf("config.yaml missing effective database:\n%s", raw)
	}
}

// newSessionClient logs in against srv with password and returns a client that
// replays the session cookie on subsequent requests.
func newSessionClient(t *testing.T, srv *httptest.Server, password string) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Jar: jar}
	rec := doAs(t, client, srv, http.MethodPost, "/api/auth/login", `{"password":"`+password+`"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("login = %d %s", rec.Code, rec.Body.String())
	}
	return client
}

// setupAuthFixture installs an existing admin password and session secret on
// env, mirroring a configuration that was initialized but has no upstream yet.
func setupAuthFixture(t *testing.T, env *configTestEnv, password string) string {
	t.Helper()
	hash, err := secure.HashPassword(password)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := secure.RandomSecret()
	if err != nil {
		t.Fatal(err)
	}
	if err := env.config.Update(func(c *config.Config) error {
		c.Auth.AdminPasswordHash = hash
		c.Auth.SessionSecret = base64.StdEncoding.EncodeToString(raw)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return hash
}

func TestSetupWithExistingPasswordRequiresSession(t *testing.T) {
	env := newConfigEnv(t, "127.0.0.1:8100")
	hash := setupAuthFixture(t, env, "oldpass1")

	// A loopback request without a session must not be able to replace the
	// password or configure an upstream.
	rec := doPOST(t, env.srv, "/api/setup", `{
		"upstreamName":"Takeover",
		"baseUrl":"https://attacker.example.com",
		"adminPassword":"newpass99",
		"currentPassword":"oldpass1"
	}`)
	if rec.Code != http.StatusUnauthorized && rec.Code != http.StatusForbidden {
		t.Fatalf("unauthenticated setup = %d %s", rec.Code, rec.Body.String())
	}
	snap := env.config.Snapshot().Config
	if len(snap.Upstreams) != 0 {
		t.Fatalf("unauthenticated setup wrote upstreams: %+v", snap.Upstreams)
	}
	if snap.Auth.AdminPasswordHash != hash || !secure.VerifyPassword(hash, "oldpass1") {
		t.Fatal("unauthenticated setup changed the admin password hash")
	}
}

func TestSetupWithExistingPasswordRequiresCurrentPassword(t *testing.T) {
	env := newConfigEnv(t, "127.0.0.1:8100")
	hash := setupAuthFixture(t, env, "oldpass1")
	client := newSessionClient(t, env.srv, "oldpass1")

	for _, tc := range []struct {
		name string
		body string
	}{
		{"missing", `{"upstreamName":"DeepSeek","baseUrl":"https://api.deepseek.com","adminPassword":"newpass99"}`},
		{"wrong", `{"upstreamName":"DeepSeek","baseUrl":"https://api.deepseek.com","adminPassword":"newpass99","currentPassword":"wrongpass"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := doAs(t, client, env.srv, http.MethodPost, "/api/setup", tc.body)
			if rec.Code != http.StatusForbidden {
				t.Fatalf("setup = %d %s", rec.Code, rec.Body.String())
			}
			snap := env.config.Snapshot().Config
			if len(snap.Upstreams) != 0 {
				t.Fatalf("rejected setup wrote upstreams: %+v", snap.Upstreams)
			}
			if snap.Auth.AdminPasswordHash != hash {
				t.Fatal("rejected setup changed the admin password hash")
			}
		})
	}
}

func TestSetupReplacesPasswordWithSessionAndCurrentPassword(t *testing.T) {
	env := newConfigEnv(t, "127.0.0.1:8100")
	setupAuthFixture(t, env, "oldpass1")
	client := newSessionClient(t, env.srv, "oldpass1")

	rec := doAs(t, client, env.srv, http.MethodPost, "/api/setup", `{
		"upstreamName":"DeepSeek",
		"baseUrl":"https://api.deepseek.com",
		"adminPassword":"newpass99",
		"currentPassword":"oldpass1"
	}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("setup = %d %s", rec.Code, rec.Body.String())
	}
	snap := env.config.Snapshot().Config
	if len(snap.Upstreams) != 1 || snap.Upstreams[0].ID != "deepseek" {
		t.Fatalf("upstreams = %+v", snap.Upstreams)
	}
	if !secure.VerifyPassword(snap.Auth.AdminPasswordHash, "newpass99") {
		t.Fatal("setup did not install the new admin password")
	}
	if secure.VerifyPassword(snap.Auth.AdminPasswordHash, "oldpass1") {
		t.Fatal("old admin password still verifies")
	}
}

func TestSetupRejectsNonLoopbackHostWithPassword(t *testing.T) {
	env := newConfigEnv(t, "127.0.0.1:8100")
	hash := setupAuthFixture(t, env, "oldpass1")
	client := newSessionClient(t, env.srv, "oldpass1")
	login := doAs(t, client, env.srv, http.MethodPost, "/api/auth/login", `{"password":"oldpass1"}`)
	if login.Code != http.StatusOK {
		t.Fatalf("login = %d %s", login.Code, login.Body.String())
	}
	cookie := sessionCookie(t, login)

	req := httptest.NewRequest(http.MethodPost, "http://rebind.example/api/setup",
		strings.NewReader(`{"upstreamName":"Remote","baseUrl":"https://remote.example.com"}`))
	req.RemoteAddr = "127.0.0.1:5000"
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Requested-With", "llmmeter")
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	env.apiServer.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("non-loopback host setup = %d %s", rec.Code, rec.Body.String())
	}
	snap := env.config.Snapshot().Config
	if len(snap.Upstreams) != 0 || snap.Auth.AdminPasswordHash != hash {
		t.Fatal("non-loopback host setup mutated the configuration")
	}
}
