// Package gateway proxies every non-reserved request to the configured
// upstream, counting bytes on both directions, extracting token usage and
// recording one row per proxied request.
package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/kiry163/llmmeter/internal/config"
	"github.com/kiry163/llmmeter/internal/metrics"
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
	// copyBuffer is the size of one proxy read/write chunk.
	copyBuffer = 32 << 10
)

// sharedClient is shared by every Gateway so connections are pooled across
// configuration changes. It deliberately has no Timeout: upstream requests are
// not cancelled by the gateway once started.
var sharedClient = &http.Client{
	Transport: &http.Transport{
		Proxy:               http.ProxyFromEnvironment,
		MaxIdleConns:        256,
		MaxIdleConnsPerHost: 64,
		IdleConnTimeout:     90 * time.Second,
		ForceAttemptHTTP2:   true,
		// Count response body bytes as they arrive on the wire; transparent
		// gzip would hide both the compressed size and the encoding headers.
		DisableCompression: true,
	},
}

// Gateway forwards requests to the configured upstream and records the
// accounting of every proxied request.
type Gateway struct {
	cfg   *config.Store
	store *store.Store
	hub   *metrics.Hub
	now   func() time.Time
}

// New returns a gateway reading configuration from cfg and writing records and
// live observations to st and hub. now is used for every timestamp; nil means
// time.Now.
func New(cfg *config.Store, st *store.Store, hub *metrics.Hub, now func() time.Time) *Gateway {
	if now == nil {
		now = time.Now
	}
	return &Gateway{cfg: cfg, store: st, hub: hub, now: now}
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
	snap := g.cfg.Snapshot()
	if snap == nil || len(snap.Upstreams) == 0 {
		writeError(w, http.StatusServiceUnavailable, "setup_required")
		return
	}
	raw, ok := bearerToken(r.Header.Get("Authorization"))
	if !ok {
		writeError(w, http.StatusUnauthorized, "invalid_api_key")
		return
	}
	// The binding is captured for the whole request, so a rebind only affects
	// requests that start after the configuration changes.
	binding, ok := snap.LookupKey(raw)
	if !ok || !binding.Enabled {
		writeError(w, http.StatusUnauthorized, "invalid_api_key")
		return
	}
	upstream := binding.Upstream
	if upstream == nil || !upstream.Active() {
		writeError(w, http.StatusServiceUnavailable, "upstream_disabled")
		return
	}

	started := g.now()
	class := ClassifyPath(r.URL.Path)
	prep := prepareRequest(r, class)
	inflight := g.hub.Started(class, binding.UpstreamID, prep.model)

	res := &proxyResult{
		model:       prep.model,
		stream:      prep.stream,
		usageStatus: usage.StatusNotApplicable,
	}
	if class != classChat && class != classResponses {
		// Non-parsed paths carry no model, stream or usage information.
		res.model, res.stream = "", false
	}

	target := strings.TrimRight(upstream.BaseURL, "/") + r.URL.Path
	if r.URL.RawQuery != "" {
		target += "?" + r.URL.RawQuery
	}
	// The upstream exchange is detached from the client context and runs to
	// completion. The gateway never cancels a started upstream request.
	ctx := context.WithoutCancel(r.Context())

	cr := &countingReader{r: prep.body}
	outReq, err := http.NewRequestWithContext(ctx, r.Method, target, cr)
	if err != nil {
		res.status = http.StatusBadGateway
		res.usageStatus = usage.StatusError
		res.errText = "invalid upstream URL"
		g.complete(snap, binding, r, inflight, class, started, g.now(), res)
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
	if upstream.APIKey != "" {
		outReq.Header.Set("Authorization", "Bearer "+upstream.APIKey)
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
		g.complete(snap, binding, r, inflight, class, started, g.now(), res)
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
	tracker := trackerForClass(class, res.stream)
	flusher, canFlush := w.(http.Flusher)

	done := make(chan struct{})
	var disconnected atomic.Bool
	defer close(done)
	go func() {
		select {
		case <-r.Context().Done():
			// The client is gone. Keep reading the upstream response to
			// completion so usage and bytes are still accounted for.
			disconnected.Store(true)
		case <-done:
		}
	}()

	buf := make([]byte, copyBuffer)
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			chunk := buf[:n]
			if res.ttft == nil {
				ms := g.now().Sub(started).Milliseconds()
				res.ttft = &ms
			}
			res.respBody += int64(n)
			if tracker != nil {
				tracker.feed(chunk)
			}
			if !disconnected.Load() {
				if _, writeErr := w.Write(chunk); writeErr != nil {
					// Keep reading upstream so usage is still accounted for.
					disconnected.Store(true)
				} else if canFlush {
					flusher.Flush()
				}
			}
			if disconnected.Load() {
				res.clientDisconnected = true
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
	completed := g.now()
	// The watcher may not have been scheduled yet; sample the signal directly
	// so a disconnect observed while the body ended is still recorded.
	if !disconnected.Load() && r.Context().Err() != nil {
		disconnected.Store(true)
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

	g.complete(snap, binding, r, inflight, class, started, completed, res)
}

// complete prices the exchange, enqueues its record and feeds the live metrics.
func (g *Gateway) complete(snap *config.Snapshot, binding config.KeyBinding, r *http.Request, inflight *metrics.Inflight, class string, started, completed time.Time, res *proxyResult) {
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
		UpstreamID:         binding.UpstreamID,
		UpstreamName:       binding.UpstreamName,
		KeyID:              binding.KeyID,
		KeyName:            binding.KeyName,
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
		// Spec 6.4: a request body without a model falls back to the model the
		// upstream reported, so logs, pricing and live byModel all see it.
		if rec.Model == "" {
			rec.Model = rec.UpstreamModel
		}
		q := binding.Table.QuoteForUpstream(binding.UpstreamID, rec.Model, completed, loc, rec.Usage)
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
		UpstreamID:       binding.UpstreamID,
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

// bearerToken extracts the token from an Authorization header. An absent or
// empty token is reported as missing; the server-side hash lookup decides
// whether the token is valid.
func bearerToken(header string) (string, bool) {
	const prefix = "Bearer "
	if !strings.HasPrefix(header, prefix) {
		return "", false
	}
	token := strings.TrimSpace(header[len(prefix):])
	if token == "" {
		return "", false
	}
	return token, true
}

// upstreamErrorText describes a failed upstream exchange without leaking the
// request URL.
func upstreamErrorText(err error) string {
	return "upstream request failed"
}

// writeError writes an OpenAI-shaped error body with the given status code.
func writeError(w http.ResponseWriter, status int, code string) {
	message, kind := errorDetail(code)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]map[string]string{
		"error": {"message": message, "type": kind, "code": code},
	})
}

// errorDetail returns the client-facing message and OpenAI error type for a
// gateway error code.
func errorDetail(code string) (message, kind string) {
	switch code {
	case "invalid_api_key":
		return "invalid gateway key", "invalid_request_error"
	case "setup_required":
		return "gateway is not configured", "server_error"
	case "upstream_disabled":
		return "upstream is disabled", "server_error"
	case "upstream_error":
		return "upstream request failed", "upstream_error"
	}
	return code, "server_error"
}
