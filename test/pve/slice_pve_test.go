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
		if keepForDiagnosis != 0 {
			t.Logf("kept for diagnosis: VMID %d, its template, documents and import file (no cleanup)", keepForDiagnosis)
			return
		}
		// Deferred: a Fatal in the delete Plan or the template guard must
		// still free this run's import files (review N5).
		defer func() {
			freeImports(a.t, importsBefore, imports)
			checkImports(a.t, importsBefore, map[string]bool{})
		}()
		cleanup(a, before, expected, refs, owned)
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
			// Unique Image names per run (pl-lead, 2026-11-11): a stable name
			// hits the stale Image binding defect (run 3). The import file of
			// this run is freed by exact name after the run.
			image, target := prefix+"-img", prefix+"-vm"
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
			// D-192: stock Ubuntu 26.04 does not take a static address (dracut
			// DHCP in the initramfs); it runs on DHCP and the test finds its
			// address from the MAC (qm config) and the controller's neighbors.
			dhcp := distro == "UBUNTU2604"
			address, gateway := os.Getenv("PLUMB_TEST_VM_IP_1"), os.Getenv("PLUMB_TEST_VM_GW")
			if dhcp {
				address, gateway = "dhcp", ""
			}
			docs = append(docs, object{"kind": "Image", "name": image, "spec": object{
				"type": "vm", "url": os.Getenv("PLUMB_TEST_IMAGE_" + distro + "_URL"), "sha256": sha}},
				object{"kind": "Target", "name": target, "spec": object{"type": "vm", "image": image,
					"guest": guestSpec(address, gateway, pub),
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
			if dhcp {
				g.address = leaseAddress(t, n)
			}
			// The Run ends when the agent is online (enrolled); SSH may come
			// later. Limit 6 min (pl-lead); the time is logged.
			agentOnline := time.Now()
			ready := false
			// Run 4: Ubuntu's first boot took ~3 min until the agent was
			// online; SSH then works at once with the right address. Margin.
			deadline := time.Now().Add(5 * time.Minute)
			for !ready && time.Now().Before(deadline) {
				_, ready = guestSSH(t, g, "true")
				if !ready {
					time.Sleep(2 * time.Second)
				}
			}
			if !ready {
				class := sshFailureClass(g)
				keepForDiagnosis = n
				t.Fatalf("guest SSH not ready %s after the agent came online (class: %s); VM kept", time.Since(agentOnline).Round(time.Second), class)
			}
			t.Logf("guest SSH ready %s after the agent came online", time.Since(agentOnline).Round(time.Second))
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
				if dhcp { // the lease can change on a reboot
					if a, ok := tryLeaseAddress(t, n); ok {
						g.address = a
					}
				}
				now, ok := guestSSH(t, g, fingerprintCommand)
				if !ok || now == initial {
					return false
				}
				if strings.Split(now, "\n")[0] != strings.Split(initial, "\n")[0] {
					t.Fatal("SSH fingerprint changed after reboot") // new boot, other key
				}
				s := c.call("GET", "/targets/"+target, nil, 200)["status"].(map[string]any)
				return s["agent"] == "online" && s["power"] == "running"
			})
			after := fingerprint(t, g)
			if strings.Split(initial, "\n")[0] != strings.Split(after, "\n")[0] {
				t.Fatal("SSH fingerprint changed after reboot")
			}
			if dhcp {
				g.address = leaseAddress(t, n)
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
			memoryDrift(t, c.call("GET", "/issues/"+issue, nil, 200))
			c.approve(c.call("POST", "/issues/"+issue+"/resolve", object{"action": "revert"}, 201), false)
			c.closed(issue, "revert")
			// The revert writes memory 1024 on a running VM: only pending
			// (T-056 fact 3). qm config shows the pending view, so this
			// proves the desired value was written, not that the VM runs it.
			// TODO(T-056): when T-056 is merged, assert the running value.
			if config(t, n)["memory"] != "1024" {
				t.Fatal("revert did not write memory 1024 to the VM config")
			}
			t.Log("guest:config revert wrote memory 1024 (pending); the running value stays 1536 until T-056")
		})
		if !ok {
			break
		} // Stop at the first product failure; cleanup still runs.
	}
}

// memoryDrift checks that the guest:config Issue is the test's own change:
// exactly memory from 1536 (the VM runs it) to 1024 (desired).
func memoryDrift(t *testing.T, i object) {
	t.Helper()
	changes, _ := i["changes"].([]any)
	if len(changes) != 1 {
		t.Fatalf("guest:config Issue has %d changes, want 1 (memory)", len(changes))
	}
	ch := changes[0].(map[string]any)
	// Print from/to only for memory: other attributes (bridge, ip) can hold
	// private values such as an address.
	if ch["attr"] != "memory" {
		t.Fatalf("guest:config Issue change is attribute %q, want memory", ch["attr"])
	}
	if ch["from"] != float64(1536) || ch["to"] != float64(1024) {
		t.Fatalf("guest:config Issue change is memory %v→%v, want 1536→1024", ch["from"], ch["to"])
	}
}

func guestSpec(address, gateway, pub string) object {
	g := object{"cores": 2, "memory": 1024, "disk": 10, "bridge": "vmbr0", "ip": address, "ssh_keys": []string{pub}}
	if gateway != "" {
		g["gateway"] = gateway
	}
	return g
}
