package pricing

import (
	"testing"
	"time"

	"github.com/kiry163/llmmeter/internal/config"
)

func testCatalog(t *testing.T) *Catalog {
	t.Helper()
	c, err := New(config.Pricing{Models: map[string]config.Model{
		"deepseek-chat": {
			InputPerMTok: 0.28, OutputPerMTok: 0.42, CacheReadPerMTok: 0.028,
			Windows: []config.Window{
				{Label: "peak", Days: []string{"mon", "tue", "wed", "thu", "fri"},
					Start: "09:00", End: "12:00",
					InputPerMTok: new(0.56), OutputPerMTok: new(0.84)},
				{Label: "night", Start: "23:00", End: "01:00", InputPerMTok: new(0.1)},
			},
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestWindowHitAndFallback(t *testing.T) {
	c := testCatalog(t)
	loc, _ := time.LoadLocation("Asia/Shanghai")
	at := time.Date(2026, 10, 28, 10, 30, 0, 0, loc) // Wednesday
	r, label, ok := c.Price("deepseek-chat", at, loc)
	if !ok || label != "peak" || r.Input != 0.56 || r.Output != 0.84 {
		t.Fatalf("expected peak rates, got %+v %q %v", r, label, ok)
	}
	if r.CacheRead != 0.028 {
		t.Errorf("cache read should fall back to base, got %v", r.CacheRead)
	}
	at = time.Date(2026, 10, 28, 13, 0, 0, 0, loc)
	r, label, _ = c.Price("deepseek-chat", at, loc)
	if label != "base" || r.Input != 0.28 {
		t.Fatalf("expected base, got %+v %q", r, label)
	}
}

func TestWindowWeekendAndEdges(t *testing.T) {
	c := testCatalog(t)
	loc, _ := time.LoadLocation("Asia/Shanghai")
	sat := time.Date(2026, 10, 31, 10, 0, 0, 0, loc)
	if _, label, _ := c.Price("deepseek-chat", sat, loc); label != "base" {
		t.Fatalf("weekend should not match weekday peak, got %q", label)
	}
	end := time.Date(2026, 10, 28, 12, 0, 0, 0, loc) // end exclusive
	if _, label, _ := c.Price("deepseek-chat", end, loc); label != "base" {
		t.Fatalf("end must be exclusive, got %q", label)
	}
}

func TestCrossMidnightWindow(t *testing.T) {
	c := testCatalog(t)
	loc, _ := time.LoadLocation("Asia/Shanghai")
	// 23:30 on Wednesday matches same-day start; 00:30 Thursday matches spill from Wednesday.
	for _, at := range []time.Time{
		time.Date(2026, 10, 28, 23, 30, 0, 0, loc),
		time.Date(2026, 10, 29, 0, 30, 0, 0, loc),
	} {
		if _, label, _ := c.Price("deepseek-chat", at, loc); label != "night" {
			t.Fatalf("%s: expected night, got %q", at, label)
		}
	}
}

func TestUnknownModel(t *testing.T) {
	c := testCatalog(t)
	loc, _ := time.LoadLocation("Asia/Shanghai")
	if _, _, ok := c.Price("nope", time.Now(), loc); ok {
		t.Fatal("unknown model must be unpriced")
	}
}

func TestCostSplitsCacheAndReasoning(t *testing.T) {
	r := Rates{Input: 1, Output: 10, CacheRead: 0.1, Reasoning: 20}
	// prompt 1000 (200 cached), completion 500 (100 reasoning)
	got := Cost(r, 1000, 500, 200, 0, 100)
	// 800*1 + 200*0.1 + 400*10 + 100*20 = 6820 micro-USD
	// nano = 6820 * 1000
	if want := int64(6_820_000); got != want {
		t.Fatalf("cost = %d, want %d", got, want)
	}
}

func TestCostReasoningFallsBackToOutput(t *testing.T) {
	r := Rates{Input: 1, Output: 10}
	if got, want := Cost(r, 0, 500, 0, 0, 100), int64(5_000_000); got != want {
		t.Fatalf("cost = %d, want %d", got, want)
	}
}
