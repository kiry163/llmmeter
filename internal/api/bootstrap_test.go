package api_test

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/kiry163/llmmeter/internal/store"
)

func TestBootstrapNeedsSetupReflectsConfig(t *testing.T) {
	env := newConfigEnv(t, "127.0.0.1:8100")
	rec := doGET(t, env.srv, "/api/bootstrap")
	if rec.Code != http.StatusOK {
		t.Fatalf("bootstrap = %d %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Version      string `json:"version"`
		NeedsSetup   bool   `json:"needsSetup"`
		AuthRequired bool   `json:"authRequired"`
		Listen       string `json:"listen"`
		Timezone     string `json:"timezone"`
		ConfigPath   string `json:"configPath"`
		DBPath       string `json:"dbPath"`
		EarliestDay  string `json:"earliestDay"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if !body.NeedsSetup || body.AuthRequired {
		t.Fatalf("bootstrap = %+v", body)
	}
	if body.Version != "test" || body.Listen != "127.0.0.1:8100" || body.Timezone != "UTC" {
		t.Fatalf("bootstrap = %+v", body)
	}
	if body.ConfigPath != env.configPath || body.DBPath != env.dbPath || body.EarliestDay != "" {
		t.Fatalf("bootstrap paths = %+v", body)
	}

	addTestUpstream(t, env.config, "up")
	rec = doGET(t, env.srv, "/api/bootstrap")
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.NeedsSetup {
		t.Fatalf("needsSetup after upstream = %s", rec.Body.String())
	}
}

func TestBootstrapHidesSecretsAfterSetup(t *testing.T) {
	env := newConfigEnv(t, "127.0.0.1:8100")
	rec := doPOST(t, env.srv, "/api/setup", `{
		"upstreamName":"DeepSeek","baseUrl":"https://api.deepseek.com",
		"apiKey":"sk-upstream-secret","timezone":"Asia/Shanghai","adminPassword":"hunter22"
	}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("setup = %d %s", rec.Code, rec.Body.String())
	}
	rec = doGET(t, env.srv, "/api/bootstrap")
	if rec.Code != http.StatusOK {
		t.Fatalf("bootstrap = %d %s", rec.Code, rec.Body.String())
	}
	var body struct {
		NeedsSetup   bool   `json:"needsSetup"`
		AuthRequired bool   `json:"authRequired"`
		Timezone     string `json:"timezone"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.NeedsSetup || !body.AuthRequired || body.Timezone != "Asia/Shanghai" {
		t.Fatalf("bootstrap = %+v", body)
	}
	for _, secret := range []string{"sk-local-", "sk-upstream-secret", "session_secret", "admin_password"} {
		if strings.Contains(rec.Body.String(), secret) {
			t.Fatalf("bootstrap leaked %q: %s", secret, rec.Body.String())
		}
	}
}

func TestGeneralPatchRequiresSetupAndPersists(t *testing.T) {
	env := newConfigEnv(t, "127.0.0.1:8100")
	rec := doPATCH(t, env.srv, "/api/general", `{"timezone":"Asia/Tokyo"}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("pre-setup patch = %d %s", rec.Code, rec.Body.String())
	}

	addTestUpstream(t, env.config, "up")
	rec = doGET(t, env.srv, "/api/general")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"retentionDays":90`) {
		t.Fatalf("get general = %d %s", rec.Code, rec.Body.String())
	}

	rec = doPATCH(t, env.srv, "/api/general", `{"timezone":"Asia/Tokyo","retentionDays":30}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("patch general = %d %s", rec.Code, rec.Body.String())
	}
	snap := env.config.Snapshot()
	if snap.Config.General.Timezone != "Asia/Tokyo" || snap.Config.General.RetentionDays != 30 {
		t.Fatalf("general = %+v", snap.Config.General)
	}
	raw := readConfigFile(t, env.configPath)
	if !strings.Contains(raw, "timezone: Asia/Tokyo") || !strings.Contains(raw, "retention_days: 30") {
		t.Fatalf("config.yaml = %s", raw)
	}

	rec = doGET(t, env.srv, "/api/general")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"timezone":"Asia/Tokyo"`) ||
		!strings.Contains(rec.Body.String(), `"retentionDays":30`) {
		t.Fatalf("get general after patch = %d %s", rec.Code, rec.Body.String())
	}
}

func TestGeneralPatchValidation(t *testing.T) {
	env := newConfigEnv(t, "127.0.0.1:8100")
	addTestUpstream(t, env.config, "up")

	rec := doPATCH(t, env.srv, "/api/general", `{"timezone":"Nowhere/Nope"}`)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), `"timezone"`) {
		t.Fatalf("bad timezone = %d %s", rec.Code, rec.Body.String())
	}
	rec = doPATCH(t, env.srv, "/api/general", `{"retentionDays":1}`)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), `"retentionDays"`) {
		t.Fatalf("bad retention = %d %s", rec.Code, rec.Body.String())
	}
	if got := env.config.Snapshot().Config.General.RetentionDays; got != 90 {
		t.Fatalf("retention changed by rejected patch: %d", got)
	}
}

func TestDataInfoReportsPathsAndState(t *testing.T) {
	env := newConfigEnv(t, "127.0.0.1:8100")
	env.data.EnqueueMinute(store.MinuteDelta{
		LocalDay: "2026-10-29", MinuteOfDay: 720, PathClass: "chat",
		UpstreamID: "up", Model: "m", Requests: 1,
	})
	if err := env.data.Flush(); err != nil {
		t.Fatal(err)
	}

	rec := doGET(t, env.srv, "/api/data")
	if rec.Code != http.StatusOK {
		t.Fatalf("data = %d %s", rec.Code, rec.Body.String())
	}
	var body struct {
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
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.ConfigPath != env.configPath || body.DBPath != env.dbPath {
		t.Fatalf("paths = %+v", body)
	}
	if body.DBBytes <= 0 || body.SchemaVersion != store.UserVersion {
		t.Fatalf("data = %+v", body)
	}
	if body.Version != "test" || body.RetentionDays != 90 || body.Timezone != "UTC" {
		t.Fatalf("data = %+v", body)
	}
	if body.EffectiveListen != "127.0.0.1:8100" {
		t.Fatalf("effective listen = %q", body.EffectiveListen)
	}
	if body.WriteQueue != 0 || body.EarliestDay != "2026-10-29" {
		t.Fatalf("data = %+v", body)
	}
}

func TestConfigReloadUsesNewFileAndKeepsOldOnFailure(t *testing.T) {
	env := newConfigEnv(t, "127.0.0.1:8100")
	valid := `version: 1
server:
  listen: 127.0.0.1:8100
  database: ` + env.dbPath + `
general:
  timezone: Asia/Tokyo
  retention_days: 45
upstreams:
  - id: reloaded
    name: Reloaded
    base_url: https://reload.example.com
    api_key: sk-reload
    models: []
gateway_keys: []
`
	if err := os.WriteFile(env.configPath, []byte(valid), 0o600); err != nil {
		t.Fatal(err)
	}
	rec := doPOST(t, env.srv, "/api/config/reload", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("reload = %d %s", rec.Code, rec.Body.String())
	}
	snap := env.config.Snapshot()
	if len(snap.Config.Upstreams) != 1 || snap.Config.Upstreams[0].ID != "reloaded" ||
		snap.Config.General.Timezone != "Asia/Tokyo" || snap.Config.General.RetentionDays != 45 {
		t.Fatalf("snapshot after reload = %+v", snap.Config)
	}

	if err := os.WriteFile(env.configPath, []byte("version: 1\nupstreams: [\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	rec = doPOST(t, env.srv, "/api/config/reload", "")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("bad reload = %d %s", rec.Code, rec.Body.String())
	}
	snap = env.config.Snapshot()
	if len(snap.Config.Upstreams) != 1 || snap.Config.Upstreams[0].ID != "reloaded" {
		t.Fatalf("old config not retained: %+v", snap.Config.Upstreams)
	}
}
