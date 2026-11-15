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
