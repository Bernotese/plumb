package proxmox

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/Bernotese/plumb/internal/driver"
	"github.com/Bernotese/plumb/internal/event"
	"github.com/Bernotese/plumb/internal/link"
	"github.com/Bernotese/plumb/internal/plan"
	"github.com/Bernotese/plumb/internal/state"
	"github.com/Bernotese/plumb/internal/store"
)

// QA of the adoption end to end against the fake Proxmox API (T-195 B6 for
// T-193; D-340, D-386, D-388, review R3 of #290): the real driver behind
// the production guard, a real store, link and engine.

type qaLog struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *qaLog) Write(p []byte) (int, error) { s.mu.Lock(); defer s.mu.Unlock(); return s.b.Write(p) }
func (s *qaLog) String() string              { s.mu.Lock(); defer s.mu.Unlock(); return s.b.String() }

type adoptE2E struct {
	ctx  context.Context
	f    *fakePVE
	st   *store.Store
	db   string
	eng  *plan.Engine
	logs *qaLog
}

func newAdoptE2E(t *testing.T) *adoptE2E {
	t.Helper()
	a := &adoptE2E{ctx: context.Background(), f: newFakePVE(t), logs: &qaLog{}}
	a.db = filepath.Join(t.TempDir(), "plumb.db")
	st, err := store.Open(a.ctx, a.db)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	a.st = st
	log := slog.New(slog.NewTextHandler(a.logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	// Code without its own logger writes to the default one: capture it
	// too (no test of this package runs in parallel).
	old := slog.Default()
	slog.SetDefault(log)
	t.Cleanup(func() { slog.SetDefault(old) })
	ev := event.New(st)
	lk, err := link.New(link.Config{Store: st, Events: ev, Dir: filepath.Join(t.TempDir(), "link"), Logger: log})
	if err != nil {
		t.Fatal(err)
	}
	drv := driver.ReadOnlyWhen(a.f.driver(t), func() bool { return false })
	if a.eng, err = plan.New(plan.Config{Store: st, Events: ev, Agents: lk, Driver: drv, Logger: log}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.eng.Close)
	if err := a.eng.SyncAdopted(a.ctx); err != nil {
		t.Fatal(err)
	}
	return a
}

// adopt lists, proposes and approves the adoption of a pool guest. before
// runs between the Plan and the approval. It returns the Run status, or
// the approval error.
func (a *adoptE2E) adopt(t *testing.T, vmid int, name string, before func()) string {
	t.Helper()
	us, err := a.eng.UnmanagedGuests(a.ctx)
	if err != nil {
		t.Fatal(err)
	}
	digest := ""
	for _, u := range us {
		if u.ID == driver.GuestID(strconv.Itoa(vmid)) {
			if !u.Adoptable() {
				t.Fatalf("guest %d not adoptable: %v", vmid, u.Reasons)
			}
			digest = u.Digest
		}
	}
	if digest == "" {
		t.Fatalf("guest %d not in the unmanaged list", vmid)
	}
	p, err := a.eng.ProposeAdopt(a.ctx, plan.AdoptRequest{Target: name, Node: fakeNode, VMID: vmid, Digest: digest}, "user:admin", "")
	if err != nil {
		t.Fatal(err)
	}
	if before != nil {
		before()
	}
	run, err := a.eng.Approve(a.ctx, p.ID, "user:admin", nil)
	if err != nil {
		return "refused: " + err.Error()
	}
	a.eng.Wait()
	r, err := a.st.GetRun(a.ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	return string(r.Status)
}

// noMark greps the DB file (and its WAL), every event and the log.
func (a *adoptE2E) noMark(t *testing.T, mark string) {
	t.Helper()
	for _, p := range []string{a.db, a.db + "-wal"} {
		b, err := os.ReadFile(p)
		if err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		if bytes.Contains(b, []byte(mark)) {
			t.Fatalf("%s holds a secret value of the guest config", filepath.Base(p))
		}
	}
	evs, err := a.st.EventsAfter(a.ctx, 0, 100000)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range evs {
		if bytes.Contains(e.Data, []byte(mark)) {
			t.Fatalf("event %s holds a secret value", e.Type)
		}
	}
	if strings.Contains(a.logs.String(), mark) {
		t.Fatal("the log holds a secret value")
	}
}

// TestAdoptQASecretsNowhere: cipassword, sshkeys, description of a VM and
// env of an LXC never reach the DB file, an event or the log: not by the
// unmanaged list, the adopt Plan, the Run, the day-2 poll, nor the unbind.
func TestAdoptQASecretsNowhere(t *testing.T) {
	a := newAdoptE2E(t)
	const mark = "Q4-SECRET-MARK"
	a.f.addVM(150, fakePool, vmCfg(map[string]string{"cipassword": "$5$" + mark, "sshkeys": "ssh-ed25519%20AAAA" + mark,
		"description": "root pw " + mark, "digest": ""}), true)
	a.f.addCT(151, fakePool, ctCfg(map[string]string{"env": "DB_PASSWORD=" + mark, "description": "db " + mark, "digest": ""}), true)

	if s := a.adopt(t, 150, "wiki", nil); s != "succeeded" {
		t.Fatalf("adopt VM: %s", s)
	}
	if s := a.adopt(t, 151, "old-db", nil); s != "succeeded" {
		t.Fatalf("adopt LXC: %s", s)
	}
	// Day 2: a person changes the VM; the poll reads its full config.
	a.f.mu.Lock()
	a.f.vms[150].cfg["cores"] = "3"
	a.f.mu.Unlock()
	if err := a.eng.CheckGuests(a.ctx); err != nil {
		t.Fatal(err)
	}
	// The delete of an adopted Target only unbinds.
	p, err := a.eng.Propose(a.ctx, plan.Input{Author: "user:admin", Reason: "unbind",
		Proposal: plan.Proposal{Delete: []plan.DocKey{{Kind: state.KindTarget, Name: "wiki"}}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.eng.Approve(a.ctx, p.ID, "user:admin", nil); err != nil {
		t.Fatal(err)
	}
	a.eng.Wait()
	if n := a.f.count("DELETE "); n != 0 {
		t.Fatalf("the unbind sent %d Proxmox DELETE calls", n)
	}
	a.f.mu.Lock()
	_, ok := a.f.vms[150]
	a.f.mu.Unlock()
	if !ok {
		t.Fatal("the unbound guest is gone")
	}
	a.noMark(t, mark)
}

// TestAdoptQAGuestSwapped: a guest that changes between the Plan and the
// Run (config changed, renamed, VMID reused, left the pool) is not bound,
// and Plumb writes nothing to Proxmox.
func TestAdoptQAGuestSwapped(t *testing.T) {
	for _, c := range []struct {
		name   string
		change func(f *fakePVE)
	}{
		{"config changed", func(f *fakePVE) { f.mu.Lock(); f.vms[150].cfg["cores"] = "8"; f.mu.Unlock() }},
		{"renamed", func(f *fakePVE) { f.mu.Lock(); f.vms[150].cfg["name"] = "other"; f.mu.Unlock() }},
		{"VMID reused", func(f *fakePVE) {
			f.mu.Lock()
			delete(f.vms, 150)
			f.mu.Unlock()
			f.addVM(150, fakePool, vmCfg(map[string]string{"cores": "4", "digest": ""}), true)
		}},
		{"left the pool", func(f *fakePVE) { f.mu.Lock(); f.vms[150].pool = ""; f.mu.Unlock() }},
	} {
		t.Run(c.name, func(t *testing.T) {
			a := newAdoptE2E(t)
			a.f.addVM(150, fakePool, vmCfg(map[string]string{"digest": ""}), true)
			writes := func() int { return a.f.count("POST ") + a.f.count("PUT ") + a.f.count("DELETE ") }
			before := 0
			if s := a.adopt(t, 150, "wiki", func() { c.change(a.f); before = writes() }); s == "succeeded" {
				t.Fatal("a changed guest was adopted")
			}
			if _, err := a.st.GetBinding(a.ctx, state.KindTarget, "wiki"); err == nil {
				t.Fatal("the changed guest is bound")
			}
			if n := writes() - before; n != 0 {
				t.Fatalf("%d Proxmox writes to the changed guest", n)
			}
		})
	}
}
