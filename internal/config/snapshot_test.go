package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kiry163/llmmeter/internal/usage"
)

func TestBuildIndexesKeysAndPerUpstreamTables(t *testing.T) {
	c := minimalConfig()
	c.Upstreams = []Upstream{
		{ID: "a", Name: "A", BaseURL: "https://a.example", Enabled: new(true), Models: []Model{{Name: "m", Input: 1, Output: 2}}},
		{ID: "b", Name: "B", BaseURL: "https://b.example", Enabled: new(true), Models: []Model{{Name: "m", Input: 3, Output: 4}}},
	}
	c.GatewayKeys = []GatewayKey{
		{ID: "ka", Name: "KA", Key: "sk-local-aaaaaaaaaaaa", UpstreamID: "a", Enabled: new(true)},
		{ID: "kb", Name: "KB", Key: "sk-local-bbbbbbbbbbbb", UpstreamID: "b", Enabled: new(true)},
	}
	snap, err := Build(c)
	if err != nil {
		t.Fatal(err)
	}
	ka, ok := snap.LookupKey("sk-local-aaaaaaaaaaaa")
	if !ok || ka.UpstreamID != "a" {
		t.Fatalf("ka = %+v", ka)
	}
	if snap.Tables["a"] == nil || snap.Tables["b"] == nil {
		t.Fatal("missing tables")
	}
	qa := snap.Tables["a"].Quote("m", time.Now(), time.UTC, usage.Usage{PromptTokens: 1_000_000})
	qb := snap.Tables["b"].Quote("m", time.Now(), time.UTC, usage.Usage{PromptTokens: 1_000_000})
	if qa.Rates.Input != 1 || qb.Rates.Input != 3 {
		t.Fatalf("rates: %v %v", qa.Rates.Input, qb.Rates.Input)
	}
	if ka.KeyName != "KA" || ka.UpstreamName != "A" || ka.Upstream == nil || ka.Upstream.ID != "a" || ka.Table != snap.Tables["a"] {
		t.Fatalf("binding = %+v", ka)
	}
	if !ka.Enabled {
		t.Fatal("binding must be enabled")
	}
}

func TestBuildLookupKeyUnknown(t *testing.T) {
	snap, err := Build(minimalConfig())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := snap.LookupKey("sk-local-missing"); ok {
		t.Fatal("unknown key must not resolve")
	}
}

func TestBuildKeepsDisabledBinding(t *testing.T) {
	c := minimalConfig()
	c.Upstreams = []Upstream{
		{ID: "a", Name: "A", BaseURL: "https://a.example", Enabled: new(false)},
	}
	c.GatewayKeys = []GatewayKey{
		{ID: "ka", Name: "KA", Key: "sk-local-aaaaaaaaaaaa", UpstreamID: "a", Enabled: new(false)},
	}
	snap, err := Build(c)
	if err != nil {
		t.Fatal(err)
	}
	b, ok := snap.LookupKey("sk-local-aaaaaaaaaaaa")
	if !ok {
		t.Fatal("disabled key must still be indexed")
	}
	if b.Enabled {
		t.Fatal("disabled key binding must report Enabled=false")
	}
	if _, ok := snap.Upstream("a"); !ok {
		t.Fatal("disabled upstream must still be indexed")
	}
}

func TestBuildConvertsWindowDaysAndClocks(t *testing.T) {
	c := minimalConfig()
	override := 5.0
	reasoning := 7.0
	c.Upstreams = []Upstream{{
		ID: "a", Name: "A", BaseURL: "https://a.example",
		Models: []Model{{
			Name: "m", Input: 10, Output: 20, Reasoning: &reasoning,
			Windows: []Window{
				{Label: "night", Days: []string{"mon"}, Start: "22:00", End: "23:30", Input: &override},
				{Label: "late", Days: []string{"mon"}, Start: "23:45", End: "01:15"},
			},
		}},
	}}
	snap, err := Build(c)
	if err != nil {
		t.Fatal(err)
	}
	table := snap.Tables["a"]
	monday := time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC) // Monday.
	cases := []struct {
		name  string
		at    time.Time
		label string
		input float64
	}{
		{"before window", monday.Add(21*time.Hour + 59*time.Minute), "base", 10},
		{"inside window", monday.Add(22*time.Hour + 30*time.Minute), "night", 5},
		{"at window end", monday.Add(23*time.Hour + 30*time.Minute), "base", 10},
		{"wrong day", monday.Add(24*time.Hour + 22*time.Hour + 30*time.Minute), "base", 10},
		{"overnight after midnight", monday.Add(24*time.Hour + 30*time.Minute), "late", 10},
	}
	for _, tc := range cases {
		q := table.Quote("m", tc.at, time.UTC, usage.Usage{PromptTokens: 1})
		if q.WindowLabel != tc.label || q.Rates.Input != tc.input {
			t.Fatalf("%s: label=%q input=%v, want %q %v", tc.name, q.WindowLabel, q.Rates.Input, tc.label, tc.input)
		}
	}
	base := table.Quote("m", monday, time.UTC, usage.Usage{ReasoningTokens: 1})
	if base.Rates.Reasoning != reasoning {
		t.Fatalf("reasoning = %v, want %v", base.Rates.Reasoning, reasoning)
	}
}

func TestBuildLocation(t *testing.T) {
	utcSnap, err := Build(minimalConfig())
	if err != nil {
		t.Fatal(err)
	}
	if utcSnap.Location != time.UTC {
		t.Fatalf("empty timezone location = %v", utcSnap.Location)
	}
	c := minimalConfig()
	c.General.Timezone = "Asia/Shanghai"
	snap, err := Build(c)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Location.String() != "Asia/Shanghai" {
		t.Fatalf("location = %v", snap.Location)
	}
}

func TestBuildRejectsInvalidConfig(t *testing.T) {
	c := minimalConfig()
	c.Version = 2
	if _, err := Build(c); err == nil {
		t.Fatal("expected validation error")
	}
	if _, err := Build(nil); err == nil {
		t.Fatal("expected error for nil config")
	}
}

func TestStoreSnapshotFollowsUpdates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	st, err := OpenStore(path, WithEffectiveDatabase("/tmp/llmmeter.db"))
	if err != nil {
		t.Fatal(err)
	}
	snap := st.Snapshot()
	if snap == nil {
		t.Fatal("missing initial snapshot")
	}
	if _, ok := snap.LookupKey("sk-local-aaaaaaaaaaaa"); ok {
		t.Fatal("empty store must not resolve keys")
	}
	if snap.Config.Server.Database != "/tmp/llmmeter.db" {
		t.Fatalf("database = %q", snap.Config.Server.Database)
	}

	if err := st.Update(func(c *Config) error {
		c.Upstreams = []Upstream{{
			ID: "a", Name: "A", BaseURL: "https://a.example",
			Models: []Model{{Name: "m", Input: 2, Output: 4}},
		}}
		c.GatewayKeys = []GatewayKey{{ID: "ka", Name: "KA", Key: "sk-local-aaaaaaaaaaaa", UpstreamID: "a"}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	snap = st.Snapshot()
	b, ok := snap.LookupKey("sk-local-aaaaaaaaaaaa")
	if !ok || b.UpstreamID != "a" {
		t.Fatalf("binding = %+v ok=%v", b, ok)
	}
	if q := b.Table.Quote("m", time.Now(), time.UTC, usage.Usage{}); q.Rates.Input != 2 {
		t.Fatalf("input rate = %v", q.Rates.Input)
	}

	if err := st.Update(func(c *Config) error {
		c.Version = 2
		return nil
	}); err == nil {
		t.Fatal("expected validation error")
	}
	if st.Snapshot() != snap {
		t.Fatal("failed update must keep the previous snapshot")
	}
}

func TestStoreReloadRebuildsSnapshot(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	st, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	c := minimalConfig()
	c.Upstreams = []Upstream{{
		ID: "a", Name: "A", BaseURL: "https://a.example",
		Models: []Model{{Name: "m", Input: 2, Output: 4}},
	}}
	c.GatewayKeys = []GatewayKey{{ID: "ka", Name: "KA", Key: "sk-local-aaaaaaaaaaaa", UpstreamID: "a"}}
	data, err := marshalConfig(c)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := st.Reload(); err != nil {
		t.Fatal(err)
	}
	if _, ok := st.Snapshot().LookupKey("sk-local-aaaaaaaaaaaa"); !ok {
		t.Fatal("reload must rebuild the key index")
	}
	if st.Snapshot().Tables["a"] == nil {
		t.Fatal("reload must rebuild price tables")
	}
}
