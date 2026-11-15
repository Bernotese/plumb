package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Bernotese/plumb/internal/backup"
	"github.com/Bernotese/plumb/internal/plan"
	"github.com/Bernotese/plumb/internal/state"
	"github.com/Bernotese/plumb/internal/store"
)

// apiGuestDriver is an in-memory Proxmox backup storage for the API tests.
type apiGuestDriver struct {
	mu   sync.Mutex
	vols []backup.Volume
	seq  int
}

func (d *apiGuestDriver) BackUpGuest(_ context.Context, storage, guest, notes string, _ []string) (string, bool, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.seq++
	v := backup.Volume{Volid: fmt.Sprintf("%s:backup/vzdump-qemu-%s-%d.vma.zst", storage, guest, d.seq), Time: time.Now().Add(time.Duration(d.seq) * time.Second), Notes: notes, Size: 42}
	d.vols = append(d.vols, v)
	return v.Volid, false, nil
}

func (d *apiGuestDriver) ListGuestBackups(context.Context, string, string) ([]backup.Volume, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]backup.Volume(nil), d.vols...), nil
}

func (d *apiGuestDriver) DeleteGuestBackup(_ context.Context, _, _, volid string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	for i, v := range d.vols {
		if v.Volid == volid {
			d.vols = append(d.vols[:i], d.vols[i+1:]...)
			return nil
		}
	}
	return errors.New("gone")
}

func (d *apiGuestDriver) BackupStorageState(context.Context, string) (backup.StorageState, error) {
	return backup.StorageState{Encrypted: true, FreePct: 70}, nil
}

func withGuestBackups(t *testing.T, e *env) (*backup.Guests, *apiGuestDriver) {
	t.Helper()
	if err := e.st.PutBinding(e.ctx, store.Binding{Kind: "Target", Name: "db1", Ref: "9001"}); err != nil {
		t.Fatal(err)
	}
	d := &apiGuestDriver{}
	target := func(ctx context.Context, name string) (backup.GuestTarget, error) {
		b, err := e.st.GetBinding(ctx, "Target", name)
		if err != nil {
			return backup.GuestTarget{}, err
		}
		return backup.GuestTarget{Name: name, Guest: b.Ref, Gen: b.Gen, Backup: &backup.GuestSchedule{Schedule: "daily", Hour: 3, Keep: 7}}, nil
	}
	g := backup.NewGuests(backup.GuestConfig{Store: e.st, Events: e.events, Driver: d, InstallID: "6f1c2a9e-3b4d-4e5f-8a7b-0c1d2e3f4a5b",
		Storage: "plumb-backup", Target: target,
		Targets: func(context.Context) ([]backup.GuestTarget, error) { return nil, nil }})
	t.Cleanup(g.Close)
	e.srv.cfg.GuestBackups = g
	return g, d
}

// T-221: the routes against the contract; a token reads no notes and no
// volume id; delete is for a human admin session only (D-409).
func TestGuestBackupRoutes(t *testing.T) {
	e := newEnv(t, true)
	adm := e.admin()
	g, d := withGuestBackups(t, e)
	rc := &recorder{t: t}
	read := e.token(TierRead, "")
	if code, body := rc.do(read, "GET", "/api/v1/targets/db1/backups", "/targets/{name}/backups", nil); code != 200 || !strings.Contains(string(body), `"items":[]`) {
		t.Fatalf("empty list: %d %s", code, body)
	}
	if code, _ := rc.do(e.token(TierPlan, ""), "POST", "/api/v1/targets/db1/backups", "/targets/{name}/backups", nil); code != 403 {
		t.Fatalf("plan token back up now: %d", code)
	}
	apply := e.token(TierApply, "disruptive")
	if code, body := rc.do(apply, "POST", "/api/v1/targets/db1/backups", "/targets/{name}/backups", map[string]any{"reason": "before the update"}); code != 202 {
		t.Fatalf("apply token back up now: %d %s", code, body)
	}
	g.Wait()
	d.vols = append(d.vols, backup.Volume{Volid: "plumb-backup:backup/vzdump-qemu-9001-person.vma.zst", Time: time.Now(), Notes: "my own"})
	code, body := rc.do(read, "GET", "/api/v1/targets/db1/backups", "/targets/{name}/backups", nil)
	if code != 200 {
		t.Fatalf("list: %d %s", code, body)
	}
	if strings.Contains(string(body), "vzdump") || strings.Contains(string(body), "plumb:") || strings.Contains(string(body), "plumb-backup:") {
		t.Fatalf("a volume id or the marker leaks: %s", body)
	}
	var l struct {
		Storage struct{ State string }
		Items   []struct{ ID, Kind, Trigger string }
		Last    struct{ Result string }
	}
	json.Unmarshal(body, &l)
	if l.Storage.State != "pbs-key" || len(l.Items) != 2 || l.Last.Result != "done" {
		t.Fatalf("list %s", body)
	}
	var mine, person string
	for _, it := range l.Items {
		switch it.Kind {
		case "plumb":
			mine = it.ID
			if it.Trigger != "manual" {
				t.Fatalf("trigger %s", it.Trigger)
			}
		case "unmarked":
			person = it.ID
		}
	}
	// Delete: no token, only an admin session; never an unmarked volume.
	if code, _ := rc.do(e.token(TierApply, "disruptive"), "DELETE", "/api/v1/targets/db1/backups/"+mine, "/targets/{name}/backups/{backup}", nil); code != 403 {
		t.Fatalf("token delete: %d", code)
	}
	if code, body := rc.do(adm, "DELETE", "/api/v1/targets/db1/backups/"+person, "/targets/{name}/backups/{backup}", nil); code != 409 || problemType(body) != "not-plumb" {
		t.Fatalf("unmarked delete: %d %s", code, body)
	}
	if code, _ := rc.do(adm, "DELETE", "/api/v1/targets/db1/backups/"+mine, "/targets/{name}/backups/{backup}", nil); code != 204 {
		t.Fatalf("admin delete: %d", code)
	}
	if code, _ := rc.do(adm, "DELETE", "/api/v1/targets/db1/backups/"+mine, "/targets/{name}/backups/{backup}", nil); code != 404 {
		t.Fatalf("second delete: %d", code)
	}
	if code, _ := rc.do(read, "GET", "/api/v1/targets/nope/backups", "/targets/{name}/backups", nil); code != 404 {
		t.Fatalf("unknown Target: %d", code)
	}
}

func targetWithBackup(name string, keep int) state.Doc {
	return doc(state.KindTarget, name, `{"type":"vm","image":"ubuntu","guest":{"cores":2,"memory":1024,"disk":10,"bridge":"vmbr0"},`+
		`"blocks":[{"use":"motd","params":{"text":"hello"}}],"backup":{"schedule":"daily","time":"03:00","keep":`+itoa(int64(keep))+`}}`)
}

// T-221 part B (D-406, D-417): the backup field in a Plan. A new field is
// read risk with the storage note; a lower keep is destructive with the
// count, so a token cannot approve it; removing the field deletes nothing.
func TestBackupFieldPlans(t *testing.T) {
	e := newEnv(t, true, func(c *plan.Config) {
		// 6 recorded scheduled backups exist: keep k deletes 6+1-k (at most 6).
		c.BackupKeepDeletes = func(_ context.Context, _ string, keep int) (int, error) { return min(max(7-keep, 0), 6), nil }
		c.BackupStorageNote = func(context.Context) (string, error) { return "the storage keeps backups in clear", nil }
	})
	adm := e.admin()
	e.seed(adm, "web1")
	type planOut struct {
		ID       int64
		Risk     string
		Warnings []string
	}
	propose := func(d state.Doc) planOut {
		t.Helper()
		code, body := adm.do("POST", "/api/v1/plans", map[string]any{"reason": "backup", "put": []state.Doc{d}})
		var p planOut
		json.Unmarshal(body, &p)
		if code != 201 {
			t.Fatalf("propose: %d %s", code, body)
		}
		return p
	}
	p := propose(targetWithBackup("web1", 7))
	if p.Risk != "read" || !strings.Contains(strings.Join(p.Warnings, " "), "clear") {
		t.Fatalf("new field: %+v", p)
	}
	if code, body := adm.do("POST", "/api/v1/plans/"+itoa(p.ID)+"/approve", nil); code != 202 {
		t.Fatalf("approve: %d %s", code, body)
	}
	e.eng.Wait()
	p = propose(targetWithBackup("web1", 3))
	if p.Risk != "destructive" || !strings.Contains(strings.Join(p.Warnings, " "), "deletes 4 backup") {
		t.Fatalf("lower keep: %+v", p)
	}
	if code, _ := e.token(TierApply, "disruptive").do("POST", "/api/v1/plans/"+itoa(p.ID)+"/approve", nil); code != 403 {
		t.Fatalf("a token approved a destructive keep: %d", code)
	}
	adm.do("DELETE", "/api/v1/plans/"+itoa(p.ID), nil)
	// A new Target with the field: the create Plan carries the note too.
	if p := propose(targetWithBackup("web9", 3)); !strings.Contains(strings.Join(p.Warnings, " "), "clear") {
		t.Fatalf("new Target: %+v", p)
	} else {
		adm.do("DELETE", "/api/v1/plans/"+itoa(p.ID), nil)
	}
	p = propose(targetDoc("web1", "hello"))
	if p.Risk != "read" || !strings.Contains(strings.Join(p.Warnings, " "), "deletes none") {
		t.Fatalf("removed: %+v", p)
	}
}

// M1 of #334: a token removes the field (read, it may approve that), then
// adds it again with keep 1: destructive by the count, the token gets 403.
func TestBackupFieldRemoveThenReAdd(t *testing.T) {
	e := newEnv(t, true, func(c *plan.Config) {
		c.BackupKeepDeletes = func(_ context.Context, _ string, keep int) (int, error) { return min(max(7-keep, 0), 6), nil }
	})
	adm := e.admin()
	e.seed(adm, "web1")
	planTok, applyTok := e.token(TierPlan, ""), e.token(TierApply, "disruptive")
	run := func(c *client, d state.Doc) (int64, string) {
		t.Helper()
		code, body := c.do("POST", "/api/v1/plans", map[string]any{"reason": "backup", "put": []state.Doc{d}})
		var p struct {
			ID   int64
			Risk string
		}
		json.Unmarshal(body, &p)
		if code != 201 {
			t.Fatalf("propose: %d %s", code, body)
		}
		return p.ID, p.Risk
	}
	id, _ := run(adm, targetWithBackup("web1", 7))
	adm.do("POST", "/api/v1/plans/"+itoa(id)+"/approve", nil)
	e.eng.Wait()
	id, risk := run(planTok, targetDoc("web1", "hello")) // remove the field
	if risk != "read" {
		t.Fatalf("removal risk %s", risk)
	}
	if code, body := applyTok.do("POST", "/api/v1/plans/"+itoa(id)+"/approve", nil); code != 202 {
		t.Fatalf("token approves the removal: %d %s", code, body)
	}
	e.eng.Wait()
	id, risk = run(planTok, targetWithBackup("web1", 1)) // add it again with keep 1
	if risk != "destructive" {
		t.Fatalf("re-added with keep 1: risk %s", risk)
	}
	if code, _ := applyTok.do("POST", "/api/v1/plans/"+itoa(id)+"/approve", nil); code != 403 {
		t.Fatalf("a token approved the re-add: %d", code)
	}
}

// T-233 (host observation S1 of T-225): at the keep limit, a Plan that only
// moves the backup time causes no delete (the old field deletes the same
// backups at its next slot): read, and a token may approve it.
func TestBackupFieldTimeOnlyAtTheLimit(t *testing.T) {
	e := newEnv(t, true, func(c *plan.Config) {
		c.BackupKeepDeletes = func(_ context.Context, _ string, keep int) (int, error) { return min(max(7-keep, 0), 6), nil }
	})
	adm := e.admin()
	e.seed(adm, "web1")
	propose := func(d state.Doc) (int64, string) {
		t.Helper()
		code, body := adm.do("POST", "/api/v1/plans", map[string]any{"reason": "backup", "put": []state.Doc{d}})
		var p struct {
			ID   int64
			Risk string
		}
		json.Unmarshal(body, &p)
		if code != 201 {
			t.Fatalf("propose: %d %s", code, body)
		}
		return p.ID, p.Risk
	}
	id, _ := propose(targetWithBackup("web1", 3)) // the next job deletes 4
	adm.do("POST", "/api/v1/plans/"+itoa(id)+"/approve", nil)
	e.eng.Wait()
	later := doc(state.KindTarget, "web1", strings.Replace(string(targetWithBackup("web1", 3).Spec), `"03:00"`, `"04:30"`, 1))
	if !strings.Contains(string(later.Spec), `"04:30"`) {
		t.Fatal("the time did not change")
	}
	id, risk := propose(later)
	if risk != "read" {
		t.Fatalf("time only at the limit: risk %s", risk)
	}
	if code, body := e.token(TierApply, "disruptive").do("POST", "/api/v1/plans/"+itoa(id)+"/approve", nil); code != 202 {
		t.Fatalf("a token may approve a time change: %d %s", code, body)
	}
}
