package issue_test

import (
	"net/http"
	"testing"

	"github.com/Bernotese/plumb/internal/driver"
	"github.com/Bernotese/plumb/internal/state"
	"github.com/Bernotese/plumb/internal/wire"
)

// T-070 review B1: guest resources belong to the controller. An agent
// item of type guest (guest:config, guest:address) or of a type that no
// agent manages is refused with 400: it neither opens nor closes an Issue,
// and no event is written.
func TestAgentCannotReportGuestItems(t *testing.T) {
	e := newEnv(t)
	e.seed("web1")
	e.mu.Lock()
	a := e.agents["web1"]
	e.mu.Unlock()
	lastSeq := func() int64 {
		n, err := e.st.LastEventSeq(e.ctx)
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	report := func(st wire.Status, res string, changes []wire.Change) int {
		return a.Report(e.ctx, wire.Report{Type: wire.ReportDrift, Items: []wire.Item{{Resource: res, Status: st, Changes: changes}}})
	}

	// Open: refused, no Issue, no event.
	seq := lastSeq()
	for _, res := range []string{wire.GuestConfigID, "guest:address", "secret:x", "nope:y"} {
		if code := report(wire.StatusDiffers, res, []wire.Change{{Attr: "cores", From: 8, To: 2, Risk: wire.RiskDisruptive}}); code != http.StatusBadRequest {
			t.Errorf("drift on %s: %d, want 400", res, code)
		}
	}
	if all, _ := e.st.Issues(e.ctx, ""); len(all) != 0 {
		t.Fatalf("agent item opened issues: %+v", all)
	}
	if n := lastSeq(); n != seq {
		t.Fatalf("agent item wrote %d events", n-seq)
	}

	// Close: a real guest:config Issue from the guest poll stays open.
	b, _ := e.st.GetBinding(e.ctx, state.KindTarget, "web1")
	e.drv.Tamper(driver.GuestID(b.Ref), func(s *driver.GuestSpec) { s.Cores = 8 })
	if err := e.eng.CheckGuests(e.ctx); err != nil {
		t.Fatal(err)
	}
	iss := e.openIssue("web1", wire.GuestConfigID)
	seq = lastSeq()
	if code := report(wire.StatusUnchanged, wire.GuestConfigID, nil); code != http.StatusBadRequest {
		t.Fatalf("unchanged guest:config from the agent: %d, want 400", code)
	}
	// A result item of type guest is refused too.
	if code := a.Report(e.ctx, wire.Report{Type: wire.ReportResult, CommandID: "x",
		Items: []wire.Item{{Resource: wire.GuestConfigID, Status: wire.StatusUnchanged}}}); code != http.StatusBadRequest {
		t.Fatalf("result with guest:config: %d, want 400", code)
	}
	got, err := e.st.GetIssue(e.ctx, iss.ID)
	if err != nil || got.Status != "open" {
		t.Fatalf("guest:config issue after the agent item: %+v %v", got, err)
	}
	if n := lastSeq(); n != seq {
		t.Fatalf("agent item wrote %d events", n-seq)
	}
}
