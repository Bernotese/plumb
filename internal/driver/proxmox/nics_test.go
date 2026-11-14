package proxmox

// More NICs (T-219, D-399 … D-404).

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"testing"

	"github.com/Bernotese/plumb/internal/driver"
	"github.com/Bernotese/plumb/internal/wire"
)

var testNets = InstallExtras{ExtraNets: []ExtraNet{{Bridge: "vmbr9"}, {Bridge: "vmbr9", Tag: 20}}}

func mac(n int) string { return driver.NICMAC("6f1c0b4e-0000-4000-8000-000000000001", "web1", n) }

func nicChanges(t *testing.T, d *Driver, desired driver.GuestSpec, id driver.GuestID) []wire.Change {
	t.Helper()
	st, err := d.Observe(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	ch, err := d.Diff(desired, st)
	if err != nil {
		t.Fatal(err)
	}
	return ch
}

func TestVMNICsCreateObserveUpdate(t *testing.T) {
	f := newFakePVE(t)
	d := f.driver(t)
	d.SetExtraNets(testNets)
	ctx := context.Background()
	ref, err := d.FetchImage(ctx, image)
	if err != nil {
		t.Fatal(err)
	}
	spec := guest(ref)
	spec.NICs = []driver.NIC{{Bridge: "vmbr9", Tag: 20, MAC: mac(1)}}
	id, err := d.Create(ctx, spec, driver.Bootstrap{Token: "tok-1"})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := f.vms[9001].cfg["net1"], "virtio="+strings.ToUpper(mac(1))+",bridge=vmbr9,tag=20"; got != want {
		t.Errorf("net1 = %q, want %q", got, want)
	}
	if ch := nicChanges(t, d, spec, id); len(ch) != 0 {
		t.Errorf("diff after create: %+v", ch)
	}
	// Add NIC 2 (untagged on vmbr9) and move NIC 1 to the main bridge.
	f.vms[9001].running = true
	next := spec
	next.NICs = []driver.NIC{{Bridge: "vmbr0", MAC: mac(1)}, {Bridge: "vmbr9", MAC: mac(2)}}
	ch := nicChanges(t, d, next, id)
	if len(ch) != 2 || ch[0].Attr != "nic1" || ch[1].Attr != "nic2" || ch[0].Risk != wire.RiskDisruptive || ch[1].Risk != wire.RiskDisruptive || ch[1].From != nil {
		t.Fatalf("changes: %+v", ch)
	}
	if err := d.Update(ctx, id, ch, nil); err != nil {
		t.Fatal(err)
	}
	cfg := f.vms[9001].cfg
	if cfg["net1"] != "virtio="+strings.ToUpper(mac(1))+",bridge=vmbr0" || cfg["net2"] != "virtio="+strings.ToUpper(mac(2))+",bridge=vmbr9" {
		t.Errorf("after update: net1 %q net2 %q", cfg["net1"], cfg["net2"])
	}
	if ch := nicChanges(t, d, next, id); len(ch) != 0 {
		t.Errorf("diff after update: %+v", ch)
	}
	// Remove NIC 2: a delete, NIC 1 stays.
	ch = nicChanges(t, d, spec, id)
	if err := d.Update(ctx, id, ch, nil); err != nil {
		t.Fatal(err)
	}
	if p := f.lastParams("POST /nodes/pve/qemu/9001/config"); p.Get("delete") != "net2" {
		t.Errorf("remove params: %v", p)
	}
	if _, ok := f.vms[9001].cfg["net2"]; ok {
		t.Error("net2 stays")
	}
	if ch := nicChanges(t, d, spec, id); len(ch) != 0 {
		t.Errorf("diff after remove: %+v", ch)
	}
	// The tags and a NIC are deleted in one call.
	next = spec
	next.NICs = nil
	next.Tags, next.TagsSet = nil, true
	f.vms[9001].cfg["tags"] = "x"
	if err := d.Update(ctx, id, nicChanges(t, d, next, id), nil); err != nil {
		t.Fatal(err)
	}
	if p := f.lastParams("POST /nodes/pve/qemu/9001/config"); p.Get("delete") != "tags,net1" && p.Get("delete") != "net1,tags" {
		t.Errorf("delete = %q", p.Get("delete"))
	}
}

// D-401: a bridge or tag that the install did not name is refused before
// any Proxmox call, although the fake would take it.
func TestNICNetworkNotAllowed(t *testing.T) {
	f := newFakePVE(t)
	d := f.driver(t)
	d.SetExtraNets(testNets)
	ctx := context.Background()
	ref, err := d.FetchImage(ctx, image)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range []driver.NIC{{Bridge: "vmbr9", Tag: 30, MAC: mac(1)}, {Bridge: "vmbr8", MAC: mac(1)}, {Bridge: "vmbr0", Tag: 20, MAC: mac(1)}} {
		f.mu.Lock()
		f.log = nil
		f.mu.Unlock()
		spec := guest(ref)
		spec.NICs = []driver.NIC{n}
		_, err := d.Create(ctx, spec, driver.Bootstrap{Token: "t"})
		var na *NetworkNotAllowedError
		if !errors.As(err, &na) || na.Bridge != n.Bridge || na.Tag != n.Tag {
			t.Errorf("%+v: %v", n, err)
		}
		f.mu.Lock()
		for _, l := range f.log {
			if !strings.HasPrefix(l, "GET ") {
				t.Errorf("%+v: sent %s", n, l)
			}
		}
		f.mu.Unlock()
		// Update: refused before the first call.
		f.mu.Lock()
		f.log = nil
		f.mu.Unlock()
		err = d.Update(ctx, "9001", []wire.Change{{Attr: "nic1", To: n.Value(), Risk: wire.RiskDisruptive}}, nil)
		if !errors.As(err, &na) || !strings.Contains(err.Error(), "install.sh with --extra-") {
			t.Errorf("update %+v: %v", n, err)
		}
		if f.count("") != 0 {
			t.Errorf("update %+v sent a request", n)
		}
	}
	if !strings.Contains((&NetworkNotAllowedError{NIC: 1, Bridge: "vmbr9", Tag: 30}).Error(), "--extra-vlan vmbr9:30") {
		t.Error("the message does not name the flag")
	}
}

func TestNICChecks(t *testing.T) {
	f := newFakePVE(t)
	d := f.driver(t)
	d.SetExtraNets(testNets)
	for name, c := range map[string]struct {
		n  driver.NIC
		ct bool
	}{
		"vm with ip":         {driver.NIC{Bridge: "vmbr9", MAC: mac(1), IP: "10.9.0.5/24"}, false},
		"foreign mac":        {driver.NIC{Bridge: "vmbr9", MAC: "bc:24:11:00:00:01"}, true},
		"no mac":             {driver.NIC{Bridge: "vmbr9"}, true},
		"bad ip":             {driver.NIC{Bridge: "vmbr9", MAC: mac(1), IP: "10.9.0.5"}, true},
		"gateway with dhcp":  {driver.NIC{Bridge: "vmbr9", MAC: mac(1), IP: "dhcp", Gateway: "10.9.0.1"}, true},
		"gateway without ip": {driver.NIC{Bridge: "vmbr9", MAC: mac(1), Gateway: "10.9.0.1"}, true},
		"bad bridge":         {driver.NIC{Bridge: "vmbr9,tag=30", MAC: mac(1)}, true},
	} {
		if err := d.checkNIC(1, c.n, c.ct); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if err := d.checkNICs(make([]driver.NIC, 4), true); err == nil {
		t.Error("4 NICs accepted")
	}
	if err := d.checkNIC(2, driver.NIC{Bridge: "vmbr9", Tag: 20, MAC: mac(2), IP: "10.9.20.5/24", Gateway: "10.9.20.1"}, true); err != nil {
		t.Error(err)
	}
	// The last guard of an update.
	for _, p := range []url.Values{
		{"delete": {"net0"}}, {"delete": {"tags,scsi0"}}, {"delete": {"net4"}},
		{"net1": {"virtio=BC:24:11:00:00:01,bridge=vmbr9"}}, {"net2": {"name=eth2,bridge=vmbr9"}},
	} {
		if err := checkUpdateNICs(p); err == nil {
			t.Errorf("%v accepted", p)
		}
	}
	if err := checkUpdateNICs(url.Values{"delete": {"tags,net1,net3"}, "net2": {vmNetValue(driver.NIC{Bridge: "vmbr9", MAC: mac(2)})}}); err != nil {
		t.Error(err)
	}
}

// Review L2 of #332: with the network hot-plug off, Proxmox keeps the NIC
// pending. The Update is no failure; the NIC waits for a restart, the next
// diff writes nothing again, and WaitingGuest lists it. A write that ends
// in no view fails with the config shown (D-402).
func TestVMNICHotplugOff(t *testing.T) {
	f := newFakePVE(t)
	d := f.driver(t)
	d.SetExtraNets(testNets)
	ctx := context.Background()
	ref, _ := d.FetchImage(ctx, image)
	spec := guest(ref)
	id, err := d.Create(ctx, spec, driver.Bootstrap{Token: "t"})
	if err != nil {
		t.Fatal(err)
	}
	f.vms[9001].running = true
	f.hotplugFails = true
	next := spec
	next.NICs = []driver.NIC{{Bridge: "vmbr9", MAC: mac(1)}}
	if err := d.Update(ctx, id, nicChanges(t, d, next, id), nil); err != nil {
		t.Fatalf("update: %v", err)
	}
	st, _ := d.Observe(ctx, id)
	if st.Pending == nil || len(st.Pending.NICs) != 1 || len(st.Spec.NICs) != 0 {
		t.Fatalf("state: %+v", st)
	}
	if ch := nicChanges(t, d, next, id); len(ch) != 0 {
		t.Errorf("the waiting NIC is written again: %+v", ch)
	}
	w := driver.WaitingGuest(next, st)
	if len(w) != 1 || w[0].Attr != "nic1" || !w[0].WaitsForRestart || w[0].Risk != wire.RiskRead {
		t.Errorf("waiting: %+v", w)
	}

	// A write that Proxmox takes into no view.
	f.hotplugFails, f.dropNICWrite = false, true
	next.NICs = append(next.NICs, driver.NIC{Bridge: "vmbr9", Tag: 20, MAC: mac(2)})
	err = d.Update(ctx, id, nicChanges(t, d, next, id), nil)
	if !errors.Is(err, errNICNotApplied) || !strings.Contains(err.Error(), `net2 is "missing"`) {
		t.Fatalf("lost write: %v", err)
	}
}

func TestCTNICs(t *testing.T) {
	f := newFakePVE(t)
	f.files[ctTemplate] = true
	d := f.driver(t)
	d.SetExtraNets(testNets)
	ctx := context.Background()
	spec := ctSpec()
	spec.NICs = []driver.NIC{{Bridge: "vmbr9", Tag: 20, IP: "10.9.20.5/24", Gateway: "10.9.20.1", MAC: mac(1)}, {Bridge: "vmbr9", MAC: mac(2)}}
	id, err := d.Create(ctx, spec, driver.Bootstrap{Token: ctToken})
	if err != nil {
		t.Fatal(err)
	}
	p := f.lastParams("POST /nodes/pve/lxc")
	if got, want := p.Get("net1"), "name=eth1,bridge=vmbr9,hwaddr="+strings.ToUpper(mac(1))+",tag=20,ip=10.9.20.5/24,gw=10.9.20.1"; got != want {
		t.Errorf("net1 = %q, want %q", got, want)
	}
	if got, want := p.Get("net2"), "name=eth2,bridge=vmbr9,hwaddr="+strings.ToUpper(mac(2)); got != want {
		t.Errorf("net2 = %q, want %q", got, want)
	}
	if ch := nicChanges(t, d, spec, id); len(ch) != 0 {
		t.Errorf("diff after create: %+v", ch)
	}
	// A new address on NIC 1 is a disruptive change with the same MAC.
	next := spec
	next.NICs = []driver.NIC{{Bridge: "vmbr9", Tag: 20, IP: "10.9.20.6/24", MAC: mac(1)}, spec.NICs[1]}
	ch := nicChanges(t, d, next, id)
	if len(ch) != 1 || ch[0].Attr != "nic1" || ch[0].Risk != wire.RiskDisruptive {
		t.Fatalf("changes %+v", ch)
	}
	if err := d.Update(ctx, id, ch, nil); err != nil {
		t.Fatal(err)
	}
	if got := f.lastParams("PUT /nodes/pve/lxc/" + string(id) + "/config").Get("net1"); !strings.Contains(got, "hwaddr="+strings.ToUpper(mac(1))) || !strings.Contains(got, "ip=10.9.20.6/24") || strings.Contains(got, "gw=") {
		t.Errorf("net1 write %q", got)
	}
	if ch := nicChanges(t, d, next, id); len(ch) != 0 {
		t.Errorf("diff after update: %+v", ch)
	}
}
