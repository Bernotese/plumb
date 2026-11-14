package api

import (
	"errors"
	"net/http"
	"regexp"
	"strings"

	"github.com/Bernotese/plumb/internal/plan"
)

// repairRequest is RepairRequest of the contract. Neither field reaches
// the repair bundle (D-383): the reason is Plan text, the fingerprint
// only selects a host key that the Run may pin.
type repairRequest struct {
	Reason        string `json:"reason"`
	AcceptHostKey string `json:"accept_host_key"`
}

var hostKeyFP = regexp.MustCompile(`^SHA256:[A-Za-z0-9+/]{43}$`)

// repairTarget is POST /targets/{name}/repair (D-385).
func (s *Server) repairTarget(w http.ResponseWriter, r *http.Request, p *principal) {
	if s.restoredMode(w) {
		return
	}
	var req repairRequest
	if !decode(w, r, &req, true) {
		return
	}
	if len(req.Reason) > 2000 {
		problem(w, http.StatusBadRequest, "bad-request", "Bad request", "reason is longer than 2000 characters.")
		return
	}
	if req.AcceptHostKey != "" && !hostKeyFP.MatchString(req.AcceptHostKey) {
		problem(w, http.StatusBadRequest, "bad-request", "Bad request", "accept_host_key must be SHA256:<43 base64 characters>.")
		return
	}
	name := r.PathValue("name")
	if _, err := s.headTarget(r.Context(), name); err != nil {
		s.fail(w, err)
		return
	}
	pl, err := s.cfg.Plans.ProposeRepair(r.Context(), name, req.Reason, req.AcceptHostKey, p.name)
	if err != nil {
		s.fail(w, err)
		return
	}
	s.writePlan(w, r, http.StatusCreated, pl)
}

// repairProblem answers the refusals of a repair (409, D-385); false
// when err is none of them.
func repairProblem(w http.ResponseWriter, err error) bool {
	switch {
	case errors.Is(err, plan.ErrRepairNotNeeded):
		problem(w, http.StatusConflict, "repair-not-needed", "Repair not needed", strings.TrimPrefix(err.Error(), "plan: "))
	case errors.Is(err, plan.ErrRepairNotReady):
		problem(w, http.StatusConflict, "repair-not-ready", "Repair not ready", strings.TrimPrefix(err.Error(), "plan: "))
	default:
		return false
	}
	return true
}
