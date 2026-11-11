//go:build pve

package pve_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestUbuntuStaticSecondBoot records one fact for the text of D-192 (pl-lead,
// 2026-11-11): does a second boot of a static Ubuntu 26.04 guest apply the
// static address? Opt-in: PLUMB_TEST_UBUNTU_SECOND_BOOT=1. It records the
// answer and fails only on a product or test error, not on "no".
func TestUbuntuStaticSecondBoot(t *testing.T) {
	if os.Getenv("PLUMB_TEST_UBUNTU_SECOND_BOOT") != "1" {
		t.Skip("opt-in: PLUMB_TEST_UBUNTU_SECOND_BOOT=1")
	}
	a := settings(t)
	if os.Getenv("PLUMB_TEST_VM_IP_2") == "" {
		t.Skip("PLUMB_TEST_VM_IP_2 is not set")
	}
	before := snapshot(t)
	importsBefore := importFiles(t)
	prefix := fmt.Sprintf("t015-%d-9", time.Now().UnixNano())
	image, target := prefix+"-img", prefix+"-vm"
	sha := os.Getenv("PLUMB_TEST_IMAGE_UBUNTU2604_SHA256")
	expected := map[string]bool{"plumb-img-" + image + "-" + sha[:12]: true}
	imports := map[string]bool{"plumb-" + image + "-" + sha[:12] + ".qcow2": true}
	refs := []any{object{"kind": "Image", "name": image}, object{"kind": "Target", "name": target}}
	owned := map[int]bool{}
	t.Cleanup(func() {
		cleanup(a, before, expected, refs, owned)
		freeImports(t, importsBefore, imports)
		checkImports(t, importsBefore, map[string]bool{})
	})
	key, err := os.ReadFile(os.Getenv("PLUMB_TEST_GUEST_KEY") + ".pub")
	check(t, err, "read guest public key")
	pub := strings.TrimSpace(string(key))
	address := os.Getenv("PLUMB_TEST_VM_IP_2")
	docs := []any{
		object{"kind": "Image", "name": image, "spec": object{"type": "vm", "url": os.Getenv("PLUMB_TEST_IMAGE_UBUNTU2604_URL"), "sha256": sha}},
		object{"kind": "Target", "name": target, "spec": object{"type": "vm", "image": image,
			"guest": guestSpec(address, os.Getenv("PLUMB_TEST_VM_GW"), pub)}},
	}
	start := time.Now()
	a.approve(a.call("POST", "/plans", object{"reason": "T-015 fact: static Ubuntu second boot", "put": docs}, 201), false)
	t.Logf("first boot until the agent is online: %s", time.Since(start).Round(time.Second))
	n := vmid(t, a.call("GET", "/targets/"+target, nil, 200)["status"].(map[string]any)["guest_id"].(string))
	if _, exists := before[n]; exists {
		t.Fatal("test guest reuses a preexisting VMID")
	}
	owned[n] = true
	g := guest{id: n, address: address, hosts: filepath.Join(t.TempDir(), "known_hosts")}
	_, first := guestSSH(t, g, "true")
	t.Logf("first boot: static address answers SSH: %v", first)
	if _, ok := hostSSH(t, owned, n, "reboot"); !ok {
		t.Fatal("host reboot failed (output withheld)")
	}
	second := false
	deadline := time.Now().Add(3 * time.Minute)
	for !second && time.Now().Before(deadline) {
		time.Sleep(5 * time.Second)
		_, second = guestSSH(t, g, "true")
	}
	t.Logf("FACT second boot applies the static address: %v", second)
	if !second {
		t.Logf("no SSH on the static address within 3 min after the reboot (class: %s)", sshFailureClass(g))
	}
}
