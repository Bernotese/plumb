package api

import (
	"bytes"
	"strings"
	"testing"

	"github.com/Bernotese/plumb/internal/plan"
	"github.com/Bernotese/plumb/internal/state"
	"github.com/Bernotese/plumb/internal/wire"
)

func TestQASecretBindingBoundary(t *testing.T) {
	for _, mode := range []string{"new-target", "new-resource", "other-name", "rollback", "import-like"} {
		t.Run(mode, func(t *testing.T) {
			e := newEnv(t, true)
			adm := e.admin()
			e.seed(adm, "web1")
			target := "web1"
			a := e.agents[target]
			if mode == "new-target" {
				target = "web2"
				a = e.agent(target)
			}
			propose := func(c *client, docs ...state.Doc) string {
				t.Helper()
				code, b := c.do("POST", "/api/v1/plans", map[string]any{"reason": "boundary", "put": docs})
				if code != 201 {
					t.Fatalf("propose: %d %s", code, b)
				}
				return "/api/v1/plans/" + e.planID(b)
			}
			approve := func(p string) {
				t.Helper()
				if c, b := adm.do("POST", p+"/approve", nil); c != 202 {
					t.Fatalf("approve: %d %s", c, b)
				}
				e.eng.Wait()
			}
			adm.do("PUT", "/api/v1/secrets/app-db", map[string]string{"value": "old-password"})
			var bound int64
			if mode == "new-resource" || mode == "other-name" || mode == "rollback" {
				approve(propose(adm, appBlock("/etc/app.env"), appTarget("web1", "app-db")))
				h, _ := e.st.Head(e.ctx)
				bound = h.ID
			}
			if mode == "rollback" {
				approve(propose(adm, targetDoc("web1", "unbound")))
			}
			secretName, resource := "app-db", "file:/etc/app.env"
			if mode == "other-name" {
				secretName = "another-name"
			}
			canary := "boundary-canary-" + mode
			adm.do("PUT", "/api/v1/secrets/"+secretName, map[string]string{"value": canary})
			// Bodies records every raw command body before the fake processes it.
			skip := len(a.Bodies())
			holds := func() bool { return bodiesHold(a, skip, canary) }
			block := appBlock("/etc/app.env")
			if mode == "new-resource" {
				block.Spec = bytes.ReplaceAll(block.Spec, []byte("/etc/app.env"), []byte("/etc/new.env"))
				resource = "file:/etc/new.env"
			}
			docs := []state.Doc{block, appTarget(target, secretName)}
			tok := e.token(TierPlan, "")
			var p string
			switch mode {
			case "rollback":
				c, b := tok.do("POST", "/api/v1/revisions/"+itoa(bound)+"/rollback", map[string]string{"reason": "restore binding"})
				if c != 201 {
					t.Fatalf("rollback: %d %s", c, b)
				}
				p = "/api/v1/plans/" + e.planID(b)
			case "import-like":
				// This branch has no import handler; exercise its reachable engine origin.
				pl, err := e.eng.Propose(e.ctx, plan.Input{Author: "user:admin", Origin: plan.OriginImport, Reason: "import-like", Proposal: plan.Proposal{Put: docs}})
				if err != nil {
					t.Fatal(err)
				}
				p = "/api/v1/plans/" + itoa(pl.ID)
			default:
				p = propose(tok, docs...)
			}
			_, b := adm.do("GET", p, nil)
			if !strings.Contains(string(b), `"human_only":["secret-binding"]`) {
				t.Fatalf("missing human-only: %s", b)
			}
			if c, b := e.token(TierApply, wire.RiskDisruptive).do("POST", p+"/approve", nil); c != 403 || problemType(b) != "human-required" {
				t.Fatalf("token approval: %d %s", c, b)
			}
			if holds() {
				t.Fatal("secret reached agent before human approval")
			}
			approve(p)
			e.waitGuest(target, resource, "DB_PASSWORD="+canary+"\n")
			if !holds() {
				t.Fatal("approved value never reached agent")
			}
		})
	}
}
