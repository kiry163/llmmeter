package pricing

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/kiry163/llmmeter/internal/config"
)

type Rates struct {
	Input      float64
	Output     float64
	CacheRead  float64
	CacheWrite float64
	Reasoning  float64
}

type partial struct {
	input, output, cacheRead, cacheWrite, reasoning *float64
}

type window struct {
	label     string
	days      uint8 // bit i = time.Weekday(i); 0 means every day
	startMin  int
	endMin    int
	overrides partial
}

type compiledModel struct {
	base    Rates
	windows []window
}

type Catalog struct {
	models map[string]compiledModel
}

func New(p config.Pricing) (*Catalog, error) {
	c := &Catalog{models: make(map[string]compiledModel, len(p.Models))}
	for name, m := range p.Models {
		cm := compiledModel{base: Rates{
			Input: m.InputPerMTok, Output: m.OutputPerMTok,
			CacheRead: m.CacheReadPerMTok, CacheWrite: m.CacheWritePerMTok,
			Reasoning: m.ReasoningPerMTok,
		}}
		for i, w := range m.Windows {
			sh, sm, err := config.ParseClock(w.Start)
			if err != nil {
				return nil, fmt.Errorf("%s window %d: %w", name, i, err)
			}
			eh, em, err := config.ParseClock(w.End)
			if err != nil {
				return nil, fmt.Errorf("%s window %d: %w", name, i, err)
			}
			var mask uint8
			for _, d := range w.Days {
				wd, ok := config.Weekday(d)
				if !ok {
					return nil, fmt.Errorf("%s window %d: bad day %q", name, i, d)
				}
				mask |= 1 << uint(wd)
			}
			cm.windows = append(cm.windows, window{
				label: w.Label, days: mask,
				startMin: sh*60 + sm, endMin: eh*60 + em,
				overrides: partial{
					input: w.InputPerMTok, output: w.OutputPerMTok,
					cacheRead: w.CacheReadPerMTok, cacheWrite: w.CacheWritePerMTok,
					reasoning: w.ReasoningPerMTok,
				},
			})
		}
		c.models[name] = cm
	}
	return c, nil
}

// Price returns the rates in effect at at, the matching window label ("base"
// when none matches), and whether the model is priced.
func (c *Catalog) Price(model string, at time.Time, loc *time.Location) (Rates, string, bool) {
	cm, ok := c.models[model]
	if !ok {
		return Rates{}, "", false
	}
	local := at.In(loc)
	for _, w := range cm.windows {
		if w.match(local) {
			return w.apply(cm.base), w.label, true
		}
	}
	return cm.base, "base", true
}

func (w window) match(t time.Time) bool {
	minute := t.Hour()*60 + t.Minute()
	allowed := func(d time.Weekday) bool {
		return w.days == 0 || w.days&(1<<uint(d)) != 0
	}
	if w.startMin < w.endMin {
		return allowed(t.Weekday()) && minute >= w.startMin && minute < w.endMin
	}
	// Wraps past midnight: same-day tail, or spill from the previous day.
	if allowed(t.Weekday()) && minute >= w.startMin {
		return true
	}
	prev := (t.Weekday() + 6) % 7
	return allowed(prev) && minute < w.endMin
}

func (w window) apply(base Rates) Rates {
	r := base
	if w.overrides.input != nil {
		r.Input = *w.overrides.input
	}
	if w.overrides.output != nil {
		r.Output = *w.overrides.output
	}
	if w.overrides.cacheRead != nil {
		r.CacheRead = *w.overrides.cacheRead
	}
	if w.overrides.cacheWrite != nil {
		r.CacheWrite = *w.overrides.cacheWrite
	}
	if w.overrides.reasoning != nil {
		r.Reasoning = *w.overrides.reasoning
	}
	return r
}

// Lookup normalizes model names for pricing: exact match wins, otherwise a
// provider-prefixed name such as "deepseek/deepseek-chat" falls back to the
// suffix after the last slash.
func (c *Catalog) Lookup(name string) (string, bool) {
	if _, ok := c.models[name]; ok {
		return name, true
	}
	if i := strings.LastIndexByte(name, '/'); i >= 0 {
		tail := name[i+1:]
		if _, ok := c.models[tail]; ok {
			return tail, true
		}
	}
	return "", false
}

// Cost returns the request cost in nano-USD.
func Cost(r Rates, prompt, completion, cacheRead, cacheWrite, reasoning int64) int64 {
	if cacheRead > prompt {
		cacheRead = prompt
	}
	if reasoning > completion {
		reasoning = completion
	}
	nano := func(tokens int64, rate float64) int64 {
		return int64(math.Round(float64(tokens) * rate * 1000))
	}
	total := nano(prompt-cacheRead, r.Input)
	total += nano(cacheRead, r.CacheRead)
	total += nano(cacheWrite, r.CacheWrite)
	if r.Reasoning > 0 {
		total += nano(reasoning, r.Reasoning)
		total += nano(completion-reasoning, r.Output)
	} else {
		total += nano(completion, r.Output)
	}
	return total
}
