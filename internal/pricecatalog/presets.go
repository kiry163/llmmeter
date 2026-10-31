// Package pricecatalog holds the embedded model-price snapshot and the pure
// matching rules that pre-fill prices for a configured upstream. Nothing in
// this package touches the network: the snapshot ships in the binary and is
// refreshed by regenerating this file.
//
// snapshot_date: 2026-10-31
// source_url: https://models.dev/api.json
// license: models.dev data is MIT licensed; rates are collected from the
// provider price pages it aggregates. Prices are indicative only: providers
// change them without notice, so the UI must show the snapshot date and ask
// the user to verify before saving.
//
// Rates are USD per million tokens. Reasoning defaults to the output rate
// unless a provider prices it separately. A zero rate for Ollama and LM Studio
// is intentional: local inference has no per-token price.
package pricecatalog

import (
	"net/url"
	"strconv"
	"strings"
)

const (
	// SnapshotDate is the date the embedded rates were captured.
	SnapshotDate = "2026-10-31"
	// SourceURL is the upstream data source of the snapshot.
	SourceURL = "https://models.dev/api.json"
)

// Preset is one model price suggestion. Model is the name the caller asked
// about, so it can be submitted as an upstream model name unchanged. Provider
// is the detected provider name and is not part of the API payload.
type Preset struct {
	Provider   string  `json:"-"`
	Model      string  `json:"model"`
	Input      float64 `json:"input"`
	Output     float64 `json:"output"`
	CacheRead  float64 `json:"cacheRead"`
	CacheWrite float64 `json:"cacheWrite"`
	Reasoning  float64 `json:"reasoning"`
}

// modelPreset is one entry of the embedded snapshot. Aliases are alternative
// names (for example a versioned suffix or a provider-prefixed name) that map
// onto the same rates.
type modelPreset struct {
	name       string
	aliases    []string
	input      float64
	output     float64
	cacheRead  float64
	cacheWrite float64
	reasoning  float64
}

// providerPreset groups the host rules and model rates of one provider. A
// non-zero localPort only matches loopback hosts listening on that port.
type providerPreset struct {
	name      string
	hosts     []string
	localPort int
	models    []modelPreset
}

// providers is the embedded snapshot. Host matching is exact or by a
// dot-boundary suffix; model matching is exact name or alias, case-insensitive.
var providers = []providerPreset{
	{
		name:  "deepseek",
		hosts: []string{"deepseek.com"},
		models: []modelPreset{
			{name: "deepseek-chat", aliases: []string{"deepseek-v3", "deepseek-v3-0324"}, input: 0.27, output: 1.10, cacheRead: 0.07, reasoning: 1.10},
			{name: "deepseek-reasoner", aliases: []string{"deepseek-r1"}, input: 0.55, output: 2.19, cacheRead: 0.14, reasoning: 2.19},
		},
	},
	{
		name:  "openai",
		hosts: []string{"openai.com"},
		models: []modelPreset{
			{name: "gpt-4o", input: 2.50, output: 10.00, cacheRead: 1.25, reasoning: 10.00},
			{name: "gpt-4o-mini", input: 0.15, output: 0.60, cacheRead: 0.075, reasoning: 0.60},
			{name: "gpt-4.1", input: 2.00, output: 8.00, cacheRead: 0.50, reasoning: 8.00},
			{name: "o3-mini", input: 1.10, output: 4.40, cacheRead: 0.55, reasoning: 4.40},
		},
	},
	{
		name:  "openrouter",
		hosts: []string{"openrouter.ai"},
		models: []modelPreset{
			{name: "openai/gpt-4o-mini", input: 0.15, output: 0.60, cacheRead: 0.075, reasoning: 0.60},
			{name: "anthropic/claude-3.5-sonnet", input: 3.00, output: 15.00, cacheRead: 0.30, cacheWrite: 3.75, reasoning: 15.00},
			{name: "google/gemini-2.0-flash-001", input: 0.10, output: 0.40, cacheRead: 0.025, reasoning: 0.40},
		},
	},
	{
		name:  "moonshot",
		hosts: []string{"moonshot.cn", "moonshot.ai", "kimi.com"},
		models: []modelPreset{
			{name: "moonshot-v1-8k", input: 1.68, output: 1.68, cacheRead: 0.34, reasoning: 1.68},
			{name: "kimi-k2-0711-preview", aliases: []string{"kimi-k2"}, input: 0.60, output: 2.50, cacheRead: 0.15, reasoning: 2.50},
		},
	},
	{
		name:  "zhipu",
		hosts: []string{"bigmodel.cn", "zhipuai.cn"},
		models: []modelPreset{
			{name: "glm-4-plus", input: 0.70, output: 0.70, cacheRead: 0.14, reasoning: 0.70},
			{name: "glm-4.5", input: 0.60, output: 2.20, cacheRead: 0.11, reasoning: 2.20},
			{name: "glm-4.5-air", input: 0.20, output: 1.10, cacheRead: 0.04, reasoning: 1.10},
		},
	},
	{
		name:  "dashscope",
		hosts: []string{"dashscope.aliyuncs.com", "dashscope-intl.aliyuncs.com"},
		models: []modelPreset{
			{name: "qwen-plus", input: 0.40, output: 1.20, cacheRead: 0.08, reasoning: 1.20},
			{name: "qwen-max", input: 1.60, output: 6.40, cacheRead: 0.32, reasoning: 6.40},
			{name: "qwen-turbo", input: 0.05, output: 0.20, cacheRead: 0.01, reasoning: 0.20},
		},
	},
	{
		name:  "siliconflow",
		hosts: []string{"siliconflow.cn", "siliconflow.com"},
		models: []modelPreset{
			{name: "deepseek-ai/DeepSeek-V3", input: 0.27, output: 1.10, cacheRead: 0.07, reasoning: 1.10},
			{name: "deepseek-ai/DeepSeek-R1", input: 0.55, output: 2.19, cacheRead: 0.14, reasoning: 2.19},
			{name: "Qwen/Qwen2.5-72B-Instruct", input: 0.59, output: 0.59, reasoning: 0.59},
		},
	},
	{
		name:  "groq",
		hosts: []string{"groq.com"},
		models: []modelPreset{
			{name: "llama-3.3-70b-versatile", input: 0.59, output: 0.79, reasoning: 0.79},
			{name: "llama-3.1-8b-instant", input: 0.05, output: 0.08, reasoning: 0.08},
		},
	},
	{
		name:  "together",
		hosts: []string{"together.ai", "together.xyz"},
		models: []modelPreset{
			{name: "meta-llama/Llama-3.3-70B-Instruct-Turbo", input: 0.88, output: 0.88, reasoning: 0.88},
			{name: "Qwen/Qwen2.5-72B-Instruct-Turbo", input: 1.20, output: 1.20, reasoning: 1.20},
		},
	},
	{
		name:  "fireworks",
		hosts: []string{"fireworks.ai"},
		models: []modelPreset{
			{name: "accounts/fireworks/models/llama-v3p3-70b-instruct", input: 0.90, output: 0.90, reasoning: 0.90},
			{name: "accounts/fireworks/models/deepseek-v3", input: 0.90, output: 0.90, reasoning: 0.90},
		},
	},
	{
		name:  "xai",
		hosts: []string{"x.ai"},
		models: []modelPreset{
			{name: "grok-3", input: 3.00, output: 15.00, cacheRead: 0.75, reasoning: 15.00},
			{name: "grok-2-1212", input: 2.00, output: 10.00, cacheRead: 0.50, reasoning: 10.00},
		},
	},
	{
		name:  "mistral",
		hosts: []string{"mistral.ai"},
		models: []modelPreset{
			{name: "mistral-large-latest", input: 2.00, output: 6.00, reasoning: 6.00},
			{name: "mistral-small-latest", input: 0.20, output: 0.60, reasoning: 0.60},
		},
	},
	{
		name:  "minimax",
		hosts: []string{"minimaxi.com", "minimax.io", "minimax.chat"},
		models: []modelPreset{
			{name: "MiniMax-M1", input: 0.40, output: 2.20, reasoning: 2.20},
			{name: "MiniMax-Text-01", input: 0.20, output: 1.10, reasoning: 1.10},
			{name: "abab6.5s-chat", input: 0.70, output: 0.70, reasoning: 0.70},
		},
	},
	{
		name:      "ollama",
		localPort: 11434,
		models: []modelPreset{
			{name: "llama3.2", aliases: []string{"llama3.2:latest"}, reasoning: 0},
			{name: "qwen2.5", aliases: []string{"qwen2.5:latest"}, reasoning: 0},
			{name: "deepseek-r1", aliases: []string{"deepseek-r1:latest"}, reasoning: 0},
			{name: "mistral", aliases: []string{"mistral:latest"}, reasoning: 0},
		},
	},
	{
		name:      "lmstudio",
		localPort: 1234,
		models: []modelPreset{
			{name: "qwen2.5-7b-instruct", aliases: []string{"qwen2.5-7b-instruct@q4_k_m"}, reasoning: 0},
			{name: "llama-3.2-3b-instruct", aliases: []string{"llama-3.2-3b-instruct@q4_k_m"}, reasoning: 0},
			{name: "mistral-7b-instruct-v0.3", reasoning: 0},
		},
	},
}

// Provider returns the provider preset name matching the base URL host, or ""
// when the host is unknown. Matching is exact or a dot-boundary suffix, never a
// substring. Loopback hosts match the Ollama and LM Studio default ports.
func Provider(baseURL string) string {
	p, _ := providerFor(baseURL)
	if p == nil {
		return ""
	}
	return p.name
}

// Match returns one preset per recognised model, in the order given. Unknown
// hosts and unrecognised model names are skipped; duplicate names are returned
// once. The Model field echoes the queried name so it can be saved unchanged.
func Match(baseURL string, models []string) []Preset {
	p, _ := providerFor(baseURL)
	out := make([]Preset, 0, len(models))
	if p == nil {
		return out
	}
	seen := make(map[string]bool, len(models))
	for _, raw := range models {
		name := strings.TrimSpace(raw)
		if name == "" {
			continue
		}
		key := strings.ToLower(name)
		if seen[key] {
			continue
		}
		seen[key] = true
		m, ok := p.lookup(key)
		if !ok {
			continue
		}
		out = append(out, Preset{
			Provider:   p.name,
			Model:      name,
			Input:      m.input,
			Output:     m.output,
			CacheRead:  m.cacheRead,
			CacheWrite: m.cacheWrite,
			Reasoning:  m.reasoning,
		})
	}
	return out
}

// lookup finds the model entry whose name or alias equals key, which must
// already be lowercased.
func (p *providerPreset) lookup(key string) (modelPreset, bool) {
	for _, m := range p.models {
		if strings.ToLower(m.name) == key {
			return m, true
		}
		for _, alias := range m.aliases {
			if strings.ToLower(alias) == key {
				return m, true
			}
		}
	}
	return modelPreset{}, false
}

// providerFor resolves the provider preset and the URL port.
func providerFor(baseURL string) (*providerPreset, int) {
	u, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || u.Host == "" {
		return nil, 0
	}
	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	if host == "" {
		return nil, 0
	}
	port := 0
	if raw := u.Port(); raw != "" {
		port, _ = strconv.Atoi(raw)
	} else if u.Scheme == "https" {
		port = 443
	} else {
		port = 80
	}
	loopback := host == "localhost" || host == "127.0.0.1" || host == "::1"
	for i := range providers {
		p := &providers[i]
		if p.localPort != 0 {
			if loopback && port == p.localPort {
				return p, port
			}
			continue
		}
		for _, h := range p.hosts {
			if host == h || strings.HasSuffix(host, "."+h) {
				return p, port
			}
		}
	}
	return nil, port
}
