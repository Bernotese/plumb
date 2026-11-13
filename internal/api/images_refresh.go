package api

import (
	"errors"
	"net/http"
	"time"

	"github.com/Bernotese/plumb/internal/plan"
)

// pinRefreshView is PinRefresh of the contract: every field, null where
// there is no value.
type pinRefreshView struct {
	Image          string     `json:"image"`
	Source         string     `json:"source"`
	Signed         bool       `json:"signed"`
	KeyFingerprint *string    `json:"key_fingerprint"`
	File           string     `json:"file"`
	Algorithm      string     `json:"algorithm"`
	Old            *string    `json:"old"`
	New            string     `json:"new"`
	UsedBefore     bool       `json:"used_before"`
	SignedAt       *time.Time `json:"signed_at"`
	LastSignedAt   *time.Time `json:"last_signed_at"`
}

func viewPin(p plan.PinRefresh) pinRefreshView {
	return pinRefreshView{Image: p.Image, Source: p.Source, Signed: p.Signed, KeyFingerprint: nullString(p.KeyFingerprint),
		File: p.File, Algorithm: p.Algorithm, Old: nullString(p.Old), New: p.New, UsedBefore: p.UsedBefore,
		SignedAt: p.SignedAt, LastSignedAt: p.LastSignedAt}
}

// refreshImagePin is POST /images/{name}/refresh (D-335): a Plan of origin
// pin_refresh, or 200 unchanged. A failed vendor list is 502
// pin-source-failed; its detail names the step, never fetched data.
func (s *Server) refreshImagePin(w http.ResponseWriter, r *http.Request, p *principal) {
	pl, pin, err := s.cfg.Plans.RefreshPin(r.Context(), r.PathValue("name"), p.name)
	var pse *plan.PinSourceError
	switch {
	case errors.As(err, &pse):
		problem(w, http.StatusBadGateway, "pin-source-failed", "Checksum source failed", pse.Detail())
		return
	case err != nil:
		s.fail(w, err)
		return
	case pl.ID == 0:
		writeJSON(w, http.StatusOK, struct {
			Status string         `json:"status"`
			Pin    pinRefreshView `json:"pin"`
		}{"unchanged", viewPin(pin)})
		return
	}
	s.writePlan(w, r, http.StatusCreated, pl)
}
