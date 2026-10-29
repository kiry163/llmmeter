package gateway

import (
	"bytes"
	"encoding/json"
	"strings"

	"github.com/kiry163/llmmeter/internal/usage"
)

// maxBuffered is the largest request body, response body or SSE line held in
// memory for usage parsing. Anything bigger is forwarded unchanged and
// reported as too_large.
const maxBuffered = 1 << 20

// lineBuffer splits a byte stream into complete lines while bounding the memory
// held for any single line. A line longer than maxBuffered is dropped and sets
// tooLarge; the rest of that line is discarded through the next newline.
type lineBuffer struct {
	buf      []byte
	tooLarge bool
	skip     bool
}

// Feed appends p and returns each complete line that became available, with a
// trailing carriage return trimmed. Incomplete line remainders are kept until
// their newline arrives.
func (b *lineBuffer) Feed(p []byte) []string {
	b.buf = append(b.buf, p...)
	var lines []string
	for {
		i := bytes.IndexByte(b.buf, '\n')
		if i < 0 {
			if len(b.buf) > maxBuffered {
				b.buf = b.buf[:0]
				b.tooLarge = true
				b.skip = true
			}
			return lines
		}
		line := string(b.buf[:i])
		b.buf = append(b.buf[:0], b.buf[i+1:]...)
		if len(line) > maxBuffered {
			b.tooLarge = true
			continue
		}
		if b.skip {
			// Tail of a line that was already discarded as over-long.
			b.skip = false
			continue
		}
		if n := len(line); n > 0 && line[n-1] == '\r' {
			line = line[:n-1]
		}
		lines = append(lines, line)
	}
}

// extracted is the usage accounting found in one response fragment.
type extracted struct {
	u         usage.Usage
	found     bool
	malformed bool
	model     string
}

// decodeUsage decodes a usage object. found is false only when the fragment
// carried no usage value at all; a value that cannot be decoded is malformed.
func decodeUsage(class string, raw json.RawMessage) extracted {
	if len(raw) == 0 || string(raw) == "null" {
		return extracted{}
	}
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(raw, &probe); err != nil || probe == nil {
		return extracted{malformed: true}
	}
	if !numericObject(raw) {
		return extracted{malformed: true}
	}
	var u usage.Usage
	if class == classResponses {
		u, _ = usage.ParseResponsesUsage(raw)
	} else {
		u, _ = usage.ParseChatUsage(raw)
	}
	return extracted{u: u, found: true}
}

// parseJSONUsage extracts usage and the reported model from a response object.
// For the responses API the usage may be nested in a "response" object, which
// is how SSE completion events carry it.
func parseJSONUsage(class string, body []byte) extracted {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(body, &top); err != nil || top == nil {
		return extracted{}
	}
	out := extracted{}
	out.model = rawString(top["model"])
	raw, ok := top["usage"]
	if !ok && class == classResponses {
		var inner map[string]json.RawMessage
		if r, has := top["response"]; has && json.Unmarshal(r, &inner) == nil {
			raw, ok = inner["usage"]
			if out.model == "" {
				out.model = rawString(inner["model"])
			}
		}
	}
	if !ok {
		return out
	}
	decoded := decodeUsage(class, raw)
	decoded.model = out.model
	return decoded
}

// numericObject reports whether raw is a JSON object whose values are all
// numbers, null, or nested objects of the same shape. A token-count object
// cannot legitimately contain strings, booleans or arrays, so such a payload is
// a malformed usage field rather than an absent one.
func numericObject(raw json.RawMessage) bool {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return false
	}
	for _, v := range fields {
		if !numericValue(v) {
			return false
		}
	}
	return true
}

// numericValue reports whether v is a number, null, or a numeric object.
func numericValue(v json.RawMessage) bool {
	v = bytes.TrimSpace(v)
	if len(v) == 0 {
		return false
	}
	switch v[0] {
	case '{':
		return numericObject(v)
	case '[', '"':
		return false
	}
	if string(v) == "null" {
		return true
	}
	var n json.Number
	return json.Unmarshal(v, &n) == nil
}

// rawString decodes a JSON string value, returning "" for anything else.
func rawString(raw json.RawMessage) string {
	var s string
	if len(raw) == 0 || json.Unmarshal(raw, &s) != nil {
		return ""
	}
	return s
}

// usageTracker accumulates usage from a response as it is proxied. Streams are
// parsed line by line; regular responses are buffered up to maxBuffered.
type usageTracker struct {
	class  string
	stream bool
	lines  lineBuffer
	body   []byte

	tooLarge bool
	result   extracted
}

func newUsageTracker(class string, stream bool) *usageTracker {
	return &usageTracker{class: class, stream: stream}
}

// feed consumes one proxied chunk of the response body.
func (t *usageTracker) feed(p []byte) {
	if t.stream {
		for _, line := range t.lines.Feed(p) {
			t.feedLine(line)
		}
		if t.lines.tooLarge {
			t.tooLarge = true
		}
		return
	}
	if t.tooLarge {
		return
	}
	if len(t.body)+len(p) > maxBuffered {
		t.tooLarge = true
		t.body = nil
		return
	}
	t.body = append(t.body, p...)
}

// feedLine handles one SSE line.
func (t *usageTracker) feedLine(line string) {
	if !strings.HasPrefix(line, "data:") {
		return
	}
	payload := strings.TrimSpace(line[len("data:"):])
	if payload == "" || payload == "[DONE]" {
		return
	}
	got := parseJSONUsage(t.class, []byte(payload))
	if got.model != "" && t.result.model == "" {
		t.result.model = got.model
	}
	if got.found {
		t.result.u, t.result.found = got.u, true
	}
	if got.malformed {
		t.result.malformed = true
	}
}

// finish reports the usage, its parse status and the model reported upstream.
// too_large wins over every parse outcome.
func (t *usageTracker) finish() (usage.Usage, string, string) {
	if t.tooLarge {
		return usage.Usage{}, usage.StatusTooLarge, t.result.model
	}
	if t.stream {
		if t.result.malformed {
			return usage.Usage{}, usage.StatusParseFailed, t.result.model
		}
		if t.result.found {
			return t.result.u, usage.StatusOK, t.result.model
		}
		return usage.Usage{}, usage.StatusNoUsage, t.result.model
	}
	got := parseJSONUsage(t.class, t.body)
	switch {
	case got.malformed:
		return usage.Usage{}, usage.StatusParseFailed, got.model
	case got.found:
		return got.u, usage.StatusOK, got.model
	default:
		return usage.Usage{}, usage.StatusNoUsage, got.model
	}
}
