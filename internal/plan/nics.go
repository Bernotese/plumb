package plan

// More NICs of a Target (slice 7, T-219, D-399 … D-404). The document
// lists them in guest.nics; the plan gives each Plumb's MAC (D-400) and
// checks the network against the install before any Proxmox call, at
// propose and again at approval (D-401). An adopted Target stays
// single-NIC (D-404).

import (
	"errors"
	"fmt"

	"github.com/Bernotese/plumb/internal/driver"
	"github.com/Bernotese/plumb/internal/state"
	"github.com/Bernotese/plumb/internal/wire"
)

// ErrNetworkNotAllowed is wrapped when a NIC uses a bridge or bridge/tag
// that the install did not name (422 network-not-allowed, D-401).
var ErrNetworkNotAllowed = errors.New("network-not-allowed")

// ErrNICsNotAdoptable is wrapped when an adopted Target has guest.nics
// (422 nics-not-adoptable, D-404).
var ErrNICsNotAdoptable = errors.New("nics-not-adoptable")

// netAllowed is Config.NetAllowed; without one (serve --dev, the fake
// driver) every network is allowed.
func (e *Engine) netAllowed(bridge string, tag int) bool {
	return e.cfg.NetAllowed == nil || e.cfg.NetAllowed(bridge, tag)
}

// netFlags names a network that the install does not name, and every
// installer flag that it needs (T-253, host 3b): a tag needs its
// --extra-vlan, and its bridge an --extra-bridge when the install does not
// name the bridge either.
func (e *Engine) netFlags(bridge string, tag int) (what, flag string) {
	if tag == 0 {
		return "bridge " + bridge, "--extra-bridge " + bridge
	}
	what, flag = fmt.Sprintf("bridge %s tag %d", bridge, tag), fmt.Sprintf("--extra-vlan %s:%d", bridge, tag)
	if !e.netAllowed(bridge, 0) {
		flag = "--extra-bridge " + bridge + " " + flag
	}
	return what, flag
}

// repairHint is the way out for a network that the install does not name
// (T-253 L2 of #384): a repair run sets the whole extra list from its
// flags, and a line that it does not name is removed. So the hint names
// every flag of the install now, plus the new ones.
func repairHint(flag string) string {
	return "run install.sh again as a repair run with every --extra-bridge and --extra-vlan that the install has now, plus " + flag
}

// notAllowed is the ProposalError of a NIC on an unnamed network.
func (e *Engine) notAllowed(target string, n int, nic driver.NIC) error {
	what, flag := e.netFlags(nic.Bridge, nic.Tag)
	return &ProposalError{Target: target, Err: ErrNetworkNotAllowed,
		Msg: fmt.Sprintf("NIC %d: %s is not named by the install; the Proxmox token has no right on it. A person must %s", n, what, repairHint(flag))}
}

// docNICs reads guest.nics of a rendered Target into driver NICs with
// Plumb's MAC. A VM NIC carries no address: the agent's network resource
// sets it (D-403). It checks the network and the adopted rule.
func (e *Engine) docNICs(target string, r *state.Rendered) ([]driver.NIC, error) {
	raw, ok := r.Guest.Attrs["nics"]
	if !ok || raw == nil {
		return nil, nil
	}
	list, _ := raw.([]any)
	if len(list) == 0 {
		return nil, nil
	}
	if r.Adopted != nil {
		return nil, &ProposalError{Target: target, Err: ErrNICsNotAdoptable,
			Msg: "an adopted Target stays single-NIC; remove guest.nics"}
	}
	if len(list) > driver.MaxNICs {
		return nil, &ProposalError{Target: target, Msg: fmt.Sprintf("guest.nics has %d NICs; at most %d", len(list), driver.MaxNICs)}
	}
	if e.cfg.InstallID == "" {
		return nil, &ProposalError{Target: target, Msg: "more NICs need the install id (PLUMB_INSTALL_ID) for their MAC addresses; run a repair run of install.sh"}
	}
	out := make([]driver.NIC, 0, len(list))
	gateways := 0 // D-399: at most one over NIC 0 and guest.nics (one default route)
	if g, _ := r.Guest.Attrs["gateway"].(string); g != "" {
		gateways++
	}
	for i, item := range list {
		m, _ := item.(map[string]any)
		nic := driver.NIC{MAC: driver.NICMAC(e.cfg.InstallID, target, i+1)}
		nic.Bridge, _ = m["bridge"].(string)
		if t, ok := m["tag"]; ok && t != nil {
			tag, err := driver.IntValue(t)
			if err != nil || tag < 1 || tag > 4094 {
				return nil, &ProposalError{Target: target, Msg: fmt.Sprintf("NIC %d: bad VLAN tag %v", i+1, t)}
			}
			nic.Tag = tag
		}
		if nic.Bridge == "" {
			return nil, &ProposalError{Target: target, Msg: fmt.Sprintf("NIC %d has no bridge", i+1)}
		}
		if r.Kind == wire.KindLXC {
			nic.IP, _ = m["ip"].(string)
			nic.Gateway, _ = m["gateway"].(string)
		}
		if g, _ := m["gateway"].(string); g != "" {
			if gateways++; gateways > 1 {
				return nil, &ProposalError{Target: target, Msg: fmt.Sprintf("NIC %d: a second gateway; at most one gateway over guest.gateway and guest.nics", i+1)}
			}
		}
		if !e.netAllowed(nic.Bridge, nic.Tag) {
			return nil, e.notAllowed(target, i+1, nic)
		}
		out = append(out, nic)
	}
	return out, nil
}

// checkPlanNICs checks the NIC changes of a Plan again at approval: the
// install may have lost a network since the proposal (a repair run), and
// an adopted Target never gets a NIC (D-401, D-404).
func (e *Engine) checkPlanNICs(targets []TargetPlan) error {
	for _, tp := range targets {
		for _, rc := range tp.Resources {
			if rc.Resource != wire.GuestConfigID {
				continue
			}
			for _, c := range rc.Changes {
				n := driver.NICIndex(c.Attr)
				if n == 0 || c.To == nil {
					continue
				}
				if tp.Adopted != nil {
					return &ProposalError{Target: tp.Target, Err: ErrNICsNotAdoptable, Msg: "an adopted Target stays single-NIC"}
				}
				nic, err := driver.ParseNICValue(c.To)
				if err != nil {
					return &ProposalError{Target: tp.Target, Msg: err.Error()}
				}
				if !e.netAllowed(nic.Bridge, nic.Tag) {
					return e.notAllowed(tp.Target, n, nic)
				}
			}
		}
	}
	return nil
}
