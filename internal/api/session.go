package api

import (
	"net/http"
	"strconv"
	"time"

	"github.com/Bernotese/plumb/internal/store"
)

type sessionView struct {
	Principal          string     `json:"principal"`
	MustChangePassword bool       `json:"must_change_password"`
	Tier               string     `json:"tier"`
	MaxRisk            string     `json:"max_risk,omitempty"`
	ExpiresAt          *time.Time `json:"expires_at"`
}

func (p *principal) view() sessionView {
	v := sessionView{Principal: p.name, Tier: p.tier, MustChangePassword: p.mustChange, MaxRisk: string(p.maxRisk)}
	if p.session != nil {
		t := p.session.ExpiresAt.UTC()
		v.ExpiresAt = &t
	}
	return v
}

func (s *Server) getSession(w http.ResponseWriter, r *http.Request, p *principal) {
	writeJSON(w, http.StatusOK, p.view())
}

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func (s *Server) login(w http.ResponseWriter, r *http.Request, _ *principal) {
	// Login CSRF: a foreign page must not log the browser in.
	if !s.sameOrigin(r) {
		problem(w, http.StatusForbidden, "origin-mismatch", "Origin mismatch", "The Origin header must match the server.")
		return
	}
	var req loginRequest
	if !decode(w, r, &req, false) {
		return
	}
	if req.Username == "" || len(req.Username) > 64 || req.Password == "" || len(req.Password) > 1024 {
		problem(w, http.StatusBadRequest, "bad-request", "Bad request", "username and password are required.")
		return
	}
	keys := []string{"user:" + req.Username, clientKey(r)}
	if wait := s.logins.blocked(keys...); wait > 0 {
		secs := int(wait.Seconds()) + 1
		w.Header().Set("Retry-After", strconv.Itoa(secs))
		problem(w, http.StatusTooManyRequests, "rate-limited", "Too many attempts", "Try again in "+strconv.Itoa(secs)+" seconds.")
		return
	}
	u, err := s.st.UserByName(r.Context(), req.Username)
	hash := s.dummy // an unknown user costs the same time
	if err == nil {
		hash = u.PasswordHash
	}
	if !VerifyPassword(req.Password, hash) || err != nil {
		s.logins.fail(keys...)
		problem(w, http.StatusUnauthorized, "unauthenticated", "Authentication required", "Wrong user name or password.")
		return
	}
	s.logins.reset(keys[0])
	id := randomText(32)
	sess := store.Session{IDHash: sha256Hex(id), UserID: u.ID, ExpiresAt: s.now().Add(s.cfg.SessionTTL)}
	if err := s.st.CreateSession(r.Context(), sess); err != nil {
		s.internal(w, err)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: id, Path: "/", Expires: sess.ExpiresAt,
		HttpOnly: true, Secure: !s.cfg.Dev, SameSite: http.SameSiteStrictMode})
	p := &principal{name: "user:" + u.Name, tier: TierAdmin, user: &u, session: &sess, mustChange: u.MustChangePassword}
	writeJSON(w, http.StatusOK, p.view())
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request, p *principal) {
	if err := s.st.DeleteSession(r.Context(), p.session.IDHash); err != nil {
		s.internal(w, err)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, Secure: !s.cfg.Dev, SameSite: http.SameSiteStrictMode})
	w.WriteHeader(http.StatusNoContent)
}

type passwordRequest struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

func (s *Server) changePassword(w http.ResponseWriter, r *http.Request, p *principal) {
	var req passwordRequest
	if !decode(w, r, &req, false) {
		return
	}
	if req.CurrentPassword == "" || len(req.CurrentPassword) > 1024 || len(req.NewPassword) > 1024 {
		problem(w, http.StatusBadRequest, "bad-request", "Bad request", "current_password and new_password are required.")
		return
	}
	keys := []string{"user:" + p.user.Name, clientKey(r)}
	if s.logins.blocked(keys...) > 0 {
		problem(w, http.StatusTooManyRequests, "rate-limited", "Too many attempts", "Try again later.")
		return
	}
	if !VerifyPassword(req.CurrentPassword, p.user.PasswordHash) {
		s.logins.fail(keys...)
		problem(w, http.StatusUnauthorized, "unauthenticated", "Authentication required", "The current password is wrong.")
		return
	}
	var errs []fieldError
	if len(req.NewPassword) < 12 {
		errs = append(errs, fieldError{"/new_password", "The new password needs at least 12 characters."})
	}
	if req.NewPassword == req.CurrentPassword {
		errs = append(errs, fieldError{"/new_password", "The new password must differ from the current one."})
	}
	if len(errs) > 0 {
		writeProblem(w, problemBody{Type: "urn:plumb:problem:validation", Title: "Validation failed", Status: http.StatusUnprocessableEntity, Errors: errs})
		return
	}
	// Other sessions of the user end; this one stays.
	if err := s.st.SetPassword(r.Context(), p.user.ID, HashPassword(req.NewPassword, s.cfg.PasswordParams), p.session.IDHash); err != nil {
		s.internal(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) internal(w http.ResponseWriter, err error) {
	s.log.Error("api", "err", err)
	problem(w, http.StatusInternalServerError, "internal", "Internal error", "")
}
