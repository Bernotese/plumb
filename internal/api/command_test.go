package api

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/Bernotese/plumb/internal/plan"
	"github.com/Bernotese/plumb/internal/state"
	"github.com/Bernotese/plumb/internal/store"
	"github.com/Bernotese/plumb/internal/wire"
)

// T-094: a command is always destructive in a Plan (D-260, D-031): no
// token approves it, also when the agent reports a lower risk (the test
// agent reports every change as reversible) and for a new Target, whose
// resources nobody observed.
func TestCommandOnlyHumanApproves(t *testing.T) {
	e := newEnv(t, true)
	adm := e.admin()
	e.seed(adm, "web1")
	planTok := e.token(TierPlan, "")
	dis := e.token(TierApply, wire.RiskDisruptive)

	cmd := doc(state.KindBlock, "init", `{"resources":[{"command":{"name":"init","argv":["/usr/local/bin/init"],"creates":"/var/lib/init.done"}}]}`)
	withInit := func(name string) state.Doc {
		return doc(state.KindTarget, name, `{"type":"vm","image":"ubuntu","guest":{"cores":1,"memory":1024,"disk":10,"bridge":"vmbr0"},`+
			`"blocks":[{"use":"motd","params":{"text":"hello"}},{"use":"init"}]}`)
	}
	propose := func(body any) (int64, string) {
		t.Helper()
		code, b := planTok.do("POST", "/api/v1/plans", body)
		if code != 201 {
			t.Fatalf("propose: %d %s", code, b)
		}
		var p struct {
			ID   int64
			Risk string
		}
		if err := json.Unmarshal(b, &p); err != nil {
			t.Fatal(err)
		}
		return p.ID, p.Risk
	}
	for _, tc := range []struct {
		name string
		docs []state.Doc
	}{
		{"existing target", []state.Doc{cmd, withInit("web1")}},
		{"new target", []state.Doc{cmd, withInit("web2")}},
	} {
		id, risk := propose(map[string]any{"reason": "init", "put": tc.docs})
		if risk != "destructive" {
			t.Fatalf("%s: plan risk %s, want destructive", tc.name, risk)
		}
		if _, err := e.eng.Approve(e.ctx, id, plan.PrincipalSystem, plan.SystemCheck); !errors.Is(err, plan.ErrNotSystemPlan) {
			t.Fatalf("%s: system approval: %v", tc.name, err)
		}
		p, err := e.st.GetPlan(e.ctx, id)
		if err != nil || p.Status != store.PlanPending {
			t.Fatalf("refused approval changed Plan: %+v, %v", p, err)
		}
		code, b := dis.do("POST", "/api/v1/plans/"+itoa(id)+"/approve", map[string]string{"comment": "ok"})
		if code != 403 || problemType(b) != "risk-too-high" {
			t.Errorf("%s: token approves a command: %d %s", tc.name, code, b)
		}
		if tc.name == "existing target" {
			if code, b := adm.do("POST", "/api/v1/plans/"+itoa(id)+"/approve", map[string]string{"comment": "ok"}); code != 202 {
				t.Fatalf("human approval: %d %s", code, b)
			}
			e.eng.Wait()
		}
	}
}

// Exercise proposal validation against the saved head, including Block-only edits.
func TestCommandImageProposalRefusals(t *testing.T) {
	e := newEnv(t, false, withBuilds)
	adm := e.admin()
	docs := builtImageDoc("web", "hi")
	command := doc(state.KindBlock, "banner", `{"resources":[{"command":{"name":"init","argv":["/bin/true"],"creates":"/done"}}]}`)
	assertRefused := func(put []state.Doc) {
		t.Helper()
		code, b := adm.do("POST", "/api/v1/plans", map[string]any{"reason": "command in image", "put": put})
		if code != 422 || !strings.Contains(string(b), "cannot hold a command") {
			t.Fatalf("Image command proposal: %d %s", code, b)
		}
	}
	assertRefused([]state.Doc{command, docs[1]})
	e.runPlanAnyStatus(adm, map[string]any{"reason": "safe image", "put": docs})
	assertRefused([]state.Doc{command})
}
