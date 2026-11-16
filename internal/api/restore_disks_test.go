package api

// T-238 (F3 (a) of #359, T-244 D9): a restore Plan names the data disks
// that the backup lacks, by type.

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Bernotese/plumb/internal/plan"
	"github.com/Bernotese/plumb/internal/state"
)

// diskRestorer is a fakeRestorer that also reads a backup's config.
type diskRestorer struct {
	*fakeRestorer
	cfg map[string]string
	err error
}

func (d *diskRestorer) BackupConfig(context.Context, string, plan.RestoreVolume) (map[string]string, error) {
	return d.cfg, d.err
}

func TestRestorePlanNamesMissingDisks(t *testing.T) {
	dr := &diskRestorer{fakeRestorer: &fakeRestorer{vols: map[string]plan.RestoreVolume{}}, cfg: map[string]string{"scsi0": "x", "scsi1": "y"}}
	e := newEnv(t, true, func(c *plan.Config) { c.GuestRestore = dr; c.InstallID = "iid" })
	adm := e.admin()
	e.seed(adm, "web1")
	e.diskRun(adm, diskTarget("web1", `[{"slot":1,"size_gb":1},{"slot":3,"size_gb":2}]`, true))
	ctx := context.Background()
	b, _ := e.st.GetBinding(ctx, state.KindTarget, "web1")
	lineage, _ := e.st.BackupLineage(ctx, "web1", b.Gen)
	at := time.Date(2026, 11, 15, 3, 0, 12, 0, time.UTC)
	run, _ := e.st.StartGuestBackupRun(ctx, "web1", lineage, "scheduled")
	e.st.FinishGuestBackupRun(ctx, run, "done", "bk:v1", 100, at.Unix(), "")
	dr.vols["3f2a9c0d1e4b5a67"] = plan.RestoreVolume{ID: "3f2a9c0d1e4b5a67", Volid: "bk:v1", Kind: "plumb", Notes: "plumb:iid:web1:" + lineage, Time: at, Size: 100}

	code, body := adm.do("POST", "/api/v1/targets/web1/restore", map[string]any{"backup": "3f2a9c0d1e4b5a67", "reason": "older"})
	if code != 201 {
		t.Fatalf("restore plan: %d %s", code, body)
	}
	var p diskPlan
	json.Unmarshal(body, &p)
	if _, to, risk, ok := p.change("disk3"); !ok || to != "detached: not in the backup" || risk != "destructive" {
		t.Errorf("disk3: %s", body)
	}
	if _, _, _, ok := p.change("disk1"); ok {
		t.Errorf("disk1 is in the backup: %s", body)
	}
	if w := strings.Join(p.Warnings, " "); !strings.Contains(w, "Disk 3 (fake:vm-") || !strings.Contains(w, "kept as an unused volume") {
		t.Errorf("warnings %q", p.Warnings)
	}
	// The Plan still runs: the extra change is a valid restore Plan.
	if code, body := adm.do("POST", "/api/v1/plans/"+itoa(p.ID)+"/approve", nil); code != 202 {
		t.Fatalf("approve: %d %s", code, body)
	}
	e.eng.Wait()
	if len(dr.restored) != 1 {
		t.Errorf("restored %v", dr.restored)
	}
}
