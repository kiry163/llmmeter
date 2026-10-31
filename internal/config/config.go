// Package config defines the llmmeter YAML configuration schema, its
// defaults, strict parsing, and validation. Configuration is the single
// source of truth; the database holds usage data only.
package config

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

// Defaults applied by Default and Parse.
const (
	DefaultListen        = "127.0.0.1:8100"
	DefaultDatabase      = "~/.cache/llmmeter/llmmeter.db"
	DefaultRetentionDays = 90
)

// Validation bounds from the design spec.
const (
	minRetentionDays = 7
	maxRetentionDays = 3650
	maxNameLen       = 100
	maxModelNameLen  = 200
	minGatewayKeyLen = 16
	maxWindows       = 32
	sessionSecretLen = 32
)

var (
	slugPattern  = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)
	clockPattern = regexp.MustCompile(`^(?:[01][0-9]|2[0-3]):[0-5][0-9]$`)
	validDays    = map[string]bool{
		"mon": true, "tue": true, "wed": true, "thu": true,
		"fri": true, "sat": true, "sun": true,
	}
)

// Config is the root of config.yaml.
type Config struct {
	Version     int          `yaml:"version"`
	Server      Server       `yaml:"server"`
	General     General      `yaml:"general"`
	Auth        Auth         `yaml:"auth"`
	Upstreams   []Upstream   `yaml:"upstreams"`
	GatewayKeys []GatewayKey `yaml:"gateway_keys"`
}

// Server holds process-level listener and database settings.
type Server struct {
	Listen   string `yaml:"listen"`
	Database string `yaml:"database"`
}

// General holds reporting settings.
type General struct {
	Timezone      string `yaml:"timezone"`
	RetentionDays int    `yaml:"retention_days"`
}

// Auth holds the admin password hash and session signing secret. Empty
// values mean "not configured".
type Auth struct {
	AdminPasswordHash string `yaml:"admin_password_hash"`
	SessionSecret     string `yaml:"session_secret"`
}

// Upstream is an OpenAI-compatible upstream provider.
type Upstream struct {
	ID      string  `yaml:"id"`
	Name    string  `yaml:"name"`
	BaseURL string  `yaml:"base_url"`
	APIKey  string  `yaml:"api_key"`
	Enabled *bool   `yaml:"enabled"`
	Models  []Model `yaml:"models"`
}

// GatewayKey is a local key bound to one upstream. Key is the plaintext
// secret clients present; it is globally unique.
type GatewayKey struct {
	ID         string `yaml:"id"`
	Name       string `yaml:"name"`
	Key        string `yaml:"key"`
	UpstreamID string `yaml:"upstream_id"`
	Enabled    *bool  `yaml:"enabled"`
}

// Model holds base rates in USD per million tokens. Nil Reasoning means
// "charge reasoning at the output rate".
type Model struct {
	Name       string   `yaml:"name"`
	Input      float64  `yaml:"input"`
	Output     float64  `yaml:"output"`
	CacheRead  float64  `yaml:"cache_read"`
	CacheWrite float64  `yaml:"cache_write"`
	Reasoning  *float64 `yaml:"reasoning"`
	Windows    []Window `yaml:"windows"`
}

// Window is a repeating local wall-clock price window. Nil rates inherit the
// model base rate. End before Start means the window crosses midnight.
type Window struct {
	Label      string   `yaml:"label"`
	Days       []string `yaml:"days"`
	Start      string   `yaml:"start"`
	End        string   `yaml:"end"`
	Input      *float64 `yaml:"input"`
	Output     *float64 `yaml:"output"`
	CacheRead  *float64 `yaml:"cache_read"`
	CacheWrite *float64 `yaml:"cache_write"`
	Reasoning  *float64 `yaml:"reasoning"`
}

// Active reports whether the upstream is enabled. An omitted enabled field
// means active.
func (u Upstream) Active() bool { return u.Enabled == nil || *u.Enabled }

// Active reports whether the gateway key is enabled. An omitted enabled field
// means active.
func (k GatewayKey) Active() bool { return k.Enabled == nil || *k.Enabled }

// Default returns a Config with the documented defaults. The result passes
// Validate. The caller fills the timezone, which has no safe cross-platform
// default.
func Default() *Config {
	return &Config{
		Version: 1,
		Server:  Server{Listen: DefaultListen, Database: DefaultDatabase},
		General: General{RetentionDays: DefaultRetentionDays},
	}
}

// Parse strictly decodes YAML over the defaults. Unknown fields, duplicate
// keys and type errors are rejected. Any trailing YAML document is rejected
// too, even an empty one after a bare "---". Parsing does not validate; call
// Validate on the result.
func Parse(data []byte) (*Config, error) {
	c := Default()
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	err := dec.Decode(c)
	if errors.Is(err, io.EOF) {
		return c, nil
	}
	if err != nil {
		return nil, fmt.Errorf("config: parse: %w", err)
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		if err != nil {
			return nil, fmt.Errorf("config: parse: %w", err)
		}
		return nil, errors.New("config: parse: multiple YAML documents")
	}
	return c, nil
}

// ValidationError reports every invalid field keyed by its YAML path.
type ValidationError struct {
	Fields map[string]string
}

// Error renders the field errors in stable path order.
func (e *ValidationError) Error() string {
	keys := make([]string, 0, len(e.Fields))
	for k := range e.Fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+": "+e.Fields[k])
	}
	return "config: invalid: " + strings.Join(parts, "; ")
}

type fieldErrors map[string]string

func (e fieldErrors) add(field, format string, args ...any) {
	if _, ok := e[field]; ok {
		return
	}
	e[field] = fmt.Sprintf(format, args...)
}

// Validate checks the full spec 4.3 rule set and returns a *ValidationError
// listing every violation. It also normalizes upstream base URLs by dropping
// trailing slashes.
func (c *Config) Validate() error {
	errs := fieldErrors{}

	if c.Version != 1 {
		errs.add("version", "must be 1")
	}

	if err := checkListen(c.Server.Listen); err != nil {
		errs.add("server.listen", "%v", err)
	} else if !isLoopbackListen(c.Server.Listen) && c.Auth.AdminPasswordHash == "" {
		errs.add("server.listen", "non-loopback listen requires auth.admin_password_hash")
	}
	if strings.TrimSpace(c.Server.Database) == "" {
		errs.add("server.database", "must not be empty")
	}

	if c.General.Timezone != "" {
		if _, err := time.LoadLocation(c.General.Timezone); err != nil {
			errs.add("general.timezone", "unknown timezone %q", c.General.Timezone)
		}
	}
	if c.General.RetentionDays < minRetentionDays || c.General.RetentionDays > maxRetentionDays {
		errs.add("general.retention_days", "must be between %d and %d", minRetentionDays, maxRetentionDays)
	}

	if c.Auth.AdminPasswordHash != "" && !validPHC(c.Auth.AdminPasswordHash) {
		errs.add("auth.admin_password_hash", "must be an Argon2id PHC string")
	}
	if c.Auth.SessionSecret != "" && !validSessionSecret(c.Auth.SessionSecret) {
		errs.add("auth.session_secret", "must be base64 for %d bytes", sessionSecretLen)
	}

	upstreamIDs := make(map[string]int, len(c.Upstreams))
	for i := range c.Upstreams {
		u := &c.Upstreams[i]
		base := fmt.Sprintf("upstreams[%d]", i)
		if !slugPattern.MatchString(u.ID) {
			errs.add(base+".id", "must match [a-z0-9][a-z0-9-]{0,31}")
		} else if prev, ok := upstreamIDs[u.ID]; ok {
			errs.add(base+".id", "duplicate of upstreams[%d].id", prev)
		} else {
			upstreamIDs[u.ID] = i
		}
		if err := checkName(u.Name, "upstream name"); err != nil {
			errs.add(base+".name", "%v", err)
		}
		if err := checkBaseURL(u.BaseURL); err != nil {
			errs.add(base+".base_url", "%v", err)
		} else {
			u.BaseURL = strings.TrimRight(u.BaseURL, "/")
		}
		validateModels(base+".models", u.Models, errs)
	}

	keyIDs := make(map[string]int, len(c.GatewayKeys))
	keyValues := make(map[string]int, len(c.GatewayKeys))
	for i := range c.GatewayKeys {
		k := &c.GatewayKeys[i]
		base := fmt.Sprintf("gateway_keys[%d]", i)
		if !slugPattern.MatchString(k.ID) {
			errs.add(base+".id", "must match [a-z0-9][a-z0-9-]{0,31}")
		} else if prev, ok := keyIDs[k.ID]; ok {
			errs.add(base+".id", "duplicate of gateway_keys[%d].id", prev)
		} else {
			keyIDs[k.ID] = i
		}
		if err := checkName(k.Name, "key name"); err != nil {
			errs.add(base+".name", "%v", err)
		}
		if len(k.Key) < minGatewayKeyLen {
			errs.add(base+".key", "must be at least %d characters", minGatewayKeyLen)
		} else if prev, ok := keyValues[k.Key]; ok {
			errs.add(base+".key", "duplicate of gateway_keys[%d].key", prev)
		} else {
			keyValues[k.Key] = i
		}
		if k.UpstreamID == "" {
			errs.add(base+".upstream_id", "must reference an upstream")
		} else if _, ok := upstreamIDs[k.UpstreamID]; !ok {
			errs.add(base+".upstream_id", "unknown upstream %q", k.UpstreamID)
		}
	}

	if len(errs) > 0 {
		return &ValidationError{Fields: errs}
	}
	return nil
}

func validateModels(base string, models []Model, errs fieldErrors) {
	names := make(map[string]int, len(models))
	for i := range models {
		m := &models[i]
		path := fmt.Sprintf("%s[%d]", base, i)
		if m.Name == "" || strings.TrimSpace(m.Name) == "" {
			errs.add(path+".name", "must not be empty")
		} else if utf8.RuneCountInString(m.Name) > maxModelNameLen {
			errs.add(path+".name", "must be at most %d characters", maxModelNameLen)
		} else if prev, ok := names[m.Name]; ok {
			errs.add(path+".name", "duplicate of %s[%d].name", base, prev)
		} else {
			names[m.Name] = i
		}
		checkRate(errs, path+".input", m.Input)
		checkRate(errs, path+".output", m.Output)
		checkRate(errs, path+".cache_read", m.CacheRead)
		checkRate(errs, path+".cache_write", m.CacheWrite)
		checkOptionalRate(errs, path+".reasoning", m.Reasoning)

		if len(m.Windows) > maxWindows {
			errs.add(path+".windows", "must have at most %d windows", maxWindows)
		}
		for j := range m.Windows {
			validateWindow(fmt.Sprintf("%s[%d]", path+".windows", j), &m.Windows[j], errs)
		}
	}
}

func validateWindow(path string, w *Window, errs fieldErrors) {
	if strings.TrimSpace(w.Label) == "" {
		errs.add(path+".label", "must not be empty")
	}

	seen := make(map[string]bool, len(w.Days))
	for _, d := range w.Days {
		if !validDays[d] {
			errs.add(path+".days", "unknown day %q, want mon..sun", d)
			continue
		}
		if seen[d] {
			errs.add(path+".days", "duplicate day %q", d)
		}
		seen[d] = true
	}
	if len(w.Days) == 0 {
		errs.add(path+".days", "must not be empty")
	}

	if !clockPattern.MatchString(w.Start) {
		errs.add(path+".start", "must be HH:MM")
	}
	if !clockPattern.MatchString(w.End) {
		errs.add(path+".end", "must be HH:MM")
	} else if w.End == w.Start {
		errs.add(path+".end", "must differ from start")
	}

	checkOptionalRate(errs, path+".input", w.Input)
	checkOptionalRate(errs, path+".output", w.Output)
	checkOptionalRate(errs, path+".cache_read", w.CacheRead)
	checkOptionalRate(errs, path+".cache_write", w.CacheWrite)
	checkOptionalRate(errs, path+".reasoning", w.Reasoning)
}

func checkRate(errs fieldErrors, field string, v float64) {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		errs.add(field, "must be finite")
		return
	}
	if v < 0 {
		errs.add(field, "must be >= 0")
	}
}

func checkOptionalRate(errs fieldErrors, field string, v *float64) {
	if v != nil {
		checkRate(errs, field, *v)
	}
}

func checkName(name, kind string) error {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return fmt.Errorf("%s must not be empty", kind)
	}
	if utf8.RuneCountInString(trimmed) > maxNameLen {
		return fmt.Errorf("%s must be at most %d characters", kind, maxNameLen)
	}
	return nil
}

func checkBaseURL(raw string) error {
	v := strings.TrimSpace(raw)
	if v == "" {
		return errors.New("must not be empty")
	}
	u, err := url.Parse(v)
	if err != nil || u.Host == "" {
		return errors.New("must be an absolute URL with a host")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return errors.New("scheme must be http or https")
	}
	return nil
}

func checkListen(listen string) error {
	host, port, err := net.SplitHostPort(listen)
	if err != nil {
		return errors.New("must be host:port")
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return errors.New("port must be 1..65535")
	}
	if strings.TrimSpace(host) != host {
		return errors.New("must be host:port")
	}
	return nil
}

func isLoopbackListen(listen string) bool {
	host, _, err := net.SplitHostPort(listen)
	if err != nil {
		return false
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return false
}

// validPHC reports whether s is an Argon2id PHC string with sane parameters.
// It does not verify any password.
func validPHC(s string) bool {
	parts := strings.Split(s, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" || parts[2] != "v=19" {
		return false
	}
	seen := map[string]bool{}
	for _, f := range strings.Split(parts[3], ",") {
		k, v, ok := strings.Cut(f, "=")
		if !ok {
			return false
		}
		switch k {
		case "m", "t", "p":
		default:
			return false
		}
		if seen[k] {
			return false
		}
		seen[k] = true
		bits := 32
		if k == "p" {
			// The runtime stores parallelism in a uint8.
			bits = 8
		}
		n, err := strconv.ParseUint(v, 10, bits)
		if err != nil || n == 0 {
			return false
		}
	}
	if len(seen) != 3 {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil || len(salt) < 8 {
		return false
	}
	hash, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(hash) < 4 {
		return false
	}
	return true
}

func validSessionSecret(s string) bool {
	raw, err := decodeBase64(s)
	return err == nil && len(raw) == sessionSecretLen
}

func decodeBase64(s string) ([]byte, error) {
	if raw, err := base64.RawStdEncoding.DecodeString(s); err == nil {
		return raw, nil
	}
	return base64.StdEncoding.DecodeString(s)
}
