package gateway_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kiry163/llmmeter/internal/gateway"
	"github.com/kiry163/llmmeter/internal/metrics"
	"github.com/kiry163/llmmeter/internal/settings"
	"github.com/kiry163/llmmeter/internal/store"
	"github.com/kiry163/llmmeter/internal/usage"
)

func TestClassifyPath(t *testing.T) {
	cases := map[string]string{
		"/v1/chat/completions": "chat", "/v1/responses": "responses",
		"/v1/models": "models", "/v1/audio/speech": "audio", "/foo": "other",
	}
	for path, want := range cases {
		if got := gateway.ClassifyPath(path); got != want {
			t.Fatalf("%s: got %q want %q", path, got, want)
		}
	}
}

func TestRequestHeaderBytesFormula(t *testing.T) {
	r := httptest.NewRequest("POST", "http://gw/v1/chat/completions", nil)
	r.Header.Set("X-A", "1")
	// "POST /v1/chat/completions HTTP/1.1\r\n" + "X-A: 1\r\n" + "\r\n"
	want := int64(len("POST /v1/chat/completions HTTP/1.1\r\n") + len("X-A: 1\r\n") + 2)
	if got := gateway.RequestHeaderBytes(r); got != want {
		t.Fatalf("got %d want %d", got, want)
	}
}

// newTestGateway builds a gateway over a temporary database with a priced
// deepseek-chat model and a frozen clock.
func newTestGateway(t *testing.T, upstreamURL string) (*gateway.Gateway, *store.Store, *settings.Repo) {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "gw.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	repo, err := settings.Open(st.DB())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Setup(ctx, settings.SetupInput{
		Upstream: settings.Upstream{BaseURL: upstreamURL, APIKey: "sk-up", Timeout: 10 * time.Second},
		Timezone: "UTC",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.CreateModel(ctx, settings.ModelInput{
		Name: "deepseek-chat", Input: 0.28, Output: 0.42,
	}); err != nil {
		t.Fatal(err)
	}
	clock := func() time.Time { return time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC) }
	hub := metrics.New(st, clock)
	return gateway.New(repo, st, hub, clock), st, repo
}

func TestProxyRecordsBytesAndUsage(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if r.Header.Get("Authorization") != "Bearer sk-up" {
			t.Errorf("upstream key not set")
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		fmt.Fprintf(w, `{"model":"deepseek-chat","usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15},"echo":%q}`, string(body))
	}))
	defer upstream.Close()

	g, st, repo := newTestGateway(t, upstream.URL)
	req := httptest.NewRequest("POST", "/v1/chat/completions",
		strings.NewReader(`{"model":"deepseek-chat","messages":[]}`))
	req.Header.Set("Authorization", "Bearer "+repo.Snapshot().GatewayKey)
	rec := httptest.NewRecorder()
	g.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	if err := st.Flush(); err != nil {
		t.Fatal(err)
	}
	rows, err := st.Requests(store.RequestFilter{From: "2026-10-29", To: "2026-10-29", Limit: 10})
	if err != nil || len(rows) != 1 {
		t.Fatalf("rows=%v err=%v", rows, err)
	}
	r0 := rows[0]
	if r0.PathClass != "chat" || r0.Model != "deepseek-chat" || r0.Usage.PromptTokens != 10 {
		t.Fatalf("record = %+v", r0)
	}
	if r0.ReqBodyBytes != int64(len(`{"model":"deepseek-chat","messages":[]}`)) {
		t.Fatalf("req body bytes = %d", r0.ReqBodyBytes)
	}
	if r0.RespBodyBytes != int64(rec.Body.Len()) {
		t.Fatalf("resp body bytes = %d want %d", r0.RespBodyBytes, rec.Body.Len())
	}
	if r0.ReqHeaderBytes == 0 || r0.RespHeaderBytes == 0 {
		t.Fatalf("header bytes missing")
	}
	if !r0.Priced || r0.CostNanoUSD <= 0 {
		t.Fatalf("not priced: %+v", r0)
	}
}

func TestStreamUsageAndInjection(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), `"include_usage":true`) {
			t.Errorf("no injection: %s", body)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n")
		flusher.Flush()
		io.WriteString(w, "data: {\"usage\":{\"prompt_tokens\":7,\"completion_tokens\":3,\"total_tokens\":10}}\n\n")
		io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer upstream.Close()

	g, st, repo := newTestGateway(t, upstream.URL)
	req := httptest.NewRequest("POST", "/v1/chat/completions",
		strings.NewReader(`{"model":"deepseek-chat","stream":true}`))
	req.Header.Set("Authorization", "Bearer "+repo.Snapshot().GatewayKey)
	rec := httptest.NewRecorder()
	g.ServeHTTP(rec, req)
	st.Flush()
	rows, _ := st.Requests(store.RequestFilter{From: "2026-10-29", To: "2026-10-29", Limit: 10})
	if len(rows) != 1 || rows[0].Usage.PromptTokens != 7 {
		t.Fatalf("rows = %+v", rows)
	}
}

func TestAllPathsRecorded(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"ok":true}`)
	}))
	defer upstream.Close()
	g, st, repo := newTestGateway(t, upstream.URL)
	req := httptest.NewRequest("GET", "/v2/whatever?key=secret", nil)
	req.Header.Set("Authorization", "Bearer "+repo.Snapshot().GatewayKey)
	g.ServeHTTP(httptest.NewRecorder(), req)
	st.Flush()
	rows, _ := st.Requests(store.RequestFilter{From: "2026-10-29", To: "2026-10-29", Limit: 10})
	if len(rows) != 1 || rows[0].UsageStatus != usage.StatusNotApplicable || rows[0].Path != "/v2/whatever" {
		t.Fatalf("rows = %+v", rows)
	}
}

func TestUpstreamErrorRecorded(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", 500)
	}))
	defer upstream.Close()
	g, st, repo := newTestGateway(t, upstream.URL)
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"m"}`))
	req.Header.Set("Authorization", "Bearer "+repo.Snapshot().GatewayKey)
	rec := httptest.NewRecorder()
	g.ServeHTTP(rec, req)
	if rec.Code != 500 {
		t.Fatalf("status %d", rec.Code)
	}
	st.Flush()
	rows, _ := st.Requests(store.RequestFilter{From: "2026-10-29", To: "2026-10-29", Limit: 10})
	if len(rows) != 1 || rows[0].Status != 500 || rows[0].UsageStatus != usage.StatusNoUsage {
		t.Fatalf("rows = %+v", rows)
	}
}

func TestOversizedRequestMarksTooLarge(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		io.WriteString(w, `{"ok":true}`)
	}))
	defer upstream.Close()
	g, st, repo := newTestGateway(t, upstream.URL)
	body := `{"model":"m","pad":"` + strings.Repeat("x", 1<<20) + `"}`
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+repo.Snapshot().GatewayKey)
	rec := httptest.NewRecorder()
	g.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status %d", rec.Code)
	}
	st.Flush()
	rows, _ := st.Requests(store.RequestFilter{From: "2026-10-29", To: "2026-10-29", Limit: 10})
	if len(rows) != 1 || rows[0].UsageStatus != usage.StatusTooLarge ||
		rows[0].ReqBodyBytes != int64(len(body)) {
		t.Fatalf("rows = %+v", rows)
	}
}

func TestSSELineOverLimitStillForwards(t *testing.T) {
	big := strings.Repeat("x", 1<<20+1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: "+big+"\n\n")
		io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer upstream.Close()
	g, st, repo := newTestGateway(t, upstream.URL)
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"m","stream":true}`))
	req.Header.Set("Authorization", "Bearer "+repo.Snapshot().GatewayKey)
	rec := httptest.NewRecorder()
	g.ServeHTTP(rec, req)
	if rec.Body.Len() < 1<<20 {
		t.Fatalf("client received %d bytes", rec.Body.Len())
	}
	st.Flush()
	rows, _ := st.Requests(store.RequestFilter{From: "2026-10-29", To: "2026-10-29", Limit: 10})
	if len(rows) != 1 || rows[0].UsageStatus != usage.StatusTooLarge {
		t.Fatalf("rows = %+v", rows)
	}
}

func TestDisconnectDrainsUpstreamAndRecordsUsage(t *testing.T) {
	release := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		f, _ := w.(http.Flusher)
		io.WriteString(w, "data: {\"choices\":[]}\n\n")
		f.Flush()
		<-release
		io.WriteString(w, "data: {\"usage\":{\"prompt_tokens\":9,\"completion_tokens\":1}}\n\n")
		io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer upstream.Close()

	g, st, repo := newTestGateway(t, upstream.URL)
	gwServer := httptest.NewServer(g)
	defer gwServer.Close()

	req, _ := http.NewRequest("POST", gwServer.URL+"/v1/chat/completions",
		strings.NewReader(`{"model":"m","stream":true}`))
	req.Header.Set("Authorization", "Bearer "+repo.Snapshot().GatewayKey)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 1)
	resp.Body.Read(buf)
	resp.Body.Close() // 客户端提前断开
	close(release)

	deadline := time.Now().Add(5 * time.Second)
	for {
		st.Flush()
		rows, _ := st.Requests(store.RequestFilter{From: "2026-10-29", To: "2026-10-29", Limit: 10})
		if len(rows) == 1 {
			if !rows[0].ClientDisconnected {
				t.Fatalf("flag missing: %+v", rows[0])
			}
			if rows[0].Usage.PromptTokens != 9 {
				t.Fatalf("usage = %+v", rows[0].Usage)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("no record after disconnect")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestMalformedUsageMarksParseFailed(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"model":"m","usage":{"prompt_tokens":"ten"}}`)
	}))
	defer upstream.Close()
	g, st, repo := newTestGateway(t, upstream.URL)
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"m"}`))
	req.Header.Set("Authorization", "Bearer "+repo.Snapshot().GatewayKey)
	g.ServeHTTP(httptest.NewRecorder(), req)
	st.Flush()
	rows, _ := st.Requests(store.RequestFilter{From: "2026-10-29", To: "2026-10-29", Limit: 10})
	if len(rows) != 1 || rows[0].UsageStatus != usage.StatusParseFailed {
		t.Fatalf("rows = %+v", rows)
	}
}
