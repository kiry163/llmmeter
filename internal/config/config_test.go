package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadExpandsEnvAndDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	body := `
listen: "127.0.0.1:8100"
gateway_key: "sk-local"
timezone: "Asia/Shanghai"
upstream:
  base_url: "https://api.deepseek.com"
  api_key: "${env:TEST_DEEPSEEK_KEY}"
pricing:
  models:
    deepseek-chat:
      input_per_mtok: 0.28
      output_per_mtok: 0.42
      windows:
        - label: peak
          days: [mon, fri]
          start: "09:00"
          end: "12:00"
          input_per_mtok: 0.56
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TEST_DEEPSEEK_KEY", "sk-upstream")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Upstream.APIKey != "sk-upstream" {
		t.Errorf("api key = %q", cfg.Upstream.APIKey)
	}
	if cfg.Upstream.Timeout.Std() != 300*time.Second {
		t.Errorf("default timeout = %v", cfg.Upstream.Timeout.Std())
	}
	w := cfg.Pricing.Models["deepseek-chat"].Windows[0]
	if w.InputPerMTok == nil || *w.InputPerMTok != 0.56 {
		t.Errorf("window override not parsed: %+v", w)
	}
	if w.OutputPerMTok != nil {
		t.Errorf("absent override should stay nil")
	}
}

func TestLoadRejectsBadWindow(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	body := `
gateway_key: "k"
timezone: "Asia/Shanghai"
upstream: {base_url: "https://example.com", api_key: "x"}
pricing:
  models:
    m:
      windows:
        - label: p
          start: "9:00"
          end: "08:00"
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("expected error for malformed start time")
	}
}
