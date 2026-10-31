// Package secure holds the password hashing, gateway key and secret generation
// helpers shared by llmmeter's settings and API layers.
package secure

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/crypto/argon2"
)

// Argon2id parameters: 64 MiB, 3 passes, 2 lanes.
const (
	argonMemory  uint32 = 64 * 1024
	argonTime    uint32 = 3
	argonThreads uint8  = 2
	argonSaltLen        = 16
	argonKeyLen  uint32 = 32

	gatewayKeyPrefix = "sk-local-"
	gatewayKeyBytes  = 32
	secretBytes      = 32
)

// HashPassword derives an Argon2id PHC string for pw.
func HashPassword(pw string) (string, error) {
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("secure: generate salt: %w", err)
	}
	key := argon2.IDKey([]byte(pw), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemory, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key)), nil
}

// VerifyPassword reports whether pw matches the PHC-encoded Argon2id hash.
// Malformed hashes and mismatches both return false.
func VerifyPassword(encoded, pw string) bool {
	params, salt, want, err := decodePHC(encoded)
	if err != nil {
		return false
	}
	got := argon2.IDKey([]byte(pw), salt, params.time, params.memory, params.threads, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1
}

type argonParams struct {
	memory  uint32
	time    uint32
	threads uint8
}

// decodePHC parses "$argon2id$v=19$m=...,t=...,p=...$salt$hash".
func decodePHC(encoded string) (argonParams, []byte, []byte, error) {
	var p argonParams
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" {
		return p, nil, nil, fmt.Errorf("secure: unsupported password hash")
	}
	if parts[2] != "v="+strconv.Itoa(argon2.Version) {
		return p, nil, nil, fmt.Errorf("secure: unsupported argon2 version")
	}
	for _, f := range strings.Split(parts[3], ",") {
		k, v, ok := strings.Cut(f, "=")
		if !ok {
			return p, nil, nil, fmt.Errorf("secure: malformed argon2 parameters")
		}
		n, err := strconv.ParseUint(v, 10, 32)
		if err != nil {
			return p, nil, nil, fmt.Errorf("secure: malformed argon2 parameter %q", k)
		}
		switch k {
		case "m":
			p.memory = uint32(n)
		case "t":
			p.time = uint32(n)
		case "p":
			p.threads = uint8(n)
		default:
			return p, nil, nil, fmt.Errorf("secure: unknown argon2 parameter %q", k)
		}
	}
	if p.memory == 0 || p.time == 0 || p.threads == 0 {
		return p, nil, nil, fmt.Errorf("secure: incomplete argon2 parameters")
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return p, nil, nil, fmt.Errorf("secure: malformed argon2 salt")
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return p, nil, nil, fmt.Errorf("secure: malformed argon2 hash")
	}
	if len(salt) == 0 || len(want) == 0 {
		return p, nil, nil, fmt.Errorf("secure: empty argon2 salt or hash")
	}
	return p, salt, want, nil
}

// NewGatewayKey returns a fresh "sk-local-" key carrying 32 random bytes
// encoded as unpadded base64url.
func NewGatewayKey() string {
	b := make([]byte, gatewayKeyBytes)
	if _, err := rand.Read(b); err != nil {
		panic("secure: crypto/rand unavailable: " + err.Error())
	}
	return gatewayKeyPrefix + base64.RawURLEncoding.EncodeToString(b)
}

// RandomSecret returns 32 cryptographically random bytes.
func RandomSecret() ([]byte, error) {
	b := make([]byte, secretBytes)
	if _, err := rand.Read(b); err != nil {
		return nil, fmt.Errorf("secure: generate secret: %w", err)
	}
	return b, nil
}
