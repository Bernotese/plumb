package wire

import (
	"path"
	"strings"
)

// Paths of the agent itself in a guest (install/install.sh). A resource
// of a document must never write, link or remove them: a Block could
// otherwise forge the build mark of seal or overwrite the credential
// (D-207, review S1 of T-039). state.Validate refuses such a document,
// and the agent refuses the resource again (T-060).
//
// This is a guard against a plain mistake or a forged mark through the
// resource types, not a sandbox: a Block runs as root in the guest.
var (
	agentDirs = []string{
		"/etc/plumb-agent",     // agent.json, ca.pem
		"/var/lib/plumb-agent", // credential, desired list, marks
	}
	agentFiles = []string{
		"/usr/local/bin/plumb-agent",
		"/usr/local/bin/.plumb-agent.new",      // temp name of the first-boot script (T-076)
		"/usr/local/bin/plumb-agent.prev",      // the binary before the last update (T-091)
		"/usr/local/bin/.plumb-agent.update",   // download of an update (T-091)
		"/usr/local/bin/.plumb-agent.prev-new", // temp name of the next .prev (T-091)
		"/usr/local/sbin/plumb-agent-install",
	}
	// Unit search paths of systemd (systemd.unit(5), system manager). A
	// unit file, a drop-in directory or an enable link of
	// plumb-agent.service in any of them is the agent's.
	unitDirs = []string{
		"/etc/systemd/system.control",
		"/run/systemd/system.control",
		"/run/systemd/transient",
		"/run/systemd/generator.early",
		"/etc/systemd/system",
		"/etc/systemd/system.attached",
		"/run/systemd/system",
		"/run/systemd/system.attached",
		"/run/systemd/generator",
		"/usr/local/lib/systemd/system",
		"/usr/lib/systemd/system",
		"/run/systemd/generator.late",
		"/lib/systemd/system",
	}
	// pathAliases are directory links that Debian and Ubuntu always have
	// (/var/run → /run; merged /usr). A path is checked in its /run or
	// /usr form.
	pathAliases = [][2]string{
		{"/var/run", "/run"},
		{"/bin", "/usr/bin"},
		{"/sbin", "/usr/sbin"},
		{"/lib", "/usr/lib"},
	}
)

// The agent's units: the agent, the first-boot installer of an LXC
// template (T-076), the update guard with its timer (T-091, D-271), and
// the firewall boot unit with the rollback timer of a safe apply (T-116,
// D-300). The firewall boot file is in /etc/plumb-agent.
var agentUnits = []string{"plumb-agent.service", "plumb-agent-install.service",
	"plumb-agent-guard.service", "plumb-agent-guard.timer",
	"plumb-firewall.service", "plumb-firewall-rollback.service", "plumb-firewall-rollback.timer"}

// IsAgentPath reports whether p (absolute) is the agent's own path, or a
// path inside one of its directories. p is cleaned first.
func IsAgentPath(p string) bool {
	if !strings.HasPrefix(p, "/") {
		return false
	}
	p = path.Clean(p)
	for _, a := range pathAliases {
		if p == a[0] || strings.HasPrefix(p, a[0]+"/") {
			p = a[1] + p[len(a[0]):]
		}
	}
	for _, d := range agentDirs {
		if p == d || strings.HasPrefix(p, d+"/") {
			return true
		}
	}
	for _, f := range agentFiles {
		if p == f {
			return true
		}
	}
	for _, d := range unitDirs {
		rest, ok := strings.CutPrefix(p, d+"/")
		if !ok {
			continue
		}
		parts := strings.Split(rest, "/")
		for _, u := range agentUnits {
			switch {
			case parts[0] == u || parts[0] == u+".d":
				return true // the unit, or its drop-ins
			case len(parts) == 2 && strings.HasSuffix(parts[0], ".wants") && parts[1] == u:
				return true // the enable link
			}
		}
	}
	return false
}

// AgentPathAttrs lists, per resource type, the attributes that name a path
// in the guest. A symlink target is relative to the directory of the link.
var AgentPathAttrs = map[string][]string{
	"file":      {"path"},
	"directory": {"path"},
	"symlink":   {"path", "target"},
	"user":      {"home"},
}

// AgentPathOf returns the first path of r that IsAgentPath, with its
// attribute, or "" when there is none. It sees only r: use AgentConflicts
// for a resource list, which also follows the symlinks of the list.
func AgentPathOf(r Resource) (attr, p string) {
	for _, a := range AgentPathAttrs[r.Type] {
		v, ok := r.Attrs[a].(string)
		if !ok || v == "" {
			continue
		}
		if r.Type == "symlink" && a == "target" && !strings.HasPrefix(v, "/") {
			link, _ := r.Attrs["path"].(string)
			v = path.Join(path.Dir(link), v)
		}
		if IsAgentPath(v) {
			return a, v
		}
	}
	return "", ""
}

// AgentConflict is a resource that would change the agent itself.
type AgentConflict struct {
	ID    string // resource id
	Attr  string
	Value string // the path as resolved, or the unit name
}

// agentUnitPrefix starts the name of every unit of the agent, also of
// units that later versions add (plumb-agent-guard.timer, T-091).
const agentUnitPrefix = "plumb-agent"

// IsAgentUnit reports whether a service or unit name belongs to the agent
// (D-274): every name that starts with "plumb-agent", with any suffix
// ("plumb-agent", "plumb-agent.timer", "plumb-agent-guard.service",
// "plumb-agent@x.service"). A prefix rule, not a list: a timer, path or
// socket unit can start the agent's service, and a list misses the units
// that a newer agent adds.
//
// The units of the firewall boot file and of its rollback timer (T-116,
// D-300) start with "plumb-firewall": a Block that masks or replaces them
// could keep unconfirmed rules or load other rules at boot.
func IsAgentUnit(name string) bool {
	return strings.HasPrefix(name, agentUnitPrefix) || strings.HasPrefix(name, firewallUnitPrefix)
}

// firewallUnitPrefix starts the units of the agent's firewall.
const firewallUnitPrefix = "plumb-firewall"

// AgentConflicts lists the resources of one target that would change the
// agent (D-207): a path of the agent, also through a symlink that the same
// list makes (/opt/x → /var/lib, then file /opt/x/plumb-agent/credential),
// and a service or unit resource on the agent's unit (it could stop, mask
// or replace the agent). Links that already exist in the guest are not known here: the
// agent resolves the real path before it writes (T-060).
func AgentConflicts(rs []Resource) []AgentConflict {
	// Symlinks of the list: link path → absolute target.
	links := map[string]string{}
	for _, r := range rs {
		if r.Type != "symlink" {
			continue
		}
		p, _ := r.Attrs["path"].(string)
		t, _ := r.Attrs["target"].(string)
		if p == "" || t == "" || !strings.HasPrefix(p, "/") {
			continue
		}
		if !strings.HasPrefix(t, "/") {
			t = path.Join(path.Dir(p), t)
		}
		links[path.Clean(p)] = path.Clean(t)
	}
	var out []AgentConflict
	for _, r := range rs {
		// A service or a unit file (T-093) of an agent unit: it could stop,
		// mask or replace the agent or its update guard (review of #190).
		if r.Type == "service" || r.Type == "unit" {
			if n, _ := r.Attrs["name"].(string); IsAgentUnit(n) {
				out = append(out, AgentConflict{ID: r.ID, Attr: "name", Value: n})
			}
			continue
		}
		for _, a := range AgentPathAttrs[r.Type] {
			v, ok := r.Attrs[a].(string)
			if !ok || v == "" {
				continue
			}
			if r.Type == "symlink" && a == "target" && !strings.HasPrefix(v, "/") {
				link, _ := r.Attrs["path"].(string)
				v = path.Join(path.Dir(link), v)
			}
			if !strings.HasPrefix(v, "/") {
				continue
			}
			for _, p := range resolvedForms(path.Clean(v), links) {
				if IsAgentPath(p) {
					out = append(out, AgentConflict{ID: r.ID, Attr: a, Value: p})
					break
				}
			}
		}
	}
	return out
}

// resolvedForms returns p and every form of p that the links make, by
// replacing a link prefix with its target, a bounded number of times.
func resolvedForms(p string, links map[string]string) []string {
	forms := []string{p}
	seen := map[string]bool{p: true}
	for i := 0; i < len(forms) && len(forms) < 64; i++ {
		cur := forms[i]
		for l, t := range links {
			if cur != l && !strings.HasPrefix(cur, l+"/") {
				continue
			}
			next := path.Clean(t + cur[len(l):])
			if !seen[next] {
				seen[next] = true
				forms = append(forms, next)
			}
		}
	}
	return forms
}
