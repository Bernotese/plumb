//go:build pve

// Real-host tests (make test-pve → scripts/test-pve.sh). The script runs
// the test binary inside the Plumb LXC with /etc/plumb/plumb.env, which the
// installer (T-013) wrote: PLUMB_PVE_URL, _TOKEN_ID, _SECRET, _CA, _NODE,
// _POOL (plumb), _DISK, _IMAGES, _SNIPPETS, _SNIPPET, _BRIDGE. The script
// adds PLUMB_PVE_IMAGE_URL and PLUMB_PVE_IMAGE_SHA256.
//
// The VMID range is 9000–9099 (ARCHITECTURE §8). Every test removes what
// it made. PLUMB_PVE_RECORD=DIR records the answers (record_test.go).
package proxmox

import (
	"os"
	"testing"
)

func hostDriver(t *testing.T) *Driver {
	t.Helper()
	env := func(k string) string {
		v := os.Getenv(k)
		if v == "" {
			t.Fatalf("%s is not set", k)
		}
		return v
	}
	c, err := NewClient(ClientConfig{URL: env("PLUMB_PVE_URL"), TokenID: env("PLUMB_PVE_TOKEN_ID"),
		Secret: env("PLUMB_PVE_SECRET"), CAFile: os.Getenv("PLUMB_PVE_CA")})
	if err != nil {
		t.Fatal(err)
	}
	// PLUMB_PVE_RECORD=DIR writes every exchange to DIR (testdata/pve form,
	// scrubbed, no header). Copy it into testdata/pve after a leak check.
	if dir := os.Getenv("PLUMB_PVE_RECORD"); dir != "" {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		c.http.Transport = newRecorder(c.http.Transport, dir, env("PLUMB_PVE_URL"), env("PLUMB_PVE_NODE"))
	}
	d, err := New(Config{Client: c, Node: env("PLUMB_PVE_NODE"), Pool: env("PLUMB_PVE_POOL"),
		DiskStorage: env("PLUMB_PVE_DISK"), ImageStorage: env("PLUMB_PVE_IMAGES"),
		SnippetStorage: env("PLUMB_PVE_SNIPPETS"), Snippet: env("PLUMB_PVE_SNIPPET"), VMIDMin: 9000, VMIDMax: 9099})
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// TestHostLifecycle: import, create, observe, update, re-enroll, delete.
func TestHostLifecycle(t *testing.T) {
	runLifecycle(t, hostDriver(t), hostImage(), os.Getenv("PLUMB_PVE_BRIDGE"))
}
