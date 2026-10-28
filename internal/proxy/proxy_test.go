package proxy

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kiry163/llmmeter/internal/config"
	"github.com/kiry163/llmmeter/internal/pricing"
	"github.com/kiry163/llmmeter/internal/store"
)

func newProxy(t *testing.T, upstreamURL string) (*Proxy, *store.Store) {
	t.Helper()
	cfg := &config.Config{
		Listen: "127.0.0.1:0", GatewayKey: "gw-key", Timezone: "Asia/Shanghai",
		Upstream: config.Upstream{BaseURL: upstreamURL, APIKey: "up-key", Timeout: config.Duration(5 * time.Second)},
		Pricing: config.Pricing{Models: map[string]config.Model{
			"deepseek-chat": {InputPerMTok: 1, OutputPerMTok: 10},
		}},
	}
	cat, err := pricing.New(cfg.Pricing)
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(filepath.Join(t.TempDir(), "p.db"))
	if err != nil {
		t.Fatal(err)
	}
	p, err := New(cfg, cat, st, func() time.Time { return time.Date(2026, 10, 28, 2, 0, 0, 0, time.UTC) })
	if err != nil {
		t.Fatal(err)
	}
	return p, st
}

func TestAuthRejected(t *testing.T) {
	p, st := newProxy(t, "http://127.0.0.1:1")
	defer st.Close()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{}`))
	p.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d", rr.Code)
	}
}

func TestChatNonStreamRecordsCost(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer up-key" {
			t.Errorf("upstream auth = %q", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"model":"deepseek-chat","usage":{"prompt_tokens":1000,"completion_tokens":100}}`)
	}))
	defer up.Close()

	p, st := newProxy(t, up.URL)
	body := `{"model":"deepseek-chat","messages":[{"role":"user","content":"hi"}]}`
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer gw-key")
	p.ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	if err := p.FlushForTest(); err != nil {
		t.Fatal(err)
	}
	rows, err := st.Requests("2026-10-28", "2026-10-28", "", 10, 0)
	if err != nil || len(rows) != 1 {
		t.Fatalf("rows = %d err=%v", len(rows), err)
	}
	// 1000 input @1/MTok = 1000*1*1000 nano; 100 output @10/MTok = 100*10*1000
	if rows[0].CostNanoUSD != 2_000_000 {
		t.Fatalf("cost = %d", rows[0].CostNanoUSD)
	}
	if rows[0].Model != "deepseek-chat" || rows[0].Usage.Prompt != 1000 {
		t.Fatalf("row = %+v", rows[0])
	}
}

func TestStreamChatInjectsAndRecordsUsage(t *testing.T) {
	var sawInclude bool
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		json.NewDecoder(r.Body).Decode(&req)
		opts, _ := req["stream_options"].(map[string]any)
		sawInclude, _ = opts["include_usage"].(bool)
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n")
		io.WriteString(w, "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":5}}\n\n")
		io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer up.Close()

	p, st := newProxy(t, up.URL)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(`{"model":"deepseek-chat","stream":true,"messages":[]}`))
	req.Header.Set("Authorization", "Bearer gw-key")
	p.ServeHTTP(rr, req)

	if !sawInclude {
		t.Fatal("expected include_usage injection")
	}
	if !strings.Contains(rr.Body.String(), "data: [DONE]") {
		t.Fatalf("client stream altered: %q", rr.Body.String())
	}
	p.FlushForTest()
	rows, _ := st.Requests("2026-10-28", "2026-10-28", "", 10, 0)
	if len(rows) != 1 || rows[0].Usage.Prompt != 10 || rows[0].UsageStatus != "ok" {
		t.Fatalf("rows = %+v", rows)
	}
}

func TestUpstreamErrorRecordedUnmetered(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"boom"}`, http.StatusInternalServerError)
	}))
	defer up.Close()

	p, st := newProxy(t, up.URL)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(`{"model":"deepseek-chat","messages":[]}`))
	req.Header.Set("Authorization", "Bearer gw-key")
	p.ServeHTTP(rr, req)
	p.FlushForTest()

	rows, _ := st.Requests("2026-10-28", "2026-10-28", "", 10, 0)
	if len(rows) != 1 || rows[0].Status != 500 || rows[0].UsageStatus != "no_usage" || rows[0].CostNanoUSD != 0 {
		t.Fatalf("rows = %+v", rows)
	}
}

func TestPassthroughPathNotStored(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"data":[]}`)
	}))
	defer up.Close()

	p, st := newProxy(t, up.URL)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.Header.Set("Authorization", "Bearer gw-key")
	p.ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Fatalf("status = %d", rr.Code)
	}
	p.FlushForTest()
	rows, _ := st.Requests("2026-10-28", "2026-10-28", "", 10, 0)
	if len(rows) != 0 {
		t.Fatalf("passthrough must not be stored, got %d", len(rows))
	}
}

func TestUpstreamUnreachableRecorded(t *testing.T) {
	p, st := newProxy(t, "http://127.0.0.1:1")
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(`{"model":"deepseek-chat","messages":[]}`))
	req.Header.Set("Authorization", "Bearer gw-key")
	p.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadGateway {
		t.Fatalf("status = %d", rr.Code)
	}
	p.FlushForTest()
	rows, _ := st.Requests("2026-10-28", "2026-10-28", "", 10, 0)
	if len(rows) != 1 || rows[0].Status != http.StatusBadGateway || rows[0].UsageStatus != "no_usage" {
		t.Fatalf("rows = %+v", rows)
	}
}

func TestInvalidJSONLabeledParseFailed(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, "not json at all")
	}))
	defer up.Close()

	p, st := newProxy(t, up.URL)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(`{"model":"deepseek-chat","messages":[]}`))
	req.Header.Set("Authorization", "Bearer gw-key")
	p.ServeHTTP(rr, req)
	p.FlushForTest()
	rows, _ := st.Requests("2026-10-28", "2026-10-28", "", 10, 0)
	if len(rows) != 1 || rows[0].UsageStatus != "parse_failed" {
		t.Fatalf("rows = %+v", rows)
	}
}

func TestPassthroughAccessLog(t *testing.T) {
	var buf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(old) })

	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"data":[]}`)
	}))
	defer up.Close()

	p, st := newProxy(t, up.URL)
	defer st.Close()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.Header.Set("Authorization", "Bearer gw-key")
	p.ServeHTTP(rr, req)
	if !strings.Contains(buf.String(), "access") || !strings.Contains(buf.String(), "/v1/models") {
		t.Fatalf("missing access log: %q", buf.String())
	}
}
