package api

import (
	"bytes"
	"context"
	"testing"
)

// The install settings of slice 7 in GET /settings (T-218, D-401, D-405):
// nil lists are [] and both views match the contract.
func TestSettingsSlice7View(t *testing.T) {
	k := loadContract(t)
	e := newEnv(t, true)
	tag := 20
	views := []*ProxmoxView{
		{ACL: "ok", GuestBackup: &GuestBackupView{Storage: "plumb-backup", State: "clear-refused"}},
		{ACL: "missing", ACLLines: []ACLLineView{{Path: "/sdn/zones/localnetwork/vmbr9/20", Role: "PlumbBridge", OK: false}},
			ExtraNets:   []ExtraNetView{{Bridge: "vmbr9"}, {Bridge: "vmbr9", Tag: &tag}},
			GuestBackup: &GuestBackupView{Storage: "pbs1", State: "pbs-key", OtherBackupStorages: []string{"local"}, DiskContent: []string{"images"}},
			ACLExtra:    []ACLExtraView{{Path: "/pool/plumb", Role: "PlumbGuestBackupVM"}}},
	}
	wants := [][]string{
		{`"extra_nets":[]`, `"guest_backup":{"storage":"plumb-backup","state":"clear-refused","other_backup_storages":[],"disk_content":[]},"acl_extra":[]`},
		{`"extra_nets":[{"bridge":"vmbr9","tag":null},{"bridge":"vmbr9","tag":20}]`, `"other_backup_storages":["local"],"disk_content":["images"]`,
			`"acl_extra":[{"path":"/pool/plumb","role":"PlumbGuestBackupVM"}]`},
	}
	adm := e.admin()
	for i, v := range views {
		e.srv.cfg.Proxmox = func(context.Context) *ProxmoxView { return v }
		rec := adm.raw("GET", "/api/v1/settings", nil)
		k.check(t, recorded{"GET", "/settings", rec.Code, "application/json", rec.Body.Bytes()})
		for _, w := range wants[i] {
			if rec.Code != 200 || !bytes.Contains(rec.Body.Bytes(), []byte(w)) {
				t.Errorf("view %d: %d %s\nwithout %s", i, rec.Code, rec.Body, w)
			}
		}
	}
	// serve --dev: no guest backup storage.
	e.srv.cfg.Proxmox = nil
	rec := adm.raw("GET", "/api/v1/settings", nil)
	k.check(t, recorded{"GET", "/settings", rec.Code, "application/json", rec.Body.Bytes()})
	if !bytes.Contains(rec.Body.Bytes(), []byte(`"extra_nets":[],"guest_backup":null,"acl_extra":[]`)) {
		t.Errorf("dev view: %s", rec.Body)
	}
}
