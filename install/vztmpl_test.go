package install

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// T-077, D-229: plumb-images holds content import,vztmpl. An install from
// before D-229 gets it in a repair run: new binary, then content, then
// restart; a failed start sets the content back to import.

const (
	setVztmpl = "pvesm set plumb-images --content import,vztmpl"
	setImport = "pvesm set plumb-images --content import"
	renameBin = "pct exec 105 -- mv -f /usr/local/bin/.plumb.plumb-new /usr/local/bin/plumb"
	restart   = "pct exec 105 -- systemctl restart plumb.service"
)

func (h *host) content(id string) string {
	b, _ := os.ReadFile(filepath.Join(h.state, "content."+id))
	return strings.TrimSpace(string(b))
}

func (h *host) setContent(id, c string) {
	os.WriteFile(filepath.Join(h.state, "content."+id), []byte(c+"\n"), 0o644)
}

// oldInstall is a finished install from before D-229.
func oldInstall(t *testing.T) *host {
	t.Helper()
	h := installed(t)
	h.setContent("plumb-images", "import")
	h.resetCalls()
	return h
}

// exact returns the position of the call that is exactly c, or -1.
func exact(calls []string, c string) int {
	for i, x := range calls {
		if x == c {
			return i
		}
	}
	return -1
}

func TestNewInstallSetsVztmpl(t *testing.T) {
	h := installed(t)
	if c := h.content("plumb-images"); c != "import,vztmpl" {
		t.Errorf("content of plumb-images = %q", c)
	}
	// A second run sees the set and changes no content.
	h.resetCalls()
	if code, out, errOut := h.run(asRoot, "--yes", "--plumb-binary", h.plumb); code != 0 || strings.Contains(out, "Change: storage plumb-images") {
		t.Fatalf("repair: %d %s %s", code, out, errOut)
	}
	if index(h.calls(), "pvesm set") >= 0 {
		t.Errorf("a repair run with import,vztmpl set the content: %v", h.calls())
	}
}

func TestUpgradeOrder(t *testing.T) {
	h := oldInstall(t)
	want := h.newBinary()
	code, out, errOut := h.run(asRoot, "--yes", "--plumb-binary", h.plumb)
	if code != 0 {
		t.Fatalf("repair: %d %s %s", code, out, errOut)
	}
	calls := h.calls()
	mv, set, rs := exact(calls, renameBin), exact(calls, setVztmpl), exact(calls, restart)
	if mv < 0 || set < 0 || rs < 0 || mv > set || set > rs {
		t.Errorf("order binary %d, content %d, restart %d: %v", mv, set, rs, calls)
	}
	if strings.Count(strings.Join(calls, "\n"), "pvesm set") != 1 {
		t.Errorf("more than one content change: %v", calls)
	}
	if c := h.content("plumb-images"); c != "vztmpl,import" { // reordered like Proxmox
		t.Errorf("content = %q", c)
	}
	if h.running() != want {
		t.Error("the new binary does not run")
	}
	// The plan shows the change before the question (D-084).
	if !strings.Contains(out, "Change: storage plumb-images content import → import,vztmpl") {
		t.Errorf("no change line:\n%s", out)
	}
	// Reordered by Proxmox: still the set, so the next run changes nothing.
	h.resetCalls()
	if code, _, errOut := h.run(asRoot, "--yes", "--plumb-binary", h.plumb); code != 0 || index(h.calls(), "pvesm set") >= 0 {
		t.Errorf("run after the upgrade: %d %q %v", code, errOut, h.calls())
	}
}

func TestUpgradeDryRun(t *testing.T) {
	h := oldInstall(t)
	code, out, errOut := h.run(nil, "--dry-run", "--plumb-binary", h.plumb)
	if code != 0 {
		t.Fatalf("dry run: %d %s %s", code, out, errOut)
	}
	if c := h.changed(); len(c) != 0 {
		t.Errorf("dry run changed %v", c)
	}
	change, note, ask := strings.Index(out, "Change: storage plumb-images content import → import,vztmpl"),
		strings.Index(out, "reorders the content lists of other storages"), strings.Index(out, "would ask: Continue?")
	if change < 0 || note < 0 || ask < 0 || note > ask || change > ask {
		t.Errorf("change %d, note %d, question %d:\n%s", change, note, ask, out)
	}
	mv := strings.Index(out, "+ pct exec 105 -- mv -f /usr/local/bin/.plumb.plumb-new /usr/local/bin/plumb")
	set := strings.Index(out, `+ pvesm set plumb-images --content import\,vztmpl`)
	rs := strings.Index(out, "+ pct exec 105 -- systemctl restart plumb.service")
	if mv < 0 || set < 0 || rs < 0 || mv > set || set > rs {
		t.Errorf("printed order binary %d, content %d, restart %d:\n%s", mv, set, rs, out)
	}
	if h.content("plumb-images") != "import" {
		t.Error("dry run changed the content")
	}
}

// Review S9 of T-038: when the new binary does not start, the content goes
// back to import, and the failure text gives the line for a manual
// rollback of the binary.
func TestUpgradeRollback(t *testing.T) {
	for name, env := range map[string][]string{
		"crash loop":     {"FAKE_CRASH_LOOP=1", "PLUMB_INSTALL_RUNNING_TRIES=2"},
		"old process":    {"FAKE_RESTART_OLD=1", "PLUMB_INSTALL_RUNNING_TRIES=2"},
		"restart failed": {"FAKE_EXEC_FAIL=systemctl restart plumb.service"},
	} {
		t.Run(name, func(t *testing.T) {
			h := oldInstall(t)
			h.newBinary()
			code, out, errOut := h.run(append(asRoot, env...), "--yes", "--plumb-binary", h.plumb)
			if code == 0 {
				t.Fatalf("no failure: %s", out)
			}
			calls := h.calls()
			mv, set, rs, back := exact(calls, renameBin), exact(calls, setVztmpl), exact(calls, restart), exact(calls, setImport)
			if mv < 0 || set <= mv || rs <= set || back <= rs {
				t.Errorf("binary %d, content %d, restart %d, rollback %d: %v", mv, set, rs, back, calls)
			}
			if n := strings.Count("\n"+strings.Join(calls, "\n")+"\n", "\n"+setImport+"\n"); n != 1 {
				t.Errorf("rollback count = %d, want 1", n)
			}
			if c := h.content("plumb-images"); c != "import" {
				t.Errorf("content after the rollback = %q", c)
			}
			for _, want := range []string{"set back to \"import\"", "before you put an older plumb binary back by hand, run: pvesm set plumb-images --content import"} {
				if !strings.Contains(errOut, want) {
					t.Errorf("failure text without %q:\n%s", want, errOut)
				}
			}
		})
	}
	// A failure before the content step changes no content.
	h := oldInstall(t)
	h.newBinary()
	code, _, _ := h.run(append(asRoot, "FAKE_EXEC_FAIL=systemctl daemon-reload"), "--yes", "--plumb-binary", h.plumb)
	if code == 0 || index(h.calls(), "pvesm set") >= 0 || h.content("plumb-images") != "import" {
		t.Errorf("failure before the content step: %d %v", code, h.calls())
	}
	// The rollback keeps the exit code of the failure.
	h = oldInstall(t)
	code, _, _ = h.run(append(asRoot, "FAKE_CRASH_LOOP=1", "PLUMB_INSTALL_RUNNING_TRIES=1"), "--yes", "--plumb-binary", h.plumb)
	if code != 1 {
		t.Errorf("exit %d, want 1", code)
	}
}

// The image storage rule of D-084 accepts {import} and {import, vztmpl}
// as sets; install.sh changes only the plumb-images that it made.
func TestImageStorageContentRule(t *testing.T) {
	for content, ok := range map[string]bool{
		"import":            true,
		"import,vztmpl":     true,
		"vztmpl,import":     true,
		"vztmpl":            false,
		"import,vztmpl,iso": false,
	} {
		h := newHost(t)
		h.setContent("imgs", content)
		code, out, errOut := h.run(nil, "--dry-run", "--plumb-binary", h.plumb, "--image-storage", "imgs")
		if ok != (code == 0) {
			t.Errorf("%q: exit %d %s", content, code, errOut)
		}
		if !ok && !strings.Contains(errOut, `must have exactly "import" or "import,vztmpl"`) {
			t.Errorf("%q: text %q", content, errOut)
		}
		if strings.Contains(out, "+ pvesm set") {
			t.Errorf("%q: changes a storage it did not make", content)
		}
		if note := strings.Contains(out, "NOTE: storage imgs has the content \"import\" only"); note != (content == "import") {
			t.Errorf("%q: note %v", content, note)
		}
	}

	// Adopted with either set; a foreign plumb-images is never changed.
	h := newHost(t)
	seedHalfInstalled(t, h, h.images())
	h.setContent("plumb-images", "vztmpl,import")
	_, out, _ := h.run(nil, "--dry-run", "--plumb-binary", h.plumb)
	if !strings.Contains(out, "so it is adopted") || strings.Contains(out, "+ pvesm set") {
		t.Errorf("adopt import,vztmpl:\n%s", out)
	}
	h = newHost(t)
	seedHalfInstalled(t, h, h.images())
	_, out, _ = h.run(nil, "--dry-run", "--plumb-binary", h.plumb)
	if !strings.Contains(out, "so it is adopted") || !strings.Contains(out, `+ pvesm set plumb-images --content import\,vztmpl`) {
		t.Errorf("adopt import:\n%s", out)
	}
	h = newHost(t)
	seedHalfInstalled(t, h, h.images())
	os.WriteFile(filepath.Join(h.state, "extra.plumb-images"), []byte(`"nodes":"pve2"`), 0o644)
	_, out, _ = h.run(nil, "--dry-run", "--plumb-binary", h.plumb)
	if strings.Contains(out, "+ pvesm set") || !strings.Contains(out, "NOTE: storage plumb-images has the content \"import\" only") {
		t.Errorf("foreign plumb-images:\n%s", out)
	}
}

// --remove also removes the empty template directories of vztmpl.
func TestRemoveTemplateDirs(t *testing.T) {
	h := installed(t)
	os.MkdirAll(filepath.Join(h.images(), "template", "cache"), 0o755)
	os.MkdirAll(filepath.Join(h.images(), "import"), 0o755)
	if code, out, errOut := h.runRemove(); code != 0 {
		t.Fatalf("remove: %d %s %s", code, out, errOut)
	}
	if _, err := os.Stat(h.images()); !os.IsNotExist(err) {
		t.Errorf("image dir stays: %v", err)
	}
}
