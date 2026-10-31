package secure_test

import (
	"strings"
	"testing"

	"github.com/kiry163/llmmeter/internal/secure"
)

func TestPasswordRoundTrip(t *testing.T) {
	h, err := secure.HashPassword("correct horse")
	if err != nil {
		t.Fatal(err)
	}
	if !secure.VerifyPassword(h, "correct horse") {
		t.Fatal("password did not verify")
	}
	if secure.VerifyPassword(h, "wrong") {
		t.Fatal("wrong password verified")
	}
}

func TestVerifyPasswordRejectsMalformed(t *testing.T) {
	for _, encoded := range []string{
		"",
		"plaintext",
		"$argon2i$v=19$m=65536,t=3,p=2$c2FsdA$aGFzaA",
		"$argon2id$v=19$m=0,t=3,p=2$c2FsdA$aGFzaA",
		"$argon2id$v=19$m=65536,t=3,p=2$!!!$aGFzaA",
	} {
		if secure.VerifyPassword(encoded, "whatever") {
			t.Fatalf("malformed hash verified: %q", encoded)
		}
	}
}

func TestGatewayKeyShape(t *testing.T) {
	k := secure.NewGatewayKey()
	if !strings.HasPrefix(k, "sk-local-") || len(k) < 20 {
		t.Fatalf("key = %q", k)
	}
	if len(k) != len("sk-local-")+32 {
		t.Fatalf("key length = %d, want %d", len(k), len("sk-local-")+32)
	}
}

func TestRandomSecret(t *testing.T) {
	a, err := secure.RandomSecret()
	if err != nil {
		t.Fatal(err)
	}
	b, err := secure.RandomSecret()
	if err != nil {
		t.Fatal(err)
	}
	if len(a) != 32 || len(b) != 32 {
		t.Fatalf("secret lengths = %d, %d", len(a), len(b))
	}
	if string(a) == string(b) {
		t.Fatal("two secrets were identical")
	}
}
