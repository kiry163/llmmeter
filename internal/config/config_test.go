package config

import (
	"errors"
	"math"
	"strings"
	"testing"
)

func TestParseRejectsUnknownField(t *testing.T) {
	_, err := Parse([]byte("version: 1\nserver:\n  listen: 127.0.0.1:8100\n  nope: true\n"))
	if err == nil || !strings.Contains(err.Error(), "nope") {
		t.Fatalf("err = %v", err)
	}
}

func TestValidateRejectsKeyWithoutUpstream(t *testing.T) {
	c := minimalConfig()
	c.GatewayKeys = []GatewayKey{{ID: "mac", Name: "Mac", Key: "sk-local-1234567890", UpstreamID: "missing", Enabled: new(true)}}
	err := c.Validate()
	if err == nil || !strings.Contains(err.Error(), "upstream_id") {
		t.Fatalf("err = %v", err)
	}
}

func TestActiveDefaultsTrue(t *testing.T) {
	u := Upstream{}
	if !u.Active() {
		t.Fatal("omitted enabled must default true")
	}
	if !(GatewayKey{}).Active() {
		t.Fatal("omitted enabled must default true")
	}
	if (Upstream{Enabled: new(false)}).Active() {
		t.Fatal("explicit false must be inactive")
	}
}

func TestParseAppliesDefaults(t *testing.T) {
	c, err := Parse([]byte("version: 1\n"))
	if err != nil {
		t.Fatal(err)
	}
	if c.Server.Listen != "127.0.0.1:8100" {
		t.Fatalf("listen = %q", c.Server.Listen)
	}
	if c.General.RetentionDays != 90 {
		t.Fatalf("retention = %d", c.General.RetentionDays)
	}
	if c.General.Timezone != "" {
		t.Fatalf("timezone = %q, want empty for caller default", c.General.Timezone)
	}
}

func TestParseEmptyDocumentUsesDefaults(t *testing.T) {
	c, err := Parse(nil)
	if err != nil {
		t.Fatal(err)
	}
	if c.Server.Listen != "127.0.0.1:8100" || c.General.RetentionDays != 90 {
		t.Fatalf("defaults missing: %+v", c)
	}
}

func TestParseRejectsDuplicateKey(t *testing.T) {
	_, err := Parse([]byte("version: 1\nversion: 1\n"))
	if err == nil {
		t.Fatal("duplicate key must fail")
	}
}

func TestParseRejectsDuplicateNestedKey(t *testing.T) {
	_, err := Parse([]byte("version: 1\nserver:\n  listen: \":8100\"\n  listen: \":8101\"\n"))
	if err == nil {
		t.Fatal("duplicate nested key must fail")
	}
}

func TestParseRejectsWrongType(t *testing.T) {
	_, err := Parse([]byte("version: one\n"))
	if err == nil {
		t.Fatal("wrong type must fail")
	}
}

func TestParseFullExample(t *testing.T) {
	c, err := Parse([]byte(fullExample))
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if got := c.Upstreams[0].BaseURL; got != "https://api.deepseek.com" {
		t.Fatalf("base_url = %q", got)
	}
	if len(c.Upstreams[0].Models) != 1 || len(c.Upstreams[0].Models[0].Windows) != 1 {
		t.Fatalf("nested decoding failed: %+v", c.Upstreams[0])
	}
	if c.Upstreams[0].Models[0].Windows[0].CacheWrite != nil {
		t.Fatal("null window rate must stay nil")
	}
	if c.Upstreams[0].Models[0].Windows[0].Input == nil || *c.Upstreams[0].Models[0].Windows[0].Input != 0.5 {
		t.Fatal("window rate not decoded")
	}
}

func TestValidateAcceptsMinimalConfig(t *testing.T) {
	if err := minimalConfig().Validate(); err != nil {
		t.Fatalf("minimal config must validate: %v", err)
	}
}

func TestValidateCollectsAllErrors(t *testing.T) {
	c := minimalConfig()
	c.Version = 2
	c.Server.Database = ""
	c.General.Timezone = "Not/AZone"
	c.General.RetentionDays = 1
	err := c.Validate()
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("err = %v, want *ValidationError", err)
	}
	for _, want := range []string{"version", "server.database", "general.timezone", "general.retention_days"} {
		if _, ok := ve.Fields[want]; !ok {
			t.Errorf("missing field error %q in %v", want, ve.Fields)
		}
	}
}

func TestValidateVersion(t *testing.T) {
	for _, v := range []int{0, 2, -1} {
		c := minimalConfig()
		c.Version = v
		if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "version") {
			t.Fatalf("version %d: err = %v", v, err)
		}
	}
}

func TestValidateListen(t *testing.T) {
	bad := []string{"", "127.0.0.1", "127.0.0.1:0", "127.0.0.1:70000", "127.0.0.1:http", "not a listen"}
	for _, listen := range bad {
		c := minimalConfig()
		c.Server.Listen = listen
		if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "server.listen") {
			t.Errorf("listen %q: err = %v", listen, err)
		}
	}
}

func TestValidateNonLoopbackRequiresPassword(t *testing.T) {
	c := minimalConfig()
	c.Server.Listen = "0.0.0.0:8100"
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "admin_password_hash") {
		t.Fatalf("err = %v", err)
	}

	c.Auth.AdminPasswordHash = testPasswordHash
	if err := c.Validate(); err != nil {
		t.Fatalf("non-loopback with password: %v", err)
	}

	for _, host := range []string{"localhost:8100", "[::1]:8100", "127.0.0.2:8100"} {
		c := minimalConfig()
		c.Server.Listen = host
		if err := c.Validate(); err != nil {
			t.Fatalf("loopback %q: %v", host, err)
		}
	}
}

func TestValidateDatabaseNonEmpty(t *testing.T) {
	c := minimalConfig()
	c.Server.Database = "   "
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "server.database") {
		t.Fatalf("err = %v", err)
	}
}

func TestValidateRetentionRange(t *testing.T) {
	for _, days := range []int{6, 3651, 0} {
		c := minimalConfig()
		c.General.RetentionDays = days
		if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "retention_days") {
			t.Errorf("retention %d: err = %v", days, err)
		}
	}
	for _, days := range []int{7, 3650} {
		c := minimalConfig()
		c.General.RetentionDays = days
		if err := c.Validate(); err != nil {
			t.Errorf("retention %d: %v", days, err)
		}
	}
}

func TestValidateAuthFields(t *testing.T) {
	cases := []struct {
		name    string
		hash    string
		secret  string
		wantErr string
	}{
		{"empty ok", "", "", ""},
		{"valid", testPasswordHash, testSessionSecret, ""},
		{"bad hash", "$argon2id$nope", "", "admin_password_hash"},
		{"wrong algo", "$argon2i$v=19$m=65536,t=3,p=2$c2FsdA$aGFzaA", "", "admin_password_hash"},
		{"secret not base64", "", "not base64!!", "session_secret"},
		{"secret wrong length", "", "c2hvcnQ", "session_secret"},
	}
	for _, tc := range cases {
		c := minimalConfig()
		c.Auth.AdminPasswordHash = tc.hash
		c.Auth.SessionSecret = tc.secret
		err := c.Validate()
		if tc.wantErr == "" {
			if err != nil {
				t.Errorf("%s: %v", tc.name, err)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
			t.Errorf("%s: err = %v", tc.name, err)
		}
	}
}

func TestValidateUpstreamIdentity(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*Upstream)
		wantErr string
	}{
		{"missing id", func(u *Upstream) { u.ID = "" }, "upstreams[0].id"},
		{"bad slug", func(u *Upstream) { u.ID = "DeepSeek" }, "upstreams[0].id"},
		{"long slug", func(u *Upstream) { u.ID = strings.Repeat("a", 33) }, "upstreams[0].id"},
		{"blank name", func(u *Upstream) { u.Name = "  " }, "upstreams[0].name"},
		{"long name", func(u *Upstream) { u.Name = strings.Repeat("名", 101) }, "upstreams[0].name"},
		{"empty base_url", func(u *Upstream) { u.BaseURL = "" }, "upstreams[0].base_url"},
		{"bad scheme", func(u *Upstream) { u.BaseURL = "ftp://api.example.com" }, "upstreams[0].base_url"},
		{"no host", func(u *Upstream) { u.BaseURL = "https://" }, "upstreams[0].base_url"},
	}
	for _, tc := range cases {
		c := minimalConfig()
		u := testUpstream()
		tc.mutate(&u)
		c.Upstreams = []Upstream{u}
		err := c.Validate()
		if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
			t.Errorf("%s: err = %v", tc.name, err)
		}
	}
}

func TestValidateNormalizesBaseURL(t *testing.T) {
	c := minimalConfig()
	u := testUpstream()
	u.BaseURL = "https://api.example.com/v1///"
	c.Upstreams = []Upstream{u}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	if got := c.Upstreams[0].BaseURL; got != "https://api.example.com/v1" {
		t.Fatalf("base_url = %q", got)
	}
}

func TestValidateDuplicateUpstreamID(t *testing.T) {
	c := minimalConfig()
	c.Upstreams = []Upstream{testUpstream(), testUpstream()}
	err := c.Validate()
	if err == nil || !strings.Contains(err.Error(), "upstreams[1].id") {
		t.Fatalf("err = %v", err)
	}
}

func TestValidateGatewayKeys(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*GatewayKey)
		wantErr string
	}{
		{"missing id", func(k *GatewayKey) { k.ID = "" }, "gateway_keys[0].id"},
		{"bad slug", func(k *GatewayKey) { k.ID = "Mac Book" }, "gateway_keys[0].id"},
		{"blank name", func(k *GatewayKey) { k.Name = "" }, "gateway_keys[0].name"},
		{"short key", func(k *GatewayKey) { k.Key = "short" }, "gateway_keys[0].key"},
		{"empty upstream", func(k *GatewayKey) { k.UpstreamID = "" }, "upstream_id"},
		{"missing upstream", func(k *GatewayKey) { k.UpstreamID = "nope" }, "upstream_id"},
	}
	for _, tc := range cases {
		c := minimalConfig()
		c.Upstreams = []Upstream{testUpstream()}
		k := testKey()
		tc.mutate(&k)
		c.GatewayKeys = []GatewayKey{k}
		err := c.Validate()
		if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
			t.Errorf("%s: err = %v", tc.name, err)
		}
	}
}

func TestValidateDuplicateGatewayKeys(t *testing.T) {
	c := minimalConfig()
	c.Upstreams = []Upstream{testUpstream()}

	dupID := testKey()
	dupID.Key = "sk-local-abcdefghijklmnop"
	c.GatewayKeys = []GatewayKey{testKey(), dupID}
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "gateway_keys[1].id") {
		t.Fatalf("duplicate id: err = %v", err)
	}

	dupKey := testKey()
	dupKey.ID = "other"
	dupKey.Name = "Other"
	c.GatewayKeys = []GatewayKey{testKey(), dupKey}
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "gateway_keys[1].key") {
		t.Fatalf("duplicate key: err = %v", err)
	}
}

func TestValidateModelRules(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*Model)
		wantErr string
	}{
		{"empty name", func(m *Model) { m.Name = "" }, "models[0].name"},
		{"blank name", func(m *Model) { m.Name = "  " }, "models[0].name"},
		{"long name", func(m *Model) { m.Name = strings.Repeat("m", 201) }, "models[0].name"},
		{"negative input", func(m *Model) { m.Input = -1 }, "models[0].input"},
		{"nan output", func(m *Model) { m.Output = math.NaN() }, "models[0].output"},
		{"inf cache_read", func(m *Model) { m.CacheRead = math.Inf(1) }, "models[0].cache_read"},
		{"negative cache_write", func(m *Model) { m.CacheWrite = -0.5 }, "models[0].cache_write"},
		{"negative reasoning", func(m *Model) { m.Reasoning = new(-1.0) }, "models[0].reasoning"},
		{"too many windows", func(m *Model) { m.Windows = manyWindows(33) }, "models[0].windows"},
	}
	for _, tc := range cases {
		c := minimalConfig()
		m := testModel()
		tc.mutate(&m)
		c.Upstreams = []Upstream{withModel(m)}
		err := c.Validate()
		if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
			t.Errorf("%s: err = %v", tc.name, err)
		}
	}
}

func TestValidateDuplicateModelName(t *testing.T) {
	c := minimalConfig()
	u := testUpstream()
	u.Models = []Model{testModel(), testModel()}
	c.Upstreams = []Upstream{u}
	err := c.Validate()
	if err == nil || !strings.Contains(err.Error(), "models[1].name") {
		t.Fatalf("err = %v", err)
	}
}

func TestValidateWindows(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*Window)
		wantErr string
	}{
		{"blank label", func(w *Window) { w.Label = "  " }, "windows[0].label"},
		{"empty days", func(w *Window) { w.Days = nil }, "windows[0].days"},
		{"unknown day", func(w *Window) { w.Days = []string{"monday"} }, "windows[0].days"},
		{"duplicate day", func(w *Window) { w.Days = []string{"mon", "mon"} }, "windows[0].days"},
		{"bad start", func(w *Window) { w.Start = "8:00" }, "windows[0].start"},
		{"bad end", func(w *Window) { w.End = "24:00" }, "windows[0].end"},
		{"equal times", func(w *Window) { w.End = w.Start }, "windows[0].end"},
		{"negative window input", func(w *Window) { w.Input = new(-1.0) }, "windows[0].input"},
		{"nan window output", func(w *Window) { w.Output = new(math.NaN()) }, "windows[0].output"},
	}
	for _, tc := range cases {
		c := minimalConfig()
		m := testModel()
		w := testWindow()
		tc.mutate(&w)
		m.Windows = []Window{w}
		c.Upstreams = []Upstream{withModel(m)}
		err := c.Validate()
		if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
			t.Errorf("%s: err = %v", tc.name, err)
		}
	}
}

func TestValidateAllowsOvernightWindow(t *testing.T) {
	c := minimalConfig()
	m := testModel()
	w := testWindow()
	w.Start = "22:00"
	w.End = "06:00"
	m.Windows = []Window{w}
	c.Upstreams = []Upstream{withModel(m)}
	if err := c.Validate(); err != nil {
		t.Fatalf("overnight window: %v", err)
	}
}

func TestValidationErrorMessageIsStable(t *testing.T) {
	c := minimalConfig()
	c.Version = 2
	c.General.Timezone = "Nope/Nope"
	first := c.Validate().Error()
	second := c.Validate().Error()
	if first != second {
		t.Fatalf("unstable message: %q vs %q", first, second)
	}
	if !strings.HasPrefix(first, "config: invalid: ") {
		t.Fatalf("message = %q", first)
	}
}

func minimalConfig() *Config {
	return &Config{
		Version: 1,
		Server:  Server{Listen: "127.0.0.1:8100", Database: "~/.cache/llmmeter/llmmeter.db"},
		General: General{Timezone: "UTC", RetentionDays: 90},
	}
}

func testUpstream() Upstream {
	return Upstream{
		ID:      "deepseek",
		Name:    "DeepSeek",
		BaseURL: "https://api.deepseek.com",
		APIKey:  "sk-upstream",
	}
}

func withModel(m Model) Upstream {
	u := testUpstream()
	u.Models = []Model{m}
	return u
}

func testKey() GatewayKey {
	return GatewayKey{ID: "mac", Name: "Mac", Key: "sk-local-1234567890", UpstreamID: "deepseek"}
}

func testModel() Model {
	return Model{Name: "deepseek-chat", Input: 1, Output: 4, CacheRead: 0.02, CacheWrite: 0}
}

func testWindow() Window {
	return Window{Label: "night", Days: []string{"mon", "tue"}, Start: "00:00", End: "08:00"}
}

func manyWindows(n int) []Window {
	out := make([]Window, n)
	for i := range out {
		out[i] = testWindow()
	}
	return out
}

const testPasswordHash = "$argon2id$v=19$m=65536,t=3,p=2$c2FsdHNhbHRzYWx0c2FsdA$aGFzaGhhc2hoYXNoaGFzaGhhc2hoYXNoaGFzaA"

const testSessionSecret = "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY="

const fullExample = `version: 1

server:
  listen: "127.0.0.1:8100"
  database: "~/.cache/llmmeter/llmmeter.db"

general:
  timezone: "Asia/Shanghai"
  retention_days: 90

auth:
  admin_password_hash: ""
  session_secret: ""

upstreams:
  - id: deepseek
    name: DeepSeek
    base_url: "https://api.deepseek.com/"
    api_key: "sk-upstream"
    enabled: true
    models:
      - name: deepseek-chat
        input: 1.0
        output: 4.0
        cache_read: 0.02
        cache_write: 0.0
        reasoning: 4.0
        windows:
          - label: "夜间"
            days: [mon, tue, wed, thu, fri, sat, sun]
            start: "00:00"
            end: "08:00"
            input: 0.5
            output: 2.0
            cache_read: 0.01
            cache_write: null
            reasoning: null

gateway_keys:
  - id: macbook
    name: "MacBook"
    key: "sk-local-1234567890"
    upstream_id: deepseek
    enabled: false
`
