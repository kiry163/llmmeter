package gateway

import (
	"io"
	"net/http"
	"strconv"
	"sync/atomic"
)

// RequestHeaderBytes estimates the wire size of the request line and headers:
// the request line, every "name: value" pair, and the terminating blank line.
// HTTP/2 compresses headers, so for HTTP/2 this is an estimate only.
func RequestHeaderBytes(r *http.Request) int64 {
	target := r.URL.RequestURI()
	n := int64(len(r.Method)) + 1 + int64(len(target)) + int64(len(" HTTP/1.1\r\n"))
	return n + headerBytes(r.Header) + 2
}

// ResponseHeaderBytes estimates the wire size of the status line and headers.
// HTTP/2 compresses headers, so for HTTP/2 this is an estimate only.
func ResponseHeaderBytes(status int, h http.Header) int64 {
	n := int64(len("HTTP/1.1")) + 1 + int64(len(strconv.Itoa(status))) + 1 +
		int64(len(http.StatusText(status))) + 2
	return n + headerBytes(h) + 2
}

// headerBytes sums every "name: value\r\n" pair.
func headerBytes(h http.Header) int64 {
	var n int64
	for name, values := range h {
		for _, v := range values {
			n += int64(len(name)) + 2 + int64(len(v)) + 2
		}
	}
	return n
}

// countingReader counts the bytes actually read from the wrapped body, i.e.
// the bytes the transport sends upstream.
type countingReader struct {
	r io.Reader
	n atomic.Int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n.Add(int64(n))
	return n, err
}

// Count reports the number of bytes read so far.
func (c *countingReader) Count() int64 { return c.n.Load() }
