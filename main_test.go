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

func TestServeArgsStripsSubcommand(t *testing.T) {
	got := serveArgs([]string{"serve", "-config", "x.yaml"})
	if len(got) != 2 || got[0] != "-config" || got[1] != "x.yaml" {
		t.Fatalf("got %q", got)
	}
	got = serveArgs([]string{"-config", "x.yaml"})
	if len(got) != 2 || got[0] != "-config" {
		t.Fatalf("got %q", got)
	}
}
