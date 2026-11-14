package api

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/Bernotese/plumb/internal/driver/fake"
)

// QA of the adoption (T-195 B6 for T-193; D-386, D-388): an apply token
// never approves an adopt Plan, a Plan that sets delete_guest, or the
// delete of an adopted Target with delete_guest; a person can. The delete
// without delete_guest keeps the guest (no Proxmox delete).
func TestAdoptQAApplyTokenRefused(t *testing.T) {
	e := newEnv(t, true, withInstall)
	adm := e.admin()
	planTok := e.token(TierPlan, "")
	vmid, digest := e.person("wiki")
	deletes := func() int { return e.drv.Calls(fake.OpDelete) }

	code, body := planTok.do("POST", "/api/v1/targets/wiki/adopt", map[string]any{"node": "pve", "vmid": vmid, "digest": digest})
	var p struct {
		ID        int64
		Risk      string
		HumanOnly []string `json:"human_only"`
	}
	json.Unmarshal(body, &p)
	if code != 201 {
		t.Fatalf("adopt: %d %s", code, body)
	}
	e.tokenRefused(p.ID)
	if code, body := adm.do("POST", "/api/v1/plans/"+itoa(p.ID)+"/approve", nil); code != 202 {
		t.Fatalf("approve: %d %s", code, body)
	}
	e.eng.Wait()

	// The Target document as it is now.
	_, body = adm.do("GET", "/api/v1/targets/wiki", nil)
	var view struct {
		Document struct{ Spec map[string]any }
	}
	err := json.Unmarshal(body, &view)
	doc := view.Document
	if err != nil || doc.Spec["adopted"] == nil {
		t.Fatalf("target: %s", body)
	}
	propose := func(c *client, req map[string]any) (int64, string, []string) {
		t.Helper()
		code, body := c.do("POST", "/api/v1/plans", req)
		json.Unmarshal(body, &p)
		if code != 201 {
			t.Fatalf("propose: %d %s", code, body)
		}
		return p.ID, p.Risk, p.HumanOnly
	}

	// delete_guest: true – human-only adopted-guest-delete, destructive.
	ad := doc.Spec["adopted"].(map[string]any)
	ad["delete_guest"] = true
	id, risk, human := propose(planTok, map[string]any{"reason": "may delete",
		"put": []map[string]any{{"kind": "Target", "name": "wiki", "spec": doc.Spec}}})
	if risk != "destructive" || !slices.Contains(human, "adopted-guest-delete") {
		t.Fatalf("delete_guest plan: risk %s human_only %v", risk, human)
	}
	e.tokenRefused(id)
	if code, body := adm.do("POST", "/api/v1/plans/"+itoa(id)+"/approve", nil); code != 202 {
		t.Fatalf("approve delete_guest: %d %s", code, body)
	}
	e.eng.Wait()

	// The delete with delete_guest: an apply token is refused again.
	id, _, human = propose(planTok, map[string]any{"reason": "delete", "delete": []map[string]any{{"kind": "Target", "name": "wiki"}}})
	if !slices.Contains(human, "adopted-guest-delete") {
		t.Fatalf("delete plan: human_only %v", human)
	}
	e.tokenRefused(id)
	if n := deletes(); n != 0 {
		t.Fatalf("%d Proxmox deletes before any approval", n)
	}

	// Back to delete_guest: false (a person), then the delete only unbinds.
	adm.do("DELETE", "/api/v1/plans/"+itoa(id), nil)
	ad["delete_guest"] = false
	id, _, _ = propose(adm, map[string]any{"reason": "keep", "put": []map[string]any{{"kind": "Target", "name": "wiki", "spec": doc.Spec}}})
	if code, body := adm.do("POST", "/api/v1/plans/"+itoa(id)+"/approve", nil); code != 202 {
		t.Fatalf("approve keep: %d %s", code, body)
	}
	e.eng.Wait()
	id, _, human = propose(adm, map[string]any{"reason": "unbind", "delete": []map[string]any{{"kind": "Target", "name": "wiki"}}})
	if slices.Contains(human, "adopted-guest-delete") {
		t.Fatalf("unbind plan is adopted-guest-delete: %v", human)
	}
	if code, body := adm.do("POST", "/api/v1/plans/"+itoa(id)+"/approve", nil); code != 202 {
		t.Fatalf("approve unbind: %d %s", code, body)
	}
	e.eng.Wait()
	if n := deletes(); n != 0 {
		t.Fatalf("the unbind made %d Proxmox deletes", n)
	}
	_, body = adm.do("GET", "/api/v1/guests/unmanaged", nil)
	if !strings.Contains(string(body), `"name":"wiki"`) {
		t.Fatalf("the unbound guest is not unmanaged again: %s", body)
	}
}
