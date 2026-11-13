package fake

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/Bernotese/plumb/internal/driver"
	"github.com/Bernotese/plumb/internal/wire"
)

const sum = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func setup(t *testing.T) (*Driver, driver.GuestID, driver.GuestSpec) {
	t.Helper()
	ctx := context.Background()
	d := New()
	ref, err := d.FetchImage(ctx, driver.ImageSpec{Name: "ubuntu", Kind: wire.KindVM, URL: "https://x/i.img", SHA256: sum})
	if err != nil {
		t.Fatal(err)
	}
	if again, _ := d.FetchImage(ctx, driver.ImageSpec{Name: "ubuntu", URL: "https://x/i.img", SHA256: sum}); again != ref {
		t.Errorf("FetchImage is not idempotent: %s vs %s", again, ref)
	}
	spec := driver.GuestSpec{Name: "web1", Kind: wire.KindVM, ImageName: "ubuntu", Image: ref, Cores: 2, MemoryMB: 2048, DiskGB: 20, Bridge: "vmbr0", IP: "dhcp", User: "plumb"}
	id, err := d.Create(ctx, spec, driver.Bootstrap{Token: "tok-1"})
	if err != nil {
		t.Fatal(err)
	}
	return d, id, spec
}

func TestLifecycle(t *testing.T) {
	ctx := context.Background()
	d, id, spec := setup(t)
	if id != "100" {
		t.Errorf("first id = %s", id)
	}
	st, err := d.Observe(ctx, id)
	if err != nil || st.Power != driver.PowerRunning || len(st.IPs) != 1 || st.Digest == "" {
		t.Fatalf("Observe = %+v, %v", st, err)
	}
	if b, ok := d.Bootstrap(id); !ok || b.Token != "tok-1" {
		t.Errorf("Bootstrap = %+v, %v", b, ok)
	}

	want := spec
	want.Cores, want.MemoryMB = 4, 4096
	changes, err := d.Diff(want, st)
	if err != nil || len(changes) != 2 {
		t.Fatalf("Diff = %+v, %v", changes, err)
	}
	// Changes may come back from a stored Plan (JSON round trip).
	raw, _ := json.Marshal(changes)
	var stored []wire.Change
	json.Unmarshal(raw, &stored)
	if err := d.Update(ctx, id, stored, nil); err != nil {
		t.Fatal(err)
	}
	// The guest runs: both values wait for the next cold start (T-056).
	after, _ := d.Observe(ctx, id)
	if after.Spec.Cores != 2 || after.Pending == nil || after.Pending.Cores != 4 || after.Pending.MemoryMB != 4096 || after.Digest == st.Digest {
		t.Errorf("after update: %+v", after)
	}
	if c, err := d.Diff(want, after); err != nil || len(c) != 0 {
		t.Errorf("second Diff = %+v, %v", c, err)
	}
	if w := driver.WaitingGuest(want, after); len(w) != 2 {
		t.Errorf("waiting = %+v", w)
	}
	if err := d.Power(ctx, id, driver.PowerRestart); err != nil {
		t.Fatal(err)
	}
	if after, _ = d.Observe(ctx, id); after.Spec.Cores != 4 || after.Spec.MemoryMB != 4096 || after.Pending != nil {
		t.Errorf("after restart: %+v", after)
	}

	if err := d.Power(ctx, id, driver.PowerStop); err != nil {
		t.Fatal(err)
	}
	if st, _ := d.Observe(ctx, id); st.Power != driver.PowerStopped || len(st.IPs) != 0 {
		t.Errorf("stopped guest: %+v", st)
	}
	if _, ok := d.Bootstrap(id); ok {
		t.Error("a stopped guest has no agent to read the token")
	}
	if err := d.Delete(ctx, id); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Observe(ctx, id); !errors.Is(err, driver.ErrNotFound) {
		t.Errorf("deleted guest: %v", err)
	}
}

func TestUpdateRules(t *testing.T) {
	ctx := context.Background()
	d, id, _ := setup(t)
	for name, c := range map[string]wire.Change{
		"create-only":      {Attr: driver.AttrIP, From: "dhcp", To: "192.0.2.2/24", Risk: wire.RiskDestructive},
		"shrink":           {Attr: driver.AttrDisk, From: 20, To: 2, Risk: wire.RiskDisruptive},
		"power":            {Attr: driver.AttrPower, From: "running", To: "stopped"},
		"unknown":          {Attr: "vga", To: "std"},
		"token with value": {Attr: driver.AttrEnrollToken, To: "secret", Sensitive: true},
		"bad cores":        {Attr: driver.AttrCores, To: "four"},
	} {
		before, _ := d.Observe(ctx, id)
		if err := d.Update(ctx, id, []wire.Change{{Attr: driver.AttrCores, To: 8}, c}, &driver.Bootstrap{Token: "x"}); err == nil {
			t.Errorf("%s: want an error", name)
		}
		if after, _ := d.Observe(ctx, id); after.Digest != before.Digest || after.Spec.Cores != before.Spec.Cores {
			t.Errorf("%s: a failed update changed the guest", name)
		}
	}
	var co *driver.CreateOnlyError
	if err := d.Update(ctx, id, []wire.Change{{Attr: driver.AttrIP, To: "192.0.2.2/24"}}, nil); !errors.As(err, &co) {
		t.Errorf("create-only update: %v", err)
	}
	if err := d.Update(ctx, id, []wire.Change{{Attr: driver.AttrEnrollToken, Sensitive: true, Risk: wire.RiskDisruptive}}, nil); err == nil {
		t.Error("token change without Bootstrap: want an error")
	}
}

// Re-enrollment: the new token becomes active at the next cold start.
func TestReenrollToken(t *testing.T) {
	ctx := context.Background()
	d, id, _ := setup(t)
	tokenChange := []wire.Change{{Attr: driver.AttrEnrollToken, Sensitive: true, Risk: wire.RiskDisruptive}}
	if err := d.Update(ctx, id, tokenChange, &driver.Bootstrap{Token: "tok-2"}); err != nil {
		t.Fatal(err)
	}
	if b, _ := d.Bootstrap(id); b.Token != "tok-1" {
		t.Errorf("token before restart = %s", b.Token)
	}
	if err := d.Power(ctx, id, driver.PowerRestart); err != nil {
		t.Fatal(err)
	}
	if b, _ := d.Bootstrap(id); b.Token != "tok-2" {
		t.Errorf("token after restart = %s", b.Token)
	}
}

func TestInjection(t *testing.T) {
	ctx := context.Background()
	d, id, spec := setup(t)
	boom := errors.New("task failed: storage full")
	d.Fail(OpObserve, boom)
	if _, err := d.Observe(ctx, id); !errors.Is(err, boom) {
		t.Errorf("injected failure: %v", err)
	}
	if _, err := d.Observe(ctx, id); err != nil {
		t.Errorf("a failure is one-shot: %v", err)
	}
	if d.Calls(OpObserve) != 2 {
		t.Errorf("calls = %d", d.Calls(OpObserve))
	}

	// An outside change: Proxmox UI sets 8 cores. The digest changes and
	// Diff shows the drift with from = guest now.
	before, _ := d.Observe(ctx, id)
	if err := d.Tamper(id, func(s *driver.GuestSpec) { s.Cores = 8 }); err != nil {
		t.Fatal(err)
	}
	after, _ := d.Observe(ctx, id)
	if after.Digest == before.Digest {
		t.Error("outside change did not change the digest")
	}
	c, err := d.Diff(spec, after)
	if err != nil || len(c) != 1 || c[0].From != 8 || c[0].To != 2 {
		t.Errorf("drift = %+v, %v", c, err)
	}

	// Async tasks respect the context.
	d.SetLatency(time.Second)
	short, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
	defer cancel()
	if _, err := d.Observe(short, id); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("slow task: %v", err)
	}
}

func TestCreateChecks(t *testing.T) {
	ctx := context.Background()
	d, _, spec := setup(t)
	if _, err := d.Create(ctx, spec, driver.Bootstrap{}); err == nil {
		t.Error("duplicate name: want an error")
	}
	spec.Name, spec.Image = "web2", "tpl-404"
	if _, err := d.Create(ctx, spec, driver.Bootstrap{}); !errors.Is(err, driver.ErrNotFound) {
		t.Errorf("unknown image: %v", err)
	}
	if _, err := d.FetchImage(ctx, driver.ImageSpec{Name: "x", URL: "http://x", SHA256: sum}); err == nil {
		t.Error("http image URL: want an error")
	}
	if _, err := d.FetchImage(ctx, driver.ImageSpec{Name: "x", URL: "https://x", SHA256: "abc"}); err == nil {
		t.Error("bad checksum: want an error")
	}
}

// TestPendingModel pins the fake's model of T-056: hotplug, a stuck
// bridge, an outside pending value, a start of a running guest, a cold
// restart that keeps the digest, and a restart that fails half way.
func TestPendingModel(t *testing.T) {
	ctx := context.Background()
	d, id, spec := setup(t)

	d.SetHotplug(true)
	if err := d.Update(ctx, id, []wire.Change{{Attr: driver.AttrMemory, To: 4096}}, nil); err != nil {
		t.Fatal(err)
	}
	if st, _ := d.Observe(ctx, id); st.Spec.MemoryMB != 4096 || st.Pending != nil {
		t.Errorf("hotplug: %+v", st)
	}
	d.SetHotplug(false)

	d.SetBridgeStuck(true)
	if err := d.Update(ctx, id, []wire.Change{{Attr: driver.AttrBridge, To: "vmbr9"}}, nil); err != nil {
		t.Fatal(err)
	}
	if st, _ := d.Observe(ctx, id); st.Spec.Bridge != spec.Bridge || st.Pending == nil || st.Pending.Bridge != "vmbr9" {
		t.Errorf("stuck bridge: %+v", st)
	}
	d.SetBridgeStuck(false)
	if err := d.Update(ctx, id, []wire.Change{{Attr: driver.AttrBridge, To: spec.Bridge}}, nil); err != nil {
		t.Fatal(err)
	}

	if err := d.TamperPending(id, func(s *driver.GuestSpec) { s.Cores = 8 }); err != nil {
		t.Fatal(err)
	}
	st, _ := d.Observe(ctx, id)
	if st.Spec.Cores != spec.Cores || st.Pending == nil || st.Pending.Cores != 8 {
		t.Errorf("outside pending: %+v", st)
	}
	// A start of a running guest applies nothing.
	if err := d.Power(ctx, id, driver.PowerStart); err != nil {
		t.Fatal(err)
	}
	if st2, _ := d.Observe(ctx, id); st2.Pending == nil {
		t.Error("a start of a running guest applied the pending value")
	}
	// A cold restart from outside that keeps the digest.
	if err := d.ColdRestart(id, true); err != nil {
		t.Fatal(err)
	}
	if st2, _ := d.Observe(ctx, id); st2.Spec.Cores != 8 || st2.Pending != nil || st2.Digest != st.Digest {
		t.Errorf("cold restart: %+v (digest before %s)", st2, st.Digest)
	}

	// A restart that fails at the start leaves the guest stopped.
	d.FailRestart("start", errors.New("start failed"))
	if err := d.Power(ctx, id, driver.PowerRestart); err == nil {
		t.Error("restart fault: no error")
	}
	if st, _ := d.Observe(ctx, id); st.Power != driver.PowerStopped {
		t.Errorf("after a failed start: %s", st.Power)
	}
	// A hook runs before the call.
	d.Hook(OpPower, func() { _ = d.StartOutside(id) })
	if err := d.Power(ctx, id, driver.PowerStart); err != nil {
		t.Fatal(err)
	}
	if st, _ := d.Observe(ctx, id); st.Power != driver.PowerRunning {
		t.Errorf("after the hook: %s", st.Power)
	}
}

// Slice 4 (D-299): tags and a disk grow, also on a stopped guest.
func TestUpdateDay2(t *testing.T) {
	ctx := context.Background()
	d, id, _ := setup(t)
	if err := d.Power(ctx, id, driver.PowerStop); err != nil {
		t.Fatal(err)
	}
	ch := []wire.Change{{Attr: driver.AttrTags, To: []any{"web", "Prod"}}, {Attr: driver.AttrDisk, From: 20, To: 30}}
	if err := d.Update(ctx, id, ch, nil); err != nil {
		t.Fatal(err)
	}
	st, _ := d.Observe(ctx, id)
	if st.Spec.DiskGB != 30 || len(st.Spec.Tags) != 2 || st.Spec.Tags[0] != "prod" || st.Pending != nil {
		t.Errorf("after the update: %+v pending %+v", st.Spec, st.Pending)
	}
	if err := d.Update(ctx, id, []wire.Change{{Attr: driver.AttrTags, To: nil}}, nil); err != nil {
		t.Fatal(err)
	}
	if st, _ := d.Observe(ctx, id); st.Spec.Tags != nil {
		t.Errorf("tags after a removal: %v", st.Spec.Tags)
	}
	refused := errors.New("403 Forbidden")
	d.RefuseTags(refused)
	if err := d.Update(ctx, id, []wire.Change{{Attr: driver.AttrTags, To: []string{"x"}}}, nil); !errors.Is(err, refused) {
		t.Errorf("refused tags: %v", err)
	}
}
