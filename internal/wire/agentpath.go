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
		"/usr/local/sbin/plumb-agent-install",
	}
	// Unit search paths of systemd. A unit file, a drop-in directory or an
	// enable link of plumb-agent.service in any of them is the agent's.
	unitDirs = []string{
		"/etc/systemd/system",
		"/run/systemd/system",
		"/usr/local/lib/systemd/system",
		"/usr/lib/systemd/system",
		"/lib/systemd/system",
	}
)

const agentUnit = "plumb-agent.service"

// IsAgentPath reports whether p (absolute) is the agent's own path, or a
// path inside one of its directories. p is cleaned first.
func IsAgentPath(p string) bool {
	if !strings.HasPrefix(p, "/") {
		return false
	}
	p = path.Clean(p)
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
		switch {
		case parts[0] == agentUnit || parts[0] == agentUnit+".d":
			return true // the unit, or its drop-ins
		case len(parts) == 2 && strings.HasSuffix(parts[0], ".wants") && parts[1] == agentUnit:
			return true // the enable link
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
// attribute, or "" when there is none.
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
