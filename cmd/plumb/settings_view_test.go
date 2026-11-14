package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Bernotese/plumb/internal/driver/proxmox"
)

// proxmoxView: empty values are null, the backup storage is null, the
// token name is never there, acl_lines is never null.
func TestProxmoxViewMapping(t *testing.T) {
	b, _ := json.Marshal(proxmoxView(proxmox.View{ACL: proxmox.ACLUnknown, ACLError: "cannot reach Proxmox"}))
	want := `{"node":null,"pool":null,"storages":{"images":null,"snippets":null,"backup":null},"api":{"host":null,"port":null,"tls_sha256":null},` +
		`"version":null,"token_user":null,"last_ok_at":null,"acl":"unknown","acl_error":"cannot reach Proxmox","acl_lines":[]}`
	if string(b) != want {
		t.Errorf("empty view:\n%s\nwant\n%s", b, want)
	}
	at := time.Date(2026, 11, 14, 9, 0, 0, 0, time.UTC)
	v := proxmoxView(proxmox.View{Node: "pve", Pool: "plumb", ImageStorage: "plumb-images", SnippetStorage: "plumb-snippets",
		APIHost: "pve.home.arpa", APIPort: 8006, TLSSHA256: "5d:1f", Version: "9.2.26", TokenUser: proxmox.TokenUser("plumb@pve!plumb"),
		LastOK: at, ACL: proxmox.ACLOK, ACLLines: []proxmox.ACLLine{{Path: "/", Role: "PlumbAudit", OK: true}}})
	b, _ = json.Marshal(v)
	if strings.Contains(string(b), "!plumb") || *v.TokenUser != "plumb@pve" || *v.API.Port != 8006 || !v.LastOKAt.Equal(at) ||
		v.Storages.Backup != nil || len(v.ACLLines) != 1 {
		t.Errorf("view: %s", b)
	}
}
