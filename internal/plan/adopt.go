package plan

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Bernotese/plumb/internal/driver"
	"github.com/Bernotese/plumb/internal/link/agentinstall"
	"github.com/Bernotese/plumb/internal/state"
	"github.com/Bernotese/plumb/internal/store"
	"github.com/Bernotese/plumb/internal/wire"
)

// Adoption (D-373, D-378, D-386, D-391, D-392, ARCHITECTURE §19). A guest
// that a person put into the pool becomes a Target by a human-only Plan.
// Plumb never moves a guest and writes nothing to Proxmox to adopt one.

// OriginAdopt is the origin of an adopt Plan.
const OriginAdopt = "adopt"

// Errors of the adoption. The API maps them to 409 with these codes.
var (
	// ErrNotAdoptable: the guest has a reason against the adoption.
	ErrNotAdoptable = errors.New("not-adoptable")
	// ErrGuestChanged: the digest or the name differs from the request
	// (the guest changed, was renamed, or the VMID holds another guest).
	ErrGuestChanged = errors.New("guest-changed")
	// ErrGuestBound: a Target or an Image already holds the guest.
	ErrGuestBound = errors.New("guest-bound")
	// ErrNoAdopter: the driver cannot adopt (the fake without it, or a
	// driver that does not implement driver.Adopter).
	ErrNoAdopter = errors.New("plan: the driver cannot adopt guests")
)

// AdoptRequest names the guest of an adoption and the Target name.
type AdoptRequest struct {
	Target string `json:"target"`
	Node   string `json:"node"`
	VMID   int    `json:"vmid"`
	Digest string `json:"digest"`
}

func (e *Engine) adopter() (driver.Adopter, error) {
	a, ok := e.cfg.Driver.(driver.Adopter)
	if !ok {
		return nil, ErrNoAdopter
	}
	return a, nil
}

// boundRefs are the guest refs that any Target or Image binding holds,
// foreign marks included: such a guest is never unmanaged.
func (e *Engine) boundRefs(ctx context.Context) (map[string]bool, error) {
	out := map[string]bool{}
	for _, kind := range []string{state.KindTarget, state.KindImage} {
		binds, err := e.st.Bindings(ctx, kind)
		if err != nil {
			return nil, err
		}
		for _, b := range binds {
			out[b.Ref] = true
		}
	}
	return out, nil
}

// UnmanagedGuests lists the pool guests that no Target, Image binding,
// build or template holds, each as InspectGuest reads it (D-386).
func (e *Engine) UnmanagedGuests(ctx context.Context) ([]driver.Unmanaged, error) {
	a, err := e.adopter()
	if err != nil {
		return nil, err
	}
	pool, err := e.UnknownGuests(ctx)
	if err != nil {
		return nil, err
	}
	bound, err := e.boundRefs(ctx)
	if err != nil {
		return nil, err
	}
	out := []driver.Unmanaged{}
	for _, g := range pool {
		if g.Template || bound[string(g.ID)] {
			continue
		}
		u, err := a.InspectGuest(ctx, g.ID)
		if errors.Is(err, driver.ErrNotInPool) || errors.Is(err, driver.ErrNotFound) {
			continue // left the pool since the list
		}
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, nil
}

// checkAdoptable reads the guest again and checks it against the request:
// in the pool, adoptable, the same digest, and the Proxmox name equal to
// the Target name (D-392). It returns the fresh view.
func (e *Engine) checkAdoptable(ctx context.Context, req AdoptRequest) (driver.Unmanaged, error) {
	a, err := e.adopter()
	if err != nil {
		return driver.Unmanaged{}, err
	}
	bound, err := e.boundRefs(ctx)
	if err != nil {
		return driver.Unmanaged{}, err
	}
	ref := driver.GuestID(strconv.Itoa(req.VMID))
	if bound[string(ref)] {
		return driver.Unmanaged{}, fmt.Errorf("%w: VMID %d is held by a Target or an Image", ErrGuestBound, req.VMID)
	}
	u, err := a.InspectGuest(ctx, ref)
	if errors.Is(err, driver.ErrNotInPool) || errors.Is(err, driver.ErrNotFound) {
		return driver.Unmanaged{}, fmt.Errorf("%w: VMID %d is not in the pool", ErrGuestChanged, req.VMID)
	}
	if err != nil {
		return driver.Unmanaged{}, err
	}
	switch {
	case u.Node != req.Node:
		return driver.Unmanaged{}, fmt.Errorf("%w: the guest is on node %q, not %q", ErrGuestChanged, u.Node, req.Node)
	case u.Digest == "" || u.Digest != req.Digest:
		return driver.Unmanaged{}, fmt.Errorf("%w: the Proxmox config of VMID %d changed (digest)", ErrGuestChanged, req.VMID)
	case u.Name != req.Target:
		return driver.Unmanaged{}, fmt.Errorf("%w: the guest is named %q; the Target must have the same name (D-392)", ErrGuestChanged, u.Name)
	case !u.Adoptable():
		return driver.Unmanaged{}, fmt.Errorf("%w: %v", ErrNotAdoptable, u.Reasons)
	}
	return u, nil
}

// AdoptDoc checks the request and returns the Target document of the
// adoption: type, guest (never ssh_keys) and spec.adopted, no image
// (D-386). The adopt Plan puts it; the Run checks the guest again.
func (e *Engine) AdoptDoc(ctx context.Context, req AdoptRequest) (state.Doc, error) {
	u, err := e.checkAdoptable(ctx, req)
	if err != nil {
		return state.Doc{}, err
	}
	spec := map[string]any{
		"type":    string(u.Kind),
		"guest":   u.Guest,
		"adopted": map[string]any{"node": req.Node, "vmid": req.VMID, "delete_guest": false},
	}
	raw, err := json.Marshal(spec)
	if err != nil {
		return state.Doc{}, err
	}
	return state.Doc{Kind: state.KindTarget, Name: req.Target, Spec: raw}, nil
}

// ProposeAdopt makes the adopt Plan of a guest (D-386): it checks the
// guest, builds the Target document and proposes it with origin adopt and
// the checked request (the Run checks it again). Human-only by origin.
func (e *Engine) ProposeAdopt(ctx context.Context, req AdoptRequest, author, reason string) (store.Plan, error) {
	doc, err := e.AdoptDoc(ctx, req)
	if err != nil {
		return store.Plan{}, err
	}
	if reason == "" {
		reason = fmt.Sprintf("adopt the guest %d on %s as Target %s", req.VMID, req.Node, req.Target)
	}
	return e.Propose(ctx, Input{Author: author, Reason: reason, Origin: OriginAdopt,
		Proposal: Proposal{Put: []state.Doc{doc}, Adopt: []AdoptRequest{req}}})
}

// adoptBind is the Run step of an adopt Plan: it reads the guest again
// (node, VMID, name, digest; D-386) and binds it to the Target. It writes
// nothing to Proxmox. The driver learns the ref before the Observe, so
// own() accepts a VMID outside the Plumb range (D-392).
func (e *Engine) adoptBind(ctx context.Context, req AdoptRequest) error {
	if _, err := e.checkAdoptable(ctx, req); err != nil {
		return err
	}
	ref := driver.GuestID(strconv.Itoa(req.VMID))
	a, aerr := e.adopter()
	if aerr != nil {
		return aerr
	}
	adoptMu.Lock()
	defer adoptMu.Unlock()
	before, err := adoptedRefsFn(e, ctx)
	if err != nil {
		return err
	}
	with := maps.Clone(before)
	with[ref] = req.Target
	a.SetAdopted(with)
	st, err := e.cfg.Driver.Observe(ctx, ref)
	if err == nil {
		err = e.st.PutBinding(ctx, store.Binding{Kind: state.KindTarget, Name: req.Target, Ref: string(ref), Digest: pollKey(st)})
	}
	// After the bind the set is read again. When that read fails, the set
	// falls back to what is known: the extra ref stays only when the bind
	// worked (review L2 of #299).
	refs, serr := adoptedRefsFn(e, ctx)
	switch {
	case serr == nil:
		a.SetAdopted(refs)
	case err == nil:
		a.SetAdopted(with)
	default:
		a.SetAdopted(before)
	}
	if err == nil {
		err = serr
	}
	return err
}

// adoptedRefsFn reads the adopted refs; tests replace it to make a read
// fail.
var adoptedRefsFn = (*Engine).adoptedRefs

// adoptMu serializes the whole-set updates of SetAdopted.
var adoptMu sync.Mutex

// SyncAdopted tells the driver the guests of the bound adopted Targets
// of the head revision (D-392): only those may be outside the Plumb VMID
// range, and only under their Target's name. The engine calls it at the
// start and after every Run that changed a Target.
func (e *Engine) SyncAdopted(ctx context.Context) error {
	adoptMu.Lock()
	defer adoptMu.Unlock()
	return e.syncAdoptedLocked(ctx, nil)
}

func (e *Engine) syncAdoptedLocked(ctx context.Context, extra map[driver.GuestID]string) error {
	a, err := e.adopter()
	if err != nil {
		return nil // no adoption in this driver: nothing to tell
	}
	refs, err := adoptedRefsFn(e, ctx)
	if err != nil {
		return err
	}
	for ref, name := range extra {
		refs[ref] = name
	}
	a.SetAdopted(refs)
	return nil
}

// adoptedRefs are the guests of the bound adopted Targets of the head.
func (e *Engine) adoptedRefs(ctx context.Context) (map[driver.GuestID]string, error) {
	_, set, err := e.base(ctx)
	if err != nil {
		return nil, err
	}
	binds, err := e.st.Bindings(ctx, state.KindTarget)
	if err != nil {
		return nil, err
	}
	refs := map[driver.GuestID]string{}
	for _, b := range binds {
		ad, ok := set.AdoptedOf(b.Name)
		if !ok || strconv.Itoa(ad.VMID) != b.Ref {
			continue
		}
		refs[driver.GuestID(b.Ref)] = b.Name
	}
	return refs, nil
}

// ErrAdoptedTarget: a re-enrollment of an adopted Target (D-392).
var ErrAdoptedTarget = errors.New("adopted-target")

// attrAdopted is the change of guest:config that shows an adopt or an
// unbind in the Plan.
const attrAdopted = "adopted"

func adoptedText(a state.Adopted) string {
	return fmt.Sprintf("VMID %d on %s", a.VMID, a.Node)
}

// boundAdopted is spec.adopted of a bound Target with the VMID of its
// binding: every text of a Plan names the guest that the Run touches
// (review A1 of #299), never only what the document says.
func boundAdopted(a state.Adopted, ref string) state.Adopted {
	if n, err := strconv.Atoi(ref); err == nil {
		a.VMID = n
	}
	return a
}

// ErrAdoptedChange: spec.adopted of a bound Target changed (A1 of #299).
var ErrAdoptedChange = errors.New("plan: adopted.node and adopted.vmid are create-only")

// checkAdoptedChange refuses, for a bound Target, a change of
// adopted.node or adopted.vmid, and adopted appearing or going: the
// binding names the guest, and only a new adoption changes it.
func checkAdoptedChange(target string, before, r *state.Rendered, ref string) error {
	var b, n *state.Adopted
	if before != nil {
		b = before.Adopted
	}
	if r != nil {
		n = r.Adopted
	}
	switch {
	case b == nil && n == nil:
		return nil
	case b == nil || n == nil:
		return &ProposalError{Target: target, Err: ErrAdoptedChange,
			Msg: "adopted cannot be added to or removed from a Target that has a guest; delete the Target and create or adopt it again"}
	case b.Node != n.Node || b.VMID != n.VMID || strconv.Itoa(n.VMID) != ref:
		return &ProposalError{Target: target, Err: ErrAdoptedChange,
			Msg: fmt.Sprintf("adopted.node and adopted.vmid name the bound guest (VMID %s) and cannot change; delete the Target (unbind) and adopt the other guest", ref)}
	}
	return nil
}

// deleteGuestChange is the change of spec.adopted.delete_guest (D-388):
// destructive when it becomes true.
func deleteGuestChange(before, r *state.Rendered) *wire.Change {
	if before == nil || before.Adopted == nil || r == nil || r.Adopted == nil || before.Adopted.DeleteGuest == r.Adopted.DeleteGuest {
		return nil
	}
	risk := wire.RiskReversible
	if r.Adopted.DeleteGuest {
		risk = wire.RiskDestructive
	}
	return &wire.Change{Attr: "delete_guest", From: before.Adopted.DeleteGuest, To: r.Adopted.DeleteGuest, Risk: risk}
}

// adoptedGuestDelete: the Plan lets Plumb delete a guest that a person
// made (D-388): delete_guest becomes true, or an adopted Target with it
// is deleted.
func adoptedGuestDelete(plans []TargetPlan) bool {
	for _, tp := range plans {
		if tp.Guest == "delete" && tp.Adopted != nil && tp.Adopted.DeleteGuest {
			return true
		}
		for _, rc := range tp.Resources {
			for _, c := range rc.Changes {
				if c.Attr == "delete_guest" && c.To == true {
					return true
				}
			}
		}
	}
	return false
}

// adoptPlans attaches the adopt requests to the "adopt" Targets and
// refuses an adopt without its request: a Target with spec.adopted is
// bound only through the adopt route, which checked the guest (D-386).
// It returns the notes in clear words for the Plan (D-388).
func adoptPlans(reqs []AdoptRequest, plans []TargetPlan) ([]string, error) {
	var notes []string
	for i := range plans {
		tp := &plans[i]
		switch tp.Guest {
		case "adopt":
			for j := range reqs {
				if reqs[j].Target == tp.Target {
					r := reqs[j]
					tp.Adopt = &r
				}
			}
			if tp.Adopt == nil {
				return nil, &ProposalError{Target: tp.Target, Err: ErrNotAdoptable,
					Msg: "a Target with adopted and no guest is made only by POST /targets/{name}/adopt, which checks the guest (D-386)"}
			}
		case "unbind":
			notes = append(notes, fmt.Sprintf("Target %s: the Proxmox guest %d stays; Plumb removes its agent and repair key lines and no longer manages it (D-388).", tp.Target, tp.Adopted.VMID))
		case "delete":
			if tp.Adopted != nil && tp.Adopted.DeleteGuest {
				notes = append(notes, fmt.Sprintf("Deleting this Target deletes the Proxmox guest %d and its disks (Target %s, D-388).", tp.Adopted.VMID, tp.Target))
			}
		}
		for _, rc := range tp.Resources {
			for _, c := range rc.Changes {
				if c.Attr == "delete_guest" && c.To == true {
					notes = append(notes, fmt.Sprintf("Target %s: deleting this Target will delete the Proxmox guest %d and its disks (delete_guest, D-388).", tp.Target, tp.Adopted.VMID))
				}
			}
		}
	}
	for _, r := range reqs {
		if !slices.ContainsFunc(plans, func(tp TargetPlan) bool { return tp.Target == r.Target && tp.Guest == "adopt" }) {
			return nil, &ProposalError{Target: r.Target, Err: ErrNotAdoptable, Msg: "the adopt request has no new adopted Target"}
		}
	}
	return notes, nil
}

// unbindTimeout bounds the unbind command of the agent.
const unbindTimeout = 2 * time.Minute

// unbindGuest is the Run step of the delete of an adopted Target that
// keeps its guest (D-388): the agent removes itself and the repair lines,
// the credential is revoked, the binding goes. The guest is never touched
// in Proxmox. An offline or old agent is named in the output with what a
// person runs in the guest.
func (e *Engine) unbindGuest(ctx context.Context, target string) (string, error) {
	out := "the agent removed itself and the repair key lines; the guest stays in Proxmox"
	if !e.cfg.Agents.Online(target) {
		out = textUnbindByHand
	} else if rep, err := e.command(ctx, target, wire.CommandUnbind, struct{}{}, unbindTimeout, nil); err != nil || rep.Error != "" {
		msg := rep.Error
		if err != nil {
			msg = err.Error()
		}
		out = "the agent could not remove itself (" + clipText(msg, 300) + "). " + textUnbindByHand
	}
	if err := e.cfg.Agents.Revoke(ctx, target); err != nil {
		return out, err
	}
	if err := e.st.DeleteBinding(ctx, state.KindTarget, target); err != nil {
		return out, err
	}
	e.targetDeleted(ctx, target)
	return out, e.SyncAdopted(ctx)
}

// textUnbindByHand: what a person runs as root in the guest.
const textUnbindByHand = "Remove the agent by hand as root in the guest: " +
	"systemctl disable --now plumb-agent.service; rm -rf /etc/plumb-agent /var/lib/plumb-agent /usr/local/bin/plumb-agent /etc/systemd/system/plumb-agent*; " +
	"and delete the lines ending in plumb-repair-<id> from /root/.ssh/authorized_keys. Its credential is revoked already."

func clipText(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// ErrNoGuest: the Target has no guest yet, so there is nothing to install
// an agent in.
var ErrNoGuest = errors.New("no-guest")

// ErrInstallUnavailable: the controller has no agent URL, CA or token
// source (a controller without a Proxmox driver).
var ErrInstallUnavailable = errors.New("plan: the install command needs the agent URL and the CA of this controller")

// InstallCommand makes a one-time enrollment token for target and the
// root shell command that installs the agent with it (D-387, ARCHITECTURE
// §19). The token keeps the current credential working until the guest
// enrolls with it (review R1 of #290, D-390), and it reaches the guest
// only through a here document into a 0600 file (review R6 of #290).
func (e *Engine) InstallCommand(ctx context.Context, target, actor string) (string, time.Time, error) {
	rc := e.repairConfig()
	if strings.ContainsAny(rc.AgentURL, "' \n") {
		return "", time.Time{}, ErrInstallUnavailable // never quote around a strange URL
	}
	if _, err := e.st.GetBinding(ctx, state.KindTarget, target); errors.Is(err, store.ErrNotFound) {
		return "", time.Time{}, fmt.Errorf("%w: Target %s has no guest", ErrNoGuest, target)
	} else if err != nil {
		return "", time.Time{}, err
	}
	if rc.Tokens == nil || rc.AgentURL == "" || len(rc.CA) == 0 {
		return "", time.Time{}, ErrInstallUnavailable
	}
	tok, err := rc.Tokens.NewRepairToken(ctx, target)
	if err != nil {
		return "", time.Time{}, err
	}
	e.emit(ctx, "target.install_command", "target:"+target, actor, map[string]any{"expires_at": tok.Expires.UTC()})
	return agentinstall.InstallCommand(string(rc.CA), rc.AgentURL, tok.Token), tok.Expires, nil
}
