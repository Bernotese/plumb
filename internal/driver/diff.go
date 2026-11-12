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

// Outside lists the container attributes that only a person on the host
// can change (T-038 §3): Diff returns an *OutsideError for them.
var Outside = []string{AttrMounts, AttrDevices, AttrRawLXC}

// IsOutside reports whether attr is in Outside.
func IsOutside(attr string) bool { return slices.Contains(Outside, attr) }

// guestRisk is the risk of a change of a mutable setting. A change of
// cores, memory or bridge can need a restart or drop the network for a
// moment, so all are disruptive (D-063). A write of a new value of cores
// or memory stays disruptive also when it only waits: it decides what the
// guest is after its next cold start, and that can come with no Plan.
var guestRisk = map[string]wire.Risk{
	AttrCores:  wire.RiskDisruptive,
	AttrMemory: wire.RiskDisruptive,
	AttrBridge: wire.RiskDisruptive,
}

// ColdStart lists the attributes whose new value can wait for the next
// stop and start of a running guest without being drift (D-195): Proxmox
// applies them live only with hotplug and NUMA settings that Plumb does
// not set.
var ColdStart = []string{AttrCores, AttrMemory}

// IsColdStart reports whether attr is in ColdStart.
func IsColdStart(attr string) bool { return slices.Contains(ColdStart, attr) }

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
	for _, c := range changes {
		if IsOutside(c.Attr) {
			return nil, &OutsideError{Attr: c.Attr}
		}
	}
	return changes, nil
}

// DriftGuest lists every difference, the create-only ones included, for
// a drift report: a create-only setting changed outside Plumb has the
// risk destructive, because only a new guest can bring it back.
//
// Each attribute has three views (D-190): R the running value, P the
// value after the next cold start, D the desired value.
//
//	R = D, P = D: match
//	R = D, P ≠ D: pending drift {from: P, to: D, pending: true}
//	R ≠ D, P = D: waits (cores, memory: no drift, see WaitingGuest);
//	              drift {from: R, to: D, also_pending: P} for the rest
//	R ≠ D, P ≠ D: drift {from: R, to: D}, also_pending: P when P ≠ R
//
// A write that only removes a pending value of a mutable setting leaves
// the running guest as it is, so its risk is reversible (D-191).
func DriftGuest(desired GuestSpec, observed GuestState) []wire.Change {
	have, next := observed.Spec, observed.Next()
	var out []wire.Change
	// keepRisk: a pending difference keeps the risk (create-only settings
	// and container additions); otherwise it is reversible (D-191).
	add := func(attr string, r, p, d any, risk wire.Risk, keepRisk bool) {
		switch {
		case equal(r, d) && equal(p, d):
		case equal(r, d):
			if !keepRisk {
				risk = wire.RiskReversible
			}
			out = append(out, wire.Change{Attr: attr, From: p, To: d, Risk: risk, Pending: true})
		case equal(p, d) && (IsColdStart(attr) || attr == AttrFeatures):
			// waits for a restart: a state, not drift (D-195); a container
			// takes its features at the next start (host C/D4)
		default:
			c := wire.Change{Attr: attr, From: r, To: d, Risk: risk}
			if !equal(p, r) {
				c.AlsoPending = p
			}
			out = append(out, c)
		}
	}
	for _, attr := range CreateOnly {
		add(attr, createOnlyValue(have, attr), createOnlyValue(next, attr), createOnlyValue(desired, attr), wire.RiskDestructive, true)
	}
	add(AttrCores, have.Cores, next.Cores, desired.Cores, guestRisk[AttrCores], false)
	add(AttrMemory, have.MemoryMB, next.MemoryMB, desired.MemoryMB, guestRisk[AttrMemory], false)
	add(AttrBridge, have.Bridge, next.Bridge, desired.Bridge, guestRisk[AttrBridge], false)
	// Container settings: Plumb writes only features; the rest is what a
	// person added on the host. Both can give a container more power on
	// the host, so the risk is destructive (T-038 §3, review S6).
	// keepRisk for features too: a feature that only waits for the next
	// start gives the container that power then (QA #170).
	add(AttrFeatures, have.Features, next.Features, desired.Features, wire.RiskDestructive, true)
	add(AttrMounts, list(have.Mounts), list(next.Mounts), list(desired.Mounts), wire.RiskDestructive, true)
	add(AttrDevices, list(have.Devices), list(next.Devices), list(desired.Devices), wire.RiskDestructive, true)
	add(AttrRawLXC, list(have.RawLXC), list(next.RawLXC), list(desired.RawLXC), wire.RiskDestructive, true)
	return out
}

// WaitingGuest lists the values of cores and memory that are set and wait
// for the next stop and start (D-195): the running value differs from the
// desired one, and the value after the next cold start is the desired
// one. Who wrote the value does not matter. Each entry is {from: R,
// to: D, waits_for_restart: true} with the risk read: nothing needs to
// be written for it.
func WaitingGuest(desired GuestSpec, observed GuestState) []wire.Change {
	have, next := observed.Spec, observed.Next()
	var out []wire.Change
	add := func(attr string, r, p, d int) {
		if r != d && p == d {
			out = append(out, wire.Change{Attr: attr, From: r, To: d, Risk: wire.RiskRead, WaitsForRestart: true})
		}
	}
	add(AttrCores, have.Cores, next.Cores, desired.Cores)
	add(AttrMemory, have.MemoryMB, next.MemoryMB, desired.MemoryMB)
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

// list is a string list as a change value: nil when empty.
func list(s []string) any {
	if len(s) == 0 {
		return []string(nil)
	}
	return s
}

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
