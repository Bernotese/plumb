package backup

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestItemsListUnreadable (T-208, host finding F2): a backup file that
// serve cannot read (and no readable sum file) is listed with the state
// unreadable and no hash; it is not hidden.
func TestItemsListUnreadable(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads a 0000 file")
	}
	f := newFixture(t, nil)
	base := time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC)
	f.clock.set(base)
	good := f.backup("system").Name
	f.clock.set(base.Add(time.Second))
	bad := f.backup("system").Name
	dir := filepath.Join(f.data, DirName)
	for _, p := range []string{filepath.Join(dir, bad), filepath.Join(dir, bad+SumSuffix)} {
		if err := os.Chmod(p, 0); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { os.Chmod(p, 0o600) })
	}
	items, err := f.svc.Items(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]Item{}
	for _, it := range items {
		got[it.Name] = it
	}
	if it := got[good]; it.State != ItemOK || len(it.SHA256) != 64 {
		t.Fatalf("readable: %+v", it)
	}
	if it, ok := got[bad]; !ok || it.State != ItemUnreadable || it.SHA256 != "" || it.Size == 0 {
		t.Fatalf("unreadable: %+v (listed %v)", it, ok)
	}
}
