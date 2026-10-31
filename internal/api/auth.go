// Package api implements the HTTP API of llmmeter.
package api

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/kiry163/llmmeter/internal/secure"
	"github.com/kiry163/llmmeter/internal/settings"
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

	// epochLen is the number of hex characters of the password hash prefix
	// used to invalidate sessions when the password changes.
	epochLen = 16
)

// Auth enforces the password session policy, loopback-only endpoints and
// CSRF protection.
type Auth struct {
	repo *settings.Repo
	now  func() time.Time

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

// NewAuth returns an Auth bound to repo. now is the clock used for session
// expiry and login rate limiting; nil means time.Now.
func NewAuth(repo *settings.Repo, now func() time.Time) *Auth {
	if now == nil {
		now = time.Now
	}
	return &Auth{repo: repo, now: now, login: make(map[string]*attemptWindow)}
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
		snap := a.repo.Snapshot()
		if snap == nil || snap.AdminPasswordHash == "" {
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
	snap := a.repo.Snapshot()
	if snap == nil {
		return false
	}
	if snap.AdminPasswordHash == "" {
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

// RequireLoopback rejects requests that did not originate from the local
// machine with 403.
func (a *Auth) RequireLoopback(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !IsLoopback(r) {
			writeAPIError(w, http.StatusForbidden, "forbidden", "loopback required")
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

	snap := a.repo.Snapshot()
	if snap == nil || snap.AdminPasswordHash == "" || !secure.VerifyPassword(snap.AdminPasswordHash, in.Password) {
		writeAPIError(w, http.StatusUnauthorized, "unauthorized", "invalid password")
		return
	}

	a.Issue(w)
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

// Issue sets a fresh signed session cookie on the response.
func (a *Auth) Issue(w http.ResponseWriter) {
	snap := a.repo.Snapshot()
	if snap == nil || len(snap.SessionSecret) == 0 {
		return
	}
	payload, err := json.Marshal(sessionPayload{
		Exp:   a.now().Add(sessionTTL).Unix(),
		Epoch: epochOf(snap.AdminPasswordHash),
	})
	if err != nil {
		return
	}
	mac := hmac.New(sha256.New, snap.SessionSecret)
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
}

// verifySession checks the HMAC, expiry and password epoch of a cookie value.
func verifySession(snap *settings.Snapshot, value string, nowUnix int64) bool {
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
	mac := hmac.New(sha256.New, snap.SessionSecret)
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
	return subtle.ConstantTimeCompare([]byte(p.Epoch), []byte(epochOf(snap.AdminPasswordHash))) == 1
}

// epochOf derives the session epoch from a password hash. A password change
// yields a new hash and therefore a new epoch, invalidating old sessions.
func epochOf(hash string) string {
	if hash == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(hash))
	return hex.EncodeToString(sum[:])[:epochLen]
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
