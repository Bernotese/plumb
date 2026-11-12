package resource

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/Bernotese/plumb/internal/wire"
	"golang.org/x/sys/unix"
)

// sysctlNameRE is the agent's own check of the key (the schema has the
// same pattern). No "/" and no empty or ".." component: the name maps to a
// path under /proc/sys.
var sysctlNameRE = regexp.MustCompile(`^[a-z0-9_]+(\.[a-zA-Z0-9_-]+)+$`)

// maxSysctlName is the schema limit of the name.
const maxSysctlName = 255

// maxSysctlValue bounds a value; kernel values are short.
const maxSysctlValue = 4096

// sysctl is one kernel parameter (schema/sysctl.json), VM only (an LXC
// shares the host kernel, ARCHITECTURE §6). Plumb sets the running value
// and keeps it across reboots in its own file in /etc/sysctl.d.
type sysctl struct {
	base
	a     sysctlAttrs
	value string // a.Value with whitespace runs folded to one space
}

type sysctlAttrs struct {
	Name  string          `json:"name"`
	Value json.RawMessage `json:"value"` // a string or an integer (YAML "value: 1")
	State string          `json:"state"`
}

// sysctlValue returns the value as text. The schema allows a string or an
// integer; nothing else.
func sysctlValue(raw json.RawMessage) (string, error) {
	if len(raw) == 0 {
		return "", nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s, nil
	}
	var n json.Number
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&n); err == nil {
		if i, err := n.Int64(); err == nil {
			return strconv.FormatInt(i, 10), nil
		}
	}
	return "", errors.New("value must be a string or an integer")
}

func newSysctl(r wire.Resource, env Env) (Resource, error) {
	s := &sysctl{base: base{res: r, env: env}, a: sysctlAttrs{State: wire.StatePresent}}
	if err := decode(r, "sysctl", &s.a, func() string { return s.a.Name }); err != nil {
		return nil, err
	}
	if len(s.a.Name) > maxSysctlName || !sysctlNameRE.MatchString(s.a.Name) {
		return nil, fmt.Errorf("resource %s: bad sysctl name", r.ID)
	}
	v, err := sysctlValue(s.a.Value)
	if err != nil {
		return nil, fmt.Errorf("resource %s: %w", r.ID, err)
	}
	if len(v) > maxSysctlValue || strings.ContainsAny(v, "\n\r\x00") {
		return nil, fmt.Errorf("resource %s: value must be one line of at most %d bytes", r.ID, maxSysctlValue)
	}
	s.value = foldSpace(v)
	if s.a.State == wire.StatePresent && s.value == "" {
		return nil, fmt.Errorf("resource %s: value is empty", r.ID)
	}
	return s, nil
}

// foldSpace folds whitespace runs to one space: the kernel prints
// multi-value keys with tabs (net.ipv4.ip_local_port_range).
func foldSpace(s string) string { return strings.Join(strings.Fields(s), " ") }

// confPath is the file that keeps the value across reboots.
func (s *sysctl) confPath() string { return "/etc/sysctl.d/60-plumb-" + s.a.Name + ".conf" }

// procPath is the running value.
func (s *sysctl) procPath() string { return "/proc/sys/" + strings.ReplaceAll(s.a.Name, ".", "/") }

// content is the file Plumb writes.
func (s *sysctl) content() string {
	return "# Managed by Plumb (resource " + s.res.ID + "). Local changes are drift.\n" + s.a.Name + " = " + s.value + "\n"
}

// Observe reads the running value and Plumb's file. The state is the
// file: without it the value is not Plumb's. A key the kernel does not
// have is an error, not drift.
func (s *sysctl) Observe(_ context.Context) (State, error) {
	f, err := readManaged(s.env.Root, s.confPath())
	if err != nil {
		return nil, err
	}
	run, err := readManaged(s.env.Root, s.procPath())
	if err != nil {
		return nil, err
	}
	if !run.exists {
		return nil, fmt.Errorf("the kernel has no parameter %s", s.a.Name)
	}
	st := State{wire.AttrState: wire.StateAbsent, "value": foldSpace(run.content)}
	if f.exists {
		st[wire.AttrState] = wire.StatePresent
		st["file"] = f.content
	}
	return st, nil
}

// Diff: a different running value is disruptive (a kernel setting can cut
// the network). The file alone is reversible. "absent" removes the file
// and leaves the running value; nothing knows the value from before.
func (s *sysctl) Diff(observed State) []wire.Change {
	present := observed[wire.AttrState] == wire.StatePresent
	if s.a.State == wire.StateAbsent {
		if present {
			return s.finish([]wire.Change{change(wire.AttrState, wire.StatePresent, wire.StateAbsent, wire.RiskReversible)})
		}
		return nil
	}
	var out []wire.Change
	if !present {
		out = append(out, change(wire.AttrState, wire.StateAbsent, wire.StatePresent, wire.RiskReversible))
	} else if observed["file"] != s.content() {
		out = append(out, change("file", observed["file"], s.content(), wire.RiskReversible))
	}
	if observed["value"] != s.value {
		out = append(out, change("value", observed["value"], s.value, wire.RiskDisruptive))
	}
	return s.finish(out)
}

// Apply writes the file first, then the running value, so a failed write
// to /proc leaves a file that the next boot applies.
func (s *sysctl) Apply(_ context.Context, changes []wire.Change) error {
	if len(changes) == 0 {
		return nil
	}
	if s.a.State == wire.StateAbsent {
		return removeManaged(s.env.Root, s.confPath())
	}
	if has(changes, wire.AttrState) || has(changes, "file") {
		if err := writeManaged(s.env.Root, s.confPath(), []byte(s.content()), 0o644); err != nil {
			return err
		}
	}
	if has(changes, "value") {
		return s.writeProc()
	}
	return nil
}

// writeProc writes the value to /proc/sys without a shell and without
// sysctl(8), so no value is ever parsed as an option.
func (s *sysctl) writeProc() error {
	p := s.procPath()
	dfd, name, err := openParent(s.env.Root, p)
	if err != nil {
		if isNoParent(err) {
			return fmt.Errorf("the kernel has no parameter %s", s.a.Name)
		}
		return err
	}
	defer unix.Close(dfd)
	fd, _, err := openRegular(dfd, name, p, unix.O_WRONLY)
	if err != nil {
		return err
	}
	defer unix.Close(fd)
	if _, err := unix.Write(fd, []byte(s.value+"\n")); err != nil {
		return fmt.Errorf("set %s: %w", s.a.Name, err)
	}
	return nil
}

// Watch returns Plumb's file. The running value has no file event; the
// periodic check covers it (ARCHITECTURE §6).
func (s *sysctl) Watch() []string { return []string{s.path(s.confPath())} }
