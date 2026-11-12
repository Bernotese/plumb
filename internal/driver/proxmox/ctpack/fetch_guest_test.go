//go:build guest

package ctpack

import (
	"os"
	"os/exec"
	"testing"
)

// Review S1 of T-076, in a Debian/Ubuntu guest (make test-guest): a CA in
// the system store does not pass the first-boot fetch. Only the pinned
// Plumb CA does, with curl, wget and python3.
func TestFirstBootPinInGuest(t *testing.T) {
	if _, err := os.Stat("/.dockerenv"); err != nil && os.Getenv("container") != "docker" {
		t.Fatal("guest test requires a Docker container")
	}
	run := func(name string, args ...string) {
		t.Helper()
		cmd := exec.Command(name, args...)
		cmd.Env = append(os.Environ(), "DEBIAN_FRONTEND=noninteractive")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s %v: %v\n%s", name, args, err, out)
		}
	}
	run("apt-get", "install", "-y", "--no-install-recommends", "curl", "wget", "python3")

	public, plumb := newTlsCA(t, "public"), newTlsCA(t, "plumb")
	const anchor = "/usr/local/share/ca-certificates/plumb-test-public.crt"
	if err := os.WriteFile(anchor, public.pem, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		os.Remove(anchor)
		exec.Command("update-ca-certificates", "--fresh").Run()
	})
	run("update-ca-certificates")
	srv := public.server(t)

	// The scenario is real: the old flags (--cacert alone) trust the
	// system store, so they accept the server with the Plumb CA pinned.
	pin := t.TempDir() + "/ca.pem"
	os.WriteFile(pin, plumb.pem, 0o644)
	if out, err := exec.Command("curl", "-fsS", "--cacert", pin, "-o", "/dev/null", srv.URL+"/agent/v1/binary").CombinedOutput(); err != nil {
		t.Fatalf("the system store does not trust the test CA, the test proves nothing: %v %s", err, out)
	}
	for _, tool := range []string{"curl", "wget", "python3"} {
		t.Run(tool, func(t *testing.T) {
			if ok, _ := fetchWith(t, tool, plumb.pem, srv.URL); ok {
				t.Fatal("a CA of the system store passed the Plumb pin")
			}
			if ok, out := fetchWith(t, tool, public.pem, srv.URL); !ok {
				t.Fatalf("control: the pinned CA itself failed\n%s", out)
			}
		})
	}
}
