package pricecatalog_test

import (
	"testing"

	"github.com/kiry163/llmmeter/internal/pricecatalog"
)

func TestSnapshotMetadata(t *testing.T) {
	if pricecatalog.SnapshotDate != "2026-10-31" {
		t.Fatalf("snapshot date = %q", pricecatalog.SnapshotDate)
	}
	if pricecatalog.SourceURL == "" {
		t.Fatal("source URL is empty")
	}
}

func TestPresetMatchDeepSeek(t *testing.T) {
	got := pricecatalog.Match("https://api.deepseek.com", []string{"deepseek-chat"})
	if len(got) != 1 || got[0].Model != "deepseek-chat" {
		t.Fatalf("got = %+v", got)
	}
	if got[0].Provider != "deepseek" {
		t.Fatalf("provider = %q", got[0].Provider)
	}
	if got[0].Input <= 0 || got[0].Output <= 0 || got[0].Reasoning <= 0 {
		t.Fatalf("rates = %+v", got[0])
	}
}

func TestPresetMatchIsCaseInsensitiveAndExact(t *testing.T) {
	got := pricecatalog.Match("https://api.deepseek.com/v1", []string{"DeepSeek-Chat"})
	if len(got) != 1 || got[0].Model != "DeepSeek-Chat" {
		t.Fatalf("case-insensitive match = %+v", got)
	}
	if len(pricecatalog.Match("https://api.deepseek.com", []string{"deepseek-chat-extra"})) != 0 {
		t.Fatal("matched a model that is not an exact name or alias")
	}
}

func TestPresetMatchAlias(t *testing.T) {
	got := pricecatalog.Match("https://api.deepseek.com", []string{"deepseek-v3"})
	if len(got) != 1 || got[0].Model != "deepseek-v3" || got[0].Output <= 0 {
		t.Fatalf("alias = %+v", got)
	}
}

func TestProviderRejectsLookalikeHosts(t *testing.T) {
	for _, raw := range []string{
		"https://notdeepseek.com",
		"https://deepseek.com.evil.example",
		"https://api.openai.com.evil.example",
		"https://example.com",
		"not a url",
	} {
		if got := pricecatalog.Provider(raw); got != "" {
			t.Fatalf("Provider(%q) = %q, want empty", raw, got)
		}
	}
}

func TestProviderCoverage(t *testing.T) {
	cases := []struct {
		url      string
		provider string
		model    string
	}{
		{"https://api.deepseek.com", "deepseek", "deepseek-chat"},
		{"https://api.openai.com/v1", "openai", "gpt-4o-mini"},
		{"https://openrouter.ai/api/v1", "openrouter", "openai/gpt-4o-mini"},
		{"https://api.moonshot.cn/v1", "moonshot", "moonshot-v1-8k"},
		{"https://open.bigmodel.cn/api/paas/v4", "zhipu", "glm-4.5"},
		{"https://dashscope.aliyuncs.com/compatible-mode/v1", "dashscope", "qwen-plus"},
		{"https://api.siliconflow.cn/v1", "siliconflow", "deepseek-ai/DeepSeek-V3"},
		{"https://api.groq.com/openai/v1", "groq", "llama-3.3-70b-versatile"},
		{"https://api.together.xyz/v1", "together", "meta-llama/Llama-3.3-70B-Instruct-Turbo"},
		{"https://api.fireworks.ai/inference/v1", "fireworks", "accounts/fireworks/models/llama-v3p3-70b-instruct"},
		{"https://api.x.ai/v1", "xai", "grok-3"},
		{"https://api.mistral.ai/v1", "mistral", "mistral-large-latest"},
		{"https://api.minimaxi.com/v1", "minimax", "MiniMax-M1"},
		{"http://localhost:11434/v1", "ollama", "llama3.2"},
		{"http://127.0.0.1:1234/v1", "lmstudio", "qwen2.5-7b-instruct"},
	}
	for _, tc := range cases {
		t.Run(tc.provider, func(t *testing.T) {
			if got := pricecatalog.Provider(tc.url); got != tc.provider {
				t.Fatalf("Provider(%q) = %q, want %q", tc.url, got, tc.provider)
			}
			got := pricecatalog.Match(tc.url, []string{tc.model})
			if len(got) != 1 || got[0].Model != tc.model {
				t.Fatalf("Match(%q, %q) = %+v", tc.url, tc.model, got)
			}
		})
	}
}

func TestLocalProvidersAreFree(t *testing.T) {
	for _, raw := range []string{"http://localhost:11434", "http://127.0.0.1:1234"} {
		for _, p := range pricecatalog.Match(raw, []string{"llama3.2", "qwen2.5-7b-instruct", "llama-3.2-3b-instruct"}) {
			if p.Input != 0 || p.Output != 0 || p.CacheRead != 0 || p.CacheWrite != 0 || p.Reasoning != 0 {
				t.Fatalf("%s preset is not free: %+v", raw, p)
			}
		}
	}
	// A loopback host on a non-default port has no provider.
	if got := pricecatalog.Provider("http://localhost:8080"); got != "" {
		t.Fatalf("port 8080 provider = %q", got)
	}
}

func TestMatchUnknownProviderOrModel(t *testing.T) {
	if got := pricecatalog.Match("https://example.com", []string{"gpt-4o"}); len(got) != 0 {
		t.Fatalf("unknown provider = %+v", got)
	}
	if got := pricecatalog.Match("https://api.openai.com", []string{"totally-made-up"}); len(got) != 0 {
		t.Fatalf("unknown model = %+v", got)
	}
	out := pricecatalog.Match("https://example.com", nil)
	if out == nil {
		t.Fatal("Match must return an empty slice, not nil")
	}
}

func TestMatchDeduplicates(t *testing.T) {
	got := pricecatalog.Match("https://api.deepseek.com", []string{"deepseek-chat", "DeepSeek-Chat", " "})
	if len(got) != 1 {
		t.Fatalf("dedupe = %+v", got)
	}
}
