package proxmox

// ListBuilds and RemoveBuild (T-062 part B, D-203, review S4 of T-039):
// only a real build guest is listed and removed.

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/Bernotese/plumb/internal/driver"
)

func TestListAndRemoveBuilds(t *testing.T) {
	f := newFakePVE(t)
	d := f.driver(t)
	ctx := context.Background()
	base, err := d.FetchImage(ctx, buildImageSpec("web"))
	if err != nil {
		t.Fatal(err)
	}
	gid, err := d.CreateBuild(ctx, buildSpec("web", 2, base), driver.Bootstrap{Token: "tok-build"})
	if err != nil {
		t.Fatal(err)
	}
	// Look-alikes that must never be listed or removed.
	f.addVM(9050, fakePool, map[string]string{"name": "plumb-build-web-v2", "description": "not ours"}, true)                              // name, no marker
	f.addVM(9051, fakePool, map[string]string{"name": "web1", "description": "plumb-build: web v2"}, true)                                 // marker, other name
	f.addVM(9052, fakePool, map[string]string{"name": "plumb-build-web-v7", "description": "plumb-build: web v7", "template": "1"}, false) // template
	f.addVM(9053, "other", map[string]string{"name": "plumb-build-web-v8", "description": "plumb-build: web v8"}, true)                    // other pool
	f.addVM(9054, fakePool, map[string]string{"name": "plumb-build-web-v09", "description": "plumb-build: web v09"}, true)                 // bad number

	list, err := d.ListBuilds(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// The build guest, and the converted rest (9052) with Template set.
	if len(list) != 2 || list[0].ID != gid || list[0].Image != "web" || list[0].Version != 2 || list[0].Template ||
		list[1].ID != "9052" || !list[1].Template || list[1].Version != 7 {
		t.Fatalf("ListBuilds = %+v, want %s and the template 9052", list, gid)
	}
	for _, c := range []struct {
		id      int
		image   string
		version int
	}{{9050, "web", 2}, {9051, "web", 2}, {9052, "web", 7}, {9053, "web", 8}} {
		err := d.RemoveBuild(ctx, driver.GuestID(strconv.Itoa(c.id)), c.image, c.version)
		if err == nil {
			t.Errorf("RemoveBuild(%d) removed a guest that is not a build guest", c.id)
		}
		if f.cfgOf(c.id) == nil {
			t.Errorf("VMID %d is gone", c.id)
		}
	}
	// The real one, but asked as another version: refused.
	if err := d.RemoveBuild(ctx, gid, "web", 3); err == nil || !strings.Contains(err.Error(), "not the build guest") {
		t.Fatalf("other version: %v", err)
	}
	// The real one: stopped and removed; a second call is ErrNotFound.
	if err := d.RemoveBuild(ctx, gid, "web", 2); err != nil {
		t.Fatal(err)
	}
	if f.cfgOf(gid) != nil {
		t.Fatal("the build guest is still there")
	}
	if err := d.RemoveBuild(ctx, gid, "web", 2); !errors.Is(err, driver.ErrNotFound) {
		t.Fatalf("second remove: %v", err)
	}
	// The base template is untouched.
	if f.cfgOf(base) == nil {
		t.Fatal("the base template is gone")
	}
}
