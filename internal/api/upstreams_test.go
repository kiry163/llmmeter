package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/kiry163/llmmeter/internal/config"
	"github.com/kiry163/llmmeter/internal/pricecatalog"
	"github.com/kiry163/llmmeter/internal/secure"
	"github.com/kiry163/llmmeter/internal/store"
	"github.com/kiry163/llmmeter/internal/usage"
)

// addTestKey appends one gateway key bound to upstreamID and returns its
// plaintext value.
func addTestKey(t *testing.T, cs *config.Store, id, upstreamID string) string {
	t.Helper()
	key := "sk-local-test-" + id + "-000000000000"
	if err := cs.Update(func(c *config.Config) error {
		c.GatewayKeys = append(c.GatewayKeys, config.GatewayKey{
			ID:         id,
			Name:       id,
			Key:        key,
			UpstreamID: upstreamID,
		})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return key
}

// seedRequest records one completed request so lastUsedAt can be observed.
func seedRequest(t *testing.T, env *configTestEnv, upstreamID, keyID string, at time.Time) {
	t.Helper()
	env.data.EnqueueRecord(store.Record{
		StartedAt: at, CompletedAt: at.Add(time.Second),
		LocalDay: at.Format("2006-01-02"), MinuteOfDay: at.Hour()*60 + at.Minute(),
		Path: "/v1/chat/completions", PathClass: "chat",
		UpstreamID: upstreamID, UpstreamName: upstreamID,
		KeyID: keyID, KeyName: keyID,
		Model: "m", Status: 200, LatencyMS: 10,
		Usage:       usage.Usage{TotalTokens: 1},
		WindowLabel: "base", UsageStatus: usage.StatusOK,
	})
	if err := env.data.Flush(); err != nil {
		t.Fatal(err)
	}
}

// upstreamBody is the decoded shape of one upstream list entry.
type upstreamBody struct {
	ID           string  `json:"id"`
	Name         string  `json:"name"`
	BaseURL      string  `json:"baseUrl"`
	APIKeyMasked string  `json:"apiKeyMasked"`
	Enabled      bool    `json:"enabled"`
	LastUsedAt   *string `json:"lastUsedAt"`
	ModelCount   int     `json:"modelCount"`
	KeyCount     int     `json:"keyCount"`
}

func decodeUpstreams(t *testing.T, body []byte) []upstreamBody {
	t.Helper()
	var out struct {
		Upstreams []upstreamBody `json:"upstreams"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode upstreams: %v: %s", err, body)
	}
	return out.Upstreams
}

func TestListUpstreamsHasCountsAndNeverSecrets(t *testing.T) {
	env := newConfigEnv(t, "127.0.0.1:8100")
	if err := env.config.Update(func(c *config.Config) error {
		c.Upstreams = append(c.Upstreams, config.Upstream{
			ID: "a", Name: "Upstream A", BaseURL: "https://a.example.com",
			APIKey: "sk-upstream-secret",
			Models: []config.Model{{Name: "m", Input: 1}},
		})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	addTestKey(t, env.config, "ka", "a")
	addTestKey(t, env.config, "kb", "a")
	seedRequest(t, env, "a", "ka", time.Date(2026, 10, 29, 4, 0, 0, 0, time.UTC))

	rec := doGET(t, env.srv, "/api/upstreams")
	if rec.Code != http.StatusOK {
		t.Fatalf("list = %d %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "sk-upstream-secret") {
		t.Fatalf("list leaked the upstream key: %s", rec.Body.String())
	}
	ups := decodeUpstreams(t, rec.Body.Bytes())
	if len(ups) != 1 {
		t.Fatalf("upstreams = %+v", ups)
	}
	got := ups[0]
	if got.ID != "a" || got.Name != "Upstream A" || got.BaseURL != "https://a.example.com" ||
		!got.Enabled || got.ModelCount != 1 || got.KeyCount != 2 {
		t.Fatalf("upstream = %+v", got)
	}
	if got.APIKeyMasked == "" || strings.Contains(got.APIKeyMasked, "upstream-secret") {
		t.Fatalf("masked key = %q", got.APIKeyMasked)
	}
	if got.LastUsedAt == nil || *got.LastUsedAt != "2026-10-29T04:00:00Z" {
		t.Fatalf("lastUsedAt = %v", got.LastUsedAt)
	}
}

func TestCreateUpstreamSlugsAndDefaults(t *testing.T) {
	env := newConfigEnv(t, "127.0.0.1:8100")
	addTestUpstream(t, env.config, "deepseek")

	rec := doPOST(t, env.srv, "/api/upstreams", `{
		"name": "DeepSeek", "baseUrl": "https://api.deepseek.com/", "apiKey": "sk-upstream"
	}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create = %d %s", rec.Code, rec.Body.String())
	}
	var created upstreamBody
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.ID != "deepseek-2" || !created.Enabled || created.BaseURL != "https://api.deepseek.com" {
		t.Fatalf("created = %+v", created)
	}
	if created.KeyCount != 0 || created.APIKeyMasked == "" {
		t.Fatalf("created = %+v", created)
	}
	file := readConfigFile(t, env.configPath)
	for _, want := range []string{"id: deepseek-2", "base_url: https://api.deepseek.com", "api_key: sk-upstream"} {
		if !strings.Contains(file, want) {
			t.Fatalf("config missing %q:\n%s", want, file)
		}
	}

	// A name without ASCII characters falls back to "upstream"; a second one
	// gets -2. enabled:false is persisted as given.
	rec = doPOST(t, env.srv, "/api/upstreams", `{"name":"模型","baseUrl":"https://b.example.com","enabled":false}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create = %d %s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.ID != "upstream" || created.Enabled {
		t.Fatalf("created = %+v", created)
	}
	rec = doPOST(t, env.srv, "/api/upstreams", `{"name":"另一个","baseUrl":"https://c.example.com"}`)
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.ID != "upstream-2" {
		t.Fatalf("created = %+v", created)
	}

	rec = doPOST(t, env.srv, "/api/upstreams", `{"name":"Bad","baseUrl":"ftp://x"}`)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), `"baseUrl"`) {
		t.Fatalf("invalid create = %d %s", rec.Code, rec.Body.String())
	}
}

func TestPatchUpstreamPartial(t *testing.T) {
	env := newConfigEnv(t, "127.0.0.1:8100")
	if err := env.config.Update(func(c *config.Config) error {
		c.Upstreams = append(c.Upstreams, config.Upstream{
			ID: "a", Name: "A", BaseURL: "https://a.example.com", APIKey: "sk-aaaa1111",
		})
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	rec := doPATCH(t, env.srv, "/api/upstreams/a", `{"name":"Renamed","enabled":false}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("patch = %d %s", rec.Code, rec.Body.String())
	}
	var got upstreamBody
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Name != "Renamed" || got.Enabled || got.BaseURL != "https://a.example.com" || got.APIKeyMasked != "sk-…" {
		t.Fatalf("patched = %+v", got)
	}

	rec = doPATCH(t, env.srv, "/api/upstreams/a", `{"baseUrl":"https://new.example.com/v1"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("patch baseUrl = %d %s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.BaseURL != "https://new.example.com/v1" {
		t.Fatalf("patched = %+v", got)
	}

	rec = doPATCH(t, env.srv, "/api/upstreams/a", `{"baseUrl":"file:///tmp/x"}`)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), `"baseUrl"`) {
		t.Fatalf("invalid patch = %d %s", rec.Code, rec.Body.String())
	}
	rec = doPATCH(t, env.srv, "/api/upstreams/missing", `{"name":"X"}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing patch = %d %s", rec.Code, rec.Body.String())
	}
}

func TestDeleteUpstreamWithKeysIs409(t *testing.T) {
	env := newConfigEnv(t, "127.0.0.1:8100")
	addTestUpstream(t, env.config, "a")
	addTestKey(t, env.config, "k", "a")
	seedRequest(t, env, "a", "k", time.Date(2026, 10, 29, 4, 0, 0, 0, time.UTC))

	rec := doDELETE(t, env.srv, "/api/upstreams/a")
	if rec.Code != http.StatusConflict {
		t.Fatalf("delete = %d %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Error struct {
			Code   string         `json:"code"`
			Fields map[string]any `json:"fields"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Error.Code != "upstream_in_use" {
		t.Fatalf("code = %q", body.Error.Code)
	}
	keys, _ := body.Error.Fields["keys"].([]any)
	if len(keys) != 1 || keys[0] != "k" {
		t.Fatalf("fields.keys = %v", body.Error.Fields)
	}
	if _, ok := env.config.Snapshot().Upstream("a"); !ok {
		t.Fatal("upstream was removed despite the conflict")
	}

	rec = doDELETE(t, env.srv, "/api/keys/k")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete key = %d %s", rec.Code, rec.Body.String())
	}
	rec = doDELETE(t, env.srv, "/api/upstreams/a")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete = %d %s", rec.Code, rec.Body.String())
	}
	if _, ok := env.config.Snapshot().Upstream("a"); ok {
		t.Fatal("upstream still configured")
	}
	var n int
	if err := env.data.DB().QueryRow("SELECT COUNT(*) FROM requests WHERE upstream_id = 'a'").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("history rows = %d, want 1", n)
	}
}

// modelsUpstream serves an OpenAI-compatible models listing at /v1/models and
// nothing at /models, exercising the fallback path.
func modelsUpstream(t *testing.T, status int) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/models", func(w http.ResponseWriter, r *http.Request) {
		if status != http.StatusOK {
			w.WriteHeader(status)
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer sk-draft" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"m1","owned_by":"x"},{"id":"m2","owned_by":"y"}]}`))
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts
}

func TestUpstreamProbeDoesNotWriteConfig(t *testing.T) {
	up := modelsUpstream(t, http.StatusOK)
	env := newConfigEnv(t, "127.0.0.1:8100")

	rec := doPOST(t, env.srv, "/api/upstreams/probe",
		`{"baseUrl":"`+up.URL+`/","apiKey":"sk-draft"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("probe = %d %s", rec.Code, rec.Body.String())
	}
	var got struct {
		OK             bool   `json:"ok"`
		LatencyMS      int64  `json:"latencyMs"`
		Provider       string `json:"provider"`
		PresetSnapshot string `json:"presetSnapshot"`
		Models         []struct {
			ID      string `json:"id"`
			OwnedBy string `json:"ownedBy"`
			Matched bool   `json:"matched"`
		} `json:"models"`
		Presets []any  `json:"presets"`
		Error   string `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if !got.OK || got.Error != "" || got.LatencyMS < 0 {
		t.Fatalf("probe = %+v", got)
	}
	if len(got.Models) != 2 || got.Models[0].ID != "m1" || got.Models[1].OwnedBy != "y" {
		t.Fatalf("models = %+v", got.Models)
	}
	// The test upstream is a loopback host, so no provider or preset matches.
	if got.Provider != "" {
		t.Fatalf("provider = %q, want empty", got.Provider)
	}
	if got.PresetSnapshot != pricecatalog.SnapshotDate {
		t.Fatalf("presetSnapshot = %q", got.PresetSnapshot)
	}
	if got.Models[0].Matched || got.Models[1].Matched {
		t.Fatalf("unexpected preset match: %+v", got.Models)
	}
	if got.Presets == nil {
		t.Fatal("presets must be an empty array, not null")
	}
	if _, err := os.Stat(env.configPath); !os.IsNotExist(err) {
		t.Fatalf("probe wrote the config file: %v", err)
	}

	rec = doPOST(t, env.srv, "/api/upstreams/probe", `{"baseUrl":"ftp://example.com"}`)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), `"baseUrl"`) {
		t.Fatalf("invalid probe = %d %s", rec.Code, rec.Body.String())
	}

	failing := modelsUpstream(t, http.StatusInternalServerError)
	rec = doPOST(t, env.srv, "/api/upstreams/probe",
		`{"baseUrl":"`+failing.URL+`","apiKey":"sk-draft"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("failing probe = %d %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"ok":false`) || strings.Contains(rec.Body.String(), "sk-draft") {
		t.Fatalf("failing probe = %s", rec.Body.String())
	}
}

func TestUpstreamTestReportsUpstreamFailureWith200(t *testing.T) {
	env := newConfigEnv(t, "127.0.0.1:8100")
	up := modelsUpstream(t, http.StatusOK)
	if err := env.config.Update(func(c *config.Config) error {
		c.Upstreams = append(c.Upstreams, config.Upstream{
			ID: "a", Name: "A", BaseURL: up.URL, APIKey: "sk-draft",
		})
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	rec := doPOST(t, env.srv, "/api/upstreams/a/test", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("test = %d %s", rec.Code, rec.Body.String())
	}
	var got struct {
		OK         bool   `json:"ok"`
		ModelCount int    `json:"modelCount"`
		Error      string `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if !got.OK || got.ModelCount != 2 || got.Error != "" {
		t.Fatalf("test = %+v", got)
	}

	failing := modelsUpstream(t, http.StatusInternalServerError)
	if err := env.config.Update(func(c *config.Config) error {
		c.Upstreams[0].BaseURL = failing.URL
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	rec = doPOST(t, env.srv, "/api/upstreams/a/test", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("failing test = %d %s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.OK || got.ModelCount != 0 || got.Error == "" {
		t.Fatalf("failing test = %+v", got)
	}

	rec = doPOST(t, env.srv, "/api/upstreams/missing/test", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing test = %d %s", rec.Code, rec.Body.String())
	}
}

func TestUpstreamSecretRevealNoStore(t *testing.T) {
	env := newConfigEnv(t, "127.0.0.1:8100")
	if err := env.config.Update(func(c *config.Config) error {
		c.Upstreams = append(c.Upstreams, config.Upstream{
			ID: "a", Name: "A", BaseURL: "https://a.example.com", APIKey: "sk-upstream-secret",
		})
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	rec := doGET(t, env.srv, "/api/upstreams/a/secret")
	if rec.Code != http.StatusOK {
		t.Fatalf("secret = %d %s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("Cache-Control = %q", rec.Header().Get("Cache-Control"))
	}
	if !strings.Contains(rec.Body.String(), `"apiKey":"sk-upstream-secret"`) {
		t.Fatalf("secret body = %s", rec.Body.String())
	}

	rec = doGET(t, env.srv, "/api/upstreams/missing/secret")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing secret = %d %s", rec.Code, rec.Body.String())
	}

	// With an admin password configured, loopback alone is not enough.
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
	rec = doGET(t, env.srv, "/api/upstreams/a/secret")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated secret = %d %s", rec.Code, rec.Body.String())
	}
}

func TestUpstreamMaskHidesShortKeys(t *testing.T) {
	env := newConfigEnv(t, "127.0.0.1:8100")
	short := map[string]string{
		"u9":  "lm-studio",
		"u10": "1234567890",
		"u12": "123456789012",
		"u13": "1234567890123",
	}
	ids := []string{"u9", "u10", "u12", "u13"}
	if err := env.config.Update(func(c *config.Config) error {
		for _, id := range ids {
			c.Upstreams = append(c.Upstreams, config.Upstream{
				ID: id, Name: id, BaseURL: "https://" + id + ".example.com", APIKey: short[id],
			})
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	rec := doGET(t, env.srv, "/api/upstreams")
	if rec.Code != http.StatusOK {
		t.Fatalf("list = %d %s", rec.Code, rec.Body.String())
	}
	ups := decodeUpstreams(t, rec.Body.Bytes())
	if len(ups) != len(ids) {
		t.Fatalf("upstreams = %+v", ups)
	}
	// A short secret keeps only its prefix; head and tail would cover it.
	want := map[string]string{
		"u9":  "lm-s…",
		"u10": "1234…",
		"u12": "1234…",
		"u13": "1234…",
	}
	seen := map[string]int{}
	for _, u := range ups {
		if u.APIKeyMasked != want[u.ID] {
			t.Fatalf("%s masked = %q, want %q", u.ID, u.APIKeyMasked, want[u.ID])
		}
		if strings.Contains(rec.Body.String(), short[u.ID]) {
			t.Fatalf("%s leaked the full key %q", u.ID, short[u.ID])
		}
		seen[u.ID]++
	}
	for _, id := range ids {
		if seen[id] != 1 {
			t.Fatalf("%s missing from list", id)
		}
	}

	// The create response uses the same masking.
	rec = doPOST(t, env.srv, "/api/upstreams", `{"name":"Short","baseUrl":"https://s.example.com","apiKey":"lm-studio"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create = %d %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "lm-studio") {
		t.Fatalf("create leaked the key: %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"apiKeyMasked":"lm-s…"`) {
		t.Fatalf("create = %s", rec.Body.String())
	}
}

func TestUpstreamProbeWithoutKeySendsNoAuthorization(t *testing.T) {
	var gotAuth string
	mux := http.NewServeMux()
	mux.HandleFunc("/models", func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"data":[{"id":"m1"}]}`))
	})
	up := httptest.NewServer(mux)
	t.Cleanup(up.Close)

	env := newConfigEnv(t, "127.0.0.1:8100")
	if err := env.config.Update(func(c *config.Config) error {
		c.Upstreams = append(c.Upstreams, config.Upstream{
			ID: "a", Name: "A", BaseURL: up.URL, APIKey: "sk-upstream-secret",
		})
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	rec := doPOST(t, env.srv, "/api/upstreams/probe", `{"baseUrl":"`+up.URL+`"}`)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"ok":true`) {
		t.Fatalf("probe = %d %s", rec.Code, rec.Body.String())
	}
	if gotAuth != "" {
		t.Fatalf("probe reused a saved key: Authorization = %q", gotAuth)
	}
	if strings.Contains(rec.Body.String(), "sk-upstream-secret") {
		t.Fatalf("probe leaked the saved key: %s", rec.Body.String())
	}
}

func TestUpstreamProbeTimeoutIsTotal(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/models", func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(4 * time.Second):
			w.WriteHeader(http.StatusNotFound)
		case <-r.Context().Done():
		}
	})
	mux.HandleFunc("/v1/models", func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(4 * time.Second):
			_, _ = w.Write([]byte(`{"data":[{"id":"late"}]}`))
		case <-r.Context().Done():
		}
	})
	up := httptest.NewServer(mux)
	t.Cleanup(up.Close)

	env := newConfigEnv(t, "127.0.0.1:8100")
	start := time.Now()
	rec := doPOST(t, env.srv, "/api/upstreams/probe", `{"baseUrl":"`+up.URL+`"}`)
	elapsed := time.Since(start)
	if rec.Code != http.StatusOK {
		t.Fatalf("probe = %d %s", rec.Code, rec.Body.String())
	}
	var got struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.OK || got.Error == "" {
		t.Fatalf("probe = %+v", got)
	}
	// Per-attempt deadlines would allow 4s + 4s; the shared deadline must stop
	// the probe near 5s.
	if elapsed >= 7*time.Second {
		t.Fatalf("probe took %v, want the two attempts to share one 5s deadline", elapsed)
	}
}

func TestConfigWritesRequireSetup(t *testing.T) {
	env := newConfigEnv(t, "127.0.0.1:8100")

	for _, tc := range []struct {
		method, path, body string
	}{
		{http.MethodPost, "/api/upstreams", `{"name":"A","baseUrl":"https://a.example.com"}`},
		{http.MethodPatch, "/api/upstreams/a", `{"name":"B"}`},
		{http.MethodDelete, "/api/upstreams/a", ""},
		{http.MethodPost, "/api/keys", `{"name":"K","upstreamId":"a"}`},
		{http.MethodPatch, "/api/keys/k", `{"name":"B"}`},
		{http.MethodDelete, "/api/keys/k", ""},
		{http.MethodPost, "/api/keys/k/rotate", ""},
	} {
		var rec *httptest.ResponseRecorder
		switch tc.method {
		case http.MethodPost:
			rec = doPOST(t, env.srv, tc.path, tc.body)
		case http.MethodPatch:
			rec = doPATCH(t, env.srv, tc.path, tc.body)
		default:
			rec = doDELETE(t, env.srv, tc.path)
		}
		if rec.Code != http.StatusConflict {
			t.Fatalf("%s %s = %d %s", tc.method, tc.path, rec.Code, rec.Body.String())
		}
		var body struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("%s %s: decode: %v", tc.method, tc.path, err)
		}
		if body.Error.Code != "setup_required" {
			t.Fatalf("%s %s code = %q, want setup_required", tc.method, tc.path, body.Error.Code)
		}
	}

	for _, path := range []string{"/api/upstreams", "/api/keys"} {
		rec := doGET(t, env.srv, path)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s = %d %s", path, rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), `[]`) {
			t.Fatalf("%s = %s", path, rec.Body.String())
		}
	}
}
