package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Paths are the resolved process locations and listen address.
type Paths struct {
	Config   string
	Database string
	Listen   string
}

// ResolvePaths resolves the configuration file, database and listen address
// from command-line flags, the configuration file and built-in defaults, in
// that order of precedence. An empty flag means "not set". A missing
// configuration file is legal and yields the defaults.
//
// The database may be written as "~/..." or relative to the configuration
// file's directory; Paths.Database is always absolute. The configuration and
// database directories are created with mode 0700 when missing.
func ResolvePaths(configFlag, dbFlag, listenFlag string) (Paths, error) {
	configPath, err := resolveConfigPath(configFlag)
	if err != nil {
		return Paths{}, err
	}
	cfg, present, err := readConfigFile(configPath)
	if err != nil {
		return Paths{}, err
	}

	db := dbFlag
	var dbValue string
	if present.Server.Database != nil {
		dbValue = strings.TrimSpace(*present.Server.Database)
	}
	switch {
	case db != "":
		// A flag path is relative to the working directory.
		db, err = expandPath(db, ".")
	case dbValue != "":
		// A configured database is relative to the configuration file.
		db, err = expandPath(dbValue, filepath.Dir(configPath))
	default:
		db, err = defaultDatabasePath()
	}
	if err != nil {
		return Paths{}, err
	}
	// Persist and run with an absolute database path. A relative one would be
	// interpreted against the working directory now but against the config
	// directory on the next start (where server.database is relative to the
	// file), silently opening a different database.
	db, err = filepath.Abs(db)
	if err != nil {
		return Paths{}, fmt.Errorf("config: resolve database path %q: %w", db, err)
	}

	listen := listenFlag
	if listen == "" {
		listen = cfg.Server.Listen
	}

	paths := Paths{Config: configPath, Database: db, Listen: listen}
	if err := ensureDir(filepath.Dir(configPath)); err != nil {
		return Paths{}, err
	}
	if err := ensureDir(filepath.Dir(db)); err != nil {
		return Paths{}, err
	}
	return paths, nil
}

// filePresence records which overridable fields the configuration file set
// explicitly. It distinguishes an omitted server.database from the same value
// supplied by the built-in default, so XDG_CACHE_HOME still wins when the
// file is silent.
type filePresence struct {
	Server struct {
		Database *string `yaml:"database"`
	} `yaml:"server"`
}

// readConfigFile loads the configuration at path, returning the defaults when
// the file does not exist.
func readConfigFile(path string) (*Config, filePresence, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Default(), filePresence{}, nil
	}
	if err != nil {
		return nil, filePresence{}, fmt.Errorf("config: read %s: %w", path, err)
	}
	cfg, err := Parse(data)
	if err != nil {
		return nil, filePresence{}, fmt.Errorf("config: load %s: %w", path, err)
	}
	var present filePresence
	if err := yaml.Unmarshal(data, &present); err != nil {
		return nil, filePresence{}, fmt.Errorf("config: parse %s: %w", path, err)
	}
	return cfg, present, nil
}

// resolveConfigPath applies -config, $XDG_CONFIG_HOME and ~/.config in order.
func resolveConfigPath(flag string) (string, error) {
	if flag != "" {
		return expandPath(flag, ".")
	}
	if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" {
		return filepath.Join(dir, "llmmeter", "config.yaml"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("config: locate config directory: %w", err)
	}
	return filepath.Join(home, ".config", "llmmeter", "config.yaml"), nil
}

// defaultDatabasePath applies $XDG_CACHE_HOME and ~/.cache.
func defaultDatabasePath() (string, error) {
	if dir := os.Getenv("XDG_CACHE_HOME"); dir != "" {
		return filepath.Join(dir, "llmmeter", "llmmeter.db"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("config: locate cache directory: %w", err)
	}
	return filepath.Join(home, ".cache", "llmmeter", "llmmeter.db"), nil
}

// expandPath expands a leading "~" and resolves a relative path against
// baseDir.
func expandPath(p, baseDir string) (string, error) {
	switch {
	case p == "~":
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("config: expand %q: %w", p, err)
		}
		p = home
	case strings.HasPrefix(p, "~/") || strings.HasPrefix(p, `~\`):
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("config: expand %q: %w", p, err)
		}
		p = filepath.Join(home, p[2:])
	case strings.HasPrefix(p, "~"):
		return "", fmt.Errorf("config: unsupported home reference %q", p)
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(baseDir, p)
	}
	return filepath.Clean(p), nil
}

// ensureDir creates dir with mode 0700 when missing. Existing directories keep
// their permissions; the config directory may be shared with other tools.
func ensureDir(dir string) error {
	if dir == "" {
		dir = "."
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("config: create %s: %w", dir, err)
	}
	return nil
}
