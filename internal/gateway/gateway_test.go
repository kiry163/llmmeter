package gateway_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kiry163/llmmeter/internal/config"
	"github.com/kiry163/llmmeter/internal/gateway"
	"github.com/kiry163/llmmeter/internal/metrics"
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

const testGatewayKey = "sk-local-test-000000000000000000000000000000"

// testClock is the frozen clock shared by the gateway and its metrics hub so
// every record falls on one reporting day.
func testClock() time.Time {
	return time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
}

// newTestGatewayWith builds a gateway over a temporary database and config
// store with the given upstreams and keys.
func newTestGatewayWith(t *testing.T, upstreams []config.Upstream, keys []config.GatewayKey) (*gateway.Gateway, *store.Store, *config.Store, *metrics.Hub) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "gw.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	cfg, err := config.OpenStore(filepath.Join(t.TempDir(), "config.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.Update(func(c *config.Config) error {
		c.Server.Database = dbPath
		c.General.Timezone = "UTC"
		c.Upstreams = upstreams
		c.GatewayKeys = keys
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	hub := metrics.New(st, testClock)
	return gateway.New(cfg, st, hub, testClock), st, cfg, hub
}

// newTestGateway builds a gateway over a temporary database with one upstream,
// one local key, a priced deepseek-chat model and a frozen clock.
func newTestGateway(t *testing.T, upstreamURL string) (*gateway.Gateway, *store.Store, string) {
	t.Helper()
	g, st, _, _ := newTestGatewayWith(t,
		[]config.Upstream{{
			ID:      "deepseek",
			Name:    "DeepSeek",
			BaseURL: upstreamURL,
			APIKey:  "sk-up",
			Models: []config.Model{{
				Name: "deepseek-chat", Input: 0.28, Output: 0.42,
			}},
		}},
		[]config.GatewayKey{{
			ID: "local", Name: "Local", Key: testGatewayKey, UpstreamID: "deepseek",
		}},
	)
	return g, st, testGatewayKey
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

	g, st, gwKey := newTestGateway(t, upstream.URL)
	req := httptest.NewRequest("POST", "/v1/chat/completions",
		strings.NewReader(`{"model":"deepseek-chat","messages":[]}`))
	req.Header.Set("Authorization", "Bearer "+gwKey)
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

	g, st, gwKey := newTestGateway(t, upstream.URL)
	req := httptest.NewRequest("POST", "/v1/chat/completions",
		strings.NewReader(`{"model":"deepseek-chat","stream":true}`))
	req.Header.Set("Authorization", "Bearer "+gwKey)
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
	g, st, gwKey := newTestGateway(t, upstream.URL)
	req := httptest.NewRequest("GET", "/v2/whatever?key=secret", nil)
	req.Header.Set("Authorization", "Bearer "+gwKey)
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
	g, st, gwKey := newTestGateway(t, upstream.URL)
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"m"}`))
	req.Header.Set("Authorization", "Bearer "+gwKey)
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
	g, st, gwKey := newTestGateway(t, upstream.URL)
	body := `{"model":"m","pad":"` + strings.Repeat("x", 1<<20) + `"}`
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+gwKey)
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
	g, st, gwKey := newTestGateway(t, upstream.URL)
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"m","stream":true}`))
	req.Header.Set("Authorization", "Bearer "+gwKey)
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

	g, st, gwKey := newTestGateway(t, upstream.URL)
	gwServer := httptest.NewServer(g)
	defer gwServer.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "POST", gwServer.URL+"/v1/chat/completions",
		strings.NewReader(`{"model":"m","stream":true}`))
	req.Header.Set("Authorization", "Bearer "+gwKey)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 1)
	resp.Body.Read(buf)
	cancel() // 客户端提前断开：取消请求上下文，连接立即关闭
	resp.Body.Close()
	// 让代理进程观察到上下文取消后再放行上游剩余数据，避免与记录竞争。
	time.Sleep(50 * time.Millisecond)
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
	g, st, gwKey := newTestGateway(t, upstream.URL)
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"m"}`))
	req.Header.Set("Authorization", "Bearer "+gwKey)
	g.ServeHTTP(httptest.NewRecorder(), req)
	st.Flush()
	rows, _ := st.Requests(store.RequestFilter{From: "2026-10-29", To: "2026-10-29", Limit: 10})
	if len(rows) != 1 || rows[0].UsageStatus != usage.StatusParseFailed {
		t.Fatalf("rows = %+v", rows)
	}
}

func TestLargeNonStreamResponseUsageParsed(t *testing.T) {
	body := `{"model":"deepseek-chat","usage":{"prompt_tokens":11,"completion_tokens":2,"total_tokens":13},"pad":"` +
		strings.Repeat("x", 3<<19) + `"}`
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, body)
	}))
	defer upstream.Close()
	g, st, gwKey := newTestGateway(t, upstream.URL)
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"deepseek-chat"}`))
	req.Header.Set("Authorization", "Bearer "+gwKey)
	rec := httptest.NewRecorder()
	g.ServeHTTP(rec, req)
	if rec.Body.Len() != len(body) {
		t.Fatalf("client body = %d want %d", rec.Body.Len(), len(body))
	}
	st.Flush()
	rows, _ := st.Requests(store.RequestFilter{From: "2026-10-29", To: "2026-10-29", Limit: 10})
	if len(rows) != 1 || rows[0].UsageStatus != usage.StatusOK || rows[0].Usage.PromptTokens != 11 {
		t.Fatalf("rows = %+v", rows)
	}
	if rows[0].RespBodyBytes != int64(len(body)) {
		t.Fatalf("resp body bytes = %d want %d", rows[0].RespBodyBytes, len(body))
	}
}

func TestResponseBytesMatchWireBodyWithoutTransparentGzip(t *testing.T) {
	body := `{"model":"deepseek-chat","usage":{"prompt_tokens":3,"completion_tokens":1,"total_tokens":4}}`
	acceptEncoding := make(chan string, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		acceptEncoding <- r.Header.Get("Accept-Encoding")
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, body)
	}))
	defer upstream.Close()
	g, st, gwKey := newTestGateway(t, upstream.URL)
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"deepseek-chat"}`))
	req.Header.Set("Authorization", "Bearer "+gwKey)
	req.Header.Set("Accept-Encoding", "gzip")
	g.ServeHTTP(httptest.NewRecorder(), req)
	if got := <-acceptEncoding; got != "" {
		t.Fatalf("upstream received Accept-Encoding %q", got)
	}
	st.Flush()
	rows, _ := st.Requests(store.RequestFilter{From: "2026-10-29", To: "2026-10-29", Limit: 10})
	if len(rows) != 1 || rows[0].RespBodyBytes != int64(len(body)) || rows[0].Usage.PromptTokens != 3 {
		t.Fatalf("rows = %+v", rows)
	}
}

func TestLargeNonUsageResponseIsForwardedWithoutUsageTracking(t *testing.T) {
	const size = 2 << 20
	payload := strings.Repeat("x", size)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "audio/mpeg")
		w.WriteHeader(http.StatusOK)
		io.WriteString(w, payload)
	}))
	defer upstream.Close()

	g, st, gwKey := newTestGateway(t, upstream.URL)
	req := httptest.NewRequest("POST", "/v1/audio/speech", strings.NewReader(`{"model":"tts"}`))
	req.Header.Set("Authorization", "Bearer "+gwKey)
	rec := httptest.NewRecorder()
	g.ServeHTTP(rec, req)
	if rec.Code != 200 || rec.Body.Len() != size {
		t.Fatalf("client got %d bytes status %d", rec.Body.Len(), rec.Code)
	}
	if err := st.Flush(); err != nil {
		t.Fatal(err)
	}
	rows, err := st.Requests(store.RequestFilter{From: "2026-10-29", To: "2026-10-29", Limit: 10})
	if err != nil || len(rows) != 1 {
		t.Fatalf("rows=%v err=%v", rows, err)
	}
	r0 := rows[0]
	if r0.UsageStatus != usage.StatusNotApplicable || !r0.Priced {
		t.Fatalf("record = %+v", r0)
	}
	if r0.RespBodyBytes != int64(size) || r0.Usage.TotalTokens != 0 {
		t.Fatalf("bytes=%d usage=%+v", r0.RespBodyBytes, r0.Usage)
	}
}

// gatewayErrorBody is the OpenAI-shaped error envelope the gateway returns.
type gatewayErrorBody struct {
	Error struct {
		Message string `json:"message"`
		Type    string `json:"type"`
		Code    string `json:"code"`
	} `json:"error"`
}

func decodeGatewayError(t *testing.T, rec *httptest.ResponseRecorder) gatewayErrorBody {
	t.Helper()
	var body gatewayErrorBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("error body %q: %v", rec.Body.String(), err)
	}
	return body
}

func chatModel() []config.Model {
	return []config.Model{{Name: "deepseek-chat", Input: 1, Output: 2}}
}

// upstreamWithBody returns a test upstream that answers with body and reports
// the Authorization header it received on gotAuth.
func upstreamWithBody(t *testing.T, body string, gotAuth chan<- string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		if gotAuth != nil {
			gotAuth <- r.Header.Get("Authorization")
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestGatewayRoutesKeyToBoundUpstream(t *testing.T) {
	a := upstreamWithBody(t, `{"from":"a"}`, nil)
	b := upstreamWithBody(t, `{"from":"b"}`, nil)
	keys := []config.GatewayKey{
		{ID: "key-a", Name: "Key A", Key: "sk-local-key-a-000000000000000000000000", UpstreamID: "a"},
		{ID: "key-b", Name: "Key B", Key: "sk-local-key-b-000000000000000000000000", UpstreamID: "b"},
	}
	g, st, _, hub := newTestGatewayWith(t,
		[]config.Upstream{
			{ID: "a", Name: "Upstream A", BaseURL: a.URL, APIKey: "sk-a", Models: chatModel()},
			{ID: "b", Name: "Upstream B", BaseURL: b.URL, APIKey: "sk-b", Models: chatModel()},
		}, keys)

	for i, key := range keys {
		req := httptest.NewRequest("POST", "/v1/chat/completions",
			strings.NewReader(`{"model":"deepseek-chat"}`))
		req.Header.Set("Authorization", "Bearer "+key.Key)
		rec := httptest.NewRecorder()
		g.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("request %d status %d body %s", i, rec.Code, rec.Body.String())
		}
		want := fmt.Sprintf(`{"from":"%s"}`, key.UpstreamID)
		if rec.Body.String() != want {
			t.Fatalf("request %d body %q want %q", i, rec.Body.String(), want)
		}
	}

	if err := st.Flush(); err != nil {
		t.Fatal(err)
	}
	rows, err := st.Requests(store.RequestFilter{From: "2026-10-29", To: "2026-10-29", Limit: 10})
	if err != nil || len(rows) != 2 {
		t.Fatalf("rows=%v err=%v", rows, err)
	}
	byUpstream := make(map[string]store.Record, len(rows))
	for _, r := range rows {
		byUpstream[r.UpstreamID] = r
	}
	for _, want := range []struct {
		upstream, upstreamName, keyID, keyName string
	}{
		{"a", "Upstream A", "key-a", "Key A"},
		{"b", "Upstream B", "key-b", "Key B"},
	} {
		r, ok := byUpstream[want.upstream]
		if !ok {
			t.Fatalf("missing record for upstream %q: %+v", want.upstream, rows)
		}
		if r.UpstreamName != want.upstreamName || r.KeyID != want.keyID || r.KeyName != want.keyName {
			t.Fatalf("record snapshots = %+v", r)
		}
	}

	live := hub.Snapshot(time.Minute)
	for _, id := range []string{"a", "b"} {
		if live.ByUpstream[id].Requests != 1 {
			t.Fatalf("metrics for upstream %q = %+v", id, live.ByUpstream[id])
		}
	}
}

func TestGatewayRebindAffectsOnlyNewRequests(t *testing.T) {
	a := upstreamWithBody(t, `{"from":"a"}`, nil)
	b := upstreamWithBody(t, `{"from":"b"}`, nil)
	const key = "sk-local-rebind-000000000000000000000000"
	g, st, cfg, _ := newTestGatewayWith(t,
		[]config.Upstream{
			{ID: "a", Name: "Upstream A", BaseURL: a.URL, APIKey: "sk-a", Models: chatModel()},
			{ID: "b", Name: "Upstream B", BaseURL: b.URL, APIKey: "sk-b", Models: chatModel()},
		},
		[]config.GatewayKey{{ID: "key", Name: "Key", Key: key, UpstreamID: "a"}},
	)

	send := func() string {
		req := httptest.NewRequest("POST", "/v1/chat/completions",
			strings.NewReader(`{"model":"deepseek-chat"}`))
		req.Header.Set("Authorization", "Bearer "+key)
		rec := httptest.NewRecorder()
		g.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
		}
		return rec.Body.String()
	}

	if got := send(); got != `{"from":"a"}` {
		t.Fatalf("first request body = %q", got)
	}
	if err := cfg.Update(func(c *config.Config) error {
		c.GatewayKeys[0].UpstreamID = "b"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if got := send(); got != `{"from":"b"}` {
		t.Fatalf("second request body = %q", got)
	}

	if err := st.Flush(); err != nil {
		t.Fatal(err)
	}
	rows, err := st.Requests(store.RequestFilter{From: "2026-10-29", To: "2026-10-29", Limit: 10})
	if err != nil || len(rows) != 2 {
		t.Fatalf("rows=%v err=%v", rows, err)
	}
	counts := map[string]int{}
	for _, r := range rows {
		counts[r.UpstreamID]++
	}
	if counts["a"] != 1 || counts["b"] != 1 {
		t.Fatalf("upstream counts = %v", counts)
	}
}

func TestGatewayMissingOrDisabledKeyIs401(t *testing.T) {
	upstream := upstreamWithBody(t, `{"ok":true}`, nil)
	disabled := false
	const (
		goodKey = "sk-local-good-000000000000000000000000"
		badKey  = "sk-local-disabled-0000000000000000000000"
	)
	g, _, _, _ := newTestGatewayWith(t,
		[]config.Upstream{{ID: "a", Name: "Upstream A", BaseURL: upstream.URL, Models: chatModel()}},
		[]config.GatewayKey{
			{ID: "good", Name: "Good", Key: goodKey, UpstreamID: "a"},
			{ID: "disabled", Name: "Disabled", Key: badKey, UpstreamID: "a", Enabled: &disabled},
		},
	)

	cases := []struct {
		name   string
		header string
	}{
		{"missing", ""},
		{"unknown", "Bearer sk-local-unknown-00000000000000000000"},
		{"disabled", "Bearer " + badKey},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("POST", "/v1/chat/completions",
				strings.NewReader(`{"model":"deepseek-chat"}`))
			if tc.header != "" {
				req.Header.Set("Authorization", tc.header)
			}
			rec := httptest.NewRecorder()
			g.ServeHTTP(rec, req)
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
			}
			body := decodeGatewayError(t, rec)
			if body.Error.Code != "invalid_api_key" || body.Error.Type != "invalid_request_error" {
				t.Fatalf("error = %+v", body.Error)
			}
		})
	}
}

func TestGatewayDisabledUpstreamIs503(t *testing.T) {
	upstream := upstreamWithBody(t, `{"ok":true}`, nil)
	disabled := false
	g, _, _, _ := newTestGatewayWith(t,
		[]config.Upstream{{
			ID: "a", Name: "Upstream A", BaseURL: upstream.URL,
			Enabled: &disabled, Models: chatModel(),
		}},
		[]config.GatewayKey{{ID: "key", Name: "Key", Key: testGatewayKey, UpstreamID: "a"}},
	)

	req := httptest.NewRequest("POST", "/v1/chat/completions",
		strings.NewReader(`{"model":"deepseek-chat"}`))
	req.Header.Set("Authorization", "Bearer "+testGatewayKey)
	rec := httptest.NewRecorder()
	g.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	body := decodeGatewayError(t, rec)
	if body.Error.Code != "upstream_disabled" {
		t.Fatalf("error = %+v", body.Error)
	}
}

func TestGatewayNoKeysIs401InvalidAPIKey(t *testing.T) {
	upstream := upstreamWithBody(t, `{"ok":true}`, nil)
	g, _, _, _ := newTestGatewayWith(t,
		[]config.Upstream{{ID: "a", Name: "Upstream A", BaseURL: upstream.URL, Models: chatModel()}},
		nil,
	)
	for _, header := range []string{"", "Bearer sk-local-anything-000000000000000000"} {
		req := httptest.NewRequest("POST", "/v1/chat/completions",
			strings.NewReader(`{"model":"deepseek-chat"}`))
		if header != "" {
			req.Header.Set("Authorization", header)
		}
		rec := httptest.NewRecorder()
		g.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("header %q: status %d body %s", header, rec.Code, rec.Body.String())
		}
		body := decodeGatewayError(t, rec)
		if body.Error.Code != "invalid_api_key" {
			t.Fatalf("header %q: error = %+v", header, body.Error)
		}
	}
}

func TestGatewayNoUpstreamsIs503NotConfigured(t *testing.T) {
	g, _, _, _ := newTestGatewayWith(t, nil, nil)
	req := httptest.NewRequest("GET", "/v1/models", nil)
	rec := httptest.NewRecorder()
	g.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	body := decodeGatewayError(t, rec)
	if body.Error.Code != "not_configured" || body.Error.Message != "no upstream configured" || body.Error.Type == "" {
		t.Fatalf("error = %+v", body.Error)
	}
}

func TestGatewayEmptyUpstreamKeyRemovesAuthorization(t *testing.T) {
	gotAuth := make(chan string, 1)
	upstream := upstreamWithBody(t, `{"ok":true}`, gotAuth)
	g, st, _, _ := newTestGatewayWith(t,
		[]config.Upstream{{ID: "local", Name: "Ollama", BaseURL: upstream.URL, Models: chatModel()}},
		[]config.GatewayKey{{ID: "key", Name: "Key", Key: testGatewayKey, UpstreamID: "local"}},
	)

	req := httptest.NewRequest("POST", "/v1/chat/completions",
		strings.NewReader(`{"model":"deepseek-chat"}`))
	req.Header.Set("Authorization", "Bearer "+testGatewayKey)
	g.ServeHTTP(httptest.NewRecorder(), req)
	if got := <-gotAuth; got != "" {
		t.Fatalf("upstream received Authorization %q", got)
	}
	st.Flush()
	rows, _ := st.Requests(store.RequestFilter{From: "2026-10-29", To: "2026-10-29", Limit: 10})
	if len(rows) != 1 || rows[0].UpstreamID != "local" {
		t.Fatalf("rows = %+v", rows)
	}
}

func TestGatewayPricesWithBoundUpstreamTable(t *testing.T) {
	const usageBody = `{"model":"deepseek-chat","usage":{"prompt_tokens":1000000,"completion_tokens":0}}`
	a := upstreamWithBody(t, usageBody, nil)
	b := upstreamWithBody(t, usageBody, nil)
	const (
		keyA = "sk-local-price-a-00000000000000000000000"
		keyB = "sk-local-price-b-00000000000000000000000"
	)
	g, st, _, _ := newTestGatewayWith(t,
		[]config.Upstream{
			{ID: "a", Name: "Cheap", BaseURL: a.URL, Models: []config.Model{{Name: "deepseek-chat", Input: 1}}},
			{ID: "b", Name: "Pricey", BaseURL: b.URL, Models: []config.Model{{Name: "deepseek-chat", Input: 3}}},
		},
		[]config.GatewayKey{
			{ID: "ka", Name: "Key A", Key: keyA, UpstreamID: "a"},
			{ID: "kb", Name: "Key B", Key: keyB, UpstreamID: "b"},
		},
	)

	for _, key := range []string{keyA, keyB} {
		req := httptest.NewRequest("POST", "/v1/chat/completions",
			strings.NewReader(`{"model":"deepseek-chat"}`))
		req.Header.Set("Authorization", "Bearer "+key)
		rec := httptest.NewRecorder()
		g.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
		}
	}

	if err := st.Flush(); err != nil {
		t.Fatal(err)
	}
	rows, err := st.Requests(store.RequestFilter{From: "2026-10-29", To: "2026-10-29", Limit: 10})
	if err != nil || len(rows) != 2 {
		t.Fatalf("rows=%v err=%v", rows, err)
	}
	cost := make(map[string]int64, len(rows))
	rates := make(map[string]string, len(rows))
	for _, r := range rows {
		if !r.Priced {
			t.Fatalf("record not priced: %+v", r)
		}
		cost[r.UpstreamID] = r.CostNanoUSD
		rates[r.UpstreamID] = r.RatesJSON
	}
	if cost["a"] != 1_000_000_000 || cost["b"] != 3_000_000_000 {
		t.Fatalf("costs = %v", cost)
	}
	if rates["a"] == rates["b"] || rates["a"] == "" {
		t.Fatalf("rates did not follow the bound upstream: %v", rates)
	}
	for _, id := range []string{"a", "b"} {
		var snap struct {
			UpstreamID string `json:"upstream_id"`
		}
		if err := json.Unmarshal([]byte(rates[id]), &snap); err != nil {
			t.Fatal(err)
		}
		if snap.UpstreamID != id {
			t.Fatalf("rates json for %s = %s", id, rates[id])
		}
	}
}

func TestGatewayFallsBackToUpstreamModel(t *testing.T) {
	const upstreamBody = `{"model":"deepseek-chat","usage":{"prompt_tokens":1000000,"completion_tokens":0}}`
	upstream := upstreamWithBody(t, upstreamBody, nil)
	g, st, _, hub := newTestGatewayWith(t,
		[]config.Upstream{{ID: "deepseek", Name: "DeepSeek", BaseURL: upstream.URL, Models: chatModel()}},
		[]config.GatewayKey{{ID: "key", Name: "Key", Key: testGatewayKey, UpstreamID: "deepseek"}},
	)

	// The request body carries no model; the upstream-reported model must fill
	// the record so logs, aggregates and live byModel are not empty.
	req := httptest.NewRequest("POST", "/v1/chat/completions",
		strings.NewReader(`{"messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Authorization", "Bearer "+testGatewayKey)
	rec := httptest.NewRecorder()
	g.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	if err := st.Flush(); err != nil {
		t.Fatal(err)
	}
	rows, err := st.Requests(store.RequestFilter{From: "2026-10-29", To: "2026-10-29", Limit: 10})
	if err != nil || len(rows) != 1 {
		t.Fatalf("rows=%v err=%v", rows, err)
	}
	if rows[0].Model != "deepseek-chat" || rows[0].UpstreamModel != "deepseek-chat" {
		t.Fatalf("record models = %+v", rows[0])
	}
	if !rows[0].Priced || rows[0].CostNanoUSD == 0 {
		t.Fatalf("fallback model was not priced: %+v", rows[0])
	}

	snap := hub.Snapshot(time.Minute)
	var found bool
	for _, m := range snap.ByModel {
		if m.Model == "deepseek-chat" {
			found = true
		}
	}
	if !found {
		t.Fatalf("live byModel missing fallback model: %+v", snap.ByModel)
	}
}
