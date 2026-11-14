package agent

import (
	"crypto/ed25519"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"

	"github.com/Bernotese/plumb/internal/wire"
)

func repairKey(t testing.TB, id string) wire.RepairKey {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	sp, _ := ssh.NewPublicKey(pub)
	return wire.RepairKey{ID: id, PublicKey: strings.TrimSpace(string(ssh.MarshalAuthorizedKey(sp)))}
}

// TestRepairLines (D-383): one line per key, other lines stay, foreign
// plumb-repair- lines go; a rotation keeps both keys, a revoke leaves
// only the new one; unbind removes every repair line. 0700/0600.
func TestRepairLines(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, authorizedKeysPath)
	// A missing /root/.ssh is made 0700.
	if err := writeRepairLines(root, &wire.RepairWatch{}); err != nil {
		t.Fatal(err)
	}
	if di, _ := os.Stat(filepath.Dir(path)); di.Mode().Perm() != 0o700 {
		t.Fatalf("new .ssh mode %v", di.Mode())
	}
	write(t, path, "ssh-ed25519 AAAAuser user@laptop\nssh-ed25519 AAAAold plumb-repair-gone\n# comment\n")
	k1, k2 := repairKey(t, "aaa111"), repairKey(t, "bbb222")
	from := []string{"192.0.2.10"}
	w := &wire.RepairWatch{Keys: []wire.RepairKey{k1}, From: from}
	if err := writeRepairLines(root, w); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	want := "ssh-ed25519 AAAAuser user@laptop\n# comment\n" + wire.RepairLine(k1, from) + "\n"
	if string(b) != want {
		t.Fatalf("authorized_keys:\n%s", b)
	}
	if got := presentLines(root, w); len(got) != 1 || got[0] != "aaa111" {
		t.Fatalf("lines %v", got)
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", fi.Mode())
	}
	// Rotation: both lines. Revoke: only the new one.
	both := &wire.RepairWatch{Keys: []wire.RepairKey{k2, k1}, From: from}
	if err := writeRepairLines(root, both); err != nil {
		t.Fatal(err)
	}
	if got := presentLines(root, both); len(got) != 2 {
		t.Fatalf("after rotation %v", got)
	}
	only := &wire.RepairWatch{Keys: []wire.RepairKey{k2}, From: from}
	if err := writeRepairLines(root, only); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(path)
	if strings.Contains(string(b), "plumb-repair-aaa111") || !strings.Contains(string(b), "plumb-repair-bbb222") {
		t.Fatalf("after revoke:\n%s", b)
	}
	// A new from rewrites the line: the old line does not count.
	moved := &wire.RepairWatch{Keys: []wire.RepairKey{k2}, From: []string{"192.0.2.11"}}
	if got := presentLines(root, moved); len(got) != 0 {
		t.Fatalf("a line with the old from counts: %v", got)
	}
	if err := removeRepairLines(root); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(path)
	if string(b) != "ssh-ed25519 AAAAuser user@laptop\n# comment\n" {
		t.Fatalf("after unbind:\n%s", b)
	}
}

// A symlinked authorized_keys is not followed or written.
func TestRepairLinesSymlink(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(t.TempDir(), "victim")
	write(t, target, "x\n")
	path := filepath.Join(root, authorizedKeysPath)
	os.MkdirAll(filepath.Dir(path), 0o700)
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
	w := &wire.RepairWatch{Keys: []wire.RepairKey{repairKey(t, "aaa111")}, From: []string{"192.0.2.10"}}
	if err := writeRepairLines(root, w); err == nil {
		t.Fatal("a symlink was written")
	}
	if b, _ := os.ReadFile(target); string(b) != "x\n" {
		t.Fatal("the symlink target changed")
	}
}

func TestParseSSHDT(t *testing.T) {
	for in, want := range map[string]wire.RepairSSHD{
		"port 22\npermitrootlogin prohibit-password\n":      {Status: wire.SSHDOk, Port: 22},
		"permitrootlogin forced-commands-only\nport 2222\n": {Status: wire.SSHDOk, Port: 2222},
		"permitrootlogin without-password\nport 22\nport 2": {Status: wire.SSHDOk, Port: 22},
		"permitrootlogin no\nport 22\n":                     {Status: wire.SSHDRootRefused, Port: 22},
		"port 22\n":                                         {Status: wire.SSHDAbsent},
		"":                                                  {Status: wire.SSHDAbsent},
		"permitrootlogin yes\nport 99999\n":                 {Status: wire.SSHDAbsent},
	} {
		if got, _ := parseSSHDT([]byte(in)); got != want {
			t.Errorf("%q: %+v, want %+v", in, got, want)
		}
	}
	// authorizedkeysfile (review n1 of #298).
	for in, want := range map[string]bool{
		"port 22\npermitrootlogin yes\n": true,
		"port 22\npermitrootlogin yes\nauthorizedkeysfile .ssh/authorized_keys .ssh/authorized_keys2\n": true,
		"port 22\npermitrootlogin yes\nauthorizedkeysfile %h/.ssh/authorized_keys\n":                    true,
		"port 22\npermitrootlogin yes\nauthorizedkeysfile /etc/ssh/keys/%u\n":                           false,
		"port 22\npermitrootlogin yes\nauthorizedkeysfile none\n":                                       false,
	} {
		if _, got := parseSSHDT([]byte(in)); got != want {
			t.Errorf("%q: keysFile %v, want %v", in, got, want)
		}
	}
}

// FuzzParseSSHDT: whatever sshd prints, the result passes the report check.
func FuzzParseSSHDT(f *testing.F) {
	f.Add("port 22\npermitrootlogin yes\n")
	f.Add("port -1\npermitrootlogin \x00\n")
	f.Fuzz(func(t *testing.T, out string) {
		st, _ := parseSSHDT([]byte(out))
		r := wire.RepairReport{SSHD: st}
		if err := wire.CheckRepairReport(&r); err != nil {
			t.Fatalf("%q → %+v: %v", out, st, err)
		}
	})
}

// The fingerprint equals OpenSSH's (x/crypto/ssh); garbage gives none.
func TestHostKeyFingerprints(t *testing.T) {
	root := t.TempDir()
	pub, _, _ := ed25519.GenerateKey(nil)
	sp, _ := ssh.NewPublicKey(pub)
	write(t, filepath.Join(root, "etc/ssh/ssh_host_ed25519_key.pub"), string(ssh.MarshalAuthorizedKey(sp)))
	write(t, filepath.Join(root, "etc/ssh/ssh_host_rsa_key.pub"), "ssh-rsa !!!not-base64 root@x\n")
	got := hostKeyFingerprints(root)
	if len(got) != 1 || got[0] != ssh.FingerprintSHA256(sp) {
		t.Fatalf("%v, want %s", got, ssh.FingerprintSHA256(sp))
	}
}

func FuzzPubKeyFingerprint(f *testing.F) {
	f.Add("ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIG root@x")
	f.Fuzz(func(t *testing.T, line string) {
		if fp, ok := pubKeyFingerprint(line); ok && !wire.ValidHostKeyFP(fp) {
			t.Fatalf("%q → %q", line, fp)
		}
	})
}
