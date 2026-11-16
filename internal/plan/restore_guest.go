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
	// SafetyBackup backs up the guest of target as it is now (trigger
	// pre-restore, D-420) and waits; it returns the marked volume or an
	// error with Plumb's own text.
	SafetyBackup(ctx context.Context, target string) (RestoreVolume, error)
	// DropSafetyBackups deletes the Target's older pre-restore volumes
	// except keep (a volid), after a restore that worked.
	DropSafetyBackups(ctx context.Context, target, keep string) error
}

// ErrBackupNotFound: no backup with this id of the Target's guest (404).
var ErrBackupNotFound = errors.New("no such backup of this Target")

// ErrNotRestorable is wrapped when a backup is not a plumb backup of the
// Target's current lineage, or has no run row of this controller (409
// not-restorable).
var ErrNotRestorable = errors.New("not-restorable")

// ErrRestoreNotStarted: Proxmox did not begin the restore (a check of the
// driver, or a refused call): the guest's disks are as before (M1 of #346).
// Its text after the colon is Plumb's reason.
var ErrRestoreNotStarted = errors.New("the restore did not start")

// ErrGuestGone: the Target's guest is not in the pool (the restorer cannot
// list it): no restore after the guest is gone (D-411).
var ErrGuestGone = errors.New("the guest of the Target is gone")

// GuestRestoreFailedID is the Issue of a Target whose restore failed after
// Proxmox began (D-420): the guest may have no disk; the Issue names the
// way back. A restore that works resolves it.
const GuestRestoreFailedID = "guest:restore-failed"

// AttrSafetyBackup is the second change of a restore Plan: whether the Run
// takes the safety backup first (D-420).
const AttrSafetyBackup = "safety_backup"

// Values of AttrSafetyBackup.
const (
	safetyFirst   = "first, of the guest as it is now"
	safetySkipped = "skipped: the guest is broken already"
	// safetyKept: the last restore failed (guest:restore-failed open), so
	// the guest may be empty: no backup of it, and the safety backup of the
	// guest before that failure stays the way back (D1 of #346).
	safetyKept = "kept: the last restore failed; the safety backup from before it stays"
)

var reBackupID = regexp.MustCompile(`^[0-9a-f]{16}$`)

// restoreText is the change text: the volume and its time (D-410).
func restoreText(v RestoreVolume) string {
	return fmt.Sprintf("backup %s of %s", v.ID, v.Time.UTC().Format(time.RFC3339))
}

// restoreNotes are the warnings of a restore Plan: what comes back and
// what happens to the agent (review L1 of #325, D-410).
func restoreNotes(target string, adopted, skip, failedBefore bool, size int64) []string {
	notes := []string{
		"Proxmox removes the guest's disks before it writes the backup: if the restore fails, the guest is left empty.",
		"The guest goes back to the backup time: every change in the guest after it is lost.",
		"Access that a person removed after the backup comes back (authorized_keys lines, passwords, users).",
		"The desired state is not rolled back: the next Run converges the guest to the head revision.",
		"The agent credential is revoked before the guest starts, and the host key pins are dropped.",
	}
	switch {
	case failedBefore:
		notes = append(notes, "No new safety backup: the last restore failed, so the guest may be empty. The safety backup from before that failure stays the way back.")
	case skip:
		notes = append(notes, "No safety backup first: a person said the guest is broken already. If the restore fails, only another backup brings it back.")
	default:
		notes = append(notes, fmt.Sprintf("Plumb first takes a safety backup of the guest as it is now (about %s on the backup storage, the free-space gate applies); if that backup fails, nothing is changed.", sizeText(size)))
	}
	if adopted {
		return append(notes, "The guest starts again without an agent: run the install command of "+target+" (an adopted Target).")
	}
	return append(notes, "A re-enroll Plan follows: approve it to start the guest with a new agent credential.")
}

// sizeText is a size in plain units.
func sizeText(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GiB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%d MiB", n>>20)
	case n > 0:
		return fmt.Sprintf("%d KiB", (n+1023)>>10)
	}
	return "the size of a backup of the guest"
}

var reUPID = regexp.MustCompile(`UPID:[A-Za-z0-9._-]+:[0-9A-F]+:[0-9A-F]+:[0-9A-F]+:[A-Za-z0-9_-]+:[^: ]*:[^: ]+:`)

// pveText is Plumb's text of a driver error in a restore step: never the
// raw Proxmox task log (it holds host paths), only the task id when there
// is one (D-420 (5)).
func pveText(err error) string {
	if errors.Is(err, driver.ErrNotFound) {
		return "the guest is not in the pool any more"
	}
	if id := reUPID.FindString(err.Error()); id != "" {
		return "the Proxmox task " + id + " failed (see its log in Proxmox)"
	}
	return "Proxmox did not do it (see the task list in Proxmox)"
}

// failedWayBack is the text of a restore that failed after Proxmox began.
func failedWayBack(target string, safety *RestoreVolume) string {
	t := " Proxmox removes the guest's disks before it writes the backup, so the guest may have no disk now. The way back: "
	if safety != nil {
		t += "restore the safety " + restoreText(*safety) + " (POST /targets/" + target + "/restore with backup " + safety.ID + "), or another backup"
	} else {
		t += "restore another backup (POST /targets/" + target + "/restore)"
	}
	return t + "; re-enroll only after a restore that worked."
}

// failedWayBackText is the way back of a further failure: the text of the
// open Issue ("restore the safety backup <id> of <time>").
func failedWayBackText(target, issueTo string) string {
	t := " Proxmox removes the guest's disks before it writes the backup, so the guest may have no disk now. The way back: "
	if issueTo != "" {
		t += issueTo + " (POST /targets/" + target + "/restore), or another backup"
	} else {
		t += "restore another backup (POST /targets/" + target + "/restore)"
	}
	return t + "; re-enroll only after a restore that worked."
}

// wayBack is the end of every failure text after the revoke (review F1 of
// #335): the guest is stopped and its old credential gets 401.
func wayBack(target string, adopted bool) string {
	if adopted {
		return " The way back: start the guest in Proxmox, then run the install command of the Target (POST /targets/" + target + "/install-command)."
	}
	return " The way back: re-enroll the Target (POST /targets/" + target + "/reenroll); its Run starts the guest with a new agent credential."
}

// goneGuest is the refusal of a restore whose guest left the pool (D-411,
// S3 of T-225).
func goneGuest(target string) error {
	return notRestorable(target, "the guest of the Target is gone; there is no restore after the guest is gone. A person restores the volume in Proxmox into the pool and adopts it (D-411)")
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
	if errors.Is(err, ErrGuestGone) || errors.Is(err, driver.ErrNotFound) {
		return RestoreVolume{}, goneGuest(target)
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
	// The engine's own second check, as strict as backup.Classify (T-229,
	// Q1 of #340): it does not trust the restorer's kind. The marker names
	// this install, this Target and its current lineage; a recorded,
	// finished and not deleted job row of this controller has the volume
	// with the same lineage, trigger, size and time (M1, F1, F2 of #331).
	line, _, _ := strings.Cut(v.Notes, "\n")
	parts := strings.Split(strings.TrimSpace(line), ":")
	trigger := "scheduled"
	if len(parts) == 5 {
		switch parts[4] {
		case "m":
			trigger = "manual"
		case "r":
			trigger = "pre-restore" // a safety backup (D-420)
		default:
			trigger = ""
		}
	}
	if len(parts) < 4 || len(parts) > 5 || trigger == "" || parts[0] != "plumb" || parts[1] != e.cfg.InstallID || e.cfg.InstallID == "" ||
		parts[2] != target || parts[3] != lineage {
		return RestoreVolume{}, notRestorable(target, fmt.Sprintf("backup %s has no marker of this install, this Target and its lineage", id))
	}
	recs, err := e.st.RecordedGuestBackups(ctx, target)
	if err != nil {
		return RestoreVolume{}, err
	}
	rec, ok := recs[v.Volid]
	if !ok || rec.Lineage != lineage || rec.Trigger != trigger || rec.Size != v.Size || rec.CTime != v.Time.Unix() {
		return RestoreVolume{}, notRestorable(target, fmt.Sprintf("backup %s is not the volume that a finished backup job of this controller recorded for this guest (M1 of #331)", id))
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
	if errors.Is(err, driver.ErrNotFound) {
		return st, goneGuest(target)
	}
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
func (e *Engine) ProposeRestore(ctx context.Context, target, backupID, reason, author string, skipSafety bool) (store.Plan, error) {
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
	st, err := e.checkRestoreStop(ctx, target, r, b)
	if err != nil {
		return store.Plan{}, err
	}
	if err := e.checkBackupNetworks(ctx, target, b.Ref, v); err != nil { // T-244 R1, T-241
		return store.Plan{}, err
	}
	failedBefore, _, err := e.restoreFailedOpen(ctx, target)
	if err != nil {
		return store.Plan{}, err
	}
	if skipSafety && !failedBefore {
		if err := e.checkBroken(ctx, target, st); err != nil {
			return store.Plan{}, err
		}
	}
	if err := e.checkIdle([]string{target}); err != nil {
		return store.Plan{}, err
	}
	if reason == "" {
		reason = "restore " + target + " from " + restoreText(v)
	}
	named := map[int]bool{}
	if dk, _, err := docDisks(target, r); err == nil {
		for _, d := range dk {
			named[d.Slot] = true
		}
	}
	diskChanges, diskNotes := e.restoreDiskChanges(ctx, b, v, st, named) // T-238
	tp := TargetPlan{Target: target, Resources: []ResourceChange{{Resource: wire.GuestConfigID, Action: wire.ActionUpdate,
		Risk: wire.RiskDestructive, Changes: append([]wire.Change{{Attr: AttrRestore, To: restoreText(v), Risk: wire.RiskDestructive},
			{Attr: AttrSafetyBackup, To: safetyValue(skipSafety, failedBefore), Risk: wire.RiskDestructive}}, diskChanges...)}}}
	changes, _ := json.Marshal([]TargetPlan{tp})
	proposal, _ := json.Marshal(Proposal{Targets: []string{target}, Notes: append(restoreNotes(target, r.Adopted != nil, skipSafety || failedBefore, failedBefore, v.Size), diskNotes...)})
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

func safetyValue(skip, failedBefore bool) string {
	switch {
	case failedBefore:
		return safetyKept
	case skip:
		return safetySkipped
	}
	return safetyFirst
}

// restoreFailedOpen reports an open guest:restore-failed of target and the
// way back that it names (the safety backup from before the first failure).
func (e *Engine) restoreFailedOpen(ctx context.Context, target string) (bool, string, error) {
	iss, err := e.st.OpenIssue(ctx, target, GuestRestoreFailedID)
	if errors.Is(err, store.ErrNotFound) {
		return false, "", nil
	}
	if err != nil {
		return false, "", err
	}
	var ch []wire.Change
	json.Unmarshal(iss.Changes, &ch)
	for _, c := range ch {
		if c.Attr != "restore" {
			continue
		}
		// The "restored" states of D-439 are no broken guest: the disks are
		// the restored ones (high QA finding of #374). A restore from there
		// is a normal one: a safety backup first, "running" before the call.
		if from, _ := c.From.(string); strings.HasPrefix(from, "restored") {
			return false, "", nil
		}
		if to, ok := c.To.(string); ok {
			return true, to, nil
		}
	}
	return true, "", nil
}

// restoredState returns the changes of an open guest:restore-failed Issue
// in a "restored" state (D-439), or nil.
func (e *Engine) restoredState(ctx context.Context, target string) []wire.Change {
	iss, err := e.st.OpenIssue(ctx, target, GuestRestoreFailedID)
	if err != nil {
		return nil
	}
	var ch []wire.Change
	if json.Unmarshal(iss.Changes, &ch) != nil {
		return nil
	}
	for _, c := range ch {
		if from, _ := c.From.(string); c.Attr == "restore" && strings.HasPrefix(from, "restored") {
			return ch
		}
	}
	return nil
}

// checkBroken allows "no safety backup" only for a guest that is broken
// already (D-420 (4)): it has no disk, or its last restore failed (an open
// Issue guest:restore-failed).
func (e *Engine) checkBroken(ctx context.Context, target string, st driver.GuestState) error {
	if st.Spec.DiskGB == 0 {
		return nil
	}
	// M-B of #382: the same rule as restoreFailedOpen. Only a "failed" or
	// "running" restore Issue is a broken guest; in a "restored" state the
	// disks are the restored ones, so the safety backup is taken.
	if open, _, err := e.restoreFailedOpen(ctx, target); err != nil {
		return err
	} else if open {
		return nil
	}
	return &ProposalError{Target: target, Msg: "the safety backup may be skipped only for a broken guest (no disk, or its last restore failed); this guest is not (D-420)"}
}

// restorePlanned reads the Target and the planned change text of a
// restore Plan, and the backup id in it.
func restorePlanned(p store.Plan) (TargetPlan, string, string, error) {
	var tps []TargetPlan
	if err := json.Unmarshal(p.Changes, &tps); err != nil {
		return TargetPlan{}, "", "", err
	}
	if len(tps) != 1 || len(tps[0].Resources) != 1 || len(tps[0].Resources[0].Changes) < 1 {
		return TargetPlan{}, "", "", errors.New("plan: a restore Plan has one Target and the restore change")
	}
	// After the restore change: the safety backup, and the data disks that
	// the backup lacks (T-238). Nothing else.
	for i, c := range tps[0].Resources[0].Changes[1:] {
		if (i > 0 || c.Attr != AttrSafetyBackup) && driver.DiskIndex(c.Attr) == 0 {
			return TargetPlan{}, "", "", fmt.Errorf("plan: a restore Plan has no change %q", c.Attr)
		}
	}
	c := tps[0].Resources[0].Changes[0]
	text, _ := c.To.(string)
	id, _, ok := strings.Cut(strings.TrimPrefix(text, "backup "), " of ")
	if c.Attr != AttrRestore || !ok || !reBackupID.MatchString(id) {
		return TargetPlan{}, "", "", fmt.Errorf("plan: bad restore change %q", text)
	}
	return tps[0], text, id, nil
}

// skipSafety reports whether a restore Plan skips the safety backup. A Plan
// without the change (made before D-420) takes it: the safe side.
func skipSafety(tp TargetPlan) bool {
	for _, c := range tp.Resources[0].Changes {
		if c.Attr == AttrSafetyBackup {
			return c.To == safetySkipped || c.To == safetyKept
		}
	}
	return false
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
	ptp, text, id, err := restorePlanned(p)
	if err != nil {
		return fail(err.Error())
	}
	skip := skipSafety(ptp)
	r, err := e.renderOrNil(set, tp.Target)
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
	// The network pre-check before the stop (T-244 R1, T-241): also for a
	// restore of the safety backup, which takes the same path.
	if err := e.checkBackupNetworks(ctx, tp.Target, b.Ref, v); err != nil {
		var pe *ProposalError
		if errors.As(err, &pe) {
			return fail(pe.Msg)
		}
		return fail("nothing was changed: " + err.Error())
	}
	failedBefore, prevWay, err := e.restoreFailedOpen(ctx, tp.Target)
	if err != nil {
		return fail("nothing was changed: " + err.Error())
	}
	// A restore from the D-439 "restored" state: the "running" Issue below
	// replaces it at the start, so the old proof ends there; a restore that
	// does not start puts it back (the guest is as before).
	prevRestored := e.restoredState(ctx, tp.Target)
	if skip && !failedBefore {
		if err := e.checkBroken(ctx, tp.Target, st); err != nil {
			return fail("nothing was changed: " + err.Error())
		}
	}
	// D1 of #346: after a failed restore the guest may be empty: no backup
	// of it, and the safety backup from before stays the way back.
	skip = skip || failedBefore
	// 2. Stop, before the safety backup: the backup then holds the guest
	// exactly as the restore overwrites it (no lost window; design point e
	// of #345).
	wasRunning := st.Power != driver.PowerStopped
	if wasRunning {
		if err := e.stopGuest(ctx, tp.Target); err != nil {
			return fail("nothing was restored; the guest may be stopped: " + pveText(err) + ". Plan the restore again.")
		}
	}
	// 2b. The safety backup of the guest as it is (D-420): Proxmox removes
	// the guest's disks before it writes the backup, so a failed restore
	// needs a way back. The 10 % gate does not prove that it fits: if it
	// fails, the guest is started again and nothing else is changed.
	var safety *RestoreVolume
	if !skip {
		sv, err := e.cfg.GuestRestore.SafetyBackup(ctx, tp.Target)
		if err != nil {
			msg := "nothing was changed: the safety backup before the restore failed: " + safetyText(err)
			if wasRunning {
				if serr := e.cfg.Driver.Power(ctx, driver.GuestID(b.Ref), driver.PowerStart); serr != nil {
					return fail(msg + ". The guest was stopped for it, and its start failed: " + pveText(serr) + "; start it in Proxmox")
				}
				msg += ". The guest was stopped for it and runs again"
			}
			return fail(msg)
		}
		safety = &sv
	}
	// 2c. The Issue opens before anything else changes (M2 of #352): a
	// controller that stops while Proxmox restores leaves the gate of
	// T-234 closed, so the next poll never offers a revert of an empty
	// guest. Fail closed: when it cannot be written, the Run stops here
	// (re-check of #352). A failure before keeps its Issue (D1 of #346).
	if !failedBefore {
		if err := e.restoreRunningIssue(ctx, tp.Target, safety); err != nil {
			msg := "nothing was changed: the Issue of the restore could not be written: " + err.Error()
			if wasRunning {
				if serr := e.cfg.Driver.Power(ctx, driver.GuestID(b.Ref), driver.PowerStart); serr != nil {
					return fail(msg + ". The guest was stopped for the restore, and its start failed: " + pveText(serr) + "; start it in Proxmox")
				}
				msg += ". The guest was stopped for the restore and runs again"
			}
			return fail(msg + ". Plan the restore again")
		}
	}
	// endRunning ends the Issue of this Run when it stops before the
	// restore call: the disks are as before.
	endRunning := func() {
		if failedBefore {
			return
		}
		if prevRestored != nil {
			if err := e.restoreIssue(ctx, tp.Target, GuestRestoreFailedID, wire.StatusDiffers, prevRestored); err != nil {
				e.log.Warn("put back the restored state of the Issue", "target", tp.Target, "err", err)
			}
			return
		}
		if err := e.endRestoreIssue(ctx, tp.Target, GuestRestoreFailedID, "the restore did not start; the guest's disks are as before"); err != nil { // D-428
			e.log.Warn("end the Issue of a restore that did not start", "target", tp.Target, "err", err)
		}
	}
	// 3. Revoke before the guest can start again (D-410); drop the pins
	// and the repair report of the old guest (D-417 (4)).
	way := wayBack(tp.Target, r.Adopted != nil)
	if err := e.cfg.Agents.Revoke(ctx, tp.Target); err != nil {
		endRunning()
		return fail("the guest is stopped, nothing was restored: the agent credential could not be revoked: " + err.Error() + ". Start the guest in Proxmox, or plan the restore again")
	}
	if _, err := e.st.DropHostKeys(ctx, tp.Target); err != nil {
		endRunning()
		return fail("the guest is stopped and its agent credential is revoked, nothing was restored: the host key pins could not be dropped: " + err.Error() + "." + way)
	}
	// 4. Restore.
	if err := e.cfg.GuestRestore.RestoreGuest(ctx, b.Ref, v); err != nil {
		if errors.Is(err, ErrRestoreNotStarted) {
			// The disks are as before: the Issue of this Run ends.
			endRunning()
			// The disks are as before: no Issue, no "may have no disk"; the
			// guest runs again if it ran (M1 of #346).
			msg := "the restore did not start: " + strings.TrimPrefix(err.Error(), ErrRestoreNotStarted.Error()+": ") +
				". The guest's disks are as before; nothing was restored."
			if wasRunning {
				if serr := e.cfg.Driver.Power(ctx, driver.GuestID(b.Ref), driver.PowerStart); serr != nil {
					msg += " Its start failed: " + pveText(serr) + "; start it in Proxmox."
				} else {
					msg += " The guest runs again."
				}
			}
			return fail(msg + " The agent credential is revoked." + way)
		}
		if failedBefore {
			// The Issue keeps naming the good backup from before the first
			// failure.
			return fail("the agent credential is revoked; the restore failed again: " + pveText(err) + "." + failedWayBackText(tp.Target, prevWay))
		}
		msg := "the agent credential is revoked; the restore failed: " + pveText(err) + "." + failedWayBack(tp.Target, safety)
		e.restoreFailedIssue(ctx, tp.Target, safety)
		return fail(msg)
	}
	// The disk is the restored one now: the follow-up (re-enroll Plan)
	// comes also when a later step fails (review F1 of #335). D-427: the
	// fact is stored first (the step output), so a start after a stop
	// finds it; the guest:restore-failed Issue says "restored" and ends
	// only after the follow-up (afterRestore, recoverRestores).
	s.set(ctx, wire.GuestConfigID, wire.StatusRunning, nil, restoredMark+text)
	e.restored.Store(p.ID, true)
	if err := e.restoredIssue(ctx, tp.Target, run.ID, "restored: the follow-up (a re-enroll Plan, or the install command of an adopted Target) comes next"); err != nil {
		e.log.Warn("mark the Issue of the restore as restored", "target", tp.Target, "err", err)
	}
	// Exactly one safety backup per Target: this Run's replaces the older
	// ones. It stays after the restore, so a wrong restore can be undone.
	// D-429: a Run without one (the last restore had failed) that restored
	// a pre-restore volume keeps it and every newer one; older ones go.
	keep := ""
	switch {
	case safety != nil:
		keep = safety.Volid
	case isPreRestore(v):
		keep = v.Volid
	}
	if keep != "" {
		if err := e.cfg.GuestRestore.DropSafetyBackups(ctx, tp.Target, keep); err != nil {
			e.log.Warn("delete the older safety backups", "target", tp.Target, "err", err)
		}
	}
	// T-234: an open guest:config Issue showed the guest before the
	// restore (or the empty guest of a failed one): it ends, and the next
	// poll compares the restored guest again (an empty digest), so real
	// drift of the restored config opens a new Issue.
	e.endStaleConfigIssue(ctx, tp.Target)
	after, err := e.cfg.Driver.Observe(ctx, driver.GuestID(b.Ref))
	if err != nil {
		return fail("restored, but the guest cannot be read: " + pveText(err) + "." + way)
	}
	if nb, err := e.st.GetBinding(ctx, state.KindTarget, tp.Target); err == nil && nb.Ref == b.Ref {
		nb.Digest = ""
		if err := e.st.PutBinding(ctx, nb); err != nil {
			return fail("restored, but the binding could not be written: " + err.Error() + "." + way)
		}
	}
	// 5. The way back for the agent.
	out := "restored " + text + "; the agent credential is revoked and the host key pins are dropped. "
	if safety != nil {
		out += "The safety " + restoreText(*safety) + " keeps the guest as it was before. "
	}
	if r.Adopted != nil {
		if after.Power != driver.PowerRunning {
			if err := e.cfg.Driver.Power(ctx, driver.GuestID(b.Ref), driver.PowerStart); err != nil {
				return fail(out + "The start failed: " + pveText(err) + "." + way)
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
	if p.Origin == OriginReenroll && ok {
		e.endRestoredByReenroll(ctx, p)
		return
	}
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
	r, err := e.renderOrNil(set, tp.Target)
	if err != nil || r == nil {
		return
	}
	e.restoreFollowUp(ctx, p, tp.Target, r.Adopted != nil)
}

// safetyText is the reason of a failed safety backup: Plumb's own text of
// the job (refused, the storage, the free-space gate), never a task log.
func safetyText(err error) string {
	msg := err.Error()
	for _, p := range []string{"backup: guest backup refused: ", "backup: the safety backup before the restore failed: "} {
		if i := strings.Index(msg, p); i >= 0 {
			return msg[i+len(p):]
		}
	}
	return pveText(err)
}

// restoreFailedIssue opens the Issue guest:restore-failed (D-420): the
// guest may have no disk; the change names the safety backup to restore.
func (e *Engine) restoreFailedIssue(ctx context.Context, target string, safety *RestoreVolume) {
	if err := e.writeRestoreIssue(ctx, target, safety, "failed: the guest may have no disk"); err != nil {
		// The "running" text of this Run stays open: the gate holds.
		e.log.Error("update the Issue of a failed restore", "target", target, "err", err)
	}
}

// restoreRunningIssue opens guest:restore-failed before the restore call
// (M2 of #352): if the Run does not end (a controller stop), the guest may
// have no disk, and the Issue names the way back already.
func (e *Engine) restoreRunningIssue(ctx context.Context, target string, safety *RestoreVolume) error {
	return e.writeRestoreIssue(ctx, target, safety, "running: if this restore does not end, the guest may have no disk")
}

func (e *Engine) writeRestoreIssue(ctx context.Context, target string, safety *RestoreVolume, from string) error {
	to := "restore another backup"
	if safety != nil {
		to = "restore the safety " + restoreText(*safety)
	}
	ch := []wire.Change{{Attr: "restore", From: from, To: to, Risk: wire.RiskDestructive}}
	if safety != nil { // the way back as data, for the web (L1 of #350)
		ch = append(ch, wayBackChange(*safety))
	}
	return e.restoreIssue(ctx, target, GuestRestoreFailedID, wire.StatusDiffers, ch)
}

// restoredMark starts the guest:config step output of a restore Run as soon
// as the restore call worked (D-427): a start after a stop reads it.
const restoredMark = "restored "

// isPreRestore reports a volume whose marker has the trigger pre-restore
// (a safety backup, D-420).
func isPreRestore(v RestoreVolume) bool {
	line, _, _ := strings.Cut(v.Notes, "\n")
	parts := strings.Split(strings.TrimSpace(line), ":")
	return len(parts) == 5 && parts[4] == "r"
}

// restoreFollowUp is the follow-up of a restore that worked (D-427): one
// re-enroll Plan for a Plumb-made Target (host D12: an open one is used,
// none is made twice), the install command for an adopted one. Then the
// guest:restore-failed Issue ends with the reason (D-428). A refused
// re-enroll leaves the Issue open in the "restored" state with Plumb's
// reason and the way out (host D10); its gate lets only a re-enroll pass.
func (e *Engine) restoreFollowUp(ctx context.Context, p store.Plan, target string, adopted bool) {
	reason := fmt.Sprintf("a restore worked (plan %d)", p.ID)
	var runID int64
	if run, err := e.st.RunOfPlan(ctx, p.ID); err == nil {
		runID = run.ID
		reason = fmt.Sprintf("a restore worked in run %d (plan %d)", run.ID, p.ID)
	}
	switch {
	case adopted:
		reason += "; the guest runs without an agent: run the install command of the Target"
	default:
		open, err := e.openReenroll(ctx, target)
		if err != nil {
			e.log.Error("re-enroll after the restore", "target", target, "plan", p.ID, "err", err)
			return
		}
		if open != 0 {
			// T-252: no second Plan. D-440: the Issue waits for its Run.
			e.waitForReenroll(ctx, target, runID, open)
			return
		}
		re, err := e.Propose(ctx, Input{Proposal: Proposal{Reenroll: []string{target}}, Origin: OriginReenroll, Author: "system",
			Reason: fmt.Sprintf("re-enroll %s after the restore of plan %d", target, p.ID)})
		if err != nil {
			e.log.Error("re-enroll after the restore", "target", target, "plan", p.ID, "err", err)
			e.reenrollRefused(ctx, target, runID, err)
			return
		}
		e.log.Info("re-enroll Plan after the restore", "target", target, "plan", re.ID)
		// D-440: the Issue stays open in the "restored" state until the
		// re-enroll Run worked (endRestoredByReenroll), never at the Plan:
		// a Plan that goes stale (another Plan moved the head) or whose Run
		// fails would else leave a stopped, revoked guest with no word.
		e.waitForReenroll(ctx, target, runID, re.ID)
		return
	}
	if err := e.endRestoreIssue(ctx, target, GuestRestoreFailedID, reason); err != nil {
		e.log.Warn("end the Issue of the restore", "target", target, "err", err)
	}
}

// openReenroll returns the id of a pending or approved re-enroll Plan of
// target, or 0 (host D12: one re-enroll Plan, never a second one that
// makes the first stale). A restore commits no revision, so an open Plan
// stays valid.
func (e *Engine) openReenroll(ctx context.Context, target string) (int64, error) {
	pending, err := e.st.PendingPlans(ctx, OriginReenroll)
	if err != nil {
		return 0, err
	}
	approved, err := e.st.ApprovedPlans(ctx, OriginReenroll)
	if err != nil {
		return 0, err
	}
	head, err := e.st.Head(ctx)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return 0, err
	}
	for _, pl := range append(pending, approved...) {
		var tps []TargetPlan
		if json.Unmarshal(pl.Changes, &tps) != nil || len(tps) != 1 || tps[0].Target != target {
			continue
		}
		// T-252: only a Plan that can still do the work counts.
		ok, _, err := e.reenrollUsable(ctx, pl, head.ID)
		if err != nil {
			return 0, err
		}
		if ok {
			return pl.ID, nil
		}
	}
	return 0, nil
}

// reenrollRefused keeps the guest:restore-failed Issue open in the
// "restored" state with Plumb's reason why the re-enroll Plan was refused
// and the way out (host D10: never only a log line).
func (e *Engine) reenrollRefused(ctx context.Context, target string, runID int64, err error) {
	why := "the re-enroll Plan was refused"
	var pe *ProposalError
	if errors.As(err, &pe) {
		// Plumb's own text; it can list config lines, so it is cut. A
		// refusal is never guest text (D-250 names the Proxmox config).
		why += ": " + cutRunes(pe.Msg, 300)
	}
	if ierr := e.restoreIssue(ctx, target, GuestRestoreFailedID, wire.StatusDiffers, []wire.Change{
		{Attr: "restore", From: RestoredRefusedPrefix + why, To: "remove the addition in Proxmox, then POST /targets/" + target + "/reenroll", Risk: wire.RiskDisruptive},
		{Attr: AttrRestoreRun, To: runID, Risk: wire.RiskRead},
	}); ierr != nil {
		e.log.Error("show the refused re-enroll in the Issue", "target", target, "err", ierr)
	}
}

// RestoredRefusedPrefix starts the state of a guest:restore-failed Issue
// whose restore worked but whose re-enroll Plan was refused (host D10).
const RestoredRefusedPrefix = "restored; the agent is not enrolled: "

// AttrRestoreRun is the change of a guest:restore-failed Issue in the
// "restored" state that names the restore Run: the gate takes its proof
// from that Run's stored step, never from the Issue text.
const AttrRestoreRun = "restore_run"

// restoredIssue sets the guest:restore-failed Issue to the "restored"
// state: the guest's disks are the restored ones; only the follow-up is
// missing. The gate then lets a re-enroll pass (restoreFailedGate).
func (e *Engine) restoredIssue(ctx context.Context, target string, runID int64, from string) error {
	return e.restoreIssue(ctx, target, GuestRestoreFailedID, wire.StatusDiffers, []wire.Change{
		{Attr: "restore", From: from, To: "the follow-up of the restore", Risk: wire.RiskDisruptive},
		{Attr: AttrRestoreRun, To: runID, Risk: wire.RiskRead},
	})
}

// restoredProof reports whether the open guest:restore-failed Issue iss
// names a restore Run whose stored guest:config step says "restored"
// (condition 1 of the T-241 design: proof from the store).
func (e *Engine) restoredProof(ctx context.Context, iss store.Issue) bool {
	var ch []wire.Change
	if json.Unmarshal(iss.Changes, &ch) != nil {
		return false
	}
	for _, c := range ch {
		if c.Attr != AttrRestoreRun {
			continue
		}
		runID, err := driver.IntValue(c.To)
		if err != nil {
			return false
		}
		run, err := e.st.GetRun(ctx, int64(runID))
		if err != nil {
			return false
		}
		if p, err := e.st.GetPlan(ctx, run.PlanID); err != nil || p.Origin != OriginRestore {
			return false
		}
		steps, err := e.st.Steps(ctx, run.ID)
		if err != nil {
			return false
		}
		for _, s := range steps {
			if s.Resource == wire.GuestConfigID && s.Target == iss.Target && restoredOutput(s.Output) {
				return true
			}
		}
	}
	return false
}

// endRestoredByReenroll ends a guest:restore-failed Issue in the
// "restored" state when a re-enroll Run of its Target worked (condition 4,
// D-428).
func (e *Engine) endRestoredByReenroll(ctx context.Context, p store.Plan) {
	var tps []TargetPlan
	if json.Unmarshal(p.Changes, &tps) != nil {
		return
	}
	for _, tp := range tps {
		iss, err := e.st.OpenIssue(ctx, tp.Target, GuestRestoreFailedID)
		if err != nil || !e.restoredProof(ctx, iss) {
			continue
		}
		reason := fmt.Sprintf("the re-enroll Plan %d worked after the restore", p.ID)
		if err := e.endRestoreIssue(ctx, tp.Target, GuestRestoreFailedID, reason); err != nil {
			e.log.Warn("end the Issue of the restore after the re-enroll", "target", tp.Target, "err", err)
		}

	}
}

// cutRunes cuts s at n runes with a mark.
func cutRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…(cut)"
}

// restoredOutput reports a restore step output that proves the restore
// call worked: "restored <backup>…" while it runs or when it applied, and
// "restored, but …" when a later step failed (the disk is the restored
// one, review F1 of #335). "nothing was restored" does not count.
func restoredOutput(out string) bool {
	return strings.HasPrefix(out, restoredMark) || strings.HasPrefix(out, "restored, but")
}

// RestoredNote is the actions note of a guest:restore-failed Issue in the
// "restored" state (D-439), or "": the state and the way out.
func RestoredNote(ch []wire.Change) string {
	for _, c := range ch {
		from, _ := c.From.(string)
		to, _ := c.To.(string)
		if c.Attr == "restore" && strings.HasPrefix(from, "restored") {
			return from + ". Way out: " + to + ". The disks are the restored ones, so Plans of this Target pass; the Issue ends when a re-enroll Run worked."
		}
	}
	return ""
}

// RestoredWaitsPrefix starts the D-440 state of a guest:restore-failed
// Issue: the restore worked, a re-enroll Plan is made, and its Run has not
// worked yet.
const RestoredWaitsPrefix = "restored; the agent is not enrolled yet: "

// AttrReenrollPlan is the change of a guest:restore-failed Issue in the
// D-440 state that names the re-enroll Plan. The way out follows that
// Plan's real state when the Issue is read (RestoredActionNote).
const AttrReenrollPlan = "reenroll_plan"

// waitForReenroll sets the Issue to the D-440 state for the re-enroll
// Plan planID; the restore Run runID is the proof of the D-439 gate.
func (e *Engine) waitForReenroll(ctx context.Context, target string, runID, planID int64) {
	if err := e.restoreIssue(ctx, target, GuestRestoreFailedID, wire.StatusDiffers, []wire.Change{
		{Attr: "restore", From: fmt.Sprintf("%sapprove the re-enroll Plan %d", RestoredWaitsPrefix, planID),
			To: fmt.Sprintf("approve the re-enroll Plan %d; when it can no longer run, plan the re-enroll again: POST /targets/%s/reenroll", planID, target), Risk: wire.RiskDisruptive},
		{Attr: AttrRestoreRun, To: runID, Risk: wire.RiskRead},
		{Attr: AttrReenrollPlan, To: planID, Risk: wire.RiskRead},
	}); err != nil {
		e.log.Error("keep the Issue of the restore open until the re-enroll", "target", target, "err", err)
	}
}

// reenrollUsable reports whether the re-enroll Plan pl can still do the
// work (T-252): pending on the head, or approved with no Run yet or a Run
// that has not ended. why says what it is else.
func (e *Engine) reenrollUsable(ctx context.Context, pl store.Plan, head int64) (bool, string, error) {
	switch pl.Status {
	case store.PlanPending:
		if pl.BaseRevision != head {
			return false, "it is stale (another Plan changed the revision)", nil
		}
		return true, "", nil
	case store.PlanApproved:
		run, err := e.st.RunOfPlan(ctx, pl.ID)
		switch {
		case errors.Is(err, store.ErrNotFound):
			return true, "", nil
		case err != nil:
			return false, "", err
		case run.Status == "running":
			return true, "", nil
		case run.Status == "succeeded":
			return false, "its Run worked", nil
		}
		return false, "its Run failed", nil
	}
	if pl.Status == store.PlanStale {
		return false, "it is stale (another Plan changed the revision, or it was dropped)", nil
	}
	return false, "it is " + pl.Status, nil
}

// RestoredActionNote is the actions note of a guest:restore-failed Issue
// in a "restored" state, read at this moment (D-440): for the D-440 state
// it follows the named re-enroll Plan; for the D-439 refusal it is
// RestoredNote. "" for any other state.
func (e *Engine) RestoredActionNote(ctx context.Context, iss store.Issue) string {
	var ch []wire.Change
	if json.Unmarshal(iss.Changes, &ch) != nil {
		return ""
	}
	var planID int64
	for _, c := range ch {
		if c.Attr == AttrReenrollPlan {
			if n, err := driver.IntValue(c.To); err == nil {
				planID = int64(n)
			}
		}
	}
	if planID == 0 {
		return RestoredNote(ch)
	}
	tail := " The disks are the restored ones, so Plans of this Target pass (fix the Target first if the re-enroll failed because of it); the Issue ends when a re-enroll Run worked."
	again := fmt.Sprintf("plan the re-enroll again (\"Propose the re-enroll\", or POST /targets/%s/reenroll).", iss.Target)
	pl, err := e.st.GetPlan(ctx, planID)
	if err != nil {
		return fmt.Sprintf("%sthe re-enroll Plan %d is gone: %s%s", RestoredWaitsPrefix, planID, again, tail)
	}
	head, err := e.st.Head(ctx)
	if err != nil {
		return fmt.Sprintf("%sapprove the re-enroll Plan %d.%s", RestoredWaitsPrefix, planID, tail)
	}
	ok, why, err := e.reenrollUsable(ctx, pl, head.ID)
	if ok && pl.Status == store.PlanApproved {
		if run, err := e.st.RunOfPlan(ctx, pl.ID); err == nil && run.Status == "running" {
			return fmt.Sprintf("%sthe re-enroll Plan %d runs (run %d): wait for it.%s", RestoredWaitsPrefix, planID, run.ID, tail)
		}
	}
	switch {
	case err != nil, ok:
		return fmt.Sprintf("%sapprove the re-enroll Plan %d.%s", RestoredWaitsPrefix, planID, tail)
	}
	return fmt.Sprintf("%sthe re-enroll Plan %d can no longer run (%s): %s%s", RestoredWaitsPrefix, planID, why, again, tail)
}
