package api

import (
	"errors"
	"net/http"
	"regexp"
	"time"

	"github.com/Bernotese/plumb/internal/store"
	"github.com/Bernotese/plumb/internal/wire"
)

// User management (D-329, D-330, D-342, D-343). Every operation here is
// for an admin session only; the route table enforces it.

var userNameRE = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)

func validRole(r string) bool {
	return r == TierRead || r == TierPlan || r == TierApply || r == TierAdmin
}

// userView is a user without its password hash.
type userView struct {
	Name               string     `json:"name"`
	Role               string     `json:"role"`
	Disabled           bool       `json:"disabled"`
	Locked             bool       `json:"locked"`
	MustChangePassword bool       `json:"must_change_password"`
	CreatedAt          time.Time  `json:"created_at"`
	CreatedBy          string     `json:"created_by"`
	LastLoginAt        *time.Time `json:"last_login_at"`
}

func viewUser(u store.User) userView {
	return userView{Name: u.Name, Role: u.Role, Disabled: !u.DisabledAt.IsZero(), Locked: !u.LockedAt.IsZero(),
		MustChangePassword: u.MustChangePassword, CreatedAt: u.CreatedAt.UTC(), CreatedBy: u.CreatedBy,
		LastLoginAt: utc(u.LastLoginAt)}
}

func (s *Server) listUsers(w http.ResponseWriter, r *http.Request, _ *principal) {
	n, err := limit(r)
	if err != nil {
		badQuery(w, err)
		return
	}
	after, err := decodeCursor(r)
	if err != nil {
		badQuery(w, err)
		return
	}
	us, err := s.st.Users(r.Context(), after, n+1)
	if err != nil {
		s.internal(w, err)
		return
	}
	items := []userView{}
	var next *string
	for i, u := range us {
		if i == n {
			next = encodeCursor(us[n-1].Name)
			break
		}
		items = append(items, viewUser(u))
	}
	writeJSON(w, http.StatusOK, page{Items: items, Next: next})
}

type createUserRequest struct {
	Name string `json:"name"`
	Role string `json:"role"`
}

// createUser makes a user with a one-time password (D-330, rules of
// D-321): shown once (no-store), only the hash is stored, it must change
// at the first login.
func (s *Server) createUser(w http.ResponseWriter, r *http.Request, p *principal) {
	var req createUserRequest
	if !decode(w, r, &req, false) {
		return
	}
	var errs []fieldError
	if !userNameRE.MatchString(req.Name) {
		errs = append(errs, fieldError{"/name", "A user name is 1 to 32 characters: a-z, 0-9, _ and -, not first a digit or -."})
	}
	if !validRole(req.Role) {
		errs = append(errs, fieldError{"/role", "role is read, plan, apply or admin."})
	}
	if len(errs) > 0 {
		writeProblem(w, problemBody{Type: "urn:plumb:problem:validation", Title: "Validation failed",
			Status: http.StatusUnprocessableEntity, Errors: errs})
		return
	}
	password := RandomPassword()
	var hash string
	if !s.withHashSlot(func() { hash = HashPassword(password, s.cfg.PasswordParams) }) {
		tooMany(w, time.Second)
		return
	}
	_, err := s.st.CreateUser(r.Context(), store.User{Name: req.Name, PasswordHash: hash, MustChangePassword: true,
		Role: req.Role, CreatedBy: p.name})
	if errors.Is(err, store.ErrExists) {
		problem(w, http.StatusConflict, "user-exists", "User exists",
			"A user with this name exists, or existed and is disabled. Names are never given twice.")
		return
	}
	if err != nil {
		s.internal(w, err)
		return
	}
	s.userEvent(r, "user.created", req.Name, p, map[string]any{"role": req.Role})
	w.Header().Set("Location", "/api/v1/users/"+req.Name)
	writeJSON(w, http.StatusCreated, oneTimePassword{Username: req.Name, Password: password}) // no-store
}

type changeUserRequest struct {
	Role     *string `json:"role"`
	Disabled *bool   `json:"disabled"`
}

// changeUser changes the role of a user or disables it (D-330). The own
// user is 409 own-user (a user never changes its own role); the last
// enabled admin is 409 last-admin.
func (s *Server) changeUser(w http.ResponseWriter, r *http.Request, p *principal) {
	name := r.PathValue("name")
	if name == "" || len(name) > 64 {
		problem(w, http.StatusNotFound, "not-found", "Not found", "No such user.")
		return
	}
	var req changeUserRequest
	if !decode(w, r, &req, false) {
		return
	}
	if req.Role == nil && req.Disabled == nil {
		problem(w, http.StatusBadRequest, "bad-request", "Bad request", "Give role, disabled or both.")
		return
	}
	if req.Role != nil && !validRole(*req.Role) {
		writeProblem(w, problemBody{Type: "urn:plumb:problem:validation", Title: "Validation failed",
			Status: http.StatusUnprocessableEntity, Errors: []fieldError{{"/role", "role is read, plan, apply or admin."}}})
		return
	}
	if name == p.user.Name {
		problem(w, http.StatusConflict, "own-user", "Own user",
			"You cannot change your own role or disable yourself. Another admin can.")
		return
	}
	res, err := s.st.ChangeUser(r.Context(), name, store.UserChange{Role: req.Role, Disabled: req.Disabled})
	switch {
	case errors.Is(err, store.ErrNotFound):
		problem(w, http.StatusNotFound, "not-found", "Not found", "No such user.")
		return
	case errors.Is(err, store.ErrLastAdmin):
		problem(w, http.StatusConflict, "last-admin", "Last admin",
			"This is the last enabled admin. Make another admin first.")
		return
	case err != nil:
		s.internal(w, err)
		return
	}
	if res.Changed {
		s.userEvent(r, "user.changed", name, p, map[string]any{"role": res.User.Role, "disabled": !res.User.DisabledAt.IsZero()})
	}
	writeJSON(w, http.StatusOK, viewUser(res.User))
}

func (s *Server) userEvent(r *http.Request, kind, name string, p *principal, data map[string]any) {
	if s.cfg.Events == nil {
		return
	}
	if _, err := s.cfg.Events.Emit(r.Context(), kind, "user:"+name, p.name, data); err != nil {
		s.log.Error("user event", "user", name, "err", err)
	}
}

// --- roles inside operations (D-329, review M2 of #241) ---

// approveAsHuman is the approval rule for a session: role apply approves
// up to disruptive (also human-only Plans up to that risk); destructive
// needs role admin. The route already needs apply.
func approveAsHuman(p *principal, risk wire.Risk) error {
	if tierLevel[p.tier] < tierLevel[TierApply] {
		return &riskError{"forbidden", "Forbidden", "Approving needs role apply; you have " + p.tier + "."}
	}
	if risk == wire.RiskDestructive && p.tier != TierAdmin {
		return &riskError{"risk-too-high", "Risk exceeds your role",
			"Plan risk is destructive. Only a user with role admin approves it; you have " + p.tier + "."}
	}
	return nil
}

// ignoreAsHuman is the ignore_once rule for a session: role apply up to
// disruptive (also sensitive changes), admin for destructive.
func ignoreAsHuman(p *principal, changes []wire.Change) error {
	if tierLevel[p.tier] < tierLevel[TierApply] {
		return &riskError{"forbidden", "Forbidden", "Ignoring drift needs role apply; you have " + p.tier + "."}
	}
	for _, c := range changes {
		if c.Risk == wire.RiskDestructive && p.tier != TierAdmin {
			return &riskError{"risk-too-high", "Risk exceeds your role",
				"Attribute " + c.Attr + " has risk destructive. Only a user with role admin ignores it; you have " + p.tier + "."}
		}
	}
	return nil
}
