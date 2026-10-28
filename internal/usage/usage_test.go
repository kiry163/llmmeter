package usage

import "testing"

func TestParseChatDeepSeek(t *testing.T) {
	body := []byte(`{"usage":{"prompt_tokens":120,"completion_tokens":30,
		"total_tokens":150,"prompt_cache_hit_tokens":100,
		"prompt_tokens_details":{"cached_tokens":100},
		"completion_tokens_details":{"reasoning_tokens":10}}}`)
	u, ok := ParseChat(body)
	if !ok {
		t.Fatal("expected usage")
	}
	if u.Prompt != 120 || u.Completion != 30 || u.CacheRead != 100 || u.Reasoning != 10 || u.Total != 150 {
		t.Fatalf("got %+v", u)
	}
}

func TestParseChatAcceptsPromptTokensDetailsOnly(t *testing.T) {
	body := []byte(`{"usage":{"prompt_tokens":10,"completion_tokens":2,
		"prompt_tokens_details":{"cached_tokens":4}}}`)
	u, ok := ParseChat(body)
	if !ok || u.CacheRead != 4 || u.Total != 12 {
		t.Fatalf("got %+v ok=%v", u, ok)
	}
}

func TestParseResponses(t *testing.T) {
	body := []byte(`{"type":"response.completed","response":{"model":"deepseek-chat",
		"usage":{"input_tokens":80,"output_tokens":20,"total_tokens":100,
		"input_tokens_details":{"cached_tokens":50},
		"output_tokens_details":{"reasoning_tokens":5}}}}`)
	u, ok, terminal := FromSSEEvent("/v1/responses", body)
	if !ok || !terminal {
		t.Fatalf("got ok=%v terminal=%v", ok, terminal)
	}
	if u.Prompt != 80 || u.Completion != 20 || u.CacheRead != 50 || u.Reasoning != 5 {
		t.Fatalf("got %+v", u)
	}
}

func TestParseChatSSEEvent(t *testing.T) {
	body := []byte(`{"choices":[{"delta":{"content":"hi"}}],"usage":null}`)
	if _, ok, terminal := FromSSEEvent("/v1/chat/completions", body); ok || terminal {
		t.Fatal("null usage must not count")
	}
	body = []byte(`{"choices":[],"usage":{"prompt_tokens":5,"completion_tokens":1}}`)
	u, ok, _ := FromSSEEvent("/v1/chat/completions", body)
	if !ok || u.Prompt != 5 {
		t.Fatalf("got %+v ok=%v", u, ok)
	}
}

func TestParseResponsesTopLevelUsage(t *testing.T) {
	body := []byte(`{"object":"response","model":"deepseek-chat","output":[],
		"usage":{"input_tokens":9,"output_tokens":3,"total_tokens":12,
		"input_tokens_details":{"cached_tokens":4}}}`)
	u, ok := ParseResponses(body)
	if !ok {
		t.Fatal("expected top-level usage")
	}
	if u.Prompt != 9 || u.Completion != 3 || u.CacheRead != 4 || u.Total != 12 {
		t.Fatalf("got %+v", u)
	}
}
