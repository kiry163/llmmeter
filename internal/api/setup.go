package api

import (
	"encoding/base64"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/kiry163/llmmeter/internal/config"
	"github.com/kiry163/llmmeter/internal/secure"
)

// maxSlugLen is the longest generated upstream or key ID.
const maxSlugLen = 32

// errSetupExists is returned by the setup mutation when another upstream was
// configured between the handler's check and the store lock.
var errSetupExists = errors.New("api: setup already completed")

// errSetupAuthChanged is returned by the setup mutation when the admin
// password hash changed after the handler authenticated the request, so a
// concurrent password change can never be replaced without its current value.
var errSetupAuthChanged = errors.New("api: admin password changed during setup")

// gatewayKeyResponse is the one shape in which a plaintext gateway key is
// returned after setup or rotation.
type gatewayKeyResponse struct {
	GatewayKey string `json:"gatewayKey"`
}

// setupRequest is the first-run payload from spec 8.2. Models use the YAML
// schema shape so the request can be validated by the configuration rules.
type setupRequest struct {
	UpstreamName  string `json:"upstreamName"`
	BaseURL       string `json:"baseUrl"`
	APIKey        string `json:"apiKey"`
	Timezone      string `json:"timezone"`
	AdminPassword string `json:"adminPassword"`
	// CurrentPassword must verify against an existing admin hash before
	// setup may replace it; it is ignored on first run.
	CurrentPassword string         `json:"currentPassword"`
	KeyName         string         `json:"keyName"`
	Models          []config.Model `json:"models"`
}

type setupResponse struct {
	UpstreamID string `json:"upstreamId"`
	KeyID      string `json:"keyId"`
	GatewayKey string `json:"gatewayKey"`
	ConfigPath string `json:"configPath"`
}

// setup performs first-run initialization. It is available only from the
// local machine (enforced by the route) and only while no upstream exists. The
// upstream, gateway key, admin password and session secret are written in one
// atomic configuration update, so a failure leaves the previous file intact.
func (s *Server) setup(w http.ResponseWriter, r *http.Request) {
	if s.configStore == nil {
		writeInternal(w, errors.New("api: configuration store not configured"))
		return
	}
	// Setup is a local recovery operation: it must be driven from a loopback
	// host even when an admin password is configured, because HostGuard stops
	// checking the Host header once a password exists.
	if !loopbackRequestHost(r.Host) {
		writeAPIError(w, http.StatusForbidden, "forbidden", "loopback host required")
		return
	}
	if snap := s.configSnapshot(); snap != nil && len(snap.Config.Upstreams) > 0 {
		writeAPIError(w, http.StatusConflict, codeConflict, "setup already completed")
		return
	}
	var in setupRequest
	if !decodeJSON(w, r, &in) {
		return
	}
	existingHash := ""
	if snap := s.configSnapshot(); snap != nil {
		existingHash = snap.Config.Auth.AdminPasswordHash
	}
	if fields := s.validateSetup(&in, existingHash); len(fields) > 0 {
		writeAPIErrorFields(w, http.StatusBadRequest, codeValidation, "invalid setup", fields)
		return
	}
	// A configuration that already holds a password may only be completed by
	// an authenticated admin, and replacing that password needs the current
	// one, so a proxied loopback request cannot take over the instance.
	if existingHash != "" {
		if !s.auth.Authenticated(r) {
			writeAPIError(w, http.StatusUnauthorized, "unauthorized", "authentication required")
			return
		}
		if in.AdminPassword != "" && !secure.VerifyPassword(existingHash, in.CurrentPassword) {
			writeAPIErrorFields(w, http.StatusForbidden, "forbidden", "invalid current password",
				map[string]string{"currentPassword": "incorrect password"})
			return
		}
	}

	passwordHash := ""
	if in.AdminPassword != "" {
		hash, err := secure.HashPassword(in.AdminPassword)
		if err != nil {
			writeInternal(w, err)
			return
		}
		passwordHash = hash
	}
	// Setup may run on a document that already holds an admin password (and
	// therefore a session secret) before any upstream exists. Never clear
	// those: preserve the hash and only generate a secret when there is none.
	var newSecret []byte
	if s.configStore.Config().Auth.SessionSecret == "" {
		secret, err := secure.RandomSecret()
		if err != nil {
			writeInternal(w, err)
			return
		}
		newSecret = secret
	}
	gatewayKey := secure.NewGatewayKey()

	name := strings.TrimSpace(in.UpstreamName)
	keyName := strings.TrimSpace(in.KeyName)
	if keyName == "" {
		keyName = "default"
	}
	upstreamID := slugify(name, "upstream")
	keyID := slugify(keyName, "default")
	timezone := strings.TrimSpace(in.Timezone)
	listen := s.effectiveListen()
	dbPath := s.databasePath()

	err := s.configStore.Update(func(c *config.Config) error {
		if len(c.Upstreams) > 0 {
			return errSetupExists
		}
		if passwordHash != "" && c.Auth.AdminPasswordHash != existingHash {
			return errSetupAuthChanged
		}
		// Record the values this process actually runs with, so validation
		// checks the same effective listen and the file keeps them across
		// restarts.
		if listen != "" {
			c.Server.Listen = listen
		}
		if dbPath != "" {
			c.Server.Database = dbPath
		}
		c.Upstreams = []config.Upstream{{
			ID:      upstreamID,
			Name:    name,
			BaseURL: strings.TrimSpace(in.BaseURL),
			APIKey:  strings.TrimSpace(in.APIKey),
			Models:  in.Models,
		}}
		c.GatewayKeys = []config.GatewayKey{{
			ID:         keyID,
			Name:       keyName,
			Key:        gatewayKey,
			UpstreamID: upstreamID,
		}}
		c.General.Timezone = timezone
		if passwordHash != "" {
			c.Auth.AdminPasswordHash = passwordHash
		}
		if len(newSecret) > 0 {
			c.Auth.SessionSecret = base64.StdEncoding.EncodeToString(newSecret)
		}
		return nil
	})
	if err != nil {
		var ve *config.ValidationError
		switch {
		case errors.Is(err, errSetupExists):
			writeAPIError(w, http.StatusConflict, codeConflict, "setup already completed")
		case errors.Is(err, errSetupAuthChanged):
			writeAPIError(w, http.StatusUnauthorized, "unauthorized", "authentication required")
		case errors.As(err, &ve):
			writeAPIErrorFields(w, http.StatusBadRequest, codeValidation, "invalid setup", ve.Fields)
		default:
			writeInternal(w, err)
		}
		return
	}

	s.catalog.invalidate()
	// Setup generated the session secret in the same write, so a configured
	// password can be used immediately without a second login round-trip.
	if passwordHash != "" {
		_ = s.auth.Issue(w)
	}
	writeJSON(w, http.StatusOK, setupResponse{
		UpstreamID: upstreamID,
		KeyID:      keyID,
		GatewayKey: gatewayKey,
		ConfigPath: s.configPath(),
	})
}

// validateSetup reports body-level field errors with the API field names,
// before the configuration validator sees the constructed document. Models
// are validated by config.Validate in the update.
func (s *Server) validateSetup(in *setupRequest, existingHash string) map[string]string {
	fields := map[string]string{}
	if strings.TrimSpace(in.UpstreamName) == "" {
		fields["upstreamName"] = "required"
	}
	if !validSetupURL(strings.TrimSpace(in.BaseURL)) {
		fields["baseUrl"] = "must be an http or https URL with a host"
	}
	if tz := strings.TrimSpace(in.Timezone); tz != "" {
		if _, err := time.LoadLocation(tz); err != nil {
			fields["timezone"] = "unknown timezone"
		}
	}
	if in.AdminPassword == "" && existingHash == "" && !LoopbackListen(s.effectiveListen()) {
		fields["adminPassword"] = "required when the listen address is not loopback"
	}
	return fields
}

// loopbackRequestHost reports whether the request Host header names the local
// machine. Setup uses it independently of HostGuard, which stops checking the
// Host header once an admin password exists.
func loopbackRequestHost(host string) bool {
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	return loopbackName(host)
}

// validSetupURL mirrors the configuration rule: an absolute http(s) URL with
// a host.
func validSetupURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return false
	}
	return u.Scheme == "http" || u.Scheme == "https"
}

// slugify turns a display name into a valid configuration ID: lowercase ASCII
// letters and digits, single dashes between runs, at most 32 characters. It
// falls back when the name has no usable characters (e.g. a CJK name).
func slugify(name, fallback string) string {
	var b strings.Builder
	lastDash := false
	for _, r := range strings.ToLower(strings.TrimSpace(name)) {
		if b.Len() >= maxSlugLen {
			break
		}
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		case !lastDash && b.Len() > 0:
			b.WriteByte('-')
			lastDash = true
		}
	}
	out := strings.TrimRight(b.String(), "-")
	if out == "" {
		return fallback
	}
	return out
}
