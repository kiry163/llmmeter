package main

import (
	"testing"

	"github.com/kiry163/llmmeter/internal/settings"
)

func TestValidateStartupNonLoopbackRequiresPassword(t *testing.T) {
	snap := &settings.Snapshot{AdminPasswordHash: ""}
	if err := validateStartup("0.0.0.0:8100", snap); err == nil {
		t.Fatal("must refuse non-loopback without password")
	}
	snap.AdminPasswordHash = "$argon2id$..."
	if err := validateStartup("0.0.0.0:8100", snap); err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if err := validateStartup("127.0.0.1:8100", &settings.Snapshot{}); err != nil {
		t.Fatalf("loopback must pass: %v", err)
	}

	for _, listen := range []string{"localhost:8100", "[::1]:8100", "127.0.0.2:8100"} {
		if err := validateStartup(listen, &settings.Snapshot{}); err != nil {
			t.Fatalf("%s must pass without password: %v", listen, err)
		}
	}
	for _, listen := range []string{":8100", "[::]:8100", "192.168.1.10:8100", "example.com:8100"} {
		if err := validateStartup(listen, &settings.Snapshot{}); err == nil {
			t.Fatalf("%s must be refused without password", listen)
		}
	}
}
