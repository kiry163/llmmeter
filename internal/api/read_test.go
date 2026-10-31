package api_test

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/kiry163/llmmeter/internal/config"
	"github.com/kiry163/llmmeter/internal/secure"
	"github.com/kiry163/llmmeter/internal/store"
	"github.com/kiry163/llmmeter/internal/usage"
)

// newReadEnv starts a handler backed by a real config store with the two
// upstreams "a" (Alpha) and "b" (Beta), then seeds readData.
func newReadEnv(t *testing.T) *configTestEnv {
	t.Helper()
	env := newConfigEnv(t, "127.0.0.1:8100")
	if err := env.config.Update(func(c *config.Config) error {
		c.General.Timezone = "UTC"
		c.General.RetentionDays = 90
		c.Upstreams = []config.Upstream{
			{ID: "a", Name: "Alpha", BaseURL: "https://a.example.com", APIKey: "sk-a"},
			{ID: "b", Name: "Beta", BaseURL: "https://b.example.com", APIKey: "sk-b"},
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	seedReadData(t, env.data)
	return env
}

// seedReadData writes three 2026-10-29 requests across upstreams a and b: a
// priced shared-model call, a pricier shared-model call on b, and an unpriced
// failed "other" call on a. Both upstreams serve the same model in the same
// minute so upstream dimensions are observable.
func seedReadData(t *testing.T, st *store.Store) {
	t.Helper()
	base := time.Date(2026, 10, 29, 4, 0, 0, 0, time.UTC)
	add := func(r store.Record, d store.MinuteDelta) {
		st.EnqueueRecord(r)
		st.EnqueueMinute(d)
	}
	add(store.Record{
		StartedAt: base, CompletedAt: base.Add(120 * time.Millisecond),
		LocalDay: "2026-10-29", MinuteOfDay: 720,
		Path: "/v1/chat/completions", PathClass: "chat",
		UpstreamID: "a", UpstreamName: "Alpha", KeyID: "ka", KeyName: "Key A",
		Model: "shared", Status: 200, LatencyMS: 120,
		ReqBodyBytes: 10, RespBodyBytes: 20,
		Usage:       usage.Usage{PromptTokens: 100, CompletionTokens: 40, CacheReadTokens: 10, TotalTokens: 140},
		WindowLabel: "base", CostNanoUSD: 1000, Priced: true,
		UsageStatus: usage.StatusOK,
	}, store.MinuteDelta{
		LocalDay: "2026-10-29", MinuteOfDay: 720, PathClass: "chat",
		UpstreamID: "a", Model: "shared",
		Requests: 1, ReqBytes: 10, RespBytes: 20,
		PromptTokens: 100, CompletionTokens: 40, CacheReadTokens: 10,
		CostNanoUSD: 1000, LatencySumMS: 120, LatencyMaxMS: 120,
	})
	add(store.Record{
		StartedAt: base.Add(20 * time.Second), CompletedAt: base.Add(20*time.Second + 220*time.Millisecond),
		LocalDay: "2026-10-29", MinuteOfDay: 720,
		Path: "/v1/chat/completions", PathClass: "chat",
		UpstreamID: "b", UpstreamName: "Beta", KeyID: "kb", KeyName: "Key B",
		Model: "shared", Status: 200, LatencyMS: 220,
		ReqBodyBytes: 20, RespBodyBytes: 40,
		Usage:       usage.Usage{PromptTokens: 200, CompletionTokens: 80, CacheReadTokens: 20, TotalTokens: 280},
		WindowLabel: "base", CostNanoUSD: 2000, Priced: true,
		UsageStatus: usage.StatusOK,
	}, store.MinuteDelta{
		LocalDay: "2026-10-29", MinuteOfDay: 720, PathClass: "chat",
		UpstreamID: "b", Model: "shared",
		Requests: 1, ReqBytes: 20, RespBytes: 40,
		PromptTokens: 200, CompletionTokens: 80, CacheReadTokens: 20,
		CostNanoUSD: 2000, LatencySumMS: 220, LatencyMaxMS: 220,
	})
	add(store.Record{
		StartedAt: base.Add(60 * time.Second), CompletedAt: base.Add(60*time.Second + 250*time.Millisecond),
		LocalDay: "2026-10-29", MinuteOfDay: 721,
		Path: "/v1/chat/completions", PathClass: "chat",
		UpstreamID: "a", UpstreamName: "Alpha", KeyID: "ka", KeyName: "Key A",
		Model: "other", Status: 500, LatencyMS: 250,
		ReqBodyBytes: 30, RespBodyBytes: 60,
		Usage:       usage.Usage{PromptTokens: 20, TotalTokens: 20},
		WindowLabel: "base", Priced: false,
		UsageStatus: usage.StatusOK,
	}, store.MinuteDelta{
		LocalDay: "2026-10-29", MinuteOfDay: 721, PathClass: "chat",
		UpstreamID: "a", Model: "other",
		Requests: 1, Errors: 1, ReqBytes: 30, RespBytes: 60,
		PromptTokens: 20, LatencySumMS: 250, LatencyMaxMS: 250,
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

func TestOverviewAndRequests(t *testing.T) {
	env := newReadEnv(t)
	rec := doGET(t, env.srv, "/api/overview?from=2026-10-29&to=2026-10-29")
	if rec.Code != 200 {
		t.Fatalf("overview %d %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Current struct{ Requests, Unpriced int64 } `json:"current"`
	}
	json.Unmarshal(rec.Body.Bytes(), &body)
	if body.Current.Requests != 3 || body.Current.Unpriced != 1 {
		t.Fatalf("body = %+v", body)
	}

	rec = doGET(t, env.srv, "/api/requests?from=2026-10-29&to=2026-10-29&limit=1")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"requests":[`) {
		t.Fatalf("requests %d %s", rec.Code, rec.Body.String())
	}
}

func TestSeriesIntervalGuard(t *testing.T) {
	env := newReadEnv(t)
	rec := doGET(t, env.srv, "/api/series?from=2026-01-01&to=2026-10-29&interval=minute")
	if rec.Code != 400 {
		t.Fatalf("code = %d", rec.Code)
	}
}

func TestBootstrapShape(t *testing.T) {
	env := newReadEnv(t)
	rec := doGET(t, env.srv, "/api/bootstrap")
	if rec.Code != 200 {
		t.Fatalf("bootstrap = %d %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Version      string `json:"version"`
		AuthRequired bool   `json:"authRequired"`
		Listen       string `json:"listen"`
		Timezone     string `json:"timezone"`
		EarliestDay  string `json:"earliestDay"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Version != "test" || body.AuthRequired ||
		body.Listen != "127.0.0.1:8100" || body.Timezone != "UTC" ||
		body.EarliestDay != "2026-10-29" {
		t.Fatalf("bootstrap = %+v", body)
	}

	// Without traffic the earliest day is empty rather than omitted.
	empty := newConfigEnv(t, "127.0.0.1:8100")
	rec = doGET(t, empty.srv, "/api/bootstrap")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"earliestDay":""`) {
		t.Fatalf("empty bootstrap = %d %s", rec.Code, rec.Body.String())
	}
}

func TestMeAndHealth(t *testing.T) {
	env := newReadEnv(t)

	rec := doGET(t, env.srv, "/api/me")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"authRequired":false`) ||
		!strings.Contains(rec.Body.String(), `"authenticated":true`) {
		t.Fatalf("me = %d %s", rec.Code, rec.Body.String())
	}

	rec = doGET(t, env.srv, "/api/health")
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
	env := newReadEnv(t)
	srv := env.srv

	rec := doGET(t, srv, "/api/series?from=2026-10-29&to=2026-10-29&interval=day")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"points":[`) {
		t.Fatalf("series = %d %s", rec.Code, rec.Body.String())
	}
	var series struct {
		Points []struct {
			Day       string `json:"day"`
			Requests  int64  `json:"requests"`
			CacheRead int64  `json:"cacheReadTokens"`
			Cost      int64  `json:"costNanoUsd"`
		} `json:"points"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &series); err != nil {
		t.Fatal(err)
	}
	if len(series.Points) != 1 || series.Points[0].Requests != 3 ||
		series.Points[0].Cost != 3000 || series.Points[0].CacheRead != 30 {
		t.Fatalf("series = %+v", series.Points)
	}

	rec = doGET(t, srv, "/api/breakdown?from=2026-10-29&to=2026-10-29&by=model")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"rows":[`) {
		t.Fatalf("breakdown = %d %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"unpriced":1`) {
		t.Fatalf("breakdown unpriced = %s", rec.Body.String())
	}
	var breakdown struct {
		Rows []struct {
			UpstreamID   string `json:"upstreamId"`
			UpstreamName string `json:"upstreamName"`
			Key          string `json:"key"`
		} `json:"rows"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &breakdown); err != nil {
		t.Fatal(err)
	}
	// Same model under two upstreams stays two rows, ordered by cost.
	if len(breakdown.Rows) != 3 ||
		breakdown.Rows[0].UpstreamID != "b" || breakdown.Rows[0].UpstreamName != "Beta" ||
		breakdown.Rows[1].UpstreamID != "a" || breakdown.Rows[1].UpstreamName != "Alpha" ||
		breakdown.Rows[0].Key != "shared" || breakdown.Rows[1].Key != "shared" {
		t.Fatalf("breakdown rows = %+v", breakdown.Rows)
	}

	rec = doGET(t, srv, "/api/breakdown?from=2026-10-29&to=2026-10-29&by=nope")
	if rec.Code != 400 || !strings.Contains(rec.Body.String(), `"code":"validation"`) {
		t.Fatalf("breakdown by guard = %d %s", rec.Code, rec.Body.String())
	}
}

func TestOverviewFiltersByUpstream(t *testing.T) {
	env := newReadEnv(t)
	total := func(query string) int64 {
		t.Helper()
		rec := doGET(t, env.srv, "/api/overview?from=2026-10-29&to=2026-10-29"+query)
		if rec.Code != 200 {
			t.Fatalf("overview %q = %d %s", query, rec.Code, rec.Body.String())
		}
		var body struct {
			Current struct {
				Requests int64 `json:"requests"`
			} `json:"current"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		return body.Current.Requests
	}
	if got := total(""); got != 3 {
		t.Fatalf("all = %d", got)
	}
	if got := total("&upstream=a"); got != 2 {
		t.Fatalf("upstream a = %d", got)
	}
	if got := total("&upstream=b"); got != 1 {
		t.Fatalf("upstream b = %d", got)
	}
	if got := total("&upstream=deleted"); got != 0 {
		t.Fatalf("deleted upstream = %d", got)
	}
}

func TestSeriesAndBreakdownFilterByUpstream(t *testing.T) {
	env := newReadEnv(t)

	rec := doGET(t, env.srv, "/api/series?from=2026-10-29&to=2026-10-29&interval=day&upstream=a")
	if rec.Code != 200 {
		t.Fatalf("series = %d %s", rec.Code, rec.Body.String())
	}
	var series struct {
		Points []struct {
			Requests int64 `json:"requests"`
			Cost     int64 `json:"costNanoUsd"`
		} `json:"points"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &series); err != nil {
		t.Fatal(err)
	}
	if len(series.Points) != 1 || series.Points[0].Requests != 2 || series.Points[0].Cost != 1000 {
		t.Fatalf("filtered series = %+v", series.Points)
	}

	rec = doGET(t, env.srv, "/api/breakdown?from=2026-10-29&to=2026-10-29&by=model&upstream=a")
	if rec.Code != 200 {
		t.Fatalf("breakdown = %d %s", rec.Code, rec.Body.String())
	}
	var breakdown struct {
		Rows []struct {
			UpstreamID   string `json:"upstreamId"`
			UpstreamName string `json:"upstreamName"`
			Key          string `json:"key"`
		} `json:"rows"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &breakdown); err != nil {
		t.Fatal(err)
	}
	if len(breakdown.Rows) != 2 {
		t.Fatalf("filtered breakdown = %+v", breakdown.Rows)
	}
	for _, row := range breakdown.Rows {
		if row.UpstreamID != "a" || row.UpstreamName != "Alpha" {
			t.Fatalf("filtered breakdown row = %+v", row)
		}
	}
}

func TestRequestsKeyFilterRequiresUpstream(t *testing.T) {
	env := newReadEnv(t)

	// A key filter without an upstream is rejected instead of being treated
	// as a global key name.
	for _, q := range []string{"&key=ka", "&key=ka&upstream="} {
		rec := doGET(t, env.srv, "/api/requests?from=2026-10-29&to=2026-10-29"+q)
		if rec.Code != 400 || !strings.Contains(rec.Body.String(), `"code":"validation"`) ||
			!strings.Contains(rec.Body.String(), "key filter requires upstream") {
			t.Fatalf("requests %q = %d %s", q, rec.Code, rec.Body.String())
		}
	}
}

func TestRequestsUpstreamAndKeyFilters(t *testing.T) {
	env := newReadEnv(t)

	count := func(query string) int {
		t.Helper()
		rec := doGET(t, env.srv, "/api/requests?from=2026-10-29&to=2026-10-29"+query)
		if rec.Code != 200 {
			t.Fatalf("requests %q = %d %s", query, rec.Code, rec.Body.String())
		}
		var page struct {
			Requests []struct {
				UpstreamID   string `json:"upstreamId"`
				UpstreamName string `json:"upstreamName"`
				KeyID        string `json:"keyId"`
				KeyName      string `json:"keyName"`
			} `json:"requests"`
			Summary struct {
				Requests int64 `json:"requests"`
			} `json:"summary"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
			t.Fatal(err)
		}
		if len(page.Requests) == 0 {
			t.Fatalf("requests %q returned no rows", query)
		}
		for _, row := range page.Requests {
			if row.UpstreamID == "" || row.UpstreamName == "" || row.KeyID == "" || row.KeyName == "" {
				t.Fatalf("requests %q lost the upstream snapshot: %+v", query, row)
			}
		}
		if page.Summary.Requests != int64(len(page.Requests)) {
			t.Fatalf("requests %q summary = %+v", query, page.Summary)
		}
		return len(page.Requests)
	}
	if got := count(""); got != 3 {
		t.Fatalf("all = %d", got)
	}
	if got := count("&upstream=a"); got != 2 {
		t.Fatalf("upstream a = %d", got)
	}
	if got := count("&upstream=a&key=ka"); got != 2 {
		t.Fatalf("upstream a key ka = %d", got)
	}
	if got := count("&upstream=b&key=kb"); got != 1 {
		t.Fatalf("upstream b key kb = %d", got)
	}

	// A key that belongs to another upstream matches nothing.
	rec := doGET(t, env.srv, "/api/requests?from=2026-10-29&to=2026-10-29&upstream=b&key=ka")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"requests":[]`) {
		t.Fatalf("foreign key = %d %s", rec.Code, rec.Body.String())
	}
}

func TestRequestsDetailAndCursor(t *testing.T) {
	env := newReadEnv(t)

	rec := doGET(t, env.srv, "/api/requests?from=2026-10-29&to=2026-10-29&limit=1")
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

	rec = doGET(t, env.srv, "/api/requests/99999")
	if rec.Code != 404 || !strings.Contains(rec.Body.String(), `"code":"not_found"`) {
		t.Fatalf("missing request = %d %s", rec.Code, rec.Body.String())
	}

	// The newest record is the unpriced failed call.
	rec = doGET(t, env.srv, "/api/requests?from=2026-10-29&to=2026-10-29&priced=false")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"model":"other"`) ||
		strings.Contains(rec.Body.String(), `"model":"shared"`) {
		t.Fatalf("priced filter = %d %s", rec.Code, rec.Body.String())
	}

	// All seeded records are non-streamed, so the stream filter must exclude
	// everything for stream=true and keep all rows for stream=false.
	var streamPage struct {
		Requests []struct {
			ID     int64 `json:"id"`
			Stream bool  `json:"stream"`
		} `json:"requests"`
	}
	rec = doGET(t, env.srv, "/api/requests?from=2026-10-29&to=2026-10-29&stream=false")
	if rec.Code != 200 {
		t.Fatalf("stream=false = %d %s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &streamPage); err != nil {
		t.Fatal(err)
	}
	if len(streamPage.Requests) != 3 || streamPage.Requests[0].Stream {
		t.Fatalf("stream=false page = %+v", streamPage)
	}
	rec = doGET(t, env.srv, "/api/requests?from=2026-10-29&to=2026-10-29&stream=true")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"requests":[]`) {
		t.Fatalf("stream=true = %d %s", rec.Code, rec.Body.String())
	}
	rec = doGET(t, env.srv, "/api/requests?from=2026-10-29&to=2026-10-29&stream=maybe")
	if rec.Code != 400 || !strings.Contains(rec.Body.String(), `"code":"validation"`) {
		t.Fatalf("stream=maybe = %d %s", rec.Code, rec.Body.String())
	}
}

func TestRequestsStatusClassAndSummary(t *testing.T) {
	env := newReadEnv(t)

	type page struct {
		Requests []struct {
			Status int `json:"status"`
		} `json:"requests"`
		Summary struct {
			Requests    int64 `json:"requests"`
			CostNanoUSD int64 `json:"costNanoUsd"`
			TotalTokens int64 `json:"totalTokens"`
			Errors      int64 `json:"errors"`
		} `json:"summary"`
	}
	get := func(t *testing.T, query string) page {
		t.Helper()
		rec := doGET(t, env.srv, "/api/requests?from=2026-10-29&to=2026-10-29"+query)
		if rec.Code != 200 {
			t.Fatalf("requests %s = %d %s", query, rec.Code, rec.Body.String())
		}
		var p page
		if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
			t.Fatal(err)
		}
		return p
	}

	cases := []struct {
		name  string
		query string
		rows  int
	}{
		{"2xx", "&statusClass=2xx", 2},
		{"4xx", "&statusClass=4xx", 0},
		{"5xx", "&statusClass=5xx", 1},
		{"no filter", "", 3},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := get(t, tc.query)
			if len(p.Requests) != tc.rows {
				t.Fatalf("rows = %+v", p.Requests)
			}
			if p.Summary.Requests != int64(tc.rows) {
				t.Fatalf("summary = %+v", p.Summary)
			}
		})
	}

	for _, bad := range []string{"3xx", "abc", "200"} {
		rec := doGET(t, env.srv, "/api/requests?from=2026-10-29&to=2026-10-29&statusClass="+bad)
		if rec.Code != 400 || !strings.Contains(rec.Body.String(), `"code":"validation"`) {
			t.Fatalf("statusClass=%s = %d %s", bad, rec.Code, rec.Body.String())
		}
	}

	// The summary covers the whole filtered set even when the page is cut by
	// the cursor and limit.
	p := get(t, "&limit=1&before_id=999")
	if len(p.Requests) != 1 {
		t.Fatalf("paged rows = %+v", p.Requests)
	}
	if p.Summary.Requests != 3 || p.Summary.CostNanoUSD != 3000 ||
		p.Summary.TotalTokens != 440 || p.Summary.Errors != 1 {
		t.Fatalf("summary = %+v", p.Summary)
	}
	// Exact status and class compose.
	if p := get(t, "&statusClass=2xx&status=200"); len(p.Requests) != 2 {
		t.Fatalf("2xx + 200 rows = %+v", p.Requests)
	}
	if p := get(t, "&statusClass=4xx&status=200"); len(p.Requests) != 0 {
		t.Fatalf("4xx + 200 rows = %+v", p.Requests)
	}
}

func TestDeletedUpstreamFallsBackToStoredName(t *testing.T) {
	env := newReadEnv(t)
	if err := env.config.Update(func(c *config.Config) error {
		c.Upstreams = c.Upstreams[:1] // drop "b"
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	// Detail rows keep the name snapshotted when the request started.
	rec := doGET(t, env.srv, "/api/requests?from=2026-10-29&to=2026-10-29&upstream=b")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"upstreamName":"Beta"`) {
		t.Fatalf("requests = %d %s", rec.Code, rec.Body.String())
	}

	// The analysis top ranking has the same stored snapshot.
	rec = doGET(t, env.srv, "/api/analysis?from=2026-10-29&to=2026-10-29&upstream=b")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"upstreamName":"Beta"`) {
		t.Fatalf("analysis = %d %s", rec.Code, rec.Body.String())
	}

	// Aggregate rows have no stored name, so the ID keeps them identifiable
	// instead of reporting an empty string.
	rec = doGET(t, env.srv, "/api/breakdown?from=2026-10-29&to=2026-10-29&by=model&upstream=b")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"upstreamName":"b"`) {
		t.Fatalf("breakdown = %d %s", rec.Code, rec.Body.String())
	}
}

func TestDateRangeValidation(t *testing.T) {
	env := newReadEnv(t)
	for _, path := range []string{
		"/api/overview?from=2026-10-29&to=2026-10-01",
		"/api/overview?from=2026-13-01&to=2026-13-02",
		"/api/series?from=2026-10-29&to=2026-10-29&interval=week",
		"/api/requests?from=2026-10-29&to=2026-10-29&limit=201",
	} {
		rec := doGET(t, env.srv, path)
		if rec.Code != 400 || !strings.Contains(rec.Body.String(), `"code":"validation"`) {
			t.Fatalf("%s = %d %s", path, rec.Code, rec.Body.String())
		}
	}
}

func TestLiveSnapshotWindow(t *testing.T) {
	env := newReadEnv(t)

	rec := doGET(t, env.srv, "/api/live/snapshot")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"windowS":60`) {
		t.Fatalf("snapshot = %d %s", rec.Code, rec.Body.String())
	}
	rec = doGET(t, env.srv, "/api/live/snapshot?window=300")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"windowS":300`) {
		t.Fatalf("snapshot 300 = %d %s", rec.Code, rec.Body.String())
	}
	rec = doGET(t, env.srv, "/api/live/snapshot?window=120")
	if rec.Code != 400 {
		t.Fatalf("snapshot 120 = %d %s", rec.Code, rec.Body.String())
	}
}

func TestLiveStreamFrames(t *testing.T) {
	env := newReadEnv(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, env.srv.URL+"/api/live/stream?window=300", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := env.srv.Client().Do(req)
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
	env := newReadEnv(t)
	hash, err := secure.HashPassword("hunter22")
	if err != nil {
		t.Fatal(err)
	}
	if err := env.config.Update(func(c *config.Config) error {
		c.Auth.AdminPasswordHash = hash
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	rec := doGET(t, env.srv, "/api/overview?from=2026-10-29&to=2026-10-29")
	if rec.Code != 401 || !strings.Contains(rec.Body.String(), `"code":"unauthorized"`) {
		t.Fatalf("unauthenticated overview = %d %s", rec.Code, rec.Body.String())
	}
	rec = doGET(t, env.srv, "/api/live/stream")
	if rec.Code != 401 {
		t.Fatalf("unauthenticated stream = %d", rec.Code)
	}
}

func TestHealthReportsUnwritableDatabase(t *testing.T) {
	env := newReadEnv(t)
	if err := env.data.DB().Close(); err != nil {
		t.Fatal(err)
	}
	rec := doGET(t, env.srv, "/api/health")
	if rec.Code != http.StatusServiceUnavailable ||
		!strings.Contains(rec.Body.String(), `"dbWritable":false`) {
		t.Fatalf("health = %d %s", rec.Code, rec.Body.String())
	}
}

func TestHostGuardRejectsNonLoopbackHostWithoutPassword(t *testing.T) {
	env := newReadEnv(t)
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

	if resp := doHost(env.srv, "evil.example"); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("rebound host = %d", resp.StatusCode)
	}
	if resp := doHost(env.srv, "127.0.0.1"); resp.StatusCode != http.StatusOK {
		t.Fatalf("loopback host = %d", resp.StatusCode)
	}

	// Once a password is configured the guard steps aside and session auth
	// applies instead; bootstrap stays reachable so the login page can load.
	hash, err := secure.HashPassword("hunter22")
	if err != nil {
		t.Fatal(err)
	}
	if err := env.config.Update(func(c *config.Config) error {
		c.Auth.AdminPasswordHash = hash
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if resp := doHost(env.srv, "evil.example"); resp.StatusCode != http.StatusOK {
		t.Fatalf("secured rebound host = %d", resp.StatusCode)
	}
}

func TestSeriesAutoInterval(t *testing.T) {
	env := newReadEnv(t)
	rec := doGET(t, env.srv, "/api/series?from=2026-10-29&to=2026-10-29&interval=auto")
	if rec.Code != 200 {
		t.Fatalf("series auto = %d %s", rec.Code, rec.Body.String())
	}
	var series struct {
		Points []struct {
			MinuteOfDay int   `json:"minuteOfDay"`
			Requests    int64 `json:"requests"`
		} `json:"points"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &series); err != nil {
		t.Fatal(err)
	}
	// A one-day span resolves to minute buckets, matching /api/analysis auto.
	var total int64
	for _, p := range series.Points {
		total += p.Requests
		if p.MinuteOfDay == 0 {
			t.Fatalf("auto interval did not use minute buckets: %+v", p)
		}
	}
	if len(series.Points) != 2 || total != 3 {
		t.Fatalf("series auto points = %+v", series.Points)
	}
}

func TestRenameUpstreamPreservesHistory(t *testing.T) {
	env := newReadEnv(t)
	if err := env.config.Update(func(c *config.Config) error {
		for i := range c.Upstreams {
			if c.Upstreams[i].ID == "b" {
				c.Upstreams[i].Name = "Beta Renamed"
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	// Detail rows show the name snapshotted when the request started.
	rec := doGET(t, env.srv, "/api/requests?from=2026-10-29&to=2026-10-29&upstream=b")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"upstreamName":"Beta"`) {
		t.Fatalf("requests = %d %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "Beta Renamed") {
		t.Fatalf("request history was relabeled after rename: %s", rec.Body.String())
	}

	// The analysis top ranking keeps the same snapshot; its aggregate trend
	// and model rows have no stored name and follow the live configuration.
	rec = doGET(t, env.srv, "/api/analysis?from=2026-10-29&to=2026-10-29&upstream=b")
	if rec.Code != 200 {
		t.Fatalf("analysis = %d %s", rec.Code, rec.Body.String())
	}
	var analysis struct {
		TopRequests json.RawMessage `json:"topRequests"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &analysis); err != nil {
		t.Fatal(err)
	}
	if top := string(analysis.TopRequests); !strings.Contains(top, `"upstreamName":"Beta"`) ||
		strings.Contains(top, "Beta Renamed") {
		t.Fatalf("analysis topRequests = %s", top)
	}

	// Aggregate rows carry no stored name, so they follow the live config.
	rec = doGET(t, env.srv, "/api/breakdown?from=2026-10-29&to=2026-10-29&by=model&upstream=b")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"upstreamName":"Beta Renamed"`) {
		t.Fatalf("breakdown = %d %s", rec.Code, rec.Body.String())
	}
}
