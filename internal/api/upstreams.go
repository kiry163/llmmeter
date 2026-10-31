package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/kiry163/llmmeter/internal/config"
)

const (
	// upstreamProbeTimeout bounds one upstream models listing, including the
	// fallback path attempt.
	upstreamProbeTimeout = 5 * time.Second

	// maxUpstreamNameLen mirrors the configuration rule for upstream and key
	// names; the API rejects longer input before the store validates it.
	maxUpstreamNameLen = 100

	// codeUpstreamInUse is the error code of a blocked upstream deletion.
	codeUpstreamInUse = "upstream_in_use"
)

var (
	// errUpstreamNotFound reports an unknown upstream ID.
	errUpstreamNotFound = errors.New("api: upstream not found")
	// errUpstreamInUse reports that gateway keys still reference the upstream.
	errUpstreamInUse = errors.New("api: upstream in use")
)

// upstreamJSON is one upstream list entry. It never carries the API key.
type upstreamJSON struct {
	ID           string  `json:"id"`
	Name         string  `json:"name"`
	BaseURL      string  `json:"baseUrl"`
	APIKeyMasked string  `json:"apiKeyMasked"`
	Enabled      bool    `json:"enabled"`
	LastUsedAt   *string `json:"lastUsedAt"`
	ModelCount   int     `json:"modelCount"`
	KeyCount     int     `json:"keyCount"`
}

// upstreamsResponse is the GET /api/upstreams body.
type upstreamsResponse struct {
	Upstreams []upstreamJSON `json:"upstreams"`
}

// upstreamCreateRequest is the POST /api/upstreams body.
type upstreamCreateRequest struct {
	Name    string `json:"name"`
	BaseURL string `json:"baseUrl"`
	APIKey  string `json:"apiKey"`
	Enabled *bool  `json:"enabled"`
}

// upstreamPatchRequest is the PATCH /api/upstreams/{id} body. Absent fields
// stay unchanged.
type upstreamPatchRequest struct {
	Name    *string `json:"name"`
	BaseURL *string `json:"baseUrl"`
	APIKey  *string `json:"apiKey"`
	Enabled *bool   `json:"enabled"`
}

// upstreamSecretResponse reveals the stored upstream API key.
type upstreamSecretResponse struct {
	APIKey string `json:"apiKey"`
}

// maskSecret keeps the recognizable prefix and the last four characters of a
// secret. Secrets too short to mask keep only their prefix, so masking never
// reveals the whole value.
func maskSecret(secret string) string {
	if secret == "" {
		return ""
	}
	prefix := ""
	switch {
	case strings.HasPrefix(secret, "sk-local-"):
		prefix = "sk-local-"
	case strings.HasPrefix(secret, "sk-"):
		prefix = "sk-"
	case len(secret) > 8:
		prefix = secret[:4]
	default:
		return "…"
	}
	rest := secret[len(prefix):]
	if len(rest) <= 4 {
		return prefix + "…"
	}
	return prefix + rest[:4] + "…" + rest[len(rest)-4:]
}

// lastUsedTimes returns the most recent request start time per distinct value
// of column, formatted as RFC 3339. column is one of the requests table's
// internal ID columns (upstream_id or key_id), never user input.
func (s *Server) lastUsedTimes(column string) map[string]*string {
	if s.store == nil {
		return nil
	}
	rows, err := s.store.DB().Query(
		`SELECT ` + column + `, MAX(started_at) FROM requests GROUP BY ` + column)
	if err != nil {
		return nil
	}
	defer rows.Close()
	out := make(map[string]*string)
	for rows.Next() {
		var id, raw string
		if err := rows.Scan(&id, &raw); err != nil {
			return out
		}
		if id == "" || raw == "" {
			continue
		}
		t, err := time.Parse(time.RFC3339Nano, raw)
		if err != nil {
			continue
		}
		formatted := t.UTC().Format(time.RFC3339)
		out[id] = &formatted
	}
	return out
}

// upstreamJSONFor assembles one list entry from the configuration upstream and
// the request-derived last-use time.
func upstreamJSONFor(u config.Upstream, keyCount int, lastUsed *string) upstreamJSON {
	return upstreamJSON{
		ID:           u.ID,
		Name:         u.Name,
		BaseURL:      u.BaseURL,
		APIKeyMasked: maskSecret(u.APIKey),
		Enabled:      u.Active(),
		LastUsedAt:   lastUsed,
		ModelCount:   len(u.Models),
		KeyCount:     keyCount,
	}
}

// keyCountsByUpstream counts the gateway keys bound to each upstream.
func keyCountsByUpstream(c *config.Config) map[string]int {
	counts := make(map[string]int, len(c.GatewayKeys))
	for _, k := range c.GatewayKeys {
		counts[k.UpstreamID]++
	}
	return counts
}

// upstreamEntry returns the current list entry of one upstream.
func (s *Server) upstreamEntry(id string) (upstreamJSON, bool) {
	snap := s.configSnapshot()
	if snap == nil {
		return upstreamJSON{}, false
	}
	lastUsed := s.lastUsedTimes("upstream_id")
	counts := keyCountsByUpstream(snap.Config)
	for _, u := range snap.Config.Upstreams {
		if u.ID == id {
			return upstreamJSONFor(u, counts[u.ID], lastUsed[u.ID]), true
		}
	}
	return upstreamJSON{}, false
}

// listUpstreams answers GET /api/upstreams.
func (s *Server) listUpstreams(w http.ResponseWriter, _ *http.Request) {
	snap := s.configSnapshot()
	out := upstreamsResponse{Upstreams: []upstreamJSON{}}
	if snap == nil {
		writeJSON(w, http.StatusOK, out)
		return
	}
	lastUsed := s.lastUsedTimes("upstream_id")
	counts := keyCountsByUpstream(snap.Config)
	out.Upstreams = make([]upstreamJSON, 0, len(snap.Config.Upstreams))
	for _, u := range snap.Config.Upstreams {
		out.Upstreams = append(out.Upstreams, upstreamJSONFor(u, counts[u.ID], lastUsed[u.ID]))
	}
	writeJSON(w, http.StatusOK, out)
}

// upstreamInputFields validates the API-name fields of an upstream write.
func upstreamInputFields(name, baseURL string) map[string]string {
	fields := map[string]string{}
	switch {
	case strings.TrimSpace(name) == "":
		fields["name"] = "required"
	case utf8.RuneCountInString(strings.TrimSpace(name)) > maxUpstreamNameLen:
		fields["name"] = fmt.Sprintf("must be at most %d characters", maxUpstreamNameLen)
	}
	if !validSetupURL(strings.TrimSpace(baseURL)) {
		fields["baseUrl"] = "must be an http or https URL with a host"
	}
	return fields
}

// createUpstream answers POST /api/upstreams. The ID is derived from the name
// and made unique by appending -2, -3, ...
func (s *Server) createUpstream(w http.ResponseWriter, r *http.Request) {
	if !s.requireConfigSetup(w) {
		return
	}
	var in upstreamCreateRequest
	if !decodeJSON(w, r, &in) {
		return
	}
	name := strings.TrimSpace(in.Name)
	baseURL := strings.TrimSpace(in.BaseURL)
	if fields := upstreamInputFields(name, baseURL); len(fields) > 0 {
		writeAPIErrorFields(w, http.StatusBadRequest, codeValidation, "invalid upstream", fields)
		return
	}
	enabled := true
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	var id string
	err := s.configStore.Update(func(c *config.Config) error {
		id = uniqueSlug(slugify(name, "upstream"), func(cand string) bool {
			return upstreamIDTaken(c, cand)
		})
		c.Upstreams = append(c.Upstreams, config.Upstream{
			ID:      id,
			Name:    name,
			BaseURL: baseURL,
			APIKey:  strings.TrimSpace(in.APIKey),
			Enabled: &enabled,
		})
		return nil
	})
	if err != nil {
		writeConfigWriteError(w, err)
		return
	}
	s.catalog.invalidate()
	entry, _ := s.upstreamEntry(id)
	writeJSON(w, http.StatusCreated, entry)
}

// patchUpstream answers PATCH /api/upstreams/{id}. Changing the endpoint or
// the API key drops the cached model catalog of the instance.
func (s *Server) patchUpstream(w http.ResponseWriter, r *http.Request) {
	if !s.requireConfigSetup(w) {
		return
	}
	id := r.PathValue("id")
	var in upstreamPatchRequest
	if !decodeJSON(w, r, &in) {
		return
	}
	fields := map[string]string{}
	if in.Name != nil {
		name := strings.TrimSpace(*in.Name)
		switch {
		case name == "":
			fields["name"] = "required"
		case utf8.RuneCountInString(name) > maxUpstreamNameLen:
			fields["name"] = fmt.Sprintf("must be at most %d characters", maxUpstreamNameLen)
		}
	}
	if in.BaseURL != nil && !validSetupURL(strings.TrimSpace(*in.BaseURL)) {
		fields["baseUrl"] = "must be an http or https URL with a host"
	}
	if len(fields) > 0 {
		writeAPIErrorFields(w, http.StatusBadRequest, codeValidation, "invalid upstream", fields)
		return
	}

	endpointChanged := false
	err := s.configStore.Update(func(c *config.Config) error {
		idx := upstreamIndex(c, id)
		if idx < 0 {
			return errUpstreamNotFound
		}
		u := &c.Upstreams[idx]
		if in.Name != nil {
			u.Name = strings.TrimSpace(*in.Name)
		}
		if in.BaseURL != nil {
			baseURL := strings.TrimSpace(*in.BaseURL)
			if baseURL != u.BaseURL {
				endpointChanged = true
			}
			u.BaseURL = baseURL
		}
		if in.APIKey != nil {
			apiKey := strings.TrimSpace(*in.APIKey)
			if apiKey != u.APIKey {
				endpointChanged = true
			}
			u.APIKey = apiKey
		}
		if in.Enabled != nil {
			enabled := *in.Enabled
			u.Enabled = &enabled
		}
		return nil
	})
	if errors.Is(err, errUpstreamNotFound) {
		writeAPIError(w, http.StatusNotFound, codeNotFound, "upstream not found")
		return
	}
	if err != nil {
		writeConfigWriteError(w, err)
		return
	}
	if endpointChanged {
		s.catalog.invalidate()
	}
	entry, _ := s.upstreamEntry(id)
	writeJSON(w, http.StatusOK, entry)
}

// deleteUpstream answers DELETE /api/upstreams/{id}. Upstreams still bound to
// local keys are refused with 409 upstream_in_use and the key IDs in
// fields.keys. Usage history is retained.
func (s *Server) deleteUpstream(w http.ResponseWriter, r *http.Request) {
	if !s.requireConfigSetup(w) {
		return
	}
	id := r.PathValue("id")
	var inUse []string
	err := s.configStore.Update(func(c *config.Config) error {
		idx := upstreamIndex(c, id)
		if idx < 0 {
			return errUpstreamNotFound
		}
		inUse = inUse[:0]
		for _, k := range c.GatewayKeys {
			if k.UpstreamID == id {
				inUse = append(inUse, k.ID)
			}
		}
		if len(inUse) > 0 {
			return errUpstreamInUse
		}
		c.Upstreams = append(c.Upstreams[:idx], c.Upstreams[idx+1:]...)
		return nil
	})
	switch {
	case errors.Is(err, errUpstreamNotFound):
		writeAPIError(w, http.StatusNotFound, codeNotFound, "upstream not found")
		return
	case errors.Is(err, errUpstreamInUse):
		writeUpstreamInUse(w, inUse)
		return
	case err != nil:
		writeConfigWriteError(w, err)
		return
	}
	s.catalog.invalidate()
	w.WriteHeader(http.StatusNoContent)
}

// upstreamSecret answers GET /api/upstreams/{id}/secret with the plaintext
// upstream key.
func (s *Server) upstreamSecret(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	snap := s.configSnapshot()
	if snap == nil {
		writeAPIError(w, http.StatusNotFound, codeNotFound, "upstream not found")
		return
	}
	u, ok := snap.Upstream(id)
	if !ok {
		writeAPIError(w, http.StatusNotFound, codeNotFound, "upstream not found")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, upstreamSecretResponse{APIKey: u.APIKey})
}

// --- probing ---------------------------------------------------------------

// upstreamProbeRequest is the POST /api/upstreams/probe body.
type upstreamProbeRequest struct {
	BaseURL string `json:"baseUrl"`
	APIKey  string `json:"apiKey"`
}

// upstreamProbeModel is one model discovered by the probe.
type upstreamProbeModel struct {
	ID      string `json:"id"`
	OwnedBy string `json:"ownedBy"`
	Matched bool   `json:"matched"`
}

// upstreamProbePreset is one price preset suggested for a probed model. The
// preset catalog arrives with the pricing task; the field is present so the
// response shape is stable.
type upstreamProbePreset struct {
	Model      string  `json:"model"`
	Input      float64 `json:"input"`
	Output     float64 `json:"output"`
	CacheRead  float64 `json:"cacheRead"`
	CacheWrite float64 `json:"cacheWrite"`
	Reasoning  float64 `json:"reasoning"`
}

// upstreamProbeResponse is the probe result. OK is false for upstream
// failures; Error never contains the API key.
type upstreamProbeResponse struct {
	OK             bool                  `json:"ok"`
	LatencyMS      int64                 `json:"latencyMs"`
	Provider       string                `json:"provider"`
	PresetSnapshot string                `json:"presetSnapshot,omitempty"`
	Models         []upstreamProbeModel  `json:"models"`
	Presets        []upstreamProbePreset `json:"presets"`
	Error          string                `json:"error"`
}

// upstreamTestResult is the result of testing a saved upstream.
type upstreamTestResult struct {
	OK         bool   `json:"ok"`
	LatencyMS  int64  `json:"latencyMs"`
	ModelCount int    `json:"modelCount"`
	Error      string `json:"error"`
}

// normalizeProbeBaseURL trims a draft base URL and rejects anything but an
// absolute http(s) URL.
func normalizeProbeBaseURL(raw string) (string, bool) {
	base := strings.TrimRight(strings.TrimSpace(raw), "/")
	if !validSetupURL(base) {
		return "", false
	}
	return base, true
}

// probeUpstream answers POST /api/upstreams/probe. It fetches the models
// listing of a draft upstream and never touches the configuration. An omitted
// API key falls back to the key of a saved upstream with the same base URL, so
// the UI can preview a configured upstream without revealing its key.
func (s *Server) probeUpstream(w http.ResponseWriter, r *http.Request) {
	var in upstreamProbeRequest
	if !decodeJSON(w, r, &in) {
		return
	}
	base, ok := normalizeProbeBaseURL(in.BaseURL)
	if !ok {
		writeAPIErrorFields(w, http.StatusBadRequest, codeValidation, "invalid upstream probe",
			map[string]string{"baseUrl": "must be an http or https URL with a host"})
		return
	}
	key := strings.TrimSpace(in.APIKey)
	if key == "" {
		key = s.savedAPIKeyForBase(base)
	}
	models, latency, errMsg := fetchUpstreamModels(r.Context(), base, key)
	if errMsg != "" {
		errMsg = s.catalog.redact(errMsg, key)
	}
	out := upstreamProbeResponse{
		OK:        errMsg == "",
		LatencyMS: latency,
		Provider:  detectProvider(base),
		Models:    make([]upstreamProbeModel, 0, len(models)),
		Presets:   []upstreamProbePreset{},
		Error:     errMsg,
	}
	for _, m := range models {
		out.Models = append(out.Models, upstreamProbeModel{ID: m.ID, OwnedBy: m.OwnedBy})
	}
	writeJSON(w, http.StatusOK, out)
}

// testUpstream answers POST /api/upstreams/{id}/test. Upstream failures are
// reported in the body with HTTP 200; only an unknown ID is a 404.
func (s *Server) testUpstream(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	snap := s.configSnapshot()
	if snap == nil {
		writeAPIError(w, http.StatusNotFound, codeNotFound, "upstream not found")
		return
	}
	u, ok := snap.Upstream(id)
	if !ok {
		writeAPIError(w, http.StatusNotFound, codeNotFound, "upstream not found")
		return
	}
	models, latency, errMsg := fetchUpstreamModels(r.Context(), u.BaseURL, u.APIKey)
	if errMsg != "" {
		errMsg = s.catalog.redact(errMsg, u.APIKey)
	}
	writeJSON(w, http.StatusOK, upstreamTestResult{
		OK:         errMsg == "",
		LatencyMS:  latency,
		ModelCount: len(models),
		Error:      errMsg,
	})
}

// savedAPIKeyForBase returns the API key of a configured upstream whose base
// URL matches, or "".
func (s *Server) savedAPIKeyForBase(base string) string {
	snap := s.configSnapshot()
	if snap == nil {
		return ""
	}
	for _, u := range snap.Config.Upstreams {
		if strings.TrimRight(strings.TrimSpace(u.BaseURL), "/") == base {
			return u.APIKey
		}
	}
	return ""
}

// fetchUpstreamModels lists the models of an OpenAI-compatible upstream. It
// tries <base>/models first and, when that path is missing, <base>/v1/models
// unless base already ends in /v1. The returned error message is plain text;
// the caller redacts the key.
func fetchUpstreamModels(ctx context.Context, base, apiKey string) ([]catalogModel, int64, string) {
	endpoints := []string{"/models"}
	if !strings.HasSuffix(base, "/v1") {
		endpoints = append(endpoints, "/v1/models")
	}
	client := &http.Client{Timeout: upstreamProbeTimeout}
	start := time.Now()

	var resp *http.Response
	for i, path := range endpoints {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+path, nil)
		if err != nil {
			return nil, 0, "invalid upstream URL"
		}
		if apiKey != "" {
			req.Header.Set("Authorization", "Bearer "+apiKey)
		}
		resp, err = client.Do(req)
		if err != nil {
			return nil, time.Since(start).Milliseconds(), err.Error()
		}
		// A missing /models endpoint is often an OpenAI-compatible server
		// that only exposes the versioned path; retry once before failing.
		if (resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusMethodNotAllowed) &&
			i < len(endpoints)-1 {
			resp.Body.Close()
			continue
		}
		break
	}
	if resp == nil {
		return nil, time.Since(start).Milliseconds(), "upstream request failed"
	}
	defer resp.Body.Close()
	latency := time.Since(start).Milliseconds()
	if resp.StatusCode != http.StatusOK {
		return nil, latency, fmt.Sprintf("upstream returned HTTP %d", resp.StatusCode)
	}
	models, err := decodeCatalogModels(resp)
	if err != nil {
		return nil, latency, "invalid upstream response"
	}
	return models, latency, ""
}

// probeProviders maps a base URL host suffix to the provider preset name.
// Matching is a dot-boundary suffix comparison, never a substring search.
var probeProviders = []struct {
	suffix string
	name   string
}{
	{"deepseek.com", "deepseek"},
	{"openai.com", "openai"},
	{"openrouter.ai", "openrouter"},
	{"moonshot.cn", "moonshot"},
	{"bigmodel.cn", "zhipu"},
	{"dashscope.aliyuncs.com", "dashscope"},
	{"siliconflow.cn", "siliconflow"},
	{"siliconflow.com", "siliconflow"},
	{"groq.com", "groq"},
	{"together.ai", "together"},
	{"together.xyz", "together"},
	{"fireworks.ai", "fireworks"},
	{"x.ai", "xai"},
	{"mistral.ai", "mistral"},
	{"minimax.io", "minimax"},
	{"minimaxi.com", "minimax"},
}

// detectProvider names the provider a base URL host belongs to, or "" when it
// is unknown.
func detectProvider(raw string) string {
	host := ""
	if u, err := url.Parse(raw); err == nil {
		host = strings.ToLower(u.Hostname())
	}
	for _, p := range probeProviders {
		if host == p.suffix || strings.HasSuffix(host, "."+p.suffix) {
			return p.name
		}
	}
	return ""
}

// --- shared helpers --------------------------------------------------------

// writeConfigWriteError maps a configuration update failure onto the API
// envelope: validation errors carry the offending YAML paths, everything else
// is an internal error.
func writeConfigWriteError(w http.ResponseWriter, err error) {
	var ve *config.ValidationError
	if errors.As(err, &ve) {
		writeAPIErrorFields(w, http.StatusBadRequest, codeValidation, "invalid configuration", ve.Fields)
		return
	}
	writeInternal(w, err)
}

// writeUpstreamInUse answers a blocked upstream deletion.
func writeUpstreamInUse(w http.ResponseWriter, ids []string) {
	writeJSON(w, http.StatusConflict, map[string]any{
		"error": map[string]any{
			"code":    codeUpstreamInUse,
			"message": "upstream still has local keys bound",
			"fields":  map[string]any{"keys": ids},
		},
	})
}

// upstreamIndex returns the position of id in cfg.Upstreams, or -1.
func upstreamIndex(c *config.Config, id string) int {
	for i := range c.Upstreams {
		if c.Upstreams[i].ID == id {
			return i
		}
	}
	return -1
}

// upstreamIDTaken reports whether an upstream already uses id.
func upstreamIDTaken(c *config.Config, id string) bool {
	return upstreamIndex(c, id) >= 0
}

// uniqueSlug appends -2, -3, ... to base until taken reports false, trimming
// the stem so the result stays a valid configuration ID.
func uniqueSlug(base string, taken func(string) bool) string {
	if !taken(base) {
		return base
	}
	for i := 2; ; i++ {
		suffix := "-" + strconv.Itoa(i)
		stemLen := min(len(base), maxSlugLen-len(suffix))
		candidate := strings.TrimRight(base[:stemLen], "-") + suffix
		if !taken(candidate) {
			return candidate
		}
	}
}
