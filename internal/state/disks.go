package state

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Bernotese/plumb/internal/wire"
)

// systemTrees are the top directories of a guest that a data disk must
// never mount over or under (D-423, review of #359): a mount over /etc
// hides the guest's config. A top directory that starts with "lib"
// (lib, lib32, lib64, libx32) counts too.
var systemTrees = map[string]bool{"bin": true, "boot": true, "dev": true, "etc": true, "proc": true,
	"root": true, "run": true, "sbin": true, "sys": true, "usr": true, "var": true}

// checkDisks checks guest.disks beyond the schema (T-237, D-423, D-436):
// a slot only once; path required for an lxc Target and refused for a
// vm; no "." or ".." part, no system tree, no agent path, and no path at
// or inside the path of another disk. The schema already refuses a path
// that is not absolute, "/", "//" and a trailing "/". The checks are
// lexical: the guest may make a part of the path a symlink (T-244 host
// fact, ARCHITECTURE §21).
func checkDisks(t *targetSpec, errf func(path, format string, args ...any) *Error) Errors {
	disks, _ := t.Guest["disks"].([]any)
	var errs Errors
	slots := map[int]int{}
	type placed struct {
		i int
		p string
	}
	var paths []placed
	for i, item := range disks {
		d, _ := item.(map[string]any)
		at := fmt.Sprintf("/spec/guest/disks/%d", i)
		// The document is decoded with UseNumber; the schema made it an
		// integer 1 … 3.
		if n, ok := d["slot"].(json.Number); ok {
			s, _ := n.Int64()
			if j, dup := slots[int(s)]; dup {
				errs = append(errs, errf(at+"/slot", "slot %d is also used by disk %d; each slot names one disk", s, j))
			} else {
				slots[int(s)] = i
			}
		}
		p, has := d["path"].(string)
		switch {
		case t.Type == string(wire.KindVM) && has:
			errs = append(errs, errf(at+"/path", "a VM data disk has no path: it is a raw block device (serial plumb-<slot>); format and mount it in the guest"))
			continue
		case t.Type == string(wire.KindLXC) && !has:
			errs = append(errs, errf(at, "a container data disk needs a path, for example /srv/data"))
			continue
		case !has:
			continue
		}
		if msg := diskPathProblem(p); msg != "" {
			errs = append(errs, errf(at+"/path", "%s", msg))
			continue
		}
		for _, q := range paths {
			if within(p, q.p) || within(q.p, p) {
				errs = append(errs, errf(at+"/path", "%s is at or inside the path %s of disk %d", p, q.p, q.i))
			}
		}
		paths = append(paths, placed{i, p})
	}
	return errs
}

// diskPathProblem returns why p cannot be the mount path of a data disk,
// or "".
func diskPathProblem(p string) string {
	if !strings.HasPrefix(p, "/") || p == "/" {
		return "the path must be absolute and not /"
	}
	parts := strings.Split(p[1:], "/")
	for _, part := range parts {
		if part == "" || part == "." || part == ".." {
			return fmt.Sprintf("the path %s has an empty, . or .. part", p)
		}
	}
	if top := parts[0]; systemTrees[top] || strings.HasPrefix(top, "lib") {
		return fmt.Sprintf("the path %s is in the system tree /%s; use for example /srv, /opt, /data, /mnt or /home", p, top)
	}
	if wire.IsAgentPath(p) {
		return fmt.Sprintf("the path %s is a path of the Plumb agent", p)
	}
	return ""
}

// within reports whether p is q or below q.
func within(p, q string) bool { return p == q || strings.HasPrefix(p, q+"/") }
