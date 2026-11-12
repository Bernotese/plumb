//go:build pve

package proxmox

import (
	"context"
	"os"
	"testing"
	"time"
)

// hostCTKeys are public test keys without a private half (made for T-079
// and thrown away). The second one has a non-ASCII comment (review S1 of
// #162). The replay sees them as the recorder scrubbed them.
var hostCTKeys = []string{
	"ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIMHuvKdtDvFdfKZ91D5pB6tM/c80ZiwpVeGJBZfWOygU t079",
	"ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIFij5Knm0cqjtziBGCk3P49yteRZKWJ1JuHOp03EVi+O Jürgen's Schlüssel ✓",
}

// hostCTDriver is hostDriver with the start check (it reads the vztmpl
// content of the image storage, D-229) and the test CTSource.
func hostCTDriver(t *testing.T) *Driver {
	t.Helper()
	d := hostDriver(t)
	if _, err := d.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	// The root fs, as /var/lib/plumb/tmp of serve: /tmp may be a small tmpfs.
	dir, err := os.MkdirTemp("/root", "plumb-t079-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	if err := d.SetCTSource(testCTSource(dir)); err != nil {
		t.Fatal(err)
	}
	return d
}

// TestHostCTFetch downloads, repacks and uploads the test template (not
// recorded: the upload is the file). It logs the host facts.
func TestHostCTFetch(t *testing.T) {
	d := hostCTDriver(t)
	start := time.Now()
	ref, err := d.FetchImage(context.Background(), hostCTImage)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("host fact: FetchImage(lxc) %s in %s", ref, time.Since(start).Round(100*time.Millisecond))
	start = time.Now()
	if again, err := d.FetchImage(context.Background(), hostCTImage); err != nil || again != ref {
		t.Fatalf("reuse: %s %v", again, err)
	}
	t.Logf("host fact: reuse of the file in %s", time.Since(start).Round(time.Millisecond))
}

// TestHostCTLifecycle runs runCTLifecycle on the host (recorded).
func TestHostCTLifecycle(t *testing.T) {
	runCTLifecycle(t, hostCTDriver(t), hostCTImage, os.Getenv("PLUMB_PVE_BRIDGE"), hostCTKeys)
}

// TestHostCTBuild runs runCTBuild on the host (recorded, T-082 H-B1).
func TestHostCTBuild(t *testing.T) {
	runCTBuild(t, hostCTDriver(t), hostCTImage, os.Getenv("PLUMB_PVE_BRIDGE"))
}
