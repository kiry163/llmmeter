package api

import (
	"encoding/base64"
	"errors"
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

// gatewayKeyResponse is the one shape in which a plaintext gateway key is
// returned after setup or rotation.
type gatewayKeyResponse struct {
	GatewayKey string `json:"gatewayKey"`
}

// setupRequest is the first-run payload from spec 8.2. Models use the YAML
// schema shape so the request can be validated by the configuration rules.
type setupRequest struct {
	UpstreamName  string         `json:"upstreamName"`
	BaseURL       string         `json:"baseUrl"`
	APIKey        string         `json:"apiKey"`
	Timezone      string         `json:"timezone"`
	AdminPassword string         `json:"adminPassword"`
	KeyName       string         `json:"keyName"`
	Models        []config.Model `json:"models"`
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
	if snap := s.configSnapshot(); snap != nil && len(snap.Config.Upstreams) > 0 {
		writeAPIError(w, http.StatusConflict, codeConflict, "setup already completed")
		return
	}
	var in setupRequest
	if !decodeJSON(w, r, &in) {
		return
	}
	if fields := s.validateSetup(&in); len(fields) > 0 {
		writeAPIErrorFields(w, http.StatusBadRequest, codeValidation, "invalid setup", fields)
		return
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
	secret, err := secure.RandomSecret()
	if err != nil {
		writeInternal(w, err)
		return
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

	err = s.configStore.Update(func(c *config.Config) error {
		if len(c.Upstreams) > 0 {
			return errSetupExists
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
		c.Auth.AdminPasswordHash = passwordHash
		c.Auth.SessionSecret = base64.StdEncoding.EncodeToString(secret)
		return nil
	})
	if err != nil {
		var ve *config.ValidationError
		switch {
		case errors.Is(err, errSetupExists):
			writeAPIError(w, http.StatusConflict, codeConflict, "setup already completed")
		case errors.As(err, &ve):
			writeAPIErrorFields(w, http.StatusBadRequest, codeValidation, "invalid setup", ve.Fields)
		default:
			writeInternal(w, err)
		}
		return
	}

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
func (s *Server) validateSetup(in *setupRequest) map[string]string {
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
	if in.AdminPassword == "" && !LoopbackListen(s.effectiveListen()) {
		fields["adminPassword"] = "required when the listen address is not loopback"
	}
	return fields
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
