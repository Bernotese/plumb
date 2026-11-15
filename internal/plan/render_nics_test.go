package plan

import (
	"strings"
	"testing"

	"github.com/Bernotese/plumb/internal/driver"
	"github.com/Bernotese/plumb/internal/state"
)

// T-220: every render of the engine (Plan, Run, watch, the agent gate)
// holds the network resources of the more NICs, with Plumb's MAC of each
// card (D-400, D-403).
func TestEngineRenderAddsNICNetworks(t *testing.T) {
	img := state.Doc{Kind: state.KindImage, Name: "deb", Spec: []byte(`{"type":"vm","url":"https://example.invalid/i.img","sha256":"` + strings.Repeat("0", 64) + `"}`)}
	tgt := state.Doc{Kind: state.KindTarget, Name: "web1", Spec: []byte(`{"type":"vm","image":"deb","guest":{"cores":1,"memory":512,"disk":4,"bridge":"vmbr0","nics":[{"bridge":"vmbr9","ip":"10.9.20.5/24"}]}}`)}
	set, err := state.Validate([]state.Doc{img, tgt})
	if err != nil {
		t.Fatal(err)
	}
	e := &Engine{cfg: Config{InstallID: "inst-1"}}
	r, err := e.renderOrNil(set, "web1")
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, res := range r.Resources {
		if res.ID == "network:nic1" {
			found = true
			if res.Attrs["mac"] != driver.NICMAC("inst-1", "web1", 1) || res.Attrs["addresses"] != "10.9.20.5/24" {
				t.Fatalf("nic1 %v", res.Attrs)
			}
		}
	}
	if !found {
		t.Fatalf("no network:nic1 in %+v", r.Resources)
	}
	// The plain render (firewallOff only) has none.
	if p, _ := renderPlain(set, "web1"); len(p.Resources) != len(r.Resources)-1 {
		t.Fatalf("plain render %+v", p.Resources)
	}
	// No install id: no MAC, refused with the reason.
	if _, err := (&Engine{}).renderOrNil(set, "web1"); err == nil || !strings.Contains(err.Error(), "install id") {
		t.Fatalf("no install id: %v", err)
	}
	// A Target without nics needs no install id.
	plain := state.Doc{Kind: state.KindTarget, Name: "web2", Spec: []byte(`{"type":"vm","image":"deb","guest":{"cores":1,"memory":512,"disk":4,"bridge":"vmbr0"}}`)}
	set2, _ := state.Validate([]state.Doc{img, plain})
	if _, err := (&Engine{}).renderOrNil(set2, "web2"); err != nil {
		t.Fatalf("without nics: %v", err)
	}
}
