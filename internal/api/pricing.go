package api

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/kiry163/llmmeter/internal/config"
)

const (
	// maxPricingImport caps one import request, matching spec 8.5.
	maxPricingImport = 100
)

// pricingWindowJSON is one price window in API shape. Nil rates inherit the
// model base rate.
type pricingWindowJSON struct {
	Label      string   `json:"label"`
	Days       []string `json:"days"`
	Start      string   `json:"start"`
	End        string   `json:"end"`
	Input      *float64 `json:"input"`
	Output     *float64 `json:"output"`
	CacheRead  *float64 `json:"cacheRead"`
	CacheWrite *float64 `json:"cacheWrite"`
	Reasoning  *float64 `json:"reasoning"`
}

// pricingModelJSON is one model price in API shape. A nil Reasoning means the
// request is priced at the output rate; cacheWrite is always rendered.
type pricingModelJSON struct {
	Name       string              `json:"name"`
	Input      float64             `json:"input"`
	Output     float64             `json:"output"`
	CacheRead  float64             `json:"cacheRead"`
	CacheWrite float64             `json:"cacheWrite"`
	Reasoning  *float64            `json:"reasoning"`
	Windows    []pricingWindowJSON `json:"windows"`
}

// pricingBody is the GET/PUT /api/upstreams/{id}/pricing body.
type pricingBody struct {
	Models []pricingModelJSON `json:"models"`
}

// pricingImportRequest is the POST .../pricing/import body.
type pricingImportRequest struct {
	Names []string `json:"names"`
}

// pricingImportResponse reports which models were added and which already
// existed.
type pricingImportResponse struct {
	Created []string `json:"created"`
	Skipped []string `json:"skipped"`
}

// upstreamFromPath resolves the {id} path value against the configuration
// snapshot, answering 404 for an unknown upstream.
func (s *Server) upstreamFromPath(w http.ResponseWriter, r *http.Request) (*config.Upstream, bool) {
	snap := s.configSnapshot()
	if snap == nil {
		writeAPIError(w, http.StatusNotFound, codeNotFound, "upstream not found")
		return nil, false
	}
	u, ok := snap.Upstream(r.PathValue("id"))
	if !ok {
		writeAPIError(w, http.StatusNotFound, codeNotFound, "upstream not found")
		return nil, false
	}
	return u, true
}

// upstreamPricing answers GET /api/upstreams/{id}/pricing with the complete
// price list of one upstream.
func (s *Server) upstreamPricing(w http.ResponseWriter, r *http.Request) {
	u, ok := s.upstreamFromPath(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, pricingBody{Models: pricingModelsJSON(u.Models)})
}

// replaceUpstreamPricing answers PUT /api/upstreams/{id}/pricing. The request
// list atomically replaces only the target upstream's models; any validation
// failure leaves the configuration untouched.
func (s *Server) replaceUpstreamPricing(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, ok := s.upstreamFromPath(w, r); !ok {
		return
	}
	var in pricingBody
	if !decodeJSON(w, r, &in) {
		return
	}
	models, fields := pricingModelsFromJSON(in.Models)
	if len(fields) > 0 {
		writeAPIErrorFields(w, http.StatusBadRequest, codeValidation, "invalid pricing", fields)
		return
	}
	err := s.configStore.Update(func(c *config.Config) error {
		idx := upstreamIndex(c, id)
		if idx < 0 {
			return errUpstreamNotFound
		}
		c.Upstreams[idx].Models = models
		return nil
	})
	switch {
	case errors.Is(err, errUpstreamNotFound):
		writeAPIError(w, http.StatusNotFound, codeNotFound, "upstream not found")
		return
	case err != nil:
		writeConfigWriteError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, pricingBody{Models: pricingModelsJSON(models)})
}

// importUpstreamPricing answers POST /api/upstreams/{id}/pricing/import. It
// adds the named models at zero rates, skipping names that already exist.
func (s *Server) importUpstreamPricing(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, ok := s.upstreamFromPath(w, r); !ok {
		return
	}
	var in pricingImportRequest
	if !decodeJSON(w, r, &in) {
		return
	}
	names, fields := importNames(in.Names)
	if len(fields) > 0 {
		writeAPIErrorFields(w, http.StatusBadRequest, codeValidation, "invalid pricing import", fields)
		return
	}
	created := make([]string, 0, len(names))
	skipped := make([]string, 0)
	err := s.configStore.Update(func(c *config.Config) error {
		idx := upstreamIndex(c, id)
		if idx < 0 {
			return errUpstreamNotFound
		}
		u := &c.Upstreams[idx]
		existing := make(map[string]bool, len(u.Models))
		for _, m := range u.Models {
			existing[m.Name] = true
		}
		created = created[:0]
		skipped = skipped[:0]
		for _, name := range names {
			if existing[name] {
				skipped = append(skipped, name)
				continue
			}
			existing[name] = true
			created = append(created, name)
			u.Models = append(u.Models, config.Model{Name: name})
		}
		return nil
	})
	switch {
	case errors.Is(err, errUpstreamNotFound):
		writeAPIError(w, http.StatusNotFound, codeNotFound, "upstream not found")
		return
	case err != nil:
		writeConfigWriteError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, pricingImportResponse{Created: created, Skipped: skipped})
}

// importNames trims and validates the import list. Duplicate names are dropped
// after their first occurrence.
func importNames(raw []string) ([]string, map[string]string) {
	fields := map[string]string{}
	if len(raw) > maxPricingImport {
		fields["names"] = fmt.Sprintf("must have at most %d entries", maxPricingImport)
		return nil, fields
	}
	names := make([]string, 0, len(raw))
	seen := make(map[string]bool, len(raw))
	for i, v := range raw {
		name := strings.TrimSpace(v)
		switch {
		case name == "":
			fields[fmt.Sprintf("names[%d]", i)] = "must not be empty"
			continue
		case utf8.RuneCountInString(name) > maxPricedModelNameLen:
			fields[fmt.Sprintf("names[%d]", i)] = fmt.Sprintf("must be at most %d characters", maxPricedModelNameLen)
			continue
		case seen[name]:
			continue
		}
		seen[name] = true
		names = append(names, name)
	}
	if len(raw) == 0 {
		fields["names"] = "required"
	}
	return names, fields
}

// pricingModelsJSON renders configured models in API shape.
func pricingModelsJSON(models []config.Model) []pricingModelJSON {
	out := make([]pricingModelJSON, 0, len(models))
	for _, m := range models {
		windows := make([]pricingWindowJSON, 0, len(m.Windows))
		for _, w := range m.Windows {
			windows = append(windows, pricingWindowJSON{
				Label:      w.Label,
				Days:       w.Days,
				Start:      w.Start,
				End:        w.End,
				Input:      cloneRate(w.Input),
				Output:     cloneRate(w.Output),
				CacheRead:  cloneRate(w.CacheRead),
				CacheWrite: cloneRate(w.CacheWrite),
				Reasoning:  cloneRate(w.Reasoning),
			})
		}
		out = append(out, pricingModelJSON{
			Name:       m.Name,
			Input:      m.Input,
			Output:     m.Output,
			CacheRead:  m.CacheRead,
			CacheWrite: m.CacheWrite,
			Reasoning:  cloneRate(m.Reasoning),
			Windows:    windows,
		})
	}
	return out
}

// cloneRate copies a nullable rate so responses never alias configuration
// memory.
func cloneRate(v *float64) *float64 {
	if v == nil {
		return nil
	}
	out := *v
	return &out
}

// maxPricedModelNameLen mirrors the configuration bound. The package keeps its
// own copy because config does not export it.
const maxPricedModelNameLen = 200

// pricingModelsFromJSON converts and validates an API price list. Model names
// are trimmed; a nil reasoning stays nil (inherit output) and an omitted
// cacheWrite decodes as 0. Validation reuses config.Validate so the API and the
// YAML document accept exactly the same values; the returned field keys use the
// request paths.
func pricingModelsFromJSON(in []pricingModelJSON) ([]config.Model, map[string]string) {
	models := make([]config.Model, 0, len(in))
	for _, m := range in {
		cm := config.Model{
			Name:       strings.TrimSpace(m.Name),
			Input:      m.Input,
			Output:     m.Output,
			CacheRead:  m.CacheRead,
			CacheWrite: m.CacheWrite,
			Reasoning:  cloneRate(m.Reasoning),
			Windows:    make([]config.Window, 0, len(m.Windows)),
		}
		for _, w := range m.Windows {
			cm.Windows = append(cm.Windows, config.Window{
				Label:      w.Label,
				Days:       w.Days,
				Start:      w.Start,
				End:        w.End,
				Input:      cloneRate(w.Input),
				Output:     cloneRate(w.Output),
				CacheRead:  cloneRate(w.CacheRead),
				CacheWrite: cloneRate(w.CacheWrite),
				Reasoning:  cloneRate(w.Reasoning),
			})
		}
		models = append(models, cm)
	}
	return models, validatePricingModels(models)
}

// validatePricingModels validates a price list with the configuration rules and
// rewrites the field paths to the pricing request shape (models[0].input, ...).
func validatePricingModels(models []config.Model) map[string]string {
	c := config.Default()
	c.Upstreams = []config.Upstream{{
		ID:      "pricing",
		Name:    "pricing",
		BaseURL: "http://localhost",
		Models:  models,
	}}
	err := c.Validate()
	if err == nil {
		return nil
	}
	var ve *config.ValidationError
	if !errors.As(err, &ve) {
		return map[string]string{"models": err.Error()}
	}
	fields := make(map[string]string, len(ve.Fields))
	const prefix = "upstreams[0].models"
	for k, v := range ve.Fields {
		if rest, ok := strings.CutPrefix(k, prefix); ok {
			fields["models"+rest] = v
			continue
		}
		// Anything outside the model list cannot be caused by this request.
		fields["models"] = v
	}
	return fields
}
