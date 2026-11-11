//go:build pve

package pve_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Bernotese/plumb/blocks"
	"go.yaml.in/yaml/v3"
)

func TestRealSliceAcceptance(t *testing.T) {
	a := settings(t)
	before := snapshot(t)
	t.Logf("guest/template list before: %v", inventory(before))
	expected := map[string]bool{}
	imports := map[string]bool{} // the import files this run may leave
	importsBefore := importFiles(t)
	refs := []any{}
	owned := map[int]bool{}
	t.Cleanup(func() {
		cleanup(a, before, expected, refs, owned)
		checkImports(a.t, importsBefore, imports)
	})
	for i, distro := range []string{"DEBIAN13", "UBUNTU2604"} {
		// PLUMB_TEST_DISTROS=UBUNTU2604 runs one distribution (diagnosis).
		if only := os.Getenv("PLUMB_TEST_DISTROS"); only != "" && !strings.Contains(only, distro) {
			continue
		}
		ok := t.Run(distro, func(t *testing.T) {
			c := *a
			c.t = t
			prefix := fmt.Sprintf("t015-%d-%d", time.Now().UnixNano(), i)
			// The Image name is stable per distribution, so its import file
			// (plumb-<name>-<sha256[:12]>.qcow2, D-077) is reused by a later
			// run and not downloaded again (pl-lead, 2026-11-11).
			image, target := "t015-"+strings.ToLower(distro), prefix+"-vm"
			sha := os.Getenv("PLUMB_TEST_IMAGE_" + distro + "_SHA256")
			if len(sha) != 64 || strings.Trim(sha, "0123456789abcdef") != "" {
				t.Fatal("invalid pinned checksum")
			}
			expected["plumb-img-"+image+"-"+sha[:12]] = true
			imports["plumb-"+image+"-"+sha[:12]+".qcow2"] = true
			docs := []any{}
			for _, block := range []string{"deploy-user", "motd", "nginx"} {
				b, err := blocks.FS.ReadFile(block + ".yaml")
				check(t, err, "read starter block")
				var d object
				check(t, yaml.Unmarshal(b, &d), "decode starter block")
				d["name"] = prefix + "-" + block
				docs = append(docs, d)
				refs = append(refs, object{"kind": "Block", "name": d["name"]})
			}
			key, err := os.ReadFile(os.Getenv("PLUMB_TEST_GUEST_KEY") + ".pub")
			check(t, err, "read guest public key")
			pub := strings.TrimSpace(string(key))
			address := os.Getenv(fmt.Sprintf("PLUMB_TEST_VM_IP_%d", i+1))
			docs = append(docs, object{"kind": "Image", "name": image, "spec": object{
				"type": "vm", "url": os.Getenv("PLUMB_TEST_IMAGE_" + distro + "_URL"), "sha256": sha}},
				object{"kind": "Target", "name": target, "spec": object{"type": "vm", "image": image,
					"guest": object{"cores": 2, "memory": 1024, "disk": 10, "bridge": "vmbr0", "ip": address,
						"gateway": os.Getenv("PLUMB_TEST_VM_GW"), "ssh_keys": []string{pub}},
					"blocks": []any{object{"use": prefix + "-deploy-user", "params": object{"ssh_key": pub}},
						object{"use": prefix + "-motd"}, object{"use": prefix + "-nginx"}}}})
			refs = append(refs, object{"kind": "Image", "name": image}, object{"kind": "Target", "name": target})
			// Cleanup is registered before approval so partial provisioning is covered.
			c.approve(c.call("POST", "/plans", object{"reason": "T-015 real slice", "put": docs}, 201), false)
			status := c.call("GET", "/targets/"+target, nil, 200)["status"].(map[string]any)
			n := vmid(t, status["guest_id"].(string))
			if _, exists := before[n]; exists {
				t.Fatal("test guest reuses a preexisting VMID")
			}
			owned[n] = true
			g := guest{id: n, address: address, hosts: filepath.Join(t.TempDir(), "known_hosts")}
			ready := false
			deadline := time.Now().Add(2 * time.Minute)
			for !ready && time.Now().Before(deadline) {
				_, ready = guestSSH(t, g, "true")
				if !ready {
					time.Sleep(time.Second)
				}
			}
			if !ready {
				t.Fatalf("guest SSH ready timed out (class: %s)", sshFailureClass(g))
			}
			wantOS := []string{"debian 13", "ubuntu 26.04"}[i]
			if guestRead(t, g, `. /etc/os-release; printf "%s %s" "$ID" "$VERSION_ID"`) != wantOS {
				t.Fatal("unexpected guest distribution or release")
			}
			initial := fingerprint(t, g)
			content := guestRead(t, g, "sudo cat /etc/nginx/sites-available/default")
			if guestRead(t, g, "cat /etc/motd") != "Managed by Plumb." ||
				!strings.Contains(guestRead(t, g, "sudo cat /home/deploy/.ssh/authorized_keys"), pub) {
				t.Fatal("starter blocks missing")
			}
			driftPath(&c, g, target, content)
			if _, ok := hostSSH(t, owned, n, "reboot"); !ok {
				t.Fatal("host reboot failed (output withheld)")
			}
			wait(t, "reboot changes boot ID and agent returns", 3*time.Minute, func() bool {
				now, ok := guestSSH(t, g, fingerprintCommand)
				if !ok || now == initial || strings.Split(now, "\n")[0] != strings.Split(initial, "\n")[0] {
					return false
				}
				s := c.call("GET", "/targets/"+target, nil, 200)["status"].(map[string]any)
				return s["agent"] == "online" && s["power"] == "running"
			})
			after := fingerprint(t, g)
			if strings.Split(initial, "\n")[0] != strings.Split(after, "\n")[0] {
				t.Fatal("SSH fingerprint changed after reboot")
			}
			if _, ok := hostSSH(t, owned, n, "memory"); !ok {
				t.Fatal("host memory update failed (output withheld)")
			}
			// Memory is not in Proxmox's default hotplug list: on a running VM
			// the change is pending, and the driver observes the running values
			// (config?current=1). The allowed reboot applies it; then it is drift.
			if _, ok := hostSSH(t, owned, n, "reboot"); !ok {
				t.Fatal("host reboot after the memory change failed (output withheld)")
			}
			issue := c.issue(target, "guest:config", 90*time.Second)
			c.approve(c.call("POST", "/issues/"+issue+"/resolve", object{"action": "revert"}, 201), false)
			c.closed(issue, "revert")
		})
		if !ok {
			break
		} // Stop at the first product failure; cleanup still runs.
	}
}
