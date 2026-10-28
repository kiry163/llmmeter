package usage

import "encoding/json"

type Usage struct {
	Prompt     int64 `json:"prompt_tokens"`
	Completion int64 `json:"completion_tokens"`
	CacheRead  int64 `json:"cache_read_tokens"`
	CacheWrite int64 `json:"cache_write_tokens"`
	Reasoning  int64 `json:"reasoning_tokens"`
	Total      int64 `json:"total_tokens"`
}

type chatUsage struct {
	PromptTokens        int64 `json:"prompt_tokens"`
	CompletionTokens    int64 `json:"completion_tokens"`
	TotalTokens         int64 `json:"total_tokens"`
	PromptCacheHit      int64 `json:"prompt_cache_hit_tokens"`
	PromptTokensDetails *struct {
		CachedTokens int64 `json:"cached_tokens"`
	} `json:"prompt_tokens_details"`
	CompletionTokensDetails *struct {
		ReasoningTokens int64 `json:"reasoning_tokens"`
	} `json:"completion_tokens_details"`
}

type chatEnvelope struct {
	Usage *chatUsage `json:"usage"`
}

type responsesEnvelope struct {
	Type     string `json:"type"`
	Response *struct {
		Usage *struct {
			InputTokens        int64 `json:"input_tokens"`
			OutputTokens       int64 `json:"output_tokens"`
			TotalTokens        int64 `json:"total_tokens"`
			InputTokensDetails *struct {
				CachedTokens int64 `json:"cached_tokens"`
			} `json:"input_tokens_details"`
			OutputTokensDetails *struct {
				ReasoningTokens int64 `json:"reasoning_tokens"`
			} `json:"output_tokens_details"`
		} `json:"usage"`
	} `json:"response"`
}

func (c chatUsage) normalize() Usage {
	u := Usage{
		Prompt: c.PromptTokens, Completion: c.CompletionTokens,
		Total: c.TotalTokens, CacheRead: c.PromptCacheHit,
	}
	if u.CacheRead == 0 && c.PromptTokensDetails != nil {
		u.CacheRead = c.PromptTokensDetails.CachedTokens
	}
	if c.CompletionTokensDetails != nil {
		u.Reasoning = c.CompletionTokensDetails.ReasoningTokens
	}
	if u.Total == 0 {
		u.Total = u.Prompt + u.Completion
	}
	return u
}

// ParseChat parses a non-streaming chat completion body.
func ParseChat(body []byte) (Usage, bool) {
	var env chatEnvelope
	if err := json.Unmarshal(body, &env); err != nil || env.Usage == nil {
		return Usage{}, false
	}
	if env.Usage.PromptTokens == 0 && env.Usage.CompletionTokens == 0 {
		return Usage{}, false
	}
	return env.Usage.normalize(), true
}

// ParseResponses parses a non-streaming Responses API body.
func ParseResponses(body []byte) (Usage, bool) {
	var env responsesEnvelope
	if err := json.Unmarshal(body, &env); err != nil || env.Response == nil || env.Response.Usage == nil {
		return Usage{}, false
	}
	r := env.Response.Usage
	u := Usage{Prompt: r.InputTokens, Completion: r.OutputTokens, Total: r.TotalTokens}
	if r.InputTokensDetails != nil {
		u.CacheRead = r.InputTokensDetails.CachedTokens
	}
	if r.OutputTokensDetails != nil {
		u.Reasoning = r.OutputTokensDetails.ReasoningTokens
	}
	if u.Total == 0 {
		u.Total = u.Prompt + u.Completion
	}
	return u, true
}

// FromSSEEvent parses one SSE data payload for the given request path.
// isTerminal reports that the stream reached its final event.
func FromSSEEvent(path string, data []byte) (u Usage, found bool, terminal bool) {
	if len(data) == 0 {
		return Usage{}, false, false
	}
	if path == "/v1/responses" {
		var env responsesEnvelope
		if json.Unmarshal(data, &env) != nil {
			return Usage{}, false, false
		}
		if env.Type != "response.completed" || env.Response == nil || env.Response.Usage == nil {
			return Usage{}, false, false
		}
		r := env.Response.Usage
		u = Usage{Prompt: r.InputTokens, Completion: r.OutputTokens, Total: r.TotalTokens}
		if r.InputTokensDetails != nil {
			u.CacheRead = r.InputTokensDetails.CachedTokens
		}
		if r.OutputTokensDetails != nil {
			u.Reasoning = r.OutputTokensDetails.ReasoningTokens
		}
		if u.Total == 0 {
			u.Total = u.Prompt + u.Completion
		}
		return u, true, true
	}
	var env chatEnvelope
	if json.Unmarshal(data, &env) != nil || env.Usage == nil {
		return Usage{}, false, false
	}
	if env.Usage.PromptTokens == 0 && env.Usage.CompletionTokens == 0 {
		return Usage{}, false, false
	}
	return env.Usage.normalize(), true, false
}
