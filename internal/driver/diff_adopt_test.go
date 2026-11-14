package driver

import (
	"testing"

	"github.com/Bernotese/plumb/internal/wire"
)

// D-391: the create-only attributes and container features of an adopted
// guest are not compared; kind and the mutable settings are.
func TestDiffAdopted(t *testing.T) {
	have := GuestSpec{Kind: wire.KindLXC, Cores: 1, MemoryMB: 512, DiskGB: 8, Bridge: "vmbr0",
		IP: "", User: "", Features: "keyctl=1"}
	want := GuestSpec{Kind: wire.KindLXC, Cores: 1, MemoryMB: 512, DiskGB: 8, Bridge: "vmbr0",
		ImageName: "", IP: "dhcp", User: "plumb", Features: CTFeatures, Adopted: true}
	if ch, err := DiffGuest(want, GuestState{Spec: have}); err != nil || len(ch) != 0 {
		t.Fatalf("adopted: %v %v", ch, err)
	}
	// Not adopted: the same values are a create-only error.
	plain := want
	plain.Adopted = false
	if _, err := DiffGuest(plain, GuestState{Spec: have}); err == nil {
		t.Fatal("not adopted: no create-only error")
	}
	// Cores are compared; kind too.
	more := want
	more.Cores = 2
	if ch, err := DiffGuest(more, GuestState{Spec: have}); err != nil || len(ch) != 1 || ch[0].Attr != AttrCores {
		t.Fatalf("cores: %v %v", ch, err)
	}
	vm := want
	vm.Kind = wire.KindVM
	if _, err := DiffGuest(vm, GuestState{Spec: have}); err == nil {
		t.Fatal("kind change of an adopted guest: no error")
	}
}
