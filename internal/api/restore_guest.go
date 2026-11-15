package api

import (
	"errors"
	"net/http"
	"regexp"
	"strings"

	"github.com/Bernotese/plumb/internal/plan"
)

// guestRestoreRequest is GuestRestoreRequest of the contract (T-222).
type guestRestoreRequest struct {
	Backup string `json:"backup"`
	Reason string `json:"reason"`
}

var reGuestBackupID = regexp.MustCompile(`^[0-9a-f]{16}$`)

// restoreGuest is POST /targets/{name}/restore (D-410): a Plan of origin
// restore, risk destructive, human-only guest-restore.
func (s *Server) restoreGuest(w http.ResponseWriter, r *http.Request, p *principal) {
	if s.restoredMode(w) {
		return
	}
	var req guestRestoreRequest
	if !decode(w, r, &req, true) {
		return
	}
	if !reGuestBackupID.MatchString(req.Backup) {
		problem(w, http.StatusBadRequest, "bad-request", "Bad request", "backup must be the 16 hex digits of a backup id.")
		return
	}
	if req.Reason == "" || len(req.Reason) > 2000 {
		problem(w, http.StatusBadRequest, "bad-request", "Bad request", "reason must have 1 to 2000 characters.")
		return
	}
	name := r.PathValue("name")
	if _, err := s.headTarget(r.Context(), name); err != nil {
		s.fail(w, err)
		return
	}
	pl, err := s.cfg.Plans.ProposeRestore(r.Context(), name, req.Backup, req.Reason, p.name)
	if err != nil {
		s.fail(w, err)
		return
	}
	s.writePlan(w, r, http.StatusCreated, pl)
}

// restoreProblem answers the refusals of a restore; false when err is none
// of them.
func restoreProblem(w http.ResponseWriter, err error) bool {
	switch {
	case errors.Is(err, plan.ErrBackupNotFound):
		problem(w, http.StatusNotFound, "not-found", "Not found", strings.TrimPrefix(err.Error(), "plan: "))
	case errors.Is(err, plan.ErrNotRestorable):
		problem(w, http.StatusConflict, "not-restorable", "Backup not restorable", strings.TrimPrefix(err.Error(), "plan: "))
	default:
		return false
	}
	return true
}
