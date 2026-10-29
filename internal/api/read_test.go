package api_test

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kiry163/llmmeter/internal/api"
	"github.com/kiry163/llmmeter/internal/metrics"
	"github.com/kiry163/llmmeter/internal/settings"
	"github.com/kiry163/llmmeter/internal/store"
	"github.com/kiry163/llmmeter/internal/usage"
)

// apiNow is the fixed clock the API tests run at so date handling is
// deterministic.
var apiNow = time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)

// newAPIServer starts the full handler against a temporary database. When
// seeded it has completed setup (upstream key sk-secret-1234, no admin
// password so loopback requests stay authenticated) and holds the same two
// 2026-10-29 requests as store.seedStore.
func newAPIServer(t *testing.T, seeded bool) *httptest.Server {
	t.Helper()
	return newAPIServerWithListen(t, seeded, "127.0.0.1:8100")
}

// newAPIServerWithListen is newAPIServer with an explicit listen address,
// which the password-clear rule inspects.
func newAPIServerWithListen(t *testing.T, seeded bool, listen string) *httptest.Server {
	t.Helper()
	ts, _ := newAPIServerStore(t, seeded, listen)
	return ts
}

// newAPIServerStore also returns the store so tests can inspect or break the
// database underneath the handler.
func newAPIServerStore(t *testing.T, seeded bool, listen string) (*httptest.Server, *store.Store) {
	t.Helper()

	st, err := store.Open(filepath.Join(t.TempDir(), "api.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	repo, err := settings.Open(st.DB())
	if err != nil {
		t.Fatal(err)
	}
	if seeded {
		if _, err := repo.Setup(context.Background(), settings.SetupInput{
			Upstream: settings.Upstream{
				BaseURL: "https://api.example.com",
				APIKey:  "sk-secret-1234",
				Timeout: 30 * time.Second,
			},
			Timezone: "UTC",
		}); err != nil {
			t.Fatal(err)
		}
		seedAPIData(t, st)
	}

	now := func() time.Time { return apiNow }
	hub := metrics.New(st, now)
	ctx, cancel := context.WithCancel(context.Background())
	go hub.Run(ctx)
	t.Cleanup(func() {
		cancel()
		hub.Stop()
	})

	srv := api.New(api.Config{
		Version: "test",
		Listen:  listen,
		DBPath:  "/tmp/llmmeter-test.db",
		Repo:    repo,
		Store:   st,
		Hub:     hub,
		Now:     now,
	})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts, st
}

// newAPIServerWithSeed is the alias used by the configuration API tests.
func newAPIServerWithSeed(t *testing.T, seeded bool) *httptest.Server {
	t.Helper()
	return newAPIServer(t, seeded)
}

// seedAPIData mirrors store.seedStore: one priced deepseek-chat call and one
// unpriced unknown-model call on 2026-10-29.
func seedAPIData(t *testing.T, st *store.Store) {
	t.Helper()
	base := time.Date(2026, 10, 29, 4, 0, 0, 0, time.UTC)
	st.EnqueueRecord(store.Record{
		StartedAt: base, CompletedAt: base.Add(900 * time.Millisecond),
		LocalDay: "2026-10-29", MinuteOfDay: 720,
		Path: "/v1/chat/completions", PathClass: "chat",
		Model: "deepseek-chat", Status: 200, LatencyMS: 120,
		ReqBodyBytes: 10, RespBodyBytes: 20,
		Usage:       usage.Usage{PromptTokens: 100, CompletionTokens: 40, CacheReadTokens: 10, TotalTokens: 140},
		WindowLabel: "base", CostNanoUSD: 1000, Priced: true,
		UsageStatus: usage.StatusOK,
	})
	st.EnqueueMinute(store.MinuteDelta{
		LocalDay: "2026-10-29", MinuteOfDay: 720, PathClass: "chat", Model: "deepseek-chat",
		Requests: 1, ReqBytes: 10, RespBytes: 20,
		PromptTokens: 100, CompletionTokens: 40, CacheReadTokens: 10,
		CostNanoUSD: 1000, LatencySumMS: 120, LatencyMaxMS: 120,
	})

	st.EnqueueRecord(store.Record{
		StartedAt: base.Add(30 * time.Second), CompletedAt: base.Add(31 * time.Second),
		LocalDay: "2026-10-29", MinuteOfDay: 720,
		Path: "/v1/chat/completions", PathClass: "chat",
		Model: "unknown", Status: 200, LatencyMS: 220,
		ReqBodyBytes: 20, RespBodyBytes: 40,
		Usage:       usage.Usage{PromptTokens: 20, TotalTokens: 20},
		WindowLabel: "base", Priced: false,
		UsageStatus: usage.StatusOK,
	})
	st.EnqueueMinute(store.MinuteDelta{
		LocalDay: "2026-10-29", MinuteOfDay: 720, PathClass: "chat", Model: "unknown",
		Requests: 1, ReqBytes: 20, RespBytes: 40,
		PromptTokens: 20, LatencySumMS: 220, LatencyMaxMS: 220,
	})
	if err := st.Flush(); err != nil {
		t.Fatal(err)
	}
}

// do issues a request against the test server carrying the CSRF header the
// real frontend sends, and buffers the response into a recorder.
func do(t *testing.T, srv *httptest.Server, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	return doAs(t, srv.Client(), srv, method, path, body)
}

// doAs is do with a caller-supplied client, so tests can keep a session cookie
// across requests.
func doAs(t *testing.T, client *http.Client, srv *httptest.Server, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	var rdr io.Reader
	if body != "" {
		rdr = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, srv.URL+path, rdr)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Requested-With", "llmmeter")
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	rec.Code = resp.StatusCode
	for k, v := range resp.Header {
		rec.Header()[k] = v
	}
	rec.Body.Write(data)
	return rec
}

func doGET(t *testing.T, srv *httptest.Server, path string) *httptest.ResponseRecorder {
	t.Helper()
	return do(t, srv, http.MethodGet, path, "")
}

func doPOST(t *testing.T, srv *httptest.Server, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	return do(t, srv, http.MethodPost, path, body)
}

func doPATCH(t *testing.T, srv *httptest.Server, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	return do(t, srv, http.MethodPatch, path, body)
}

// doPut keeps the spelling used by the configuration API tests.
func doPut(t *testing.T, srv *httptest.Server, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	return do(t, srv, http.MethodPut, path, body)
}

func doDELETE(t *testing.T, srv *httptest.Server, path string) *httptest.ResponseRecorder {
	t.Helper()
	return do(t, srv, http.MethodDelete, path, "")
}

func TestBootstrapAndSetupState(t *testing.T) {
	srv := newAPIServer(t, false) // 未 setup
	rec := doGET(t, srv, "/api/bootstrap")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"needsSetup":true`) {
		t.Fatalf("bootstrap = %d %s", rec.Code, rec.Body.String())
	}
}

func TestOverviewAndRequests(t *testing.T) {
	srv := newAPIServer(t, true) // 已 setup + seed 数据
	rec := doGET(t, srv, "/api/overview?from=2026-10-29&to=2026-10-29")
	if rec.Code != 200 {
		t.Fatalf("overview %d %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Current struct{ Requests, Unpriced int64 } `json:"current"`
	}
	json.Unmarshal(rec.Body.Bytes(), &body)
	if body.Current.Requests != 2 || body.Current.Unpriced != 1 {
		t.Fatalf("body = %+v", body)
	}

	rec = doGET(t, srv, "/api/requests?from=2026-10-29&to=2026-10-29&limit=1")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"requests":[`) {
		t.Fatalf("requests %d %s", rec.Code, rec.Body.String())
	}
}

func TestSeriesIntervalGuard(t *testing.T) {
	srv := newAPIServer(t, true)
	rec := doGET(t, srv, "/api/series?from=2026-01-01&to=2026-10-29&interval=minute")
	if rec.Code != 400 {
		t.Fatalf("code = %d", rec.Code)
	}
}

func TestBootstrapShape(t *testing.T) {
	srv := newAPIServer(t, true)
	rec := doGET(t, srv, "/api/bootstrap")
	if rec.Code != 200 {
		t.Fatalf("bootstrap = %d %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Version      string `json:"version"`
		NeedsSetup   bool   `json:"needsSetup"`
		AuthRequired bool   `json:"authRequired"`
		Listen       string `json:"listen"`
		Timezone     string `json:"timezone"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Version != "test" || body.NeedsSetup || body.AuthRequired ||
		body.Listen != "127.0.0.1:8100" || body.Timezone != "UTC" {
		t.Fatalf("bootstrap = %+v", body)
	}
}

func TestMeAndHealth(t *testing.T) {
	srv := newAPIServer(t, true)

	rec := doGET(t, srv, "/api/me")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"authRequired":false`) ||
		!strings.Contains(rec.Body.String(), `"authenticated":true`) {
		t.Fatalf("me = %d %s", rec.Code, rec.Body.String())
	}

	rec = doGET(t, srv, "/api/health")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"dbWritable":true`) {
		t.Fatalf("health = %d %s", rec.Code, rec.Body.String())
	}
	var health struct {
		OK         bool   `json:"ok"`
		QueueDepth int64  `json:"queueDepth"`
		Version    string `json:"version"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &health); err != nil {
		t.Fatal(err)
	}
	if !health.OK || health.Version != "test" {
		t.Fatalf("health = %+v", health)
	}
}

func TestSeriesAndBreakdown(t *testing.T) {
	srv := newAPIServer(t, true)

	rec := doGET(t, srv, "/api/series?from=2026-10-29&to=2026-10-29&interval=day")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"points":[`) {
		t.Fatalf("series = %d %s", rec.Code, rec.Body.String())
	}
	var series struct {
		Points []struct {
			Day      string `json:"day"`
			Requests int64  `json:"requests"`
			Cost     int64  `json:"costNanoUsd"`
		} `json:"points"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &series); err != nil {
		t.Fatal(err)
	}
	if len(series.Points) != 1 || series.Points[0].Requests != 2 ||
		series.Points[0].Cost != 1000 {
		t.Fatalf("series = %+v", series.Points)
	}

	rec = doGET(t, srv, "/api/breakdown?from=2026-10-29&to=2026-10-29&by=model")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"rows":[`) {
		t.Fatalf("breakdown = %d %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"unpriced":1`) {
		t.Fatalf("breakdown unpriced = %s", rec.Body.String())
	}

	rec = doGET(t, srv, "/api/breakdown?from=2026-10-29&to=2026-10-29&by=nope")
	if rec.Code != 400 || !strings.Contains(rec.Body.String(), `"code":"validation"`) {
		t.Fatalf("breakdown by guard = %d %s", rec.Code, rec.Body.String())
	}
}

func TestRequestsDetailAndCursor(t *testing.T) {
	srv := newAPIServer(t, true)

	rec := doGET(t, srv, "/api/requests?from=2026-10-29&to=2026-10-29&limit=1")
	if rec.Code != 200 {
		t.Fatalf("requests = %d %s", rec.Code, rec.Body.String())
	}
	var page struct {
		Requests []struct {
			ID           int64  `json:"id"`
			PromptTokens int64  `json:"promptTokens"`
			TotalTokens  int64  `json:"totalTokens"`
			Rates        any    `json:"rates"`
			TTFTMS       *int64 `json:"ttftMs"`
		} `json:"requests"`
		NextBeforeID int64 `json:"nextBeforeId"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Requests) != 1 || page.NextBeforeID != page.Requests[0].ID {
		t.Fatalf("page = %+v", page)
	}

	rec = doGET(t, srv, "/api/requests/99999")
	if rec.Code != 404 || !strings.Contains(rec.Body.String(), `"code":"not_found"`) {
		t.Fatalf("missing request = %d %s", rec.Code, rec.Body.String())
	}

	// The newest record is the unpriced unknown-model call.
	rec = doGET(t, srv, "/api/requests?from=2026-10-29&to=2026-10-29&priced=false")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"model":"unknown"`) ||
		strings.Contains(rec.Body.String(), `"model":"deepseek-chat"`) {
		t.Fatalf("priced filter = %d %s", rec.Code, rec.Body.String())
	}
}

func TestDateRangeValidation(t *testing.T) {
	srv := newAPIServer(t, true)
	for _, path := range []string{
		"/api/overview?from=2026-10-29&to=2026-10-01",
		"/api/overview?from=2026-13-01&to=2026-13-02",
		"/api/series?from=2026-10-29&to=2026-10-29&interval=week",
		"/api/requests?from=2026-10-29&to=2026-10-29&limit=201",
	} {
		rec := doGET(t, srv, path)
		if rec.Code != 400 || !strings.Contains(rec.Body.String(), `"code":"validation"`) {
			t.Fatalf("%s = %d %s", path, rec.Code, rec.Body.String())
		}
	}
}

func TestLiveSnapshotWindow(t *testing.T) {
	srv := newAPIServer(t, true)

	rec := doGET(t, srv, "/api/live/snapshot")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"windowS":60`) {
		t.Fatalf("snapshot = %d %s", rec.Code, rec.Body.String())
	}
	rec = doGET(t, srv, "/api/live/snapshot?window=300")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"windowS":300`) {
		t.Fatalf("snapshot 300 = %d %s", rec.Code, rec.Body.String())
	}
	rec = doGET(t, srv, "/api/live/snapshot?window=120")
	if rec.Code != 400 {
		t.Fatalf("snapshot 120 = %d %s", rec.Code, rec.Body.String())
	}
}

func TestLiveStreamFrames(t *testing.T) {
	srv := newAPIServer(t, true)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/api/live/stream?window=300", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("stream = %d %q", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	if resp.Header.Get("Cache-Control") != "no-cache" || resp.Header.Get("X-Accel-Buffering") != "no" {
		t.Fatalf("stream headers = %v", resp.Header)
	}

	br := bufio.NewReader(resp.Body)
	var sawEvent, sawData bool
	for !(sawEvent && sawData) {
		line, err := br.ReadString('\n')
		if err != nil {
			t.Fatalf("read frame: %v (event=%v data=%v)", err, sawEvent, sawData)
		}
		line = strings.TrimRight(line, "\r\n")
		switch {
		case line == "event: tick":
			sawEvent = true
		case strings.HasPrefix(line, "data: "):
			if !json.Valid([]byte(strings.TrimPrefix(line, "data: "))) {
				t.Fatalf("data frame is not JSON: %q", line)
			}
			sawData = true
		}
	}
}

func TestUnauthenticatedRequestsRejected(t *testing.T) {
	// A password-protected server must reject requests without a session.
	st, err := store.Open(filepath.Join(t.TempDir(), "auth.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	repo, err := settings.Open(st.DB())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Setup(context.Background(), settings.SetupInput{
		Upstream: settings.Upstream{BaseURL: "https://api.example.com", APIKey: "sk-up", Timeout: 30 * time.Second},
		Timezone: "UTC", AdminPassword: "hunter22",
	}); err != nil {
		t.Fatal(err)
	}
	hub := metrics.New(st, nil)
	srv := api.New(api.Config{Version: "test", Repo: repo, Store: st, Hub: hub})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	rec := doGET(t, ts, "/api/overview?from=2026-10-29&to=2026-10-29")
	if rec.Code != 401 || !strings.Contains(rec.Body.String(), `"code":"unauthorized"`) {
		t.Fatalf("unauthenticated overview = %d %s", rec.Code, rec.Body.String())
	}
	rec = doGET(t, ts, "/api/live/stream")
	if rec.Code != 401 {
		t.Fatalf("unauthenticated stream = %d", rec.Code)
	}
}

func TestHealthReportsUnwritableDatabase(t *testing.T) {
	srv, st := newAPIServerStore(t, true, "127.0.0.1:8100")
	if err := st.DB().Close(); err != nil {
		t.Fatal(err)
	}
	rec := doGET(t, srv, "/api/health")
	if rec.Code != http.StatusServiceUnavailable ||
		!strings.Contains(rec.Body.String(), `"dbWritable":false`) {
		t.Fatalf("health = %d %s", rec.Code, rec.Body.String())
	}
}

func TestHostGuardRejectsNonLoopbackHostWithoutPassword(t *testing.T) {
	srv := newAPIServer(t, true)
	doHost := func(server *httptest.Server, host string) *http.Response {
		t.Helper()
		req, err := http.NewRequest(http.MethodGet, server.URL+"/api/bootstrap", nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Host = host
		resp, err := server.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { resp.Body.Close() })
		return resp
	}

	if resp := doHost(srv, "evil.example"); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("rebound host = %d", resp.StatusCode)
	}
	if resp := doHost(srv, "127.0.0.1"); resp.StatusCode != http.StatusOK {
		t.Fatalf("loopback host = %d", resp.StatusCode)
	}

	// Once a password is configured the guard steps aside and session auth
	// applies instead; bootstrap stays reachable so the login page can load.
	secured := newAPIServer(t, true)
	if rec := doPATCH(t, secured, "/api/settings", `{"newPassword":"hunter22"}`); rec.Code != 200 {
		t.Fatalf("set password = %d %s", rec.Code, rec.Body.String())
	}
	if resp := doHost(secured, "evil.example"); resp.StatusCode != http.StatusOK {
		t.Fatalf("secured rebound host = %d", resp.StatusCode)
	}
}
