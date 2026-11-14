package plan

import (
	"errors"
	"strings"
	"testing"

	"github.com/Bernotese/plumb/internal/state"
	"github.com/Bernotese/plumb/internal/wire"
)

// Review L1 of #332 (D-399): at most one gateway over guest.gateway and
// guest.nics; a container NIC keeps its address, a VM NIC never gets one.
func TestDocNICsGateways(t *testing.T) {
	e := &Engine{cfg: Config{InstallID: "6f1c0b4e-0000-4000-8000-000000000001"}}
	nic := func(gw string) map[string]any {
		m := map[string]any{"bridge": "vmbr9", "ip": "10.9.0.5/24"}
		if gw != "" {
			m["gateway"] = gw
		}
		return m
	}
	rendered := func(kind wire.Kind, gw string, nics ...any) *state.Rendered {
		a := map[string]any{"nics": nics}
		if gw != "" {
			a["gateway"] = gw
		}
		return &state.Rendered{Kind: kind, Guest: wire.Resource{Attrs: a}}
	}
	for name, r := range map[string]*state.Rendered{
		"NIC 0 and NIC 1": rendered(wire.KindLXC, "192.0.2.1", nic("10.9.0.1")),
		"NIC 1 and NIC 2": rendered(wire.KindLXC, "", nic("10.9.0.1"), nic("10.9.1.1")),
	} {
		_, err := e.docNICs("web1", r)
		var pe *ProposalError
		if !errors.As(err, &pe) || !strings.Contains(pe.Msg, "at most one gateway") {
			t.Errorf("%s: %v", name, err)
		}
	}
	nics, err := e.docNICs("web1", rendered(wire.KindLXC, "", nic("10.9.0.1"), nic("")))
	if err != nil || len(nics) != 2 || nics[0].Gateway != "10.9.0.1" || nics[0].IP != "10.9.0.5/24" {
		t.Errorf("one gateway: %+v %v", nics, err)
	}
	nics, err = e.docNICs("web1", rendered(wire.KindVM, "192.0.2.1", map[string]any{"bridge": "vmbr9"}))
	if err != nil || len(nics) != 1 || nics[0].IP != "" || nics[0].MAC == "" {
		t.Errorf("VM: %+v %v", nics, err)
	}
}
