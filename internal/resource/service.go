package resource

import (
	"context"
	"fmt"
	"strings"

	"github.com/Bernotese/plumb/internal/wire"
)

// Service states of the schema.
const (
	ServiceRunning = "running"
	ServiceStopped = "stopped"
)

// unitMasked is the observed "enabled" value of a masked unit.
const unitMasked = "masked"

// unitSuffixes are the systemd unit types. A name without one is a service.
var unitSuffixes = []string{".service", ".socket", ".timer", ".target", ".mount", ".automount", ".path", ".slice", ".scope", ".swap", ".device"}

// service is a systemd unit (schema/service.json).
type service struct {
	base
	a    svcAttrs
	unit string
}

type svcAttrs struct {
	Name      string   `json:"name"`
	State     string   `json:"state"`
	Enabled   bool     `json:"enabled"`
	RestartOn []string `json:"restart_on"`
}

func newService(r wire.Resource, env Env) (Resource, error) {
	s := &service{base: base{res: r, env: env}, a: svcAttrs{State: ServiceRunning, Enabled: true}}
	if err := decode(r, "service", &s.a, func() string { return s.a.Name }); err != nil {
		return nil, err
	}
	s.unit = unitName(s.a.Name)
	return s, nil
}

func unitName(name string) string {
	for _, suf := range unitSuffixes {
		if strings.HasSuffix(name, suf) {
			return name
		}
	}
	return name + ".service"
}

// RestartChange returns the restart change for a service when at least one
// of the changed resource ids is in its restart_on list (D-036). It returns
// one change however many listed resources changed. The agent adds it to the
// changes of the service and applies the service after those resources.
func RestartChange(svc wire.Resource, changed []string) (wire.Change, bool) {
	if svc.Type != "service" {
		return wire.Change{}, false
	}
	for _, id := range wire.RestartOn(svc) {
		for _, c := range changed {
			if c == id {
				return wire.Change{Attr: wire.AttrRestart, Risk: wire.RiskDisruptive}, true
			}
		}
	}
	return wire.Change{}, false
}

// NewUnitCovers is the one exception to wire.Covers (T-103, D-075). A Plan
// that observed a service before its unit existed shows "state: absent →
// S" and "enabled: null → E". When an earlier step of the same apply made
// the unit, the agent observes the new unit as "stopped" and, when it has
// an [Install] section, "enabled: false": a direct result of that step,
// not a change of the guest that the Plan did not show.
//
// made are the resources of the apply that the service waited for and
// that this apply created. Only a maker of the unit counts: the unit
// resource of the same name, the file /etc/systemd/system/<unit>, or a
// package. Then "state: stopped → S" is covered by the planned "state:
// absent → S" and "enabled: false → E" by the planned "enabled: null → E",
// each with the same to and risk. A stopped service may plan only the
// enable change when its unit is absent. Every other change must be covered by
// wire.Covers as before.
func NewUnitCovers(svc wire.Resource, planned, now []wire.Change, made []wire.Resource) bool {
	if svc.Type != "service" {
		return false
	}
	name, _ := svc.Attrs["name"].(string)
	if name == "" {
		return false
	}
	unit := unitName(name)
	maker := false
	for _, r := range made {
		switch r.Type {
		case "unit":
			maker = maker || r.Attrs["name"] == unit
		case "file":
			maker = maker || r.Attrs["path"] == unitDir+unit
		case "package":
			maker = true
		}
	}
	if !maker {
		return false
	}
	// plannedFrom finds the planned change of attr with the given from and
	// the same to and risk as c.
	plannedFrom := func(c wire.Change, from any) bool {
		for _, p := range planned {
			if p.Attr == c.Attr && !p.Sensitive && p.Risk == c.Risk &&
				wire.SameValue(p.From, from) && wire.SameValue(p.To, c.To) {
				return true
			}
		}
		return false
	}
	created, statePlanned, enableAbsent := false, false, false
	for _, p := range planned {
		statePlanned = statePlanned || p.Attr == wire.AttrState
		created = created || (p.Attr == wire.AttrState && wire.SameValue(p.From, wire.StateAbsent))
		enableAbsent = enableAbsent || (p.Attr == "enabled" && p.From == nil && wire.SameValue(p.To, true))
	}
	if !created && (statePlanned || !enableAbsent) {
		return false
	}
	for _, c := range now {
		switch {
		case wire.Covers(planned, []wire.Change{c}):
		case c.Attr == wire.AttrState && wire.SameValue(c.From, ServiceStopped) && plannedFrom(c, wire.StateAbsent):
		case c.Attr == "enabled" && wire.SameValue(c.From, false) && plannedFrom(c, nil):
		default:
			return false
		}
	}
	return true
}

// Observe asks systemd for the load, active and enablement state. A unit
// that systemd does not know is absent. "enabled" is left out when the unit
// cannot be enabled or disabled (static, generated, indirect ...). A masked
// unit is observed as enabled "masked"; Plumb does not unmask units, so
// Apply fails with a clear message for it.
func (s *service) Observe(ctx context.Context) (State, error) {
	out, err := s.run(ctx, "systemctl", "show", "--property=LoadState,ActiveState,UnitFileState", "--", s.unit)
	if err != nil {
		return nil, err
	}
	props := map[string]string{}
	for _, line := range strings.Split(string(out), "\n") {
		if k, v, ok := strings.Cut(strings.TrimSpace(line), "="); ok {
			props[k] = v
		}
	}
	if props["LoadState"] == "" {
		return nil, fmt.Errorf("systemctl show %s: unexpected output %q", s.unit, clip(string(out), 200))
	}
	if props["LoadState"] == "not-found" {
		return absent(), nil
	}
	st := State{wire.AttrState: ServiceStopped}
	switch props["ActiveState"] {
	case "active", "activating", "reloading", "refreshing":
		st[wire.AttrState] = ServiceRunning
	}
	switch props["UnitFileState"] {
	case "enabled", "enabled-runtime":
		st["enabled"] = true
	case "disabled":
		st["enabled"] = false
	case "masked", "masked-runtime":
		st["enabled"] = unitMasked
	}
	return st, nil
}

// Diff compares state and enablement. Starting and enabling are reversible;
// stopping is disruptive. A restart change is not made here (RestartChange).
func (s *service) Diff(observed State) []wire.Change {
	var out []wire.Change
	cur := observed[wire.AttrState]
	if cur == wire.StateAbsent {
		if s.a.State == ServiceStopped {
			// A unit that does not exist is not running, but may need enabling.
			if s.a.Enabled {
				out = append(out, change("enabled", nil, true, wire.RiskReversible))
			}
			return s.finish(out)
		}
		out = append(out, change(wire.AttrState, wire.StateAbsent, s.a.State, wire.RiskReversible))
		out = append(out, change("enabled", nil, s.a.Enabled, wire.RiskReversible))
		return s.finish(out)
	}
	if cur != s.a.State {
		risk := wire.RiskReversible
		if s.a.State == ServiceStopped {
			risk = wire.RiskDisruptive
		}
		out = append(out, change(wire.AttrState, cur, s.a.State, risk))
	}
	switch en := observed["enabled"].(type) {
	case bool:
		if en != s.a.Enabled {
			out = append(out, change("enabled", en, s.a.Enabled, wire.RiskReversible))
		}
	case string: // masked: a running or enabled unit is wanted, or it is drift
		if s.a.Enabled || s.a.State == ServiceRunning {
			out = append(out, change("enabled", en, s.a.Enabled, wire.RiskReversible))
		}
	}
	return s.finish(out)
}

// Apply enables or disables, then starts or stops, then restarts. It
// restarts at most once, and not when it has just started the unit or when
// the unit must be stopped.
func (s *service) Apply(ctx context.Context, changes []wire.Change) error {
	if len(changes) == 0 {
		return nil
	}
	for _, c := range changes {
		if c.From == unitMasked {
			return fmt.Errorf("unit %s is masked; Plumb does not unmask units (systemctl unmask -- %s)", s.unit, s.unit)
		}
	}
	ctl := func(args ...string) error {
		_, err := s.run(ctx, "systemctl", append(args, "--", s.unit)...)
		return err
	}
	if has(changes, wire.AttrState) || has(changes, wire.AttrRestart) {
		// A file resource may have changed the unit file before.
		if _, err := s.run(ctx, "systemctl", "daemon-reload"); err != nil {
			return err
		}
	}
	if has(changes, "enabled") {
		verb := "disable"
		if s.a.Enabled {
			verb = "enable"
		}
		if err := ctl(verb); err != nil {
			return err
		}
	}
	started := false
	if has(changes, wire.AttrState) {
		verb := "stop"
		if s.a.State == ServiceRunning {
			verb, started = "start", true
		}
		if err := ctl(verb); err != nil {
			return err
		}
	}
	if has(changes, wire.AttrRestart) && !started && s.a.State == ServiceRunning {
		return ctl("restart")
	}
	return nil
}

// Watch returns the unit file and its enablement link. Whether the unit
// runs has no file event; the periodic check covers it.
func (s *service) Watch() []string {
	return []string{
		s.path("/etc/systemd/system/" + s.unit),
		s.path("/etc/systemd/system/multi-user.target.wants/" + s.unit),
	}
}
