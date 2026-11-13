package api

import (
	"errors"
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
	ip := clientKey(r)
	pair := "user:" + req.Username + "|" + ip
	if wait := max(s.loginPair.blocked(pair), s.loginIP.blocked(ip), s.loginUser.blocked("user:"+req.Username)); wait > 0 {
		tooMany(w, wait)
		return
	}
	u, err := s.st.UserByName(r.Context(), req.Username)
	hash := s.dummy // an unknown user costs the same time
	if err == nil {
		hash = u.PasswordHash
	}
	var good bool
	if !s.withHashSlot(func() { good = VerifyPassword(req.Password, hash) }) {
		tooMany(w, time.Second)
		return
	}
	// A user that a restore locked cannot log in (D-303).
	if !good || err != nil || !u.LockedAt.IsZero() {
		s.loginPair.fail(pair)
		s.loginIP.fail(ip)
		if err == nil { // only real users get a user key (review m4)
			s.loginUser.fail("user:" + req.Username)
		}
		problem(w, http.StatusUnauthorized, "unauthenticated", "Authentication required", "Wrong user name or password.")
		return
	}
	s.loginPair.reset(pair)
	id := randomText(32)
	sess := store.Session{IDHash: sha256Hex(id), UserID: u.ID, ExpiresAt: s.now().Add(s.cfg.SessionTTL)}
	if err := s.st.CreateSession(r.Context(), sess); err != nil {
		s.internal(w, err)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: s.cookieName(), Value: id, Path: "/", Expires: sess.ExpiresAt,
		HttpOnly: true, Secure: !s.cfg.Dev, SameSite: http.SameSiteStrictMode})
	p := &principal{name: "user:" + u.Name, tier: TierAdmin, user: &u, session: &sess, mustChange: u.MustChangePassword}
	writeJSON(w, http.StatusOK, p.view())
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request, p *principal) {
	if err := s.st.DeleteSession(r.Context(), p.session.IDHash); err != nil {
		s.internal(w, err)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: s.cookieName(), Value: "", Path: "/", MaxAge: -1,
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
	pair := "user:" + p.user.Name + "|" + clientKey(r)
	if wait := s.loginPair.blocked(pair); wait > 0 {
		tooMany(w, wait)
		return
	}
	var good bool
	if !s.withHashSlot(func() { good = VerifyPassword(req.CurrentPassword, p.user.PasswordHash) }) {
		tooMany(w, time.Second)
		return
	}
	if !good {
		s.loginPair.fail(pair)
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
	var hash string
	if !s.withHashSlot(func() { hash = HashPassword(req.NewPassword, s.cfg.PasswordParams) }) {
		tooMany(w, time.Second)
		return
	}
	if err := s.st.SetPassword(r.Context(), p.user.ID, hash, p.session.IDHash); err != nil {
		s.internal(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type oneTimePassword struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// resetUserPassword gives another user a new one-time password (D-321):
// shown once, must change at the first login; it ends a lock of a
// restore and every session of the user. Humans only.
func (s *Server) resetUserPassword(w http.ResponseWriter, r *http.Request, p *principal) {
	name := r.PathValue("name")
	if name == "" || len(name) > 64 {
		problem(w, http.StatusNotFound, "not-found", "Not found", "No such user.")
		return
	}
	if name == p.user.Name {
		problem(w, http.StatusConflict, "own-user", "Own user", "Change your own password with PUT /api/v1/session/password.")
		return
	}
	password := RandomPassword()
	var hash string
	if !s.withHashSlot(func() { hash = HashPassword(password, s.cfg.PasswordParams) }) {
		tooMany(w, time.Second)
		return
	}
	err := s.st.ResetPassword(r.Context(), name, hash)
	if errors.Is(err, store.ErrNotFound) {
		problem(w, http.StatusNotFound, "not-found", "Not found", "No such user.")
		return
	}
	if err != nil {
		s.internal(w, err)
		return
	}
	if s.cfg.Events != nil {
		if _, err := s.cfg.Events.Emit(r.Context(), "user.password_reset", "user:"+name, p.name,
			map[string]string{"username": name}); err != nil {
			s.log.Error("user event", "user", name, "err", err)
		}
	}
	writeJSON(w, http.StatusOK, oneTimePassword{Username: name, Password: password}) // writeJSON sets no-store
}

func (s *Server) internal(w http.ResponseWriter, err error) {
	s.log.Error("api", "err", err)
	problem(w, http.StatusInternalServerError, "internal", "Internal error", "")
}

// tooMany answers 429 with Retry-After.
func tooMany(w http.ResponseWriter, wait time.Duration) {
	secs := int(wait.Seconds())
	if secs < 1 {
		secs = 1
	}
	w.Header().Set("Retry-After", strconv.Itoa(secs))
	problem(w, http.StatusTooManyRequests, "rate-limited", "Too many attempts", "Try again in "+strconv.Itoa(secs)+" seconds.")
}
