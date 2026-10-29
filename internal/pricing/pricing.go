// Package pricing compiles model price tables, resolves time-of-day price
// windows in the local wall clock, and turns token usage into integer
// nano-USD costs.
package pricing

import (
	"encoding/json"
	"math"
	"strconv"
	"time"

	"github.com/kiry163/llmmeter/internal/usage"
)

// Model is a priced model with optional peak windows. Rates are USD per
// million tokens; nil Reasoning means "charge reasoning at the output rate".
type Model struct {
	ID         int64
	Name       string
	Input      float64
	Output     float64
	CacheRead  float64
	CacheWrite float64
	Reasoning  *float64
	Windows    []Window
}

// Window is a repeating local wall-clock interval. DaysMask bit 0 is Monday
// and bit 6 is Sunday. EndMinute < StartMinute means the window crosses
// midnight and the day mask applies to the day the window starts on.
type Window struct {
	ID          int64
	Label       string
	DaysMask    uint8
	StartMinute int
	EndMinute   int
	Input       *float64
	Output      *float64
	CacheRead   *float64
	CacheWrite  *float64
	Reasoning   *float64
}

// Rates are effective USD-per-million-token rates for one request.
type Rates struct {
	Input      float64
	Output     float64
	CacheRead  float64
	CacheWrite float64
	Reasoning  float64
}

// Quote is the pricing decision for one request.
type Quote struct {
	Priced      bool
	WindowLabel string
	Rates       Rates
	CostNanoUSD int64
	RatesJSON   string
}

// Table is an immutable compiled price table.
type Table struct {
	models map[string]compiledModel
}

type compiledModel struct {
	base    Rates
	windows []Window
}

// NewTable compiles models into a Table. A nil or empty slice yields a usable
// table that prices nothing.
func NewTable(models []Model) *Table {
	t := &Table{models: make(map[string]compiledModel, len(models))}
	for _, m := range models {
		base := Rates{
			Input:      m.Input,
			Output:     m.Output,
			CacheRead:  m.CacheRead,
			CacheWrite: m.CacheWrite,
		}
		base.Reasoning = m.Output
		if m.Reasoning != nil {
			base.Reasoning = *m.Reasoning
		}
		t.models[m.Name] = compiledModel{base: base, windows: m.Windows}
	}
	return t
}

// MaskBit returns the DaysMask bit for wd, with Monday as bit 0.
func MaskBit(wd time.Weekday) uint8 {
	return 1 << uint((int(wd)+6)%7)
}

// Quote prices u for model at instant at, resolved in loc's wall clock.
// Unknown models and nil tables report Priced=false.
func (t *Table) Quote(model string, at time.Time, loc *time.Location, u usage.Usage) Quote {
	if t == nil {
		return Quote{WindowLabel: "base"}
	}
	m, ok := t.models[model]
	if !ok {
		return Quote{WindowLabel: "base"}
	}
	if loc == nil {
		loc = time.UTC
	}
	rates := m.base
	label := "base"
	local := at.In(loc)
	minute := local.Hour()*60 + local.Minute()
	day := local.Weekday()
	for i := range m.windows {
		w := &m.windows[i]
		if !w.matches(day, minute) {
			continue
		}
		label = w.Label
		applyWindow(&rates, w)
		break
	}
	q := Quote{
		Priced:      true,
		WindowLabel: label,
		Rates:       rates,
		CostNanoUSD: costNanoUSD(rates, u),
	}
	q.RatesJSON = ratesJSON(model, label, rates)
	return q
}

func (w *Window) matches(day time.Weekday, minute int) bool {
	current := MaskBit(day)
	previous := MaskBit((day + 6) % 7)
	switch {
	case w.EndMinute > w.StartMinute:
		return w.DaysMask&current != 0 &&
			w.StartMinute <= minute && minute < w.EndMinute
	case w.EndMinute < w.StartMinute:
		return (w.DaysMask&current != 0 && minute >= w.StartMinute) ||
			(w.DaysMask&previous != 0 && minute < w.EndMinute)
	default:
		return false
	}
}

// applyWindow overlays the window's non-nil rates. A nil window reasoning rate
// follows the merged output rate.
func applyWindow(rates *Rates, w *Window) {
	if w.Input != nil {
		rates.Input = *w.Input
	}
	if w.Output != nil {
		rates.Output = *w.Output
	}
	if w.CacheRead != nil {
		rates.CacheRead = *w.CacheRead
	}
	if w.CacheWrite != nil {
		rates.CacheWrite = *w.CacheWrite
	}
	if w.Reasoning != nil {
		rates.Reasoning = *w.Reasoning
	} else {
		rates.Reasoning = rates.Output
	}
}

// costNanoUSD converts token usage to nano-USD (USD * 1e9):
// tokens * USD-per-MTok * 1000. Negative components contribute nothing.
func costNanoUSD(rates Rates, u usage.Usage) int64 {
	sum := tokenCost(u.PromptTokens-u.CacheReadTokens, rates.Input) +
		tokenCost(u.CacheReadTokens, rates.CacheRead) +
		tokenCost(u.CacheWriteTokens, rates.CacheWrite) +
		tokenCost(u.CompletionTokens-u.ReasoningTokens, rates.Output) +
		tokenCost(u.ReasoningTokens, rates.Reasoning)
	if sum <= 0 {
		return 0
	}
	return int64(math.Round(sum * 1000))
}

func tokenCost(tokens int64, rate float64) float64 {
	if tokens <= 0 || rate <= 0 {
		return 0
	}
	return float64(tokens) * rate
}

type ratesSnapshot struct {
	Model             string          `json:"model"`
	Window            string          `json:"window"`
	InputPerMTok      json.RawMessage `json:"input_per_mtok"`
	OutputPerMTok     json.RawMessage `json:"output_per_mtok"`
	CacheReadPerMTok  json.RawMessage `json:"cache_read_per_mtok"`
	CacheWritePerMTok json.RawMessage `json:"cache_write_per_mtok"`
	ReasoningPerMTok  json.RawMessage `json:"reasoning_per_mtok"`
}

func ratesJSON(model, window string, r Rates) string {
	b, err := json.Marshal(ratesSnapshot{
		Model:             model,
		Window:            window,
		InputPerMTok:      rateValue(r.Input),
		OutputPerMTok:     rateValue(r.Output),
		CacheReadPerMTok:  rateValue(r.CacheRead),
		CacheWritePerMTok: rateValue(r.CacheWrite),
		ReasoningPerMTok:  rateValue(r.Reasoning),
	})
	if err != nil {
		return ""
	}
	return string(b)
}

// rateValue renders a rate with exactly six decimal places so snapshots stay
// stable and readable.
func rateValue(v float64) json.RawMessage {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		v = 0
	}
	return json.RawMessage(strconv.FormatFloat(v, 'f', 6, 64))
}
