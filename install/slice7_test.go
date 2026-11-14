package install

// T-218 (slice 7, D-401, D-405, D-406, review M1/M2/L2 of #321): the
// extra bridges and VLAN tags, and the guest backup storage.

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// slice7Flags are the flags of the host acceptance C9.1.
var slice7Flags = []string{"--extra-vlan", "vmbr9:20", "--extra-bridge", "vmbr9", "--guest-backup-storage", "plumb-backup", "--guest-backup-clear"}

// addStorage makes a storage in the fake: type, content and more config
// fields (JSON members, for example `"encryption-key":"aa:bb"`).
func (h *host) addStorage(id, typ, content, extra string) {
	h.t.Helper()
	files := map[string]string{"storage." + id: "", "type." + id: typ + "\n", "content." + id: content + "\n", "path." + id: "/srv/" + id + "\n"}
	if extra != "" {
		files["extra."+id] = extra
	}
	for name, c := range files {
		if err := os.WriteFile(filepath.Join(h.state, name), []byte(c), 0o644); err != nil {
			h.t.Fatal(err)
		}
	}
}

// pveumLines are the "+ pveum" lines of a dry run.
func pveumLines(out string) []string {
	var l []string
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "+ pveum ") {
			l = append(l, line)
		}
	}
	return l
}

// The dry run prints each new line in the rights list and each new role and
// ACL command, with exactly the rights of D-379 / D-380; it changes nothing.
func TestSlice7DryRun(t *testing.T) {
	t.Parallel()
	h := newHost(t)
	h.addStorage("plumb-backup", "dir", "backup", "")
	code, out, errOut := h.run(nil, append([]string{"--dry-run", "--plumb-binary", h.plumb}, slice7Flags...)...)
	if code != 0 {
		t.Fatalf("dry run: %d\n%s\n%s", code, out, errOut)
	}
	if c := h.changed(); len(c) != 0 {
		t.Errorf("dry run changed the host: %v", c)
	}
	rights := out[:strings.Index(out, "+ ")]
	var granted []string
	for _, line := range strings.Split(rights, "\n") {
		if strings.HasPrefix(line, "  /") {
			granted = append(granted, strings.Join(strings.Fields(line), " "))
		}
	}
	want := []string{
		"/pool/plumb PlumbGuests VM.Allocate VM.Audit VM.Clone VM.Config.CPU VM.Config.Cloudinit VM.Config.Disk VM.Config.HWType VM.Config.Memory VM.Config.Network VM.Config.Options VM.PowerMgmt Pool.Audit",
		"/storage/local-lvm PlumbDisk Datastore.AllocateSpace Datastore.Audit",
		"/storage/plumb-images PlumbImages Datastore.AllocateTemplate Datastore.Audit",
		"/storage/plumb-snippets PlumbSnippets Datastore.AllocateSpace",
		"/sdn/zones/localnetwork/vmbr0 PlumbBridge SDN.Use",
		"/nodes/pve1 PlumbNode Sys.AccessNetwork",
		"/ PlumbAudit Sys.Audit",
		"/sdn/zones/localnetwork/vmbr9 PlumbBridge SDN.Use",
		"/sdn/zones/localnetwork/vmbr9/20 PlumbBridge SDN.Use",
		"/pool/plumb PlumbGuestBackupVM VM.Backup",
		"/storage/plumb-backup PlumbGuestBackup Datastore.AllocateSpace Datastore.Audit",
	}
	if strings.Join(granted, "\n") != strings.Join(want, "\n") {
		t.Errorf("rights:\n%s\nwant:\n%s", strings.Join(granted, "\n"), strings.Join(want, "\n"))
	}
	for _, bad := range []string{"Datastore.Allocate ", "Sys.Modify", "Permissions.Modify", "VM.Snapshot"} {
		if strings.Contains(strings.Join(granted, "\n")+" ", bad) {
			t.Errorf("forbidden right %s", bad)
		}
	}
	for _, text := range []string{"line on a bridge does not give its tags (D-401)",
		"the token itself can back up, restore over (overwrite) and delete the backups",
		"WARNING: storage plumb-backup (type dir) has no client-side encryption, and --guest-backup-clear is set"} {
		if !strings.Contains(out, text) {
			t.Errorf("dry run does not say %q", text)
		}
	}
	pveum := strings.Join(pveumLines(out), "\n")
	for _, w := range []string{
		"+ pveum role add PlumbGuestBackupVM --privs VM.Backup",
		`+ pveum role add PlumbGuestBackup --privs Datastore.AllocateSpace\,Datastore.Audit`,
	} {
		if !strings.Contains(pveum, w) {
			t.Errorf("dry run does not print %q", w)
		}
	}
	for _, ug := range []string{"--users plumb@pve", `--tokens plumb@pve\!plumb`} {
		for _, w := range []string{
			"/sdn/zones/localnetwork/vmbr9 " + ug + " --roles PlumbBridge --propagate 0",
			"/sdn/zones/localnetwork/vmbr9/20 " + ug + " --roles PlumbBridge --propagate 0",
			"/pool/plumb " + ug + " --roles PlumbGuestBackupVM --propagate 1",
			"/storage/plumb-backup " + ug + " --roles PlumbGuestBackup --propagate 0",
		} {
			if !strings.Contains(pveum, "+ pveum acl modify "+w) {
				t.Errorf("dry run does not print the ACL %q", w)
			}
		}
	}
	// Never an inherited line on an SDN path (D-401).
	if regexp.MustCompile(`acl modify /sdn/\S+ .*--propagate 1`).MatchString(pveum) {
		t.Errorf("propagate 1 on a bridge:\n%s", pveum)
	}
	// The env lines of a new install (env file in the dry run is not printed).
	if strings.Contains(out, "PLUMB_PVE_SECRET") {
		t.Error("the dry run printed the env file")
	}
}

// A repair run with the flags adds only the new roles and lines, and the
// env lines; a repair run without them removes exactly these lines and
// clears the env lines; --remove removes the roles.
func TestSlice7RepairAddsAndRemoves(t *testing.T) {
	t.Parallel()
	h := newHost(t)
	root := h.installed()
	h.addStorage("plumb-backup", "dir", "backup", "")
	acl := filepath.Join(h.state, "acl")
	before, _ := os.ReadFile(acl)

	if code, out, errOut := h.run(root, append([]string{"--yes", "--plumb-binary", h.plumb}, slice7Flags...)...); code != 0 {
		t.Fatalf("repair with flags: %d\n%s\n%s", code, out, errOut)
	}
	var pveum []string
	for _, c := range h.changed() {
		if strings.HasPrefix(c, "pveum ") {
			pveum = append(pveum, c)
		}
	}
	wantAdd := []string{"pveum role add PlumbGuestBackupVM --privs VM.Backup", "pveum role add PlumbGuestBackup --privs Datastore.AllocateSpace,Datastore.Audit"}
	newLines := [][3]string{ // path, role, propagate
		{"/sdn/zones/localnetwork/vmbr9", "PlumbBridge", "0"},
		{"/sdn/zones/localnetwork/vmbr9/20", "PlumbBridge", "0"},
		{"/pool/plumb", "PlumbGuestBackupVM", "1"},
		{"/storage/plumb-backup", "PlumbGuestBackup", "0"},
	}
	var added []string // acl file lines: path ugid role type propagate
	for _, l := range newLines {
		wantAdd = append(wantAdd, "pveum acl modify "+l[0]+" --users plumb@pve --roles "+l[1]+" --propagate "+l[2],
			"pveum acl modify "+l[0]+" --tokens plumb@pve!plumb --roles "+l[1]+" --propagate "+l[2])
		added = append(added, l[0]+" plumb@pve "+l[1]+" user "+l[2], l[0]+" plumb@pve!plumb "+l[1]+" token "+l[2])
	}
	if strings.Join(pveum, "\n") != strings.Join(wantAdd, "\n") {
		t.Errorf("repair pveum changes:\n%s\nwant:\n%s", strings.Join(pveum, "\n"), strings.Join(wantAdd, "\n"))
	}
	now, _ := os.ReadFile(acl)
	if got, want := sortedLines(string(now)), sortedLines(string(before)+strings.Join(added, "\n")+"\n"); !slices.Equal(got, want) {
		t.Errorf("ACLs after the repair:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	envCall := envLinesCall(t, h.calls())
	for _, w := range []string{"echo 'PLUMB_PVE_EXTRA_NETS=vmbr9,vmbr9/20'", "echo 'PLUMB_PVE_GUEST_BACKUP_STORAGE=plumb-backup'", "echo 'PLUMB_PVE_GUEST_BACKUP_CLEAR=1'"} {
		if !strings.Contains(envCall, w) {
			t.Errorf("env update without %q: %s", w, envCall)
		}
	}

	// The fake LXC runs no sh -c: put the lines in as the update did.
	envFile := filepath.Join(h.state, "push", "_etc_plumb_plumb.env")
	envText, _ := os.ReadFile(envFile)
	envText = []byte(strings.NewReplacer("PLUMB_PVE_EXTRA_NETS=\n", "PLUMB_PVE_EXTRA_NETS=vmbr9,vmbr9/20\n",
		"PLUMB_PVE_GUEST_BACKUP_STORAGE=\n", "PLUMB_PVE_GUEST_BACKUP_STORAGE=plumb-backup\n",
		"PLUMB_PVE_GUEST_BACKUP_CLEAR=0\n", "PLUMB_PVE_GUEST_BACKUP_CLEAR=1\n").Replace(string(envText)))
	os.WriteFile(envFile, envText, 0o600)

	// Without the flags: the dry run names the lines that go, the real run
	// removes exactly them (user and token) and nothing else.
	h.resetCalls()
	code, out, errOut := h.run(root, "--dry-run", "--plumb-binary", h.plumb)
	if code != 0 {
		t.Fatalf("dry run without flags: %d %s %s", code, out, errOut)
	}
	for _, l := range added {
		f := strings.Fields(l)
		if !strings.Contains(out, "  "+strings.Join(f[:4], " ")) {
			t.Errorf("dry run does not name the old line %q:\n%s", l, out)
		}
	}
	h.resetCalls()
	if code, out, errOut := h.run(root, "--yes", "--plumb-binary", h.plumb); code != 0 {
		t.Fatalf("repair without flags: %d\n%s\n%s", code, out, errOut)
	}
	pveum = nil
	for _, c := range h.changed() {
		if strings.HasPrefix(c, "pveum ") {
			pveum = append(pveum, c)
		}
	}
	if len(pveum) != 2*len(newLines) {
		t.Errorf("repair without flags changed %d things, want %d deletes:\n%s", len(pveum), 2*len(newLines), strings.Join(pveum, "\n"))
	}
	for _, c := range pveum {
		if !strings.HasPrefix(c, "pveum acl delete ") {
			t.Errorf("repair without flags: %s", c)
		}
	}
	if after, _ := os.ReadFile(acl); !slices.Equal(sortedLines(string(after)), sortedLines(string(before))) {
		t.Errorf("ACLs after the repair without flags:\n%s\nwant:\n%s", after, before)
	}
	envCall = envLinesCall(t, h.calls())
	for _, w := range []string{"echo 'PLUMB_PVE_EXTRA_NETS='", "echo 'PLUMB_PVE_GUEST_BACKUP_STORAGE='", "echo 'PLUMB_PVE_GUEST_BACKUP_CLEAR=0'"} {
		if !strings.Contains(envCall, w) {
			t.Errorf("env update without %q: %s", w, envCall)
		}
	}

	// --remove deletes the roles too (they still exist).
	h.resetCalls()
	if code, out, errOut := h.run(root, "--remove", "--yes"); code != 0 {
		t.Fatalf("remove: %d %s %s", code, out, errOut)
	}
	for _, r := range []string{"PlumbGuestBackupVM", "PlumbGuestBackup"} {
		if _, err := os.Stat(filepath.Join(h.state, "role."+r)); !os.IsNotExist(err) {
			t.Errorf("role %s stays after --remove", r)
		}
	}
	noTokenLines(t, acl)
}

// noTokenLines: no ACL line of the token stays (the fake keeps the lines
// of a deleted user; Proxmox deletes them with the user).
func noTokenLines(t *testing.T, acl string) {
	t.Helper()
	b, _ := os.ReadFile(acl)
	if strings.Contains(string(b), " token ") {
		t.Errorf("token ACLs after --remove:\n%s", b)
	}
}

// --remove after a run with the flags removes every new line with the
// token and the user.
func TestSlice7Remove(t *testing.T) {
	t.Parallel()
	h := newHost(t)
	h.addStorage("plumb-backup", "dir", "backup", "")
	root := []string{"PLUMB_INSTALL_ASSUME_ROOT=1", "TMPDIR=" + h.tmpDir()}
	if code, out, errOut := h.run(root, append([]string{"--yes", "--plumb-binary", h.plumb}, slice7Flags...)...); code != 0 {
		t.Fatalf("install: %d\n%s\n%s", code, out, errOut)
	}
	env, _ := os.ReadFile(filepath.Join(h.state, "push", "_etc_plumb_plumb.env"))
	for _, w := range []string{"\nPLUMB_PVE_EXTRA_NETS=vmbr9,vmbr9/20\n", "\nPLUMB_PVE_GUEST_BACKUP_STORAGE=plumb-backup\n", "\nPLUMB_PVE_GUEST_BACKUP_CLEAR=1\n"} {
		if !strings.Contains(string(env), w) {
			t.Errorf("env file without %q:\n%s", w, env)
		}
	}
	if code, out, errOut := h.run(root, "--remove", "--yes"); code != 0 {
		t.Fatalf("remove: %d %s %s", code, out, errOut)
	}
	noTokenLines(t, filepath.Join(h.state, "acl"))
	if calls := strings.Join(h.calls(), "\n"); !strings.Contains(calls, "pveum user delete plumb@pve") ||
		!strings.Contains(calls, "pveum acl delete /pool/plumb --tokens plumb@pve!plumb --roles PlumbGuestBackupVM") {
		t.Errorf("remove calls:\n%s", calls)
	}
	roles, _ := filepath.Glob(filepath.Join(h.state, "role.*"))
	if len(roles) != 0 {
		t.Errorf("roles after --remove: %v", roles)
	}
}

func sortedLines(s string) []string {
	var l []string
	for _, line := range strings.Split(strings.TrimSpace(s), "\n") {
		if line != "" {
			l = append(l, line)
		}
	}
	slices.Sort(l)
	return l
}

// envLinesCall is the pct exec call that rewrites plumb.env in a repair run.
func envLinesCall(t *testing.T, calls []string) string {
	t.Helper()
	for _, c := range calls {
		if strings.HasPrefix(c, "pct exec ") && strings.Contains(c, ".plumb.env.new") {
			return c
		}
	}
	t.Fatalf("no env update:\n%s", strings.Join(calls, "\n"))
	return ""
}

// Bad flags and storages are refused before any change.
func TestSlice7Refusals(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name  string
		setup func(h *host)
		args  []string
		want  string
	}{
		{"tag 0", nil, []string{"--extra-vlan", "vmbr9:0"}, "bad VLAN tag 0"},
		{"tag 4095", nil, []string{"--extra-vlan", "vmbr9:20,4095"}, "bad VLAN tag 4095"},
		{"tag 020", nil, []string{"--extra-vlan", "vmbr9:020"}, "bad VLAN tag 020"},
		{"vlan without tag", nil, []string{"--extra-vlan", "vmbr9"}, "bad --extra-vlan vmbr9"},
		{"vlan with a wildcard", nil, []string{"--extra-vlan", "vmbr9:*"}, "bad --extra-vlan"},
		{"bad bridge", nil, []string{"--extra-bridge", "vmbr9/20"}, "bad --extra-bridge vmbr9/20"},
		{"bridge injection", nil, []string{"--extra-bridge", "vmbr9 --propagate"}, "bad --extra-bridge"},
		{"main bridge", nil, []string{"--extra-bridge", "vmbr0"}, "is the bridge of the install"},
		{"clear alone", nil, []string{"--guest-backup-clear"}, "--guest-backup-clear works only with --guest-backup-storage"},
		{"bad storage", nil, []string{"--guest-backup-storage", "a b"}, "bad --guest-backup-storage"},
		{"missing storage", nil, []string{"--guest-backup-storage", "nope"}, "storage nope (--guest-backup-storage) does not exist"},
		{"no backup content", func(h *host) { h.addStorage("bk", "dir", "iso,images", "") }, []string{"--guest-backup-storage", "bk"},
			`has the content "iso,images" without "backup"`},
		{"snippets content", func(h *host) { h.addStorage("bk", "dir", "backup,snippets", "") }, []string{"--guest-backup-storage", "bk"},
			`the token could point cicustom at any snippet there`},
		{"a storage of Plumb", nil, []string{"--guest-backup-storage", "plumb-snippets"}, "is a storage of Plumb itself"},
		{"the disk storage", nil, []string{"--guest-backup-storage", "local-lvm"}, "is a storage of Plumb itself"},
		// M2 of #321: another storage with a Plumb AllocateSpace line and backup content.
		{"second storage (disk)", func(h *host) {
			h.addStorage("bk", "dir", "backup", "")
			h.setContent("local-lvm", "images,rootdir,backup")
		}, []string{"--guest-backup-storage", "bk"}, "another storage of Plumb has the content \"backup\""},
		{"second storage (snippets)", func(h *host) {
			h.addStorage("bk", "dir", "backup", "")
			h.setContent("plumb-snippets", "snippets,backup")
		}, []string{"--guest-backup-storage", "bk"}, "another storage of Plumb has the content \"backup\""},
		{"remove with flags", nil, []string{"--remove", "--extra-bridge", "vmbr9"}, "work only with an install or a repair run"},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			h := newHost(t)
			if c.setup != nil {
				c.setup(h)
			}
			code, out, errOut := h.run([]string{"PLUMB_INSTALL_ASSUME_ROOT=1"}, append([]string{"--yes", "--plumb-binary", h.plumb}, c.args...)...)
			if code == 0 || !strings.Contains(errOut, c.want) {
				t.Errorf("exit %d, want the error %q:\n%s\n%s", code, c.want, out, errOut)
			}
			if ch := h.changed(); len(ch) != 0 {
				t.Errorf("changed the host: %v", ch)
			}
		})
	}
}

// The second-storage refusal names each storage with the fix.
func TestSlice7SecondStorageFix(t *testing.T) {
	t.Parallel()
	h := newHost(t)
	h.addStorage("bk", "dir", "backup", "")
	h.setContent("local-lvm", "images,backup,rootdir")
	code, out, errOut := h.run(nil, "--dry-run", "--plumb-binary", h.plumb, "--guest-backup-storage", "bk")
	if code == 0 || !strings.Contains(out, "  local-lvm: pvesm set local-lvm --content images,rootdir") || strings.Contains(out, "plumb-snippets:") {
		t.Errorf("exit %d:\n%s\n%s", code, out, errOut)
	}
	// Without a guest backup storage there is no VM.Backup, so no check.
	if code, out, errOut := h.run(nil, "--dry-run", "--plumb-binary", h.plumb); code != 0 {
		t.Errorf("without --guest-backup-storage: %d\n%s\n%s", code, out, errOut)
	}
}

// The encryption state (D-406) is said before any change.
func TestSlice7Encryption(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name, typ, extra string
		clear            bool
		want             string
	}{
		{"pbs with key", "pbs", `"encryption-key":"aa:bb:cc"`, false, "Guest backups go to bk: a Proxmox Backup Server with client-side encryption."},
		{"pbs without key", "pbs", "", false, "NOTE: storage bk (type pbs) has no client-side encryption. Plumb refuses every guest backup"},
		{"dir", "dir", "", false, "until a repair run with --guest-backup-clear says that backups in clear are wanted (D-406)"},
		{"dir, clear", "dir", "", true, "guest backups are stored IN CLEAR there"},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			h := newHost(t)
			h.addStorage("bk", c.typ, "backup", c.extra)
			args := []string{"--dry-run", "--plumb-binary", h.plumb, "--guest-backup-storage", "bk"}
			if c.clear {
				args = append(args, "--guest-backup-clear")
			}
			code, out, errOut := h.run(nil, args...)
			if code != 0 || !strings.Contains(out, c.want) {
				t.Errorf("exit %d, want %q:\n%s\n%s", code, c.want, out, errOut)
			}
		})
	}
}

// L2 of #321: backups of VMIDs in Plumb's range without this install's
// marker are named; Plumb's own and those outside the range are not.
func TestSlice7ForeignBackups(t *testing.T) {
	t.Parallel()
	h := newHost(t)
	root := h.installed()
	st, _ := os.ReadFile(h.stateF)
	id := regexp.MustCompile(`(?m)^install_id=(\S+)$`).FindStringSubmatch(string(st))
	if id == nil {
		t.Fatalf("no install id:\n%s", st)
	}
	h.addStorage("bk", "pbs", "backup", `"encryption-key":"1"`)
	list := `[{"volid":"bk:backup/vm/9001/2026-11-01T03:00:00Z","vmid":9001,"notes":"","content":"backup"},` +
		`{"volid":"bk:backup/vm/9002/2026-11-01T03:00:00Z","vmid":9002,"notes":"plumb:` + id[1] + `:web:1","content":"backup"},` +
		`{"volid":"bk:backup/vm/9003/2026-11-01T03:00:00Z","vmid":"9003","notes":"plumb:00000000-0000-0000-0000-000000000000:web:1"},` +
		`{"volid":"bk:backup/ct/120/2026-11-01T03:00:00Z","vmid":120,"notes":""}]`
	os.WriteFile(filepath.Join(h.state, "backups.bk"), []byte(list), 0o644)
	code, out, errOut := h.run(root, "--dry-run", "--plumb-binary", h.plumb, "--guest-backup-storage", "bk")
	if code != 0 || !strings.Contains(out, "WARNING: storage bk holds backups of VMIDs in Plumb's range (9000-9099)") {
		t.Fatalf("exit %d:\n%s\n%s", code, out, errOut)
	}
	for vol, named := range map[string]bool{"vm/9001/": true, "vm/9002/": false, "vm/9003/": true, "ct/120/": false} {
		if strings.Contains(out, "  bk:backup/"+vol) != named {
			t.Errorf("%s named = %v, want %v:\n%s", vol, !named, named, out)
		}
	}
	// A list that cannot be read is a note, not a stop.
	code, out, _ = h.run(append(root, "FAKE_CONTENT_FAIL=1"), "--dry-run", "--plumb-binary", h.plumb, "--guest-backup-storage", "bk")
	if code != 0 || !strings.Contains(out, "NOTE: cannot list the backups on bk") {
		t.Errorf("list fails: %d\n%s", code, out)
	}
}
