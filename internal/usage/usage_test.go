package usage_test

import (
	"testing"

	"github.com/kiry163/llmmeter/internal/usage"
)

func TestParseChatUsage(t *testing.T) {
	raw := []byte(`{"prompt_tokens":120,"completion_tokens":30,"total_tokens":150,
		"prompt_cache_hit_tokens":100,"completion_tokens_details":{"reasoning_tokens":10}}`)
	u, ok := usage.ParseChatUsage(raw)
	if !ok || u.PromptTokens != 120 || u.CompletionTokens != 30 ||
		u.CacheReadTokens != 100 || u.ReasoningTokens != 10 || u.TotalTokens != 150 {
		t.Fatalf("got %+v ok=%v", u, ok)
	}
}

func TestParseChatUsageFallsBackToCachedTokens(t *testing.T) {
	raw := []byte(`{"prompt_tokens":80,"completion_tokens":20,
		"prompt_tokens_details":{"cached_tokens":64}}`)
	u, ok := usage.ParseChatUsage(raw)
	if !ok || u.CacheReadTokens != 64 {
		t.Fatalf("got %+v ok=%v", u, ok)
	}
}

func TestParseResponsesUsage(t *testing.T) {
	raw := []byte(`{"input_tokens":50,"output_tokens":9,"total_tokens":59,
		"input_tokens_details":{"cached_tokens":40},
		"output_tokens_details":{"reasoning_tokens":4}}`)
	u, ok := usage.ParseResponsesUsage(raw)
	if !ok || u.PromptTokens != 50 || u.CompletionTokens != 9 ||
		u.CacheReadTokens != 40 || u.ReasoningTokens != 4 {
		t.Fatalf("got %+v ok=%v", u, ok)
	}
}

func TestParseRejectsNullAndEmpty(t *testing.T) {
	for _, raw := range [][]byte{nil, []byte("null"), []byte(`{}`)} {
		if _, ok := usage.ParseChatUsage(raw); ok {
			t.Fatalf("expected no usage for %q", raw)
		}
	}
}
