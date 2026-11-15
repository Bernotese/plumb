package api

// T-219: more NICs through the API (D-399 … D-404). The fake driver takes
// any network, so only the server check refuses an unnamed one.

import (
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Bernotese/plumb/internal/driver"
	"github.com/Bernotese/plumb/internal/plan"
	"github.com/Bernotese/plumb/internal/state"
)

func nicTarget(name, nics string) state.Doc {
	return doc(state.KindTarget, name, `{"type":"vm","image":"ubuntu","guest":{"cores":2,"memory":1024,"disk":10,"bridge":"vmbr0","nics":`+nics+`},`+
		`"blocks":[{"use":"motd","params":{"text":"hello"}}]}`)
}

func TestNICsThroughTheAPI(t *testing.T) {
	var narrow atomic.Bool // a repair run without --extra-vlan vmbr9:20
	e := newEnv(t, true, func(c *plan.Config) {
		c.InstallID = "6f1c0b4e-0000-4000-8000-000000000001"
		c.NetAllowed = func(b string, tag int) bool {
			return (b == "vmbr0" && tag == 0) || (b == "vmbr9" && tag == 0) || (b == "vmbr9" && tag == 20 && !narrow.Load())
		}
	})
	adm := e.admin()
	e.seed(adm, "web1")
	propose := func(nics string) (int, []byte) {
		return adm.do("POST", "/api/v1/plans", map[string]any{"reason": "nics", "put": []state.Doc{nicTarget("web1", nics)}})
	}

	// Add a tagged NIC: one disruptive change with Plumb's MAC.
	code, body := propose(`[{"bridge":"vmbr9","tag":20}]`)
	if code != 201 {
		t.Fatalf("propose: %d %s", code, body)
	}
	var p struct {
		ID      int64
		Risk    string
		Targets []struct {
			Resources []struct {
				Resource string
				Changes  []struct {
					Attr string
					To   any
					Risk string
				}
			}
		}
	}
	json.Unmarshal(body, &p)
	mac := driver.NICMAC("6f1c0b4e-0000-4000-8000-000000000001", "web1", 1)
	found := false
	for _, r := range p.Targets[0].Resources {
		for _, c := range r.Changes {
			if c.Attr == "nic1" {
				found = c.To == "bridge=vmbr9,tag=20,mac="+mac && c.Risk == "disruptive" && r.Resource == "guest:config"
			}
		}
	}
	if !found || p.Risk != "disruptive" {
		t.Fatalf("plan: %s", body)
	}
	if code, body := adm.do("POST", "/api/v1/plans/"+itoa(p.ID)+"/approve", nil); code != 202 {
		t.Fatalf("approve: %d %s", code, body)
	}
	e.eng.Wait()
	b, _ := e.st.GetBinding(e.ctx, "Target", "web1")
	st, err := e.drv.Observe(e.ctx, driver.GuestID(b.Ref))
	if err != nil || len(st.Spec.NICs) != 1 || st.Spec.NICs[0].MAC != mac || st.Spec.NICs[0].Tag != 20 {
		t.Fatalf("guest NICs: %+v %v", st.Spec.NICs, err)
	}

	// An unnamed tag or bridge: 422 network-not-allowed before any driver call.
	for _, nics := range []string{`[{"bridge":"vmbr9","tag":30}]`, `[{"bridge":"vmbr7"}]`, `[{"bridge":"vmbr0","tag":20}]`} {
		code, body := propose(nics)
		if code != 422 || problemType(body) != "network-not-allowed" || !strings.Contains(string(body), "install.sh with --extra-") {
			t.Errorf("%s: %d %s", nics, code, body)
		}
	}

	// Again at approval: the install lost the tag after the proposal.
	code, body = propose(`[{"bridge":"vmbr9","tag":20},{"bridge":"vmbr9","tag":20}]`) // nic2 needs the tag too
	if code != 201 {
		t.Fatalf("propose 2: %d %s", code, body)
	}
	json.Unmarshal(body, &p)
	narrow.Store(true)
	if code, body := adm.do("POST", "/api/v1/plans/"+itoa(p.ID)+"/approve", nil); code != 422 || problemType(body) != "network-not-allowed" {
		t.Errorf("approve after the repair run: %d %s", code, body)
	}
}

// D-404: an adopted Target stays single-NIC.
func TestNICsNotAdoptable(t *testing.T) {
	e := newEnv(t, true, func(c *plan.Config) { c.InstallID = "6f1c0b4e-0000-4000-8000-000000000001" })
	adm := e.admin()
	e.adoptWithAgent(adm, "pet")
	head, err := e.st.Head(e.ctx)
	if err != nil {
		t.Fatal(err)
	}
	docs, err := e.st.RevisionDocs(e.ctx, head.ID)
	if err != nil {
		t.Fatal(err)
	}
	var spec map[string]any
	for _, d := range docs {
		if d.Kind == state.KindTarget && d.Name == "pet" {
			var body map[string]any
			if err := json.Unmarshal(d.Body, &body); err != nil {
				t.Fatal(err)
			}
			spec, _ = body["spec"].(map[string]any)
			if spec == nil {
				spec = body
			}
		}
	}
	if spec == nil {
		t.Fatal("no target doc")
	}
	target := state.Doc{Kind: state.KindTarget, Name: "pet"}
	spec["guest"].(map[string]any)["nics"] = []any{map[string]any{"bridge": "vmbr0"}}
	target.Spec, _ = json.Marshal(spec)
	code, body := adm.do("POST", "/api/v1/plans", map[string]any{"reason": "nics", "put": []state.Doc{target}})
	if code != 422 || problemType(body) != "nics-not-adoptable" {
		t.Errorf("adopted with nics: %d %s", code, body)
	}
}
