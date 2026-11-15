package api

// T-226 (slice 7 QA, row C10): the refusals of "back up now" (T-221 part
// B, D-303, D-406, D-408) through the API, and the proposal rate.

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/Bernotese/plumb/internal/backup"
	"github.com/Bernotese/plumb/internal/store"
)

// qaGuestDriver is apiGuestDriver with the storage state as knobs and a
// vzdump that can wait.
type qaGuestDriver struct {
	apiGuestDriver
	mu    sync.Mutex
	enc   bool
	free  int
	hold  chan struct{} // non-nil: BackUpGuest waits for it
	start chan struct{} // closed when a held vzdump started
}

func (d *qaGuestDriver) BackUpGuest(ctx context.Context, storage, guest, notes string, ex []string, onTask func(string)) (string, bool, error) {
	d.mu.Lock()
	hold, start := d.hold, d.start
	d.mu.Unlock()
	if hold != nil {
		close(start)
		select {
		case <-hold:
		case <-ctx.Done():
			return "", false, ctx.Err()
		}
	}
	return d.apiGuestDriver.BackUpGuest(ctx, storage, guest, notes, ex, onTask)
}

func (d *qaGuestDriver) BackupStorageState(context.Context, string) (backup.StorageState, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return backup.StorageState{Encrypted: d.enc, FreePct: d.free}, nil
}

func qaGuestBackups(t *testing.T, e *env, clear bool) (*backup.Guests, *qaGuestDriver) {
	t.Helper()
	if err := e.st.PutBinding(e.ctx, store.Binding{Kind: "Target", Name: "db1", Ref: "9001"}); err != nil {
		t.Fatal(err)
	}
	d := &qaGuestDriver{enc: true, free: 70}
	target := func(ctx context.Context, name string) (backup.GuestTarget, error) {
		b, err := e.st.GetBinding(ctx, "Target", name)
		if err != nil {
			return backup.GuestTarget{}, err
		}
		return backup.GuestTarget{Name: name, Guest: b.Ref, Gen: b.Gen, Backup: &backup.GuestSchedule{Schedule: "daily", Hour: 3, Keep: 7}}, nil
	}
	g := backup.NewGuests(backup.GuestConfig{Store: e.st, Events: e.events, Driver: d, InstallID: "6f1c2a9e-3b4d-4e5f-8a7b-0c1d2e3f4a5b",
		Storage: "plumb-backup", Clear: clear, Target: target,
		Targets: func(context.Context) ([]backup.GuestTarget, error) { return nil, nil }})
	t.Cleanup(g.Close)
	e.srv.cfg.GuestBackups = g
	return g, d
}

func TestQABackUpNowRefusals(t *testing.T) {
	e := newEnv(t, true)
	g, d := qaGuestBackups(t, e, false)
	rc := &recorder{t: t}
	tok := e.token(TierApply, "disruptive")
	now := func() (int, []byte) {
		return rc.do(tok, "POST", "/api/v1/targets/db1/backups", "/targets/{name}/backups", nil)
	}
	for _, c := range []struct {
		name   string
		set    func()
		detail string
	}{
		{"clear storage without the flag", func() { d.enc = false }, "--guest-backup-clear"},
		{"less than 10 % free", func() { d.enc, d.free = true, 9 }, "9 % free"},
	} {
		d.mu.Lock()
		c.set()
		d.mu.Unlock()
		code, body := now()
		if code != 409 || problemType(body) != "backup-refused" || !strings.Contains(string(body), c.detail) {
			t.Errorf("%s: %d %s", c.name, code, body)
		}
	}
	// Each refusal is a row: the list shows it as the last result.
	if last, err := e.st.LastGuestBackupRun(e.ctx, "db1"); err != nil || last.Result != "refused" || !strings.Contains(last.Message, "% free") {
		t.Errorf("last run: %+v %v", last, err)
	}
	// A running job: the second call is 409 backup-running.
	d.mu.Lock()
	d.free, d.hold, d.start = 70, make(chan struct{}), make(chan struct{})
	hold, start := d.hold, d.start
	d.mu.Unlock()
	if code, body := now(); code != 202 {
		t.Fatalf("back up now: %d %s", code, body)
	}
	<-start
	if code, body := now(); code != 409 || problemType(body) != "backup-running" {
		t.Errorf("second call: %d %s", code, body)
	}
	close(hold)
	g.Wait()
	d.mu.Lock()
	d.hold = nil
	d.mu.Unlock()
	if code, body := now(); code != 202 {
		t.Errorf("after the job: %d %s", code, body)
	}
	g.Wait()
}

// --guest-backup-clear lets a clear storage run (D-406).
func TestQABackUpNowClearStated(t *testing.T) {
	e := newEnv(t, true)
	g, d := qaGuestBackups(t, e, true)
	d.enc = false
	if code, body := e.token(TierApply, "disruptive").do("POST", "/api/v1/targets/db1/backups", nil); code != 202 {
		t.Fatalf("clear stated: %d %s", code, body)
	}
	g.Wait()
	if last, err := e.st.LastGuestBackupRun(e.ctx, "db1"); err != nil || last.Result != "done" {
		t.Errorf("last run: %+v %v", last, err)
	}
}

// "Back up now" has the proposal rate of D-365: the 31st quick call of one
// token is 429, whatever the earlier ones answered.
func TestQABackUpNowRate(t *testing.T) {
	e := newEnv(t, true)
	g, _ := qaGuestBackups(t, e, false)
	tok := e.token(TierApply, "disruptive")
	codes := map[int]int{}
	for range 31 {
		code, _ := tok.do("POST", "/api/v1/targets/db1/backups", nil)
		codes[code]++
	}
	g.Wait()
	if codes[429] != 1 || codes[202]+codes[409] != 30 {
		t.Fatalf("codes %v", codes)
	}
}

// Restored mode: "back up now" is 409 restored-mode before any job check
// (D-303).
func TestQABackUpNowRestoredMode(t *testing.T) {
	e, _, _, _ := restoredEnv(t)
	qaGuestBackups(t, e, false)
	adm := e.login("admin", "restored-password-1")
	if adm == nil {
		t.Fatal("admin login")
	}
	adm.do("PUT", "/api/v1/session/password", map[string]string{"current_password": "restored-password-1", "new_password": "another-good-password"})
	if code, body := adm.do("POST", "/api/v1/targets/web1/backups", nil); code != 409 || problemType(body) != "restored-mode" {
		t.Fatalf("restored mode: %d %s", code, body)
	}
	if _, err := e.st.LastGuestBackupRun(e.ctx, "web1"); err == nil {
		t.Error("a job row in restored mode")
	}
}
