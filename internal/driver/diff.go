package driver

import (
	"encoding/json"
	"fmt"
	"slices"

	"github.com/Bernotese/plumb/internal/wire"
)

// CreateOnly lists the guest:config attributes that cannot change after
// the guest exists (ARCHITECTURE §5: cloud-init runs once). Every driver
// uses this list through DiffGuest, so the fake and Proxmox agree.
var CreateOnly = []string{AttrKind, AttrImage, AttrDisk, AttrIP, AttrGateway, AttrUser, AttrSSHKeys}

// Attribute names of create-only settings that are not in the user's
// guest block but are still part of GuestSpec.
const (
	AttrKind  = "kind"
	AttrImage = "image"
)

// IsCreateOnly reports whether attr can be set only at creation.
func IsCreateOnly(attr string) bool { return slices.Contains(CreateOnly, attr) }

// guestRisk is the risk of a change of a mutable setting. A change of
// cores, memory or bridge can need a restart or drop the network for a
// moment, so all are disruptive.
var guestRisk = map[string]wire.Risk{
	AttrCores:  wire.RiskDisruptive,
	AttrMemory: wire.RiskDisruptive,
	AttrBridge: wire.RiskDisruptive,
}

// DiffGuest is the shared, pure Diff rule of all drivers. It lists the
// changes from the observed to the desired settings with their risk.
// A difference in a create-only setting returns a *CreateOnlyError for
// the first such attribute, in the order of CreateOnly.
func DiffGuest(desired GuestSpec, observed GuestState) ([]wire.Change, error) {
	changes := DriftGuest(desired, observed)
	for _, c := range changes {
		if IsCreateOnly(c.Attr) {
			return nil, &CreateOnlyError{Attr: c.Attr}
		}
	}
	return changes, nil
}

// DriftGuest lists every difference, the create-only ones included, for
// a drift report: a create-only setting changed outside Plumb has the
// risk destructive, because only a new guest can bring it back.
func DriftGuest(desired GuestSpec, observed GuestState) []wire.Change {
	have := observed.Spec
	var out []wire.Change
	for _, attr := range CreateOnly {
		from, to := createOnlyValue(have, attr), createOnlyValue(desired, attr)
		if !equal(from, to) {
			out = append(out, wire.Change{Attr: attr, From: from, To: to, Risk: wire.RiskDestructive})
		}
	}
	add := func(attr string, from, to any) {
		if !equal(from, to) {
			out = append(out, wire.Change{Attr: attr, From: from, To: to, Risk: guestRisk[attr]})
		}
	}
	add(AttrCores, have.Cores, desired.Cores)
	add(AttrMemory, have.MemoryMB, desired.MemoryMB)
	add(AttrBridge, have.Bridge, desired.Bridge)
	return out
}

func createOnlyValue(s GuestSpec, attr string) any {
	switch attr {
	case AttrKind:
		return s.Kind
	case AttrImage:
		return s.ImageName
	case AttrDisk:
		return s.DiskGB
	case AttrIP:
		return s.IP
	case AttrGateway:
		return s.Gateway
	case AttrUser:
		return s.User
	case AttrSSHKeys:
		if len(s.SSHKeys) == 0 {
			return []string(nil)
		}
		return s.SSHKeys
	}
	panic("driver: unknown create-only attribute " + attr)
}

func equal(a, b any) bool { return fmt.Sprint(a) == fmt.Sprint(b) }

// IntValue reads a change value as an int. Changes can come back from a
// JSON round trip (stored Plans), so json.Number and float64 are accepted.
func IntValue(v any) (int, error) {
	switch v := v.(type) {
	case int:
		return v, nil
	case int64:
		return int(v), nil
	case float64:
		if v == float64(int(v)) {
			return int(v), nil
		}
	case json.Number:
		n, err := v.Int64()
		if err == nil {
			return int(n), nil
		}
	}
	return 0, fmt.Errorf("driver: %v is not an integer", v)
}
