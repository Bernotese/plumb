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
	"strconv"
	"strings"
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
	lo, hi := vmidRange(t)
	d, err := New(Config{Client: c, Node: env("PLUMB_PVE_NODE"), Pool: env("PLUMB_PVE_POOL"),
		DiskStorage: env("PLUMB_PVE_DISK"), ImageStorage: env("PLUMB_PVE_IMAGES"),
		SnippetStorage: env("PLUMB_PVE_SNIPPETS"), Snippet: env("PLUMB_PVE_SNIPPET"), VMIDMin: lo, VMIDMax: hi,
		Nameservers: strings.Split(env("PLUMB_PVE_NAMESERVER"), ","), SearchDomain: env("PLUMB_PVE_SEARCHDOMAIN"),
		Bridge: env("PLUMB_PVE_BRIDGE")})
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// TestHostLifecycle: import, create, observe, update, re-enroll, delete.
func TestHostLifecycle(t *testing.T) {
	runLifecycle(t, hostDriver(t), hostImage(), os.Getenv("PLUMB_PVE_BRIDGE"))
}

// vmidRange is PLUMB_PVE_VMID_RANGE (installer: plumb.env), default 9000-9099.
func vmidRange(t *testing.T) (int, int) {
	t.Helper()
	v := os.Getenv("PLUMB_PVE_VMID_RANGE")
	if v == "" {
		return 9000, 9099
	}
	a, b, ok := strings.Cut(v, "-")
	lo, err1 := strconv.Atoi(a)
	hi, err2 := strconv.Atoi(b)
	if !ok || err1 != nil || err2 != nil || lo < 100 || hi < lo {
		t.Fatalf("bad PLUMB_PVE_VMID_RANGE %q", v)
	}
	return lo, hi
}

// TestHostImageBuild: build guest, MakeImage, FindImage, ListImages, a
// Target of the version, a linked clone that blocks RemoveImage, RemoveImage
// (T-061). It logs the host facts of the card ("host fact:" lines).
func TestHostImageBuild(t *testing.T) {
	runImageBuild(t, hostDriver(t), hostImage(), os.Getenv("PLUMB_PVE_BRIDGE"))
}

// TestHostDiskSpace reads the use of the disk storage with the real token
// (T-084, D-240). It only reads. On an lvmthin disk storage the token has
// Sys.Audit on / since D-240, so the thin-pool metadata use is known.
func TestHostDiskSpace(t *testing.T) {
	ds, err := hostDriver(t).DiskSpace(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("host fact: %s total %d used %d; metadata known %v, %d of %d",
		ds.Storage, ds.TotalBytes, ds.UsedBytes, ds.MetaKnown, ds.MetaUsedBytes, ds.MetaTotalBytes)
	if !ds.MetaKnown || ds.MetaTotalBytes <= 0 || ds.MetaUsedBytes <= 0 || ds.MetaUsedBytes > ds.MetaTotalBytes {
		t.Errorf("thin-pool metadata use not read: %+v (does the token have Sys.Audit on /?)", ds)
	}
}
