package api

import (
	"encoding/json"
	"testing"

	"github.com/Bernotese/plumb/internal/state"
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
