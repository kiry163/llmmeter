package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kiry163/llmmeter/internal/api"
	"github.com/kiry163/llmmeter/internal/settings"
	"github.com/kiry163/llmmeter/internal/store"
)

func TestSetupOnceAndMasking(t *testing.T) {
	srv := newAPIServer(t, false)
	body := `{"upstream":{"baseUrl":"https://api.example.com","apiKey":"sk-secret-1234","timeoutS":30},"timezone":"Asia/Shanghai","adminPassword":"hunter22"}`
	rec := doPOST(t, srv, "/api/setup", body)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"gatewayKey":"sk-local-`) {
		t.Fatalf("setup = %d %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "sk-secret-1234") {
		t.Fatalf("setup echoed upstream key: %s", rec.Body.String())
	}
	if rec := doPOST(t, srv, "/api/setup", body); rec.Code != 409 {
		t.Fatalf("second setup = %d", rec.Code)
	}
	srv2 := newAPIServerWithSeed(t, true)
	rec = doGET(t, srv2, "/api/settings")
	if strings.Contains(rec.Body.String(), "sk-up") || !strings.Contains(rec.Body.String(), `"last4":"1234"`) {
		t.Fatalf("mask leak: %s", rec.Body.String())
	}
}

func TestSetupValidationAndLoopback(t *testing.T) {
	srv := newAPIServer(t, false)
	rec := doPOST(t, srv, "/api/setup", `{"upstream":{"baseUrl":"not-a-url"}}`)
	if rec.Code != 400 || !strings.Contains(rec.Body.String(), `"baseUrl"`) {
		t.Fatalf("bad setup = %d %s", rec.Code, rec.Body.String())
	}

	// A fresh server whose handler sees a non-loopback peer must refuse.
	st, err := store.Open(filepath.Join(t.TempDir(), "setup.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	repo, err := settings.Open(st.DB())
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.RemoteAddr = "203.0.113.9:4321"
		api.New(api.Config{Repo: repo, Store: st}).Handler().ServeHTTP(w, r)
	}))
	t.Cleanup(ts.Close)
	rec = doPOST(t, ts, "/api/setup",
		`{"upstream":{"baseUrl":"https://api.example.com","apiKey":"sk-remote-1234"}}`)
	if rec.Code != 403 {
		t.Fatalf("remote setup = %d %s", rec.Code, rec.Body.String())
	}
}

func TestModelsCRUDAndWindows(t *testing.T) {
	srv := newAPIServer(t, true)
	rec := doPOST(t, srv, "/api/models", `{"name":"deepseek-chat","inputPerMTok":0.28,"outputPerMTok":0.42}`)
	if rec.Code != 201 {
		t.Fatalf("create = %d %s", rec.Code, rec.Body.String())
	}
	var created struct {
		ID int64 `json:"id"`
	}
	json.Unmarshal(rec.Body.Bytes(), &created)
	rec = doPut(t, srv, fmt.Sprintf("/api/models/%d/windows", created.ID),
		`{"windows":[{"label":"peak","daysMask":31,"startMinute":540,"endMinute":720,"inputPerMTok":0.56}]}`)
	if rec.Code != 200 {
		t.Fatalf("windows = %d %s", rec.Code, rec.Body.String())
	}
	rec = doPut(t, srv, fmt.Sprintf("/api/models/%d/windows", created.ID),
		`{"windows":[{"label":"bad","daysMask":0,"startMinute":60,"endMinute":60}]}`)
	if rec.Code != 400 {
		t.Fatalf("invalid windows = %d", rec.Code)
	}
	rec = doPOST(t, srv, "/api/models", `{"name":"deepseek-chat"}`)
	if rec.Code != 409 {
		t.Fatalf("duplicate = %d", rec.Code)
	}

	// The stored windows and the null reasoning rate round-trip.
	rec = doGET(t, srv, "/api/models")
	if rec.Code != 200 {
		t.Fatalf("list = %d %s", rec.Code, rec.Body.String())
	}
	var list struct {
		Models []struct {
			Name             string   `json:"name"`
			ReasoningPerMTok *float64 `json:"reasoningPerMTok"`
			Windows          []struct {
				Label     string   `json:"label"`
				DaysMask  int      `json:"daysMask"`
				InputRate *float64 `json:"inputPerMTok"`
			} `json:"windows"`
		} `json:"models"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Models) != 1 || len(list.Models[0].Windows) != 1 {
		t.Fatalf("list = %s", rec.Body.String())
	}
	if list.Models[0].Windows[0].Label != "peak" || list.Models[0].Windows[0].DaysMask != 31 {
		t.Fatalf("window = %+v", list.Models[0].Windows[0])
	}
	if !strings.Contains(rec.Body.String(), `"reasoningPerMTok":null`) {
		t.Fatalf("reasoning null missing: %s", rec.Body.String())
	}

	rec = doPATCH(t, srv, fmt.Sprintf("/api/models/%d", created.ID), `{"outputPerMTok":0.5,"reasoningPerMTok":null}`)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"outputPerMTok":0.5`) {
		t.Fatalf("patch = %d %s", rec.Code, rec.Body.String())
	}
	rec = doPATCH(t, srv, "/api/models/9999", `{"outputPerMTok":0.5}`)
	if rec.Code != 404 {
		t.Fatalf("patch missing = %d", rec.Code)
	}
	rec = doPut(t, srv, "/api/models/9999/windows", `{"windows":[]}`)
	if rec.Code != 404 {
		t.Fatalf("windows missing = %d", rec.Code)
	}
	if rec := doDELETE(t, srv, fmt.Sprintf("/api/models/%d", created.ID)); rec.Code != 204 {
		t.Fatalf("delete = %d", rec.Code)
	}
	if rec := doDELETE(t, srv, fmt.Sprintf("/api/models/%d", created.ID)); rec.Code != 404 {
		t.Fatalf("second delete = %d", rec.Code)
	}
}

func TestSettingsPatchAndRotation(t *testing.T) {
	srv := newAPIServerWithSeed(t, true)
	rec := doPATCH(t, srv, "/api/settings", `{"retentionDays":30,"upstream":{"timeoutS":60}}`)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"retentionDays":30`) {
		t.Fatalf("patch = %d %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"timeoutS":60`) {
		t.Fatalf("timeoutS not applied: %s", rec.Body.String())
	}
	rec = doPATCH(t, srv, "/api/settings", `{"retentionDays":1}`)
	if rec.Code != 400 {
		t.Fatalf("invalid retention = %d", rec.Code)
	}

	before := doGET(t, srv, "/api/settings")
	var state struct {
		GatewayKey struct {
			Set    bool   `json:"set"`
			Prefix string `json:"prefix"`
			Last4  string `json:"last4"`
		} `json:"gatewayKey"`
		AdminPasswordSet bool `json:"adminPasswordSet"`
	}
	if err := json.Unmarshal(before.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	if !state.GatewayKey.Set || state.GatewayKey.Prefix != "sk-local-" || len(state.GatewayKey.Last4) != 4 {
		t.Fatalf("gateway mask = %+v", state.GatewayKey)
	}

	rec = doPOST(t, srv, "/api/gateway-key/rotate", "")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"sk-local-`) {
		t.Fatalf("rotate = %d %s", rec.Code, rec.Body.String())
	}
	var rotated struct {
		GatewayKey string `json:"gatewayKey"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &rotated); err != nil {
		t.Fatal(err)
	}
	after := doGET(t, srv, "/api/settings")
	if strings.Contains(after.Body.String(), rotated.GatewayKey) {
		t.Fatalf("settings leaked gateway key: %s", after.Body.String())
	}
	if !strings.Contains(after.Body.String(), `"last4":"`+rotated.GatewayKey[len(rotated.GatewayKey)-4:]+`"`) {
		t.Fatalf("rotation not reflected: %s", after.Body.String())
	}
	if state.AdminPasswordSet {
		t.Fatalf("seeded server should have no admin password")
	}
}

func TestSettingsPasswordPatch(t *testing.T) {
	srv := newAPIServerWithSeed(t, true)
	rec := doPATCH(t, srv, "/api/settings", `{"newPassword":"short"}`)
	if rec.Code != 400 || !strings.Contains(rec.Body.String(), `"newPassword"`) {
		t.Fatalf("short password = %d %s", rec.Code, rec.Body.String())
	}
	rec = doPATCH(t, srv, "/api/settings", `{"newPassword":"hunter22"}`)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"adminPasswordSet":true`) {
		t.Fatalf("password patch = %d %s", rec.Code, rec.Body.String())
	}
	// The new password authenticates; a wrong one does not.
	if rec := doPOST(t, srv, "/api/auth/login", `{"password":"hunter22"}`); rec.Code != 200 {
		t.Fatalf("login with new password = %d %s", rec.Code, rec.Body.String())
	}
	if rec := doPOST(t, srv, "/api/auth/login", `{"password":"hunter23"}`); rec.Code != 401 {
		t.Fatalf("login with wrong password = %d", rec.Code)
	}
}

func TestUpstreamTest(t *testing.T) {
	var gotPath, gotAuth string
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotAuth = r.URL.Path, r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(ok.Close)
	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(failing.Close)

	// Before setup the endpoint needs only loopback.
	setupSrv := newAPIServer(t, false)
	rec := doPOST(t, setupSrv, "/api/upstream/test",
		fmt.Sprintf(`{"baseUrl":%q,"apiKey":"sk-probe-9999"}`, ok.URL+"/"))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"ok":true`) ||
		!strings.Contains(rec.Body.String(), `"status":200`) {
		t.Fatalf("pre-setup probe = %d %s", rec.Code, rec.Body.String())
	}
	if gotPath != "/models" {
		t.Fatalf("probe path = %q", gotPath)
	}
	if gotAuth != "Bearer sk-probe-9999" {
		t.Fatalf("probe auth = %q", gotAuth)
	}
	if strings.Contains(rec.Body.String(), "sk-probe-9999") {
		t.Fatalf("probe echoed key: %s", rec.Body.String())
	}

	// After setup the endpoint requires authentication (loopback without a
	// password is authenticated).
	srv := newAPIServerWithSeed(t, true)
	rec = doPOST(t, srv, "/api/upstream/test", fmt.Sprintf(`{"baseUrl":%q}`, failing.URL))
	if rec.Code != 502 || !strings.Contains(rec.Body.String(), `"ok":false`) ||
		!strings.Contains(rec.Body.String(), `"status":401`) {
		t.Fatalf("failing probe = %d %s", rec.Code, rec.Body.String())
	}
	rec = doPOST(t, srv, "/api/upstream/test", `{"baseUrl":"http://127.0.0.1:1"}`)
	if rec.Code != 502 || !strings.Contains(rec.Body.String(), `"ok":false`) ||
		!strings.Contains(rec.Body.String(), `"status":0`) {
		t.Fatalf("network probe = %d %s", rec.Code, rec.Body.String())
	}
	// The snapshot's saved key is used when the body omits it; the probe
	// never reflects secrets.
	rec = doPOST(t, srv, "/api/upstream/test", `{"baseUrl":"`+ok.URL+`"}`)
	if rec.Code != 200 || strings.Contains(rec.Body.String(), "sk-secret-1234") {
		t.Fatalf("snapshot key probe = %d %s", rec.Code, rec.Body.String())
	}
}

func TestConfigWritesBlockedBeforeSetup(t *testing.T) {
	srv := newAPIServer(t, false)
	rec := doPATCH(t, srv, "/api/settings", `{"retentionDays":30}`)
	if rec.Code != 409 || !strings.Contains(rec.Body.String(), `"code":"conflict"`) ||
		!strings.Contains(rec.Body.String(), "setup required") {
		t.Fatalf("pre-setup patch = %d %s", rec.Code, rec.Body.String())
	}
	rec = doPOST(t, srv, "/api/gateway-key/rotate", "")
	if rec.Code != 409 || !strings.Contains(rec.Body.String(), "setup required") {
		t.Fatalf("pre-setup rotate = %d %s", rec.Code, rec.Body.String())
	}
	rec = doPOST(t, srv, "/api/setup",
		`{"upstream":{"baseUrl":"https://api.example.com","apiKey":"sk-secret-1234","timeoutS":30}}`)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"gatewayKey":"sk-local-`) {
		t.Fatalf("setup after refused writes = %d %s", rec.Code, rec.Body.String())
	}
}

func TestTimeoutOverflowRejected(t *testing.T) {
	// 2^55+1 seconds wraps to 1s when multiplied by time.Second.
	const overflowing = 36028797018963969

	srv := newAPIServer(t, false)
	rec := doPOST(t, srv, "/api/setup",
		fmt.Sprintf(`{"upstream":{"baseUrl":"https://api.example.com","apiKey":"sk-secret-1234","timeoutS":%d}}`, overflowing))
	if rec.Code != 400 || !strings.Contains(rec.Body.String(), `"timeoutS"`) {
		t.Fatalf("overflow setup = %d %s", rec.Code, rec.Body.String())
	}
	// Setup is still possible afterwards.
	rec = doPOST(t, srv, "/api/setup",
		`{"upstream":{"baseUrl":"https://api.example.com","apiKey":"sk-secret-1234","timeoutS":30}}`)
	if rec.Code != 200 {
		t.Fatalf("setup after overflow = %d %s", rec.Code, rec.Body.String())
	}

	seeded := newAPIServerWithSeed(t, true)
	rec = doPATCH(t, seeded, "/api/settings", fmt.Sprintf(`{"upstream":{"timeoutS":%d}}`, overflowing))
	if rec.Code != 400 || !strings.Contains(rec.Body.String(), `"timeoutS"`) {
		t.Fatalf("overflow patch = %d %s", rec.Code, rec.Body.String())
	}
	// The stored timeout is untouched.
	if rec := doGET(t, seeded, "/api/settings"); !strings.Contains(rec.Body.String(), `"timeoutS":30`) {
		t.Fatalf("timeout changed: %s", rec.Body.String())
	}
}
