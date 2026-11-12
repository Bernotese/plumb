package agent

import (
	"errors"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/Bernotese/plumb/internal/wire"
)

// ownPathRefusal returns why the agent refuses a resource, or "" (D-207,
// reviews S1 of T-039 and T-059). A resource may not write, link or remove
// a path of the agent itself (wire.IsAgentPath, the configured state dir)
// and a service or unit resource may not touch the agent's units. The save check
// (wire.AgentConflicts) knows only the links of the same resource list, so
// the agent resolves every symbolic link that exists in the guest and
// checks the real path too.
//
// This is a guard against a forged build mark and an overwritten
// credential through the resource types, not a sandbox: a Block runs as
// root in the guest.
func (a *Agent) ownPathRefusal(r wire.Resource) string {
	if r.Type == "service" || r.Type == "unit" {
		// wire knows every unit of the agent: also the update guard (D-271).
		if len(wire.AgentConflicts([]wire.Resource{r})) > 0 {
			return "the agent does not manage its own unit"
		}
		return ""
	}
	for _, attr := range wire.AgentPathAttrs[r.Type] {
		v, ok := r.Attrs[attr].(string)
		if !ok || v == "" {
			continue
		}
		if r.Type == "symlink" && attr == "target" && !strings.HasPrefix(v, "/") {
			// A relative target is relative to the real directory of the link.
			link, _ := r.Attrs["path"].(string)
			if !strings.HasPrefix(link, "/") {
				continue
			}
			dir, err := a.realPath(path.Dir(link))
			if err != nil {
				return "cannot resolve the path of " + attr + ": " + err.Error()
			}
			v = path.Join(dir, v)
		}
		if !strings.HasPrefix(v, "/") {
			continue
		}
		if a.isOwnPath(v) {
			return attr + " is a path of the Plumb agent; a resource must not change the agent"
		}
		// The path with every link resolved, and the path with only its
		// parent resolved (the last component may be a link that the
		// resource replaces). Both must stay outside the agent.
		clean := path.Clean(v)
		forms := []string{}
		if p, err := a.realPath(clean); err == nil {
			forms = append(forms, p)
		} else {
			return "cannot resolve the path of " + attr + ": " + err.Error()
		}
		if clean != "/" {
			dir, err := a.realPath(path.Dir(clean))
			if err != nil {
				return "cannot resolve the path of " + attr + ": " + err.Error()
			}
			forms = append(forms, path.Join(dir, path.Base(clean)))
		}
		for _, p := range forms {
			if a.isOwnPath(p) {
				return attr + " resolves to a path of the Plumb agent through a symbolic link; a resource must not change the agent"
			}
		}
	}
	return ""
}

// isOwnPath reports whether the guest path p is the agent's, also by the
// configured state dir (Config.StateDir is a host path; with Root "/"
// both are the same).
func (a *Agent) isOwnPath(p string) bool {
	if wire.IsAgentPath(p) {
		return true
	}
	host := filepath.Join(a.opt.Root, p)
	sd := a.cfg.StateDir
	return host == sd || strings.HasPrefix(host, sd+"/")
}

// maxLinks bounds the links that one resolve follows (like Linux, 40).
const maxLinks = 40

var errLinkLoop = errors.New("too many symbolic links")

// realPath resolves the guest path p (absolute) inside the guest root:
// every existing symbolic link is followed, an absolute link target is
// relative to the guest root, ".." never leaves it. The part after the
// first missing component stays as it is.
func (a *Agent) realPath(p string) (string, error) {
	rest := strings.Split(strings.TrimPrefix(path.Clean(p), "/"), "/")
	var done []string // resolved components
	links := 0
	for len(rest) > 0 {
		c := rest[0]
		rest = rest[1:]
		switch c {
		case "", ".":
			continue
		case "..":
			if len(done) > 0 {
				done = done[:len(done)-1]
			}
			continue
		}
		cur := "/" + path.Join(append(done, c)...)
		fi, err := os.Lstat(filepath.Join(a.opt.Root, cur))
		if errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR) {
			// Nothing more exists: the rest cannot hold a link.
			return path.Clean(cur + "/" + strings.Join(rest, "/")), nil
		}
		if err != nil {
			return "", err
		}
		if fi.Mode()&fs.ModeSymlink == 0 {
			done = append(done, c)
			continue
		}
		if links++; links > maxLinks {
			return "", errLinkLoop
		}
		t, err := os.Readlink(filepath.Join(a.opt.Root, cur))
		if err != nil {
			return "", err
		}
		if strings.HasPrefix(t, "/") {
			done = nil
		}
		rest = append(strings.Split(t, "/"), rest...)
	}
	return "/" + path.Join(done...), nil
}
