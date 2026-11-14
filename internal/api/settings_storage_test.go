package api

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestSettingsBackupStorage (T-208, host finding F1): proxmox.storages.backup
// is the storage of the host copy status file; null before its first run
// and for a bad file. The answer matches the contract.
func TestSettingsBackupStorage(t *testing.T) {
	k := loadContract(t)
	e := newEnv(t, true)
	e.srv.cfg.Proxmox = func(context.Context) *ProxmoxView {
		return &ProxmoxView{ACL: "ok", ACLLines: []ACLLineView{}}
	}
	adm := e.admin()
	storage := func() *string {
		t.Helper()
		rec := adm.raw("GET", "/api/v1/settings", nil)
		ct, _, _ := strings.Cut(rec.Header().Get("Content-Type"), ";")
		k.check(t, recorded{"GET", "/settings", rec.Code, ct, rec.Body.Bytes()})
		var v struct {
			Proxmox struct {
				Storages struct {
					Backup *string `json:"backup"`
				} `json:"storages"`
			} `json:"proxmox"`
		}
		if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &v) != nil {
			t.Fatalf("settings: %d %s", rec.Code, rec.Body.Bytes())
		}
		return v.Proxmox.Storages.Backup
	}
	if s := storage(); s != nil {
		t.Fatalf("before the first copy: %q", *s)
	}
	file := filepath.Join(e.dir, "copy-status.json")
	now := time.Now().UTC().Format(time.RFC3339)
	os.WriteFile(file, []byte(`{"v":1,"at":"`+now+`","result":"ok","name":"","sha256":"","storage":"plumb-backup","message":""}`), 0o644)
	if s := storage(); s == nil || *s != "plumb-backup" {
		t.Fatalf("after a copy: %v", s)
	}
	// A failed copy still names its storage.
	os.WriteFile(file, []byte(`{"v":1,"at":"`+now+`","result":"failed","name":"","sha256":"","storage":"nas","message":"not mounted"}`), 0o644)
	if s := storage(); s == nil || *s != "nas" {
		t.Fatalf("after a failed copy: %v", s)
	}
	os.WriteFile(file, []byte(`{"v":1,"storage":"x","extra":1}`), 0o644)
	if s := storage(); s != nil {
		t.Fatalf("bad status file: %q", *s)
	}
}
