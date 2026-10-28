package e2e

import (
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kiry163/llmmeter/internal/config"
	"github.com/kiry163/llmmeter/internal/pricing"
	"github.com/kiry163/llmmeter/internal/proxy"
	"github.com/kiry163/llmmeter/internal/store"
	"github.com/kiry163/llmmeter/internal/web"
)

func TestEndToEndStreamingAccounting(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n")
		io.WriteString(w, "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":1000,\"completion_tokens\":100,\"prompt_cache_hit_tokens\":200}}\n\n")
		io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer up.Close()

	dir := t.TempDir()
	cfg := &config.Config{
		Listen: "127.0.0.1:0", GatewayKey: "gw-key", Timezone: "Asia/Shanghai",
		Upstream: config.Upstream{BaseURL: up.URL, APIKey: "up-key", Timeout: config.Duration(5 * time.Second)},
		Pricing: config.Pricing{Models: map[string]config.Model{
			"deepseek-chat": {InputPerMTok: 1, OutputPerMTok: 10, CacheReadPerMTok: 0.1},
		}},
	}
	cat, err := pricing.New(cfg.Pricing)
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(filepath.Join(dir, "e2e.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	p, err := proxy.New(cfg, cat, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	loc, _ := time.LoadLocation(cfg.Timezone)
	page := web.New(st, loc)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" || strings.HasPrefix(r.URL.Path, "/api/") {
			page.ServeHTTP(w, r)
			return
		}
		p.ServeHTTP(w, r)
	}))
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/chat/completions",
		strings.NewReader(`{"model":"deepseek-chat","stream":true,"messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Authorization", "Bearer gw-key")
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "[DONE]") {
		t.Fatalf("stream truncated: %q", body)
	}

	if err := st.Flush(); err != nil {
		t.Fatal(err)
	}

	sumResp, err := http.Get(srv.URL + "/api/summary")
	if err != nil {
		t.Fatal(err)
	}
	defer sumResp.Body.Close()
	sumBody, _ := io.ReadAll(sumResp.Body)
	if !strings.Contains(string(sumBody), `"requests":1`) {
		t.Fatalf("summary = %s", sumBody)
	}
	// 1000 prompt, 200 cached: 800*1 + 200*0.1 + 100*10 = 1820 micro-USD = 1,820,000 nano.
	if !strings.Contains(string(sumBody), `"cost_nano_usd":1820000`) {
		t.Fatalf("cost mismatch: %s", sumBody)
	}

	csvResp, err := http.Get(srv.URL + "/api/export.csv")
	if err != nil {
		t.Fatal(err)
	}
	defer csvResp.Body.Close()
	csvBody, _ := io.ReadAll(csvResp.Body)
	if !strings.Contains(string(csvBody), "deepseek-chat") {
		t.Fatalf("csv missing row: %s", csvBody)
	}
}
