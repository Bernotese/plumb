package api

// T-226 (QA of T-222, written by pl-architect, who did not write T-222):
// restore over the bound guest. Two agents never live at once; the Run
// checks the volume again and changes nothing when it is no longer
// restorable; the way back of an adopted Target.

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"path/filepath"
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

// phasedRestorer gives a good volume for the first calls of FindBackup
// (the proposal and the approval) and another one from call n on (the
// Run): the volume changed between the approval and the Run.
type phasedRestorer struct {
	mu       sync.Mutex
	calls    int
	from     int
	good     plan.RestoreVolume
	bad      plan.RestoreVolume
	restored int
	after    func()
}

func (f *phasedRestorer) FindBackup(context.Context, string, string, string) (plan.RestoreVolume, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.from > 0 && f.calls >= f.from {
		return f.bad, nil
	}
	return f.good, nil
}

func (f *phasedRestorer) RestoreGuest(context.Context, string, plan.RestoreVolume) error {
	f.mu.Lock()
	f.restored++
	after := f.after
	f.mu.Unlock()
	if after != nil {
		after()
	}
	return nil
}

// recordedVolume records one finished job of target and returns its volume.
func recordedVolume(t *testing.T, e *env, target string) plan.RestoreVolume {
	t.Helper()
	ctx := context.Background()
	b, err := e.st.GetBinding(ctx, state.KindTarget, target)
	if err != nil {
		t.Fatal(err)
	}
	lineage, _ := e.st.BackupLineage(ctx, target, b.Gen)
	at := time.Date(2026, 11, 15, 3, 0, 12, 0, time.UTC)
	volid := "plumb-backup:backup/vzdump-qemu-" + b.Ref + "-qa.vma.zst"
	run, _ := e.st.StartGuestBackupRun(ctx, target, lineage, "scheduled")
	if err := e.st.FinishGuestBackupRun(ctx, run, "done", volid, 100, at.Unix(), ""); err != nil {
		t.Fatal(err)
	}
	return plan.RestoreVolume{ID: "3f2a9c0d1e4b5a67", Volid: volid, Kind: "plumb", Notes: "plumb:iid:" + target + ":" + lineage, Time: at, Size: 100}
}

func (e *env) proposeRestore(adm *client, target, id string) int64 {
	e.t.Helper()
	code, body := adm.do("POST", "/api/v1/targets/"+target+"/restore", map[string]any{"backup": id, "reason": "qa"})
	if code != 201 {
		e.t.Fatalf("propose restore: %d %s", code, body)
	}
	var p struct{ ID int64 }
	json.Unmarshal(body, &p)
	return p.ID
}

// helloWith sends a hello with a raw credential through the real link.
func (e *env) helloWith(cred string) int {
	body, _ := json.Marshal(wire.Report{Type: wire.ReportHello, Hello: &wire.Hello{Version: "0.2", Hostname: "web1"}})
	req := httptest.NewRequest("POST", wire.PathReport, bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+cred)
	rec := httptest.NewRecorder()
	e.link.Handler().ServeHTTP(rec, req)
	return rec.Code
}

// Two agents never live at once (risks §6 rule 1, D-410): the credential
// of the agent before the restore (the one that the restored disk holds)
// is refused from the Run on, also after the re-enroll, which gives a new
// one.
func TestRestoreQATwoAgents(t *testing.T) {
	fr := &phasedRestorer{}
	e := newEnv(t, true, func(c *plan.Config) { c.GuestRestore = fr; c.InstallID = "iid" })
	adm := e.admin()
	e.seed(adm, "web1")
	e.approve(adm, "allow restart", restoreTarget("web1", true))
	fr.good = recordedVolume(t, e, "web1")
	old := e.agents["web1"].Credential()
	if old == "" || e.helloWith(old) != 204 {
		t.Fatalf("setup: the old credential does not work (%q)", old)
	}
	id := e.proposeRestore(adm, "web1", fr.good.ID)
	if code, body := adm.do("POST", "/api/v1/plans/"+itoa(id)+"/approve", nil); code != 202 {
		t.Fatalf("approve: %d %s", code, body)
	}
	e.eng.Wait()
	if fr.restored != 1 {
		t.Fatalf("restored %d", fr.restored)
	}
	if code := e.helloWith(old); code != 401 {
		t.Fatalf("the old credential after the restore: %d, want 401", code)
	}
	re, err := e.st.PendingPlans(context.Background(), plan.OriginReenroll)
	if err != nil || len(re) != 1 {
		t.Fatalf("re-enroll plans %v %v", re, err)
	}
	if code, body := adm.do("POST", "/api/v1/plans/"+itoa(re[0].ID)+"/approve", nil); code != 202 {
		t.Fatalf("approve re-enroll: %d %s", code, body)
	}
	e.eng.Wait()
	deadline := time.Now().Add(10 * time.Second)
	for !e.link.Online("web1") {
		if time.Now().After(deadline) {
			t.Fatal("no agent after the re-enroll")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if nu := e.agents["web1"].Credential(); nu == "" || nu == old {
		t.Fatalf("new credential %q (old %q)", nu, old)
	}
	if code := e.helloWith(old); code != 401 {
		t.Fatalf("the old credential after the re-enroll: %d, want 401", code)
	}
}

// The Run checks the volume again: an older-guest, an unmarked (replaced)
// volume, one of another time, and one whose job row is gone are refused
// in the Run; nothing is stopped, revoked or restored.
func TestRestoreQARunRefusesChangedVolume(t *testing.T) {
	for name, change := range map[string]func(v *plan.RestoreVolume){
		"older guest":   func(v *plan.RestoreVolume) { v.Kind = "older-guest" },
		"unmarked":      func(v *plan.RestoreVolume) { v.Kind = "unmarked" },
		"replaced time": func(v *plan.RestoreVolume) { v.Time = v.Time.Add(time.Minute) },
		"other lineage": func(v *plan.RestoreVolume) { v.Notes = "plumb:iid:web1:fedcba9876543210" },
		"other target":  func(v *plan.RestoreVolume) { v.Notes = strings.Replace(v.Notes, ":web1:", ":web2:", 1) },
		"no job row":    func(v *plan.RestoreVolume) { v.Volid = "plumb-backup:backup/never-recorded" },
	} {
		t.Run(name, func(t *testing.T) {
			fr := &phasedRestorer{from: 3} // 1 propose, 2 approve, 3 the Run
			e := newEnv(t, true, func(c *plan.Config) { c.GuestRestore = fr; c.InstallID = "iid" })
			adm := e.admin()
			e.seed(adm, "web1")
			e.approve(adm, "allow restart", restoreTarget("web1", true))
			fr.good = recordedVolume(t, e, "web1")
			fr.bad = fr.good
			change(&fr.bad)
			id := e.proposeRestore(adm, "web1", fr.good.ID)
			if code, body := adm.do("POST", "/api/v1/plans/"+itoa(id)+"/approve", nil); code != 202 {
				t.Fatalf("approve: %d %s", code, body)
			}
			e.eng.Wait()
			ctx := context.Background()
			r, _ := e.st.RunOfPlan(ctx, id)
			steps, _ := e.st.Steps(ctx, r.ID)
			if r.Status != "failed" || len(steps) != 1 || !strings.HasPrefix(steps[0].Output, "nothing was changed") {
				t.Fatalf("run %s, steps %+v", r.Status, steps)
			}
			if fr.restored != 0 {
				t.Fatal("restored")
			}
			if _, err := e.st.AgentByTarget(ctx, "web1"); err != nil {
				t.Fatalf("the agent was revoked: %v", err)
			}
			b, _ := e.st.GetBinding(ctx, state.KindTarget, "web1")
			if st, _ := e.drv.Observe(ctx, driver.GuestID(b.Ref)); st.Power != driver.PowerRunning {
				t.Fatalf("the guest was stopped: %s", st.Power)
			}
			if re, _ := e.st.PendingPlans(ctx, plan.OriginReenroll); len(re) != 0 {
				t.Fatalf("a re-enroll Plan after a refused restore: %v", re)
			}
		})
	}
}

// An adopted Target: the Run starts the restored guest and names the
// install command; no re-enroll Plan (D-392). A failed start names the
// same way back. An apply token never approves it.
func TestRestoreQAAdopted(t *testing.T) {
	for _, startFails := range []bool{false, true} {
		t.Run(map[bool]string{false: "start ok", true: "start fails"}[startFails], func(t *testing.T) {
			fr := &phasedRestorer{}
			e := newEnv(t, true, withInstall, func(c *plan.Config) { c.GuestRestore = fr; c.InstallID = "iid" })
			adm := e.admin()
			vmid, _ := e.adoptWithAgent(adm, "wiki")
			ctx := context.Background()
			ref := itoa(int64(vmid))
			if err := e.drv.Power(ctx, driver.GuestID(ref), driver.PowerStop); err != nil {
				t.Fatal(err)
			}
			fr.good = recordedVolume(t, e, "wiki")
			if startFails {
				fr.after = func() { e.drv.Fail(fake.OpPower, errors.New("start refused")) }
			}
			id := e.proposeRestore(adm, "wiki", fr.good.ID)
			if code, _ := e.token(TierApply, wire.RiskDisruptive).do("POST", "/api/v1/plans/"+itoa(id)+"/approve", nil); code != 403 {
				t.Fatalf("apply token approve: %d", code)
			}
			if code, body := adm.do("POST", "/api/v1/plans/"+itoa(id)+"/approve", nil); code != 202 {
				t.Fatalf("approve: %d %s", code, body)
			}
			e.eng.Wait()
			r, _ := e.st.RunOfPlan(ctx, id)
			steps, _ := e.st.Steps(ctx, r.ID)
			if len(steps) != 1 || !strings.Contains(steps[0].Output, "POST /targets/wiki/install-command") || strings.Contains(steps[0].Output, "reenroll") {
				t.Fatalf("run %s, steps %+v", r.Status, steps)
			}
			if (r.Status == "failed") != startFails {
				t.Fatalf("run status %s, start fails %v", r.Status, startFails)
			}
			if _, err := e.st.AgentByTarget(ctx, "wiki"); !errors.Is(err, store.ErrNotFound) {
				t.Fatalf("the agent of the adopted guest after the restore: %v", err)
			}
			if re, _ := e.st.PendingPlans(ctx, plan.OriginReenroll); len(re) != 0 {
				t.Fatalf("a re-enroll Plan for an adopted Target: %v", re)
			}
		})
	}
}

// failWrites makes writes to a table fail, through a second connection
// to the env's database (an SQLite trigger): the store has no fault hook.
func (e *env) failWrites(t *testing.T, op, table string) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+filepath.Join(e.dir, "plumb.db")+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	q := "CREATE TRIGGER qa_fail_" + strings.ToLower(op) + "_" + table + " BEFORE " + op + " ON " + table + " BEGIN SELECT RAISE(ABORT, 'qa: the write fails'); END"
	if _, err := db.Exec(q); err != nil {
		t.Fatal(err)
	}
}

// Every failure point after the revoke names the way back (review F1 of
// #335): the drop of the host key pins and the binding write after the
// restore.
func TestRestoreQAWayBackAfterRevoke(t *testing.T) {
	for _, tc := range []struct {
		name, want string
		setup      func(t *testing.T, e *env, fr *phasedRestorer)
		reenroll   int
	}{
		{"pins cannot be dropped", "the host key pins could not be dropped", func(t *testing.T, e *env, _ *phasedRestorer) {
			e.failWrites(t, "DELETE", "guest_host_key")
		}, 0},
		{"binding cannot be written", "restored, but the binding could not be written", func(t *testing.T, e *env, fr *phasedRestorer) {
			fr.after = func() { e.failWrites(t, "UPDATE", "binding") }
		}, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fr := &phasedRestorer{}
			e := newEnv(t, true, func(c *plan.Config) { c.GuestRestore = fr; c.InstallID = "iid" })
			adm := e.admin()
			e.seed(adm, "web1")
			e.approve(adm, "allow restart", restoreTarget("web1", true))
			ctx := context.Background()
			b, _ := e.st.GetBinding(ctx, state.KindTarget, "web1")
			if ok, err := e.st.PinFirstHostKeys(ctx, "web1", b.GuestKey(), []string{"SHA256:old"}, time.Now()); err != nil || !ok {
				t.Fatalf("pin: %v %v", ok, err)
			}
			fr.good = recordedVolume(t, e, "web1")
			id := e.proposeRestore(adm, "web1", fr.good.ID)
			tc.setup(t, e, fr)
			if code, body := adm.do("POST", "/api/v1/plans/"+itoa(id)+"/approve", nil); code != 202 {
				t.Fatalf("approve: %d %s", code, body)
			}
			e.eng.Wait()
			r, _ := e.st.RunOfPlan(ctx, id)
			steps, _ := e.st.Steps(ctx, r.ID)
			if r.Status != "failed" || len(steps) != 1 || !strings.Contains(steps[0].Output, tc.want) ||
				!strings.Contains(steps[0].Output, "The way back: re-enroll the Target (POST /targets/web1/reenroll)") {
				t.Fatalf("run %s, steps %+v", r.Status, steps)
			}
			if _, err := e.st.AgentByTarget(ctx, "web1"); !errors.Is(err, store.ErrNotFound) {
				t.Fatalf("the credential after a failure after the revoke: %v", err)
			}
			if re, _ := e.st.PendingPlans(ctx, plan.OriginReenroll); len(re) != tc.reenroll {
				t.Fatalf("re-enroll plans %d, want %d (restored disk: %v)", len(re), tc.reenroll, tc.reenroll == 1)
			}
		})
	}
}
