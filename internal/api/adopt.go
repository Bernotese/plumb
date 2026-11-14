package api

import (
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Bernotese/plumb/internal/plan"
	"github.com/Bernotese/plumb/internal/store"
)

// Adoption and the install command (D-373, D-386 … D-388, D-391, D-392,
// ARCHITECTURE §19).

var (
	digestRE = regexp.MustCompile(`^[0-9a-f]{40}$`)
	nodeRE   = regexp.MustCompile(`^[a-zA-Z0-9]([a-zA-Z0-9.-]{0,62})$`)
)

type unmanagedView struct {
	Node      string         `json:"node"`
	VMID      int            `json:"vmid"`
	Name      string         `json:"name"`
	Type      string         `json:"type"`
	Power     string         `json:"power,omitempty"`
	Digest    string         `json:"digest"`
	Adoptable bool           `json:"adoptable"`
	Reasons   []string       `json:"reasons"`
	Warnings  []string       `json:"warnings"`
	Guest     map[string]any `json:"guest"`
	Present   presentView    `json:"present"`
	// AgentLeft: the agent of a deleted adopted Target is still in the
	// guest; its unbind was not confirmed (D-396).
	AgentLeft bool `json:"agent_left"`
}

// presentView: only whether a secret config key exists (D-340).
type presentView struct {
	CIPassword  bool `json:"cipassword"`
	SSHKeys     bool `json:"sshkeys"`
	Description bool `json:"description"`
	Env         bool `json:"env"`
}

// listUnmanagedGuests is GET /guests/unmanaged.
func (s *Server) listUnmanagedGuests(w http.ResponseWriter, r *http.Request, _ *principal) {
	list, err := s.cfg.Plans.UnmanagedGuests(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	items := make([]unmanagedView, 0, len(list))
	for _, u := range list {
		id, _ := strconv.Atoi(string(u.ID))
		v := unmanagedView{Node: u.Node, VMID: id, Name: clipName(u.Name), Type: string(u.Kind), Power: string(u.Power),
			Digest: u.Digest, Adoptable: u.Adoptable(), Reasons: u.Reasons, Warnings: u.Warnings, Guest: u.Guest,
			Present: presentView{CIPassword: u.Present.CIPassword, SSHKeys: u.Present.SSHKeys, Description: u.Present.Description, Env: u.Present.Env}}
		if v.Warnings == nil {
			v.Warnings = []string{}
		}
		if _, err := s.st.ParkedByRef(r.Context(), string(u.ID)); err == nil {
			v.AgentLeft = true
		} else if !errors.Is(err, store.ErrNotFound) {
			s.fail(w, err)
			return
		}
		items = append(items, v)
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

type adoptRequest struct {
	Node   string `json:"node"`
	VMID   int    `json:"vmid"`
	Digest string `json:"digest"`
	Reason string `json:"reason"`
}

// adoptTarget is POST /targets/{name}/adopt.
func (s *Server) adoptTarget(w http.ResponseWriter, r *http.Request, p *principal) {
	if s.restoredMode(w) {
		return
	}
	var req adoptRequest
	if !decode(w, r, &req, false) {
		return
	}
	switch {
	case !nodeRE.MatchString(req.Node):
		problem(w, http.StatusBadRequest, "bad-request", "Bad request", "node must be a Proxmox node name.")
		return
	case req.VMID < 100 || req.VMID > 999999999:
		problem(w, http.StatusBadRequest, "bad-request", "Bad request", "vmid must be 100 … 999999999.")
		return
	case !digestRE.MatchString(req.Digest):
		problem(w, http.StatusBadRequest, "bad-request", "Bad request", "digest must be the 40 hex characters of GET /guests/unmanaged.")
		return
	case len(req.Reason) > 2000:
		problem(w, http.StatusBadRequest, "bad-request", "Bad request", "reason is longer than 2000 characters.")
		return
	}
	name := r.PathValue("name")
	if _, err := s.headTarget(r.Context(), name); err == nil {
		problem(w, http.StatusConflict, "document-exists", "Target exists", "A Target of this name exists; adopt the guest under its own (Proxmox) name.")
		return
	} else if !errors.Is(err, store.ErrNotFound) {
		s.fail(w, err)
		return
	}
	pl, err := s.cfg.Plans.ProposeAdopt(r.Context(), plan.AdoptRequest{Target: name, Node: req.Node, VMID: req.VMID, Digest: req.Digest}, p.name, req.Reason)
	if err != nil {
		s.fail(w, err)
		return
	}
	s.writePlan(w, r, http.StatusCreated, pl)
}

// createInstallCommand is POST /targets/{name}/install-command (admin
// session): the command holds a one-time token, so it is shown once.
func (s *Server) createInstallCommand(w http.ResponseWriter, r *http.Request, p *principal) {
	if s.restoredMode(w) {
		return
	}
	name := r.PathValue("name")
	if _, err := s.headTarget(r.Context(), name); err != nil {
		s.fail(w, err)
		return
	}
	cmd, expires, err := s.cfg.Plans.InstallCommand(r.Context(), name, p.name)
	if err != nil {
		s.fail(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusCreated, map[string]any{"command": cmd, "expires_at": expires.UTC().Format(time.RFC3339)})
}

// clipName bounds guest text (a Proxmox name) for the answer.
func clipName(s string) string {
	if len(s) > 128 {
		return s[:128]
	}
	return s
}

// forgetAgent is POST /guests/unmanaged/{vmid}/forget-agent (admin
// session): it ends the park of the agent left in the guest (D-396). The
// credential is revoked; what is in the guest stays for a person.
func (s *Server) forgetAgent(w http.ResponseWriter, r *http.Request, p *principal) {
	vmid, err := strconv.Atoi(r.PathValue("vmid"))
	if err != nil || vmid < 100 {
		problem(w, http.StatusBadRequest, "bad-request", "Bad request", "vmid must be a VMID.")
		return
	}
	if err := s.cfg.Plans.ForgetAgent(r.Context(), strconv.Itoa(vmid), p.name); err != nil {
		s.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// adoptProblem answers the refusals of the adoption and the install
// command (409, ARCHITECTURE §19); false when err is none of them.
func adoptProblem(w http.ResponseWriter, err error) bool {
	detail := func() string { return strings.TrimPrefix(err.Error(), "plan: ") }
	switch {
	case errors.Is(err, plan.ErrGuestChanged):
		problem(w, http.StatusConflict, "guest-changed", "Guest changed", detail())
	case errors.Is(err, plan.ErrGuestBound):
		problem(w, http.StatusConflict, "guest-bound", "Guest is bound", detail())
	case errors.Is(err, plan.ErrNotAdoptable):
		problem(w, http.StatusConflict, "not-adoptable", "Guest cannot be adopted", detail())
	case errors.Is(err, plan.ErrAdoptedTarget):
		problem(w, http.StatusConflict, "adopted-target", "Adopted Target", detail())
	case errors.Is(err, plan.ErrAgentLeft):
		problem(w, http.StatusConflict, "agent-left", "Agent left in the guest", detail())
	case errors.Is(err, plan.ErrNoGuest):
		problem(w, http.StatusConflict, "no-guest", "No guest", detail())
	case errors.Is(err, plan.ErrNoAdopter), errors.Is(err, plan.ErrInstallUnavailable):
		problem(w, http.StatusServiceUnavailable, "unavailable", "Not available", detail())
	default:
		return false
	}
	return true
}
