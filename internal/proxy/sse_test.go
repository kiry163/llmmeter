package proxy

import (
	"bytes"
	"testing"

	"github.com/kiry163/llmmeter/internal/usage"
)

func TestSSEScannerHandlesSplitLines(t *testing.T) {
	var got usage.Usage
	s := NewSSEScanner("/v1/chat/completions", func(u usage.Usage) { got = u })

	full := "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":7,\"completion_tokens\":3}}\n\ndata: [DONE]\n\n"
	for i := 0; i < len(full); i += 7 {
		end := i + 7
		if end > len(full) {
			end = len(full)
		}
		s.Write([]byte(full[i:end]))
	}
	if got.Prompt != 7 || got.Completion != 3 {
		t.Fatalf("usage = %+v", got)
	}
}

func TestSSEScannerResponsesTerminal(t *testing.T) {
	var found bool
	s := NewSSEScanner("/v1/responses", func(u usage.Usage) { found = true })
	s.Write([]byte("event: response.completed\n"))
	s.Write([]byte("data: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":5,\"output_tokens\":2}}}\n\n"))
	if !found {
		t.Fatal("expected terminal usage")
	}
}

func TestInjectOnlyWhenMissing(t *testing.T) {
	body := []byte(`{"model":"deepseek-chat","stream":true,"messages":[]}`)
	out, changed := InjectUsage(body)
	if !changed {
		t.Fatal("expected injection")
	}
	if !bytes.Contains(out, []byte(`"include_usage":true`)) {
		t.Fatalf("got %s", out)
	}

	body = []byte(`{"model":"m","stream":true,"stream_options":{"include_usage":false}}`)
	out, changed = InjectUsage(body)
	if changed || !bytes.Equal(out, body) {
		t.Fatal("must not override explicit stream_options")
	}

	body = []byte(`{"model":"m","stream":false}`)
	if _, changed = InjectUsage(body); changed {
		t.Fatal("must not touch non-streaming requests")
	}

	body = []byte(`not json`)
	if out, changed = InjectUsage(body); changed || !bytes.Equal(out, body) {
		t.Fatal("must pass through non-JSON bodies")
	}
}

func TestParseModel(t *testing.T) {
	if m := ParseModel([]byte(`{"model":"deepseek-chat"}`)); m != "deepseek-chat" {
		t.Fatalf("model = %q", m)
	}
	if m := ParseModel([]byte(`{}`)); m != "" {
		t.Fatalf("model = %q", m)
	}
}
