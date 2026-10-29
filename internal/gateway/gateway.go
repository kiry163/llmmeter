// Package gateway proxies every non-reserved request to the configured
// upstream, counting bytes on both directions, extracting token usage and
// recording one row per proxied request.
package gateway

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/kiry163/llmmeter/internal/metrics"
	"github.com/kiry163/llmmeter/internal/settings"
	"github.com/kiry163/llmmeter/internal/store"
	"github.com/kiry163/llmmeter/internal/usage"
)

// Path classes recorded for every proxied request.
const (
	classChat      = "chat"
	classResponses = "responses"
	classModels    = "models"
	classAudio     = "audio"
	classOther     = "other"
)

const (
	// drainWindow bounds how long the gateway keeps reading upstream after the
	// client went away, so accounting can still complete.
	drainWindow = 3 * time.Second
	// drainLimit caps the bytes read for accounting after a client disconnect.
	drainLimit = 2 << 20
	// disconnectSettle is how long a finished stream waits for a late
	// disconnect signal before the record is written. An HTTP client that
	// closes a response body early keeps draining the connection for up to
	// 50ms before the close becomes visible to the server, so a stream that
	// ends right after the client left would otherwise never be flagged.
	disconnectSettle = 100 * time.Millisecond
	// copyBuffer is the size of one proxy read/write chunk.
	copyBuffer = 32 << 10
	// defaultUpstreamTimeout applies when the snapshot carries no timeout.
	defaultUpstreamTimeout = 300 * time.Second
)

// sharedClient is shared by every Gateway so connections are pooled across
// settings changes. Per-request deadlines come from the settings snapshot.
var sharedClient = &http.Client{
	Transport: &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		MaxIdleConns:          256,
		MaxIdleConnsPerHost:   64,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: time.Second,
		ForceAttemptHTTP2:     true,
	},
}

// Gateway forwards requests to the configured upstream and records the
// accounting of every proxied request.
type Gateway struct {
	repo  *settings.Repo
	store *store.Store
	hub   *metrics.Hub
	now   func() time.Time
}

// New returns a gateway reading configuration from repo and writing records and
// live observations to st and hub. now is used for every timestamp; nil means
// time.Now.
func New(repo *settings.Repo, st *store.Store, hub *metrics.Hub, now func() time.Time) *Gateway {
	if now == nil {
		now = time.Now
	}
	return &Gateway{repo: repo, store: st, hub: hub, now: now}
}

// ClassifyPath maps a request path to the class reported in metrics and
// records. Paths that are not recognized fall back to "other".
func ClassifyPath(path string) string {
	switch {
	case strings.HasSuffix(path, "/chat/completions"):
		return classChat
	case strings.HasSuffix(path, "/responses"):
		return classResponses
	case strings.HasSuffix(path, "/models") || strings.Contains(path, "/models/"):
		return classModels
	case strings.Contains(path, "/audio/"):
		return classAudio
	default:
		return classOther
	}
}

// requestPrep is the prepared upstream request body plus what could be learned
// from it.
type requestPrep struct {
	body     io.Reader
	length   int64 // -1 when unknown
	model    string
	stream   bool
	tooLarge bool
}

// proxyResult collects what the proxied exchange produced so the record can be
// assembled in one place.
type proxyResult struct {
	model              string
	upstreamModel      string
	stream             bool
	status             int
	usageStatus        string
	usage              usage.Usage
	reqBody            int64
	reqHeader          int64
	respBody           int64
	respHeader         int64
	clientDisconnected bool
	ttft               *int64
	errText            string
}

// ServeHTTP proxies one request and records its accounting. Requests to a
// non chat/responses path are forwarded without their body being inspected.
func (g *Gateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	snap := g.repo.Snapshot()
	if snap == nil || snap.GatewayKey == "" {
		writeError(w, http.StatusServiceUnavailable, "setup_required")
		return
	}
	if !bearerMatches(r.Header.Get("Authorization"), snap.GatewayKey) {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	started := g.now()
	class := ClassifyPath(r.URL.Path)
	prep := prepareRequest(r, class)
	inflight := g.hub.Started(class, prep.model)

	res := &proxyResult{
		model:       prep.model,
		stream:      prep.stream,
		usageStatus: usage.StatusNotApplicable,
	}
	if class != classChat && class != classResponses {
		// Non-parsed paths carry no model, stream or usage information.
		res.model, res.stream = "", false
	}

	target := strings.TrimRight(snap.Upstream.BaseURL, "/") + r.URL.Path
	if r.URL.RawQuery != "" {
		target += "?" + r.URL.RawQuery
	}
	timeout := snap.Upstream.Timeout
	if timeout <= 0 {
		timeout = defaultUpstreamTimeout
	}
	// The upstream exchange outlives a client disconnect so that the drain
	// window can still capture usage.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), timeout)
	defer cancel()

	cr := &countingReader{r: prep.body}
	outReq, err := http.NewRequestWithContext(ctx, r.Method, target, cr)
	if err != nil {
		res.status = http.StatusBadGateway
		res.usageStatus = usage.StatusError
		res.errText = "invalid upstream URL"
		g.complete(snap, r, inflight, class, started, res)
		writeError(w, http.StatusBadGateway, "upstream_error")
		return
	}
	outReq.Header = r.Header.Clone()
	if outReq.Header == nil {
		outReq.Header = make(http.Header)
	}
	stripHopByHop(outReq.Header)
	outReq.Header.Del("Host")
	outReq.Header.Del("Accept-Encoding")
	if snap.Upstream.APIKey != "" {
		outReq.Header.Set("Authorization", "Bearer "+snap.Upstream.APIKey)
	} else {
		outReq.Header.Del("Authorization")
	}
	outReq.ContentLength = prep.length
	res.reqHeader = RequestHeaderBytes(outReq)

	resp, err := sharedClient.Do(outReq)
	if err != nil {
		res.reqBody = cr.Count()
		res.status = http.StatusBadGateway
		res.usageStatus = usage.StatusError
		res.errText = upstreamErrorText(err)
		g.complete(snap, r, inflight, class, started, res)
		writeError(w, http.StatusBadGateway, "upstream_error")
		return
	}
	defer resp.Body.Close()

	res.status = resp.StatusCode
	respHeader := resp.Header.Clone()
	stripHopByHop(respHeader)
	res.respHeader = ResponseHeaderBytes(resp.StatusCode, respHeader)

	dst := w.Header()
	for name, values := range respHeader {
		for _, v := range values {
			dst.Add(name, v)
		}
	}
	w.WriteHeader(resp.StatusCode)

	if (class == classChat || class == classResponses) &&
		strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "text/event-stream") {
		res.stream = true
	}
	tracker := newUsageTracker(class, res.stream)
	flusher, canFlush := w.(http.Flusher)

	done := make(chan struct{})
	var disconnected atomic.Bool
	defer close(done)
	go func() {
		select {
		case <-r.Context().Done():
			disconnected.Store(true)
			// Unblock a stalled upstream read once the drain window closes.
			time.AfterFunc(drainWindow, func() { resp.Body.Close() })
		case <-done:
		}
	}()

	buf := make([]byte, copyBuffer)
	var drained int64
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			chunk := buf[:n]
			if res.ttft == nil {
				ms := g.now().Sub(started).Milliseconds()
				res.ttft = &ms
			}
			res.respBody += int64(n)
			tracker.feed(chunk)
			if disconnected.Load() {
				drained += int64(n)
			} else if _, writeErr := w.Write(chunk); writeErr != nil {
				// Keep reading upstream so usage is still accounted for.
				disconnected.Store(true)
				time.AfterFunc(drainWindow, func() { resp.Body.Close() })
			} else if canFlush {
				flusher.Flush()
			}
			if disconnected.Load() {
				res.clientDisconnected = true
				if drained >= drainLimit {
					break
				}
			}
		}
		if readErr != nil {
			if !errors.Is(readErr, io.EOF) && !disconnected.Load() && res.errText == "" {
				res.errText = "upstream read failed"
			}
			break
		}
	}
	res.reqBody = cr.Count()
	if res.stream && !disconnected.Load() {
		// Catch a client that left just before the upstream finished.
		settle := time.NewTimer(disconnectSettle)
		select {
		case <-r.Context().Done():
			disconnected.Store(true)
		case <-settle.C:
		}
		settle.Stop()
	}
	res.clientDisconnected = res.clientDisconnected || disconnected.Load()

	if class == classChat || class == classResponses {
		u, status, model := tracker.finish()
		res.usage, res.usageStatus, res.upstreamModel = u, status, model
		if prep.tooLarge {
			// An oversized request leaves usage unknowable; it wins over every
			// response parse outcome.
			res.usageStatus = usage.StatusTooLarge
		}
	}

	g.complete(snap, r, inflight, class, started, res)
}

// complete prices the exchange, enqueues its record and feeds the live metrics.
func (g *Gateway) complete(snap *settings.Snapshot, r *http.Request, inflight *metrics.Inflight, class string, started time.Time, res *proxyResult) {
	completed := g.now()
	loc := snap.Location
	if loc == nil {
		loc = time.UTC
	}
	local := completed.In(loc)

	rec := store.Record{
		StartedAt:          started,
		CompletedAt:        completed,
		LocalDay:           local.Format("2006-01-02"),
		MinuteOfDay:        local.Hour()*60 + local.Minute(),
		Path:               r.URL.Path,
		PathClass:          class,
		Model:              res.model,
		UpstreamModel:      res.upstreamModel,
		Stream:             res.stream,
		Status:             res.status,
		LatencyMS:          completed.Sub(started).Milliseconds(),
		ReqBodyBytes:       res.reqBody,
		ReqHeaderBytes:     res.reqHeader,
		TTFTMS:             res.ttft,
		RespBodyBytes:      res.respBody,
		RespHeaderBytes:    res.respHeader,
		Usage:              res.usage,
		UsageStatus:        res.usageStatus,
		ClientDisconnected: res.clientDisconnected,
		Error:              res.errText,
	}

	if class == classChat || class == classResponses {
		priceModel := rec.Model
		if priceModel == "" {
			priceModel = rec.UpstreamModel
		}
		q := snap.Table.Quote(priceModel, completed, loc, rec.Usage)
		rec.WindowLabel, rec.RatesJSON = q.WindowLabel, q.RatesJSON
		rec.CostNanoUSD, rec.Priced = q.CostNanoUSD, q.Priced
	} else {
		rec.WindowLabel = "base"
		rec.Priced = true
	}

	g.store.EnqueueRecord(rec)
	g.hub.Finished(inflight, metrics.Observation{
		CompletedAt:      completed,
		LocalDay:         rec.LocalDay,
		MinuteOfDay:      rec.MinuteOfDay,
		PathClass:        rec.PathClass,
		Model:            rec.Model,
		Status:           rec.Status,
		UsageStatus:      rec.UsageStatus,
		ReqBytes:         rec.ReqBodyBytes + rec.ReqHeaderBytes,
		RespBytes:        rec.RespBodyBytes + rec.RespHeaderBytes,
		PromptTokens:     rec.Usage.PromptTokens,
		CompletionTokens: rec.Usage.CompletionTokens,
		CacheReadTokens:  rec.Usage.CacheReadTokens,
		ReasoningTokens:  rec.Usage.ReasoningTokens,
		CostNanoUSD:      rec.CostNanoUSD,
		LatencyMS:        rec.LatencyMS,
	})
	slog.Debug("gateway request",
		"method", r.Method,
		"path", r.URL.Path,
		"status", rec.Status,
		"latencyMs", rec.LatencyMS,
		"model", rec.Model,
		"reqBytes", rec.ReqBodyBytes+rec.ReqHeaderBytes,
		"respBytes", rec.RespBodyBytes+rec.RespHeaderBytes,
	)
}

// prepareRequest reads the body of chat and responses requests so the model,
// stream flag and stream_options can be inspected, injecting include_usage for
// chat streams that did not ask for it. Other requests pass through untouched.
func prepareRequest(r *http.Request, class string) *requestPrep {
	p := &requestPrep{body: r.Body, length: r.ContentLength}
	if class != classChat && class != classResponses {
		return p
	}
	if r.Body == nil {
		p.body = http.NoBody
		p.length = 0
		return p
	}

	buf, err := io.ReadAll(io.LimitReader(r.Body, maxBuffered+1))
	if err != nil {
		// The body could not be read; forward what is left unchanged.
		p.body = io.MultiReader(bytes.NewReader(buf), r.Body)
		p.length = -1
		return p
	}
	if len(buf) > maxBuffered {
		p.tooLarge = true
		p.body = io.MultiReader(bytes.NewReader(buf), r.Body)
		return p
	}
	p.length = int64(len(buf))
	p.body = bytes.NewReader(buf)

	var payload struct {
		Model         string          `json:"model"`
		Stream        bool            `json:"stream"`
		StreamOptions json.RawMessage `json:"stream_options"`
	}
	if err := json.Unmarshal(buf, &payload); err != nil {
		// Not JSON: forward unchanged and do not inject anything.
		return p
	}
	p.model, p.stream = payload.Model, payload.Stream
	if class == classChat && payload.Stream && len(payload.StreamOptions) == 0 {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(buf, &fields); err == nil {
			fields["stream_options"] = json.RawMessage(`{"include_usage":true}`)
			var injected bytes.Buffer
			enc := json.NewEncoder(&injected)
			enc.SetEscapeHTML(false)
			if err := enc.Encode(fields); err == nil {
				body := bytes.TrimSuffix(injected.Bytes(), []byte("\n"))
				p.body = bytes.NewReader(body)
				p.length = int64(len(body))
			}
		}
	}
	return p
}

// hopByHopHeaders are never forwarded in either direction.
var hopByHopHeaders = []string{
	"Connection", "Proxy-Connection", "Keep-Alive", "Te",
	"Trailer", "Transfer-Encoding", "Upgrade",
}

// stripHopByHop removes the hop-by-hop headers and every header named by
// Connection from h.
func stripHopByHop(h http.Header) {
	for _, v := range h.Values("Connection") {
		for _, name := range strings.Split(v, ",") {
			if name = strings.TrimSpace(name); name != "" {
				h.Del(name)
			}
		}
	}
	for _, name := range hopByHopHeaders {
		h.Del(name)
	}
}

// bearerMatches reports whether the Authorization header carries key, compared
// in constant time.
func bearerMatches(header, key string) bool {
	const prefix = "Bearer "
	if !strings.HasPrefix(header, prefix) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(header[len(prefix):]), []byte(key)) == 1
}

// upstreamErrorText describes a failed upstream exchange without leaking the
// request URL.
func upstreamErrorText(err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return "upstream timeout"
	}
	return "upstream request failed"
}

// writeError writes a JSON error body with the given status code.
func writeError(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	io.WriteString(w, `{"error":"`+code+`"}`)
}
