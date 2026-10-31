package api

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/kiry163/llmmeter/internal/config"
	"github.com/kiry163/llmmeter/internal/secure"
)

var (
	// errKeyNotFound reports an unknown gateway key ID.
	errKeyNotFound = errors.New("api: key not found")
	// errUnknownUpstream reports a key write that references a missing
	// upstream.
	errUnknownUpstream = errors.New("api: unknown upstream")
)

// keyJSON is one local key list entry. It never carries the plaintext key.
type keyJSON struct {
	ID           string  `json:"id"`
	Name         string  `json:"name"`
	KeyMasked    string  `json:"keyMasked"`
	Enabled      bool    `json:"enabled"`
	UpstreamID   string  `json:"upstreamId"`
	UpstreamName string  `json:"upstreamName"`
	LastUsedAt   *string `json:"lastUsedAt"`
}

// keysResponse is the GET /api/keys body.
type keysResponse struct {
	Keys []keyJSON `json:"keys"`
}

// keyCreatedJSON is the shape in which a plaintext key is returned once:
// creation and rotation. It is never used by the list endpoint.
type keyCreatedJSON struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Key          string `json:"key"`
	KeyMasked    string `json:"keyMasked"`
	UpstreamID   string `json:"upstreamId"`
	UpstreamName string `json:"upstreamName"`
}

// keySecretResponse reveals a stored local key.
type keySecretResponse struct {
	Key string `json:"key"`
}

// keyCreateRequest is the POST /api/keys body.
type keyCreateRequest struct {
	Name       string `json:"name"`
	UpstreamID string `json:"upstreamId"`
}

// keyPatchRequest is the PATCH /api/keys/{keyId} body. Absent fields stay
// unchanged.
type keyPatchRequest struct {
	Name       *string `json:"name"`
	UpstreamID *string `json:"upstreamId"`
	Enabled    *bool   `json:"enabled"`
}

// keyJSONFor assembles one list entry.
func keyJSONFor(k config.GatewayKey, upstreamName string, lastUsed *string) keyJSON {
	return keyJSON{
		ID:           k.ID,
		Name:         k.Name,
		KeyMasked:    maskSecret(k.Key),
		Enabled:      k.Active(),
		UpstreamID:   k.UpstreamID,
		UpstreamName: upstreamName,
		LastUsedAt:   lastUsed,
	}
}

// keyCreatedFor assembles the response of a create or rotate.
func keyCreatedFor(k config.GatewayKey, upstreamName string) keyCreatedJSON {
	return keyCreatedJSON{
		ID:           k.ID,
		Name:         k.Name,
		Key:          k.Key,
		KeyMasked:    maskSecret(k.Key),
		UpstreamID:   k.UpstreamID,
		UpstreamName: upstreamName,
	}
}

// upstreamName returns the display name of an upstream, or "".
func upstreamName(snap *config.Snapshot, id string) string {
	if snap == nil {
		return ""
	}
	if u, ok := snap.Upstream(id); ok {
		return u.Name
	}
	return ""
}

// keyEntry returns the current list entry of one key.
func (s *Server) keyEntry(id string) (keyJSON, bool) {
	snap := s.configSnapshot()
	if snap == nil {
		return keyJSON{}, false
	}
	lastUsed := s.lastUsedTimes("key_id")
	for _, k := range snap.Config.GatewayKeys {
		if k.ID == id {
			return keyJSONFor(k, upstreamName(snap, k.UpstreamID), lastUsed[k.ID]), true
		}
	}
	return keyJSON{}, false
}

// listKeys answers GET /api/keys. The optional upstream query parameter limits
// the result to the keys bound to that upstream.
func (s *Server) listKeys(w http.ResponseWriter, r *http.Request) {
	filter := strings.TrimSpace(r.URL.Query().Get("upstream"))
	out := keysResponse{Keys: []keyJSON{}}
	snap := s.configSnapshot()
	if snap == nil {
		writeJSON(w, http.StatusOK, out)
		return
	}
	lastUsed := s.lastUsedTimes("key_id")
	for _, k := range snap.Config.GatewayKeys {
		if filter != "" && k.UpstreamID != filter {
			continue
		}
		out.Keys = append(out.Keys, keyJSONFor(k, upstreamName(snap, k.UpstreamID), lastUsed[k.ID]))
	}
	writeJSON(w, http.StatusOK, out)
}

// createKey answers POST /api/keys. The plaintext key is generated here and
// returned exactly once.
func (s *Server) createKey(w http.ResponseWriter, r *http.Request) {
	var in keyCreateRequest
	if !decodeJSON(w, r, &in) {
		return
	}
	name := strings.TrimSpace(in.Name)
	upstreamID := strings.TrimSpace(in.UpstreamID)
	fields := map[string]string{}
	switch {
	case name == "":
		fields["name"] = "required"
	case utf8.RuneCountInString(name) > maxUpstreamNameLen:
		fields["name"] = fmt.Sprintf("must be at most %d characters", maxUpstreamNameLen)
	}
	if upstreamID == "" {
		fields["upstreamId"] = "required"
	}
	if len(fields) > 0 {
		writeAPIErrorFields(w, http.StatusBadRequest, codeValidation, "invalid key", fields)
		return
	}

	key := secure.NewGatewayKey()
	var id string
	err := s.configStore.Update(func(c *config.Config) error {
		if upstreamIndex(c, upstreamID) < 0 {
			return errUnknownUpstream
		}
		id = uniqueSlug(slugify(name, "key"), func(cand string) bool {
			return keyIDTaken(c, cand)
		})
		c.GatewayKeys = append(c.GatewayKeys, config.GatewayKey{
			ID:         id,
			Name:       name,
			Key:        key,
			UpstreamID: upstreamID,
		})
		return nil
	})
	if errors.Is(err, errUnknownUpstream) {
		writeAPIErrorFields(w, http.StatusBadRequest, codeValidation, "invalid key",
			map[string]string{"upstreamId": "unknown upstream"})
		return
	}
	if err != nil {
		writeConfigWriteError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, keyCreatedJSON{
		ID:           id,
		Name:         name,
		Key:          key,
		KeyMasked:    maskSecret(key),
		UpstreamID:   upstreamID,
		UpstreamName: upstreamName(s.configSnapshot(), upstreamID),
	})
}

// patchKey answers PATCH /api/keys/{keyId}: rename, enable/disable or rebind
// to another upstream. Rebinding takes effect with the next request because the
// store publishes a fresh snapshot before answering.
func (s *Server) patchKey(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("keyId")
	var in keyPatchRequest
	if !decodeJSON(w, r, &in) {
		return
	}
	fields := map[string]string{}
	if in.Name != nil {
		name := strings.TrimSpace(*in.Name)
		switch {
		case name == "":
			fields["name"] = "required"
		case utf8.RuneCountInString(name) > maxUpstreamNameLen:
			fields["name"] = fmt.Sprintf("must be at most %d characters", maxUpstreamNameLen)
		}
	}
	if in.UpstreamID != nil && strings.TrimSpace(*in.UpstreamID) == "" {
		fields["upstreamId"] = "required"
	}
	if len(fields) > 0 {
		writeAPIErrorFields(w, http.StatusBadRequest, codeValidation, "invalid key", fields)
		return
	}

	err := s.configStore.Update(func(c *config.Config) error {
		idx := keyIndex(c, id)
		if idx < 0 {
			return errKeyNotFound
		}
		k := &c.GatewayKeys[idx]
		if in.Name != nil {
			k.Name = strings.TrimSpace(*in.Name)
		}
		if in.UpstreamID != nil {
			target := strings.TrimSpace(*in.UpstreamID)
			if upstreamIndex(c, target) < 0 {
				return errUnknownUpstream
			}
			k.UpstreamID = target
		}
		if in.Enabled != nil {
			enabled := *in.Enabled
			k.Enabled = &enabled
		}
		return nil
	})
	switch {
	case errors.Is(err, errKeyNotFound):
		writeAPIError(w, http.StatusNotFound, codeNotFound, "key not found")
		return
	case errors.Is(err, errUnknownUpstream):
		writeAPIErrorFields(w, http.StatusBadRequest, codeValidation, "invalid key",
			map[string]string{"upstreamId": "unknown upstream"})
		return
	case err != nil:
		writeConfigWriteError(w, err)
		return
	}
	entry, _ := s.keyEntry(id)
	writeJSON(w, http.StatusOK, entry)
}

// deleteKey answers DELETE /api/keys/{keyId}.
func (s *Server) deleteKey(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("keyId")
	err := s.configStore.Update(func(c *config.Config) error {
		idx := keyIndex(c, id)
		if idx < 0 {
			return errKeyNotFound
		}
		c.GatewayKeys = append(c.GatewayKeys[:idx], c.GatewayKeys[idx+1:]...)
		return nil
	})
	switch {
	case errors.Is(err, errKeyNotFound):
		writeAPIError(w, http.StatusNotFound, codeNotFound, "key not found")
		return
	case err != nil:
		writeConfigWriteError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// rotateKey answers POST /api/keys/{keyId}/rotate. The new plaintext key is
// returned once and the old value stops working immediately.
func (s *Server) rotateKey(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("keyId")
	var newKey string
	err := s.configStore.Update(func(c *config.Config) error {
		idx := keyIndex(c, id)
		if idx < 0 {
			return errKeyNotFound
		}
		for {
			newKey = secure.NewGatewayKey()
			if !keyValueTaken(c, newKey) {
				break
			}
		}
		c.GatewayKeys[idx].Key = newKey
		return nil
	})
	switch {
	case errors.Is(err, errKeyNotFound):
		writeAPIError(w, http.StatusNotFound, codeNotFound, "key not found")
		return
	case err != nil:
		writeConfigWriteError(w, err)
		return
	}
	snap := s.configSnapshot()
	for _, k := range snap.Config.GatewayKeys {
		if k.ID == id {
			writeJSON(w, http.StatusOK, keyCreatedFor(k, upstreamName(snap, k.UpstreamID)))
			return
		}
	}
	writeInternal(w, errors.New("api: rotated key missing from snapshot"))
}

// keySecret answers GET /api/keys/{keyId}/secret with the plaintext key.
func (s *Server) keySecret(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("keyId")
	snap := s.configSnapshot()
	if snap == nil {
		writeAPIError(w, http.StatusNotFound, codeNotFound, "key not found")
		return
	}
	for _, k := range snap.Config.GatewayKeys {
		if k.ID == id {
			w.Header().Set("Cache-Control", "no-store")
			writeJSON(w, http.StatusOK, keySecretResponse{Key: k.Key})
			return
		}
	}
	writeAPIError(w, http.StatusNotFound, codeNotFound, "key not found")
}

// keyIndex returns the position of id in cfg.GatewayKeys, or -1.
func keyIndex(c *config.Config, id string) int {
	for i := range c.GatewayKeys {
		if c.GatewayKeys[i].ID == id {
			return i
		}
	}
	return -1
}

// keyIDTaken reports whether a local key already uses id.
func keyIDTaken(c *config.Config, id string) bool {
	return keyIndex(c, id) >= 0
}

// keyValueTaken reports whether a plaintext key value is already configured.
func keyValueTaken(c *config.Config, value string) bool {
	for _, k := range c.GatewayKeys {
		if k.Key == value {
			return true
		}
	}
	return false
}
