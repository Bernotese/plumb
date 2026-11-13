package api

import (
	"errors"
	"net/http"
	"time"

	"github.com/Bernotese/plumb/internal/plan"
	"github.com/Bernotese/plumb/internal/store"
)

// --- restore (T-113; D-290, D-303) ---

type unknownGuestView struct {
	GuestID  string `json:"guest_id"`
	Name     string `json:"name"`
	Type     string `json:"type"`
	Template bool   `json:"template"`
}

type restoreView struct {
	Restored        bool               `json:"restored"`
	RestoredMode    bool               `json:"restored_mode"`
	BackupCreatedAt *time.Time         `json:"backup_created_at,omitempty"`
	RestoredAt      *time.Time         `json:"restored_at,omitempty"`
	ConfirmedAt     *time.Time         `json:"confirmed_at,omitempty"`
	ConfirmedBy     *string            `json:"confirmed_by,omitempty"`
	DisabledTokens  *int               `json:"disabled_tokens,omitempty"`
	UnknownGuests   []unknownGuestView `json:"unknown_guests,omitempty"`
}

// restoredMode answers 409 restored-mode and returns true while restored
// mode holds (D-303).
func (s *Server) restoredMode(w http.ResponseWriter) bool {
	if s.cfg.Plans == nil || !s.cfg.Plans.RestoredMode() {
		return false
	}
	problem(w, http.StatusConflict, "restored-mode", "Restored mode",
		"This controller came from a backup. Confirm that the old controller is off first (POST /restore/confirm).")
	return true
}

func (s *Server) restoreView(r *http.Request, rs store.RestoreState) (restoreView, error) {
	v := restoreView{Restored: rs.Restored, RestoredMode: rs.RestoredMode}
	if !rs.Restored {
		return v, nil
	}
	v.BackupCreatedAt, v.RestoredAt = utc(rs.BackupCreatedAt), utc(rs.RestoredAt)
	confirmed := utc(rs.ConfirmedAt)
	v.ConfirmedAt = confirmed
	var by *string
	if rs.ConfirmedBy != "" {
		b := rs.ConfirmedBy
		by = &b
	}
	v.ConfirmedBy = by
	n, err := s.st.CountDisabledTokens(r.Context())
	if err != nil {
		return v, err
	}
	v.DisabledTokens = &n
	v.UnknownGuests = []unknownGuestView{}
	guests, err := s.cfg.Plans.UnknownGuests(r.Context())
	if err != nil {
		// The list is for display; Proxmox may not answer for a moment.
		s.log.Warn("unknown guests", "err", err)
		return v, nil
	}
	for _, g := range guests {
		v.UnknownGuests = append(v.UnknownGuests, unknownGuestView{GuestID: string(g.ID), Name: g.Name, Type: string(g.Kind), Template: g.Template})
	}
	return v, nil
}

// restoreJSON writes the view; confirmed_at and confirmed_by are null,
// not missing, on a restored controller.
func (s *Server) writeRestore(w http.ResponseWriter, status int, v restoreView) {
	if !v.Restored {
		writeJSON(w, status, map[string]any{"restored": false, "restored_mode": false})
		return
	}
	writeJSON(w, status, map[string]any{
		"restored": true, "restored_mode": v.RestoredMode, "backup_created_at": v.BackupCreatedAt,
		"restored_at": v.RestoredAt, "confirmed_at": v.ConfirmedAt, "confirmed_by": v.ConfirmedBy,
		"disabled_tokens": *v.DisabledTokens, "unknown_guests": v.UnknownGuests,
	})
}

func (s *Server) getRestore(w http.ResponseWriter, r *http.Request, _ *principal) {
	rs, err := s.st.GetRestore(r.Context())
	if err != nil {
		s.internal(w, err)
		return
	}
	v, err := s.restoreView(r, rs)
	if err != nil {
		s.internal(w, err)
		return
	}
	s.writeRestore(w, http.StatusOK, v)
}

type restoreConfirmRequest struct {
	OldControllerOff *bool  `json:"old_controller_off"`
	Reason           string `json:"reason"`
}

func (s *Server) confirmRestore(w http.ResponseWriter, r *http.Request, p *principal) {
	var req restoreConfirmRequest
	if !decode(w, r, &req, false) {
		return
	}
	if req.OldControllerOff == nil || !*req.OldControllerOff {
		problem(w, http.StatusBadRequest, "bad-request", "Bad request", "old_controller_off must be true: confirm that the controller of the backup no longer runs.")
		return
	}
	if len(req.Reason) > 2000 {
		problem(w, http.StatusBadRequest, "bad-request", "Bad request", "reason is longer than 2000 characters.")
		return
	}
	rs, err := s.cfg.Plans.ConfirmRestore(r.Context(), p.name, req.Reason)
	if errors.Is(err, plan.ErrRestoreCheck) {
		problem(w, http.StatusConflict, "restore-check-failed", "Guest check failed", err.Error()+" Restored mode stays.")
		return
	}
	if errors.Is(err, store.ErrNotRestoredMode) {
		problem(w, http.StatusConflict, "nothing-to-do", "Nothing to do", "This controller is not in restored mode.")
		return
	}
	if err != nil {
		s.internal(w, err)
		return
	}
	v, err := s.restoreView(r, rs)
	if err != nil {
		s.internal(w, err)
		return
	}
	s.writeRestore(w, http.StatusOK, v)
}

func (s *Server) enableToken(w http.ResponseWriter, r *http.Request, p *principal) {
	id := r.PathValue("id")
	if len(id) > 64 {
		problem(w, http.StatusNotFound, "not-found", "Not found", "No such token.")
		return
	}
	err := s.st.EnableToken(r.Context(), id)
	switch {
	case errors.Is(err, store.ErrNotFound):
		problem(w, http.StatusNotFound, "not-found", "Not found", "No such token.")
		return
	case errors.Is(err, store.ErrTokenRevoked):
		problem(w, http.StatusConflict, "token-revoked", "Token revoked", "A revoked token stays revoked; make a new one.")
		return
	case err != nil:
		s.internal(w, err)
		return
	}
	toks, err := s.st.TokenRow(r.Context(), id)
	if err != nil {
		s.internal(w, err)
		return
	}
	s.tokenEvent(r, "token.enabled", toks, p)
	writeJSON(w, http.StatusOK, viewToken(toks))
}
