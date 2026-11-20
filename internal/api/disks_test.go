package api

// T-238: data disks through the API (D-423, D-436, D-437). The fake driver
// models the end state of each Proxmox step and the D-437 failure paths.

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/Bernotese/plumb/internal/driver"
	"github.com/Bernotese/plumb/internal/driver/fake"
	"github.com/Bernotese/plumb/internal/issue"
	"github.com/Bernotese/plumb/internal/state"
	"github.com/Bernotese/plumb/internal/store"
	"github.com/Bernotese/plumb/internal/wire"
)

func diskTarget(name, disks string, allowRestart bool) state.Doc {
	ar := ""
	if allowRestart {
		ar = `"allow_restart":true,`
	}
	return doc(state.KindTarget, name, `{"type":"vm","image":"ubuntu",`+ar+`"guest":{"cores":2,"memory":1024,"disk":10,"bridge":"vmbr0","disks":`+disks+`},`+
		`"blocks":[{"use":"motd","params":{"text":"hello"}}]}`)
}

func ctDiskTarget(name, disks string, allowRestart bool) state.Doc {
	ar := ""
	if allowRestart {
		ar = `"allow_restart":true,`
	}
	return doc(state.KindTarget, name, `{"type":"lxc","image":"ct",`+ar+`"guest":{"cores":1,"memory":512,"disk":4,"bridge":"vmbr0","disks":`+disks+`}}`)
}

type diskPlan struct {
	ID       int64
	Risk     string
	Warnings []string
	Targets  []struct {
		Resources []struct {
			Resource string
			Changes  []struct {
				Attr     string
				From, To any
				Risk     string
			}
		}
	}
}

func (p diskPlan) change(attr string) (from, to any, risk string, ok bool) {
	for _, t := range p.Targets {
		for _, r := range t.Resources {
			for _, c := range r.Changes {
				if c.Attr == attr && r.Resource == "guest:config" {
					return c.From, c.To, c.Risk, true
				}
			}
		}
	}
	return nil, nil, "", false
}

// pointerOf returns the first JSON pointer of a 422 validation problem.
func pointerOf(body []byte) string {
	var p struct{ Errors []struct{ Pointer string } }
	json.Unmarshal(body, &p)
	if len(p.Errors) == 0 {
		return ""
	}
	return p.Errors[0].Pointer
}

func TestVMDisksThroughTheAPI(t *testing.T) {
	e := newEnv(t, true)
	adm := e.admin()
	e.seed(adm, "web1")
	propose := func(d state.Doc) (int, []byte, diskPlan) {
		code, body := adm.do("POST", "/api/v1/plans", map[string]any{"reason": "disks", "put": []state.Doc{d}})
		var p diskPlan
		json.Unmarshal(body, &p)
		return code, body, p
	}
	apply := func(p diskPlan) {
		t.Helper()
		if code, body := adm.do("POST", "/api/v1/plans/"+itoa(p.ID)+"/approve", nil); code != 202 {
			t.Fatalf("approve: %d %s", code, body)
		}
		e.eng.Wait()
	}
	guest := func() driver.GuestState {
		b, _ := e.st.GetBinding(e.ctx, "Target", "web1")
		st, err := e.drv.Observe(e.ctx, driver.GuestID(b.Ref))
		if err != nil {
			t.Fatal(err)
		}
		return st
	}

	// Add: a VM disk is a live hot-add, reversible.
	code, body, p := propose(diskTarget("web1", `[{"slot":2,"size_gb":4}]`, false))
	if _, to, risk, ok := p.change("disk2"); code != 201 || !ok || to != "4 GiB" || risk != "reversible" || p.Risk != "reversible" {
		t.Fatalf("add: %d %s", code, body)
	}
	apply(p)
	dk := guest().Spec.DiskAt(2)
	if dk == nil || dk.SizeGB != 4 || dk.Volume == "" {
		t.Fatalf("guest disks: %+v", guest().Spec.Disks)
	}

	// Grow: disruptive. Shrink: 422 at the disk's size.
	if code, body, p := propose(diskTarget("web1", `[{"slot":2,"size_gb":6}]`, false)); code != 201 || p.Risk != "disruptive" {
		t.Fatalf("grow: %d %s", code, body)
	}
	if code, body, _ := propose(diskTarget("web1", `[{"slot":2,"size_gb":3}]`, false)); code != 422 || pointerOf(body) != "/put/0/spec/guest/disks/0/size_gb" {
		t.Errorf("shrink: %d %s", code, body)
	}

	// Remove on a running guest: allow_restart first, then destructive and
	// the warning names the volume (H2 of #360: also from a missing list).
	if code, body, _ := propose(diskTarget("web1", `[]`, false)); code != 422 || pointerOf(body) != "/put/0/spec/guest/disks" || !strings.Contains(string(body), "allow_restart") {
		t.Errorf("remove without allow_restart: %d %s", code, body)
	}
	code, body, p = propose(diskTarget("web1", `[]`, true))
	if from, to, risk, ok := p.change("disk2"); code != 201 || !ok || from != "4 GiB" || to != nil || risk != "destructive" ||
		!strings.Contains(strings.Join(p.Warnings, " "), dk.Volume) {
		t.Fatalf("remove: %d %s", code, body)
	}
	apply(p)
	if guest().Spec.DiskAt(2) != nil {
		t.Fatal("disk 2 still there")
	}

	// A missing disks field manages nothing: no change for a person's disk.
	b, _ := e.st.GetBinding(e.ctx, "Target", "web1")
	e.drv.SetForeignDisk(driver.GuestID(b.Ref), 1, "other:vm-1-disk-9,size=1G")
	if code, body, _ := propose(targetDoc("web1", "hello2")); code != 201 || strings.Contains(string(body), `"disk1"`) {
		t.Errorf("unmanaged: %d %s", code, body)
	}
	// A slot with a foreign volume is never taken over (H1 of #360).
	if code, body, _ := propose(diskTarget("web1", `[{"slot":3,"size_gb":1},{"slot":1,"size_gb":1}]`, true)); code != 422 || pointerOf(body) != "/put/0/spec/guest/disks/1" || !strings.Contains(string(body), "Plumb did not make") {
		t.Errorf("foreign slot: %d %s", code, body)
	}
}

// D-437 (4), M1 of #365: a failed free opens guest:volume-left, and no
// further add is planned while it is open.
func TestDiskVolumeLeft(t *testing.T) {
	e := newEnv(t, true)
	adm := e.admin()
	e.seed(adm, "web1")
	run := func(d state.Doc) {
		t.Helper()
		code, body := adm.do("POST", "/api/v1/plans", map[string]any{"reason": "disks", "put": []state.Doc{d}})
		if code != 201 {
			t.Fatalf("propose: %d %s", code, body)
		}
		var p diskPlan
		json.Unmarshal(body, &p)
		if code, body := adm.do("POST", "/api/v1/plans/"+itoa(p.ID)+"/approve", nil); code != 202 {
			t.Fatalf("approve: %d %s", code, body)
		}
		e.eng.Wait()
	}
	run(diskTarget("web1", `[{"slot":1,"size_gb":2}]`, true))
	e.drv.FailDiskFree(true)
	run(diskTarget("web1", `[]`, true))
	left := e.drv.Leftover()
	if len(left) != 1 {
		t.Fatalf("leftover %v", left)
	}
	iss, err := e.st.OpenIssue(e.ctx, "web1", "guest:volume-left")
	if err != nil || !strings.Contains(string(iss.Changes), left[0]) {
		t.Fatalf("issue: %+v %v", iss, err)
	}
	// Only "ignore once": no Rule may hide a left volume (D-437).
	_, view := adm.do("GET", "/api/v1/issues/"+itoa(iss.ID), nil)
	var v struct{ Actions []string }
	json.Unmarshal(view, &v)
	if !slices.Equal(v.Actions, []string{"ignore_once"}) {
		t.Errorf("volume-left actions %v", v.Actions)
	}
	// F1 of #375: ignore_rule is refused, not only hidden; the Issue stays
	// open, so the poll can still end it when the volume is gone.
	if code, body := adm.do("POST", "/api/v1/issues/"+itoa(iss.ID)+"/resolve", resolveBody("ignore_rule")); code != 409 || problemType(body) != "action-not-allowed" {
		t.Errorf("ignore_rule on volume-left: %d %s", code, body)
	}
	if got, _ := e.st.GetIssue(e.ctx, iss.ID); got.Status != store.IssueOpen {
		t.Errorf("volume-left after the refused rule: %s", got.Status)
	}
	code, body := adm.do("POST", "/api/v1/plans", map[string]any{"reason": "again", "put": []state.Doc{diskTarget("web1", `[{"slot":2,"size_gb":1}]`, true)}})
	if code != 422 || pointerOf(body) != "/put/0/spec/guest/disks/0" || !strings.Contains(string(body), "waits for its removal: "+left[0]) {
		t.Errorf("add while volume-left is open: %d %s", code, body)
	}
}

// D-437: an LXC disk add on a running container needs allow_restart and
// is disruptive; a failed start is undone and fails the step.
func TestCTDisksThroughTheAPI(t *testing.T) {
	e := newEnv(t, true)
	adm := e.admin()
	e.agent("c1")
	ct := doc(state.KindImage, "ct", `{"type":"lxc","url":"http://download.proxmox.com/images/system/debian-13.tar.zst","sha512":"`+strings.Repeat("c", 128)+`"}`)
	code, body := adm.do("POST", "/api/v1/plans", map[string]any{"reason": "seed", "put": []state.Doc{ct, ctDiskTarget("c1", `[]`, false)}})
	if code != 201 {
		t.Fatalf("seed: %d %s", code, body)
	}
	var p diskPlan
	json.Unmarshal(body, &p)
	if code, body := adm.do("POST", "/api/v1/plans/"+itoa(p.ID)+"/approve", nil); code != 202 {
		t.Fatalf("seed approve: %d %s", code, body)
	}
	e.eng.Wait()

	add := ctDiskTarget("c1", `[{"slot":1,"size_gb":2,"path":"/srv/data"}]`, false)
	code, body = adm.do("POST", "/api/v1/plans", map[string]any{"reason": "add", "put": []state.Doc{add}})
	if code != 422 || pointerOf(body) != "/put/0/spec/guest/disks/0" || !strings.Contains(string(body), "set allow_restart") {
		t.Fatalf("add without allow_restart: %d %s", code, body)
	}
	add = ctDiskTarget("c1", `[{"slot":1,"size_gb":2,"path":"/srv/data"}]`, true)
	code, body = adm.do("POST", "/api/v1/plans", map[string]any{"reason": "add", "put": []state.Doc{add}})
	json.Unmarshal(body, &p)
	if _, _, risk, ok := p.change("disk1"); code != 201 || !ok || risk != "disruptive" || !strings.Contains(strings.Join(p.Warnings, " "), "the container is started again") {
		t.Fatalf("add: %d %s", code, body)
	}
	e.drv.FailDiskStart()
	if code, body := adm.do("POST", "/api/v1/plans/"+itoa(p.ID)+"/approve", nil); code != 202 {
		t.Fatalf("approve: %d %s", code, body)
	}
	e.eng.Wait()
	_, rb := adm.do("GET", "/api/v1/runs?limit=1", nil)
	if !strings.Contains(string(rb), "did not start with disk 1 at /srv/data") {
		t.Errorf("run: %s", rb)
	}
	if len(e.drv.Leftover()) != 0 {
		t.Error("a failed start left a volume")
	}
}

// The gate counts the data disks (silent caps are bypasses): a Plan whose
// added disk passes 85 % warns, and its Run adds nothing.
func TestDiskGateCountsDataDisks(t *testing.T) {
	e := newEnv(t, true)
	adm := e.admin()
	e.seed(adm, "web1")
	e.drv.SetDiskSpace(100*fake.GiB, 70*fake.GiB, false) // + the 10 GiB of web1 = 80 %
	warns, run, steps := e.diskRun(adm, diskTarget("web1", `[{"slot":1,"size_gb":6}]`, false))
	if !strings.Contains(strings.Join(warns, " "), "The disk step will fail") {
		t.Errorf("warnings %q", warns)
	}
	if out := guestStepOutput(steps, "web1"); run.Status != "failed" || !strings.Contains(out, "no data disk was added or grown") {
		t.Fatalf("run %s: %s", run.Status, out)
	}
	if len(e.drv.Guests()) != 1 || e.eng.Disk().Reserved() != 0 {
		t.Fatalf("reserved %d", e.eng.Disk().Reserved())
	}
	b, _ := e.st.GetBinding(e.ctx, "Target", "web1")
	if st, _ := e.drv.Observe(e.ctx, driver.GuestID(b.Ref)); len(st.Spec.Disks) != 0 {
		t.Fatalf("a disk was added: %+v", st.Spec.Disks)
	}
}

// A delete Plan names each observed data disk (M1 of #360), also without
// a disks field in the document.
func TestDeleteNamesDataDisks(t *testing.T) {
	e := newEnv(t, true)
	adm := e.admin()
	e.seed(adm, "web1")
	e.diskRun(adm, diskTarget("web1", `[{"slot":3,"size_gb":2}]`, false))
	code, body := adm.do("POST", "/api/v1/plans", map[string]any{"reason": "bye", "delete": []map[string]string{{"kind": "Target", "name": "web1"}}})
	if code != 201 || !strings.Contains(string(body), "Disk 3 (fake:vm-") || !strings.Contains(string(body), "2 GiB) is lost with the guest") {
		t.Fatalf("delete: %d %s", code, body)
	}
}

// D-436 as D-404: an adopted Target has no data disks of Plumb.
func TestDisksNotAdoptable(t *testing.T) {
	e := newEnv(t, true)
	adm := e.admin()
	e.adoptWithAgent(adm, "pet")
	head, _ := e.st.Head(e.ctx)
	docs, _ := e.st.RevisionDocs(e.ctx, head.ID)
	var spec map[string]any
	for _, d := range docs {
		if d.Kind == state.KindTarget && d.Name == "pet" {
			var body map[string]any
			json.Unmarshal(d.Body, &body)
			if spec, _ = body["spec"].(map[string]any); spec == nil {
				spec = body
			}
		}
	}
	spec["guest"].(map[string]any)["disks"] = []any{}
	target := state.Doc{Kind: state.KindTarget, Name: "pet"}
	target.Spec, _ = json.Marshal(spec)
	code, body := adm.do("POST", "/api/v1/plans", map[string]any{"reason": "disks", "put": []state.Doc{target}})
	if code != 422 || problemType(body) != "disks-not-adoptable" {
		t.Errorf("adopted with disks: %d %s", code, body)
	}
}

// A Plumb disk grown in Proxmox: the Issue offers accept (takes the size
// into guest.disks) and no revert; accept writes the document.
func TestDiskGrownInProxmoxAccept(t *testing.T) {
	e := newEnv(t, true)
	adm := e.admin()
	e.seed(adm, "web1")
	e.diskRun(adm, diskTarget("web1", `[{"slot":2,"size_gb":4}]`, false))
	b, _ := e.st.GetBinding(e.ctx, "Target", "web1")
	e.drv.GrowDisk(driver.GuestID(b.Ref), 2, 6)
	e.eng.CheckGuests(e.ctx)
	iss, err := e.st.OpenIssue(e.ctx, "web1", "guest:config")
	if err != nil || !strings.Contains(string(iss.Changes), `"6 GiB"`) {
		t.Fatalf("issue %+v %v", iss, err)
	}
	_, view := adm.do("GET", "/api/v1/issues/"+itoa(iss.ID), nil)
	var v struct{ Actions []string }
	json.Unmarshal(view, &v)
	if !slices.Contains(v.Actions, "accept") || slices.Contains(v.Actions, "revert") {
		t.Fatalf("actions %v", v.Actions)
	}
	code, body := adm.do("POST", "/api/v1/issues/"+itoa(iss.ID)+"/resolve", resolveBody("accept"))
	if code != 201 && code != 202 {
		t.Fatalf("accept: %d %s", code, body)
	}
	var p struct{ ID int64 }
	json.Unmarshal(body, &p)
	if code, body := adm.do("POST", "/api/v1/plans/"+itoa(p.ID)+"/approve", nil); code != 202 {
		t.Fatalf("approve accept: %d %s", code, body)
	}
	e.eng.Wait()
	head, _ := e.st.Head(e.ctx)
	row, err := e.st.RevisionDoc(e.ctx, head.ID, state.KindTarget, "web1")
	if err != nil || !strings.Contains(strings.ReplaceAll(string(row.Body), " ", ""), `{"size_gb":6,"slot":2}`) {
		t.Fatalf("head doc after accept: %s %v", row.Body, err)
	}
	if _, err := e.st.OpenIssue(e.ctx, "web1", "guest:config"); err == nil {
		t.Error("the Issue is still open")
	}
}

// Accept is refused for any other data disk change.
func TestDiskAcceptRefused(t *testing.T) {
	for _, c := range []wire.Change{
		{Attr: "disk1", From: nil, To: "2 GiB"},
		{Attr: "disk1", From: "2 GiB", To: nil},
		{Attr: "disk1", From: "other:vm-1-disk-9 (not made by Plumb)", To: nil},
		{Attr: "disk1", From: "2 GiB", To: "4 GiB"},
		{Attr: "disk1", From: "4 GiB at /srv/a", To: "2 GiB at /srv/b"},
	} {
		if err := issue.AcceptRefused("guest:config", []wire.Change{c}); !errors.Is(err, issue.ErrAcceptDisk) {
			t.Errorf("%+v: %v", c, err)
		}
	}
	if err := issue.AcceptRefused("guest:config", []wire.Change{{Attr: "disk1", From: "4 GiB at /srv/a", To: "2 GiB at /srv/a"}}); err != nil {
		t.Errorf("grown: %v", err)
	}
}

// T-253 F3: the Issue and the step say what to do when a snapshot holds
// the volume (Proxmox's "still in use").
func TestDiskVolumeLeftSnapshotHint(t *testing.T) {
	e := newEnv(t, true)
	adm := e.admin()
	e.seed(adm, "web1")
	e.diskRun(adm, diskTarget("web1", `[{"slot":1,"size_gb":2}]`, true))
	e.drv.FailDiskFreeWith(errors.New("unable to delete 'fake:vm-1-disk-1' - volume is still in use (snapshot?)"))
	_, run, steps := e.diskRun(adm, diskTarget("web1", `[]`, true))
	out := guestStepOutput(steps, "web1")
	if run.Status != "failed" || !strings.Contains(out, "A snapshot may hold the volume: remove the snapshot first") {
		t.Fatalf("step %s: %s", run.Status, out)
	}
	iss, err := e.st.OpenIssue(e.ctx, "web1", "guest:volume-left")
	if err != nil || !strings.Contains(string(iss.Changes), "remove the snapshot first") || !strings.Contains(string(iss.Changes), "Do not `pvesm free`") {
		t.Fatalf("issue %s %v", iss.Changes, err)
	}
}
