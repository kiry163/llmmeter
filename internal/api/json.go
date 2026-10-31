package api

import (
	"encoding/json"
	"net/http"
)

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
