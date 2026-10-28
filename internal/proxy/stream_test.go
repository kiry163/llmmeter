package proxy

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestResponsesStreamRecordsUsage(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "event: response.output_text.delta\n")
		io.WriteString(w, `data: {"type":"response.output_text.delta","delta":"hi"}`+"\n\n")
		io.WriteString(w, "event: response.completed\n")
		io.WriteString(w, `data: {"type":"response.completed","response":{"model":"deepseek-chat","usage":{"input_tokens":20,"output_tokens":10}}}`+"\n\n")
	}))
	defer up.Close()

	p, st := newProxy(t, up.URL)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/responses",
		strings.NewReader(`{"model":"deepseek-chat","stream":true,"input":"hi"}`))
	req.Header.Set("Authorization", "Bearer gw-key")
	p.ServeHTTP(rr, req)
	if err := p.FlushForTest(); err != nil {
		t.Fatal(err)
	}

	rows, _ := st.Requests("2026-10-28", "2026-10-28", "", 10, 0)
	if len(rows) != 1 || rows[0].Usage.Prompt != 20 || rows[0].UsageStatus != "ok" {
		t.Fatalf("rows = %+v", rows)
	}
}

func TestDisconnectStillRecords(t *testing.T) {
	release := make(chan struct{})
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, `data: {"choices":[{"delta":{"content":"a"}}]}`+"\n\n")
		w.(http.Flusher).Flush()
		<-release
		io.WriteString(w, `data: {"choices":[],"usage":{"prompt_tokens":4,"completion_tokens":2}}`+"\n\n")
	}))
	defer up.Close()

	p, st := newProxy(t, up.URL)
	srv := httptest.NewServer(p)
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/chat/completions",
		strings.NewReader(`{"model":"deepseek-chat","stream":true,"messages":[]}`))
	req.Header.Set("Authorization", "Bearer gw-key")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 10)
	resp.Body.Read(buf)
	resp.Body.Close() // simulate client disconnect

	close(release)
	time.Sleep(400 * time.Millisecond)
	if err := p.FlushForTest(); err != nil {
		t.Fatal(err)
	}
	rows, _ := st.Requests("2026-10-28", "2026-10-28", "", 10, 0)
	if len(rows) != 1 || rows[0].UsageStatus != "ok" {
		t.Fatalf("rows = %+v", rows)
	}
}
