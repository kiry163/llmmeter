package main

import (
	"path/filepath"
	"testing"
)

func TestVersionFlag(t *testing.T) {
	if version == "" {
		t.Fatal("version must be set at build time or default")
	}
}

func TestServeRequiresConfig(t *testing.T) {
	if err := serve([]string{"-config", filepath.Join(t.TempDir(), "missing.yaml")}); err == nil {
		t.Fatal("expected error for missing config file")
	}
}
