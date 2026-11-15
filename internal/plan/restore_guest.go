package plan

// Restore of a guest backup over the same bound guest (slice 7, T-222,
// D-410, D-411, D-417 (4)). A Plan of origin restore, risk destructive,
// human-only guest-restore. It commits no revision: the desired state is
// not rolled back, the next Run converges the guest to the head.
//
// The Run:
//  1. checks the binding, the guest name, the marker, the lineage and the
//     run row of the volume again (a forged or older volume is refused);
//  2. stops the guest (allow_restart, D-194);
//  3. revokes the agent credential and drops the host key pins and the
//     repair report (D-417 (4)): the old guest's keys are never used;
//  4. restores the volume (force);
//  5. then a Plumb-made Target gets a re-enroll Plan (it starts the guest
//     with a new token), an adopted Target is started and shows the
//     install command (D-392).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/Bernotese/plumb/internal/driver"
	"github.com/Bernotese/plumb/internal/state"
	"github.com/Bernotese/plumb/internal/store"
	"github.com/Bernotese/plumb/internal/wire"
)

// OriginRestore is the origin of a restore Plan (T-222).
const OriginRestore = "restore"

// AttrRestore is the change attribute of a restore Plan on guest:config:
// "backup <id> of <time>".
const AttrRestore = "restore"

// RestoreVolume is one backup volume of a Target's guest, as the backup
// service classifies it (backup.Classify).
type RestoreVolume struct {
	ID    string // GuestBackup.id: 16 hex
	Volid string
	Kind  string // "plumb", "older-guest" or "unmarked"
	Notes string // the notes with the marker
	Time  time.Time
	Size  int64 // bytes; the driver compares it right before the restore (review L1 of #335)
}

// GuestRestorer finds and restores guest backups. serve gives the backup
// service and the Proxmox driver; nil: this controller has no guest
// backups.
type GuestRestorer interface {
	// FindBackup returns backup id of the guest ref of target, or
	// ErrBackupNotFound.
	FindBackup(ctx context.Context, target, ref, id string) (RestoreVolume, error)
	// RestoreGuest writes the volume over the stopped guest ref. It
	// refuses a volume whose size or time is not v's any more.
	RestoreGuest(ctx context.Context, ref string, v RestoreVolume) error
}

// ErrBackupNotFound: no backup with this id of the Target's guest (404).
var ErrBackupNotFound = errors.New("no such backup of this Target")

// ErrNotRestorable is wrapped when a backup is not a plumb backup of the
// Target's current lineage, or has no run row of this controller (409
// not-restorable).
var ErrNotRestorable = errors.New("not-restorable")

var reBackupID = regexp.MustCompile(`^[0-9a-f]{16}$`)

// restoreText is the change text: the volume and its time (D-410).
func restoreText(v RestoreVolume) string {
	return fmt.Sprintf("backup %s of %s", v.ID, v.Time.UTC().Format(time.RFC3339))
}

// restoreNotes are the warnings of a restore Plan: what comes back and
// what happens to the agent (review L1 of #325, D-410).
func restoreNotes(target string, adopted bool) []string {
	notes := []string{
		"The guest goes back to the backup time: every change in the guest after it is lost.",
		"Access that a person removed after the backup comes back (authorized_keys lines, passwords, users).",
		"The desired state is not rolled back: the next Run converges the guest to the head revision.",
		"The agent credential is revoked before the guest starts, and the host key pins are dropped.",
	}
	if adopted {
		return append(notes, "The guest starts again without an agent: run the install command of "+target+" (an adopted Target).")
	}
	return append(notes, "A re-enroll Plan follows: approve it to start the guest with a new agent credential.")
}

// wayBack is the end of every failure text after the revoke (review F1 of
// #335): the guest is stopped and its old credential gets 401.
func wayBack(target string, adopted bool) string {
	if adopted {
		return " The way back: start the guest in Proxmox, then run the install command of the Target (POST /targets/" + target + "/install-command)."
	}
	return " The way back: re-enroll the Target (POST /targets/" + target + "/reenroll); its Run starts the guest with a new agent credential."
}

func notRestorable(target, msg string) error {
	return &ProposalError{Target: target, Err: ErrNotRestorable, Msg: msg}
}

// restoreVolume finds the volume and checks it: kind plumb (backup
// classification), the marker of this Target and its current lineage, and
// a run row of this controller with that lineage (M1 of #331: notes alone
// prove nothing).
func (e *Engine) restoreVolume(ctx context.Context, target string, b store.Binding, id string) (RestoreVolume, error) {
	if !reBackupID.MatchString(id) {
		return RestoreVolume{}, &ProposalError{Target: target, Msg: "the backup id must be 16 hex digits"}
	}
	v, err := e.cfg.GuestRestore.FindBackup(ctx, target, b.Ref, id)
	if errors.Is(err, ErrBackupNotFound) {
		return RestoreVolume{}, &ProposalError{Target: target, Err: ErrBackupNotFound, Msg: "no backup " + id + " of this guest"}
	}
	if err != nil {
		return RestoreVolume{}, err
	}
	if v.Kind != "plumb" {
		return RestoreVolume{}, notRestorable(target, fmt.Sprintf("backup %s is %s: only a backup of this guest's own lineage restores over it (D-417)", id, v.Kind))
	}
	lineage, err := e.st.BackupLineage(ctx, target, b.Gen)
	if err != nil {
		return RestoreVolume{}, err
	}
	line, _, _ := strings.Cut(v.Notes, "\n")
	parts := strings.Split(strings.TrimSpace(line), ":")
	if len(parts) < 4 || parts[0] != "plumb" || parts[2] != target || parts[3] != lineage {
		return RestoreVolume{}, notRestorable(target, fmt.Sprintf("backup %s has no marker of this Target and its lineage", id))
	}
	run, err := e.st.GuestBackupRunOfVolume(ctx, target, v.Volid)
	if errors.Is(err, store.ErrNotFound) || (err == nil && (run.Lineage != lineage || run.Result != "done")) {
		return RestoreVolume{}, notRestorable(target, fmt.Sprintf("backup %s has no finished backup job of this controller for this guest (M1 of #331)", id))
	}
	if err != nil {
		return RestoreVolume{}, err
	}
	return v, nil
}

// restoreBinding is the binding of the Target's guest: only a bound guest
// is restored over (D-411).
func (e *Engine) restoreBinding(ctx context.Context, target string) (store.Binding, error) {
	b, err := e.st.GetBinding(ctx, state.KindTarget, target)
	if errors.Is(err, store.ErrNotFound) || (err == nil && b.Ref == "") {
		return store.Binding{}, notRestorable(target, "the Target has no guest; a restore is only over the bound guest. A person restores the volume in Proxmox into the pool and adopts it (D-411)")
	}
	return b, err
}

// checkRestoreStop refuses a running guest without allow_restart: the
// restore stops it (D-194).
func (e *Engine) checkRestoreStop(ctx context.Context, target string, r *state.Rendered, b store.Binding) (driver.GuestState, error) {
	st, err := e.cfg.Driver.Observe(ctx, driver.GuestID(b.Ref))
	if err != nil {
		return st, fmt.Errorf("the guest cannot be read: %w", err)
	}
	if st.Spec.Name != "" && st.Spec.Name != target && r.Adopted == nil {
		return st, notRestorable(target, fmt.Sprintf("the guest has the name %q, not %q", st.Spec.Name, target))
	}
	if st.Power == driver.PowerRunning && !r.AllowRestart {
		return st, &ProposalError{Target: target, Msg: "the guest runs, and a restore stops it; set allow_restart: true on the Target, or stop the guest first (D-194)"}
	}
	return st, nil
}

// ProposeRestore makes a restore Plan for one Target (D-410).
func (e *Engine) ProposeRestore(ctx context.Context, target, backupID, reason, author string) (store.Plan, error) {
	if author == "" {
		return store.Plan{}, errors.New("plan: a proposal needs an author")
	}
	if e.restoreActive() {
		return store.Plan{}, ErrRestoredMode
	}
	if e.cfg.GuestRestore == nil {
		return store.Plan{}, &ProposalError{Target: target, Msg: "this controller has no guest backups (no backup storage named at install)"}
	}
	head, err := e.st.Head(ctx)
	if errors.Is(err, store.ErrNotFound) {
		return store.Plan{}, &ProposalError{Target: target, Msg: "no such target"}
	}
	if err != nil {
		return store.Plan{}, err
	}
	r, err := e.headRender(ctx, target)
	if err != nil {
		return store.Plan{}, err
	}
	if r == nil {
		return store.Plan{}, &ProposalError{Target: target, Msg: "no such target"}
	}
	b, err := e.restoreBinding(ctx, target)
	if err != nil {
		return store.Plan{}, err
	}
	v, err := e.restoreVolume(ctx, target, b, backupID)
	if err != nil {
		return store.Plan{}, err
	}
	if _, err := e.checkRestoreStop(ctx, target, r, b); err != nil {
		return store.Plan{}, err
	}
	if err := e.checkIdle([]string{target}); err != nil {
		return store.Plan{}, err
	}
	if reason == "" {
		reason = "restore " + target + " from " + restoreText(v)
	}
	tp := TargetPlan{Target: target, Resources: []ResourceChange{{Resource: wire.GuestConfigID, Action: wire.ActionUpdate,
		Risk: wire.RiskDestructive, Changes: []wire.Change{{Attr: AttrRestore, To: restoreText(v), Risk: wire.RiskDestructive}}}}}
	changes, _ := json.Marshal([]TargetPlan{tp})
	proposal, _ := json.Marshal(Proposal{Targets: []string{target}, Notes: restoreNotes(target, r.Adopted != nil)})
	p, err := e.st.CreatePlan(ctx, store.Plan{BaseRevision: head.ID, Origin: OriginRestore, Proposal: proposal,
		Changes: changes, Risk: string(wire.RiskDestructive), Reason: reason, Author: author,
		HumanOnly: []string{HumanOnlyGuestRestore}})
	if err != nil {
		return store.Plan{}, err
	}
	e.emit(ctx, "plan.created", fmt.Sprintf("plan:%d", p.ID), author,
		map[string]any{"origin": OriginRestore, "risk": wire.RiskDestructive, "targets": []string{target}})
	return p, nil
}

// restorePlanned reads the Target and the planned change text of a
// restore Plan, and the backup id in it.
func restorePlanned(p store.Plan) (TargetPlan, string, string, error) {
	var tps []TargetPlan
	if err := json.Unmarshal(p.Changes, &tps); err != nil {
		return TargetPlan{}, "", "", err
	}
	if len(tps) != 1 || len(tps[0].Resources) != 1 || len(tps[0].Resources[0].Changes) != 1 {
		return TargetPlan{}, "", "", errors.New("plan: a restore Plan has one Target and one change")
	}
	c := tps[0].Resources[0].Changes[0]
	text, _ := c.To.(string)
	id, _, ok := strings.Cut(strings.TrimPrefix(text, "backup "), " of ")
	if c.Attr != AttrRestore || !ok || !reBackupID.MatchString(id) {
		return TargetPlan{}, "", "", fmt.Errorf("plan: bad restore change %q", text)
	}
	return tps[0], text, id, nil
}

// checkRestore checks a restore Plan again (approval and Run): the Target,
// its binding and the same volume (id and time), still restorable.
func (e *Engine) checkRestore(ctx context.Context, r *state.Rendered, target, text, id string) (store.Binding, RestoreVolume, error) {
	if r == nil {
		return store.Binding{}, RestoreVolume{}, &ProposalError{Target: target, Msg: "no such target"}
	}
	if e.cfg.GuestRestore == nil {
		return store.Binding{}, RestoreVolume{}, &ProposalError{Target: target, Msg: "this controller has no guest backups"}
	}
	b, err := e.restoreBinding(ctx, target)
	if err != nil {
		return b, RestoreVolume{}, err
	}
	v, err := e.restoreVolume(ctx, target, b, id)
	if err != nil {
		return b, v, err
	}
	if restoreText(v) != text {
		return b, v, notRestorable(target, fmt.Sprintf("backup %s changed since the Plan (%s, planned %s)", id, restoreText(v), text))
	}
	return b, v, nil
}

// approveRestore approves a restore Plan: everything is checked again; no
// revision, the Run is pinned to the head.
func (e *Engine) approveRestore(ctx context.Context, p store.Plan, principal string) (store.Run, error) {
	tp, text, id, err := restorePlanned(p)
	if err != nil {
		return store.Run{}, err
	}
	r, err := e.headRender(ctx, tp.Target)
	if err != nil {
		return store.Run{}, err
	}
	b, _, err := e.checkRestore(ctx, r, tp.Target, text, id)
	if err != nil {
		return store.Run{}, err
	}
	if _, err := e.checkRestoreStop(ctx, tp.Target, r, b); err != nil {
		return store.Run{}, err
	}
	names := []string{tp.Target}
	if err := e.reserve(names); err != nil {
		return store.Run{}, err
	}
	for attempt := 0; ; attempt++ {
		head, err := e.st.Head(ctx)
		if err != nil {
			e.release(names)
			return store.Run{}, err
		}
		set, err := e.load(ctx, head.ID)
		if err != nil {
			e.release(names)
			return store.Run{}, err
		}
		run, err := e.st.ApproveBuildPlan(ctx, p.ID, principal, head.ID)
		if errors.Is(err, store.ErrHeadMoved) && attempt < 3 {
			continue
		}
		if err != nil {
			e.release(names)
			return store.Run{}, err
		}
		e.emit(ctx, "plan.approved", fmt.Sprintf("plan:%d", p.ID), principal, map[string]any{"run": run.ID, "pinned_revision": head.ID})
		e.start(p, run, set, []TargetPlan{tp}, nil, nil)
		return run, nil
	}
}

// runRestore is the Run of a restore Plan (D-410). One step on
// guest:config; its output names how far it came.
func (e *Engine) runRestore(ctx context.Context, p store.Plan, run store.Run, set *state.Set, tp TargetPlan) bool {
	s := &stepper{e: e, run: run, target: tp.Target, seq: map[string]int64{}, final: map[string]bool{}, ok: true}
	s.add(ctx, wire.GuestConfigID)
	fail := func(msg string) bool {
		s.set(ctx, wire.GuestConfigID, wire.StatusFailed, nil, msg)
		return false
	}
	s.set(ctx, wire.GuestConfigID, wire.StatusRunning, nil, "")
	_, text, id, err := restorePlanned(p)
	if err != nil {
		return fail(err.Error())
	}
	r, err := renderOrNil(set, tp.Target)
	if err != nil {
		return fail(err.Error())
	}
	// 1. The checks again, against the guest now.
	b, v, err := e.checkRestore(ctx, r, tp.Target, text, id)
	if err != nil {
		return fail("nothing was changed: " + err.Error())
	}
	st, err := e.checkRestoreStop(ctx, tp.Target, r, b)
	if err != nil {
		return fail("nothing was changed: " + err.Error())
	}
	// 2. Stop.
	if st.Power != driver.PowerStopped {
		if err := e.stopGuest(ctx, tp.Target); err != nil {
			return fail("nothing was restored: " + err.Error())
		}
	}
	// 3. Revoke before the guest can start again (D-410); drop the pins
	// and the repair report of the old guest (D-417 (4)).
	way := wayBack(tp.Target, r.Adopted != nil)
	if err := e.cfg.Agents.Revoke(ctx, tp.Target); err != nil {
		return fail("the guest is stopped, nothing was restored: the agent credential could not be revoked: " + err.Error() + ". Start the guest in Proxmox, or plan the restore again")
	}
	if _, err := e.st.DropHostKeys(ctx, tp.Target); err != nil {
		return fail("the guest is stopped and its agent credential is revoked, nothing was restored: the host key pins could not be dropped: " + err.Error() + "." + way)
	}
	// 4. Restore.
	if err := e.cfg.GuestRestore.RestoreGuest(ctx, b.Ref, v); err != nil {
		return fail("the guest is stopped and its agent credential is revoked; the restore failed: " + err.Error() + ". Plan the restore again." + way)
	}
	// The disk is the restored one now: the follow-up (re-enroll Plan)
	// comes also when a later step fails (review F1 of #335).
	e.restored.Store(p.ID, true)
	// The restored config is Plumb's own step, no drift; the next Run
	// converges it to the head.
	after, err := e.cfg.Driver.Observe(ctx, driver.GuestID(b.Ref))
	if err != nil {
		return fail("restored, but the guest cannot be read: " + err.Error() + "." + way)
	}
	if nb, err := e.st.GetBinding(ctx, state.KindTarget, tp.Target); err == nil && nb.Ref == b.Ref {
		nb.Digest = pollKey(after)
		if err := e.st.PutBinding(ctx, nb); err != nil {
			return fail("restored, but the binding could not be written: " + err.Error() + "." + way)
		}
	}
	// 5. The way back for the agent.
	out := "restored " + text + "; the agent credential is revoked and the host key pins are dropped. "
	if r.Adopted != nil {
		if after.Power != driver.PowerRunning {
			if err := e.cfg.Driver.Power(ctx, driver.GuestID(b.Ref), driver.PowerStart); err != nil {
				return fail(out + "The start failed: " + err.Error() + "." + way)
			}
		}
		out += "The guest runs without an agent: run the install command of the Target (POST /targets/" + tp.Target + "/install-command)."
	} else {
		out += "The guest stays stopped until the re-enroll Plan that follows this Run is approved."
	}
	s.set(ctx, wire.GuestConfigID, wire.StatusApplied, nil, out)
	return true
}

// afterRestore makes the re-enroll Plan of a Plumb-made Target after its
// restore Run (the Target is free again), also when a step after the
// restore call failed: the disk is the restored one. Its error is logged: a person
// can always re-enroll by hand.
func (e *Engine) afterRestore(ctx context.Context, p store.Plan, set *state.Set, ok bool) {
	if p.Origin != OriginRestore {
		return
	}
	if _, restored := e.restored.LoadAndDelete(p.ID); !ok && !restored {
		return // nothing was restored: the old disk, the Run output says the way back
	}
	tp, _, _, err := restorePlanned(p)
	if err != nil {
		return
	}
	if r, err := renderOrNil(set, tp.Target); err != nil || r == nil || r.Adopted != nil {
		return
	}
	re, err := e.Propose(ctx, Input{Proposal: Proposal{Reenroll: []string{tp.Target}}, Origin: OriginReenroll, Author: "system",
		Reason: fmt.Sprintf("re-enroll %s after the restore of plan %d", tp.Target, p.ID)})
	if err != nil {
		// The Run output says that a re-enroll Plan follows; a person
		// re-enrolls by hand when it is missing.
		e.log.Error("re-enroll after the restore", "target", tp.Target, "plan", p.ID, "err", err)
		return
	}
	e.log.Info("re-enroll Plan after the restore", "target", tp.Target, "plan", re.ID)
}
