package fake

import (
	"context"
	"strings"
	"testing"

	"github.com/Bernotese/plumb/internal/driver"
	"github.com/Bernotese/plumb/internal/wire"
)

// T-219: the fake keeps the more NICs and applies their changes live.
func TestFakeNICs(t *testing.T) {
	d := New()
	ctx := context.Background()
	ref, err := d.FetchImage(ctx, driver.ImageSpec{Name: "deb", Kind: wire.KindVM, URL: "https://x/i.img", SHA256: strings.Repeat("a", 64)})
	if err != nil {
		t.Fatal(err)
	}
	m1, m2 := driver.NICMAC("i", "web1", 1), driver.NICMAC("i", "web1", 2)
	spec := driver.GuestSpec{Name: "web1", Kind: wire.KindVM, ImageName: "deb", Image: ref, Cores: 1, MemoryMB: 512, DiskGB: 4, Bridge: "vmbr0",
		NICs: []driver.NIC{{Bridge: "vmbr9", Tag: 20, MAC: m1}}}
	id, err := d.Create(ctx, spec, driver.Bootstrap{Token: "t"})
	if err != nil {
		t.Fatal(err)
	}
	spec.NICs[0].Tag = 30 // the caller's slice is not the guest's
	spec.NICs[0].Tag = 20
	st, _ := d.Observe(ctx, id)
	if ch, err := d.Diff(spec, st); err != nil || len(ch) != 0 {
		t.Fatalf("diff after create: %+v %v", ch, err)
	}
	next := spec
	next.NICs = []driver.NIC{{Bridge: "vmbr9", Tag: 20, MAC: m1}, {Bridge: "vmbr9", MAC: m2}}
	ch, _ := d.Diff(next, st)
	if len(ch) != 1 || ch[0].Attr != "nic2" || ch[0].Risk != wire.RiskDisruptive {
		t.Fatalf("add: %+v", ch)
	}
	if err := d.Update(ctx, id, ch, nil); err != nil {
		t.Fatal(err)
	}
	st, _ = d.Observe(ctx, id)
	if ch, _ := d.Diff(next, st); len(ch) != 0 || len(st.Spec.NICs) != 2 {
		t.Fatalf("after add: %+v %+v", ch, st.Spec.NICs)
	}
	// Remove NIC 1: a gap stays at net1, net2 keeps its MAC.
	gap := next
	gap.NICs = []driver.NIC{{}, {Bridge: "vmbr9", MAC: m2}}
	ch, _ = d.Diff(gap, st)
	if len(ch) != 1 || ch[0].Attr != "nic1" || ch[0].To != nil {
		t.Fatalf("remove: %+v", ch)
	}
	if err := d.Update(ctx, id, ch, nil); err != nil {
		t.Fatal(err)
	}
	st, _ = d.Observe(ctx, id)
	if len(st.Spec.NICs) != 2 || st.Spec.NICs[0].Bridge != "" || st.Spec.NICs[1].MAC != m2 {
		t.Errorf("after remove: %+v", st.Spec.NICs)
	}
}
