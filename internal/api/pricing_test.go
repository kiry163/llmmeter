package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/kiry163/llmmeter/internal/config"
)

// pricingModelBody is the decoded API shape of one model price.
type pricingModelBody struct {
	Name       string   `json:"name"`
	Input      float64  `json:"input"`
	Output     float64  `json:"output"`
	CacheRead  float64  `json:"cacheRead"`
	CacheWrite float64  `json:"cacheWrite"`
	Reasoning  *float64 `json:"reasoning"`
	Windows    []struct {
		Label      string   `json:"label"`
		Days       []string `json:"days"`
		Start      string   `json:"start"`
		End        string   `json:"end"`
		Input      *float64 `json:"input"`
		Output     *float64 `json:"output"`
		CacheRead  *float64 `json:"cacheRead"`
		CacheWrite *float64 `json:"cacheWrite"`
		Reasoning  *float64 `json:"reasoning"`
	} `json:"windows"`
}

type pricingBody struct {
	Models []pricingModelBody `json:"models"`
}

func decodePricing(t *testing.T, body []byte) pricingBody {
	t.Helper()
	var out pricingBody
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode pricing: %v: %s", err, body)
	}
	return out
}

// upstreamModels returns the configured models of one upstream.
func upstreamModels(t *testing.T, cs *config.Store, id string) []config.Model {
	t.Helper()
	cfg := cs.Config()
	for _, u := range cfg.Upstreams {
		if u.ID == id {
			return u.Models
		}
	}
	t.Fatalf("upstream %q not found", id)
	return nil
}

func TestPutPricingReplacesOnlyTargetUpstream(t *testing.T) {
	env := newConfigEnv(t, "127.0.0.1:8100")
	if err := env.config.Update(func(c *config.Config) error {
		c.Upstreams = []config.Upstream{
			{ID: "a", Name: "a", BaseURL: "https://a.example.com",
				Models: []config.Model{{Name: "old-a", Input: 1}}},
			{ID: "b", Name: "b", BaseURL: "https://b.example.com",
				Models: []config.Model{{Name: "b-model", Input: 3, Output: 4}}},
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	body := `{"models":[
		{"name":"new","input":0.5,"output":2,"cacheRead":0.1,"reasoning":null,
		 "windows":[{"label":"night","days":["mon"],"start":"00:00","end":"08:00",
		             "input":0.25,"cacheWrite":null,"reasoning":null}]}]}`
	rec := doPut(t, env.srv, "/api/upstreams/a/pricing", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("put = %d %s", rec.Code, rec.Body.String())
	}
	got := decodePricing(t, rec.Body.Bytes())
	if len(got.Models) != 1 || got.Models[0].Name != "new" {
		t.Fatalf("put response = %+v", got)
	}
	m := got.Models[0]
	if m.Input != 0.5 || m.Output != 2 || m.CacheRead != 0.1 {
		t.Fatalf("rates = %+v", m)
	}
	if m.CacheWrite != 0 {
		t.Fatalf("cacheWrite = %v, want 0", m.CacheWrite)
	}
	// reasoning null means "inherit output" and must round-trip as null.
	if m.Reasoning != nil {
		t.Fatalf("reasoning = %v, want null", *m.Reasoning)
	}
	if len(m.Windows) != 1 || m.Windows[0].CacheWrite != nil || m.Windows[0].Reasoning != nil {
		t.Fatalf("windows = %+v", m.Windows)
	}

	// Upstream b is untouched.
	if models := upstreamModels(t, env.config, "b"); len(models) != 1 ||
		models[0].Name != "b-model" || models[0].Input != 3 || models[0].Output != 4 {
		t.Fatalf("upstream b models = %+v", models)
	}
	// Upstream a was replaced, not appended.
	models := upstreamModels(t, env.config, "a")
	if len(models) != 1 || models[0].Name != "new" {
		t.Fatalf("upstream a models = %+v", models)
	}
	if models[0].Reasoning != nil {
		t.Fatalf("stored reasoning = %v, want nil", *models[0].Reasoning)
	}

	rec = doGET(t, env.srv, "/api/upstreams/a/pricing")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"new"`) {
		t.Fatalf("get = %d %s", rec.Code, rec.Body.String())
	}

	// Duplicate model names are rejected without changing the config.
	rec = doPut(t, env.srv, "/api/upstreams/a/pricing",
		`{"models":[{"name":"dup"},{"name":"dup"}]}`)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "models[1].name") {
		t.Fatalf("duplicate = %d %s", rec.Code, rec.Body.String())
	}
	if models := upstreamModels(t, env.config, "a"); len(models) != 1 || models[0].Name != "new" {
		t.Fatalf("duplicate changed the config: %+v", models)
	}

	// A negative rate is rejected with the request path.
	rec = doPut(t, env.srv, "/api/upstreams/a/pricing",
		`{"models":[{"name":"neg","input":-1}]}`)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "models[0].input") {
		t.Fatalf("negative = %d %s", rec.Code, rec.Body.String())
	}

	// Invalid window is rejected.
	rec = doPut(t, env.srv, "/api/upstreams/a/pricing",
		`{"models":[{"name":"w","windows":[{"label":"x","days":[],"start":"00:00","end":"00:00"}]}]}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad window = %d %s", rec.Code, rec.Body.String())
	}

	rec = doPut(t, env.srv, "/api/upstreams/nope/pricing", `{"models":[]}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown = %d %s", rec.Code, rec.Body.String())
	}
}

func TestPricingImportSkipsExisting(t *testing.T) {
	env := newConfigEnv(t, "127.0.0.1:8100")
	addPricedUpstream(t, env.config, "a", "https://a.example.com", "m")

	rec := doPOST(t, env.srv, "/api/upstreams/a/pricing/import", `{"names":["m","n"]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("import = %d %s", rec.Code, rec.Body.String())
	}
	var got struct {
		Created []string `json:"created"`
		Skipped []string `json:"skipped"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Created) != 1 || got.Created[0] != "n" {
		t.Fatalf("created = %+v", got.Created)
	}
	if len(got.Skipped) != 1 || got.Skipped[0] != "m" {
		t.Fatalf("skipped = %+v", got.Skipped)
	}
	models := upstreamModels(t, env.config, "a")
	if len(models) != 2 || models[1].Name != "n" ||
		models[1].Input != 0 || models[1].Output != 0 || models[1].CacheRead != 0 ||
		models[1].CacheWrite != 0 || models[1].Reasoning != nil {
		t.Fatalf("imported models = %+v", models)
	}

	rec = doPOST(t, env.srv, "/api/upstreams/a/pricing/import", `{"names":[]}`)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), `"names"`) {
		t.Fatalf("empty import = %d %s", rec.Code, rec.Body.String())
	}

	names := make([]string, 101)
	for i := range names {
		names[i] = fmt.Sprintf("m%d", i)
	}
	body, _ := json.Marshal(map[string][]string{"names": names})
	rec = doPOST(t, env.srv, "/api/upstreams/a/pricing/import", string(body))
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "at most 100") {
		t.Fatalf("oversize import = %d %s", rec.Code, rec.Body.String())
	}
}

func TestPricingWritesRequireSetup(t *testing.T) {
	env := newConfigEnv(t, "127.0.0.1:8100")
	rec := doPut(t, env.srv, "/api/upstreams/a/pricing", `{"models":[]}`)
	assertAPIError(t, rec, http.StatusConflict, "setup_required")
	rec = doPOST(t, env.srv, "/api/upstreams/a/pricing/import", `{"names":["m"]}`)
	assertAPIError(t, rec, http.StatusConflict, "setup_required")
}

func TestPricingGetReturnsConfiguredModelsWithNullInheritance(t *testing.T) {
	env := newConfigEnv(t, "127.0.0.1:8100")
	inherit := 2.5
	if err := env.config.Update(func(c *config.Config) error {
		c.Upstreams = []config.Upstream{{
			ID: "a", Name: "a", BaseURL: "https://a.example.com",
			Models: []config.Model{{
				Name: "m", Input: 1, Output: 2, CacheWrite: 0,
				Reasoning: nil,
				Windows: []config.Window{{
					Label: "peak", Days: []string{"mon"}, Start: "09:00", End: "18:00",
					Output: &inherit,
				}},
			}},
		}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	rec := doGET(t, env.srv, "/api/upstreams/a/pricing")
	if rec.Code != http.StatusOK {
		t.Fatalf("get = %d %s", rec.Code, rec.Body.String())
	}
	got := decodePricing(t, rec.Body.Bytes())
	if len(got.Models) != 1 {
		t.Fatalf("models = %+v", got)
	}
	m := got.Models[0]
	if m.Reasoning != nil {
		t.Fatalf("reasoning = %v, want null", *m.Reasoning)
	}
	if len(m.Windows) != 1 || m.Windows[0].Output == nil || *m.Windows[0].Output != 2.5 {
		t.Fatalf("window output = %+v", m.Windows)
	}
	if m.Windows[0].Input != nil || m.Windows[0].Reasoning != nil {
		t.Fatalf("window inherit = %+v", m.Windows[0])
	}
}
