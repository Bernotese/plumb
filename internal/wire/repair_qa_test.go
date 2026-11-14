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
