package api

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Bernotese/plumb/internal/backup"
	"github.com/Bernotese/plumb/internal/store"
	"github.com/Bernotese/plumb/internal/wire"
)

// GET and POST /backups (T-112): tiers, 202, backup-running, the list
// and the copy state, every answer checked against the contract.
func TestBackupsAPI(t *testing.T) {
	e := newEnv(t, true)
	adm := e.admin()
	rc := &recorder{t: t}
	reader := e.token(TierRead, "")
	planner := e.token(TierPlan, "")
	applier := e.token(TierApply, wire.RiskReversible)

	code, body := rc.do(reader, "GET", "/api/v1/backups", "/backups", nil)
	if code != 200 || !strings.Contains(string(body), `"schedule":{"time":"03:00","keep":7}`) ||
		!strings.Contains(string(body), `"last_run":null`) || !strings.Contains(string(body), `"items":[]`) ||
		!strings.Contains(string(body), `"copy":{"state":"none","at":null,"name":null,"storage":null,"message":""}`) {
		t.Fatalf("empty list: %d %s", code, body)
	}
	for _, c := range []*client{reader, planner, e.anon()} {
		if code, body := rc.do(c, "POST", "/api/v1/backups", "/backups", nil); code != 403 && code != 401 {
			t.Errorf("POST by a low tier: %d %s", code, body)
		}
	}
	code, body = rc.do(applier, "POST", "/api/v1/backups", "/backups", nil)
	if code != 202 || !strings.Contains(string(body), `"result":"running"`) || !strings.Contains(string(body), `"name":null`) {
		t.Fatalf("POST: %d %s", code, body)
	}
	e.backups.Wait()
	code, body = rc.do(adm, "POST", "/api/v1/backups", "/backups", nil)
	if code != 202 {
		t.Fatalf("second POST: %d %s", code, body)
	}
	e.backups.Wait()

	os.WriteFile(filepath.Join(e.dir, "copy-status.json"), []byte(`{"v":1,"at":"`+time.Now().UTC().Format(time.RFC3339)+
		`","result":"failed","name":"","sha256":"","storage":"nas","message":"storage nas is not mounted"}`), 0o644)
	code, body = rc.do(reader, "GET", "/api/v1/backups", "/backups", nil)
	var v struct {
		Running bool `json:"running"`
		LastRun struct {
			Result string  `json:"result"`
			By     string  `json:"by"`
			Name   *string `json:"name"`
		} `json:"last_run"`
		Items []struct {
			Name   string `json:"name"`
			Size   int64  `json:"size"`
			SHA256 string `json:"sha256"`
		} `json:"items"`
		Copy struct {
			State   string `json:"state"`
			Message string `json:"message"`
		} `json:"copy"`
	}
	json.Unmarshal(body, &v)
	if code != 200 || v.Running || v.LastRun.Result != "ok" || v.LastRun.By != "user:admin" || v.LastRun.Name == nil ||
		len(v.Items) != 2 || v.Items[0].Name != *v.LastRun.Name || v.Items[0].Size == 0 || len(v.Items[0].SHA256) != 64 ||
		v.Copy.State != "failed" || v.Copy.Message != "storage nas is not mounted" {
		t.Fatalf("list: %d %s", code, body)
	}
	// While one runs: 409 backup-running.
	if _, err := e.st.StartBackupRun(e.ctx, "system", store.BackupSchedule, "plumb-20990101T000000Z.pbk"); err != nil {
		t.Fatal(err)
	}
	code, body = rc.do(adm, "POST", "/api/v1/backups", "/backups", nil)
	if code != 409 || problemType(body) != "backup-running" {
		t.Errorf("while running: %d %s", code, body)
	}
	if _, body := rc.do(reader, "GET", "/api/v1/backups", "/backups", nil); !strings.Contains(string(body), `"running":true`) {
		t.Errorf("running: %s", body)
	}
	// The events say the end; the file names this install.
	evs, _ := e.st.EventsAfter(e.ctx, 0, 1000)
	n := 0
	for _, ev := range evs {
		if ev.Type == "backup.finished" && strings.HasPrefix(ev.Subject, "backup:plumb-") {
			n++
		}
	}
	if n != 2 {
		t.Errorf("backup.finished events: %d", n)
	}
	f, err := os.Open(filepath.Join(e.dir, backup.DirName, v.Items[0].Name))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	h, m, err := backup.Read(f, e.key, nil)
	if err != nil || h.InstallID != "install-test" || m.Sizes[backup.EntryCAKey] == 0 {
		t.Errorf("file: %+v %+v %v", h, m, err)
	}
	rc.check()
}
