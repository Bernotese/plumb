package api

// T-245 (C9 of T-236), the C2 part: a second opinion on the data disks of
// T-238 (D-423, D-436, D-437, ARCHITECTURE §21) by tests that its author
// did not write. Through the API env and the fake driver: who may approve
// (token vs person), missing vs [], the slot as identity, occupied slots,
// the cold add, the life of guest:volume-left, the gate of a grow, and a
// Target delete with VMID reuse.

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/Bernotese/plumb/internal/driver"
	"github.com/Bernotese/plumb/internal/driver/fake"
	"github.com/Bernotese/plumb/internal/issue"
	"github.com/Bernotese/plumb/internal/state"
	"github.com/Bernotese/plumb/internal/wire"
)

// qaPropose makes a Plan with c and returns its code, body and plan.
func (e *env) qaPropose(c *client, put ...state.Doc) (int, []byte, diskPlan) {
	e.t.Helper()
	code, body := c.do("POST", "/api/v1/plans", map[string]any{"reason": "qa", "put": put})
	var p diskPlan
	json.Unmarshal(body, &p)
	return code, body, p
}

// qaApprove approves plan id with c and waits for the Run.
func (e *env) qaApprove(c *client, id int64) (int, []byte) {
	e.t.Helper()
	code, body := c.do("POST", "/api/v1/plans/"+itoa(id)+"/approve", nil)
	e.eng.Wait()
	return code, body
}

func (e *env) qaGuest(target string) driver.GuestState {
	e.t.Helper()
	st, err := e.drv.Observe(e.ctx, e.guestID(target))
	if err != nil {
		e.t.Fatal(err)
	}
	return st
}

// Who may approve (§21 risk table): a VM add is reversible, so an apply
// token may approve it; a remove is destructive, so no token may, only a
// person. A drift Revert that removes a disk is the same: a token makes
// the Plan, only a person approves it.
func TestQADiskApprovalByTier(t *testing.T) {
	e := newEnv(t, true)
	adm := e.admin()
	e.seed(adm, "web1")
	planTok := e.token(TierPlan, "")
	rev := e.token(TierApply, wire.RiskReversible)
	dis := e.token(TierApply, wire.RiskDisruptive)

	code, body, p := e.qaPropose(planTok, diskTarget("web1", `[{"slot":1,"size_gb":2}]`, true))
	if code != 201 || p.Risk != "reversible" {
		t.Fatalf("add by a plan token: %d %s", code, body)
	}
	if code, body := e.qaApprove(planTok, p.ID); code != 403 {
		t.Errorf("a plan token approved: %d %s", code, body)
	}
	if code, body := e.qaApprove(rev, p.ID); code != 202 {
		t.Fatalf("an apply token (reversible) on a reversible add: %d %s", code, body)
	}
	if e.qaGuest("web1").Spec.DiskAt(1) == nil {
		t.Fatal("no disk 1 after the add")
	}

	code, body, p = e.qaPropose(planTok, diskTarget("web1", `[]`, true))
	if _, _, risk, ok := p.change("disk1"); code != 201 || !ok || risk != "destructive" {
		t.Fatalf("remove: %d %s", code, body)
	}
	for name, c := range map[string]*client{"reversible": rev, "disruptive": dis} {
		if code, body := e.qaApprove(c, p.ID); code != 403 {
			t.Errorf("an apply token (%s) approved a remove: %d %s", name, code, body)
		}
	}
	if e.qaGuest("web1").Spec.DiskAt(1) == nil {
		t.Fatal("a token removed the disk")
	}
	if code, body := e.qaApprove(adm, p.ID); code != 202 {
		t.Fatalf("a person approves the remove: %d %s", code, body)
	}
	if e.qaGuest("web1").Spec.DiskAt(1) != nil {
		t.Fatal("disk 1 is still there")
	}

	// A foreign volume in an unnamed slot of a managed list: drift whose
	// Revert detaches it, destructive, so a token's Revert waits for a person.
	e.drv.SetForeignDisk(e.guestID("web1"), 3, "other:vm-1-disk-9,size=1G")
	e.eng.CheckGuests(e.ctx)
	iss, err := e.st.OpenIssue(e.ctx, "web1", wire.GuestConfigID)
	if err != nil || !strings.Contains(string(iss.Changes), "not made by Plumb") {
		t.Fatalf("no drift for the foreign disk: %+v %v", iss, err)
	}
	if code, body := planTok.do("POST", "/api/v1/issues/"+itoa(iss.ID)+"/resolve", resolveBody("accept")); code < 400 {
		t.Errorf("accept took a foreign volume: %d %s", code, body)
	}
	code, body = planTok.do("POST", "/api/v1/issues/"+itoa(iss.ID)+"/resolve", resolveBody("revert"))
	var rp struct {
		ID   int64
		Risk string
	}
	json.Unmarshal(body, &rp)
	if code != 201 || rp.Risk != "destructive" {
		t.Fatalf("revert by a token: %d %s", code, body)
	}
	if code, body := e.qaApprove(dis, rp.ID); code != 403 {
		t.Errorf("a token approved the detach: %d %s", code, body)
	}
	if code, body := e.qaApprove(adm, rp.ID); code != 202 {
		t.Fatalf("a person approves the detach: %d %s", code, body)
	}
	st := e.qaGuest("web1")
	if st.Spec.DiskAt(3) != nil || !slices.Contains(st.Spec.Unused, "other:vm-1-disk-9") || len(e.drv.Leftover()) != 0 {
		t.Fatalf("the foreign volume must be detached and kept: disks %+v unused %v", st.Spec.Disks, st.Spec.Unused)
	}
}

// Missing vs []: removing the field deletes nothing and plans no disk
// change; [] then removes every disk in that Plan, destructive, with the
// volumes named. The slot is the identity: removing slot 1 of two keeps
// slot 2 and its volume.
func TestQADiskMissingThenEmptyAndSlotIdentity(t *testing.T) {
	e := newEnv(t, true)
	adm := e.admin()
	e.seed(adm, "web1")
	e.diskRun(adm, diskTarget("web1", `[{"slot":1,"size_gb":2},{"slot":2,"size_gb":3}]`, true))
	before := e.qaGuest("web1").Spec
	v2 := before.DiskAt(2).Volume

	// Slot 1 goes; slot 2 keeps its volume and size.
	_, _, p := e.qaPropose(adm, diskTarget("web1", `[{"slot":2,"size_gb":3}]`, true))
	if _, _, _, ok := p.change("disk2"); ok {
		t.Fatalf("removing slot 1 changed slot 2: %+v", p)
	}
	if code, body := e.qaApprove(adm, p.ID); code != 202 {
		t.Fatalf("approve: %d %s", code, body)
	}
	if d := e.qaGuest("web1").Spec.DiskAt(2); d == nil || d.Volume != v2 || d.SizeGB != 3 || e.qaGuest("web1").Spec.DiskAt(1) != nil {
		t.Fatalf("after removing slot 1: %+v", e.qaGuest("web1").Spec.Disks)
	}

	// The field goes: no disk change, the disk stays.
	code, body, p := e.qaPropose(adm, targetDoc("web1", "hello"))
	if code == 201 {
		if _, _, _, ok := p.change("disk2"); ok {
			t.Fatalf("a missing field planned a disk change: %s", body)
		}
		e.qaApprove(adm, p.ID)
	}
	if e.qaGuest("web1").Spec.DiskAt(2) == nil {
		t.Fatal("removing the field removed the disk")
	}

	// [] from a missing field: the remove is in this Plan, destructive.
	code, body, p = e.qaPropose(adm, diskTarget("web1", `[]`, true))
	if from, to, risk, ok := p.change("disk2"); code != 201 || !ok || from != "3 GiB" || to != nil || risk != "destructive" || p.Risk != "destructive" ||
		!strings.Contains(strings.Join(p.Warnings, " "), v2) {
		t.Fatalf("missing → []: %d %s", code, body)
	}
}

// Occupied container slots (H1 of #373): a bind mount in mp1 is never
// overwritten by an add; with a missing list it is the person's mount, no
// disk change; with a managed list that does not name it, the Plan
// detaches it (destructive) and frees nothing.
func TestQADiskOccupiedContainerSlot(t *testing.T) {
	e := newEnv(t, true)
	adm := e.admin()
	e.qaSeedCT(adm, "c1")
	e.drv.SetForeignDisk(e.guestID("c1"), 1, "/srv/host,mp=/data")

	code, body, _ := e.qaPropose(adm, ctDiskTarget("c1", `[{"slot":1,"size_gb":2,"path":"/srv/data"}]`, true))
	if code != 422 || pointerOf(body) != "/put/0/spec/guest/disks/0" || !strings.Contains(string(body), "Plumb did not make") {
		t.Fatalf("an add over a bind mount: %d %s", code, body)
	}
	code, body, p := e.qaPropose(adm, ctDiskTarget("c1", `[{"slot":2,"size_gb":2,"path":"/srv/data"}]`, true))
	if _, _, risk, ok := p.change("disk1"); code != 201 || !ok || risk != "destructive" ||
		!strings.Contains(strings.Join(p.Warnings, " "), "Plumb does not free it") {
		t.Fatalf("a managed list without slot 1: %d %s", code, body)
	}
	if _, _, risk, ok := p.change("disk2"); !ok || risk != "disruptive" {
		t.Fatalf("the add of slot 2 on a running container: %s", body)
	}
	if code, body := e.qaApprove(adm, p.ID); code != 202 {
		t.Fatalf("approve: %d %s", code, body)
	}
	st := e.qaGuest("c1")
	if st.Spec.DiskAt(1) != nil || st.Spec.DiskAt(2) == nil || len(st.Spec.Unused) != 0 || len(e.drv.Leftover()) != 0 {
		t.Fatalf("after: disks %+v unused %v leftover %v", st.Spec.Disks, st.Spec.Unused, e.drv.Leftover())
	}
}

// D-437: a stopped container gets its disk written cold, reversible, with
// no allow_restart and no start; the Plan says that the disk is proven
// only at its next start.
func TestQADiskColdAddStoppedContainer(t *testing.T) {
	e := newEnv(t, true)
	adm := e.admin()
	e.qaSeedCT(adm, "c1")
	if err := e.drv.Power(e.ctx, e.guestID("c1"), driver.PowerStop); err != nil {
		t.Fatal(err)
	}
	code, body, p := e.qaPropose(adm, ctDiskTarget("c1", `[{"slot":1,"size_gb":2,"path":"/srv/data"}]`, false))
	if _, _, risk, ok := p.change("disk1"); code != 201 || !ok || risk != "reversible" ||
		!strings.Contains(strings.Join(p.Warnings, " "), "proven only at its next start") {
		t.Fatalf("cold add: %d %s", code, body)
	}
	if code, body := e.qaApprove(e.token(TierApply, wire.RiskReversible), p.ID); code != 202 {
		t.Fatalf("approve: %d %s", code, body)
	}
	st := e.qaGuest("c1")
	if st.Spec.DiskAt(1) == nil || st.Power != driver.PowerStopped {
		t.Fatalf("after the cold add: power %s disks %+v", st.Power, st.Spec.Disks)
	}
}

// The life of guest:volume-left (D-437 (4)): a failed free opens it; the
// guest poll keeps it while the volume is still there; no token can
// ignore it, no Rule hides it; every add of the Target is refused, also a
// Revert of the empty slot; when a person frees the volume, the poll ends
// it with a comment, and adds are possible again.
func TestQADiskVolumeLeftLifecycle(t *testing.T) {
	e := newEnv(t, true)
	adm := e.admin()
	e.seed(adm, "web1")
	e.diskRun(adm, diskTarget("web1", `[{"slot":1,"size_gb":2},{"slot":2,"size_gb":1}]`, true))
	e.drv.FailDiskFree(true)
	e.diskRun(adm, diskTarget("web1", `[{"slot":2,"size_gb":1}]`, true))
	e.drv.FailDiskFree(false)
	left := e.drv.Leftover()
	if len(left) != 1 {
		t.Fatalf("leftover %v", left)
	}
	iss, err := e.st.OpenIssue(e.ctx, "web1", "guest:volume-left")
	if err != nil {
		t.Fatal(err)
	}
	e.eng.CheckGuests(e.ctx)
	if again, err := e.st.OpenIssue(e.ctx, "web1", "guest:volume-left"); err != nil || again.ID != iss.ID {
		t.Fatalf("the poll ended the Issue while the volume is there: %v", err)
	}
	tok := e.token(TierApply, wire.RiskDisruptive)
	// (ignore_rule is finding F1 of T-245 C2: a token can make a Rule Plan
	// for it today, which puts the Issue into "resolving"; see the log.)
	for _, a := range []string{issue.ActionIgnoreOnce, "revert", "accept"} {
		if code, body := tok.do("POST", "/api/v1/issues/"+itoa(iss.ID)+"/resolve", resolveBody(a)); code < 400 {
			t.Errorf("a token's %s on guest:volume-left: %d %s", a, code, body)
		}
	}
	// Every add is refused while it is open; a grow of slot 2 is not an add.
	if code, body, _ := e.qaPropose(adm, diskTarget("web1", `[{"slot":2,"size_gb":1},{"slot":3,"size_gb":1}]`, true)); code != 422 ||
		!strings.Contains(string(body), "waits for its removal: "+left[0]) {
		t.Errorf("an add while the volume is left: %d %s", code, body)
	}
	if code, body, _ := e.qaPropose(adm, diskTarget("web1", `[{"slot":2,"size_gb":2}]`, true)); code != 201 {
		t.Errorf("a grow while the volume is left: %d %s", code, body)
	}
	// The slot of the failed remove is empty but named again: a Revert of
	// that drift would be an add, and is refused too.
	if code, body, _ := e.qaPropose(adm, diskTarget("web1", `[{"slot":1,"size_gb":2},{"slot":2,"size_gb":1}]`, true)); code != 422 {
		t.Errorf("re-adding slot 1 while its old volume is left: %d %s", code, body)
	}

	// A person frees the volume: the poll ends the Issue with a comment.
	e.drv.FreeVolume(e.guestID("web1"), left[0])
	e.eng.CheckGuests(e.ctx)
	if _, err := e.st.OpenIssue(e.ctx, "web1", "guest:volume-left"); err == nil {
		t.Fatal("the Issue stays after the volume is gone")
	}
	_, view := adm.do("GET", "/api/v1/issues/"+itoa(iss.ID), nil)
	if !strings.Contains(string(view), `"author":"system"`) {
		t.Errorf("no system comment on the end (D-428): %s", view)
	}
	if code, body, _ := e.qaPropose(adm, diskTarget("web1", `[{"slot":2,"size_gb":1},{"slot":3,"size_gb":1}]`, true)); code != 201 {
		t.Errorf("an add after the volume is gone: %d %s", code, body)
	}
}

// The gate counts a grow by its difference, and a create by its data
// disks too (D-219, M1 of #360).
func TestQADiskGateGrowAndCreate(t *testing.T) {
	e := newEnv(t, true)
	adm := e.admin()
	e.seed(adm, "web1")
	e.diskRun(adm, diskTarget("web1", `[{"slot":1,"size_gb":4}]`, false))
	// used 67 + template 3 + web1 10 + disk 4 = 84 %: a grow by 1 GiB
	// passes (85 %), a grow by 2 more does not.
	e.drv.SetDiskSpace(100*fake.GiB, 67*fake.GiB, false)
	_, run, steps := e.diskRun(adm, diskTarget("web1", `[{"slot":1,"size_gb":5}]`, false))
	if run.Status != "succeeded" {
		t.Fatalf("a grow to the limit: %s %s", run.Status, guestStepOutput(steps, "web1"))
	}
	_, run, steps = e.diskRun(adm, diskTarget("web1", `[{"slot":1,"size_gb":7}]`, false))
	if out := guestStepOutput(steps, "web1"); run.Status != "failed" || !strings.Contains(out, "no data disk was added or grown") {
		t.Fatalf("a grow over the limit: %s %s", run.Status, out)
	}
	if d := e.qaGuest("web1").Spec.DiskAt(1); d == nil || d.SizeGB != 5 {
		t.Fatalf("disk 1 after the refused grow: %+v", d)
	}
	if e.eng.Disk().Reserved() != 0 {
		t.Fatalf("reserved %d after the Runs", e.eng.Disk().Reserved())
	}

	// A new Target whose main disk fits but whose data disk does not:
	// used 50 + template 3 + web1 15 = 68 %; web2 10 GiB alone = 78 %,
	// with its 20 GiB data disk 98 %.
	e.drv.SetDiskSpace(100*fake.GiB, 50*fake.GiB, false)
	e.agent("web2")
	code, body := adm.do("POST", "/api/v1/plans", map[string]any{"reason": "new", "put": []state.Doc{diskTarget("web2", `[{"slot":1,"size_gb":20}]`, false)}})
	var p diskPlan
	json.Unmarshal(body, &p)
	if code != 201 || !strings.Contains(strings.Join(p.Warnings, " "), "with the 30 GiB disk of the new guest") {
		t.Fatalf("create with a big data disk: %d %s", code, body)
	}
	e.qaApprove(adm, p.ID)
	_, rb := adm.do("GET", "/api/v1/runs?limit=1", nil)
	if !strings.Contains(string(rb), "with 20 GiB of data disks") || !strings.Contains(string(rb), "nothing was made") {
		t.Fatalf("the create Run: %.400s", rb)
	}
	if len(e.drv.Guests()) != 1 || e.eng.Disk().Reserved() != 0 {
		t.Fatalf("guests %v, reserved %d", e.drv.Guests(), e.eng.Disk().Reserved())
	}
}

// Per-Target state (D-413 rule 2): a Target delete ends guest:volume-left
// and names the disks; a new Target of the same name on the reused VMID
// starts with no disk, no unused volume and no old Issue, so its add runs.
func TestQADiskDeleteAndVMIDReuse(t *testing.T) {
	e := newEnv(t, true)
	adm := e.admin()
	e.seed(adm, "web1")
	e.diskRun(adm, diskTarget("web1", `[{"slot":1,"size_gb":2},{"slot":2,"size_gb":1}]`, true))
	e.drv.FailDiskFree(true)
	e.diskRun(adm, diskTarget("web1", `[{"slot":2,"size_gb":1}]`, true))
	e.drv.FailDiskFree(false)
	oldRef := e.guestID("web1")
	iss, err := e.st.OpenIssue(e.ctx, "web1", "guest:volume-left")
	if err != nil {
		t.Fatal(err)
	}
	code, body := adm.do("POST", "/api/v1/plans", map[string]any{"reason": "bye", "delete": []map[string]string{{"kind": "Target", "name": "web1"}}})
	if code != 201 || !strings.Contains(string(body), "is lost with the guest") || !strings.Contains(string(body), "The unused volume") {
		t.Fatalf("delete plan: %d %s", code, body)
	}
	var p diskPlan
	json.Unmarshal(body, &p)
	if code, body := e.qaApprove(adm, p.ID); code != 202 {
		t.Fatalf("delete: %d %s", code, body)
	}
	if _, err := e.st.OpenIssue(e.ctx, "web1", "guest:volume-left"); err == nil {
		t.Fatal("guest:volume-left stays after the Target delete")
	}
	_, view := adm.do("GET", "/api/v1/issues/"+itoa(iss.ID), nil)
	if !strings.Contains(string(view), `"status":"resolved"`) {
		t.Errorf("the Issue after the delete: %s", view)
	}

	e.seed(adm, "web1")
	if e.guestID("web1") != oldRef {
		t.Logf("the VMID was not reused (%s, before %s): the check below still holds", e.guestID("web1"), oldRef)
	}
	st := e.qaGuest("web1")
	if len(st.Spec.Disks) != 0 || len(st.Spec.Unused) != 0 {
		t.Fatalf("the new guest has old disks: %+v %v", st.Spec.Disks, st.Spec.Unused)
	}
	if code, body, _ := e.qaPropose(adm, diskTarget("web1", `[{"slot":1,"size_gb":1}]`, true)); code != 201 {
		t.Fatalf("an add on the new Target: %d %s", code, body)
	}
}

// qaSeedCT creates and applies a running container Target without disks.
func (e *env) qaSeedCT(adm *client, name string) {
	e.t.Helper()
	e.agent(name)
	ct := doc(state.KindImage, "ct", `{"type":"lxc","url":"http://download.proxmox.com/images/system/debian-13.tar.zst","sha512":"`+strings.Repeat("c", 128)+`"}`)
	code, body := adm.do("POST", "/api/v1/plans", map[string]any{"reason": "seed", "put": []state.Doc{ct, ctDiskTarget(name, `[]`, false)}})
	if code != 201 {
		e.t.Fatalf("seed: %d %s", code, body)
	}
	var p diskPlan
	json.Unmarshal(body, &p)
	if code, body := e.qaApprove(adm, p.ID); code != 202 {
		e.t.Fatalf("seed approve: %d %s", code, body)
	}
}
