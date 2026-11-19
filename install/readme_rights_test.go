package install

// T-262 (release plan C6): the rights table of the README is the list
// that the installer sets. A dry run with an extra bridge, an extra VLAN
// tag and a guest backup storage prints every role (with its privileges)
// and every ACL line; this test compares them with the README table, in
// both directions.

import (
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
)

type rightsRow struct {
	role, path, privs, inherited string
}

func (r rightsRow) String() string {
	return r.role + " " + r.path + " [" + r.privs + "] inherited=" + r.inherited
}

func readmeRights(t *testing.T) map[string]bool {
	t.Helper()
	b, err := os.ReadFile("../README.md")
	if err != nil {
		t.Fatal(err)
	}
	rows := map[string]bool{}
	in := false
	tick := regexp.MustCompile("`([^`]*)`")
	header := regexp.MustCompile(`^\|\s*Role\s*\|\s*Path\s*\|\s*Privileges\s*\|\s*Inherited\s*\|\s*Why\s*\|\s*$`)
	for _, line := range strings.Split(string(b), "\n") {
		if header.MatchString(line) {
			in = true
			continue
		}
		if !in {
			continue
		}
		if !strings.HasPrefix(line, "|") {
			break
		}
		cells := strings.Split(strings.Trim(line, "|"), "|")
		if len(cells) != 5 || strings.HasPrefix(strings.TrimSpace(cells[0]), "---") {
			continue
		}
		role := tick.FindStringSubmatch(cells[0])
		path := tick.FindStringSubmatch(cells[1])
		if role == nil || path == nil {
			t.Fatalf("README rights row without a role or path: %s", line)
		}
		var privs []string
		for _, m := range tick.FindAllStringSubmatch(cells[2], -1) {
			privs = append(privs, m[1])
		}
		sort.Strings(privs)
		rows[rightsRow{role[1], path[1], strings.Join(privs, ","), strings.TrimSpace(cells[3])}.String()] = true
	}
	if len(rows) == 0 {
		t.Fatal("no rights table in README.md")
	}
	return rows
}

func TestReadmeRightsTableIsTheInstallersList(t *testing.T) {
	h := newHost(t)
	h.addStorage("plumb-backup", "dir", "backup", "")
	code, out, errOut := h.run(nil, append([]string{"--dry-run", "--plumb-binary", h.plumb}, slice7Flags...)...)
	if code != 0 {
		t.Fatalf("dry run: %d\n%s\n%s", code, out, errOut)
	}
	privs := map[string]string{}
	for _, m := range regexp.MustCompile(`(?m)^\+ pveum role add (\S+) --privs (\S+)$`).FindAllStringSubmatch(out, -1) {
		p := strings.Split(strings.ReplaceAll(m[2], `\,`, ","), ",")
		sort.Strings(p)
		privs[m[1]] = strings.Join(p, ",")
	}
	// The run's own names, as the README writes them.
	place := strings.NewReplacer(
		"/storage/local-lvm", "/storage/<disk storage>",
		"/storage/plumb-backup", "/storage/<guest backup storage>",
		"/sdn/zones/localnetwork/vmbr0", "/sdn/zones/localnetwork/<bridge>",
		"/sdn/zones/localnetwork/vmbr9/20", "/sdn/zones/localnetwork/<extra bridge>/<tag>",
		"/sdn/zones/localnetwork/vmbr9", "/sdn/zones/localnetwork/<extra bridge>",
	)
	node := regexp.MustCompile(`^/nodes/[^/]+$`)
	installer := map[string]bool{}
	users, tokens := map[string]bool{}, map[string]bool{}
	for _, m := range regexp.MustCompile(`(?m)^\+ pveum acl modify (\S+) --(users|tokens) (\S+) --roles (\S+) --propagate ([01])$`).FindAllStringSubmatch(out, -1) {
		path := place.Replace(m[1])
		if node.MatchString(path) {
			path = "/nodes/<node>"
		}
		inh := map[string]string{"1": "yes", "0": "no"}[m[5]]
		if _, ok := privs[m[4]]; !ok {
			t.Errorf("an ACL line names role %s, which the run does not make", m[4])
		}
		row := rightsRow{m[4], path, privs[m[4]], inh}.String()
		installer[row] = true
		if m[2] == "users" {
			users[row] = true
		} else {
			tokens[row] = true
		}
	}
	if len(installer) == 0 {
		t.Fatalf("no ACL line in the dry run:\n%s", out)
	}
	for row := range users {
		if !tokens[row] {
			t.Errorf("the user has a line that its token has not: %s", row)
		}
	}
	readme := readmeRights(t)
	for row := range installer {
		if !readme[row] {
			t.Errorf("the installer sets a right that the README table does not show: %s", row)
		}
	}
	for row := range readme {
		if !installer[row] {
			t.Errorf("the README table shows a right that the installer does not set: %s", row)
		}
	}
}
