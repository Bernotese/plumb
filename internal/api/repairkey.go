package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/Bernotese/plumb/internal/store"
)

// RepairKeys is the repair key view and rotation (D-382, T-191). The
// repair package implements it; the answers hold public data only.
type RepairKeys interface {
	// ViewJSON is RepairKeyView, or null without a key.
	ViewJSON(ctx context.Context) (json.RawMessage, error)
	RotateJSON(ctx context.Context, revokeOld bool, actor string) (json.RawMessage, error)
}

type rotateRequest struct {
	RevokeOld bool `json:"revoke_old"`
}

// rotateRepairKey is POST /settings/repair-key/rotate (admin session).
func (s *Server) rotateRepairKey(w http.ResponseWriter, r *http.Request, p *principal) {
	if s.restoredMode(w) {
		return
	}
	if s.cfg.RepairKeys == nil {
		problem(w, http.StatusConflict, "repair-off", "SSH repair is off", "This controller has no SSH repair (see the log of plumb serve).")
		return
	}
	var req rotateRequest
	if !decode(w, r, &req, true) {
		return
	}
	v, err := s.cfg.RepairKeys.RotateJSON(r.Context(), req.RevokeOld, p.name)
	switch {
	case errors.Is(err, store.ErrRotationRunning):
		problem(w, http.StatusConflict, "rotation-running", "Rotation running", "A previous repair key exists. Revoke it first ({\"revoke_old\": true}).")
	case errors.Is(err, store.ErrNothingToRevoke):
		problem(w, http.StatusConflict, "nothing-to-revoke", "Nothing to revoke", "There is no previous repair key.")
	case err != nil:
		s.fail(w, err)
	default:
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write(v)
	}
}

// repairKeyView is repair_key of GET /settings; nil without a key.
func (s *Server) repairKeyView(ctx context.Context) json.RawMessage {
	if s.cfg.RepairKeys == nil {
		return nil
	}
	v, err := s.cfg.RepairKeys.ViewJSON(ctx)
	if err != nil {
		s.log.Error("repair key view", "err", err)
		return nil
	}
	if string(v) == "null" {
		return nil
	}
	return v
}
