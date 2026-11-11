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
		AttrDisk:    func(s *GuestSpec) { s.DiskGB = 40 },
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
	if len(CreateOnly) != 7 {
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
	if len(c) != 2 || c[0].Attr != AttrDisk || c[0].Risk != wire.RiskDestructive || c[0].From != 50 ||
		c[1].Attr != AttrCores || c[1].From != 8 || c[1].To != 2 {
		t.Errorf("drift = %+v", c)
	}
	if _, err := DiffGuest(spec(), GuestState{Spec: have}); err == nil {
		t.Error("DiffGuest must refuse the same difference")
	}
}
