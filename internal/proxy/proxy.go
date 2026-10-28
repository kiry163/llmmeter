package proxy

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	"github.com/kiry163/llmmeter/internal/config"
	"github.com/kiry163/llmmeter/internal/pricing"
	"github.com/kiry163/llmmeter/internal/store"
	"github.com/kiry163/llmmeter/internal/usage"
)

const (
	maxParsedBody   = 1 << 20
	maxResponseBody = 10 << 20
	drainTimeout    = 3 * time.Second
	drainBytes      = 2 << 20
)

type Proxy struct {
	cfg     atomic.Pointer[config.Config]
	catalog atomic.Pointer[pricing.Catalog]
	loc     atomic.Pointer[time.Location]
	store   *store.Store
	client  *http.Client
	now     func() time.Time
}

func New(cfg *config.Config, cat *pricing.Catalog, st *store.Store, now func() time.Time) (*Proxy, error) {
	if now == nil {
		now = time.Now
	}
	p := &Proxy{store: st, now: now}
	if err := p.Reload(cfg, cat); err != nil {
		return nil, err
	}
	p.client = &http.Client{Transport: &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		MaxIdleConnsPerHost:   64,
		IdleConnTimeout:       90 * time.Second,
		ResponseHeaderTimeout: 60 * time.Second,
		ForceAttemptHTTP2:     true,
	}}
	return p, nil
}

func (p *Proxy) Reload(cfg *config.Config, cat *pricing.Catalog) error {
	loc, err := time.LoadLocation(cfg.Timezone)
	if err != nil {
		return err
	}
	p.cfg.Store(cfg)
	p.catalog.Store(cat)
	p.loc.Store(loc)
	return nil
}

// FlushForTest blocks until the store has drained its queue.
func (p *Proxy) FlushForTest() error { return p.store.Flush() }

func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	cfg := p.cfg.Load()
	if !validKey(r.Header.Get("Authorization"), cfg.GatewayKey) {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}
	if !isParsedPath(r.URL.Path) {
		p.forwardRaw(w, r, cfg)
		return
	}
	p.forwardParsed(w, r, cfg)
}

func validKey(h, key string) bool {
	token := strings.TrimSpace(strings.TrimPrefix(h, "Bearer "))
	return subtle.ConstantTimeCompare([]byte(token), []byte(key)) == 1
}

func isParsedPath(path string) bool {
	return path == "/v1/chat/completions" || path == "/v1/responses"
}

func (p *Proxy) forwardRaw(w http.ResponseWriter, r *http.Request, cfg *config.Config) {
	out, cancel, err := p.upstreamRequest(r, cfg, r.Body, r.ContentLength)
	if err != nil {
		http.Error(w, `{"error":"bad upstream"}`, http.StatusBadGateway)
		return
	}
	defer cancel()
	resp, err := p.client.Do(out)
	if err != nil {
		http.Error(w, `{"error":"upstream unreachable"}`, http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	copyHeader(w.Header(), resp.Header)
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}

// upstreamRequest builds the upstream request. body and length are explicit so
// callers can pass rewritten bytes, a MultiReader, or -1 for chunked encoding.
func (p *Proxy) upstreamRequest(r *http.Request, cfg *config.Config, body io.Reader, length int64) (*http.Request, context.CancelFunc, error) {
	base, err := url.Parse(cfg.Upstream.BaseURL)
	if err != nil {
		return nil, nil, err
	}
	target := *base
	target.Path = strings.TrimRight(base.Path, "/") + r.URL.Path
	target.RawQuery = r.URL.RawQuery
	// WithoutCancel keeps the upstream call alive after the client disconnects,
	// so usage that the provider already billed can still be read.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), cfg.Upstream.Timeout.Std())
	out, err := http.NewRequestWithContext(ctx, r.Method, target.String(), body)
	if err != nil {
		cancel()
		return nil, nil, err
	}
	copyHeader(out.Header, r.Header)
	out.Header.Del("Accept-Encoding")
	out.Header.Del("Host")
	out.Header.Set("Authorization", "Bearer "+cfg.Upstream.APIKey)
	out.ContentLength = length
	return out, cancel, nil
}

func (p *Proxy) forwardParsed(w http.ResponseWriter, r *http.Request, cfg *config.Config) {
	started := p.now()
	rawBody, err := io.ReadAll(io.LimitReader(r.Body, maxParsedBody+1))
	if err != nil {
		http.Error(w, `{"error":"read body"}`, http.StatusBadRequest)
		return
	}
	parsed := len(rawBody) <= maxParsedBody
	record := store.Record{StartedAt: started, Path: r.URL.Path, UsageStatus: "no_usage"}
	bodyReader := io.Reader(bytes.NewReader(rawBody))
	length := int64(len(rawBody))
	if parsed {
		body := rawBody
		if r.URL.Path == "/v1/chat/completions" {
			if injected, changed := InjectUsage(body); changed {
				body = injected
			}
		}
		bodyReader = bytes.NewReader(body)
		length = int64(len(body))
		record.Model = ParseModel(body)
		record.Stream = isStream(body)
	} else {
		// Preserve the bytes already consumed: forward them, then the rest.
		record.UsageStatus = "too_large"
		bodyReader = io.MultiReader(bytes.NewReader(rawBody), r.Body)
		length = -1
	}

	out, cancel, err := p.upstreamRequest(r, cfg, bodyReader, length)
	if err != nil {
		http.Error(w, `{"error":"bad upstream"}`, http.StatusBadGateway)
		return
	}
	defer cancel()
	resp, err := p.client.Do(out)
	if err != nil {
		http.Error(w, `{"error":"upstream unreachable"}`, http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	record.Status = resp.StatusCode
	copyHeader(w.Header(), resp.Header)
	w.WriteHeader(record.Status)

	var u usage.Usage
	found := false
	contentType := resp.Header.Get("Content-Type")

	switch {
	case strings.Contains(contentType, "text/event-stream"):
		scanner := NewSSEScanner(r.URL.Path, func(v usage.Usage) { u, found = v, true })
		p.streamThrough(w, resp.Body, scanner, &record)
	case strings.Contains(contentType, "json") && parsed:
		buf, tooLarge, bodyErr := readLimited(resp.Body, maxResponseBody)
		if tooLarge {
			record.UsageStatus = "too_large"
			w.Write(buf)
			io.Copy(w, resp.Body)
		} else if bodyErr != nil {
			record.UsageStatus = "parse_failed"
			w.Write(buf)
		} else {
			w.Write(buf)
			if r.URL.Path == "/v1/responses" {
				u, found = usage.ParseResponses(buf)
			} else {
				u, found = usage.ParseChat(buf)
			}
			if m := ParseResponseModel(buf); m != "" {
				record.UpstreamModel = m
			}
		}
	default:
		if _, err := io.Copy(w, resp.Body); err != nil {
			record.UsageStatus = "parse_failed"
		}
	}

	completed := p.now()
	p.finish(&record, u, found, started, completed)
}

func isStream(body []byte) bool {
	var v struct {
		Stream bool `json:"stream"`
	}
	_ = json.Unmarshal(body, &v)
	return v.Stream
}

func readLimited(r io.Reader, limit int64) ([]byte, bool, error) {
	buf, err := io.ReadAll(io.LimitReader(r, limit+1))
	if int64(len(buf)) > limit {
		return buf, true, nil
	}
	return buf, false, err
}

func (p *Proxy) streamThrough(w http.ResponseWriter, body io.ReadCloser, scanner *SSEScanner, rec *store.Record) {
	flusher, _ := w.(http.Flusher)
	buf := make([]byte, 32*1024)
	clientGone := false
	drained := int64(0)
	var drainTimer *time.Timer
	for {
		n, err := body.Read(buf)
		if n > 0 {
			scanner.Write(buf[:n])
			if !clientGone {
				if _, werr := w.Write(buf[:n]); werr != nil {
					clientGone = true
					// The provider already billed this call; keep reading for a
					// bounded window so the usage can still be recorded.
					drainTimer = time.AfterFunc(drainTimeout, func() { body.Close() })
				} else if flusher != nil {
					flusher.Flush()
				}
			} else {
				drained += int64(n)
				if drained > drainBytes {
					if drainTimer != nil {
						drainTimer.Stop()
					}
					return
				}
			}
		}
		if err != nil {
			if drainTimer != nil {
				drainTimer.Stop()
			}
			return
		}
	}
}

func (p *Proxy) finish(rec *store.Record, u usage.Usage, found bool, started, completed time.Time) {
	loc := p.loc.Load()
	rec.CompletedAt = completed
	rec.LocalDay = completed.In(loc).Format("2006-01-02")
	rec.LatencyMS = completed.Sub(started).Milliseconds()
	if found {
		rec.Usage = u
		rec.UsageStatus = "ok"
		cat := p.catalog.Load()
		lookup, ok := cat.Lookup(rec.Model)
		if !ok {
			rec.Priced = false
		} else {
			rates, label, priced := cat.Price(lookup, completed, loc)
			rec.Priced = priced
			rec.WindowLabel = label
			rec.RatesJSON = store.MarshalRates(rates)
			rec.CostNanoUSD = pricing.Cost(rates, u.Prompt, u.Completion, u.CacheRead, u.CacheWrite, u.Reasoning)
		}
	}
	p.store.Enqueue(*rec)
}

func copyHeader(dst, src http.Header) {
	for k, vv := range src {
		for _, v := range vv {
			dst.Add(k, v)
		}
	}
}
