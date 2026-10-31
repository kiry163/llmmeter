package api

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/kiry163/llmmeter/internal/config"
)

// Retention bounds mirror config.Validate.
const (
	minGeneralRetentionDays = 7
	maxGeneralRetentionDays = 3650
)

// generalResponse is the reporting settings payload.
type generalResponse struct {
	Timezone      string `json:"timezone"`
	RetentionDays int    `json:"retentionDays"`
}

func (s *Server) generalView() generalResponse {
	view := generalResponse{Timezone: s.configLocation().String()}
	if snap := s.configSnapshot(); snap != nil {
		view.RetentionDays = snap.Config.General.RetentionDays
	}
	return view
}

func (s *Server) getGeneral(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.generalView())
}

type generalPatchRequest struct {
	Timezone      *string `json:"timezone"`
	RetentionDays *int    `json:"retentionDays"`
}

// patchGeneral validates and writes the reporting settings.
func (s *Server) patchGeneral(w http.ResponseWriter, r *http.Request) {
	if s.configStore == nil {
		writeInternal(w, errors.New("api: configuration store not configured"))
		return
	}
	var in generalPatchRequest
	if !decodeJSON(w, r, &in) {
		return
	}

	fields := map[string]string{}
	var timezone string
	if in.Timezone != nil {
		timezone = strings.TrimSpace(*in.Timezone)
		if timezone != "" {
			if _, err := time.LoadLocation(timezone); err != nil {
				fields["timezone"] = "unknown timezone"
			}
		}
	}
	if in.RetentionDays != nil &&
		(*in.RetentionDays < minGeneralRetentionDays || *in.RetentionDays > maxGeneralRetentionDays) {
		fields["retentionDays"] = "must be between 7 and 3650"
	}
	if len(fields) > 0 {
		writeAPIErrorFields(w, http.StatusBadRequest, codeValidation, "invalid general settings", fields)
		return
	}

	err := s.configStore.Update(func(c *config.Config) error {
		if in.Timezone != nil {
			c.General.Timezone = timezone
		}
		if in.RetentionDays != nil {
			c.General.RetentionDays = *in.RetentionDays
		}
		return nil
	})
	if err != nil {
		var ve *config.ValidationError
		if errors.As(err, &ve) {
			writeAPIErrorFields(w, http.StatusBadRequest, codeValidation, ve.Error(), ve.Fields)
			return
		}
		writeInternal(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s.generalView())
}

// reloadConfig re-reads config.yaml after a manual edit, equivalent to
// SIGHUP. The store keeps the previous snapshot when the new file is invalid.
func (s *Server) reloadConfig(w http.ResponseWriter, _ *http.Request) {
	if s.configStore == nil {
		writeInternal(w, errors.New("api: configuration store not configured"))
		return
	}
	if err := s.configStore.Reload(); err != nil {
		writeInternal(w, err)
		return
	}
	s.catalog.invalidate()
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
