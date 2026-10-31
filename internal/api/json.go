package api

import (
	"encoding/json"
	"net/http"
)

// maxBodyBytes bounds every JSON request body.
const maxBodyBytes = 1 << 20

// Error codes of the API error envelope (spec §6).
const (
	codeValidation    = "validation"
	codeUnauthorized  = "unauthorized"
	codeForbidden     = "forbidden"
	codeNotFound      = "not_found"
	codeConflict      = "conflict"
	codeSetupRequired = "setup_required"
	codeInternal      = "internal"
)

// decodeJSON decodes the request body into v, answering 400 on malformed
// input.
func decodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	if err := dec.Decode(v); err != nil {
		writeAPIError(w, http.StatusBadRequest, codeValidation, "invalid JSON body")
		return false
	}
	return true
}

// writeJSON writes v as the JSON response body with the given status.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeAPIError writes the API error envelope used by every /api failure.
func writeAPIError(w http.ResponseWriter, status int, code, message string) {
	writeAPIErrorFields(w, status, code, message, nil)
}

// writeAPIErrorFields writes the API error envelope with per-field validation
// details. A nil fields map is encoded as {}.
func writeAPIErrorFields(w http.ResponseWriter, status int, code, message string, fields map[string]string) {
	if fields == nil {
		fields = map[string]string{}
	}
	writeJSON(w, status, map[string]any{
		"error": map[string]any{
			"code":    code,
			"message": message,
			"fields":  fields,
		},
	})
}
