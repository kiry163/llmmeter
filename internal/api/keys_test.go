package api_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/kiry163/llmmeter/internal/config"
	"github.com/kiry163/llmmeter/internal/secure"
)

// keyListBody is the decoded shape of one key list entry.
type keyListBody struct {
	ID           string  `json:"id"`
	Name         string  `json:"name"`
	KeyMasked    string  `json:"keyMasked"`
	Enabled      bool    `json:"enabled"`
	UpstreamID   string  `json:"upstreamId"`
	UpstreamName string  `json:"upstreamName"`
	LastUsedAt   *string `json:"lastUsedAt"`
}

// keyCreatedBody is the decoded shape of a create or rotate response.
type keyCreatedBody struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Key          string `json:"key"`
	KeyMasked    string `json:"keyMasked"`
	UpstreamID   string `json:"upstreamId"`
	UpstreamName string `json:"upstreamName"`
}

func decodeKeys(t *testing.T, body []byte) []keyListBody {
	t.Helper()
	var out struct {
		Keys []keyListBody `json:"keys"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode keys: %v: %s", err, body)
	}
	return out.Keys
}

// twoUpstreamEnv configures upstreams a and b.
func twoUpstreamEnv(t *testing.T) *configTestEnv {
	t.Helper()
	env := newConfigEnv(t, "127.0.0.1:8100")
	if err := env.config.Update(func(c *config.Config) error {
		c.Upstreams = append(c.Upstreams,
			config.Upstream{ID: "a", Name: "Alpha", BaseURL: "https://a.example.com"},
			config.Upstream{ID: "b", Name: "Beta", BaseURL: "https://b.example.com"},
		)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return env
}

func TestCreateKeyReturnsSecretOnce(t *testing.T) {
	env := twoUpstreamEnv(t)

	rec := doPOST(t, env.srv, "/api/keys", `{"name":"MacBook","upstreamId":"a"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create = %d %s", rec.Code, rec.Body.String())
	}
	var created keyCreatedBody
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.ID != "macbook" || created.Name != "MacBook" || created.UpstreamID != "a" ||
		created.UpstreamName != "Alpha" {
		t.Fatalf("created = %+v", created)
	}
	if !strings.HasPrefix(created.Key, "sk-local-") || created.KeyMasked == "" ||
		strings.Contains(created.KeyMasked, strings.TrimPrefix(created.Key, "sk-local-")) {
		t.Fatalf("created = %+v", created)
	}
	if !strings.Contains(readConfigFile(t, env.configPath), "key: "+created.Key) {
		t.Fatal("key was not persisted")
	}
	binding, ok := env.config.Snapshot().LookupKey(created.Key)
	if !ok || binding.UpstreamID != "a" || binding.KeyID != "macbook" {
		t.Fatalf("binding = %+v ok=%v", binding, ok)
	}

	rec = doGET(t, env.srv, "/api/keys")
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), created.Key) {
		t.Fatalf("list leaked the key: %d %s", rec.Code, rec.Body.String())
	}

	rec = doPOST(t, env.srv, "/api/keys", `{"name":"MacBook","upstreamId":"a"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("second create = %d %s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.ID != "macbook-2" {
		t.Fatalf("created = %+v", created)
	}

	rec = doPOST(t, env.srv, "/api/keys", `{"name":"K","upstreamId":"missing"}`)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), `"upstreamId"`) {
		t.Fatalf("unknown upstream = %d %s", rec.Code, rec.Body.String())
	}
	rec = doPOST(t, env.srv, "/api/keys", `{"upstreamId":"a"}`)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), `"name"`) {
		t.Fatalf("missing name = %d %s", rec.Code, rec.Body.String())
	}
}

func TestPatchKeyRebindsUpstream(t *testing.T) {
	env := twoUpstreamEnv(t)
	raw := addTestKey(t, env.config, "k", "a")

	rec := doPATCH(t, env.srv, "/api/keys/k", `{"upstreamId":"b"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("patch = %d %s", rec.Code, rec.Body.String())
	}
	var got keyListBody
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.ID != "k" || got.UpstreamID != "b" || got.UpstreamName != "Beta" {
		t.Fatalf("patched = %+v", got)
	}
	if !strings.Contains(readConfigFile(t, env.configPath), "upstream_id: b") {
		t.Fatal("config did not persist the rebind")
	}
	binding, ok := env.config.Snapshot().LookupKey(raw)
	if !ok || binding.UpstreamID != "b" || binding.UpstreamName != "Beta" {
		t.Fatalf("snapshot binding = %+v ok=%v", binding, ok)
	}

	rec = doPATCH(t, env.srv, "/api/keys/k", `{"name":"Laptop","enabled":false}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("patch = %d %s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Name != "Laptop" || got.Enabled || got.UpstreamID != "b" {
		t.Fatalf("patched = %+v", got)
	}
	binding, _ = env.config.Snapshot().LookupKey(raw)
	if binding.Enabled {
		t.Fatal("disabled key still reports enabled in the snapshot")
	}

	rec = doPATCH(t, env.srv, "/api/keys/k", `{"upstreamId":"missing"}`)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), `"upstreamId"`) {
		t.Fatalf("unknown upstream = %d %s", rec.Code, rec.Body.String())
	}
	if binding, _ := env.config.Snapshot().LookupKey(raw); binding.UpstreamID != "b" {
		t.Fatalf("failed patch rebind = %+v", binding)
	}
	rec = doPATCH(t, env.srv, "/api/keys/missing", `{"name":"X"}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing key = %d %s", rec.Code, rec.Body.String())
	}
}

func TestKeySecretRequiresAdminAndNoStore(t *testing.T) {
	env := twoUpstreamEnv(t)
	raw := addTestKey(t, env.config, "k", "a")

	rec := doGET(t, env.srv, "/api/keys/k/secret")
	if rec.Code != http.StatusOK {
		t.Fatalf("secret = %d %s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("Cache-Control = %q", rec.Header().Get("Cache-Control"))
	}
	var body struct {
		Key string `json:"key"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Key != raw {
		t.Fatalf("revealed key = %q, want %q", body.Key, raw)
	}

	rec = doGET(t, env.srv, "/api/keys/missing/secret")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing secret = %d %s", rec.Code, rec.Body.String())
	}

	// A configured admin password makes the session mandatory even on loopback.
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
	rec = doGET(t, env.srv, "/api/keys/k/secret")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated secret = %d %s", rec.Code, rec.Body.String())
	}
}

func TestRotateKeyInvalidatesOld(t *testing.T) {
	env := twoUpstreamEnv(t)
	raw := addTestKey(t, env.config, "k", "a")

	rec := doPOST(t, env.srv, "/api/keys/k/rotate", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("rotate = %d %s", rec.Code, rec.Body.String())
	}
	var got keyCreatedBody
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.ID != "k" || got.UpstreamID != "a" || got.UpstreamName != "Alpha" {
		t.Fatalf("rotated = %+v", got)
	}
	if got.Key == raw || !strings.HasPrefix(got.Key, "sk-local-") {
		t.Fatalf("rotated key = %q", got.Key)
	}
	snap := env.config.Snapshot()
	if _, ok := snap.LookupKey(raw); ok {
		t.Fatal("old key still resolves")
	}
	if binding, ok := snap.LookupKey(got.Key); !ok || binding.KeyID != "k" {
		t.Fatalf("new key binding = %+v ok=%v", binding, ok)
	}
	file := readConfigFile(t, env.configPath)
	if strings.Contains(file, raw) || !strings.Contains(file, got.Key) {
		t.Fatal("config file was not rotated")
	}

	rec = doPOST(t, env.srv, "/api/keys/missing/rotate", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing rotate = %d %s", rec.Code, rec.Body.String())
	}
}

func TestDeleteKey(t *testing.T) {
	env := twoUpstreamEnv(t)
	addTestKey(t, env.config, "k", "a")

	rec := doDELETE(t, env.srv, "/api/keys/k")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete = %d %s", rec.Code, rec.Body.String())
	}
	for _, k := range env.config.Snapshot().Config.GatewayKeys {
		if k.ID == "k" {
			t.Fatal("key still configured")
		}
	}
	rec = doDELETE(t, env.srv, "/api/keys/k")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("second delete = %d %s", rec.Code, rec.Body.String())
	}
}

func TestKeyListFiltersByUpstreamAndShowsLastUsed(t *testing.T) {
	env := twoUpstreamEnv(t)
	addTestKey(t, env.config, "ka", "a")
	addTestKey(t, env.config, "kb", "b")
	seedRequest(t, env, "a", "ka", time.Date(2026, 10, 29, 4, 0, 0, 0, time.UTC))

	rec := doGET(t, env.srv, "/api/keys?upstream=a")
	if rec.Code != http.StatusOK {
		t.Fatalf("list = %d %s", rec.Code, rec.Body.String())
	}
	keys := decodeKeys(t, rec.Body.Bytes())
	if len(keys) != 1 {
		t.Fatalf("keys = %+v", keys)
	}
	got := keys[0]
	if got.ID != "ka" || got.UpstreamID != "a" || got.UpstreamName != "Alpha" || !got.Enabled {
		t.Fatalf("key = %+v", got)
	}
	if got.LastUsedAt == nil || *got.LastUsedAt != "2026-10-29T04:00:00Z" {
		t.Fatalf("lastUsedAt = %v", got.LastUsedAt)
	}
	if got.KeyMasked == "" || strings.Contains(got.KeyMasked, "ka-000000000000") {
		t.Fatalf("keyMasked = %q", got.KeyMasked)
	}

	rec = doGET(t, env.srv, "/api/keys?upstream=b")
	keys = decodeKeys(t, rec.Body.Bytes())
	if len(keys) != 1 || keys[0].ID != "kb" || keys[0].LastUsedAt != nil {
		t.Fatalf("keys = %+v", keys)
	}

	rec = doGET(t, env.srv, "/api/keys")
	if keys = decodeKeys(t, rec.Body.Bytes()); len(keys) != 2 {
		t.Fatalf("keys = %+v", keys)
	}
	for _, want := range []string{"sk-local-test-ka", "sk-local-test-kb"} {
		if strings.Contains(rec.Body.String(), want) {
			t.Fatalf("list leaked %q: %s", want, rec.Body.String())
		}
	}
}
