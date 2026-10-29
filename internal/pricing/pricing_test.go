package pricing_test

import (
	"testing"
	"time"

	"github.com/kiry163/llmmeter/internal/pricing"
	"github.com/kiry163/llmmeter/internal/usage"
)

func ptr(v float64) *float64 { return &v }

func TestMaskBit(t *testing.T) {
	cases := map[time.Weekday]uint8{
		time.Monday:    1,
		time.Tuesday:   2,
		time.Wednesday: 4,
		time.Thursday:  8,
		time.Friday:    16,
		time.Saturday:  32,
		time.Sunday:    64,
	}
	for wd, want := range cases {
		if got := pricing.MaskBit(wd); got != want {
			t.Fatalf("%s: got %d want %d", wd, got, want)
		}
	}
}

func TestWindowMatchBoundaries(t *testing.T) {
	loc, _ := time.LoadLocation("Asia/Shanghai")
	m := pricing.Model{
		Name: "m", Input: 1, Output: 2,
		Windows: []pricing.Window{{
			Label: "peak", DaysMask: pricing.MaskBit(time.Monday),
			StartMinute: 9 * 60, EndMinute: 12 * 60,
			Input: ptr(3.0), Output: nil,
		}},
	}
	table := pricing.NewTable([]pricing.Model{m})
	cases := []struct {
		at   time.Time
		want string
	}{
		{time.Date(2026, 10, 26, 8, 59, 59, 0, loc), "base"}, // 周一 08:59:59
		{time.Date(2026, 10, 26, 9, 0, 0, 0, loc), "peak"},
		{time.Date(2026, 10, 26, 11, 59, 59, 0, loc), "peak"},
		{time.Date(2026, 10, 26, 12, 0, 0, 0, loc), "base"},
		{time.Date(2026, 10, 27, 10, 0, 0, 0, loc), "base"}, // 周二
	}
	for _, c := range cases {
		if got := table.Quote("m", c.at, loc, usage.Usage{PromptTokens: 1}).WindowLabel; got != c.want {
			t.Fatalf("%s: got %q want %q", c.at, got, c.want)
		}
	}
}

func TestWindowCrossesMidnight(t *testing.T) {
	loc, _ := time.LoadLocation("Asia/Shanghai")
	m := pricing.Model{Name: "m", Windows: []pricing.Window{{
		Label: "night", DaysMask: pricing.MaskBit(time.Friday),
		StartMinute: 22 * 60, EndMinute: 2 * 60,
	}}}
	table := pricing.NewTable([]pricing.Model{m})
	friLate := time.Date(2026, 10, 30, 23, 0, 0, 0, loc)
	satEarly := time.Date(2026, 10, 31, 1, 59, 0, 0, loc)
	satLate := time.Date(2026, 10, 31, 23, 0, 0, 0, loc) // 周六不匹配起始日
	for _, c := range []struct {
		at   time.Time
		want string
	}{{friLate, "night"}, {satEarly, "night"}, {satLate, "base"}} {
		if got := table.Quote("m", c.at, loc, usage.Usage{}).WindowLabel; got != c.want {
			t.Fatalf("%s: got %q want %q", c.at, got, c.want)
		}
	}
}

func TestWindowFirstMatchWins(t *testing.T) {
	loc := time.UTC
	m := pricing.Model{Name: "m", Windows: []pricing.Window{
		{Label: "first", DaysMask: pricing.MaskBit(time.Monday), StartMinute: 0, EndMinute: 24 * 60},
		{Label: "second", DaysMask: pricing.MaskBit(time.Monday), StartMinute: 0, EndMinute: 24 * 60},
	}}
	table := pricing.NewTable([]pricing.Model{m})
	at := time.Date(2026, 10, 26, 10, 0, 0, 0, loc)
	if got := table.Quote("m", at, loc, usage.Usage{}).WindowLabel; got != "first" {
		t.Fatalf("got %q want first", got)
	}
}

func TestWindowAcrossDSTFallback(t *testing.T) {
	loc, _ := time.LoadLocation("America/New_York")
	m := pricing.Model{Name: "m", Windows: []pricing.Window{{
		Label: "night", DaysMask: pricing.MaskBit(time.Sunday),
		StartMinute: 60, EndMinute: 120,
	}}}
	table := pricing.NewTable([]pricing.Model{m})
	// 2026-11-01 为周日；01:30 EDT 与 01:30 EST 按墙钟都应命中
	for _, at := range []time.Time{
		time.Date(2026, 11, 1, 5, 30, 0, 0, time.UTC),
		time.Date(2026, 11, 1, 6, 30, 0, 0, time.UTC),
	} {
		if got := table.Quote("m", at, loc, usage.Usage{}).WindowLabel; got != "night" {
			t.Fatalf("%s: got %q want night", at.In(loc), got)
		}
	}
}

func TestQuoteCostSplitsCacheAndReasoning(t *testing.T) {
	loc := time.UTC
	out := ptr(2.0)
	m := pricing.Model{
		Name: "m", Input: 1, Output: 2, CacheRead: 0.5, Reasoning: out,
	}
	table := pricing.NewTable([]pricing.Model{m})
	q := table.Quote("m", time.Now(), loc, usage.Usage{
		PromptTokens: 1_000_000, CompletionTokens: 500_000,
		CacheReadTokens: 200_000, ReasoningTokens: 100_000,
	})
	// (800k*1 + 200k*0.5 + 400k*2 + 100k*2) /1e6 *1e9 = 1.9e9
	if q.CostNanoUSD != 1_900_000_000 {
		t.Fatalf("cost = %d", q.CostNanoUSD)
	}
	if !q.Priced || q.WindowLabel != "base" || q.Rates.Reasoning != 2 {
		t.Fatalf("got %+v", q)
	}
}

func TestQuoteReasoningFallsBackToOutput(t *testing.T) {
	table := pricing.NewTable([]pricing.Model{{Name: "m", Output: 2}})
	q := table.Quote("m", time.Now(), time.UTC, usage.Usage{CompletionTokens: 1_000_000, ReasoningTokens: 1_000_000})
	if q.CostNanoUSD != 2_000_000_000 {
		t.Fatalf("cost = %d", q.CostNanoUSD)
	}
}

func TestQuoteCacheWrite(t *testing.T) {
	table := pricing.NewTable([]pricing.Model{{Name: "m", CacheWrite: 1.25}})
	q := table.Quote("m", time.Now(), time.UTC, usage.Usage{CacheWriteTokens: 400_000})
	if q.CostNanoUSD != 500_000_000 {
		t.Fatalf("cost = %d", q.CostNanoUSD)
	}
}

func TestQuoteClampsNegativeComponents(t *testing.T) {
	// prompt < cache read makes the uncached-input component negative.
	table := pricing.NewTable([]pricing.Model{{Name: "m", Input: 1, CacheRead: 5}})
	q := table.Quote("m", time.Now(), time.UTC, usage.Usage{PromptTokens: 100, CacheReadTokens: 500})
	// clamped: 0 + 500*5 = 2500 -> 2_500_000 nano; unclamped would be 2100.
	if q.CostNanoUSD != 2_500_000 {
		t.Fatalf("cost = %d", q.CostNanoUSD)
	}
}

func TestQuoteWindowOverridesAndFallback(t *testing.T) {
	loc := time.UTC
	m := pricing.Model{
		Name: "m", Input: 1, Output: 2, CacheRead: 0.5, CacheWrite: 0.25,
		Windows: []pricing.Window{{
			Label: "peak", DaysMask: pricing.MaskBit(time.Monday),
			StartMinute: 0, EndMinute: 24 * 60,
			Input: ptr(4.0), Output: ptr(8.0),
		}},
	}
	table := pricing.NewTable([]pricing.Model{m})
	q := table.Quote("m", time.Date(2026, 10, 26, 10, 0, 0, 0, loc), loc, usage.Usage{})
	if !q.Priced || q.WindowLabel != "peak" {
		t.Fatalf("got %+v", q)
	}
	if q.Rates.Input != 4 || q.Rates.Output != 8 || q.Rates.CacheRead != 0.5 || q.Rates.CacheWrite != 0.25 {
		t.Fatalf("rates = %+v", q.Rates)
	}
	if q.Rates.Reasoning != 8 {
		t.Fatalf("reasoning = %v want merged output 8", q.Rates.Reasoning)
	}
}

func TestQuoteRatesJSON(t *testing.T) {
	table := pricing.NewTable([]pricing.Model{{Name: "m", Input: 1, Output: 2}})
	q := table.Quote("m", time.Now(), time.UTC, usage.Usage{})
	want := `{"model":"m","window":"base","input_per_mtok":1.000000,"output_per_mtok":2.000000,` +
		`"cache_read_per_mtok":0.000000,"cache_write_per_mtok":0.000000,"reasoning_per_mtok":2.000000}`
	if q.RatesJSON != want {
		t.Fatalf("rates json = %s", q.RatesJSON)
	}
}

func TestQuoteUnknownModelUnpriced(t *testing.T) {
	q := pricing.NewTable(nil).Quote("x", time.Now(), time.UTC, usage.Usage{})
	if q.Priced {
		t.Fatal("want unpriced")
	}
	if q.WindowLabel != "base" || q.RatesJSON != "" {
		t.Fatalf("got %+v", q)
	}
	if q.CostNanoUSD != 0 {
		t.Fatalf("cost = %d", q.CostNanoUSD)
	}
}
