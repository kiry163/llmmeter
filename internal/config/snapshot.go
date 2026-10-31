package config

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"time"

	"github.com/kiry163/llmmeter/internal/pricing"
)

// KeyBinding is a gateway key resolved against the compiled snapshot. Enabled
// carries the key's own flag; a disabled key is still indexable so the gateway
// can reject it with a specific error. Disabled upstreams are checked
// separately when routing.
type KeyBinding struct {
	KeyID        string
	KeyName      string
	UpstreamID   string
	UpstreamName string
	Upstream     *Upstream
	Table        *pricing.Table
	Enabled      bool
}

// Snapshot is an immutable, compiled view of one validated Config. Upstreams
// and Tables are keyed by upstream ID; Keys is keyed by the SHA-256 digest of
// the plaintext gateway key, so raw secrets are never kept in memory.
type Snapshot struct {
	Config    *Config
	Location  *time.Location
	Upstreams map[string]*Upstream
	Keys      map[[32]byte]KeyBinding
	Tables    map[string]*pricing.Table
}

// dayBits maps a config day name to its pricing days-mask bit, Monday is bit 0.
var dayBits = map[string]uint8{
	"mon": 1 << 0,
	"tue": 1 << 1,
	"wed": 1 << 2,
	"thu": 1 << 3,
	"fri": 1 << 4,
	"sat": 1 << 5,
	"sun": 1 << 6,
}

// Build validates cfg and compiles it into a Snapshot: the wall-clock
// location, the upstream index, the gateway key hash index and one pricing
// table per upstream. The caller must not mutate cfg after a successful Build;
// the snapshot holds pointers into it. An empty timezone resolves to UTC.
func Build(cfg *Config) (*Snapshot, error) {
	if cfg == nil {
		return nil, errors.New("config: build: nil config")
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	loc := time.UTC
	if cfg.General.Timezone != "" {
		l, err := time.LoadLocation(cfg.General.Timezone)
		if err != nil {
			return nil, fmt.Errorf("config: build: timezone %q: %w", cfg.General.Timezone, err)
		}
		loc = l
	}
	snap := &Snapshot{
		Config:    cfg,
		Location:  loc,
		Upstreams: make(map[string]*Upstream, len(cfg.Upstreams)),
		Keys:      make(map[[32]byte]KeyBinding, len(cfg.GatewayKeys)),
		Tables:    make(map[string]*pricing.Table, len(cfg.Upstreams)),
	}
	for i := range cfg.Upstreams {
		u := &cfg.Upstreams[i]
		table, err := compileTable(u)
		if err != nil {
			return nil, fmt.Errorf("config: build: upstream %q: %w", u.ID, err)
		}
		snap.Upstreams[u.ID] = u
		snap.Tables[u.ID] = table
	}
	for _, k := range cfg.GatewayKeys {
		u := snap.Upstreams[k.UpstreamID]
		snap.Keys[sha256.Sum256([]byte(k.Key))] = KeyBinding{
			KeyID:        k.ID,
			KeyName:      k.Name,
			UpstreamID:   k.UpstreamID,
			UpstreamName: u.Name,
			Upstream:     u,
			Table:        snap.Tables[k.UpstreamID],
			Enabled:      k.Active(),
		}
	}
	return snap, nil
}

// LookupKey resolves a raw gateway key. Disabled keys still match; the caller
// decides what a disabled binding means.
func (s *Snapshot) LookupKey(raw string) (KeyBinding, bool) {
	if s == nil {
		return KeyBinding{}, false
	}
	b, ok := s.Keys[sha256.Sum256([]byte(raw))]
	return b, ok
}

// Upstream returns the upstream with the given ID.
func (s *Snapshot) Upstream(id string) (*Upstream, bool) {
	if s == nil {
		return nil, false
	}
	u, ok := s.Upstreams[id]
	return u, ok
}

// compileTable converts one upstream's config models into a price table.
func compileTable(u *Upstream) (*pricing.Table, error) {
	models := make([]pricing.Model, len(u.Models))
	for i, m := range u.Models {
		var err error
		models[i], err = compileModel(m)
		if err != nil {
			return nil, fmt.Errorf("model %q: %w", m.Name, err)
		}
	}
	return pricing.NewTable(models), nil
}

func compileModel(m Model) (pricing.Model, error) {
	out := pricing.Model{
		Name:       m.Name,
		Input:      m.Input,
		Output:     m.Output,
		CacheRead:  m.CacheRead,
		CacheWrite: m.CacheWrite,
		Reasoning:  m.Reasoning,
		Windows:    make([]pricing.Window, len(m.Windows)),
	}
	for i, w := range m.Windows {
		cw, err := compileWindow(w)
		if err != nil {
			return pricing.Model{}, fmt.Errorf("window %q: %w", w.Label, err)
		}
		out.Windows[i] = cw
	}
	return out, nil
}

func compileWindow(w Window) (pricing.Window, error) {
	out := pricing.Window{
		Label:      w.Label,
		Input:      w.Input,
		Output:     w.Output,
		CacheRead:  w.CacheRead,
		CacheWrite: w.CacheWrite,
		Reasoning:  w.Reasoning,
	}
	for _, d := range w.Days {
		out.DaysMask |= dayBits[d]
	}
	var err error
	if out.StartMinute, err = clockMinutes(w.Start); err != nil {
		return pricing.Window{}, fmt.Errorf("start: %w", err)
	}
	if out.EndMinute, err = clockMinutes(w.End); err != nil {
		return pricing.Window{}, fmt.Errorf("end: %w", err)
	}
	return out, nil
}

// clockMinutes parses a validated HH:MM clock into minutes since midnight.
func clockMinutes(s string) (int, error) {
	t, err := time.Parse("15:04", s)
	if err != nil {
		return 0, err
	}
	return t.Hour()*60 + t.Minute(), nil
}
