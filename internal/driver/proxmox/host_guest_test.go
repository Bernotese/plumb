//go:build pve

// Step-wise real-host checks of the guest side (T-012 items 4, 9, 10, 14,
// 17–20). Each step is one run of the test binary; the operator checks the
// guest over SSH between the steps. PLUMB_PVE_STEP picks the step:
//
//	create    FetchImage + Create with PLUMB_PVE_SSH_PUBKEY and token 1;
//	          prints VMID and MAC
//	observe   Observe PLUMB_PVE_VMID; prints power, IPs, digest
//	reenroll  Update with the token PLUMB_PVE_ENROLL (a test string), then
//	          a cold restart (Power restart; not with PLUMB_PVE_NORESTART=1)
//	cifield   change the cloud-init field searchdomain, then a cold restart
//	restart   a cold restart (Power restart) only
//	missing   a VMID in the range that is not ours; prints the raw answer
//	tasklog   prints the log of the latest task of PLUMB_PVE_VMID
//	mismatch  FetchImage with a wrong SHA-256 must fail
//	delete    Delete PLUMB_PVE_VMID and remove the test template
//
// The test tokens are test strings, never real enrollment tokens.
package proxmox

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Bernotese/plumb/internal/driver"
	"github.com/Bernotese/plumb/internal/wire"
)

func hostImage() driver.ImageSpec {
	return driver.ImageSpec{Name: "test-image", Kind: wire.KindVM,
		URL: os.Getenv("PLUMB_PVE_IMAGE_URL"), SHA256: os.Getenv("PLUMB_PVE_IMAGE_SHA256")}
}

func stepVMID(t *testing.T) driver.GuestID {
	t.Helper()
	v := os.Getenv("PLUMB_PVE_VMID")
	if v == "" {
		t.Fatal("PLUMB_PVE_VMID is not set")
	}
	return driver.GuestID(v)
}

func TestHostGuestStep(t *testing.T) {
	step := os.Getenv("PLUMB_PVE_STEP")
	if step == "" {
		t.Skip("PLUMB_PVE_STEP is not set")
	}
	d := hostDriver(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	switch step {
	case "create":
		key := os.Getenv("PLUMB_PVE_SSH_PUBKEY")
		if !strings.HasPrefix(key, "ssh-") {
			t.Fatal("PLUMB_PVE_SSH_PUBKEY is not a public key")
		}
		ref, err := d.FetchImage(ctx, hostImage())
		if err != nil {
			t.Fatal(err)
		}
		spec := driver.GuestSpec{Name: "plumb-host-guest", Kind: wire.KindVM, ImageName: hostImage().Name, Image: ref, Cores: 1, MemoryMB: 1024, DiskGB: 8,
			Bridge: os.Getenv("PLUMB_PVE_BRIDGE"), IP: "dhcp", User: "plumb", SSHKeys: []string{key}}
		id, err := d.Create(ctx, spec, driver.Bootstrap{Token: "host-test-token-1"})
		if err != nil {
			t.Fatal(err)
		}
		n, _ := strconv.Atoi(string(id))
		cfg, err := d.config(ctx, n)
		if err != nil {
			t.Fatal(err)
		}
		fmt.Printf("VMID=%s\nTEMPLATE=%s\nNET0=%s\n", id, ref, cfg["net0"])
	case "observe":
		st, err := d.Observe(ctx, stepVMID(t))
		if err != nil {
			t.Fatal(err)
		}
		fmt.Printf("POWER=%s\nIPS=%v\nDIGEST=%s\nCORES=%d MEMORY=%d DISK=%d BRIDGE=%s IP=%s USER=%s KEYS=%d\n",
			st.Power, st.IPs, st.Digest, st.Spec.Cores, st.Spec.MemoryMB, st.Spec.DiskGB, st.Spec.Bridge, st.Spec.IP, st.Spec.User, len(st.Spec.SSHKeys))
	case "reenroll":
		id := stepVMID(t)
		tok := os.Getenv("PLUMB_PVE_ENROLL")
		if !strings.HasPrefix(tok, "host-test-token-") {
			t.Fatal("PLUMB_PVE_ENROLL must be a test string host-test-token-N")
		}
		n, _ := strconv.Atoi(string(id))
		before, _ := d.config(ctx, n)
		change := []wire.Change{{Attr: driver.AttrEnrollToken, Sensitive: true, Risk: wire.RiskDisruptive}}
		if err := d.Update(ctx, id, change, &driver.Bootstrap{Token: tok}); err != nil {
			t.Fatal(err)
		}
		after, _ := d.config(ctx, n)
		fmt.Printf("UUID_KEPT=%v\n", smbiosField(before["smbios1"], "uuid") == smbiosField(after["smbios1"], "uuid") && smbiosField(after["smbios1"], "uuid") != "")
		if os.Getenv("PLUMB_PVE_NORESTART") == "1" {
			return // item 18: a warm reboot in the guest must not apply it
		}
		if err := d.Power(ctx, id, driver.PowerRestart); err != nil {
			t.Fatal(err)
		}
	case "cifield":
		id := stepVMID(t)
		// Like every change: first check that the guest is ours (review N3).
		n, _, err := d.own(ctx, string(id))
		if err != nil {
			t.Fatal(err)
		}
		if err := d.c.MaybeTask(ctx, d.cfg.Node, "POST", vmPath(d, n)+"/config", url.Values{"searchdomain": {"plumb.test"}}); err != nil {
			t.Fatal(err)
		}
		if err := d.Power(ctx, id, driver.PowerRestart); err != nil {
			t.Fatal(err)
		}
	case "restart":
		if err := d.Power(ctx, stepVMID(t), driver.PowerRestart); err != nil {
			t.Fatal(err)
		}
	case "missing":
		_, err := d.Observe(ctx, driver.GuestID(strconv.Itoa(d.cfg.VMIDMax)))
		fmt.Printf("OBSERVE_FOREIGN_ERR=%v NOTFOUND=%v\n", err, errors.Is(err, driver.ErrNotFound))
		var raw any
		err = d.c.Get(ctx, vmPath(d, d.cfg.VMIDMax)+"/config", nil, &raw)
		var ae *APIError
		if errors.As(err, &ae) {
			fmt.Printf("RAW_MISSING status=%d msg=%q\n", ae.Status, ae.Error())
		} else {
			fmt.Printf("RAW_MISSING err=%v\n", err)
		}
	case "tasklog":
		id := stepVMID(t)
		var tasks []struct {
			UPID string `json:"upid"`
			Type string `json:"type"`
		}
		if err := d.c.Get(ctx, d.node()+"/tasks", url.Values{"vmid": {string(id)}, "limit": {"1"}}, &tasks); err != nil {
			t.Fatal(err)
		}
		if len(tasks) == 0 {
			t.Fatal("no task")
		}
		var lines []struct {
			N int    `json:"n"`
			T string `json:"t"`
		}
		if err := d.c.Get(ctx, d.node()+"/tasks/"+url.PathEscape(tasks[0].UPID)+"/log", url.Values{"start": {"0"}, "limit": {"500"}}, &lines); err != nil {
			t.Fatal(err)
		}
		fmt.Printf("TASK=%s LINES=%d FIRST=%q\n", tasks[0].Type, len(lines), first(lines))
	case "mismatch":
		img := hostImage()
		img.SHA256 = strings.Repeat("0", 64)
		_, err := d.FetchImage(ctx, img)
		fmt.Printf("MISMATCH_ERR=%v\n", err)
		if err == nil {
			t.Fatal("a wrong SHA-256 was accepted")
		}
	case "delete":
		if v := os.Getenv("PLUMB_PVE_VMID"); v != "" {
			if err := d.Delete(ctx, driver.GuestID(v)); err != nil {
				t.Errorf("delete %s: %v", v, err)
			}
		}
		ms, err := d.members(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range ms {
			if m.Name != templateName(hostImage()) {
				continue
			}
			if id, mm, err := d.own(ctx, strconv.Itoa(m.VMID)); err == nil && mm.Template == 1 {
				if err := d.c.Task(ctx, d.cfg.Node, "DELETE", vmPath(d, id), url.Values{"purge": {"1"}}); err != nil {
					t.Errorf("remove template %d: %v", id, err)
				}
			}
		}
	default:
		t.Fatalf("unknown step %q", step)
	}
}

func first(lines []struct {
	N int    `json:"n"`
	T string `json:"t"`
}) string {
	if len(lines) == 0 {
		return ""
	}
	return lines[0].T
}

// smbiosField returns one field of an smbios1 value.
func smbiosField(v, key string) string {
	for _, p := range strings.Split(v, ",") {
		if k, val, ok := strings.Cut(p, "="); ok && k == key {
			return val
		}
	}
	return ""
}
