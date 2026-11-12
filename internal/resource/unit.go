package resource

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/Bernotese/plumb/internal/wire"
)

// unitFileNameRE is the agent's own check of a unit name (the schema has
// the same pattern): a unit file name with a type suffix that a file in
// /etc/systemd/system can have. No "/", no leading "-" or ".".
var unitFileNameRE = regexp.MustCompile(`^[A-Za-z0-9_:@][A-Za-z0-9_.:@-]*\.(service|socket|timer|path|mount|automount|swap|target|slice)$`)

// Limits of the schema.
const (
	maxUnitName    = 255
	maxUnitContent = 64 << 10
)

// unitDir is where Plumb writes unit files: the directory for units of
// the administrator, which wins over units of packages.
const unitDir = "/etc/systemd/system/"

// unitFile is a systemd unit file (schema/unit.json): the file and a
// daemon-reload, nothing more. Enabling and starting a unit, also a timer,
// is the job of a service resource with the same name.
type unitFile struct {
	base
	a unitAttrs
}

type unitAttrs struct {
	Name    string `json:"name"`
	Content string `json:"content"`
	State   string `json:"state"`
}

func newUnit(r wire.Resource, env Env) (Resource, error) {
	u := &unitFile{base: base{res: r, env: env}, a: unitAttrs{State: wire.StatePresent}}
	if err := decode(r, "unit", &u.a, func() string { return u.a.Name }); err != nil {
		return nil, err
	}
	if len(u.a.Name) > maxUnitName || !unitFileNameRE.MatchString(u.a.Name) {
		return nil, fmt.Errorf("resource %s: bad unit name", r.ID)
	}
	if wire.IsAgentUnit(u.a.Name) {
		return nil, fmt.Errorf("resource %s: the agent does not manage its own unit", r.ID)
	}
	if len(u.a.Content) > maxUnitContent {
		return nil, fmt.Errorf("resource %s: content is larger than %d bytes", r.ID, maxUnitContent)
	}
	if u.a.State == wire.StatePresent && strings.TrimSpace(u.a.Content) == "" {
		return nil, fmt.Errorf("resource %s: content is empty", r.ID)
	}
	return u, nil
}

// path0 is the unit file in the guest.
func (u *unitFile) path0() string { return unitDir + u.a.Name }

// Observe reads the unit file with its mode and owner: a unit file that a
// guest user can write runs code as root.
func (u *unitFile) Observe(_ context.Context) (State, error) {
	f, err := readManaged(u.env.Root, u.path0())
	if err != nil {
		return nil, err
	}
	if !f.exists {
		return absent(), nil
	}
	owner, grp, err := ownerNames(u.env.Root, f.uid, f.gid)
	if err != nil {
		return nil, err
	}
	return State{wire.AttrState: wire.StatePresent, "content": f.content, "mode": fmt.Sprintf("%04o", f.mode), "owner": owner, "group": grp}, nil
}

// Diff: a new or changed unit file is reversible (systemd uses it at the
// next start); removing one is destructive (a running unit loses its
// definition).
func (u *unitFile) Diff(observed State) []wire.Change {
	if u.a.State == wire.StateAbsent {
		if observed[wire.AttrState] == wire.StatePresent {
			return u.finish([]wire.Change{change(wire.AttrState, wire.StatePresent, wire.StateAbsent, wire.RiskDestructive)})
		}
		return nil
	}
	want := State{"content": u.a.Content, "mode": "0644", "owner": "root", "group": "root"}
	return u.finish(diffNode(observed, want, []string{"content", "mode", "owner", "group"}))
}

// Apply writes (always the whole file, atomically) or removes the unit
// file, then reloads systemd.
func (u *unitFile) Apply(ctx context.Context, changes []wire.Change) error {
	if len(changes) == 0 {
		return nil
	}
	var err error
	if u.a.State == wire.StateAbsent {
		err = removeManaged(u.env.Root, u.path0())
	} else {
		err = writeManaged(u.env.Root, u.path0(), []byte(u.a.Content), 0o644)
	}
	if err != nil {
		return err
	}
	_, err = u.run(ctx, "systemctl", "daemon-reload")
	return err
}

// Watch returns the unit file.
func (u *unitFile) Watch() []string { return []string{u.path(u.path0())} }
