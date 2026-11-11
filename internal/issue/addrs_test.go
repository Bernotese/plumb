package issue_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Bernotese/plumb/internal/issue"
	"github.com/Bernotese/plumb/internal/link"
	"github.com/Bernotese/plumb/internal/plan"
	"github.com/Bernotese/plumb/internal/state"
	"github.com/Bernotese/plumb/internal/store"
	"github.com/Bernotese/plumb/internal/wire"
)

// Tests of the reported address list and the guest:address Issue
// (T-070; D-212 … D-217). The clock of link, engine and store is the env
// clock, so the 30 s rule, the stable time and the 1 hour rule run in
// test time.

const (
	staticIP  = "192.0.2.41"
	otherIP   = "192.0.2.107"
	addrIssue = wire.GuestAddressID
)

func staticTarget(name, ip string) state.Doc {
	g := `"guest":{"cores":2,"memory":1024,"disk":10,"bridge":"vmbr0"}`
	if ip != "dhcp" {
		g = `"guest":{"cores":2,"memory":1024,"disk":10,"bridge":"vmbr0","ip":"` + ip + `/24","gateway":"192.0.2.1"}`
	}
	return doc(state.KindTarget, name, `{"type":"vm","image":"ubuntu",`+g+`,"blocks":[{"use":"motd","params":{"text":"hi"}}]}`)
}

// presence is plan.Agents with an online state that the test sets: the
// test clock jumps minutes, and the fake agent polls in real time, so the
// link would see it offline.
type presence struct {
	plan.Agents
	mu      sync.Mutex
	offline map[string]bool
}

func (p *presence) Online(target string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return !p.offline[target]
}

func (p *presence) setOffline(target string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.offline[target] = true
}

var presences sync.Map // *env → *presence

func (e *env) presence() *presence { p, _ := presences.Load(e); return p.(*presence) }

// addrEnv is an env whose engine and store use the env clock, with the
// given targets ("name=ip" or "name=dhcp") created and online.
func addrEnv(t *testing.T, targets ...string) *env {
	t.Helper()
	var e *env
	p := &presence{offline: map[string]bool{}}
	e = newEnv(t, func(c *plan.Config) {
		c.Now = func() time.Time { return e.clock.now() }
		p.Agents, c.Agents = c.Agents, p
	})
	presences.Store(e, p)
	e.st.Now = e.clock.now
	put := []state.Doc{doc(state.KindImage, "ubuntu", image), doc(state.KindBlock, "motd", motd)}
	for _, spec := range targets {
		name, ip, _ := strings.Cut(spec, "=")
		put = append(put, staticTarget(name, ip))
		e.agent(name)
	}
	if run := e.approve(e.propose(plan.Proposal{Put: put}).ID); run.Status != "succeeded" {
		t.Fatalf("seed run %s", run.Status)
	}
	for _, spec := range targets {
		name, _, _ := strings.Cut(spec, "=")
		e.online(name)
	}
	return e
}

// send sends an addrs report as the agent of target and returns the status.
func (e *env) send(target string, list ...string) int {
	e.t.Helper()
	e.mu.Lock()
	a := e.agents[target]
	e.mu.Unlock()
	return a.SendAddrs(e.ctx, list)
}

func (e *env) hello(target string, list []string) int {
	e.t.Helper()
	e.mu.Lock()
	a := e.agents[target]
	e.mu.Unlock()
	return a.Report(e.ctx, wire.Report{Type: wire.ReportHello, Hello: &wire.Hello{Version: "test", Hostname: "fake", OS: "fake 1", Addrs: list}})
}

// reported is the stored list of a target, or nil when unknown.
func (e *env) reported(target string) []string {
	e.t.Helper()
	r, known, err := e.st.ReportedAddrs(e.ctx, target)
	if err != nil {
		e.t.Fatal(err)
	}
	if !known {
		return nil
	}
	return r.List
}

// tick runs one round of the guest loop and moves the clock on.
func (e *env) tick(d time.Duration) {
	e.t.Helper()
	if err := e.eng.CheckGuests(e.ctx); err != nil {
		e.t.Fatal(err)
	}
	e.clock.add(d)
}

// addrIssues returns every guest:address Issue of a target, resolved too.
func (e *env) addrIssues(target string) []store.Issue {
	e.t.Helper()
	var out []store.Issue
	for id := int64(1); id < 1000; id++ {
		iss, err := e.st.GetIssue(e.ctx, id)
		if errors.Is(err, store.ErrNotFound) {
			break
		}
		if err != nil {
			e.t.Fatal(err)
		}
		if iss.Target == target && iss.Resource == addrIssue {
			out = append(out, iss)
		}
	}
	return out
}

func (e *env) openAddr(target string) (store.Issue, bool) {
	iss, err := e.st.OpenIssue(e.ctx, target, addrIssue)
	return iss, err == nil
}

// events counts the events of a type.
func (e *env) countEvents(typ string) int {
	e.t.Helper()
	evs, err := e.st.EventsAfter(e.ctx, 0, 100000)
	if err != nil {
		e.t.Fatal(err)
	}
	n := 0
	for _, ev := range evs {
		if ev.Type == typ {
			n++
		}
	}
	return n
}

func TestAddrsStoreAndForm(t *testing.T) {
	e := addrEnv(t, "web1="+staticIP)
	if l := e.reported("web1"); l != nil {
		t.Fatalf("an agent without a list: %q", l)
	}
	if code := e.send("web1", otherIP); code != http.StatusNoContent {
		t.Fatalf("addrs report: %d", code)
	}
	if l := e.reported("web1"); !slices.Equal(l, []string{otherIP}) {
		t.Fatalf("stored %q", l)
	}
	// Every refused form: 400, and the stored list stays.
	e.clock.add(time.Minute)
	for _, bad := range [][]string{{"192.0.2.41/24"}, {"127.0.0.1"}, {"fe80::1"}, {"2001:DB8::1"}, {"::ffff:192.0.2.1"},
		{"192.0.2.9", "192.0.2.1"}, {"192.0.2.1", "192.0.2.1"}, {"web1"}} {
		if code := e.send("web1", bad...); code != http.StatusBadRequest {
			t.Errorf("addrs %q: %d, want 400", bad, code)
		}
	}
	// addrs fields on another report type: 400 (review N3).
	for _, rep := range []wire.Report{
		{Type: wire.ReportDrift, Items: []wire.Item{{Resource: "file:/etc/motd", Status: wire.StatusUnchanged}}, Addrs: []string{staticIP}},
		{Type: wire.ReportHello, Hello: &wire.Hello{Version: "t"}, AddrsTruncated: true},
		{Type: wire.ReportAddrs, Addrs: []string{staticIP}, Error: "x"},
	} {
		e.mu.Lock()
		a := e.agents["web1"]
		e.mu.Unlock()
		if code := a.Report(e.ctx, rep); code != http.StatusBadRequest {
			t.Errorf("%s with addrs fields: %d, want 400", rep.Type, code)
		}
	}
	if l := e.reported("web1"); !slices.Equal(l, []string{otherIP}) {
		t.Fatalf("after bad reports: %q", l)
	}
	// An empty list is known and empty.
	if code := e.send("web1"); code != http.StatusNoContent {
		t.Fatalf("empty addrs: %d", code)
	}
	if l := e.reported("web1"); l == nil || len(l) != 0 {
		t.Fatalf("empty list stored as %#v", l)
	}
}

// A Hello without addrs clears a known list (N1). A Hello over the 30 s
// rule keeps the stored list: it is not "no addrs field" (nit for T-070).
// A Hello with a bad list is taken and clears the list.
func TestAddrsHello(t *testing.T) {
	e := addrEnv(t, "web1="+staticIP)
	if code := e.hello("web1", []string{staticIP}); code != http.StatusNoContent {
		t.Fatalf("hello: %d", code)
	}
	if l := e.reported("web1"); !slices.Equal(l, []string{staticIP}) {
		t.Fatalf("hello list %q", l)
	}
	// Over the limit: dropped, the old list stays, the Hello is taken.
	if code := e.hello("web1", []string{otherIP}); code != http.StatusNoContent {
		t.Fatalf("hello over the limit: %d", code)
	}
	if l := e.reported("web1"); !slices.Equal(l, []string{staticIP}) {
		t.Fatalf("hello over the limit changed the list: %q", l)
	}
	// An addrs report over the limit gets 429.
	if code := e.send("web1", otherIP); code != http.StatusTooManyRequests {
		t.Fatalf("addrs over the limit: %d, want 429", code)
	}
	// No addrs field (an old agent): unknown, also within the limit.
	if code := e.hello("web1", nil); code != http.StatusNoContent {
		t.Fatalf("old hello: %d", code)
	}
	if l := e.reported("web1"); l != nil {
		t.Fatalf("old hello kept %q", l)
	}
	e.clock.add(31 * time.Second)
	e.hello("web1", []string{staticIP})
	e.clock.add(31 * time.Second)
	// A bad list in a Hello: the Hello is taken (204) and the list is unknown.
	if code := e.hello("web1", []string{"127.0.0.1"}); code != http.StatusNoContent {
		t.Fatalf("hello with a bad list: %d, want 204", code)
	}
	if l := e.reported("web1"); l != nil {
		t.Fatalf("bad hello list kept %q", l)
	}
	// addrs_unknown (the agent cannot read its list any more): unknown,
	// not limited; with a list it is refused.
	e.clock.add(31 * time.Second)
	e.send("web1", staticIP)
	e.mu.Lock()
	a := e.agents["web1"]
	e.mu.Unlock()
	if code := a.Report(e.ctx, wire.Report{Type: wire.ReportAddrs, AddrsUnknown: true, Addrs: []string{staticIP}}); code != http.StatusBadRequest {
		t.Fatalf("addrs_unknown with a list: %d, want 400", code)
	}
	if code := a.Report(e.ctx, wire.Report{Type: wire.ReportAddrs, AddrsUnknown: true}); code != http.StatusNoContent {
		t.Fatalf("addrs_unknown: %d", code)
	}
	if l := e.reported("web1"); l != nil {
		t.Fatalf("addrs_unknown kept %q", l)
	}
	if code := a.Report(e.ctx, wire.Report{Type: wire.ReportDrift, AddrsUnknown: true,
		Items: []wire.Item{{Resource: "file:/etc/motd", Status: wire.StatusUnchanged}}}); code != http.StatusBadRequest {
		t.Fatalf("addrs_unknown on drift: %d, want 400", code)
	}
}

// A flood of Hellos and reports with alternating lists: at most one write
// and one event per 30 s (S2).
func TestAddrsFlood(t *testing.T) {
	e := addrEnv(t, "web1="+staticIP)
	before := e.countEvents("target.addrs")
	lists := [][]string{{staticIP}, {otherIP}}
	for i := range 200 {
		l := lists[i%2]
		if i%3 == 0 {
			e.send("web1", l...)
		} else {
			e.hello("web1", l)
		}
		e.clock.add(100 * time.Millisecond) // 20 s in all
	}
	if n := e.countEvents("target.addrs") - before; n != 1 {
		t.Fatalf("%d target.addrs events in 20 s, want 1", n)
	}
	// Equal lists write nothing and emit nothing.
	e.clock.add(time.Minute)
	cur := e.reported("web1")
	before = e.countEvents("target.addrs")
	for range 50 {
		e.send("web1", cur...)
	}
	if n := e.countEvents("target.addrs") - before; n != 0 {
		t.Fatalf("%d events for an equal list", n)
	}
	for _, ev := range mustEvents(t, e) {
		if ev.Type == "target.addrs" && (strings.Contains(string(ev.Data), staticIP) || strings.Contains(string(ev.Data), otherIP)) {
			t.Fatalf("event holds an address: %s", ev.Data)
		}
	}
}

func mustEvents(t *testing.T, e *env) []store.Event {
	evs, err := e.st.EventsAfter(e.ctx, 0, 100000)
	if err != nil {
		t.Fatal(err)
	}
	return evs
}

// N4: a report for a target without a binding changes nothing, also when
// the agent id is the one of another target.
func TestAddrsNeedBinding(t *testing.T) {
	e := addrEnv(t, "web1="+staticIP)
	e.send("web1", staticIP)
	agent, err := e.st.AgentByTarget(e.ctx, "web1")
	if err != nil {
		t.Fatal(err)
	}
	e.clock.add(time.Minute)
	err = e.eng.HandleReport(e.ctx, link.Delivery{Target: "ghost", AgentID: agent.ID,
		Report: wire.Report{Type: wire.ReportAddrs, Addrs: []string{otherIP}}})
	if err != nil {
		t.Fatal(err)
	}
	if l := e.reported("web1"); !slices.Equal(l, []string{staticIP}) {
		t.Fatalf("unbound report changed web1: %q", l)
	}
}

// The Issue opens after the address was missing for 2 minutes, and closes
// after it was there for 2 minutes. Its actions are the ignore actions.
func TestAddrIssueStableOpenClose(t *testing.T) {
	e := addrEnv(t, "web1="+staticIP)
	e.send("web1", otherIP)
	e.tick(time.Minute) // first look: the stable time starts
	e.tick(59 * time.Second)
	e.tick(time.Second)
	if _, ok := e.openAddr("web1"); ok {
		t.Fatal("issue before 2 minutes")
	}
	e.tick(time.Minute)
	iss, ok := e.openAddr("web1")
	if !ok {
		t.Fatal("no issue after 2 minutes")
	}
	var ch []wire.Change
	json.Unmarshal(iss.Changes, &ch)
	if len(ch) != 1 || ch[0].Attr != "ip" || ch[0].To != staticIP || ch[0].Risk != wire.RiskRead ||
		fmt.Sprint(ch[0].From) != "["+otherIP+"]" {
		t.Fatalf("changes %+v", ch)
	}
	// Resolve: revert and accept are refused; the ignore actions stay.
	for _, a := range []string{issue.ActionRevert, issue.ActionAccept} {
		if _, err := e.iss.Resolve(e.ctx, iss.ID, a, "user:admin"); !errors.Is(err, issue.ErrAddressFix) {
			t.Errorf("%s: %v, want ErrAddressFix", a, err)
		}
	}
	// The same state does not write the Issue again.
	upd := iss.UpdatedAt
	e.tick(time.Minute)
	e.tick(time.Minute)
	if again, _ := e.openAddr("web1"); !again.UpdatedAt.Equal(upd) {
		t.Fatal("an unchanged mismatch wrote the issue again")
	}
	// The right address arrives: closed after 2 minutes, as gone by system.
	e.send("web1", staticIP, otherIP)
	e.tick(time.Minute)
	if _, ok := e.openAddr("web1"); !ok {
		t.Fatal("closed before 2 minutes")
	}
	e.tick(time.Minute)
	e.tick(time.Minute)
	if _, ok := e.openAddr("web1"); ok {
		t.Fatal("not closed after 2 minutes")
	}
	got, _ := e.st.GetIssue(e.ctx, iss.ID)
	if got.Resolution != "gone" || got.ResolvedBy != "system" {
		t.Fatalf("resolution %q by %q", got.Resolution, got.ResolvedBy)
	}
}

// A flapping guest for a day: one Issue row; a re-open stays open for an
// hour (S3).
func TestAddrIssueFlapDay(t *testing.T) {
	e := addrEnv(t, "web1="+staticIP)
	before := e.countEvents("issue.opened") + e.countEvents("issue.updated") + e.countEvents("issue.resolved")
	good := true
	for step := 0; step < 24*60; step++ { // one minute per step
		if step%3 == 0 { // the list flips every 3 minutes
			good = !good
			l := []string{otherIP}
			if good {
				l = []string{staticIP}
			}
			if code := e.send("web1", l...); code != http.StatusNoContent {
				t.Fatalf("step %d: %d", step, code)
			}
		}
		e.tick(time.Minute)
	}
	rows := e.addrIssues("web1")
	if len(rows) != 1 {
		t.Fatalf("%d issue rows after a day of flapping, want 1", len(rows))
	}
	n := e.countEvents("issue.opened") + e.countEvents("issue.updated") + e.countEvents("issue.resolved") - before
	if n > 2*24+4 {
		t.Fatalf("%d issue events in a day, want about 2 per hour", n)
	}
}

// After a close, a new mismatch within an hour opens the same Issue, and
// it stays open for an hour also when the address comes back.
func TestAddrIssueReopenHour(t *testing.T) {
	e := addrEnv(t, "web1="+staticIP)
	open := func() {
		e.t.Helper()
		e.send("web1", otherIP)
		for range 3 {
			e.tick(time.Minute)
		}
	}
	closeIt := func() {
		e.t.Helper()
		e.send("web1", staticIP)
		for range 3 {
			e.tick(time.Minute)
		}
	}
	open()
	first, ok := e.openAddr("web1")
	if !ok {
		t.Fatal("no issue")
	}
	e.clock.add(time.Minute)
	closeIt()
	if _, ok := e.openAddr("web1"); ok {
		t.Fatal("not closed")
	}
	e.clock.add(10 * time.Minute)
	open()
	again, ok := e.openAddr("web1")
	if !ok || again.ID != first.ID || again.ReopenedAt.IsZero() {
		t.Fatalf("re-open: %+v %v, want issue %d again", again, ok, first.ID)
	}
	e.clock.add(time.Minute)
	closeIt()
	if _, ok := e.openAddr("web1"); !ok {
		t.Fatal("a re-opened issue closed within the hour")
	}
	for range 60 {
		e.tick(time.Minute)
	}
	if _, ok := e.openAddr("web1"); ok {
		t.Fatal("re-opened issue still open after the hour with the right address")
	}
	// More than an hour after the close: a new Issue row.
	e.clock.add(2 * time.Hour)
	open()
	if third, ok := e.openAddr("web1"); !ok || third.ID == first.ID {
		t.Fatalf("after 2 h: %+v %v, want a new issue", third, ok)
	}
}

// No Issue and no close when the rule does not look: DHCP, old agent,
// truncated list, offline agent.
func TestAddrIssueRuleDoesNotLook(t *testing.T) {
	e := addrEnv(t, "web1="+staticIP, "web2=dhcp", "web3=192.0.2.43", "web4=192.0.2.44")
	e.send("web2", otherIP) // DHCP: shows the list, never an Issue
	// web3: an old agent, no list.
	var many []string
	for i := 1; i <= wire.MaxAddrs; i++ {
		many = append(many, fmt.Sprintf("10.0.0.%d", i))
	}
	e.mu.Lock()
	a4 := e.agents["web4"]
	e.mu.Unlock()
	if code := a4.Report(e.ctx, wire.Report{Type: wire.ReportAddrs, Addrs: many, AddrsTruncated: true}); code != http.StatusNoContent {
		t.Fatalf("truncated: %d", code)
	}
	e.send("web1", otherIP)
	e.tick(time.Minute)
	e.presence().setOffline("web1") // offline before the stable time ends
	for range 5 {
		e.tick(time.Minute)
	}
	for _, tg := range []string{"web1", "web2", "web3", "web4"} {
		if _, ok := e.openAddr(tg); ok {
			t.Errorf("%s: issue opened", tg)
		}
	}
	live, err := e.eng.Live(e.ctx, "web2")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(live.Reported, []string{otherIP}) || len(live.IPs) == 0 || live.IPs[0] == otherIP {
		t.Fatalf("web2 live %+v", live)
	}
	if live, _ := e.eng.Live(e.ctx, "web1"); live.Reported != nil {
		t.Fatalf("offline agent shows a list: %+v", live.Reported)
	}
	if live, _ := e.eng.Live(e.ctx, "web4"); !live.ReportedMore || len(live.Reported) != wire.MaxAddrs {
		t.Fatalf("web4 live %+v", live)
	}
}

// Ignore once ends when the reported list changes; ignore by rule lasts.
func TestAddrIssueIgnore(t *testing.T) {
	e := addrEnv(t, "web1="+staticIP)
	e.send("web1", otherIP)
	for range 3 {
		e.tick(time.Minute)
	}
	iss, ok := e.openAddr("web1")
	if !ok {
		t.Fatal("no issue")
	}
	if _, err := e.iss.IgnoreOnce(e.ctx, iss.ID, "user:admin", nil); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		e.tick(time.Minute)
	}
	if _, ok := e.openAddr("web1"); ok {
		t.Fatal("ignored once, but open again with the same list")
	}
	e.send("web1", "192.0.2.108")
	for range 3 {
		e.tick(time.Minute)
	}
	iss, ok = e.openAddr("web1")
	if !ok {
		t.Fatal("a new list did not end the ignore")
	}
	pl, err := e.iss.ResolveWithRule(e.ctx, iss.ID, issue.ActionIgnoreRule, "user:admin", issue.RuleScope{})
	if err != nil {
		t.Fatal(err)
	}
	if run := e.approve(pl.ID); run.Status != "succeeded" {
		t.Fatalf("rule run %s", run.Status)
	}
	e.clock.add(time.Minute)
	e.send("web1", "192.0.2.109")
	for range 4 {
		e.tick(time.Minute)
	}
	if _, ok := e.openAddr("web1"); ok {
		t.Fatal("a rule ignores it, but it is open")
	}
}

// D-216 (a, c): an agent that reports the address of another Target or
// of the controller changes nothing for that Target, and ips stays the
// configured address.
func TestAddrsForeignAddressIsOnlyDisplay(t *testing.T) {
	e := addrEnv(t, "web1="+staticIP, "web2=192.0.2.42")
	e.send("web2", "192.0.2.42")
	if code := e.send("web1", "10.0.0.1", "192.0.2.42"); code != http.StatusNoContent { // a controller address and web2's
		t.Fatalf("addrs: %d", code)
	}
	for range 4 {
		e.tick(time.Minute)
	}
	if _, ok := e.openAddr("web2"); ok {
		t.Fatal("web1's report opened an issue on web2")
	}
	if l := e.reported("web2"); !slices.Equal(l, []string{"192.0.2.42"}) {
		t.Fatalf("web2 list %q", l)
	}
	live, err := e.eng.Live(e.ctx, "web1")
	if err != nil {
		t.Fatal(err)
	}
	if len(live.IPs) != 1 || live.IPs[0] != staticIP {
		t.Fatalf("ips %q, want the configured %s", live.IPs, staticIP)
	}
	if _, ok := e.openAddr("web1"); !ok {
		t.Fatal("web1 mismatch has no issue")
	}
}

// A Run on the target: the rule does not look.
func TestAddrIssueBusyRun(t *testing.T) {
	e := addrEnv(t, "web1="+staticIP)
	e.send("web1", otherIP)
	e.tick(time.Minute)
	e.mu.Lock()
	a := e.agents["web1"]
	e.mu.Unlock()
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	a.OnCommand(func(typ wire.CommandType) {
		if typ == wire.CommandApply {
			once.Do(func() { close(entered) })
			<-release
		}
	})
	pl := e.propose(plan.Proposal{Put: []state.Doc{doc(state.KindTarget, "web1",
		strings.Replace(string(staticTarget("web1", staticIP).Spec), `"hi"`, `"changed"`, 1))}})
	if _, err := e.eng.Approve(e.ctx, pl.ID, "user:admin", nil); err != nil {
		t.Fatal(err)
	}
	<-entered
	for range 4 {
		e.tick(time.Minute)
	}
	if _, ok := e.openAddr("web1"); ok {
		t.Fatal("issue while a Run works on the target")
	}
	close(release)
	e.eng.Wait()
	for range 3 {
		e.tick(time.Minute)
	}
	if _, ok := e.openAddr("web1"); !ok {
		t.Fatal("no issue after the Run")
	}
}

// An addrs report is not a Hello: it sends no watch list.
func TestAddrsReportIsNoHello(t *testing.T) {
	e := addrEnv(t, "web1="+staticIP)
	e.mu.Lock()
	a := e.agents["web1"]
	e.mu.Unlock()
	watches := func() int {
		n := 0
		for _, c := range a.Commands() {
			if c == wire.CommandWatch {
				n++
			}
		}
		return n
	}
	w := watches()
	e.send("web1", otherIP)
	time.Sleep(100 * time.Millisecond)
	if watches() != w {
		t.Fatal("an addrs report made the engine send the watch list")
	}
}
