package driver

import (
	"errors"
	"reflect"
	"testing"

	"github.com/Bernotese/plumb/internal/wire"
)

// views builds an observed state from a running and a pending value of
// one attribute (D-190).
func views(set func(*GuestSpec, int, string), r, p int, rb, pb string) GuestState {
	run, next := spec(), spec()
	set(&run, r, rb)
	set(&next, p, pb)
	st := GuestState{Spec: run}
	if !reflect.DeepEqual(run, next) {
		st.Pending = &next
	}
	return st
}

// TestDriftGuestTableB pins the four rows of table B for cores, memory
// and bridge (T-056).
func TestDriftGuestTableB(t *testing.T) {
	type attrCase struct {
		attr string
		set  func(*GuestSpec, int, string)
		val  func(int, string) any
		d    int
		db   string
		o1   int // a value ≠ D
		o1b  string
		o2   int // a third value
		o2b  string
	}
	cases := []attrCase{
		{AttrCores, func(s *GuestSpec, n int, _ string) { s.Cores = n }, func(n int, _ string) any { return n }, 2, "", 4, "", 8, ""},
		{AttrMemory, func(s *GuestSpec, n int, _ string) { s.MemoryMB = n }, func(n int, _ string) any { return n }, 2048, "", 1024, "", 512, ""},
		{AttrBridge, func(s *GuestSpec, _ int, b string) { s.Bridge = b }, func(_ int, b string) any { return b }, 0, "vmbr0", 0, "vmbr1", 0, "vmbr2"},
	}
	for _, c := range cases {
		d := c.val(c.d, c.db)
		x := c.val(c.o1, c.o1b)
		y := c.val(c.o2, c.o2b)
		desired := spec()
		c.set(&desired, c.d, c.db)

		// R = D, P = D: match.
		if got := DriftGuest(desired, views(c.set, c.d, c.d, c.db, c.db)); len(got) != 0 {
			t.Errorf("%s match: %+v", c.attr, got)
		}
		// R = D, P ≠ D: pending drift, reversible (D-191).
		got := DriftGuest(desired, views(c.set, c.d, c.o1, c.db, c.o1b))
		want := []wire.Change{{Attr: c.attr, From: x, To: d, Risk: wire.RiskReversible, Pending: true}}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s pending drift: %+v, want %+v", c.attr, got, want)
		}
		// R ≠ D, P = D: waits for cores and memory; drift for bridge.
		got = DriftGuest(desired, views(c.set, c.o1, c.d, c.o1b, c.db))
		if IsColdStart(c.attr) {
			if len(got) != 0 {
				t.Errorf("%s waits: drift %+v", c.attr, got)
			}
		} else {
			want = []wire.Change{{Attr: c.attr, From: x, To: d, Risk: wire.RiskDisruptive, AlsoPending: d}}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("%s R≠D P=D: %+v, want %+v", c.attr, got, want)
			}
		}
		// R ≠ D, P ≠ D, P = R: plain drift.
		got = DriftGuest(desired, views(c.set, c.o1, c.o1, c.o1b, c.o1b))
		want = []wire.Change{{Attr: c.attr, From: x, To: d, Risk: wire.RiskDisruptive}}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s drift: %+v, want %+v", c.attr, got, want)
		}
		// R ≠ D, P ≠ D, P ≠ R: drift with also_pending.
		got = DriftGuest(desired, views(c.set, c.o1, c.o2, c.o1b, c.o2b))
		want = []wire.Change{{Attr: c.attr, From: x, To: d, Risk: wire.RiskDisruptive, AlsoPending: y}}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s drift + also_pending: %+v, want %+v", c.attr, got, want)
		}
		// DiffGuest agrees for the mutable attributes.
		if dg, err := DiffGuest(desired, views(c.set, c.o1, c.o2, c.o1b, c.o2b)); err != nil || !reflect.DeepEqual(dg, want) {
			t.Errorf("%s DiffGuest: %+v %v", c.attr, dg, err)
		}
	}
}

// TestDriftGuestCreateOnlyPending: a pending create-only input stays
// destructive drift and a CreateOnlyError in Diff; R ≠ D with P = D is
// drift too (only cores and memory can wait).
func TestDriftGuestCreateOnlyPending(t *testing.T) {
	desired := spec()
	next := spec()
	next.IP = "10.0.0.9/24"
	st := GuestState{Spec: spec(), Pending: &next}
	got := DriftGuest(desired, st)
	want := []wire.Change{{Attr: AttrIP, From: "10.0.0.9/24", To: "dhcp", Risk: wire.RiskDestructive, Pending: true}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("pending ip: %+v, want %+v", got, want)
	}
	var co *CreateOnlyError
	if _, err := DiffGuest(desired, st); !errors.As(err, &co) || co.Attr != AttrIP {
		t.Errorf("DiffGuest: %v, want CreateOnlyError ip", err)
	}
	// R ≠ D, P = D for a create-only input: drift with also_pending.
	run := spec()
	run.IP = "10.0.0.9/24"
	d := spec()
	st = GuestState{Spec: run, Pending: &d}
	got = DriftGuest(desired, st)
	want = []wire.Change{{Attr: AttrIP, From: "10.0.0.9/24", To: "dhcp", Risk: wire.RiskDestructive, AlsoPending: "dhcp"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ip R≠D P=D: %+v, want %+v", got, want)
	}
	if w := WaitingGuest(desired, st); len(w) != 0 {
		t.Errorf("WaitingGuest lists a create-only input: %+v", w)
	}
}

func TestWaitingGuest(t *testing.T) {
	desired := spec()
	run := spec()
	run.Cores, run.MemoryMB, run.Bridge = 1, 1024, "vmbr9"
	next := spec()
	next.Bridge = "vmbr9" // bridge does not wait: it is not in ColdStart
	st := GuestState{Spec: run, Pending: &next}
	got := WaitingGuest(desired, st)
	want := []wire.Change{
		{Attr: AttrCores, From: 1, To: 2, Risk: wire.RiskRead, WaitsForRestart: true},
		{Attr: AttrMemory, From: 1024, To: 2048, Risk: wire.RiskRead, WaitsForRestart: true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("WaitingGuest = %+v, want %+v", got, want)
	}
	// No pending view: nothing waits, it is drift.
	if w := WaitingGuest(desired, GuestState{Spec: run}); len(w) != 0 {
		t.Errorf("no pending view: %+v", w)
	}
	if !reflect.DeepEqual(ColdStart, []string{AttrCores, AttrMemory}) {
		t.Errorf("ColdStart = %v; a change of this list needs a decision line (D-195)", ColdStart)
	}
}
