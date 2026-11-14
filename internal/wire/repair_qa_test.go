package wire

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"testing"

	"golang.org/x/crypto/ssh"
)

// T-195 (T-191 part), mutant W2: the key type word must be ssh-ed25519,
// also when the blob has the size of an ed25519 key. The agent writes the
// line only from a set that passes this check (D-383).
func TestCheckRepairWatchQAKeyType(t *testing.T) {
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	sp, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	b64 := base64.StdEncoding.EncodeToString(sp.Marshal())
	ok := RepairWatch{Keys: []RepairKey{{ID: "a1", PublicKey: "ssh-ed25519 " + b64}}, From: []string{"192.0.2.5"}}
	if err := CheckRepairWatch(ok); err != nil {
		t.Fatalf("a good set: %v", err)
	}
	for _, typ := range []string{"ssh-rsa", "ecdsa-sha2-nistp256", "sk-ssh-ed25519@openssh.com", "SSH-ED25519"} {
		bad := RepairWatch{Keys: []RepairKey{{ID: "a1", PublicKey: typ + " " + b64}}, From: []string{"192.0.2.5"}}
		if err := CheckRepairWatch(bad); err == nil {
			t.Errorf("type %s with an ed25519 blob passed", typ)
		}
	}
}

// TestRepairLineQAGolden (Q1 of #305): the whole authorized_keys line,
// character for character. Any change of the forced command or of the
// options is then a reviewed diff of this text.
func TestRepairLineQAGolden(t *testing.T) {
	k := RepairKey{ID: "a1b2c3", PublicKey: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIGolden"}
	got := RepairLine(k, []string{"192.0.2.10", "2001:db8::10"})
	want := `restrict,from="192.0.2.10,2001:db8::10",command="` +
		`umask 077; d=/run/plumb-repair; rm -rf $d; mkdir -m 700 $d && head -c 67108864 | ` +
		`tar -x -f - -C $d --no-same-owner --no-same-permissions install.sh plumb-agent plumb-agent.sha256 ca.pem enroll.json && ` +
		`cd $d && sha256sum -c --status plumb-agent.sha256 && sh ./install.sh; r=$?; cd /; rm -rf $d; exit $r` +
		`" ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIGolden plumb-repair-a1b2c3`
	if got != want {
		t.Fatalf("line:\n%s\nwant:\n%s", got, want)
	}
}
