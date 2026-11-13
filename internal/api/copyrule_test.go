package api

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Bernotese/plumb/internal/issue"
	"github.com/Bernotese/plumb/internal/plan"
	"github.com/Bernotese/plumb/internal/state"
	"github.com/Bernotese/plumb/internal/store"
	"github.com/Bernotese/plumb/internal/wire"
)

// The copy rule (D-331, D-352; T-148). Each bypass of review H1 of #241
// and the cap of review F1 of #243 has a test here.

type guestPlan struct {
	ID        int64                 `json:"id"`
	HumanOnly []string              `json:"human_only"`
	Issues    []int64               `json:"guest_data_issues"`
	Matches   []plan.GuestDataMatch `json:"guest_data_matches"`
}

func (e *env) proposeDocs(c *client, docs ...state.Doc) guestPlan {
	e.t.Helper()
	code, body := c.do("POST", "/api/v1/plans", map[string]any{"reason": "copy", "put": docs})
	if code != 201 {
		e.t.Fatalf("propose: %d %s", code, body)
	}
	var p guestPlan
	if err := json.Unmarshal(body, &p); err != nil {
		e.t.Fatal(err)
	}
	return p
}

func (e *env) getGuestPlan(c *client, id int64) guestPlan {
	e.t.Helper()
	code, body := c.do("GET", "/api/v1/plans/"+itoa(id), nil)
	if code != 200 {
		e.t.Fatalf("get plan: %d %s", code, body)
	}
	var p guestPlan
	if err := json.Unmarshal(body, &p); err != nil {
		e.t.Fatal(err)
	}
	return p
}

// tokenRefused: an apply token cannot approve the Plan (403
// human-required), so the copy needs a person.
func (e *env) tokenRefused(id int64) {
	e.t.Helper()
	code, body := e.token(TierApply, wire.RiskDisruptive).do("POST", "/api/v1/plans/"+itoa(id)+"/approve", nil)
	if code != 403 || problemType(body) != "human-required" {
		e.t.Fatalf("token approve of a copy: %d %s", code, body)
	}
}

func guestOnly(t *testing.T, p guestPlan, issue int64, target, element string) {
	t.Helper()
	if !slices.Contains(p.HumanOnly, plan.HumanOnlyAcceptGuestData) {
		t.Fatalf("plan %d: human_only %v, want accept-guest-data", p.ID, p.HumanOnly)
	}
	if !slices.Contains(p.Issues, issue) {
		t.Errorf("plan %d: guest_data_issues %v, want %d", p.ID, p.Issues, issue)
	}
	want := plan.GuestDataMatch{IssueID: issue, Target: target, Resource: "file:/etc/motd", Attr: "content", Element: element}
	if !slices.Contains(p.Matches, want) {
		t.Errorf("plan %d: guest_data_matches %+v, want %+v", p.ID, p.Matches, want)
	}
}

func (e *env) motdIssue() int64 {
	e.t.Helper()
	id := e.openIssueID()
	if id == 0 {
		e.t.Fatal("no Issue for the hand edit")
	}
	return id
}

// H1 (1): a token ignores the drift once, then copies the value into the
// same Target. The guest has the value, so the planned changes are empty:
// only the desired-state diff finds it.
func TestCopyRuleIgnoreOnceThenCopy(t *testing.T) {
	e := newEnv(t, true)
	adm := e.admin()
	e.seed(adm, "web1")
	e.motd("evil-value")
	id := e.motdIssue()
	if code, body := e.token(TierApply, wire.RiskDisruptive).do("POST", "/api/v1/issues/"+itoa(id)+"/resolve", resolveBody(issue.ActionIgnoreOnce)); code >= 300 {
		t.Fatalf("ignore once: %d %s", code, body)
	}
	if e.openIssueID() != 0 {
		t.Fatal("the Issue is still open")
	}
	p := e.proposeDocs(e.token(TierPlan, ""), targetDoc("web1", "evil-value"))
	guestOnly(t, p, id, "web1", "evil-value")
	e.tokenRefused(p.ID)
	rc := &recorder{t: t}
	rc.do(adm, "GET", "/api/v1/plans/"+itoa(p.ID), "/plans/{id}", nil)
	rc.check()
	// A person may approve it.
	if code, body := adm.do("POST", "/api/v1/plans/"+itoa(p.ID)+"/approve", nil); code != 202 {
		t.Fatalf("human approve: %d %s", code, body)
	}
	e.eng.Wait()
}

// H1 (2): the guest puts the value back, so its Issue closes as gone; the
// value still counts for 30 days.
func TestCopyRuleGuestClosesIssue(t *testing.T) {
	e := newEnv(t, true)
	adm := e.admin()
	e.seed(adm, "web1")
	e.motd("evil-value")
	id := e.motdIssue()
	e.motd("hello")
	if iss, err := e.st.GetIssue(e.ctx, id); err != nil || iss.Status != store.IssueResolved {
		t.Fatalf("the Issue is not resolved: %+v %v", iss, err)
	}
	p := e.proposeDocs(e.token(TierPlan, ""), targetDoc("web1", "evil-value"))
	guestOnly(t, p, id, "web1", "evil-value")
	e.tokenRefused(p.ID)
}

// H1 (3): the copy goes into another Target. A value that no guest
// reported stays a normal Plan.
func TestCopyRuleOtherTarget(t *testing.T) {
	e := newEnv(t, true)
	adm := e.admin()
	e.seed(adm, "web1")
	e.seed(adm, "web2")
	e.motd("evil-value")
	id := e.motdIssue()
	p := e.proposeDocs(e.token(TierPlan, ""), targetDoc("web2", "evil-value"))
	guestOnly(t, p, id, "web2", "evil-value")
	e.tokenRefused(p.ID)
	clean := e.proposeDocs(e.token(TierPlan, ""), targetDoc("web2", "own-value"))
	if len(clean.HumanOnly) != 0 || clean.Issues != nil || clean.Matches != nil {
		t.Fatalf("a value no guest reported: %+v", clean)
	}
}

// H1 (4): one element of a list. Only the guest's extra element counts;
// the elements of the desired state do not.
func TestCopyRuleOneListElement(t *testing.T) {
	e := newEnv(t, true)
	adm := e.admin()
	e.seed(adm, "web1")
	const good, evil = "ssh-ed25519 AAAAgood", "ssh-ed25519 AAAAevil"
	if err := e.iss.Intake(e.ctx, "web1", wire.Report{Type: wire.ReportDrift, Items: []wire.Item{{Resource: "user:alice", Status: wire.StatusDiffers,
		Changes: []wire.Change{{Attr: "ssh_keys", From: []any{good, evil}, To: []any{good}, Risk: wire.RiskDisruptive}}}}}); err != nil {
		t.Fatal(err)
	}
	iss, err := e.st.OpenIssue(e.ctx, "web1", "user:alice")
	if err != nil {
		t.Fatal(err)
	}
	keys := func(list ...string) state.Doc {
		b, _ := json.Marshal(list)
		return doc(state.KindBlock, "keys", `{"resources":[{"user":{"name":"bob","ssh_keys":`+string(b)+`}}]}`)
	}
	web2 := doc(state.KindTarget, "web2", `{"type":"vm","image":"ubuntu","guest":{"cores":2,"memory":1024,"disk":10,"bridge":"vmbr0"},"blocks":[{"use":"keys"}]}`)
	p := e.proposeDocs(e.token(TierPlan, ""), keys("ssh-ed25519 AAAAmine", evil), web2)
	want := []plan.GuestDataMatch{{IssueID: iss.ID, Target: "web2", Resource: "user:bob", Attr: "ssh_keys", Element: evil}}
	if !slices.Contains(p.HumanOnly, plan.HumanOnlyAcceptGuestData) || !slices.Equal(p.Issues, []int64{iss.ID}) || !slices.Equal(p.Matches, want) {
		t.Fatalf("one copied key: %+v", p)
	}
	if code, body := adm.do("DELETE", "/api/v1/plans/"+itoa(p.ID), nil); code >= 300 {
		t.Fatalf("drop: %d %s", code, body)
	}
	// The desired key of web1 was never guest data.
	if p := e.proposeDocs(e.token(TierPlan, ""), keys("ssh-ed25519 AAAAmine", good), web2); len(p.HumanOnly) != 0 {
		t.Fatalf("a desired key copied: %+v", p)
	}
}

// A report that arrives between the Plan and its approval counts: the
// approval sees the new reason.
func TestCopyRuleReportBeforeApproval(t *testing.T) {
	e := newEnv(t, true)
	adm := e.admin()
	e.seed(adm, "web1")
	e.seed(adm, "web2")
	p := e.proposeDocs(e.token(TierPlan, ""), targetDoc("web2", "late-value"))
	if len(p.HumanOnly) != 0 {
		t.Fatalf("before the report: %+v", p)
	}
	e.motd("late-value")
	id := e.motdIssue()
	e.tokenRefused(p.ID)
	guestOnly(t, e.getGuestPlan(adm, p.ID), id, "web2", "late-value")
}

// F1: the cap never evicts. Past it, the Target is flagged (every Plan
// that touches it is human-only) and agent:values opens; the daily prune
// ends both after 30 days.
func TestCopyRuleCapFailsClosed(t *testing.T) {
	e := newEnv(t, true)
	adm := e.admin()
	e.seed(adm, "web1")
	e.seed(adm, "web2")
	e.motd("key-value")
	id := e.motdIssue()
	junk := make([]store.GuestValue, store.GuestValueCap)
	for i := range junk {
		junk[i] = store.GuestValue{Resource: "file:/junk", Attr: "content", Hash: fmt.Sprintf("%064x", i)}
	}
	if _, err := e.st.RecordGuestValues(e.ctx, "web1", junk); err != nil {
		t.Fatal(err)
	}
	e.motd("flood-value") // past the cap: nothing stored, the Target is flagged
	if n, err := e.st.GuestValueCount(e.ctx, "web1"); err != nil || n != store.GuestValueCap {
		t.Fatalf("rows %d %v, want the cap", n, err)
	}
	// The first value is still there.
	p := e.proposeDocs(e.token(TierPlan, ""), targetDoc("web2", "key-value"))
	guestOnly(t, p, id, "web2", "key-value")
	// Any Plan that touches web1 is human-only, with no match.
	q := e.proposeDocs(e.token(TierPlan, ""), targetDoc("web1", "brand-new"))
	if !slices.Contains(q.HumanOnly, plan.HumanOnlyAcceptGuestData) || q.Matches != nil {
		t.Fatalf("flagged Target: %+v", q)
	}
	e.tokenRefused(q.ID)
	vals, err := e.st.OpenIssue(e.ctx, "web1", issue.GuestValuesID)
	if err != nil {
		t.Fatalf("no agent:values Issue: %v", err)
	}
	// D-353 (K2 of #248): while web1 is flagged, a new value on another
	// Target is human-only too, and names the agent:values Issue.
	other := e.proposeDocs(e.token(TierPlan, ""), targetDoc("web2", "after-the-cap"))
	if !slices.Contains(other.HumanOnly, plan.HumanOnlyAcceptGuestData) || !slices.Equal(other.Issues, []int64{vals.ID}) || other.Matches != nil {
		t.Fatalf("a new value on another Target while web1 is flagged: %+v", other)
	}
	rc := &recorder{t: t}
	_, body := rc.do(adm, "GET", "/api/v1/issues/"+itoa(vals.ID), "/issues/{id}", nil)
	if !strings.Contains(string(body), `"actions":["ignore_once","ignore_rule"]`) || !strings.Contains(string(body), `"cap reached"`) {
		t.Errorf("agent:values: %s", body)
	}
	rc.check()
	if code, _ := adm.do("POST", "/api/v1/issues/"+itoa(vals.ID)+"/resolve", resolveBody(issue.ActionRevert)); code != 409 {
		t.Errorf("revert of agent:values: %d, want 409", code)
	}
	// 31 days later: the flag ends, the Issue resolves as gone, and the
	// rows of ended drift go (the open motd Issue keeps its rows).
	later := time.Now().Add(31 * 24 * time.Hour)
	e.st.Now = func() time.Time { return later }
	if err := e.iss.PruneGuestValues(e.ctx); err != nil {
		t.Fatal(err)
	}
	if iss, err := e.st.GetIssue(e.ctx, vals.ID); err != nil || iss.Status != store.IssueResolved || iss.Resolution != "gone" {
		t.Fatalf("agent:values after the flag: %+v %v", iss, err)
	}
	if n, _ := e.st.GuestValueCount(e.ctx, "web1"); n == 0 || n >= store.GuestValueCap {
		t.Errorf("rows after the prune: %d", n)
	}
	if flagged, _ := e.st.GuestValueFlagged(e.ctx, []string{"web1"}, later); len(flagged) != 0 {
		t.Errorf("still flagged: %v", flagged)
	}
}

// keysDocs is a Block "keys" that gives user bob the keys, and a VM web2
// that uses it.
func keysDocs(list ...string) []state.Doc {
	b, _ := json.Marshal(list)
	return []state.Doc{doc(state.KindBlock, "keys", `{"resources":[{"user":{"name":"bob","ssh_keys":`+string(b)+`}}]}`),
		doc(state.KindTarget, "web2", `{"type":"vm","image":"ubuntu","guest":{"cores":2,"memory":1024,"disk":10,"bridge":"vmbr0"},"blocks":[{"use":"keys"}]}`)}
}

func junkKeys(n int) []any {
	out := make([]any, n)
	for i := range out {
		out[i] = fmt.Sprintf("ssh-ed25519 AAAAjunk%05d", i)
	}
	return out
}

// K1 of #248: a reported value past state.MaxGuestElems fails closed. 4096
// junk keys and then key K in one report flag the Target, so a copy of K
// is human-only although K was not hashed.
func TestCopyRuleElementLimitFailsClosed(t *testing.T) {
	e := newEnv(t, true)
	adm := e.admin()
	e.seed(adm, "web1")
	const evil = "ssh-ed25519 AAAAzzzzevil"
	from := append(junkKeys(state.MaxGuestElems), evil)
	if err := e.iss.Intake(e.ctx, "web1", wire.Report{Type: wire.ReportDrift, Items: []wire.Item{{Resource: "user:alice", Status: wire.StatusDiffers,
		Changes: []wire.Change{{Attr: "ssh_keys", From: from, To: []any{}, Risk: wire.RiskDisruptive}}}}}); err != nil {
		t.Fatal(err)
	}
	if flagged, _ := e.st.GuestValueFlagged(e.ctx, []string{"web1"}, time.Now()); len(flagged) != 1 {
		t.Fatal("a value past the element limit did not flag the Target")
	}
	vals, err := e.st.OpenIssue(e.ctx, "web1", issue.GuestValuesID)
	if err != nil {
		t.Fatalf("no agent:values Issue: %v", err)
	}
	p := e.proposeDocs(e.token(TierPlan, ""), keysDocs("ssh-ed25519 AAAAmine", evil)...)
	if !slices.Contains(p.HumanOnly, plan.HumanOnlyAcceptGuestData) || !slices.Contains(p.Issues, vals.ID) {
		t.Fatalf("copy of the key after the element limit: %+v", p)
	}
	e.tokenRefused(p.ID)
}

// K1 of #248, Plan side: a planned value past the limit is human-only.
func TestCopyRulePlannedElementLimit(t *testing.T) {
	e := newEnv(t, true)
	adm := e.admin()
	e.seed(adm, "web1")
	var keys []string
	for _, k := range junkKeys(state.MaxGuestElems + 1) {
		keys = append(keys, k.(string))
	}
	p := e.proposeDocs(e.token(TierPlan, ""), keysDocs(keys...)...)
	if !slices.Contains(p.HumanOnly, plan.HumanOnlyAcceptGuestData) {
		t.Fatalf("a planned value past the element limit: %+v", p.HumanOnly)
	}
	small := e.proposeDocs(e.token(TierPlan, ""), keysDocs(keys[:10]...)...)
	if len(small.HumanOnly) != 0 {
		t.Fatalf("a small list: %+v", small.HumanOnly)
	}
}

// K3 of #248 (#218 M1): a hand-made SSH source in the guest's firewall,
// copied later into ssh_sources of another Target, is human-only.
func TestCopyRuleFirewallSSHSource(t *testing.T) {
	e := newEnv(t, true)
	adm := e.admin()
	e.seed(adm, "web1")
	if err := e.iss.Intake(e.ctx, "web1", wire.Report{Type: wire.ReportDrift, Items: []wire.Item{{Resource: "firewall:main", Status: wire.StatusDiffers,
		Changes: []wire.Change{{Attr: "ssh", From: "tcp/22 from 203.0.113.7/32", To: "tcp/22 from everywhere", Risk: wire.RiskDisruptive}}}}}); err != nil {
		t.Fatal(err)
	}
	iss, err := e.st.OpenIssue(e.ctx, "web1", "firewall:main")
	if err != nil {
		t.Fatal(err)
	}
	fw := func(src string) []state.Doc {
		return []state.Doc{doc(state.KindBlock, "fw", `{"resources":[{"firewall":{"name":"main","ssh_sources":"`+src+`"}}]}`),
			doc(state.KindTarget, "web3", `{"type":"vm","image":"ubuntu","guest":{"cores":2,"memory":1024,"disk":10,"bridge":"vmbr0"},"blocks":[{"use":"fw"}]}`)}
	}
	p := e.proposeDocs(e.token(TierPlan, ""), fw("198.51.100.0/24,203.0.113.7/32")...)
	want := plan.GuestDataMatch{IssueID: iss.ID, Target: "web3", Resource: "firewall:main", Attr: "ssh_sources", Element: "203.0.113.7/32"}
	if !slices.Contains(p.HumanOnly, plan.HumanOnlyAcceptGuestData) || !slices.Equal(p.Matches, []plan.GuestDataMatch{want}) {
		t.Fatalf("copied SSH source: %+v", p)
	}
	if code, body := adm.do("DELETE", "/api/v1/plans/"+itoa(p.ID), nil); code >= 300 {
		t.Fatalf("drop: %d %s", code, body)
	}
	if q := e.proposeDocs(e.token(TierPlan, ""), fw("198.51.100.0/24")...); len(q.HumanOnly) != 0 {
		t.Fatalf("own SSH source: %+v", q)
	}
}
