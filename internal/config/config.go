package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type Duration time.Duration

func (d *Duration) Std() time.Duration { return time.Duration(*d) }

func (d *Duration) UnmarshalYAML(n *yaml.Node) error {
	var s string
	if err := n.Decode(&s); err != nil {
		return err
	}
	v, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("duration %q: %w", s, err)
	}
	*d = Duration(v)
	return nil
}

type Config struct {
	Listen     string   `yaml:"listen"`
	GatewayKey string   `yaml:"gateway_key"`
	Timezone   string   `yaml:"timezone"`
	Upstream   Upstream `yaml:"upstream"`
	Pricing    Pricing  `yaml:"pricing"`
}

type Upstream struct {
	BaseURL string   `yaml:"base_url"`
	APIKey  string   `yaml:"api_key"`
	Timeout Duration `yaml:"timeout"`
}

type Pricing struct {
	Models map[string]Model `yaml:"models"`
}

type Model struct {
	InputPerMTok      float64  `yaml:"input_per_mtok"`
	OutputPerMTok     float64  `yaml:"output_per_mtok"`
	CacheReadPerMTok  float64  `yaml:"cache_read_per_mtok"`
	CacheWritePerMTok float64  `yaml:"cache_write_per_mtok"`
	ReasoningPerMTok  float64  `yaml:"reasoning_per_mtok"`
	Windows           []Window `yaml:"windows"`
}

type Window struct {
	Label             string   `yaml:"label"`
	Days              []string `yaml:"days"`
	Start             string   `yaml:"start"`
	End               string   `yaml:"end"`
	InputPerMTok      *float64 `yaml:"input_per_mtok"`
	OutputPerMTok     *float64 `yaml:"output_per_mtok"`
	CacheReadPerMTok  *float64 `yaml:"cache_read_per_mtok"`
	CacheWritePerMTok *float64 `yaml:"cache_write_per_mtok"`
	ReasoningPerMTok  *float64 `yaml:"reasoning_per_mtok"`
}

var envPattern = regexp.MustCompile(`\$\{env:([A-Za-z_][A-Za-z0-9_]*)\}`)

func expandEnv(s string) (string, error) {
	var missing []string
	out := envPattern.ReplaceAllStringFunc(s, func(m string) string {
		name := envPattern.FindStringSubmatch(m)[1]
		v, ok := os.LookupEnv(name)
		if !ok {
			missing = append(missing, name)
			return ""
		}
		return v
	})
	if len(missing) > 0 {
		return "", fmt.Errorf("missing environment variables: %s", strings.Join(missing, ", "))
	}
	return out, nil
}

func Load(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	text, err := expandEnv(string(raw))
	if err != nil {
		return nil, err
	}
	cfg := &Config{
		Listen:   "127.0.0.1:8100",
		Timezone: "Asia/Shanghai",
	}
	dec := yaml.NewDecoder(bytes.NewReader([]byte(text)))
	dec.KnownFields(true)
	if err := dec.Decode(cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	if cfg.GatewayKey == "" {
		return nil, errors.New("gateway_key is required")
	}
	if cfg.Upstream.BaseURL == "" || cfg.Upstream.APIKey == "" {
		return nil, errors.New("upstream.base_url and upstream.api_key are required")
	}
	if cfg.Upstream.Timeout == 0 {
		cfg.Upstream.Timeout = Duration(300 * time.Second)
	}
	if _, err := time.LoadLocation(cfg.Timezone); err != nil {
		return nil, fmt.Errorf("timezone %q: %w", cfg.Timezone, err)
	}
	for name, m := range cfg.Pricing.Models {
		for i, w := range m.Windows {
			if _, _, err := ParseClock(w.Start); err != nil {
				return nil, fmt.Errorf("model %s window %d start: %w", name, i, err)
			}
			if _, _, err := ParseClock(w.End); err != nil {
				return nil, fmt.Errorf("model %s window %d end: %w", name, i, err)
			}
			if w.Label == "" {
				return nil, fmt.Errorf("model %s window %d: label is required", name, i)
			}
			for _, d := range w.Days {
				if _, ok := Weekday(d); !ok {
					return nil, fmt.Errorf("model %s window %d: bad day %q", name, i, d)
				}
			}
		}
	}
	return cfg, nil
}

// ParseClock parses "HH:MM" (24h) into hours and minutes.
func ParseClock(s string) (h, m int, err error) {
	if len(s) != 5 || s[2] != ':' {
		return 0, 0, fmt.Errorf("clock %q must be HH:MM", s)
	}
	if _, err := fmt.Sscanf(s, "%02d:%02d", &h, &m); err != nil {
		return 0, 0, fmt.Errorf("clock %q: %w", s, err)
	}
	if h < 0 || h > 24 || m < 0 || m > 59 || (h == 24 && m != 0) {
		return 0, 0, fmt.Errorf("clock %q out of range", s)
	}
	return h, m, nil
}

var weekdays = map[string]time.Weekday{
	"sun": time.Sunday, "mon": time.Monday, "tue": time.Tuesday, "wed": time.Wednesday,
	"thu": time.Thursday, "fri": time.Friday, "sat": time.Saturday,
}

func Weekday(s string) (time.Weekday, bool) {
	d, ok := weekdays[strings.ToLower(s)]
	return d, ok
}
