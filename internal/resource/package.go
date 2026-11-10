package resource

import (
	"context"
	"fmt"
	"strings"

	"github.com/Bernotese/plumb/internal/wire"
)

// Strictness values of the package resource.
const (
	StrictPresent = "present" // any installed version is fine
	StrictExact   = "exact"   // another version is drift
)

// aptOptions are passed to every apt-get call. dpkg waits for a lock held by
// another process (for example unattended-upgrades) and keeps changed
// configuration files.
var aptOptions = []string{
	"-q", "-y",
	"-o", "DPkg::Lock::Timeout=300",
	"-o", "Dpkg::Options::=--force-confdef",
	"-o", "Dpkg::Options::=--force-confold",
}

// pkg is an apt package (schema/package.json).
type pkg struct {
	base
	a pkgAttrs
}

type pkgAttrs struct {
	Name       string `json:"name"`
	Version    string `json:"version"`
	Strictness string `json:"strictness"`
	State      string `json:"state"`
}

func newPackage(r wire.Resource, env Env) (Resource, error) {
	p := &pkg{base: base{res: r, env: env}, a: pkgAttrs{Strictness: StrictPresent, State: wire.StatePresent}}
	if err := decode(r, "package", &p.a, func() string { return p.a.Name }); err != nil {
		return nil, err
	}
	// "apt-get install foo-" removes foo; the schema forbids it, the agent
	// does not trust the wire.
	if strings.HasSuffix(p.a.Name, "-") || strings.HasPrefix(p.a.Name, "-") {
		return nil, fmt.Errorf("resource %s: bad package name", r.ID)
	}
	if p.a.Strictness == StrictExact && p.a.Version == "" {
		return nil, fmt.Errorf("resource %s: strictness exact needs a version", r.ID)
	}
	return p, nil
}

// Observe asks dpkg for the installed version. A package that is not
// installed (unknown, or only its config files are left) is absent.
func (p *pkg) Observe(ctx context.Context) (State, error) {
	p.env.PackageLock.Lock()
	defer p.env.PackageLock.Unlock()
	out, err := p.run(ctx, "dpkg-query", "-W", "-f=${db:Status-Abbrev}|${Version}\n", "--", p.a.Name)
	if exitCode(err) == 1 {
		return absent(), nil
	}
	if err != nil {
		return nil, err
	}
	abbrev, version, ok := strings.Cut(strings.TrimSpace(string(out)), "|")
	if !ok {
		return nil, fmt.Errorf("dpkg-query: unexpected output %q", clip(string(out), 200))
	}
	if len(abbrev) < 2 || abbrev[1] != 'i' {
		return absent(), nil
	}
	return State{wire.AttrState: wire.StatePresent, "version": version}, nil
}

// Diff applies the strictness: with "present" any version is fine, with
// "exact" another version is a change. Install is reversible, a version
// change is disruptive (maintainer scripts restart services), removal is
// destructive (apt may remove packages that depend on it).
func (p *pkg) Diff(observed State) []wire.Change {
	present := observed[wire.AttrState] == wire.StatePresent
	if p.a.State == wire.StateAbsent {
		if present {
			return p.finish([]wire.Change{change(wire.AttrState, wire.StatePresent, wire.StateAbsent, wire.RiskDestructive)})
		}
		return nil
	}
	exact := p.a.Strictness == StrictExact
	if !present {
		out := []wire.Change{change(wire.AttrState, wire.StateAbsent, wire.StatePresent, wire.RiskReversible)}
		if exact {
			out = append(out, change("version", nil, p.a.Version, wire.RiskReversible))
		}
		return p.finish(out)
	}
	if exact && observed["version"] != p.a.Version {
		return p.finish([]wire.Change{change("version", observed["version"], p.a.Version, wire.RiskDisruptive)})
	}
	return nil
}

// Apply installs (with the exact version if asked) or removes the package.
// When an install fails, it runs "apt-get update" once and tries again.
func (p *pkg) Apply(ctx context.Context, changes []wire.Change) error {
	if len(changes) == 0 {
		return nil
	}
	p.env.PackageLock.Lock()
	defer p.env.PackageLock.Unlock()
	if p.a.State == wire.StateAbsent {
		_, err := p.apt(ctx, "remove", "--", p.a.Name)
		return err
	}
	spec := p.a.Name
	if p.a.Strictness == StrictExact {
		spec += "=" + p.a.Version
	}
	install := func() error {
		_, err := p.apt(ctx, "install", "--no-install-recommends", "--allow-downgrades", "--", spec)
		return err
	}
	if err := install(); err == nil {
		return nil
	}
	if _, err := p.apt(ctx, "update"); err != nil {
		return err
	}
	return install()
}

func (p *pkg) apt(ctx context.Context, args ...string) ([]byte, error) {
	return p.run(ctx, "apt-get", append(append([]string{}, aptOptions...), args...)...)
}

// Watch returns the dpkg status file: every install and removal writes it.
func (p *pkg) Watch() []string { return []string{p.path("/var/lib/dpkg/status")} }
