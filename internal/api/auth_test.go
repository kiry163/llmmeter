package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kiry163/llmmeter/internal/api"
	"github.com/kiry163/llmmeter/internal/settings"
	"github.com/kiry163/llmmeter/internal/store"
)

func newTestRepo(t *testing.T) *settings.Repo {
	t.Helper()
	return newTestRepoWithOpts(t, "")
}

func newTestRepoWithPassword(t *testing.T, pw string) *settings.Repo {
	t.Helper()
	return newTestRepoWithOpts(t, pw)
}

func newTestRepoWithOpts(t *testing.T, pw string) *settings.Repo {
	t.Helper()
	db, err := store.OpenDB(filepath.Join(t.TempDir(), "s.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	repo, err := settings.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Setup(context.Background(), settings.SetupInput{
		Upstream: settings.Upstream{
			BaseURL: "https://api.example.com",
			APIKey:  "sk-up",
			Timeout: 30 * time.Second,
		},
		Timezone:      "UTC",
		AdminPassword: pw,
	}); err != nil {
		t.Fatal(err)
	}
	return repo
}

func jsonReq(body string) *http.Request {
	req := httptest.NewRequest("POST", "/api/login", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	return req
}

func TestLoopbackBypassOnlyWithoutPassword(t *testing.T) {
	repo := newTestRepo(t) // setup 无密码
	a := api.NewAuth(repo, time.Now)
	req := httptest.NewRequest("GET", "/api/overview", nil)
	req.RemoteAddr = "127.0.0.1:5000"
	if !a.Authenticated(req) {
		t.Fatal("loopback without password must pass")
	}

	repo2 := newTestRepoWithPassword(t, "hunter22")
	a2 := api.NewAuth(repo2, time.Now)
	if a2.Authenticated(req) {
		t.Fatal("password set must require session")
	}
	rec := httptest.NewRecorder()
	a2.Login(rec, jsonReq(`{"password":"hunter22"}`))
	cookie := rec.Result().Cookies()[0]
	req2 := httptest.NewRequest("GET", "/api/overview", nil)
	req2.AddCookie(cookie)
	if !a2.Authenticated(req2) {
		t.Fatal("valid cookie must pass")
	}
}

func TestPasswordChangeInvalidatesSessions(t *testing.T) {
	repo := newTestRepoWithPassword(t, "hunter22")
	a := api.NewAuth(repo, time.Now)
	rec := httptest.NewRecorder()
	a.Login(rec, jsonReq(`{"password":"hunter22"}`))
	cookie := rec.Result().Cookies()[0]
	if err := repo.SetPassword(context.Background(), "hunter22", "newpass99"); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("GET", "/api/overview", nil)
	req.AddCookie(cookie)
	if a.Authenticated(req) {
		t.Fatal("old session must be invalid after password change")
	}
}

func TestCSRFRequiresOriginOrHeader(t *testing.T) {
	called := false
	h := api.NewAuth(newTestRepo(t), time.Now).CSRF(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))
	req := httptest.NewRequest("POST", "/api/models", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if called || rec.Code != 403 {
		t.Fatalf("called=%v code=%d", called, rec.Code)
	}
	req2 := httptest.NewRequest("POST", "/api/models", nil)
	req2.Header.Set("X-Requested-With", "llmmeter")
	h.ServeHTTP(httptest.NewRecorder(), req2)
	if !called {
		t.Fatal("header must pass CSRF")
	}
}

func TestCSRFSafeMethodsAndOrigin(t *testing.T) {
	called := 0
	h := api.NewAuth(newTestRepo(t), time.Now).CSRF(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called++ }))

	for _, method := range []string{"GET", "HEAD", "OPTIONS"} {
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(method, "/api/overview", nil))
	}
	if called != 3 {
		t.Fatalf("safe methods must pass, called=%d", called)
	}

	// Same-origin POST passes; cross-origin and unparsable origins do not.
	req := httptest.NewRequest("POST", "/api/models", nil)
	req.Host = "llmmeter.local:8080"
	req.Header.Set("Origin", "http://llmmeter.local:8080")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 || called != 4 {
		t.Fatalf("same-origin must pass: code=%d called=%d", rec.Code, called)
	}

	for _, origin := range []string{"http://evil.example", "null", ":"} {
		req := httptest.NewRequest("POST", "/api/models", nil)
		req.Header.Set("Origin", origin)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != 403 {
			t.Fatalf("origin %q must be rejected, code=%d", origin, rec.Code)
		}
	}

	// Wrong header value is not accepted.
	req = httptest.NewRequest("POST", "/api/models", nil)
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 403 {
		t.Fatalf("foreign X-Requested-With must be rejected, code=%d", rec.Code)
	}
}

func TestRequireAndRequireLoopback(t *testing.T) {
	a := api.NewAuth(newTestRepoWithPassword(t, "hunter22"), time.Now)
	h := a.Require(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))

	req := httptest.NewRequest("GET", "/api/overview", nil)
	req.RemoteAddr = "127.0.0.1:5000"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 401 {
		t.Fatalf("password set must require session even on loopback, code=%d", rec.Code)
	}

	lh := a.RequireLoopback(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
	req = httptest.NewRequest("POST", "/api/setup", nil)
	req.RemoteAddr = "192.0.2.7:9999"
	rec = httptest.NewRecorder()
	lh.ServeHTTP(rec, req)
	if rec.Code != 403 {
		t.Fatalf("non-loopback must be rejected, code=%d", rec.Code)
	}
	req = httptest.NewRequest("POST", "/api/setup", nil)
	req.RemoteAddr = "[::1]:9999"
	rec = httptest.NewRecorder()
	lh.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("IPv6 loopback must pass, code=%d", rec.Code)
	}
}

func TestLoginLogoutAndRateLimit(t *testing.T) {
	a := api.NewAuth(newTestRepoWithPassword(t, "hunter22"), time.Now)

	// Wrong password.
	rec := httptest.NewRecorder()
	req := jsonReq(`{"password":"nope"}`)
	req.RemoteAddr = "203.0.113.9:1111"
	a.Login(rec, req)
	if rec.Code != 401 {
		t.Fatalf("wrong password must be 401, code=%d", rec.Code)
	}

	// Malformed body.
	rec = httptest.NewRecorder()
	req = jsonReq(`not json`)
	req.RemoteAddr = "203.0.113.9:1111"
	a.Login(rec, req)
	if rec.Code != 400 {
		t.Fatalf("malformed body must be 400, code=%d", rec.Code)
	}

	// Remaining attempts hit the 5/minute per-IP limit.
	var limited bool
	for range 10 {
		rec = httptest.NewRecorder()
		req = jsonReq(`{"password":"nope"}`)
		req.RemoteAddr = "203.0.113.9:1111"
		a.Login(rec, req)
		if rec.Code == http.StatusTooManyRequests {
			limited = true
			break
		}
	}
	if !limited {
		t.Fatal("login attempts must be rate limited")
	}

	// A different IP is unaffected.
	rec = httptest.NewRecorder()
	req = jsonReq(`{"password":"hunter22"}`)
	req.RemoteAddr = "203.0.113.10:1111"
	a.Login(rec, req)
	if rec.Code != 200 {
		t.Fatalf("valid login must be 200, code=%d", rec.Code)
	}
	var body struct {
		Authenticated bool `json:"authenticated"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || !body.Authenticated {
		t.Fatalf("body=%q err=%v", rec.Body.String(), err)
	}
	res := rec.Result()
	cookies := res.Cookies()
	if len(cookies) != 1 {
		t.Fatalf("expected one cookie, got %d", len(cookies))
	}
	c := cookies[0]
	if c.Name != "llmmeter_session" || !c.HttpOnly || c.SameSite != http.SameSiteStrictMode || c.Path != "/" || c.MaxAge != 43200 {
		t.Fatalf("unexpected session cookie: %+v", c)
	}

	// Logout clears the cookie.
	rec = httptest.NewRecorder()
	a.Logout(rec, httptest.NewRequest("POST", "/api/logout", nil))
	cleared := rec.Result().Cookies()
	if len(cleared) != 1 || cleared[0].Name != "llmmeter_session" || cleared[0].MaxAge != -1 {
		t.Fatalf("logout must clear cookie: %+v", cleared)
	}

	// The logout response must not echo secrets.
	if strings.Contains(rec.Body.String(), "llmmeter_session") {
		t.Fatalf("response leaks cookie: %q", rec.Body.String())
	}
}

func TestSessionExpiry(t *testing.T) {
	repo := newTestRepoWithPassword(t, "hunter22")
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	a := api.NewAuth(repo, clock)

	rec := httptest.NewRecorder()
	a.Login(rec, jsonReq(`{"password":"hunter22"}`))
	cookie := rec.Result().Cookies()[0]

	req := httptest.NewRequest("GET", "/api/overview", nil)
	req.AddCookie(cookie)
	if !a.Authenticated(req) {
		t.Fatal("fresh session must pass")
	}
	now = now.Add(12*time.Hour + time.Second)
	if a.Authenticated(req) {
		t.Fatal("expired session must fail")
	}
}

func TestIsLoopback(t *testing.T) {
	cases := []struct {
		addr string
		want bool
	}{
		{"127.0.0.1:8080", true},
		{"[::1]:8080", true},
		{"localhost:8080", true},
		{"127.0.0.1", true},
		{"192.168.1.5:8080", false},
		{"[::2]:8080", false},
		{"example.com:80", false},
	}
	for _, tc := range cases {
		req := httptest.NewRequest("GET", "/", nil)
		req.RemoteAddr = tc.addr
		if got := api.IsLoopback(req); got != tc.want {
			t.Errorf("IsLoopback(%q)=%v want %v", tc.addr, got, tc.want)
		}
	}
}

// assertAPIError decodes the shared ApiError envelope and checks its shape.
func assertAPIError(t *testing.T, rec *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if rec.Code != status {
		t.Fatalf("status=%d want %d", rec.Code, status)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("content-type=%q want application/json", ct)
	}
	var body struct {
		Error struct {
			Code    string            `json:"code"`
			Message string            `json:"message"`
			Fields  map[string]string `json:"fields"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode envelope: %v (body=%q)", err, rec.Body.String())
	}
	if body.Error.Code != code {
		t.Fatalf("error.code=%q want %q", body.Error.Code, code)
	}
	if body.Error.Message == "" {
		t.Fatal("error.message must be set")
	}
	if body.Error.Fields == nil {
		t.Fatal("error.fields must be an object, not null")
	}
}

func TestAPIErrorEnvelope(t *testing.T) {
	a := api.NewAuth(newTestRepoWithPassword(t, "hunter22"), time.Now)

	// 401 from Require.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/overview", nil)
	req.RemoteAddr = "127.0.0.1:5000"
	a.Require(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })).ServeHTTP(rec, req)
	assertAPIError(t, rec, http.StatusUnauthorized, "unauthorized")

	// 403 from RequireLoopback.
	rec = httptest.NewRecorder()
	req = httptest.NewRequest("POST", "/api/setup", nil)
	req.RemoteAddr = "192.0.2.7:9999"
	a.RequireLoopback(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })).ServeHTTP(rec, req)
	assertAPIError(t, rec, http.StatusForbidden, "forbidden")

	// 403 from CSRF.
	rec = httptest.NewRecorder()
	req = httptest.NewRequest("POST", "/api/models", nil)
	req.Header.Set("Origin", "http://evil.example")
	a.CSRF(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })).ServeHTTP(rec, req)
	assertAPIError(t, rec, http.StatusForbidden, "forbidden")

	// Login failures.
	rec = httptest.NewRecorder()
	req = jsonReq(`{"password":"nope"}`)
	req.RemoteAddr = "198.51.100.4:1111"
	a.Login(rec, req)
	assertAPIError(t, rec, http.StatusUnauthorized, "unauthorized")

	rec = httptest.NewRecorder()
	req = jsonReq(`not json`)
	req.RemoteAddr = "198.51.100.4:1111"
	a.Login(rec, req)
	assertAPIError(t, rec, http.StatusBadRequest, "validation")
}
