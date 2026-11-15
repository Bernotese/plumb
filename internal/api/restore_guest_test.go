package api

// T-222 (D-410, D-417 (4)): restore a guest backup over the bound guest.

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Bernotese/plumb/internal/driver"
	"github.com/Bernotese/plumb/internal/driver/fake"
	"github.com/Bernotese/plumb/internal/plan"
	"github.com/Bernotese/plumb/internal/state"
	"github.com/Bernotese/plumb/internal/store"
	"github.com/Bernotese/plumb/internal/wire"
)

// fakeRestorer holds the classified volumes of the guests and records the
// restores.
type fakeRestorer struct {
	mu       sync.Mutex
	vols     map[string]plan.RestoreVolume // id → volume
	restored []string                      // "ref volid"
	fail     error
	after    func() // runs after a restore that succeeded
	// safety makes the safety backup (D-420); nil: a fixed volume.
	safety   func() (plan.RestoreVolume, error)
	safeties int
	dropped  []string // keep of each DropSafetyBackups
}

func (f *fakeRestorer) SafetyBackup(context.Context, string) (plan.RestoreVolume, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.safeties++
	if f.safety != nil {
		return f.safety()
	}
	return plan.RestoreVolume{ID: "5afe5afe5afe5afe", Volid: "bk:safety", Kind: "plumb", Time: time.Date(2026, 11, 15, 4, 0, 0, 0, time.UTC)}, nil
}

func (f *fakeRestorer) DropSafetyBackups(_ context.Context, _, keep string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.dropped = append(f.dropped, keep)
	return nil
}

func (f *fakeRestorer) FindBackup(_ context.Context, _, _, id string) (plan.RestoreVolume, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	v, ok := f.vols[id]
	if !ok {
		return v, plan.ErrBackupNotFound
	}
	return v, nil
}

func (f *fakeRestorer) RestoreGuest(_ context.Context, ref string, v plan.RestoreVolume) error {
	volid := v.Volid
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail != nil {
		return f.fail
	}
	f.restored = append(f.restored, ref+" "+volid)
	if f.after != nil {
		f.after()
	}
	return nil
}

func restoreTarget(name string, allowRestart bool) state.Doc {
	allow := "false"
	if allowRestart {
		allow = "true"
	}
	return doc(state.KindTarget, name, `{"type":"vm","image":"ubuntu","allow_restart":`+allow+`,"guest":{"cores":2,"memory":1024,"disk":10,"bridge":"vmbr0"},`+
		`"blocks":[{"use":"motd","params":{"text":"hello"}}]}`)
}

func TestRestoreGuestOverTheBoundGuest(t *testing.T) {
	fr := &fakeRestorer{vols: map[string]plan.RestoreVolume{}}
	e := newEnv(t, true, func(c *plan.Config) { c.GuestRestore = fr; c.InstallID = "iid" })
	adm := e.admin()
	e.seed(adm, "web1")
	ctx := context.Background()
	b, err := e.st.GetBinding(ctx, state.KindTarget, "web1")
	if err != nil {
		t.Fatal(err)
	}
	lineage, err := e.st.BackupLineage(ctx, "web1", b.Gen)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 11, 15, 3, 0, 12, 0, time.UTC)
	volid := "plumb-backup:backup/vzdump-qemu-" + b.Ref + "-2026_11_15-03_00_12.vma.zst"
	run, _ := e.st.StartGuestBackupRun(ctx, "web1", lineage, "scheduled")
	if err := e.st.FinishGuestBackupRun(ctx, run, "done", volid, 100, at.Unix(), ""); err != nil {
		t.Fatal(err)
	}
	fr.vols["3f2a9c0d1e4b5a67"] = plan.RestoreVolume{ID: "3f2a9c0d1e4b5a67", Volid: volid, Size: 100, Kind: "plumb", Notes: "plumb:iid:web1:" + lineage, Time: at}
	// Unmarked by the backup service (for example a replaced archive of
	// another size), although its marker and run row look right: only
	// the classification refuses it.
	run2, _ := e.st.StartGuestBackupRun(ctx, "web1", lineage, "manual")
	e.st.FinishGuestBackupRun(ctx, run2, "done", "plumb-backup:backup/x", 100, at.Unix(), "")
	fr.vols["00000000000000aa"] = plan.RestoreVolume{ID: "00000000000000aa", Volid: "plumb-backup:backup/x", Kind: "unmarked", Notes: "plumb:iid:web1:" + lineage + ":m", Time: at}
	// A forged copy of the marker: classified plumb by a broken adapter, but no run row.
	fr.vols["00000000000000bb"] = plan.RestoreVolume{ID: "00000000000000bb", Volid: "plumb-backup:backup/forged", Size: 100, Kind: "plumb", Notes: "plumb:iid:web1:" + lineage, Time: at}
	pinned, err := e.st.PinFirstHostKeys(ctx, "web1", b.GuestKey(), []string{"SHA256:old"}, time.Now())
	if err != nil || !pinned {
		t.Fatalf("pin: %v %v", pinned, err)
	}
	if err := e.st.SetRepairReport(ctx, "web1", store.RepairReportRow{GuestID: b.GuestKey(), Report: json.RawMessage(`{}`), ReportedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	planTok := e.token(TierPlan, "")
	restore := func(c *client, id string) (int, []byte) {
		return c.do("POST", "/api/v1/targets/web1/restore", map[string]any{"backup": id, "reason": "the update broke the database"})
	}

	// The guest runs and allow_restart is false: refused.
	if code, body := restore(planTok, "3f2a9c0d1e4b5a67"); code != 422 || !strings.Contains(string(body), "allow_restart") {
		t.Fatalf("running without allow_restart: %d %s", code, body)
	}
	e.approve(adm, "allow restart", restoreTarget("web1", true))
	for _, c := range []struct {
		id, typ string
		code    int
	}{{"00000000000000aa", "not-restorable", 409}, {"00000000000000bb", "not-restorable", 409}, {"0123456789abcdef", "not-found", 404}} {
		if code, body := restore(planTok, c.id); code != c.code || problemType(body) != c.typ {
			t.Errorf("%s: %d %s", c.id, code, body)
		}
	}
	if code, body := restore(planTok, "XYZ"); code != 400 {
		t.Errorf("bad id: %d %s", code, body)
	}

	// The Plan: origin restore, destructive, human-only, the text.
	code, body := restore(planTok, "3f2a9c0d1e4b5a67")
	if code != 201 {
		t.Fatalf("propose: %d %s", code, body)
	}
	var p struct {
		ID        int64
		Origin    string
		Risk      string
		HumanOnly []string `json:"human_only"`
		Warnings  []string
		Revision  *int64
		Targets   []struct {
			Resources []struct {
				Changes []struct {
					Attr string
					To   string
				}
			}
		}
	}
	json.Unmarshal(body, &p)
	if p.Origin != "restore" || p.Risk != "destructive" || !slices.Equal(p.HumanOnly, []string{"guest-restore"}) ||
		p.Targets[0].Resources[0].Changes[0].Attr != "restore" || p.Targets[0].Resources[0].Changes[0].To != "backup 3f2a9c0d1e4b5a67 of 2026-11-15T03:00:12Z" ||
		!strings.Contains(strings.Join(p.Warnings, " "), "authorized_keys") {
		t.Fatalf("plan: %s", body)
	}
	// An apply token cannot approve it.
	if code, body := e.token(TierApply, wire.RiskDisruptive).do("POST", "/api/v1/plans/"+itoa(p.ID)+"/approve", nil); code != 403 || problemType(body) != "human-required" {
		t.Fatalf("apply token approve: %d %s", code, body)
	}
	if code, body := adm.do("POST", "/api/v1/plans/"+itoa(p.ID)+"/approve", nil); code != 202 {
		t.Fatalf("approve: %d %s", code, body)
	}
	e.eng.Wait()

	// The Run: restored over the stopped guest, credential revoked, pins
	// and the repair report gone, no revision.
	if !slices.Equal(fr.restored, []string{b.Ref + " " + volid}) {
		t.Fatalf("restored: %v", fr.restored)
	}
	st, _ := e.drv.Observe(ctx, driver.GuestID(b.Ref))
	if st.Power != driver.PowerStopped {
		t.Errorf("guest power after the restore: %s", st.Power)
	}
	if _, err := e.st.AgentByTarget(ctx, "web1"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("agent after the restore: %v", err)
	}
	if pins, _ := e.st.HostKeyPins(ctx, "web1", b.GuestKey()); len(pins) != 0 {
		t.Errorf("pins after the restore: %v", pins)
	}
	if _, err := e.st.RepairReport(ctx, "web1"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("repair report after the restore: %v", err)
	}
	if code, body := adm.do("GET", "/api/v1/targets/web1", nil); code != 200 || !strings.Contains(string(body), `"repair":null`) {
		t.Errorf("target view: %d %s", code, body)
	}
	if after, _ := e.st.GetBinding(ctx, state.KindTarget, "web1"); after.Gen != b.Gen || after.Ref != b.Ref {
		t.Errorf("binding changed: %+v", after)
	}
	code, body = adm.do("GET", "/api/v1/plans/"+itoa(p.ID), nil)
	json.Unmarshal(body, &p)
	if code != 200 || p.Revision != nil {
		t.Errorf("restore plan after the Run: %d %s", code, body)
	}

	// A re-enroll Plan follows; after it the agent is ready again.
	re, err := e.st.PendingPlans(ctx, plan.OriginReenroll)
	if err != nil || len(re) != 1 {
		t.Fatalf("re-enroll plans: %v %v", re, err)
	}
	if code, body := adm.do("POST", "/api/v1/plans/"+itoa(re[0].ID)+"/approve", nil); code != 202 {
		t.Fatalf("approve re-enroll: %d %s", code, body)
	}
	e.eng.Wait()
	deadline := time.Now().Add(10 * time.Second)
	for !e.link.Online("web1") {
		if time.Now().After(deadline) {
			t.Fatal("the agent is not online after the re-enroll")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if st, _ := e.drv.Observe(ctx, driver.GuestID(b.Ref)); st.Power != driver.PowerRunning {
		t.Errorf("guest power after the re-enroll: %s", st.Power)
	}
}

// The Run checks again: a volume that lost its classification between the
// approval and the Run is refused, nothing changes.
func TestRestoreGuestRunChecksAgain(t *testing.T) {
	fr := &fakeRestorer{vols: map[string]plan.RestoreVolume{}}
	e := newEnv(t, true, func(c *plan.Config) { c.GuestRestore = fr; c.InstallID = "iid" })
	adm := e.admin()
	e.seed(adm, "web1")
	e.approve(adm, "allow restart", restoreTarget("web1", true))
	ctx := context.Background()
	b, _ := e.st.GetBinding(ctx, state.KindTarget, "web1")
	lineage, _ := e.st.BackupLineage(ctx, "web1", b.Gen)
	at := time.Date(2026, 11, 15, 3, 0, 12, 0, time.UTC)
	run, _ := e.st.StartGuestBackupRun(ctx, "web1", lineage, "manual")
	e.st.FinishGuestBackupRun(ctx, run, "done", "bk:v1", 100, at.Unix(), "")
	fr.vols["3f2a9c0d1e4b5a67"] = plan.RestoreVolume{ID: "3f2a9c0d1e4b5a67", Volid: "bk:v1", Size: 100, Kind: "plumb", Notes: "plumb:iid:web1:" + lineage + ":m", Time: at}
	code, body := adm.do("POST", "/api/v1/targets/web1/restore", map[string]any{"backup": "3f2a9c0d1e4b5a67", "reason": "r"})
	if code != 201 {
		t.Fatalf("propose: %d %s", code, body)
	}
	var p struct{ ID int64 }
	json.Unmarshal(body, &p)
	// Another volume under the same id now (a replaced archive): refused at approval.
	fr.mu.Lock()
	v := fr.vols["3f2a9c0d1e4b5a67"]
	v.Time = at.Add(time.Hour)
	fr.vols["3f2a9c0d1e4b5a67"] = v
	fr.mu.Unlock()
	if code, body := adm.do("POST", "/api/v1/plans/"+itoa(p.ID)+"/approve", nil); code != 409 || problemType(body) != "not-restorable" {
		t.Fatalf("approve of a changed volume: %d %s", code, body)
	}
	if len(fr.restored) != 0 {
		t.Errorf("restored: %v", fr.restored)
	}
	if _, err := e.st.AgentByTarget(ctx, "web1"); err != nil {
		t.Errorf("the agent was touched: %v", err)
	}
}

// A failed restore ends the Run with the state named: the guest is stopped
// and its credential is revoked; no re-enroll Plan follows.
func TestRestoreGuestFails(t *testing.T) {
	fr := &fakeRestorer{vols: map[string]plan.RestoreVolume{}, fail: errors.New("archive unreadable")}
	e := newEnv(t, true, func(c *plan.Config) { c.GuestRestore = fr; c.InstallID = "iid" })
	adm := e.admin()
	e.seed(adm, "web1")
	e.approve(adm, "allow restart", restoreTarget("web1", true))
	ctx := context.Background()
	b, _ := e.st.GetBinding(ctx, state.KindTarget, "web1")
	lineage, _ := e.st.BackupLineage(ctx, "web1", b.Gen)
	at := time.Date(2026, 11, 15, 3, 0, 12, 0, time.UTC)
	run, _ := e.st.StartGuestBackupRun(ctx, "web1", lineage, "scheduled")
	e.st.FinishGuestBackupRun(ctx, run, "done", "bk:v1", 100, at.Unix(), "")
	fr.vols["3f2a9c0d1e4b5a67"] = plan.RestoreVolume{ID: "3f2a9c0d1e4b5a67", Volid: "bk:v1", Size: 100, Kind: "plumb", Notes: "plumb:iid:web1:" + lineage, Time: at}
	code, body := adm.do("POST", "/api/v1/targets/web1/restore", map[string]any{"backup": "3f2a9c0d1e4b5a67", "reason": "r"})
	if code != 201 {
		t.Fatalf("propose: %d %s", code, body)
	}
	var p struct{ ID int64 }
	json.Unmarshal(body, &p)
	if code, body := adm.do("POST", "/api/v1/plans/"+itoa(p.ID)+"/approve", nil); code != 202 {
		t.Fatalf("approve: %d %s", code, body)
	}
	e.eng.Wait()
	r, err := e.st.RunOfPlan(ctx, p.ID)
	if err != nil || r.Status != "failed" {
		t.Fatalf("run: %+v %v", r, err)
	}
	steps, _ := e.st.Steps(ctx, r.ID)
	// D-420: the honest text, no raw Proxmox text, the safety backup named.
	if len(steps) != 1 || !strings.Contains(steps[0].Output, "the restore failed: Proxmox did not do it") ||
		!strings.Contains(steps[0].Output, "the guest may have no disk now") ||
		!strings.Contains(steps[0].Output, "restore the safety backup backup 5afe5afe5afe5afe") ||
		strings.Contains(steps[0].Output, "archive unreadable") {
		t.Errorf("steps: %+v", steps)
	}
	if re, _ := e.st.PendingPlans(ctx, plan.OriginReenroll); len(re) != 0 {
		t.Errorf("a re-enroll Plan after a failed restore: %v", re)
	}
}

// Review F1 of #335: the restore call succeeded, a later step failed. The
// Run fails and names the way back, and the re-enroll Plan still follows:
// the disk is the restored one.
func TestRestoreGuestFailsAfterTheRestore(t *testing.T) {
	fr := &fakeRestorer{vols: map[string]plan.RestoreVolume{}}
	e := newEnv(t, true, func(c *plan.Config) { c.GuestRestore = fr; c.InstallID = "iid" })
	fr.after = func() { e.drv.Fail(fake.OpObserve, errors.New("proxmox answered 595")) }
	adm := e.admin()
	e.seed(adm, "web1")
	e.approve(adm, "allow restart", restoreTarget("web1", true))
	ctx := context.Background()
	b, _ := e.st.GetBinding(ctx, state.KindTarget, "web1")
	lineage, _ := e.st.BackupLineage(ctx, "web1", b.Gen)
	at := time.Date(2026, 11, 15, 3, 0, 12, 0, time.UTC)
	run, _ := e.st.StartGuestBackupRun(ctx, "web1", lineage, "scheduled")
	e.st.FinishGuestBackupRun(ctx, run, "done", "bk:v1", 100, at.Unix(), "")
	fr.vols["3f2a9c0d1e4b5a67"] = plan.RestoreVolume{ID: "3f2a9c0d1e4b5a67", Volid: "bk:v1", Size: 100, Kind: "plumb", Notes: "plumb:iid:web1:" + lineage, Time: at}
	code, body := adm.do("POST", "/api/v1/targets/web1/restore", map[string]any{"backup": "3f2a9c0d1e4b5a67", "reason": "r"})
	if code != 201 {
		t.Fatalf("propose: %d %s", code, body)
	}
	var p struct{ ID int64 }
	json.Unmarshal(body, &p)
	if code, body := adm.do("POST", "/api/v1/plans/"+itoa(p.ID)+"/approve", nil); code != 202 {
		t.Fatalf("approve: %d %s", code, body)
	}
	e.eng.Wait()
	r, _ := e.st.RunOfPlan(ctx, p.ID)
	steps, _ := e.st.Steps(ctx, r.ID)
	if r.Status != "failed" || len(steps) != 1 || !strings.Contains(steps[0].Output, "restored, but the guest cannot be read") ||
		!strings.Contains(steps[0].Output, "POST /targets/web1/reenroll") {
		t.Fatalf("run %s, steps %+v", r.Status, steps)
	}
	if re, _ := e.st.PendingPlans(ctx, plan.OriginReenroll); len(re) != 1 {
		t.Fatalf("re-enroll plans after a restore with a failed later step: %v", re)
	}
}
