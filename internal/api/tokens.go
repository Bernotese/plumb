package api

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Bernotese/plumb/internal/store"
	"github.com/Bernotese/plumb/internal/wire"
)

// tokenView is a token without its bearer value or hash (D-046).
type tokenView struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Tier       string     `json:"tier"`
	MaxRisk    *string    `json:"max_risk"`
	CreatedBy  string     `json:"created_by"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at"`
	RevokedAt  *time.Time `json:"revoked_at"`
	DisabledAt *time.Time `json:"disabled_at"` // D-303
}

func viewToken(t store.Token) tokenView {
	return tokenView{ID: t.ID, Name: t.Name, Tier: t.Tier, MaxRisk: nullString(t.MaxRisk), CreatedBy: t.CreatedBy,
		CreatedAt: t.CreatedAt.UTC(), LastUsedAt: utc(t.LastUsedAt), RevokedAt: utc(t.RevokedAt),
		DisabledAt: utc(t.DisabledAt)}
}

func (s *Server) listTokens(w http.ResponseWriter, r *http.Request, _ *principal) {
	n, err := limit(r)
	if err != nil {
		badQuery(w, err)
		return
	}
	before, err := idCursor(r)
	if err != nil {
		badQuery(w, err)
		return
	}
	toks, err := s.st.Tokens(r.Context(), before, n+1)
	if err != nil {
		s.internal(w, err)
		return
	}
	items := []tokenView{}
	var next *string
	for i, t := range toks {
		if i == n {
			next = encodeCursor(strconv.FormatInt(toks[n-1].Seq, 10))
			break
		}
		items = append(items, viewToken(t))
	}
	writeJSON(w, http.StatusOK, page{Items: items, Next: next})
}

type tokenRequest struct {
	Name    string    `json:"name"`
	Tier    string    `json:"tier"`
	MaxRisk wire.Risk `json:"max_risk"`
}

type tokenCreated struct {
	Token  tokenView `json:"token"`
	Bearer string    `json:"bearer"`
}

// validate checks a token request against D-046 and the contract. The
// errors point at the request body.
func (req tokenRequest) validate() []fieldError {
	var errs []fieldError
	if n := utf8.RuneCountInString(req.Name); strings.TrimSpace(req.Name) == "" || n > 100 {
		errs = append(errs, fieldError{"/name", "A name of 1 to 100 characters is required."})
	}
	switch req.Tier {
	case TierRead, TierPlan:
		if req.MaxRisk != "" {
			errs = append(errs, fieldError{"/max_risk", "Only an apply token has a max_risk (D-046)."})
		}
	case TierApply:
		if req.MaxRisk != wire.RiskReversible && req.MaxRisk != wire.RiskDisruptive {
			errs = append(errs, fieldError{"/max_risk", "An apply token needs max_risk reversible or disruptive (D-046)."})
		}
	default:
		errs = append(errs, fieldError{"/tier", "tier must be read, plan or apply."})
	}
	return errs
}

func (s *Server) createToken(w http.ResponseWriter, r *http.Request, p *principal) {
	var req tokenRequest
	if !decode(w, r, &req, false) {
		return
	}
	if errs := req.validate(); len(errs) > 0 {
		writeProblem(w, problemBody{Type: "urn:plumb:problem:validation", Title: "Validation failed",
			Status: http.StatusUnprocessableEntity, Errors: errs})
		return
	}
	bearer, t, err := CreateToken(r.Context(), s.st, req.Name, req.Tier, req.MaxRisk, p.name)
	if errors.Is(err, ErrBadToken) { // validate and CreateToken agree; this is a backstop
		problem(w, http.StatusUnprocessableEntity, "validation", "Validation failed", err.Error())
		return
	}
	if err != nil {
		s.internal(w, err)
		return
	}
	s.tokenEvent(r, "token.created", t, p)
	writeJSON(w, http.StatusCreated, tokenCreated{Token: viewToken(t), Bearer: bearer}) // writeJSON sets no-store
}

func (s *Server) revokeToken(w http.ResponseWriter, r *http.Request, p *principal) {
	id := r.PathValue("id")
	if len(id) > 64 {
		problem(w, http.StatusNotFound, "not-found", "Not found", "No such token.")
		return
	}
	t, revoked, err := s.st.RevokeToken(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		problem(w, http.StatusNotFound, "not-found", "Not found", "No such token.")
		return
	}
	if err != nil {
		s.internal(w, err)
		return
	}
	// The open event streams of the token end now, not at the next
	// keep-alive (D-070). Also on a repeated revoke: it costs nothing.
	s.endStreams("token:" + t.ID)
	if revoked {
		s.tokenEvent(r, "token.revoked", t, p)
	}
	w.WriteHeader(http.StatusNoContent)
}

// tokenEvent emits a token event. The data holds no bearer and no hash.
func (s *Server) tokenEvent(r *http.Request, typ string, t store.Token, p *principal) {
	if s.cfg.Events == nil {
		return
	}
	if _, err := s.cfg.Events.Emit(r.Context(), typ, "token:"+t.ID, p.name,
		map[string]string{"id": t.ID, "name": t.Name, "tier": t.Tier}); err != nil {
		s.log.Error("token event", "type", typ, "token", t.ID, "err", err)
	}
}
