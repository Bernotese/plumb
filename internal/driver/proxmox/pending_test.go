package proxmox

import (
	"context"
	"strings"
	"testing"

	"github.com/Bernotese/plumb/internal/driver"
	"github.com/Bernotese/plumb/internal/wire"
)

// newRunningGuest makes a running guest in the fake PVE.
func newRunningGuest(t *testing.T) (*fakePVE, *Driver, driver.GuestID, driver.GuestSpec) {
	t.Helper()
	f := newFakePVE(t)
	d := f.driver(t)
	ctx := context.Background()
	ref, err := d.FetchImage(ctx, image)
	if err != nil {
		t.Fatal(err)
	}
	spec := guest(ref)
	id, err := d.Create(ctx, spec, driver.Bootstrap{Token: "tok-1"})
	if err != nil {
		t.Fatal(err)
	}
	if !f.vms[9001].running {
		t.Fatal("the guest does not run after Create")
	}
	return f, d, id, spec
}

// TestObservePendingFromOutside: a qm set --memory on a running VM is
// pending; Observe shows the running value in Spec and the new one in
// Pending, and the drift is "pending" (D-190).
func TestObservePendingFromOutside(t *testing.T) {
	f, d, id, spec := newRunningGuest(t)
	ctx := context.Background()
	f.mu.Lock()
	f.vms[9001].pending = map[string]string{"memory": "256"}
	f.mu.Unlock()

	st, err := d.Observe(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if st.Spec.MemoryMB != spec.MemoryMB || st.Pending == nil || st.Pending.MemoryMB != 256 || st.Pending.Cores != spec.Cores {
		t.Fatalf("observe: spec %d, pending %+v", st.Spec.MemoryMB, st.Pending)
	}
	if st.TokenPending {
		t.Error("TokenPending without a new token")
	}
	changes, err := d.Diff(spec, st)
	want := wire.Change{Attr: driver.AttrMemory, From: 256, To: spec.MemoryMB, Risk: wire.RiskReversible, Pending: true}
	if err != nil || len(changes) != 1 || changes[0] != want {
		t.Fatalf("diff: %+v %v", changes, err)
	}

	// Revert: a write of the running value removes the pending entry; no
	// power call (host check 2 proves it on the host).
	powerCalls := f.count("POST /nodes/pve/qemu/9001/status/")
	if err := d.Update(ctx, id, changes, nil); err != nil {
		t.Fatal(err)
	}
	st, err = d.Observe(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if st.Pending != nil || st.Spec.MemoryMB != spec.MemoryMB {
		t.Errorf("after revert: spec %d, pending %+v", st.Spec.MemoryMB, st.Pending)
	}
	if f.count("POST /nodes/pve/qemu/9001/status/") != powerCalls {
		t.Error("the revert sent a power action")
	}
}

// TestUpdateWaitsThenRestart: Plumb's own write of memory on a running
// guest waits; a restart (shutdown + start) makes it active.
func TestUpdateWaitsThenRestart(t *testing.T) {
	_, d, id, spec := newRunningGuest(t)
	ctx := context.Background()
	want := spec
	want.MemoryMB = 4096
	st, _ := d.Observe(ctx, id)
	changes, _ := d.Diff(want, st)
	if err := d.Update(ctx, id, changes, nil); err != nil {
		t.Fatal(err)
	}
	st, _ = d.Observe(ctx, id)
	if st.Spec.MemoryMB != spec.MemoryMB || st.Pending == nil || st.Pending.MemoryMB != 4096 {
		t.Fatalf("after update: spec %d, pending %+v", st.Spec.MemoryMB, st.Pending)
	}
	if w := driver.WaitingGuest(want, st); len(w) != 1 || w[0].Attr != driver.AttrMemory {
		t.Errorf("waiting: %+v", w)
	}
	if c := driver.DriftGuest(want, st); len(c) != 0 {
		t.Errorf("a waiting value is drift: %+v", c)
	}
	// A new token waits too, until the cold start.
	token := []wire.Change{{Attr: driver.AttrEnrollToken, Sensitive: true, Risk: wire.RiskDisruptive}}
	if err := d.Update(ctx, id, token, &driver.Bootstrap{Token: "tok-2"}); err != nil {
		t.Fatal(err)
	}
	if st, _ = d.Observe(ctx, id); !st.TokenPending {
		t.Error("TokenPending is false after a token write on a running guest")
	}
	if err := d.Power(ctx, id, driver.PowerRestart); err != nil {
		t.Fatal(err)
	}
	st, _ = d.Observe(ctx, id)
	if st.Spec.MemoryMB != 4096 || st.Pending != nil || st.TokenPending {
		t.Errorf("after restart: spec %d, pending %+v, token pending %v", st.Spec.MemoryMB, st.Pending, st.TokenPending)
	}
}

// TestObserveDigestSkew: both reads must show one state of the config
// (same digest, host check 1); a write between the reads makes the
// driver read again, up to viewTries.
func TestObserveDigestSkew(t *testing.T) {
	f, d, id, _ := newRunningGuest(t)
	ctx := context.Background()
	f.mu.Lock()
	f.writeDuringRead = viewTries - 1
	f.mu.Unlock()
	before := f.countExact("GET /nodes/pve/qemu/9001/config")
	if _, err := d.Observe(ctx, id); err != nil {
		t.Errorf("writes in fewer reads than the tries: %v", err)
	}
	// own reads once; views reads twice per try.
	if n := f.countExact("GET /nodes/pve/qemu/9001/config") - before; n != 1+2*viewTries {
		t.Errorf("config reads = %d, want %d", n, 1+2*viewTries)
	}
	f.mu.Lock()
	f.writeDuringRead = viewTries
	f.mu.Unlock()
	if _, err := d.Observe(ctx, id); err == nil || !strings.Contains(err.Error(), "changed during each") {
		t.Errorf("a write during every read: %v", err)
	}
}

// TestUpdateKeepsPending: when a write of the running value does not
// remove the pending entry (host check 2 open), Observe still shows it,
// so the engine can see that the revert did not work (review B1).
func TestUpdateKeepsPending(t *testing.T) {
	f, d, id, spec := newRunningGuest(t)
	ctx := context.Background()
	f.mu.Lock()
	f.keepPending = true
	f.vms[9001].pending = map[string]string{"memory": "256"}
	f.mu.Unlock()
	st, _ := d.Observe(ctx, id)
	changes, _ := d.Diff(spec, st)
	if err := d.Update(ctx, id, changes, nil); err != nil {
		t.Fatal(err)
	}
	st, _ = d.Observe(ctx, id)
	if st.Pending == nil || st.Pending.MemoryMB != 256 || st.Spec.MemoryMB != spec.MemoryMB {
		t.Errorf("after the write: spec %d, pending %+v", st.Spec.MemoryMB, st.Pending)
	}
}

// TestRestartTimeoutPinned: the Plan text says "hard power-off after 2
// minutes" (D-194); the default and the shutdown call must agree.
func TestRestartTimeoutPinned(t *testing.T) {
	if DefaultShutdownTimeout.Seconds() != 120 {
		t.Fatalf("DefaultShutdownTimeout = %v; the Plan text names 2 minutes", DefaultShutdownTimeout)
	}
	f, _, id, _ := newRunningGuest(t)
	d, err := New(Config{Client: f.client(t), Node: fakeNode, Pool: fakePool, Nameservers: []string{"192.0.2.53"}, SearchDomain: "plumb.test", Bridge: "vmbr0",
		DiskStorage: "local-lvm", ImageStorage: "local", SnippetStorage: "plumb-snippets", Snippet: "plumb-vendor.yaml", VMIDMin: 9000, VMIDMax: 9099})
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Power(context.Background(), id, driver.PowerRestart); err != nil {
		t.Fatal(err)
	}
	p := f.lastParams("POST /nodes/pve/qemu/9001/status/shutdown")
	if p.Get("timeout") != "120" || p.Get("forceStop") != "1" {
		t.Errorf("shutdown params: %v", p)
	}
}
