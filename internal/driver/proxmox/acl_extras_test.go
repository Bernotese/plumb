package proxmox

// The install settings of slice 7 in the settings view (T-218, D-401,
// D-405, D-406, M2 of #321).

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestParseExtraNets(t *testing.T) {
	good := map[string][]ExtraNet{
		"":                           nil,
		"vmbr9":                      {{Bridge: "vmbr9"}},
		"vmbr9/20,vmbr9":             {{Bridge: "vmbr9", Tag: 20}, {Bridge: "vmbr9"}},
		"vmbr1/1,vmbr1/4094,br-x.10": {{Bridge: "vmbr1", Tag: 1}, {Bridge: "vmbr1", Tag: 4094}, {Bridge: "br-x.10"}},
	}
	for in, want := range good {
		got, err := ParseExtraNets(in)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Errorf("ParseExtraNets(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	for _, in := range []string{"vmbr9/0", "vmbr9/4095", "vmbr9/020", "vmbr9/", "/20", "vmbr9,vmbr9", "vmbr9/20,vmbr9/20",
		"vm br", "vmbr9/20/30", "1abc", "abcdefghijklmnop", "vmbr9,", ",vmbr9", "vmbr9/-1", "vmbr9/+20", "vmbr9;id", "vmbr9/2 0"} {
		if got, err := ParseExtraNets(in); err == nil {
			t.Errorf("ParseExtraNets(%q) = %v, want an error", in, got)
		}
	}
}

func TestInstallExtrasFromEnv(t *testing.T) {
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	x, err := InstallExtrasFromEnv(env(nil))
	if err != nil || !reflect.DeepEqual(x, InstallExtras{}) {
		t.Errorf("none: %+v %v", x, err)
	}
	x, err = InstallExtrasFromEnv(env(map[string]string{EnvExtraNets: "vmbr9,vmbr9/20", EnvGuestBackupStorage: "plumb-backup", EnvGuestBackupClear: "1"}))
	if err != nil || len(x.ExtraNets) != 2 || x.GuestBackupStorage != "plumb-backup" || !x.GuestBackupClear {
		t.Errorf("all: %+v %v", x, err)
	}
	if !x.NetAllowed("vmbr9", 0) || !x.NetAllowed("vmbr9", 20) || x.NetAllowed("vmbr9", 30) || x.NetAllowed("vmbr0", 0) {
		t.Errorf("NetAllowed: %+v", x)
	}
	// Exactly what install.sh writes without the flags (a repair run).
	x, err = InstallExtrasFromEnv(env(map[string]string{EnvExtraNets: "", EnvGuestBackupStorage: "", EnvGuestBackupClear: "0"}))
	if err != nil || !reflect.DeepEqual(x, InstallExtras{}) {
		t.Errorf("cleared: %+v %v", x, err)
	}
	for _, c := range []struct {
		env  map[string]string
		name string // the variable that the error names
	}{
		{map[string]string{EnvGuestBackupClear: "1"}, EnvGuestBackupClear},
		{map[string]string{EnvGuestBackupStorage: "pb", EnvGuestBackupClear: "yes"}, EnvGuestBackupClear},
		{map[string]string{EnvGuestBackupStorage: "bad name"}, EnvGuestBackupStorage},
		{map[string]string{EnvGuestBackupStorage: "-x"}, EnvGuestBackupStorage},
		{map[string]string{EnvExtraNets: "vmbr9/0"}, EnvExtraNets},
	} {
		if x, err := InstallExtrasFromEnv(env(c.env)); err == nil {
			t.Errorf("%v: %+v, want an error", c.env, x)
		} else if !strings.Contains(err.Error(), c.name) {
			t.Errorf("%v: the error does not name %s: %v", c.env, c.name, err)
		}
	}
}

func slice7Extras() InstallExtras {
	return InstallExtras{ExtraNets: []ExtraNet{{Bridge: "vmbr9"}, {Bridge: "vmbr9", Tag: 20}}, GuestBackupStorage: "pbs1"}
}

// Every new line is in the view with its own state, in the order of
// acl_plan; a line on the bridge (propagate 0) does not make the tag line
// ok (D-401).
func TestViewSlice7Lines(t *testing.T) {
	f := newFakePVE(t)
	x := slice7Extras()
	cfg := f.driver(t).cfg
	f.perms = fullPerms()
	addLines(f.perms, installLines(cfg, x))
	delete(f.perms, "/sdn/zones/localnetwork/vmbr9/20")
	f.storageConfigs = []map[string]any{
		{"storage": "pbs1", "type": "pbs", "content": "backup", "encryption-key": "aa:bb:cc"},
		{"storage": "local-lvm", "type": "lvmthin", "content": "images,rootdir"},
	}
	v := NewViewer(f.driver(t), "plumb@pve", x)
	got := v.View(context.Background())
	var lines []string
	for _, l := range got.ACLLines {
		lines = append(lines, l.Path+" "+l.Role+" "+map[bool]string{true: "ok", false: "missing"}[l.OK])
	}
	want := []string{
		"/pool/" + fakePool + " PlumbGuests ok",
		"/storage/" + cfg.DiskStorage + " PlumbDisk ok",
		"/storage/" + cfg.ImageStorage + " PlumbImages ok",
		"/storage/" + cfg.SnippetStorage + " PlumbSnippets ok",
		"/sdn/zones/localnetwork/" + cfg.Bridge + " PlumbBridge ok",
		"/nodes/" + fakeNode + " PlumbNode ok",
		"/ PlumbAudit ok",
		"/sdn/zones/localnetwork/vmbr9 PlumbBridge ok",
		"/sdn/zones/localnetwork/vmbr9/20 PlumbBridge missing",
		"/pool/" + fakePool + " PlumbGuestBackupVM ok",
		"/storage/pbs1 PlumbGuestBackup ok",
	}
	if strings.Join(lines, "\n") != strings.Join(want, "\n") {
		t.Errorf("lines:\n%s\nwant:\n%s", strings.Join(lines, "\n"), strings.Join(want, "\n"))
	}
	if got.ACL != ACLMissing || !reflect.DeepEqual(got.ExtraNets, x.ExtraNets) {
		t.Errorf("view: %+v", got)
	}
	if g := got.GuestBackup; g == nil || g.Storage != "pbs1" || g.State != GuestBackupPBSKey || len(g.Others) != 0 {
		t.Errorf("guest backup: %+v", g)
	}
	// VM.Backup granted through the pool but not on the backup storage.
	f.mu.Lock()
	delete(f.perms["/pool/"+fakePool], "VM.Backup")
	f.perms["/sdn/zones/localnetwork/vmbr9/20"] = map[string]int{"SDN.Use": 0}
	f.mu.Unlock()
	v.now = func() time.Time { return time.Now().Add(time.Minute) }
	got = v.View(context.Background())
	for _, l := range got.ACLLines {
		if l.OK == (l.Role == "PlumbGuestBackupVM") {
			t.Errorf("without VM.Backup: %+v", l)
		}
	}
}

// The storage state (D-406) and the second-storage check (M2 of #321).
func TestGuestBackupView(t *testing.T) {
	perms := map[string]map[string]int{
		"/storage/local-lvm":      {"Datastore.AllocateSpace": 0, "Datastore.Audit": 0},
		"/storage/plumb-snippets": {"Datastore.AllocateSpace": 0},
		"/storage/plumb-images":   {"Datastore.AllocateTemplate": 0, "Datastore.Audit": 0},
		"/storage/bk":             {"Datastore.AllocateSpace": 0, "Datastore.Audit": 0},
		"/storage/seen":           {"Datastore.Audit": 0},
	}
	storages := []map[string]any{
		{"storage": "bk", "type": "dir", "content": "backup"},
		{"storage": "local-lvm", "type": "lvmthin", "content": "images,rootdir"},
		{"storage": "plumb-snippets", "type": "dir", "content": "backup,snippets"}, // AllocateSpace: the token could write there
		{"storage": "plumb-images", "type": "dir", "content": "import, backup"},    // no AllocateSpace
		{"storage": "seen", "type": "nfs", "content": "backup"},                    // Audit only
		{"storage": "other", "type": "dir", "content": "backup"},                   // no right
	}
	x := InstallExtras{GuestBackupStorage: "bk"}
	g := guestBackupView(x, storages, perms)
	if g.State != GuestBackupClearRefused || !reflect.DeepEqual(g.Others, []string{"plumb-snippets"}) {
		t.Errorf("dir, no flag: %+v", g)
	}
	x.GuestBackupClear = true
	if g = guestBackupView(x, storages, perms); g.State != GuestBackupStatedClear {
		t.Errorf("dir, flag: %+v", g)
	}
	// A right inherited from /storage counts too.
	perms["/storage"] = map[string]int{"Datastore.AllocateSpace": 1}
	if g = guestBackupView(x, storages, perms); !reflect.DeepEqual(g.Others, []string{"plumb-snippets", "plumb-images", "seen", "other"}) {
		t.Errorf("inherited: %+v", g)
	}
	for key, want := range map[any]string{"aa:bb": GuestBackupPBSKey, "1": GuestBackupPBSKey, float64(1): GuestBackupPBSKey,
		"": GuestBackupClearRefused, "0": GuestBackupClearRefused, nil: GuestBackupClearRefused} {
		s := []map[string]any{{"storage": "bk", "type": "pbs", "content": "backup", "encryption-key": key}}
		if g := guestBackupView(InstallExtras{GuestBackupStorage: "bk"}, s, nil); g.State != want {
			t.Errorf("pbs encryption-key %#v: %s, want %s", key, g.State, want)
		}
	}
	// encryption-key on a storage that is not PBS means nothing.
	s := []map[string]any{{"storage": "bk", "type": "dir", "content": "backup", "encryption-key": "aa"}}
	if g := guestBackupView(InstallExtras{GuestBackupStorage: "bk"}, s, nil); g.State != GuestBackupClearRefused {
		t.Errorf("dir with a key field: %s", g.State)
	}
	if guestBackupView(InstallExtras{}, storages, perms) != nil {
		t.Error("a view without a guest backup storage")
	}
}

// GET /storage is read only with a guest backup storage; when it fails, the
// ACL state is unknown and the backup state comes from the install alone.
func TestViewGuestBackupReadFails(t *testing.T) {
	f := newFakePVE(t)
	f.perms = fullPerms()
	v := NewViewer(f.driver(t), "plumb@pve", InstallExtras{})
	v.View(context.Background())
	if n := f.requests("GET /storage"); n != 0 {
		t.Errorf("GET /storage sent %d times without a guest backup storage", n)
	}
	v = NewViewer(f.driver(t), "plumb@pve", InstallExtras{GuestBackupStorage: "pbs1", GuestBackupClear: true})
	got := v.View(context.Background())
	if got.ACL != ACLUnknown || got.ACLError != "Proxmox answered 500 for /storage" || len(got.ACLLines) != 0 {
		t.Errorf("view: %+v", got)
	}
	if g := got.GuestBackup; g == nil || g.State != GuestBackupStatedClear || g.Others == nil || len(g.Others) != 0 {
		t.Errorf("guest backup: %+v", g)
	}
	// The cached copy does not share the slices.
	got.GuestBackup.Others = append(got.GuestBackup.Others, "x")
	if again := v.View(context.Background()); len(again.GuestBackup.Others) != 0 {
		t.Errorf("cache shared: %+v", again.GuestBackup)
	}
}
