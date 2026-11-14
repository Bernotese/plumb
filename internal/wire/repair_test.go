package wire

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/binary"
	"strings"
	"testing"
)

func edKey(t testing.TB) string {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	var b []byte
	for _, part := range [][]byte{[]byte("ssh-ed25519"), pub} {
		b = binary.BigEndian.AppendUint32(b, uint32(len(part)))
		b = append(b, part...)
	}
	return "ssh-ed25519 " + base64.StdEncoding.EncodeToString(b)
}

// TestRepairWatchFromIPOnly (review R4 of #290): the agent refuses a from
// entry that is not an IP literal, and every other bad key set.
func TestRepairWatchFromIPOnly(t *testing.T) {
	k := RepairKey{ID: "a1b2c3", PublicKey: edKey(t)}
	ok := RepairWatch{Keys: []RepairKey{k}, From: []string{"192.0.2.10", "2001:db8::1"}}
	if err := CheckRepairWatch(ok); err != nil {
		t.Fatal(err)
	}
	line := RepairLine(k, ok.From)
	if !strings.HasPrefix(line, `restrict,from="192.0.2.10,2001:db8::1",command="umask 077;`) ||
		!strings.HasSuffix(line, " "+k.PublicKey+" plumb-repair-a1b2c3") || strings.Count(line, `"`) != 4 {
		t.Fatalf("line %q", line)
	}
	for name, w := range map[string]RepairWatch{
		"host name":   {Keys: ok.Keys, From: []string{"plumb.lan"}},
		"cidr":        {Keys: ok.Keys, From: []string{"192.0.2.0/24"}},
		"quote":       {Keys: ok.Keys, From: []string{`192.0.2.1",command="id`}},
		"no from":     {Keys: ok.Keys},
		"rsa":         {Keys: []RepairKey{{ID: "a", PublicKey: "ssh-rsa AAAA"}}, From: ok.From},
		"bad id":      {Keys: []RepairKey{{ID: "A-1", PublicKey: k.PublicKey}}, From: ok.From},
		"key garbage": {Keys: []RepairKey{{ID: "a", PublicKey: "ssh-ed25519 " + base64.StdEncoding.EncodeToString([]byte("x"))}}, From: ok.From},
		"key option":  {Keys: []RepairKey{{ID: "a", PublicKey: k.PublicKey + " x"}}, From: ok.From},
		"three keys":  {Keys: []RepairKey{k, k, k}, From: ok.From},
	} {
		if CheckRepairWatch(w) == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if strings.ContainsRune(RepairForcedCommand, '"') {
		t.Fatal("the forced command holds a double quote")
	}
}

// TestRepairReportLimits: an oversized or malformed report is refused.
func TestRepairReportLimits(t *testing.T) {
	fp := "SHA256:" + strings.Repeat("A", 43)
	good := RepairReport{Lines: []string{"a1"}, SSHD: RepairSSHD{Status: SSHDOk, Port: 22}, HostKeys: []string{fp}}
	if err := CheckRepairReport(&good); err != nil {
		t.Fatal(err)
	}
	nine := make([]string, 9)
	for i := range nine {
		nine[i] = fp
	}
	for name, r := range map[string]RepairReport{
		"nil sshd":      {},
		"port 0":        {SSHD: RepairSSHD{Status: SSHDOk}},
		"port big":      {SSHD: RepairSSHD{Status: SSHDOk, Port: 70000}},
		"absent port":   {SSHD: RepairSSHD{Status: SSHDAbsent, Port: 22}},
		"status":        {SSHD: RepairSSHD{Status: "maybe", Port: 22}},
		"nine keys":     {SSHD: good.SSHD, HostKeys: nine},
		"long key":      {SSHD: good.SSHD, HostKeys: []string{fp + "A"}},
		"bad key":       {SSHD: good.SSHD, HostKeys: []string{"MD5:aa"}},
		"bad line":      {SSHD: good.SSHD, Lines: []string{"../x"}},
		"control chars": {SSHD: good.SSHD, Lines: []string{"a\n"}},
		// T-207: a type only for a listed key, only of the list.
		"type of an unlisted key": {SSHD: good.SSHD, HostKeys: []string{fp}, HostKeyTypes: map[string]string{"SHA256:" + strings.Repeat("B", 43): "ssh-ed25519"}},
		"unknown type":            {SSHD: good.SSHD, HostKeys: []string{fp}, HostKeyTypes: map[string]string{fp: "ssh-dss"}},
		"text as type":            {SSHD: good.SSHD, HostKeys: []string{fp}, HostKeyTypes: map[string]string{fp: "ssh-ed25519\n"}},
	} {
		if CheckRepairReport(&r) == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	typed := good
	typed.HostKeyTypes = map[string]string{fp: "ssh-ed25519"}
	if err := CheckRepairReport(&typed); err != nil {
		t.Fatalf("typed: %v", err)
	}
	if CheckRepairReport(nil) == nil {
		t.Error("nil accepted")
	}
}

// FuzzCheckRepairReport: guest data never panics the check, and what
// passes is within the limits.
func FuzzCheckRepairReport(f *testing.F) {
	f.Add("a1", "ok", 22, "SHA256:"+strings.Repeat("A", 43))
	f.Add("", "absent", 0, "")
	f.Fuzz(func(t *testing.T, line, status string, port int, key string) {
		r := RepairReport{Lines: []string{line}, SSHD: RepairSSHD{Status: status, Port: port}, HostKeys: []string{key}}
		if CheckRepairReport(&r) != nil {
			return
		}
		if len(line) > 32 || len(key) != 50 || port < 0 || port > 65535 {
			t.Fatalf("passed: %+v", r)
		}
	})
}
