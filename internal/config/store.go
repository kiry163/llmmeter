package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"

	"gopkg.in/yaml.v3"
)

// Store owns config.yaml and the current compiled configuration snapshot. All
// writes are serialized and land in the file before the in-memory snapshot
// changes, so readers always observe a complete, validated snapshot. The
// snapshot is immutable: Config returns a deep copy of its configuration.
type Store struct {
	mu       sync.Mutex
	path     string
	snap     atomic.Pointer[Snapshot]
	writable atomic.Bool
}

// fileHeader is written to every generated configuration file.
const fileHeader = "# llmmeter configuration. This file contains secrets; keep it private.\n" +
	"# It is rewritten on save, so hand-written comments are not preserved.\n"

// Option configures OpenStore.
type Option func(*storeOptions)

type storeOptions struct {
	database string
}

// WithEffectiveDatabase records db as server.database when OpenStore starts
// from a missing configuration file. Resolving the database from
// $XDG_CACHE_HOME and persisting that absolute path is what keeps the first
// save from materializing the "~/.cache/..." literal, which would shadow the
// environment on the next start. An existing configuration file always wins
// and ignores the option. An empty db is ignored.
func WithEffectiveDatabase(db string) Option {
	return func(o *storeOptions) {
		o.database = db
	}
}

// OpenStore loads the configuration at path. A missing file is a legal
// "setup mode" state: the defaults are used, Writable reports false and no
// file is created until the first successful Update. An existing file that
// fails to parse or validate makes OpenStore fail.
func OpenStore(path string, opts ...Option) (*Store, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("config: store path must not be empty")
	}
	var options storeOptions
	for _, opt := range opts {
		if opt != nil {
			opt(&options)
		}
	}
	if err := ensureDir(filepath.Dir(path)); err != nil {
		return nil, err
	}
	cfg, exists, err := readStoreFile(path)
	if err != nil {
		return nil, err
	}
	if exists {
		tightenPermissions(path)
	} else if options.database != "" {
		cfg.Server.Database = options.database
	}
	snap, err := Build(cfg)
	if err != nil {
		return nil, err
	}
	st := &Store{path: path}
	st.snap.Store(snap)
	st.writable.Store(exists)
	return st, nil
}

// Config returns a deep copy of the current configuration. The copy is safe
// for the caller to mutate.
func (s *Store) Config() *Config {
	return s.snap.Load().Config.clone()
}

// Snapshot returns the current compiled snapshot. It is immutable and shared:
// callers must not mutate it or the configuration it points at.
func (s *Store) Snapshot() *Snapshot {
	return s.snap.Load()
}

// Writable reports whether the configuration has been persisted: false in
// setup mode, true once the file exists or an Update has written it.
func (s *Store) Writable() bool {
	return s.writable.Load()
}

// Path returns the configuration file path the store reads and writes.
func (s *Store) Path() string {
	return s.path
}

// Update applies fn to a deep copy of the current configuration, validates the
// result and then replaces both the file and the in-memory configuration. Any
// error, including a validation failure, leaves the previous configuration and
// the previous file untouched. The file is replaced atomically: temp file,
// fsync, rename, then a best-effort directory fsync.
func (s *Store) Update(fn func(*Config) error) error {
	if fn == nil {
		return errors.New("config: update: nil mutation")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	next := s.snap.Load().Config.clone()
	if err := fn(next); err != nil {
		return err
	}
	snap, err := Build(next)
	if err != nil {
		return err
	}
	data, err := marshalConfig(next)
	if err != nil {
		return err
	}
	if err := writeFileAtomic(s.path, data, 0o600); err != nil {
		return err
	}
	s.snap.Store(snap)
	s.writable.Store(true)
	return nil
}

// Reload re-reads and re-validates the configuration file. On failure the old
// configuration is kept and the error returned.
func (s *Store) Reload() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	next, exists, err := readStoreFile(s.path)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("config: reload %s: %w", s.path, fs.ErrNotExist)
	}
	snap, err := Build(next)
	if err != nil {
		return err
	}
	s.snap.Store(snap)
	s.writable.Store(true)
	return nil
}

// readStoreFile parses and validates the file at path. A missing file returns
// the defaults with exists false.
func readStoreFile(path string) (*Config, bool, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Default(), false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("config: read %s: %w", path, err)
	}
	cfg, err := Parse(data)
	if err != nil {
		return nil, false, fmt.Errorf("config: load %s: %w", path, err)
	}
	if err := cfg.Validate(); err != nil {
		return nil, false, fmt.Errorf("config: load %s: %w", path, err)
	}
	return cfg, true, nil
}

// tightenPermissions best-effort restricts an existing configuration file to
// 0600. Failures are ignored: read-only configurations must stay loadable.
func tightenPermissions(path string) {
	info, err := os.Stat(path)
	if err != nil {
		return
	}
	if info.Mode().Perm()&0o077 != 0 {
		_ = os.Chmod(path, 0o600)
	}
}

// marshalConfig serializes cfg with the fixed file header.
func marshalConfig(cfg *Config) ([]byte, error) {
	body, err := yaml.Marshal(cfg)
	if err != nil {
		return nil, fmt.Errorf("config: marshal: %w", err)
	}
	return append([]byte(fileHeader), body...), nil
}

// writeFileAtomic writes data to path through a temporary file in the same
// directory so a crash never leaves a partially written configuration.
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("config: write %s: %w", path, err)
	}
	tmp, err := os.CreateTemp(dir, ".config-*.tmp")
	if err != nil {
		return fmt.Errorf("config: write %s: %w", path, err)
	}
	tmpName := tmp.Name()
	committed := false
	defer func() {
		if !committed {
			_ = tmp.Close()
			_ = os.Remove(tmpName)
		}
	}()

	if err := tmp.Chmod(perm); err != nil {
		return fmt.Errorf("config: write %s: %w", path, err)
	}
	if _, err := tmp.Write(data); err != nil {
		return fmt.Errorf("config: write %s: %w", path, err)
	}
	if err := tmp.Sync(); err != nil {
		return fmt.Errorf("config: write %s: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("config: write %s: %w", path, err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("config: write %s: %w", path, err)
	}
	committed = true
	// The rename is durable once the directory entry is flushed. This is best
	// effort: some filesystems and platforms do not support it.
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}

// clone returns a deep copy of c.
func (c *Config) clone() *Config {
	if c == nil {
		return nil
	}
	out := *c
	if c.Upstreams != nil {
		out.Upstreams = make([]Upstream, len(c.Upstreams))
		for i, u := range c.Upstreams {
			out.Upstreams[i] = u.clone()
		}
	}
	if c.GatewayKeys != nil {
		out.GatewayKeys = make([]GatewayKey, len(c.GatewayKeys))
		for i, k := range c.GatewayKeys {
			out.GatewayKeys[i] = k.clone()
		}
	}
	return &out
}

func (u Upstream) clone() Upstream {
	out := u
	out.Enabled = clonePtr(u.Enabled)
	if u.Models != nil {
		out.Models = make([]Model, len(u.Models))
		for i, m := range u.Models {
			out.Models[i] = m.clone()
		}
	}
	return out
}

func (k GatewayKey) clone() GatewayKey {
	out := k
	out.Enabled = clonePtr(k.Enabled)
	return out
}

func (m Model) clone() Model {
	out := m
	out.Reasoning = clonePtr(m.Reasoning)
	if m.Windows != nil {
		out.Windows = make([]Window, len(m.Windows))
		for i, w := range m.Windows {
			out.Windows[i] = w.clone()
		}
	}
	return out
}

func (w Window) clone() Window {
	out := w
	out.Days = slices.Clone(w.Days)
	out.Input = clonePtr(w.Input)
	out.Output = clonePtr(w.Output)
	out.CacheRead = clonePtr(w.CacheRead)
	out.CacheWrite = clonePtr(w.CacheWrite)
	out.Reasoning = clonePtr(w.Reasoning)
	return out
}

func clonePtr[T any](p *T) *T {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}
