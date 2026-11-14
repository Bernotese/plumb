package agent

import (
	"archive/tar"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/Bernotese/plumb/internal/wire"
)

// TestGuestRepairForcedQA (T-195, Q1 of #305): the forced command of the
// repair line extracts exactly the five members (a sixth one stays out)
// and keeps neither the owner nor the mode of the archive (umask 077). It
// runs in every Go job; only the root run of the guest job (^TestGuest)
// can see an owner or mode that tar would keep, so both mutants of the
// forced text die there, and the member-list mutant dies everywhere.
func TestGuestRepairForcedQA(t *testing.T) {
	for _, tool := range []string{"sh", "tar", "head", "sha256sum", "stat"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s is missing", tool)
		}
	}
	agent := []byte("#!/bin/sh\necho agent\n")
	sum := sha256.Sum256(agent)
	members := []struct {
		name string
		body string
		mode int64
		uid  int
	}{
		// install.sh reports what the forced command extracted.
		{"install.sh", "for f in * .[!.]*; do [ -e \"$f\" ] && stat -c '%u %a %n' \"$f\"; done\nexit 0\n", 0o755, 0},
		{"plumb-agent", string(agent), 0o755, 0},
		{"plumb-agent.sha256", hex.EncodeToString(sum[:]) + "  plumb-agent\n", 0o644, 0},
		{"ca.pem", "-----BEGIN CERTIFICATE-----\nx\n-----END CERTIFICATE-----\n", 0o777, 1000},
		{"enroll.json", "{}\n", 0o644, 0},
		{"evil", "not a member\n", 0o644, 0},
	}
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, m := range members {
		if err := tw.WriteHeader(&tar.Header{Name: m.name, Mode: m.mode, Size: int64(len(m.body)), Uid: m.uid, Gid: m.uid, Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		tw.Write([]byte(m.body))
	}
	tw.Close()

	dir := filepath.Join(t.TempDir(), "plumb-repair")
	forced := strings.ReplaceAll(wire.RepairForcedCommand, "/run/plumb-repair", dir)
	cmd := exec.Command("sh", "-c", forced)
	cmd.Stdin = &buf
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("forced command: %v\n%s", err, out)
	}
	var names []string
	me := strconv.Itoa(os.Getuid())
	for _, l := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		f := strings.Fields(l)
		if len(f) != 3 {
			t.Fatalf("output line %q", l)
		}
		names = append(names, f[2])
		if f[0] != me {
			t.Errorf("%s keeps the owner %s of the archive (want %s)", f[2], f[0], me)
		}
		mode, _ := strconv.ParseInt(f[1], 8, 32)
		if mode&0o077 != 0 {
			t.Errorf("%s has mode %s: group or other bits (umask 077 not applied)", f[2], f[1])
		}
	}
	slices.Sort(names)
	want := slices.Clone(wire.RepairMembers)
	slices.Sort(want)
	if !slices.Equal(names, want) {
		t.Fatalf("extracted %v, want exactly %v", names, want)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("the bundle dir stays")
	}
}
