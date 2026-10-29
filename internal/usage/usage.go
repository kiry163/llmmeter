// Package usage normalizes token accounting payloads returned by the OpenAI
// chat-completions and responses APIs into a single Usage value.
package usage

import "encoding/json"

// Usage is provider-independent token accounting for a single model call.
type Usage struct {
	PromptTokens     int64
	CompletionTokens int64
	CacheReadTokens  int64
	CacheWriteTokens int64
	ReasoningTokens  int64
	TotalTokens      int64
}

// Usage parse status values.
const (
	StatusOK            = "ok"
	StatusNoUsage       = "no_usage"
	StatusParseFailed   = "parse_failed"
	StatusTooLarge      = "too_large"
	StatusNotApplicable = "not_applicable"
	StatusError         = "error"
)

type chatPayload struct {
	PromptTokens         int64  `json:"prompt_tokens"`
	CompletionTokens     int64  `json:"completion_tokens"`
	TotalTokens          int64  `json:"total_tokens"`
	PromptCacheHitTokens *int64 `json:"prompt_cache_hit_tokens"`
	PromptTokensDetails  struct {
		CachedTokens int64 `json:"cached_tokens"`
	} `json:"prompt_tokens_details"`
	CompletionTokensDetails struct {
		ReasoningTokens int64 `json:"reasoning_tokens"`
	} `json:"completion_tokens_details"`
}

// ParseChatUsage extracts usage from a chat-completions usage object. It
// reports false when the payload carries no usage at all.
func ParseChatUsage(raw json.RawMessage) (Usage, bool) {
	if len(raw) == 0 || string(raw) == "null" {
		return Usage{}, false
	}
	var p chatPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return Usage{}, false
	}
	if p.PromptTokens == 0 && p.CompletionTokens == 0 && p.TotalTokens == 0 {
		return Usage{}, false
	}
	cache := p.PromptTokensDetails.CachedTokens
	if p.PromptCacheHitTokens != nil {
		cache = *p.PromptCacheHitTokens
	}
	total := p.TotalTokens
	if total == 0 {
		total = p.PromptTokens + p.CompletionTokens
	}
	return Usage{
		PromptTokens:     p.PromptTokens,
		CompletionTokens: p.CompletionTokens,
		CacheReadTokens:  cache,
		ReasoningTokens:  p.CompletionTokensDetails.ReasoningTokens,
		TotalTokens:      total,
	}, true
}

type responsesPayload struct {
	InputTokens        int64 `json:"input_tokens"`
	OutputTokens       int64 `json:"output_tokens"`
	TotalTokens        int64 `json:"total_tokens"`
	InputTokensDetails struct {
		CachedTokens int64 `json:"cached_tokens"`
	} `json:"input_tokens_details"`
	OutputTokensDetails struct {
		ReasoningTokens int64 `json:"reasoning_tokens"`
	} `json:"output_tokens_details"`
}

// ParseResponsesUsage extracts usage from a responses API usage object. It
// reports false when the payload carries no usage at all.
func ParseResponsesUsage(raw json.RawMessage) (Usage, bool) {
	if len(raw) == 0 || string(raw) == "null" {
		return Usage{}, false
	}
	var p responsesPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return Usage{}, false
	}
	if p.InputTokens == 0 && p.OutputTokens == 0 {
		return Usage{}, false
	}
	total := p.TotalTokens
	if total == 0 {
		total = p.InputTokens + p.OutputTokens
	}
	return Usage{
		PromptTokens:     p.InputTokens,
		CompletionTokens: p.OutputTokens,
		CacheReadTokens:  p.InputTokensDetails.CachedTokens,
		ReasoningTokens:  p.OutputTokensDetails.ReasoningTokens,
		TotalTokens:      total,
	}, true
}
