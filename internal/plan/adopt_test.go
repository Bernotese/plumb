package plan

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Bernotese/plumb/internal/driver"
	"github.com/Bernotese/plumb/internal/driver/fake"
	"github.com/Bernotese/plumb/internal/link"
	"github.com/Bernotese/plumb/internal/state"
	"github.com/Bernotese/plumb/internal/store"
	"github.com/Bernotese/plumb/internal/wire"
)

type adoptEnv struct {
	ctx context.Context
	st  *store.Store
	drv *fake.Driver
	e   *Engine
}

func newAdoptEnv(t *testing.T) *adoptEnv {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "adopt.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	lk, err := link.New(link.Config{Store: st, Dir: filepath.Join(t.TempDir(), "link")})
	if err != nil {
		t.Fatal(err)
	}
	drv := fake.New()
	e, err := New(Config{Store: st, Agents: lk, Driver: drv, EnrollTimeout: 2 * time.Second, ApplyTimeout: 2 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(e.Close)
	return &adoptEnv{ctx: ctx, st: st, drv: drv, e: e}
}

func (a *adoptEnv) person(name string, reasons ...string) (driver.GuestID, driver.Unmanaged) {
	u := driver.Unmanaged{Node: "pve", Name: name, Kind: wire.KindVM, Reasons: reasons,
		Warnings: []string{driver.WarnNotSealed}, Guest: map[string]any{"cores": 2, "memory": 1024, "disk": 10, "bridge": "vmbr0"}}
	id := a.drv.AddUnmanaged(u, driver.GuestSpec{Kind: wire.KindVM, Cores: 2, MemoryMB: 1024, DiskGB: 10, Bridge: "vmbr0"})
	u, _ = a.drv.InspectGuest(a.ctx, id)
	return id, u
}

func vmidOf(t *testing.T, id driver.GuestID) int {
	t.Helper()
	var n int
	if err := json.Unmarshal([]byte(id), &n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestUnmanagedGuests(t *testing.T) {
	a := newAdoptEnv(t)
	wiki, _ := a.person("wiki")
	odd, _ := a.person("odd", driver.AdoptPrivileged)
	held, _ := a.person("held")
	if err := a.st.PutBinding(a.ctx, store.Binding{Kind: state.KindTarget, Name: "held", Ref: string(held)}); err != nil {
		t.Fatal(err)
	}
	list, err := a.e.UnmanagedGuests(a.ctx)
	if err != nil {
		t.Fatal(err)
	}
	got := map[driver.GuestID]bool{}
	for _, u := range list {
		got[u.ID] = u.Adoptable()
	}
	if len(got) != 2 || !got[wiki] || got[odd] {
		t.Fatalf("unmanaged %v (wiki %s, odd %s, held %s)", got, wiki, odd, held)
	}
}

func TestAdoptDocChecks(t *testing.T) {
	a := newAdoptEnv(t)
	wiki, u := a.person("wiki")
	req := AdoptRequest{Target: "wiki", Node: "pve", VMID: vmidOf(t, wiki), Digest: u.Digest}
	d, err := a.e.AdoptDoc(a.ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	var spec map[string]any
	json.Unmarshal(d.Spec, &spec)
	if _, ok := spec["image"]; ok || spec["adopted"] == nil || d.Name != "wiki" {
		t.Fatalf("doc %s", d.Spec)
	}
	if strings.Contains(string(d.Spec), "ssh_keys") {
		t.Fatalf("ssh_keys in an adopted Target: %s", d.Spec)
	}
	if _, err := state.Validate([]state.Doc{d}); err != nil {
		t.Fatalf("the adopted doc does not validate: %v", err)
	}

	for _, c := range []struct {
		name string
		mod  func(r *AdoptRequest)
		want error
	}{
		{"digest", func(r *AdoptRequest) { r.Digest = strings.Repeat("0", 40) }, ErrGuestChanged},
		{"node", func(r *AdoptRequest) { r.Node = "pve2" }, ErrGuestChanged},
		{"other name", func(r *AdoptRequest) { r.Target = "wiki2" }, ErrGuestChanged},
		{"not in the pool", func(r *AdoptRequest) { r.VMID = 9999 }, ErrGuestChanged},
	} {
		r := req
		c.mod(&r)
		if _, err := a.e.AdoptDoc(a.ctx, r); !errors.Is(err, c.want) {
			t.Errorf("%s: %v, want %v", c.name, err, c.want)
		}
	}
	odd, ou := a.person("odd", driver.AdoptHostDevice)
	if _, err := a.e.AdoptDoc(a.ctx, AdoptRequest{Target: "odd", Node: "pve", VMID: vmidOf(t, odd), Digest: ou.Digest}); !errors.Is(err, ErrNotAdoptable) {
		t.Errorf("not adoptable: %v", err)
	}
	if err := a.st.PutBinding(a.ctx, store.Binding{Kind: state.KindTarget, Name: "x", Ref: string(wiki)}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.e.AdoptDoc(a.ctx, req); !errors.Is(err, ErrGuestBound) {
		t.Errorf("bound: %v", err)
	}
}

// ARCH §19: a guest swapped or renamed between Plan and Run is refused,
// and nothing is bound. A good Run binds and tells the driver the ref.
func TestAdoptBind(t *testing.T) {
	a := newAdoptEnv(t)
	wiki, u := a.person("wiki")
	req := AdoptRequest{Target: "wiki", Node: "pve", VMID: vmidOf(t, wiki), Digest: u.Digest}
	d, err := a.e.AdoptDoc(a.ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	set, err := state.Validate([]state.Doc{d})
	if err != nil {
		t.Fatal(err)
	}
	rows, err := state.Rows(set)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.st.CommitRevision(a.ctx, store.NewRevision{Author: "user:qa", Docs: rows}); err != nil {
		t.Fatal(err)
	}

	a.drv.RenameGuest(wiki, "wiki")
	a.drv.RenameGuest(wiki, "other")
	if err := a.e.adoptBind(a.ctx, req); !errors.Is(err, ErrGuestChanged) {
		t.Fatalf("renamed between Plan and Run: %v", err)
	}
	if _, err := a.st.GetBinding(a.ctx, state.KindTarget, "wiki"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("a binding after a refused Run: %v", err)
	}

	b, bu := a.person("wiki")
	req = AdoptRequest{Target: "wiki", Node: "pve", VMID: vmidOf(t, b), Digest: bu.Digest}
	// M6 of #299: a bind that fails after the driver learned the ref drops
	// the ref again; nothing is bound.
	a.drv.Fail(fake.OpObserve, errors.New("observe failed"))
	d0, _ := a.e.AdoptDoc(a.ctx, req)
	set0, _ := state.Validate([]state.Doc{d0})
	rows0, _ := state.Rows(set0)
	if _, err := a.st.CommitRevision(a.ctx, store.NewRevision{Author: "user:qa", Docs: rows0}); err != nil {
		t.Fatal(err)
	}
	if err := a.e.adoptBind(a.ctx, req); err == nil {
		t.Fatal("the bind did not fail")
	}
	if got := a.drv.Adopted(); len(got) != 0 {
		t.Fatalf("a failed bind left adopted refs: %v", got)
	}
	d, _ = a.e.AdoptDoc(a.ctx, req)
	set, _ = state.Validate([]state.Doc{d})
	rows, _ = state.Rows(set)
	if _, err := a.st.CommitRevision(a.ctx, store.NewRevision{Author: "user:qa", Docs: rows}); err != nil {
		t.Fatal(err)
	}
	if err := a.e.adoptBind(a.ctx, req); err != nil {
		t.Fatal(err)
	}
	bind, err := a.st.GetBinding(a.ctx, state.KindTarget, "wiki")
	if err != nil || bind.Ref != string(b) {
		t.Fatalf("binding %+v %v", bind, err)
	}
	if got := a.drv.Adopted(); got[b] != "wiki" || len(got) != 1 {
		t.Fatalf("adopted refs %v", got)
	}
}

// The whole flow (ARCH §19): the adopt Plan is human-only and binds the
// guest without a Proxmox write; the delete of the adopted Target keeps
// the guest (unbind); only delete_guest, human-only and destructive,
// deletes it.
func TestAdoptFlow(t *testing.T) {
	a := newAdoptEnv(t)
	id, u := a.person("wiki")
	req := AdoptRequest{Target: "wiki", Node: "pve", VMID: vmidOf(t, id), Digest: u.Digest}
	p, err := a.e.ProposeAdopt(a.ctx, req, "token:tok_qa", "")
	if err != nil {
		t.Fatal(err)
	}
	if p.Origin != OriginAdopt || !slices.Contains(p.HumanOnly, HumanOnlyAcceptGuestData) {
		t.Fatalf("adopt plan: origin %s human_only %v", p.Origin, p.HumanOnly)
	}
	writes := a.drv.Calls(fake.OpUpdate) + a.drv.Calls(fake.OpCreate) + a.drv.Calls(fake.OpDelete)
	if _, err := a.e.Approve(a.ctx, p.ID, "user:admin", nil); err != nil {
		t.Fatal(err)
	}
	a.e.Wait()
	run := a.lastRun(t)
	if run.Status != "succeeded" {
		steps, _ := a.st.Steps(a.ctx, run.ID)
		t.Fatalf("adopt run %s: %+v", run.Status, steps)
	}
	if b, err := a.st.GetBinding(a.ctx, state.KindTarget, "wiki"); err != nil || b.Ref != string(id) {
		t.Fatalf("binding %+v %v", b, err)
	}
	if n := a.drv.Calls(fake.OpUpdate) + a.drv.Calls(fake.OpCreate) + a.drv.Calls(fake.OpDelete); n != writes {
		t.Fatalf("the adoption wrote to the hypervisor: %d", n-writes)
	}

	// A1 of #299: adopted.vmid and adopted.node of a bound Target cannot
	// change, and adopted cannot go (a token could else point the delete
	// text at another guest).
	_, set, _ := a.e.base(a.ctx)
	var bound state.Doc
	for _, d := range set.Docs() {
		if d.Kind == state.KindTarget && d.Name == "wiki" {
			bound = d
		}
	}
	other0, _ := a.person("spare")
	for _, spec := range []string{
		strings.Replace(string(bound.Spec), `"vmid":`+string(id), `"vmid":`+string(other0), 1),
		strings.Replace(string(bound.Spec), `"node":"pve"`, `"node":"pve2"`, 1),
	} {
		if spec == string(bound.Spec) {
			t.Fatalf("probe did not change the doc: %s", spec)
		}
		changed := state.Doc{Kind: bound.Kind, Name: bound.Name, Spec: []byte(spec)}
		if _, err := a.e.Propose(a.ctx, Input{Author: "token:tok_qa", Reason: "move", Proposal: Proposal{Put: []state.Doc{changed}}}); !errors.Is(err, ErrAdoptedChange) {
			t.Fatalf("adopted change %s: %v", spec, err)
		}
	}

	// A plain proposal of an adopted Target (no adopt request) is refused.
	other, ou := a.person("other")
	doc, _ := a.e.AdoptDoc(a.ctx, AdoptRequest{Target: "other", Node: "pve", VMID: vmidOf(t, other), Digest: ou.Digest})
	if _, err := a.e.Propose(a.ctx, Input{Author: "token:tok_qa", Reason: "sneak", Proposal: Proposal{Put: []state.Doc{doc}}}); !errors.Is(err, ErrNotAdoptable) {
		t.Fatalf("adopted Target without the adopt route: %v", err)
	}

	// Delete keeps the guest (the agent is offline: the Run says what a
	// person does).
	p, err = a.e.Propose(a.ctx, Input{Author: "user:admin", Reason: "drop", Proposal: Proposal{Delete: []DocKey{{Kind: state.KindTarget, Name: "wiki"}}}})
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(p.HumanOnly, HumanOnlyAdoptedGuestDelete) || !strings.Contains(strings.Join(adoptNotes(t, p), " "), "stays") {
		t.Fatalf("unbind plan: %v %v", p.HumanOnly, adoptNotes(t, p))
	}
	if _, err := a.e.Approve(a.ctx, p.ID, "user:admin", nil); err != nil {
		t.Fatal(err)
	}
	a.e.Wait()
	if n := a.drv.Calls(fake.OpDelete); n != 0 {
		t.Fatalf("the unbind deleted the guest (%d calls)", n)
	}
	if _, err := a.st.GetBinding(a.ctx, state.KindTarget, "wiki"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("binding after unbind: %v", err)
	}
	if len(a.drv.Adopted()) != 0 {
		t.Fatalf("adopted refs after unbind: %v", a.drv.Adopted())
	}

	// Adopt again, set delete_guest (human-only, destructive), delete.
	_, u = a.person("wiki2")
	id2 := u.ID
	p, err = a.e.ProposeAdopt(a.ctx, AdoptRequest{Target: "wiki2", Node: "pve", VMID: vmidOf(t, id2), Digest: u.Digest}, "user:admin", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.e.Approve(a.ctx, p.ID, "user:admin", nil); err != nil {
		t.Fatal(err)
	}
	a.e.Wait()
	_, set, _ = a.e.base(a.ctx)
	var withDel state.Doc
	for _, d := range set.Docs() {
		if d.Kind == state.KindTarget && d.Name == "wiki2" {
			withDel = d
		}
	}
	withDel.Spec = []byte(strings.Replace(string(withDel.Spec), `"delete_guest":false`, `"delete_guest":true`, 1))
	p, err = a.e.Propose(a.ctx, Input{Author: "user:admin", Reason: "may delete", Proposal: Proposal{Put: []state.Doc{withDel}}})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(p.HumanOnly, HumanOnlyAdoptedGuestDelete) || p.Risk != "destructive" {
		t.Fatalf("delete_guest plan: %v %s", p.HumanOnly, p.Risk)
	}
	if _, err := a.e.Approve(a.ctx, p.ID, "user:admin", nil); err != nil {
		t.Fatal(err)
	}
	a.e.Wait()
	// A revision whose vmid differs from the binding (an old revision):
	// the delete Plan still names the bound guest (A1 of #299).
	_, set, _ = a.e.base(a.ctx)
	var docs []state.Doc
	for _, d := range set.Docs() {
		if d.Kind == state.KindTarget && d.Name == "wiki2" {
			d.Spec = []byte(strings.Replace(string(d.Spec), `"vmid":`+string(id2), `"vmid":4242`, 1))
		}
		docs = append(docs, d)
	}
	skew, err := state.Validate(docs)
	if err != nil {
		t.Fatal(err)
	}
	rows, _ := state.Rows(skew)
	if _, err := a.st.CommitRevision(a.ctx, store.NewRevision{Author: "user:qa", Docs: rows}); err != nil {
		t.Fatal(err)
	}
	p, err = a.e.Propose(a.ctx, Input{Author: "user:admin", Reason: "delete", Proposal: Proposal{Delete: []DocKey{{Kind: state.KindTarget, Name: "wiki2"}}}})
	if err != nil {
		t.Fatal(err)
	}
	if notes := strings.Join(adoptNotes(t, p), " "); !slices.Contains(p.HumanOnly, HumanOnlyAdoptedGuestDelete) ||
		!strings.Contains(notes, "deletes the Proxmox guest "+string(id2)+" ") || strings.Contains(notes, "4242") {
		t.Fatalf("delete plan: %v %v", p.HumanOnly, adoptNotes(t, p))
	}
	if _, err := a.e.Approve(a.ctx, p.ID, "user:admin", nil); err != nil {
		t.Fatal(err)
	}
	a.e.Wait()
	if n := a.drv.Calls(fake.OpDelete); n != 1 {
		t.Fatalf("delete_guest: %d delete calls, want 1", n)
	}
}

func (a *adoptEnv) lastRun(t *testing.T) store.Run {
	t.Helper()
	runs, err := a.st.ListRuns(a.ctx, 0, 1)
	if err != nil || len(runs) == 0 {
		t.Fatalf("runs: %v", err)
	}
	return runs[0]
}

func adoptNotes(t *testing.T, p store.Plan) []string {
	t.Helper()
	var prop Proposal
	if err := json.Unmarshal(p.Proposal, &prop); err != nil {
		t.Fatal(err)
	}
	return prop.Notes
}

// Review L2 of #299: when the read after the bind fails, the extra ref
// stays only if the bind worked.
func TestAdoptBindSecondReadFails(t *testing.T) {
	for _, bindFails := range []bool{true, false} {
		a := newAdoptEnv(t)
		id, u := a.person("wiki")
		req := AdoptRequest{Target: "wiki", Node: "pve", VMID: vmidOf(t, id), Digest: u.Digest}
		d, _ := a.e.AdoptDoc(a.ctx, req)
		set, _ := state.Validate([]state.Doc{d})
		rows, _ := state.Rows(set)
		if _, err := a.st.CommitRevision(a.ctx, store.NewRevision{Author: "user:qa", Docs: rows}); err != nil {
			t.Fatal(err)
		}
		if bindFails {
			a.drv.Fail(fake.OpObserve, errors.New("observe failed"))
		}
		calls := 0
		orig := adoptedRefsFn
		adoptedRefsFn = func(e *Engine, ctx context.Context) (map[driver.GuestID]string, error) {
			calls++
			if calls == 2 {
				return nil, errors.New("store read failed")
			}
			return orig(e, ctx)
		}
		err := a.e.adoptBind(a.ctx, req)
		adoptedRefsFn = orig
		if err == nil || calls != 2 {
			t.Fatalf("bindFails=%v: err %v, %d reads", bindFails, err, calls)
		}
		got := a.drv.Adopted()
		if bindFails && len(got) != 0 {
			t.Fatalf("a failed bind kept the ref: %v", got)
		}
		if !bindFails && got[id] != "wiki" {
			t.Fatalf("a good bind lost its ref: %v", got)
		}
	}
}

// TestAdoptStaleHostSequence (T-204, host finding F5 of T-192, D-395):
// the guest is renamed between the adopt Plan and its approval. The Run
// binds nothing and the document stays as a not-adopted Target. Plans that
// touch it do nothing for it; a second claim of the guest is refused; an
// adopt under the new name binds and removes the stale Target in one Plan;
// the delete of a stale Target makes no hypervisor call.
func TestAdoptStaleHostSequence(t *testing.T) {
	a := newAdoptEnv(t)
	writes := func() int {
		return a.drv.Calls(fake.OpUpdate) + a.drv.Calls(fake.OpCreate) + a.drv.Calls(fake.OpDelete) + a.drv.Calls(fake.OpPower)
	}
	id, u := a.person("wiki")
	p, err := a.e.ProposeAdopt(a.ctx, AdoptRequest{Target: "wiki", Node: "pve", VMID: vmidOf(t, id), Digest: u.Digest}, "user:admin", "")
	if err != nil {
		t.Fatal(err)
	}
	a.drv.RenameGuest(id, "wiki-new") // a person renames it before the approval
	if _, err := a.e.Approve(a.ctx, p.ID, "user:admin", nil); err != nil {
		t.Fatal(err)
	}
	a.e.Wait()
	if run := a.lastRun(t); run.Status != "failed" {
		t.Fatalf("run %s", run.Status)
	}
	if _, err := a.st.GetBinding(a.ctx, state.KindTarget, "wiki"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("binding: %v", err)
	}
	before := writes()

	// An unrelated Plan that touches the stale Target (named, or its
	// document changed by a token) plans and runs nothing for it.
	_, set, _ := a.e.base(a.ctx)
	var stale state.Doc
	for _, d := range set.Docs() {
		if d.Kind == state.KindTarget && d.Name == "wiki" {
			stale = d
		}
	}
	more := state.Doc{Kind: stale.Kind, Name: stale.Name, Spec: []byte(strings.Replace(string(stale.Spec), `"cores":2`, `"cores":4`, 1))}
	// A Block on the stale Target: without the skip, its Run would wait
	// for an agent that does not exist and fail.
	motd := state.Doc{Kind: state.KindBlock, Name: "motd", Spec: []byte(`{"resources":[{"file":{"path":"/etc/motd","content":"x"}}]}`)}
	withBlock := state.Doc{Kind: stale.Kind, Name: stale.Name, Spec: []byte(strings.Replace(string(stale.Spec), `"type":"vm"`, `"type":"vm","blocks":[{"use":"motd"}]`, 1))}
	for _, in := range []Input{
		{Author: "token:tok_qa", Reason: "touch", Proposal: Proposal{Targets: []string{"wiki"}}},
		{Author: "token:tok_qa", Reason: "cores", Proposal: Proposal{Put: []state.Doc{more}}},
		{Author: "token:tok_qa", Reason: "block", Proposal: Proposal{Put: []state.Doc{motd, withBlock}}},
	} {
		p, err := a.e.Propose(a.ctx, in)
		if err != nil {
			t.Fatalf("%s: %v", in.Reason, err)
		}
		var tps []TargetPlan
		json.Unmarshal(p.Changes, &tps)
		if len(tps) != 1 || !tps[0].NotAdopted || tps[0].Guest != "" || len(tps[0].Resources) != 0 ||
			!strings.Contains(strings.Join(adoptNotes(t, p), " "), "is not adopted") {
			t.Fatalf("%s: %s %v", in.Reason, p.Changes, adoptNotes(t, p))
		}
		if _, err := a.e.Approve(a.ctx, p.ID, "user:admin", nil); err != nil {
			t.Fatal(err)
		}
		a.e.Wait()
		if run := a.lastRun(t); run.Status != "succeeded" {
			t.Fatalf("%s: the Run of a Plan that does nothing for the stale Target is %s", in.Reason, run.Status)
		}
		if _, err := a.st.GetBinding(a.ctx, state.KindTarget, "wiki"); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("%s bound the stale Target", in.Reason)
		}
	}
	if writes() != before {
		t.Fatalf("Plans for the stale Target wrote to the hypervisor: %d", writes()-before)
	}

	// Rule 1: a second Target never claims the same guest.
	nu, _ := a.drv.InspectGuest(a.ctx, id)
	other, err := a.e.AdoptDoc(a.ctx, AdoptRequest{Target: "wiki-new", Node: "pve", VMID: vmidOf(t, id), Digest: nu.Digest})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.e.Propose(a.ctx, Input{Author: "user:admin", Reason: "second claim", Origin: OriginAdopt,
		Proposal: Proposal{Put: []state.Doc{other}, Adopt: []AdoptRequest{{Target: "wiki-new", Node: "pve", VMID: vmidOf(t, id), Digest: nu.Digest}}}}); !errors.Is(err, ErrAdoptClaimed) {
		t.Fatalf("second claim: %v", err)
	}

	// Adopt again under the new name: the same Plan removes the stale one.
	p, err = a.e.ProposeAdopt(a.ctx, AdoptRequest{Target: "wiki-new", Node: "pve", VMID: vmidOf(t, id), Digest: nu.Digest}, "user:admin", "")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(p.HumanOnly, HumanOnlyAcceptGuestData) {
		t.Fatalf("the new adopt is not human-only: %v", p.HumanOnly)
	}
	if _, err := a.e.Approve(a.ctx, p.ID, "user:admin", nil); err != nil {
		t.Fatal(err)
	}
	a.e.Wait()
	if b, err := a.st.GetBinding(a.ctx, state.KindTarget, "wiki-new"); err != nil || b.Ref != string(id) {
		t.Fatalf("binding %+v %v", b, err)
	}
	_, set, _ = a.e.base(a.ctx)
	if slices.Contains(set.Targets(), "wiki") {
		t.Fatal("the stale Target stays")
	}
	if writes() != before {
		t.Fatal("the adoption wrote to the hypervisor")
	}
}

// D-395: adopt again under the same name (the guest was renamed back)
// replaces the stale Target; the delete of a stale Target is no
// hypervisor call and never touches the guest.
func TestAdoptStaleSameNameAndDelete(t *testing.T) {
	a := newAdoptEnv(t)
	id, u := a.person("wiki")
	p, _ := a.e.ProposeAdopt(a.ctx, AdoptRequest{Target: "wiki", Node: "pve", VMID: vmidOf(t, id), Digest: u.Digest}, "user:admin", "")
	a.drv.RenameGuest(id, "x")
	a.e.Approve(a.ctx, p.ID, "user:admin", nil)
	a.e.Wait()
	a.drv.RenameGuest(id, "wiki")
	nu, _ := a.drv.InspectGuest(a.ctx, id)
	p, err := a.e.ProposeAdopt(a.ctx, AdoptRequest{Target: "wiki", Node: "pve", VMID: vmidOf(t, id), Digest: nu.Digest}, "user:admin", "")
	if err != nil {
		t.Fatalf("adopt again, same name: %v", err)
	}
	if _, err := a.e.Approve(a.ctx, p.ID, "user:admin", nil); err != nil {
		t.Fatal(err)
	}
	a.e.Wait()
	if _, err := a.st.GetBinding(a.ctx, state.KindTarget, "wiki"); err != nil {
		t.Fatalf("not bound: %v", err)
	}

	// A second stale Target: its delete makes no call and the guest stays.
	id2, u2 := a.person("db")
	p, _ = a.e.ProposeAdopt(a.ctx, AdoptRequest{Target: "db", Node: "pve", VMID: vmidOf(t, id2), Digest: u2.Digest}, "user:admin", "")
	a.drv.RenameGuest(id2, "db2")
	a.e.Approve(a.ctx, p.ID, "user:admin", nil)
	a.e.Wait()
	calls := a.drv.Calls(fake.OpDelete) + a.drv.Calls(fake.OpObserve)
	p, err = a.e.Propose(a.ctx, Input{Author: "token:tok_qa", Reason: "drop", Proposal: Proposal{Delete: []DocKey{{Kind: state.KindTarget, Name: "db"}}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.e.Approve(a.ctx, p.ID, "user:admin", nil); err != nil {
		t.Fatal(err)
	}
	a.e.Wait()
	if n := a.drv.Calls(fake.OpDelete) + a.drv.Calls(fake.OpObserve); n != calls {
		t.Fatalf("the delete of a stale Target called the hypervisor %d times", n-calls)
	}
	if _, err := a.drv.InspectGuest(a.ctx, id2); err != nil {
		t.Fatalf("the guest is gone: %v", err)
	}
}
