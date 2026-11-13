package driver

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/Bernotese/plumb/internal/wire"
)

func spec() GuestSpec {
	return GuestSpec{Name: "web1", Kind: wire.KindVM, ImageName: "debian", Image: "tpl-1", Cores: 2, MemoryMB: 2048, DiskGB: 20,
		Bridge: "vmbr0", IP: "dhcp", User: "plumb", SSHKeys: []string{"ssh-ed25519 AAAA a@b"}}
}

func TestDiffGuest(t *testing.T) {
	have := GuestState{Spec: spec()}
	if c, err := DiffGuest(spec(), have); err != nil || len(c) != 0 {
		t.Fatalf("no change: %v, %v", c, err)
	}
	want := spec()
	want.Cores, want.MemoryMB, want.Bridge = 4, 4096, "vmbr1"
	changes, err := DiffGuest(want, have)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 3 {
		t.Fatalf("changes = %+v", changes)
	}
	for _, c := range changes {
		if c.Risk != wire.RiskDisruptive || c.From == nil || c.To == nil {
			t.Errorf("change %+v: want from, to and risk disruptive", c)
		}
	}
	if changes[0] != (wire.Change{Attr: AttrCores, From: 2, To: 4, Risk: wire.RiskDisruptive}) {
		t.Errorf("cores change = %+v (from = observed, to = desired)", changes[0])
	}
	// Every create-only attribute is refused by name.
	for attr, mutate := range map[string]func(*GuestSpec){
		AttrKind:    func(s *GuestSpec) { s.Kind = wire.KindLXC },
		AttrImage:   func(s *GuestSpec) { s.ImageName = "ubuntu" },
		AttrIP:      func(s *GuestSpec) { s.IP = "10.0.0.5/24" },
		AttrGateway: func(s *GuestSpec) { s.Gateway = "10.0.0.1" },
		AttrUser:    func(s *GuestSpec) { s.User = "root" },
		AttrSSHKeys: func(s *GuestSpec) { s.SSHKeys = nil },
	} {
		d := spec()
		mutate(&d)
		_, err := DiffGuest(d, have)
		var co *CreateOnlyError
		if !errors.As(err, &co) || co.Attr != attr {
			t.Errorf("%s: got %v, want CreateOnlyError", attr, err)
		}
	}
	if len(CreateOnly) != 6 {
		t.Errorf("CreateOnly = %v; a change of this list needs a decision line", CreateOnly)
	}
	// The driver ref is no setting (D-189): a re-made template or an
	// empty observed ref is no change and no drift.
	ref := spec()
	ref.Image = "tpl-2"
	noRef := spec()
	noRef.Image = ""
	if c, err := DiffGuest(ref, GuestState{Spec: noRef}); err != nil || len(c) != 0 {
		t.Errorf("new image ref: %v, %v", c, err)
	}
	if c := DriftGuest(ref, GuestState{Spec: noRef}); len(c) != 0 {
		t.Errorf("new image ref drift: %v", c)
	}
	// Empty and nil key lists are the same.
	a, b := spec(), spec()
	a.SSHKeys, b.SSHKeys = nil, []string{}
	if _, err := DiffGuest(a, GuestState{Spec: b}); err != nil {
		t.Errorf("nil vs empty keys: %v", err)
	}
}

func TestIntValue(t *testing.T) {
	for _, v := range []any{4, int64(4), 4.0, json.Number("4")} {
		if n, err := IntValue(v); err != nil || n != 4 {
			t.Errorf("%T %v: %d, %v", v, v, n, err)
		}
	}
	for _, v := range []any{4.5, "4", nil, json.Number("4.5")} {
		if _, err := IntValue(v); err == nil {
			t.Errorf("%T %v: want an error", v, v)
		}
	}
}

func TestDriftGuest(t *testing.T) {
	have := spec()
	have.Cores, have.DiskGB = 8, 50
	c := DriftGuest(spec(), GuestState{Spec: have})
	// A disk grown in Proxmox is drift (D-299), but no Plan shrinks it.
	if len(c) != 2 || c[0].Attr != AttrCores || c[0].From != 8 || c[0].To != 2 ||
		c[1].Attr != AttrDisk || c[1].Risk != wire.RiskDisruptive || c[1].From != 50 || c[1].To != 20 {
		t.Errorf("drift = %+v", c)
	}
	var se *ShrinkError
	if _, err := DiffGuest(spec(), GuestState{Spec: have}); !errors.As(err, &se) || se.Have != 50 || se.Want != 20 {
		t.Errorf("DiffGuest must refuse the shrink: %v", err)
	}
}

// Slice 4 (D-291, D-299): disk grow, tags and power.
func TestDiffGuestDay2(t *testing.T) {
	running := GuestState{Spec: spec(), Power: PowerRunning}
	stopped := GuestState{Spec: spec(), Power: PowerStopped}

	// Disk: grow only, disruptive; a pending bigger value is a shrink too.
	grow := spec()
	grow.DiskGB = 30
	c, err := DiffGuest(grow, running)
	if err != nil || len(c) != 1 || c[0] != (wire.Change{Attr: AttrDisk, From: 20, To: 30, Risk: wire.RiskDisruptive}) {
		t.Errorf("grow: %+v, %v", c, err)
	}
	if g := Grow(c); g == nil || g.To != 30 {
		t.Errorf("Grow = %+v", g)
	}
	if Grow([]wire.Change{{Attr: AttrDisk, From: 30, To: 20}}) != nil || Grow(nil) != nil {
		t.Error("Grow of a shrink or of nothing")
	}
	pend := spec()
	pend.DiskGB = 40
	var se *ShrinkError
	if _, err := DiffGuest(grow, GuestState{Spec: spec(), Pending: &pend}); !errors.As(err, &se) || se.Have != 40 {
		t.Errorf("pending bigger disk: %v", err)
	}

	// Power: missing is not managed; start reversible, stop disruptive.
	if c, err := DiffGuest(spec(), stopped); err != nil || len(c) != 0 {
		t.Errorf("unmanaged power: %+v, %v", c, err)
	}
	on := spec()
	on.Power = PowerRunning
	if c, _ := DiffGuest(on, stopped); len(c) != 1 || c[0] != (wire.Change{Attr: AttrPower, From: "stopped", To: "running", Risk: wire.RiskReversible}) {
		t.Errorf("start: %+v", c)
	}
	if c, _ := DiffGuest(on, running); len(c) != 0 {
		t.Errorf("running as desired: %+v", c)
	}
	off := spec()
	off.Power = PowerStopped
	if c, _ := DiffGuest(off, running); len(c) != 1 || c[0] != (wire.Change{Attr: AttrPower, From: "running", To: "stopped", Risk: wire.RiskDisruptive}) {
		t.Errorf("stop: %+v", c)
	}

	// Tags: missing is not managed; else the exact set, without case and order.
	tagged := spec()
	tagged.Tags = []string{"Web", "prod"}
	if c, _ := DiffGuest(spec(), GuestState{Spec: tagged}); len(c) != 0 {
		t.Errorf("unmanaged tags: %+v", c)
	}
	want := spec()
	want.Tags, want.TagsSet = []string{"prod", "web"}, true
	if c, _ := DiffGuest(want, GuestState{Spec: tagged}); len(c) != 0 {
		t.Errorf("same set: %+v", c)
	}
	none := spec()
	none.TagsSet = true
	c, _ = DiffGuest(none, GuestState{Spec: tagged})
	if len(c) != 1 || c[0].Attr != AttrTags || c[0].Risk != wire.RiskReversible || len(c[0].To.([]string)) != 0 ||
		len(c[0].From.([]string)) != 2 {
		t.Errorf("remove all tags: %+v", c)
	}
	c, _ = DiffGuest(want, GuestState{Spec: spec()})
	if len(c) != 1 || len(c[0].From.([]string)) != 0 || len(c[0].To.([]string)) != 2 {
		t.Errorf("add tags: %+v", c)
	}
	if got := NormTags([]string{" B", "a", "b", ""}); len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Errorf("NormTags = %v", got)
	}
	if NormTags(nil) != nil || NormTags([]string{""}) != nil {
		t.Error("NormTags of nothing is nil")
	}
}
