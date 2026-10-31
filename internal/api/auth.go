// Package api implements the HTTP API of llmmeter.
package api

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/kiry163/llmmeter/internal/config"
	"github.com/kiry163/llmmeter/internal/secure"
)

const (
	// SessionCookie is the name of the session cookie.
	SessionCookie = "llmmeter_session"

	// sessionTTL is how long an issued session stays valid.
	sessionTTL = 12 * time.Hour

	// sessionMaxAge mirrors sessionTTL for the cookie attribute, in seconds.
	sessionMaxAge = int(sessionTTL / time.Second)

	csrfHeader = "X-Requested-With"
	csrfValue  = "llmmeter"

	// loginLimit is the number of login attempts allowed per IP per window.
	loginLimit  = 5
	loginWindow = time.Minute

	// maxLoginBody caps the login request body.
	maxLoginBody = 4 << 10

	// epochLen is the number of hex characters of the password hash digest
	// used to invalidate sessions when the password changes.
	epochLen = 16

	// minAdminPasswordLen is the shortest accepted admin password.
	minAdminPasswordLen = 8
)

// errNoSessionSecret reports that the configuration carries no usable session
// signing secret, so a session cannot be issued. Startup generates and
// persists one when the configuration lacks it.
var errNoSessionSecret = errors.New("api: session secret not configured")

// Auth enforces the password session policy, loopback-only endpoints and
// CSRF protection.
type Auth struct {
	store *config.Store
	// listen is the process's effective listen address, after flag and
	// configuration resolution. Password-clearing decisions use it instead of
	// the possibly stale server.listen in the current configuration.
	listen string
	now    func() time.Time

	mu    sync.Mutex
	login map[string]*attemptWindow
}

type attemptWindow struct {
	start time.Time
	count int
}

// sessionPayload is the signed content of a session cookie.
type sessionPayload struct {
	Exp   int64  `json:"exp"`
	Epoch string `json:"epoch"`
}

// NewAuth returns an Auth bound to the configuration store. effectiveListen is
// the address the process actually listens on; it decides whether clearing the
// admin password is safe. now is the clock used for session expiry and login
// rate limiting; nil means time.Now.
func NewAuth(store *config.Store, effectiveListen string, now func() time.Time) *Auth {
	if now == nil {
		now = time.Now
	}
	return &Auth{store: store, listen: effectiveListen, now: now, login: make(map[string]*attemptWindow)}
}

// snapshot returns the current immutable configuration snapshot. A server
// built without a configuration store behaves like an unconfigured instance.
func (a *Auth) snapshot() *config.Snapshot {
	if a.store == nil {
		return nil
	}
	return a.store.Snapshot()
}

// IsLoopback reports whether the request originates from the local machine.
func IsLoopback(r *http.Request) bool {
	host := r.RemoteAddr
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	return loopbackName(host)
}

// loopbackName reports whether host is localhost or a loopback IP literal.
func loopbackName(host string) bool {
	host = strings.Trim(host, "[]")
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// LoopbackListen reports whether addr binds only the local machine. An empty
// or wildcard host (e.g. ":8100" or "0.0.0.0:8100") is not loopback.
func LoopbackListen(addr string) bool {
	host := addr
	if h, _, err := net.SplitHostPort(addr); err == nil {
		host = h
	}
	if host == "" {
		return false
	}
	return loopbackName(host)
}

// HostGuard rejects requests whose Host header is not a loopback name while no
// admin password is configured. Without it a DNS-rebinding page could reach a
// local-only instance through the browser. Once a password is set, normal
// session authentication applies instead.
func (a *Auth) HostGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		snap := a.snapshot()
		if snap == nil || snap.Config.Auth.AdminPasswordHash == "" {
			host := r.Host
			if h, _, err := net.SplitHostPort(host); err == nil {
				host = h
			}
			if !loopbackName(host) {
				writeAPIError(w, http.StatusForbidden, "forbidden", "loopback host required")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// Authenticated reports whether the request carries a valid session. Without
// a configured password only loopback requests are trusted.
func (a *Auth) Authenticated(r *http.Request) bool {
	snap := a.snapshot()
	if snap == nil {
		return false
	}
	if snap.Config.Auth.AdminPasswordHash == "" {
		return IsLoopback(r)
	}
	c, err := r.Cookie(SessionCookie)
	if err != nil {
		return false
	}
	return verifySession(snap, c.Value, a.now().Unix())
}

// Require rejects unauthenticated requests with 401.
func (a *Auth) Require(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !a.Authenticated(r) {
			writeAPIError(w, http.StatusUnauthorized, "unauthorized", "authentication required")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// CSRF rejects state-changing requests that were not issued by this origin.
// Safe methods pass; otherwise the request must carry X-Requested-With:
// llmmeter or a same-host Origin.
func (a *Auth) CSRF(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			next.ServeHTTP(w, r)
			return
		}
		if r.Header.Get(csrfHeader) == csrfValue {
			next.ServeHTTP(w, r)
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" {
			if u, err := url.Parse(origin); err == nil && u.Host != "" && strings.EqualFold(u.Host, r.Host) {
				next.ServeHTTP(w, r)
				return
			}
		}
		writeAPIError(w, http.StatusForbidden, "forbidden", "csrf check failed")
	})
}

// Login verifies the submitted password and, on success, issues a session
// cookie. Attempts are limited per client IP.
func (a *Auth) Login(w http.ResponseWriter, r *http.Request) {
	if !a.allowLogin(clientIP(r)) {
		w.Header().Set("Retry-After", "60")
		writeAPIError(w, http.StatusTooManyRequests, "validation", "too many login attempts")
		return
	}

	var in struct {
		Password string `json:"password"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxLoginBody))
	if err := dec.Decode(&in); err != nil {
		writeAPIError(w, http.StatusBadRequest, "validation", "invalid request body")
		return
	}

	snap := a.snapshot()
	if snap == nil || snap.Config.Auth.AdminPasswordHash == "" ||
		!secure.VerifyPassword(snap.Config.Auth.AdminPasswordHash, in.Password) {
		writeAPIError(w, http.StatusUnauthorized, "unauthorized", "invalid password")
		return
	}

	if err := a.issueSession(w, snap); err != nil {
		writeInternal(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"authenticated": true})
}

// Logout clears the session cookie.
func (a *Auth) Logout(w http.ResponseWriter, _ *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookie,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		Expires:  time.Unix(1, 0),
	})
	writeJSON(w, http.StatusOK, map[string]bool{"authenticated": false})
}

// Password sets, changes or clears the admin password. An empty newPassword
// clears it, which is refused while the effective listen address is not
// loopback. A change verifies current and stores an Argon2id hash; every
// success rotates the session secret in a single configuration write, so other
// sessions stop verifying and the current request receives a fresh cookie.
func (a *Auth) Password(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Current     string `json:"current"`
		NewPassword string `json:"newPassword"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxLoginBody))
	if err := dec.Decode(&in); err != nil {
		writeAPIError(w, http.StatusBadRequest, codeValidation, "invalid request body")
		return
	}

	snap := a.snapshot()
	if snap == nil {
		writeInternal(w, errNoSessionSecret)
		return
	}
	if hash := snap.Config.Auth.AdminPasswordHash; hash != "" && !secure.VerifyPassword(hash, in.Current) {
		writeAPIErrorFields(w, http.StatusBadRequest, codeValidation, "invalid password change",
			map[string]string{"current": "incorrect password"})
		return
	}

	next := ""
	if in.NewPassword != "" {
		if len(in.NewPassword) < minAdminPasswordLen {
			writeAPIErrorFields(w, http.StatusBadRequest, codeValidation, "invalid password change",
				map[string]string{"newPassword": "must be at least 8 characters"})
			return
		}
		hash, err := secure.HashPassword(in.NewPassword)
		if err != nil {
			writeInternal(w, err)
			return
		}
		next = hash
	} else if !LoopbackListen(a.listen) {
		writeAPIErrorFields(w, http.StatusBadRequest, codeValidation, "invalid password change",
			map[string]string{"newPassword": "must be set when the listen address is not loopback"})
		return
	}

	secret, err := secure.RandomSecret()
	if err != nil {
		writeInternal(w, err)
		return
	}
	if err := a.store.Update(func(c *config.Config) error {
		c.Auth.AdminPasswordHash = next
		c.Auth.SessionSecret = base64.StdEncoding.EncodeToString(secret)
		return nil
	}); err != nil {
		var ve *config.ValidationError
		if errors.As(err, &ve) {
			writeAPIErrorFields(w, http.StatusBadRequest, codeValidation, ve.Error(), ve.Fields)
			return
		}
		writeInternal(w, err)
		return
	}

	if err := a.Issue(w); err != nil {
		writeInternal(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"adminPasswordSet": next != ""})
}

// Issue sets a fresh signed session cookie on the response. It fails when the
// configuration carries no usable session secret.
func (a *Auth) Issue(w http.ResponseWriter) error {
	return a.issueSession(w, a.snapshot())
}

// issueSession signs a cookie against one immutable snapshot.
func (a *Auth) issueSession(w http.ResponseWriter, snap *config.Snapshot) error {
	if snap == nil {
		return errNoSessionSecret
	}
	secret := decodeSessionSecret(snap.Config.Auth.SessionSecret)
	if len(secret) == 0 {
		return errNoSessionSecret
	}
	payload, err := json.Marshal(sessionPayload{
		Exp:   a.now().Add(sessionTTL).Unix(),
		Epoch: passwordEpoch(snap.Config.Auth.AdminPasswordHash),
	})
	if err != nil {
		return err
	}
	mac := hmac.New(sha256.New, secret)
	mac.Write(payload)
	token := base64.RawURLEncoding.EncodeToString(payload) + "." +
		base64.RawURLEncoding.EncodeToString(mac.Sum(nil))

	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookie,
		Value:    token,
		Path:     "/",
		MaxAge:   sessionMaxAge,
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	})
	return nil
}

// verifySession checks the HMAC, expiry and password epoch of a cookie value
// against one immutable snapshot.
func verifySession(snap *config.Snapshot, value string, nowUnix int64) bool {
	if snap == nil {
		return false
	}
	secret := decodeSessionSecret(snap.Config.Auth.SessionSecret)
	if len(secret) == 0 {
		return false
	}
	enc, sig, ok := strings.Cut(value, ".")
	if !ok {
		return false
	}
	payload, err := base64.RawURLEncoding.DecodeString(enc)
	if err != nil {
		return false
	}
	got, err := base64.RawURLEncoding.DecodeString(sig)
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, secret)
	mac.Write(payload)
	if !hmac.Equal(got, mac.Sum(nil)) {
		return false
	}
	var p sessionPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return false
	}
	if p.Exp <= nowUnix {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(p.Epoch), []byte(passwordEpoch(snap.Config.Auth.AdminPasswordHash))) == 1
}

// passwordEpoch derives the session epoch from a password hash. Hashing the
// hash keeps the epoch opaque while still changing on every password change,
// which invalidates old sessions even if the secret were not rotated.
func passwordEpoch(hash string) string {
	if hash == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(hash))
	return hex.EncodeToString(sum[:])[:epochLen]
}

// decodeSessionSecret decodes the base64 session secret. Empty or malformed
// secrets yield nil, which refuses session issuance and verification.
func decodeSessionSecret(encoded string) []byte {
	if encoded == "" {
		return nil
	}
	if raw, err := base64.RawStdEncoding.DecodeString(encoded); err == nil {
		return raw
	}
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil
	}
	return raw
}

// clientIP extracts the host part of the request's remote address.
func clientIP(r *http.Request) string {
	if h, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return h
	}
	return r.RemoteAddr
}

// allowLogin records an attempt and reports whether it is within the limit.
// Expired windows are pruned lazily on every call.
func (a *Auth) allowLogin(ip string) bool {
	now := a.now()

	a.mu.Lock()
	defer a.mu.Unlock()

	for k, v := range a.login {
		if now.Sub(v.start) >= loginWindow {
			delete(a.login, k)
		}
	}
	v, ok := a.login[ip]
	if !ok {
		v = &attemptWindow{start: now}
		a.login[ip] = v
	}
	if now.Sub(v.start) >= loginWindow {
		v.start = now
		v.count = 0
	}
	if v.count >= loginLimit {
		return false
	}
	v.count++
	return true
}
