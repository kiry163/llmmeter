package api_test

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kiry163/llmmeter/internal/api"
	"github.com/kiry163/llmmeter/internal/config"
	"github.com/kiry163/llmmeter/internal/secure"
)

// newTestStore returns a configuration store backed by a temporary config.yaml.
// A non-empty pw installs an Argon2id admin password hash; mutate may adjust
// the configuration before the file is written.
func newTestStore(t *testing.T, pw string, mutate func(*config.Config)) *config.Store {
	t.Helper()
	st, err := config.OpenStore(filepath.Join(t.TempDir(), "config.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	hash := ""
	if pw != "" {
		hash, err = secure.HashPassword(pw)
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := st.Update(func(c *config.Config) error {
		c.Auth.AdminPasswordHash = hash
		c.Auth.SessionSecret = testSecret(t)
		if mutate != nil {
			mutate(c)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return st
}

func newTestStoreWithPassword(t *testing.T, pw string) *config.Store {
	t.Helper()
	return newTestStore(t, pw, nil)
}

// testSecret returns a freshly generated base64 session secret.
func testSecret(t *testing.T) string {
	t.Helper()
	raw, err := secure.RandomSecret()
	if err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(raw)
}

func jsonReq(body string) *http.Request {
	req := httptest.NewRequest("POST", "/api/login", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	return req
}

// sessionCookie returns the single cookie a successful login sets.
func sessionCookie(t *testing.T, rec *httptest.ResponseRecorder) *http.Cookie {
	t.Helper()
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("expected one cookie, got %d", len(cookies))
	}
	return cookies[0]
}

func TestLoopbackBypassOnlyWithoutPassword(t *testing.T) {
	a := api.NewAuth(newTestStore(t, "", nil), time.Now) // no password
	req := httptest.NewRequest("GET", "/api/overview", nil)
	req.RemoteAddr = "127.0.0.1:5000"
	if !a.Authenticated(req) {
		t.Fatal("loopback without password must pass")
	}

	a2 := api.NewAuth(newTestStoreWithPassword(t, "hunter22"), time.Now)
	if a2.Authenticated(req) {
		t.Fatal("password set must require session")
	}
	rec := httptest.NewRecorder()
	a2.Login(rec, jsonReq(`{"password":"hunter22"}`))
	cookie := sessionCookie(t, rec)
	req2 := httptest.NewRequest("GET", "/api/overview", nil)
	req2.AddCookie(cookie)
	if !a2.Authenticated(req2) {
		t.Fatal("valid cookie must pass")
	}
}

func TestAuthUsesConfigSessionSecret(t *testing.T) {
	st := newTestStoreWithPassword(t, "hunter22")
	a := api.NewAuth(st, time.Now)

	rec := httptest.NewRecorder()
	a.Login(rec, jsonReq(`{"password":"hunter22"}`))
	cookie := sessionCookie(t, rec)

	req := httptest.NewRequest("GET", "/api/overview", nil)
	req.AddCookie(cookie)
	if !a.Authenticated(req) {
		t.Fatal("cookie signed with the configured secret must pass")
	}

	// Rotating only the secret invalidates sessions signed with the old one.
	if err := st.Update(func(c *config.Config) error {
		c.Auth.SessionSecret = testSecret(t)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if a.Authenticated(req) {
		t.Fatal("old cookie must fail after the secret changes")
	}

	// A fresh login is signed with the new secret.
	rec = httptest.NewRecorder()
	a.Login(rec, jsonReq(`{"password":"hunter22"}`))
	fresh := httptest.NewRequest("GET", "/api/overview", nil)
	fresh.AddCookie(sessionCookie(t, rec))
	if !a.Authenticated(fresh) {
		t.Fatal("new cookie must verify against the new secret")
	}
}

func TestAuthLoopbackStillBypassesPassword(t *testing.T) {
	a := api.NewAuth(newTestStore(t, "", nil), time.Now)
	h := a.Require(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))

	req := httptest.NewRequest("GET", "/api/overview", nil)
	req.RemoteAddr = "127.0.0.1:5000"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("loopback without a cookie must pass, code=%d", rec.Code)
	}
}

func TestAuthEmptySessionSecretRefusesSession(t *testing.T) {
	st := newTestStoreWithPassword(t, "hunter22")
	if err := st.Update(func(c *config.Config) error {
		c.Auth.SessionSecret = ""
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	a := api.NewAuth(st, time.Now)

	rec := httptest.NewRecorder()
	a.Login(rec, jsonReq(`{"password":"hunter22"}`))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("missing secret must return 500, code=%d", rec.Code)
	}
	if len(rec.Result().Cookies()) != 0 {
		t.Fatal("missing secret must not issue a cookie")
	}
}

func TestPasswordChangeInvalidatesSessions(t *testing.T) {
	st := newTestStoreWithPassword(t, "hunter22")
	a := api.NewAuth(st, time.Now)
	rec := httptest.NewRecorder()
	a.Login(rec, jsonReq(`{"password":"hunter22"}`))
	cookie := sessionCookie(t, rec)

	hash, err := secure.HashPassword("newpass99")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Update(func(c *config.Config) error {
		c.Auth.AdminPasswordHash = hash
		c.Auth.SessionSecret = testSecret(t)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("GET", "/api/overview", nil)
	req.AddCookie(cookie)
	if a.Authenticated(req) {
		t.Fatal("old session must be invalid after password change")
	}
}

func TestAuthPasswordSetChangeClear(t *testing.T) {
	st := newTestStore(t, "", nil)
	a := api.NewAuth(st, time.Now)

	// Initial set from a loopback request needs no current password.
	rec := httptest.NewRecorder()
	a.Password(rec, jsonReq(`{"current":"","newPassword":"hunter22"}`))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"adminPasswordSet":true`) {
		t.Fatalf("initial set = %d %s", rec.Code, rec.Body.String())
	}
	first := sessionCookie(t, rec)
	req := httptest.NewRequest("GET", "/api/overview", nil)
	req.AddCookie(first)
	if !a.Authenticated(req) {
		t.Fatal("the request that set the password must stay signed in")
	}

	// The new password authenticates; a wrong one does not.
	rec = httptest.NewRecorder()
	a.Login(rec, jsonReq(`{"password":"hunter22"}`))
	if rec.Code != http.StatusOK {
		t.Fatalf("login with the new password = %d %s", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	a.Login(rec, jsonReq(`{"password":"hunter23"}`))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("login with a wrong password = %d", rec.Code)
	}

	// A wrong current password and a too-short new one are rejected.
	rec = httptest.NewRecorder()
	a.Password(rec, jsonReq(`{"current":"wrong","newPassword":"newpass99"}`))
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), `"current"`) {
		t.Fatalf("wrong current = %d %s", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	a.Password(rec, jsonReq(`{"current":"hunter22","newPassword":"short"}`))
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), `"newPassword"`) {
		t.Fatalf("short new password = %d %s", rec.Code, rec.Body.String())
	}

	// A valid change rotates the secret: the old cookie stops verifying and
	// the reissued one works.
	rec = httptest.NewRecorder()
	a.Password(rec, jsonReq(`{"current":"hunter22","newPassword":"newpass99"}`))
	if rec.Code != http.StatusOK {
		t.Fatalf("change = %d %s", rec.Code, rec.Body.String())
	}
	if a.Authenticated(req) {
		t.Fatal("cookie issued before the change must be invalid")
	}
	changed := httptest.NewRequest("GET", "/api/overview", nil)
	changed.AddCookie(sessionCookie(t, rec))
	if !a.Authenticated(changed) {
		t.Fatal("reissued cookie must be valid")
	}

	// Clearing the password removes it, so loopback is trusted again.
	rec = httptest.NewRecorder()
	a.Password(rec, jsonReq(`{"current":"newpass99","newPassword":""}`))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"adminPasswordSet":false`) {
		t.Fatalf("clear = %d %s", rec.Code, rec.Body.String())
	}
	cleared := httptest.NewRequest("GET", "/api/overview", nil)
	cleared.RemoteAddr = "127.0.0.1:5000"
	if !a.Authenticated(cleared) {
		t.Fatal("loopback must pass after clearing the password")
	}
	rec = httptest.NewRecorder()
	a.Login(rec, jsonReq(`{"password":"newpass99"}`))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("cleared password still accepted: %d", rec.Code)
	}
}

func TestAuthPasswordClearRejectedOffLoopback(t *testing.T) {
	st := newTestStore(t, "hunter22", func(c *config.Config) {
		c.Server.Listen = "0.0.0.0:8100"
	})
	a := api.NewAuth(st, time.Now)

	rec := httptest.NewRecorder()
	a.Password(rec, jsonReq(`{"current":"hunter22","newPassword":""}`))
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), `"newPassword"`) {
		t.Fatalf("non-loopback clear = %d %s", rec.Code, rec.Body.String())
	}
	if st.Snapshot().Config.Auth.AdminPasswordHash == "" {
		t.Fatal("rejected clear must keep the password")
	}
}

func TestCSRFRequiresOriginOrHeader(t *testing.T) {
	called := false
	h := api.NewAuth(newTestStore(t, "", nil), time.Now).CSRF(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))
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
	h := api.NewAuth(newTestStore(t, "", nil), time.Now).CSRF(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called++ }))

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
	a := api.NewAuth(newTestStoreWithPassword(t, "hunter22"), time.Now)
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
	a := api.NewAuth(newTestStoreWithPassword(t, "hunter22"), time.Now)

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
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	a := api.NewAuth(newTestStoreWithPassword(t, "hunter22"), clock)

	rec := httptest.NewRecorder()
	a.Login(rec, jsonReq(`{"password":"hunter22"}`))
	cookie := sessionCookie(t, rec)

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
	a := api.NewAuth(newTestStoreWithPassword(t, "hunter22"), time.Now)

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
