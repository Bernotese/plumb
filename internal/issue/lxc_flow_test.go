package issue_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"log/slog"
	"net/http"

	"github.com/Bernotese/plumb/internal/driver"
	"github.com/Bernotese/plumb/internal/driver/fake"
	"github.com/Bernotese/plumb/internal/issue"
	"github.com/Bernotese/plumb/internal/plan"
	"github.com/Bernotese/plumb/internal/state"
	"github.com/Bernotese/plumb/internal/store"
	"github.com/Bernotese/plumb/internal/wire"
)

// T-080: the engine part of LXC Targets (T-038 §4) against driver/fake.

var (
	ctPin   = strings.Repeat("ab", 64)
	ctImage = `{"type":"lxc","url":"http://download.proxmox.com/images/system/debian-13-standard_13.6-1_amd64.tar.zst","sha512":"` + ctPin + `"}`
)

func ctTarget(name, text string, cores int, allowRestart bool) state.Doc {
	return doc(state.KindTarget, name, `{"type":"lxc","image":"ct","allow_restart":`+strconv.FormatBool(allowRestart)+
		`,"guest":{"cores":`+itoa(cores)+`,"memory":512,"disk":4,"bridge":"vmbr0"},"blocks":[{"use":"motd","params":{"text":"`+text+`"}}]}`)
}

// syncBuf is a log sink for the leak scan.
type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) { s.mu.Lock(); defer s.mu.Unlock(); return s.b.Write(p) }
func (s *syncBuf) String() string              { s.mu.Lock(); defer s.mu.Unlock(); return s.b.String() }

func newCTEnv(t *testing.T) (*env, *syncBuf) {
	logs := &syncBuf{}
	e := newEnv(t, func(c *plan.Config) {
		c.Logger = slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	})
	return e, logs
}

func (e *env) steps(run store.Run) []store.Step {
	e.t.Helper()
	steps, err := e.st.Steps(e.ctx, run.ID)
	if err != nil {
		e.t.Fatal(err)
	}
	return steps
}

func stepNames(steps []store.Step) string {
	var out []string
	for _, s := range steps {
		out = append(out, s.Resource+"="+s.Status)
	}
	return strings.Join(out, " ")
}

func (e *env) guestID(target string) driver.GuestID {
	e.t.Helper()
	b, err := e.st.GetBinding(e.ctx, state.KindTarget, target)
	if err != nil {
		e.t.Fatal(err)
	}
	return driver.GuestID(b.Ref)
}

func (e *env) observe(target string) driver.GuestState {
	e.t.Helper()
	st, err := e.drv.Observe(e.ctx, e.guestID(target))
	if err != nil {
		e.t.Fatal(err)
	}
	return st
}

// tokens records every enrollment token that the fake hands to a guest,
// for the leak scan.
func (e *env) tokens(target string, into *[]string) {
	var mu sync.Mutex
	go func() {
		for e.ctx.Err() == nil {
			if b, err := e.st.GetBinding(e.ctx, state.KindTarget, target); err == nil {
				if boot, ok := e.drv.Bootstrap(driver.GuestID(b.Ref)); ok {
					mu.Lock()
					if len(*into) == 0 || (*into)[len(*into)-1] != boot.Token {
						*into = append(*into, boot.Token)
					}
					mu.Unlock()
				}
			}
			sleep(e.ctx, 2*time.Millisecond)
		}
	}()
}

// noLeak scans the database files and the log for the tokens.
func (e *env) noLeak(logs *syncBuf, tokens []string) {
	e.t.Helper()
	if len(tokens) == 0 {
		e.t.Fatal("no token seen")
	}
	files, _ := filepath.Glob(filepath.Join(e.dir, "plumb.db*"))
	for _, tok := range tokens {
		if tok == "" {
			continue
		}
		for _, f := range files {
			b, _ := os.ReadFile(f)
			if bytes.Contains(b, []byte(tok)) {
				e.t.Errorf("token in %s", filepath.Base(f))
			}
		}
		if strings.Contains(logs.String(), tok) {
			e.t.Error("token in the log")
		}
	}
}

func seedCT() []state.Doc {
	return []state.Doc{doc(state.KindImage, "ct", ctImage), doc(state.KindBlock, "motd", motd)}
}

// Create: image:fetch, guest:config, the hello, guest:bootstrap, then the
// resources. The token leaves the config; the clear is no drift.
func TestLXCCreate(t *testing.T) {
	e, logs := newCTEnv(t)
	var toks []string
	e.tokens("c1", &toks)
	e.agent("c1")
	run := e.approve(e.propose(plan.Proposal{Put: append(seedCT(), ctTarget("c1", "hi", 1, false))}).ID)
	steps := e.steps(run)
	want := "image:fetch=applied guest:config=applied guest:bootstrap=applied file:/etc/motd=applied"
	if run.Status != "succeeded" || stepNames(steps) != want {
		t.Fatalf("run %s: %s\nwant %s", run.Status, stepNames(steps), want)
	}
	st := e.observe("c1")
	if st.TokenInConfig || st.Spec.Kind != "lxc" || st.Spec.User != "" {
		t.Errorf("after create: %+v", st)
	}
	b, _ := e.st.GetBinding(e.ctx, state.KindTarget, "c1")
	if !strings.HasPrefix(b.Digest, st.Digest+" ") { // digest and power (D-299)
		t.Error("the binding misses the digest after the clear")
	}
	// The clear is not drift: the guest poll opens no Issue.
	if err := e.eng.CheckGuests(e.ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := e.st.OpenIssue(e.ctx, "c1", "guest:config"); err == nil {
		t.Error("the clear became a guest:config Issue")
	}
	// After the next cold start PID 1 has no token any more (host C4).
	e.drv.ColdRestart(e.guestID("c1"), false)
	if _, ok := e.drv.Bootstrap(e.guestID("c1")); ok {
		t.Error("the token is back after a restart")
	}
	// A second Run has no fetch and no bootstrap step.
	e.online("c1")
	run = e.approve(e.propose(plan.Proposal{Put: []state.Doc{ctTarget("c1", "again", 1, false)}}).ID)
	if run.Status != "succeeded" || stepNames(e.steps(run)) != "file:/etc/motd=applied" {
		t.Fatalf("second run %s: %s", run.Status, stepNames(e.steps(run)))
	}
	if e.drv.Calls(fake.OpFetchImage) != 1 || e.drv.Calls(fake.OpClearBootstrap) != 1 {
		t.Errorf("fetch %d clear %d", e.drv.Calls(fake.OpFetchImage), e.drv.Calls(fake.OpClearBootstrap))
	}
	e.noLeak(logs, toks)
}

// A failed clear fails the Run with its text; the next Run of the target
// clears the token.
func TestLXCClearFails(t *testing.T) {
	e, _ := newCTEnv(t)
	e.agent("c1")
	e.drv.Fail(fake.OpClearBootstrap, errors.New("fake: config locked"))
	run := e.approve(e.propose(plan.Proposal{Put: append(seedCT(), ctTarget("c1", "hi", 1, false))}).ID)
	steps := e.steps(run)
	if run.Status != "failed" || stepNames(steps) != "image:fetch=applied guest:config=applied guest:bootstrap=failed file:/etc/motd=applied" {
		t.Fatalf("run %s: %s", run.Status, stepNames(steps))
	}
	if out := steps[2].Output; !strings.Contains(out, "config locked") || !strings.Contains(out, "next Run") {
		t.Errorf("output %q", out)
	}
	if !e.observe("c1").TokenInConfig {
		t.Fatal("the token left without a clear")
	}
	run = e.approve(e.propose(plan.Proposal{Put: []state.Doc{ctTarget("c1", "again", 1, false)}}).ID)
	if run.Status != "succeeded" || stepNames(e.steps(run)) != "guest:bootstrap=applied file:/etc/motd=applied" {
		t.Fatalf("next run %s: %s", run.Status, stepNames(e.steps(run)))
	}
	if e.observe("c1").TokenInConfig {
		t.Error("the next Run did not clear the token")
	}
}

// The controller is down at the first boot (review S4): the hello
// timeout fails the create Run, the token stays in the config. The
// re-enrollment Plan recovers: a new token, a cold restart, the hello,
// the clear.
func TestLXCNoHelloThenReenroll(t *testing.T) {
	e, logs := newCTEnv(t)
	var toks []string
	e.tokens("c1", &toks)
	run := e.approve(e.propose(plan.Proposal{Put: append(seedCT(), ctTarget("c1", "hi", 1, true))}).ID)
	if run.Status != "failed" || stepNames(e.steps(run)) != "image:fetch=applied guest:config=applied guest:bootstrap=failed file:/etc/motd=failed" {
		t.Fatalf("run %s: %s", run.Status, stepNames(e.steps(run)))
	}
	if !e.observe("c1").TokenInConfig {
		t.Fatal("token not in the config")
	}
	e.clock.add(16 * time.Minute) // the first token expired
	a := e.agent("c1")
	time.Sleep(100 * time.Millisecond)
	if a.Enrolls() != 0 {
		t.Fatal("enrolled with the expired token")
	}
	boots := e.drv.Boots(e.guestID("c1"))
	run = e.approve(e.propose(plan.Proposal{Reenroll: []string{"c1"}}).ID)
	if run.Status != "succeeded" || stepNames(e.steps(run)) != "guest:config=applied guest:bootstrap=applied" {
		t.Fatalf("reenroll run %s: %s", run.Status, stepNames(e.steps(run)))
	}
	if a.Enrolls() != 1 || e.drv.Boots(e.guestID("c1")) != boots+1 || e.observe("c1").TokenInConfig {
		t.Errorf("enrolls %d boots %d→%d token %v", a.Enrolls(), boots, e.drv.Boots(e.guestID("c1")), e.observe("c1").TokenInConfig)
	}
	e.noLeak(logs, toks)
}

// Re-enrollment of an enrolled, running container: the VM path, then the
// clear.
func TestLXCReenroll(t *testing.T) {
	e, _ := newCTEnv(t)
	a := e.agent("c1")
	run := e.approve(e.propose(plan.Proposal{Put: append(seedCT(), ctTarget("c1", "hi", 1, true))}).ID)
	if run.Status != "succeeded" {
		t.Fatalf("create %s: %s", run.Status, stepNames(e.steps(run)))
	}
	oldCredential := a.Credential()
	boots := e.drv.Boots(e.guestID("c1"))
	run = e.approve(e.propose(plan.Proposal{Reenroll: []string{"c1"}}).ID)
	if run.Status != "succeeded" || stepNames(e.steps(run)) != "guest:config=applied guest:bootstrap=applied" {
		t.Fatalf("reenroll %s: %s", run.Status, stepNames(e.steps(run)))
	}
	if a.Enrolls() != 2 || e.observe("c1").TokenInConfig || e.drv.Calls(fake.OpClearBootstrap) != 2 {
		t.Errorf("enrolls %d token %v clears %d", a.Enrolls(), e.observe("c1").TokenInConfig, e.drv.Calls(fake.OpClearBootstrap))
	}
	if e.drv.Boots(e.guestID("c1")) != boots+1 {
		t.Error("re-enrollment did not cold restart exactly once")
	}
	if a.Credential() == oldCredential {
		t.Error("re-enrollment reused the credential")
	}
	if code := ctRequest(e, wire.PathReport, oldCredential, wire.Report{Type: wire.ReportHello}); code != http.StatusUnauthorized {
		t.Errorf("old credential after re-enrollment: %d", code)
	}
}

// cores and memory are live on a container (host C5): no restart, no
// "restart needed", also without allow_restart.
func TestLXCLiveResize(t *testing.T) {
	e, _ := newCTEnv(t)
	e.agent("c1")
	if run := e.approve(e.propose(plan.Proposal{Put: append(seedCT(), ctTarget("c1", "hi", 1, false))}).ID); run.Status != "succeeded" {
		t.Fatalf("create %s", run.Status)
	}
	boots := e.drv.Boots(e.guestID("c1"))
	p := e.propose(plan.Proposal{Put: []state.Doc{ctTarget("c1", "hi", 2, false)}})
	for _, rc := range changes(t, p)["c1"].Resources {
		for _, c := range rc.Changes {
			if c.WaitsForRestart || c.Attr == "restart" {
				t.Errorf("plan change %+v", c)
			}
		}
	}
	run := e.approve(p.ID)
	if run.Status != "succeeded" || e.drv.Boots(e.guestID("c1")) != boots || e.observe("c1").Spec.Cores != 2 || e.observe("c1").Pending != nil {
		t.Errorf("run %s boots %d→%d state %+v", run.Status, boots, e.drv.Boots(e.guestID("c1")), e.observe("c1"))
	}
}

// Delete: the container goes, the credential is revoked (D-211).
func TestLXCDelete(t *testing.T) {
	e, _ := newCTEnv(t)
	a := e.agent("c1")
	if run := e.approve(e.propose(plan.Proposal{Put: append(seedCT(), ctTarget("c1", "hi", 1, false))}).ID); run.Status != "succeeded" {
		t.Fatalf("create %s", run.Status)
	}
	id := e.guestID("c1")
	credential := a.Credential()
	run := e.approve(e.propose(plan.Proposal{Delete: []plan.DocKey{{Kind: state.KindTarget, Name: "c1"}}}).ID)
	if run.Status != "succeeded" || stepNames(e.steps(run)) != "guest:config=applied" {
		t.Fatalf("delete %s: %s", run.Status, stepNames(e.steps(run)))
	}
	if _, err := e.drv.Observe(e.ctx, id); !errors.Is(err, driver.ErrNotFound) {
		t.Error("container still there")
	}
	eventually(t, "agent offline", func() bool { return !e.link.Online("c1") })
	if code := ctRequest(e, wire.PathReport, credential, wire.Report{Type: wire.ReportHello}); code != http.StatusUnauthorized {
		t.Errorf("deleted credential: %d", code)
	}
	if _, err := e.st.GetBinding(e.ctx, state.KindTarget, "c1"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("deleted binding: %v", err)
	}
}

// A stop during the fetch cancels it: nothing is made, no token is made,
// and the steps end failed.
func TestLXCCancelDuringFetch(t *testing.T) {
	e, _ := newCTEnv(t)
	e.drv.SetLatency(2 * time.Second)
	e.drv.Hook(fake.OpFetchImage, func() { go func() { time.Sleep(50 * time.Millisecond); e.eng.Stop() }() })
	p := e.propose(plan.Proposal{Put: append(seedCT(), ctTarget("c1", "hi", 1, false))})
	start := time.Now()
	run := e.approve(p.ID)
	if time.Since(start) > 1500*time.Millisecond {
		t.Errorf("the stop did not cancel the fetch (%v)", time.Since(start))
	}
	if run.Status == "succeeded" || stepNames(e.steps(run)) != "image:fetch=failed guest:config=failed guest:bootstrap=failed file:/etc/motd=failed" {
		t.Fatalf("run %s: %s", run.Status, stepNames(e.steps(run)))
	}
	if e.drv.Calls(fake.OpCreate) != 0 || len(e.drv.Guests()) != 0 {
		t.Error("a container was made")
	}
	if _, err := e.st.GetBinding(e.ctx, state.KindTarget, "c1"); err == nil {
		t.Error("a binding was made")
	}
}

// A VM target has no fetch and no bootstrap step (unchanged).
func TestVMHasNoBootstrapStep(t *testing.T) {
	e := newEnv(t)
	e.seed("web1")
	run := e.approve(e.propose(plan.Proposal{Put: []state.Doc{target("web1", "x", 10)}}).ID)
	for _, s := range e.steps(run) {
		if s.Resource == plan.StepGuestBootstrap || s.Resource == plan.StepImageFetch {
			t.Errorf("VM step %s", s.Resource)
		}
	}
	if e.drv.Calls(fake.OpClearBootstrap) != 0 {
		t.Error("ClearBootstrap on a VM")
	}
}

// Review S1 of #159: a Stop right after a good Create still records the
// binding, so a later Run can clear the token or delete the guest. For
// containers and VMs.
func TestStopAfterCreateKeepsBinding(t *testing.T) {
	for _, kind := range []string{"lxc", "vm"} {
		t.Run(kind, func(t *testing.T) {
			e, _ := newCTEnv(t)
			put := append(seedCT(), ctTarget("c1", "hi", 1, false))
			if kind == "vm" {
				put = []state.Doc{doc(state.KindImage, "ubuntu", image), doc(state.KindBlock, "motd", motd), target("c1", "hi", 10)}
			}
			// The first Observe of the Run is the one after Create.
			e.drv.Hook(fake.OpObserve, func() { e.eng.Stop() })
			run := e.approve(e.propose(plan.Proposal{Put: put}).ID)
			if run.Status == "succeeded" {
				t.Fatal("the Run went on after Stop")
			}
			ids := e.drv.Guests()
			if len(ids) != 1 {
				t.Fatalf("guests %v", ids)
			}
			b, err := e.st.GetBinding(e.ctx, state.KindTarget, "c1")
			if err != nil || b.Ref != string(ids[0]) || b.Digest == "" {
				t.Fatalf("binding %+v %v", b, err)
			}
		})
	}
}

// Review N4 of #159: a Stop between the hello and the clear fails the
// step and leaves the token; the next Run after the restart clears it.
func TestLXCStopBeforeClear(t *testing.T) {
	e, _ := newCTEnv(t)
	a := e.agent("c1")
	e.drv.Hook(fake.OpClearBootstrap, func() {
		e.eng.Stop()
		e.drv.Fail(fake.OpClearBootstrap, context.Canceled) // the call never reached Proxmox
	})
	run := e.approve(e.propose(plan.Proposal{Put: append(seedCT(), ctTarget("c1", "hi", 1, false))}).ID)
	steps := e.steps(run)
	if run.Status == "succeeded" || len(steps) < 3 || steps[2].Resource != plan.StepGuestBootstrap || steps[2].Status != "failed" {
		t.Fatalf("run %s: %s", run.Status, stepNames(steps))
	}
	if !e.observe("c1").TokenInConfig {
		t.Fatal("token left the config without a clear")
	}
	// Controller restart: the next Run of the target clears the token.
	e.eng.Close()
	e.start()
	a.SetHandler(e.link.Handler())
	e.online("c1")
	run = e.approve(e.propose(plan.Proposal{Put: []state.Doc{ctTarget("c1", "again", 1, false)}}).ID)
	if run.Status != "succeeded" || !strings.HasPrefix(stepNames(e.steps(run)), "guest:bootstrap=applied") {
		t.Fatalf("next run %s: %s", run.Status, stepNames(e.steps(run)))
	}
	if e.observe("c1").TokenInConfig {
		t.Error("token still in the config")
	}
}

// T-038 §3 (review S6): a person adds a mount point and a feature on the
// host. The guest poll opens a guest:config Issue (destructive). Revert
// refuses with the host command while the mount point is there; accept is
// refused; after the person removed the mount point, revert writes the
// features back, and they wait for the next start (host D4).
func TestLXCOutsideAdditions(t *testing.T) {
	e, _ := newCTEnv(t)
	e.agent("c1")
	if run := e.approve(e.propose(plan.Proposal{Put: append(seedCT(), ctTarget("c1", "hi", 1, false))}).ID); run.Status != "succeeded" {
		t.Fatalf("create %s", run.Status)
	}
	id := e.guestID("c1")
	e.drv.Tamper(id, func(s *driver.GuestSpec) {
		s.Features = "keyctl=1,nesting=1"
		s.Mounts = []string{"mp0=/srv/data,mp=/data"}
	})
	if err := e.eng.CheckGuests(e.ctx); err != nil {
		t.Fatal(err)
	}
	iss := e.openIssue("c1", wire.GuestConfigID)
	for _, part := range []string{`"attr":"features"`, `"attr":"mounts"`, `"destructive"`, `mp0=/srv/data`} {
		if !strings.Contains(string(iss.Changes), part) {
			t.Errorf("issue changes %s miss %s", iss.Changes, part)
		}
	}
	_, err := e.iss.Resolve(e.ctx, iss.ID, issue.ActionRevert, "user:admin")
	if !errors.Is(err, plan.ErrOutside) || !strings.Contains(err.Error(), "pct set "+string(id)+" --delete") {
		t.Errorf("revert with a mount point: %v", err)
	}
	if _, err := e.iss.Resolve(e.ctx, iss.ID, issue.ActionAccept, "user:admin"); !errors.Is(err, issue.ErrAcceptOutside) {
		t.Errorf("accept: %v", err)
	}
	// The person removes the mount point; the feature stays.
	e.drv.Tamper(id, func(s *driver.GuestSpec) { s.Mounts = nil })
	e.eng.CheckGuests(e.ctx)
	iss = e.openIssue("c1", wire.GuestConfigID)
	p, err := e.iss.Resolve(e.ctx, iss.ID, issue.ActionRevert, "user:admin")
	if err != nil {
		t.Fatal(err)
	}
	if run := e.approve(p.ID); run.Status != "succeeded" {
		t.Fatalf("revert run %s: %s", run.Status, stepNames(e.steps(run)))
	}
	st := e.observe("c1")
	if st.Spec.Features != "keyctl=1,nesting=1" || st.Next().Features != driver.CTFeatures {
		t.Errorf("features now %q, next %q", st.Spec.Features, st.Next().Features)
	}
	if got, _ := e.st.GetIssue(e.ctx, iss.ID); got.Status != store.IssueResolved {
		t.Errorf("issue %s", got.Status)
	}
	// Waiting for the start is no new drift.
	e.eng.CheckGuests(e.ctx)
	if _, err := e.st.OpenIssue(e.ctx, "c1", wire.GuestConfigID); err == nil {
		t.Error("the waiting features became drift")
	}
}

func TestLXCOutsideAdditionsIndividually(t *testing.T) {
	for _, tc := range []struct {
		attr, key string
		change    func(*driver.GuestSpec)
	}{
		{driver.AttrFeatures, "", func(s *driver.GuestSpec) { s.Features = "nesting=1,fuse=1" }},
		{driver.AttrMounts, "mp2", func(s *driver.GuestSpec) { s.Mounts = []string{"mp2=/srv/data,mp=/data"} }},
		{driver.AttrDevices, "dev3", func(s *driver.GuestSpec) { s.Devices = []string{"dev3=/dev/fuse"} }},
		{driver.AttrRawLXC, "lxc.apparmor.profile", func(s *driver.GuestSpec) { s.RawLXC = []string{"lxc.apparmor.profile: unconfined"} }},
	} {
		for _, pending := range []bool{false, true} {
			name := tc.attr
			if pending {
				name += "/pending"
			}
			t.Run(name, func(t *testing.T) {
				e, _ := newCTEnv(t)
				e.agent("c1")
				if run := e.approve(e.propose(plan.Proposal{Put: append(seedCT(), ctTarget("c1", "hi", 1, false))}).ID); run.Status != "succeeded" {
					t.Fatal(run.Status)
				}
				id := e.guestID("c1")
				tamper := e.drv.Tamper
				if pending {
					tamper = e.drv.TamperPending
				}
				if err := tamper(id, tc.change); err != nil {
					t.Fatal(err)
				}
				if err := e.eng.CheckGuests(e.ctx); err != nil {
					t.Fatal(err)
				}
				iss := e.openIssue("c1", wire.GuestConfigID)
				for _, part := range []string{`"attr":"` + tc.attr + `"`, `"destructive"`} {
					if !strings.Contains(string(iss.Changes), part) {
						t.Errorf("changes %s miss %s", iss.Changes, part)
					}
				}
				if _, err := e.iss.Resolve(e.ctx, iss.ID, issue.ActionAccept, "user:admin"); !errors.Is(err, issue.ErrAcceptOutside) {
					t.Fatalf("accept: %v", err)
				}
				p, err := e.iss.Resolve(e.ctx, iss.ID, issue.ActionRevert, "user:admin")
				if tc.key != "" {
					// A raw lxc line has no --delete in the API: the text
					// names the file and the line (review S2 of #169).
					want := "pct set " + string(id) + " --delete " + tc.key
					if tc.attr == driver.AttrRawLXC {
						want = `remove the line "lxc.apparmor.profile: unconfined" from /etc/pve/lxc/` + string(id) + ".conf"
					}
					if !errors.Is(err, plan.ErrOutside) || !strings.Contains(err.Error(), want) {
						t.Errorf("refusal must name exact key %s: %v", tc.key, err)
					}
					if pending && !strings.Contains(err.Error(), "(pending, active at the next start)") {
						t.Errorf("pending addition not named: %v", err)
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				if run := e.approve(p.ID); run.Status != "succeeded" {
					t.Fatal(run.Status)
				}
				if err := e.eng.CheckGuests(e.ctx); err != nil {
					t.Fatal(err)
				}
				if _, err := e.st.OpenIssue(e.ctx, "c1", wire.GuestConfigID); err == nil {
					t.Error("features revert left drift")
				}
			})
		}
	}
}
