package plan

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/Bernotese/plumb/internal/driver"
	"github.com/Bernotese/plumb/internal/state"
	"github.com/Bernotese/plumb/internal/store"
	"github.com/Bernotese/plumb/internal/wire"
)

// Guest backups (T-221): the bound Targets with their backup field, read
// from the head and the bindings, for the backup jobs (internal/backup
// may not read the head itself, ARCHITECTURE §2). Read-only.

// BackupTarget is one bound Target as the backup jobs need it.
type BackupTarget struct {
	Name string
	Ref  string // the binding ref (VMID)
	Gen  int64  // the binding generation (D-397, D-417)
	LXC  bool
	// Backup is the Target's backup field; nil: no Plumb backups.
	Backup *BackupField
}

// BackupField is the backup field of a Target (T-217 schema).
type BackupField struct {
	Schedule string // daily | weekly
	Weekday  string // mon … sun, with weekly
	Hour     int
	Minute   int
	Keep     int
}

// BackupTarget returns one bound Target of the head; store.ErrNotFound
// when the name is not a Target of the head with a binding.
func (e *Engine) BackupTarget(ctx context.Context, name string) (BackupTarget, error) {
	all, err := e.BackupTargets(ctx)
	if err != nil {
		return BackupTarget{}, err
	}
	for _, t := range all {
		if t.Name == name {
			return t, nil
		}
	}
	return BackupTarget{}, store.ErrNotFound
}

// BackupTargets returns every Target of the head that has a binding,
// sorted by name.
func (e *Engine) BackupTargets(ctx context.Context) ([]BackupTarget, error) {
	_, set, err := e.base(ctx)
	if err != nil {
		return nil, err
	}
	binds, err := e.st.Bindings(ctx, state.KindTarget)
	if err != nil {
		return nil, err
	}
	bound := map[string]store.Binding{}
	for _, b := range binds {
		bound[b.Name] = b
	}
	var out []BackupTarget
	for _, d := range set.Docs() {
		if d.Kind != state.KindTarget {
			continue
		}
		b, ok := bound[d.Name]
		if !ok {
			continue
		}
		t, err := backupTargetOf(d, b)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func backupTargetOf(d state.Doc, b store.Binding) (BackupTarget, error) {
	var spec struct {
		Type   string `json:"type"`
		Backup *struct {
			Schedule string `json:"schedule"`
			Weekday  string `json:"weekday"`
			Time     string `json:"time"`
			Keep     int    `json:"keep"`
		} `json:"backup"`
	}
	if err := json.Unmarshal(d.Spec, &spec); err != nil {
		return BackupTarget{}, fmt.Errorf("plan: Target %s: %w", d.Name, err)
	}
	t := BackupTarget{Name: d.Name, Ref: b.Ref, Gen: b.Gen, LXC: spec.Type == "lxc"}
	if spec.Backup != nil {
		hh, mm, ok := strings.Cut(spec.Backup.Time, ":")
		h, err1 := strconv.Atoi(hh)
		m, err2 := strconv.Atoi(mm)
		if !ok || errors.Join(err1, err2) != nil {
			return BackupTarget{}, fmt.Errorf("plan: Target %s: bad backup time %q", d.Name, spec.Backup.Time)
		}
		t.Backup = &BackupField{Schedule: spec.Backup.Schedule, Weekday: spec.Backup.Weekday, Hour: h, Minute: m, Keep: spec.Backup.Keep}
	}
	return t, nil
}

// AttrBackup is the Plan-only change of spec.backup of a Target (T-221):
// it changes no guest setting; Update never gets it.
const AttrBackup = "backup"

// backupText is the change value of a backup field ("daily 03:00 keep 7"),
// or nil without one.
func backupText(b map[string]any) any {
	if b == nil {
		return nil
	}
	s := fmt.Sprint(b["schedule"])
	if w, ok := b["weekday"].(string); ok && w != "" {
		s += " " + w
	}
	return fmt.Sprintf("%s %v keep %v", s, b["time"], b["keep"])
}

func keepOf(b map[string]any) int {
	if b == nil {
		return 0
	}
	n, _ := driver.IntValue(b["keep"])
	return n
}

// backupChange is the Plan-only change of spec.backup (D-408, D-417).
// Whenever the new field makes the next job delete backups, the change is
// destructive and its note says how many: the count comes from the jobs
// (BackupKeepDeletes), also when the field comes back after a removal (M1
// of #334: remove, then add again with keep 1, must not pass as read).
// Without a count (no jobs) a lower keep than before is the fallback.
// Removing the field deletes nothing. A new field gets the storage note
// (D-406).
func (e *Engine) backupChange(ctx context.Context, before, after *state.Rendered, target string) (*wire.Change, []string, error) {
	var old, nu map[string]any
	if before != nil {
		old = before.Backup
	}
	if after != nil {
		nu = after.Backup
	}
	from, to := backupText(old), backupText(nu)
	if from == to {
		return nil, nil, nil
	}
	c := &wire.Change{Attr: AttrBackup, From: from, To: to, Risk: wire.RiskRead}
	var notes []string
	switch {
	case nu == nil:
		notes = append(notes, fmt.Sprintf("Target %s: Plumb stops its backups; it deletes none. Its backups stay until a person deletes them.", target))
	case e.cfg.BackupKeepDeletes != nil:
		n, err := e.cfg.BackupKeepDeletes(ctx, target, keepOf(nu))
		if err != nil {
			return nil, nil, err
		}
		if n > 0 {
			c.Risk = wire.RiskDestructive
			notes = append(notes, fmt.Sprintf("Target %s: with keep %d the next backup job deletes %d backup(s).", target, keepOf(nu), n))
		}
	case old != nil && keepOf(nu) < keepOf(old): // no count: the fallback
		c.Risk = wire.RiskDestructive
		notes = append(notes, fmt.Sprintf("Target %s: keep %d → %d deletes the oldest backups above %d at the next backup job.", target, keepOf(old), keepOf(nu), keepOf(nu)))
	}
	if old == nil && nu != nil && e.cfg.BackupStorageNote != nil {
		note, err := e.cfg.BackupStorageNote(ctx)
		if err != nil {
			return nil, nil, err
		}
		if note != "" {
			notes = append(notes, fmt.Sprintf("Target %s: %s", target, note))
		}
	}
	return c, notes, nil
}
