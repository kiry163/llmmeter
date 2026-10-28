package proxy

import (
	"bytes"

	"github.com/kiry163/llmmeter/internal/usage"
)

const maxLine = 1 << 20

type SSEScanner struct {
	path    string
	onUsage func(usage.Usage)
	buf     []byte
}

func NewSSEScanner(path string, onUsage func(usage.Usage)) *SSEScanner {
	return &SSEScanner{path: path, onUsage: onUsage}
}

func (s *SSEScanner) Write(p []byte) {
	s.buf = append(s.buf, p...)
	for {
		i := bytes.IndexByte(s.buf, '\n')
		if i < 0 {
			if len(s.buf) > maxLine {
				s.buf = s.buf[:0]
			}
			return
		}
		line := s.buf[:i]
		s.buf = s.buf[i+1:]
		s.handleLine(line)
	}
}

func (s *SSEScanner) handleLine(line []byte) {
	for len(line) > 0 && (line[len(line)-1] == '\r' || line[len(line)-1] == ' ') {
		line = line[:len(line)-1]
	}
	const prefix = "data:"
	if len(line) < len(prefix) || string(line[:len(prefix)]) != prefix {
		return
	}
	payload := line[len(prefix):]
	for len(payload) > 0 && payload[0] == ' ' {
		payload = payload[1:]
	}
	if len(payload) == 0 || string(payload) == "[DONE]" {
		return
	}
	if u, ok, _ := usage.FromSSEEvent(s.path, payload); ok {
		s.onUsage(u)
	}
}
