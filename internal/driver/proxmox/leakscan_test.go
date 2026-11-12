package proxmox

// Review N2: every recording in testdata/pve is scanned in the normal
// test run, so a later recording cannot leak through review.

import (
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// sshKeyRE finds an SSH public key: type, separator, body. The body takes
// base64 characters and their form encodings (%2B, %2F, %3D) only, so it
// stops at an encoded newline (%0A) between two keys of a create form
// (review S1 of #167). The recorder scrubs with it; the leak scan checks
// with it.
var sshKeyRE = regexp.MustCompile(`(ssh-[a-z0-9-]+|ecdsa-[a-z0-9-]+|sk-[a-z0-9@.-]+)(%20|\s|\+)((?:[A-Za-z0-9+/=]|%2[BbFf]|%3[Dd])+)`)

// keyBlob is the start of a base64 SSH key body: the length-prefixed
// type name (ssh-ed25519, ssh-rsa, ecdsa-sha2-*, sk-*, ssh-dss).
var keyBlob = regexp.MustCompile(`AAAA(C3NzaC1lZDI1NTE5|B3NzaC1yc2E|E2VjZHNhLXNoYTIt|GnNrLXNzaC1lZDI1NTE5|InNrLWVjZHNh|B3NzaC1kc3M)`)

// ipv6Candidate finds strings that may be IPv6 addresses; netip decides.
var ipv6Candidate = regexp.MustCompile(`(?i)\b[0-9a-f]{0,4}(?::[0-9a-f]{0,4}){2,7}\b`)

func TestRecordingsHoldNoSecret(t *testing.T) {
	ipv4 := regexp.MustCompile(`\b(\d{1,3}\.){3}\d{1,3}\b`)
	mac := regexp.MustCompile(`\b([0-9A-Fa-f]{2}:){5}[0-9A-Fa-f]{2}\b`)
	key := sshKeyRE
	token := regexp.MustCompile(`PLUMB_ENROLL_TOKEN(?:=|%3D|\\u003d)([A-Za-z0-9_-]+)`)
	doc4 := netip.MustParsePrefix("192.0.2.0/24")
	doc6 := netip.MustParsePrefix("2001:db8::/32")
	n := 0
	err := filepath.WalkDir("testdata/pve", func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		n++
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		s := string(b)
		for _, bad := range []string{"PVEAPIToken", "PVEAuthCookie", "CSRFPreventionToken", "BEGIN "} {
			if strings.Contains(s, bad) {
				t.Errorf("%s holds %q", p, bad)
			}
		}
		for _, a := range ipv4.FindAllString(s, -1) {
			if ip, err := netip.ParseAddr(a); err == nil && !doc4.Contains(ip) {
				t.Errorf("%s holds the IPv4 address %s", p, a)
			}
		}
		for _, c := range ipv6Candidate.FindAllString(s, -1) {
			if ip, err := netip.ParseAddr(c); err == nil && ip.Is6() && !doc6.Contains(ip) {
				t.Errorf("%s holds the IPv6 address %s", p, c)
			}
		}
		for _, m := range mac.FindAllString(s, -1) {
			if m != "BC:24:11:00:00:01" {
				t.Errorf("%s holds the MAC %s", p, m)
			}
		}
		for _, m := range token.FindAllStringSubmatch(s, -1) {
			if !regexp.MustCompile(`^scrubbed-[0-9]+$`).MatchString(m[1]) {
				t.Errorf("%s holds an enrollment token (env)", p)
			}
		}
		// A key body without its type in front (a scrub that ate the
		// "ssh-" of the next key) still starts with its base64 header.
		if keyBlob.MatchString(s) {
			t.Errorf("%s holds an SSH key body", p)
		}
		for _, k := range key.FindAllStringSubmatch(s, -1) {
			if k[3] != "AAAAtestkey" {
				t.Errorf("%s holds an SSH key", p)
			}
		}
		return nil
	})
	if err != nil || n == 0 {
		t.Fatalf("scan: %d files, %v", n, err)
	}
}

// Review S1 of #167: both keys of a create form are found, and the
// recorder's scrub leaves none of them.
func TestSSHKeyRegexpTwoKeys(t *testing.T) {
	form := "hostname=web1&ssh-public-keys=ssh-ed25519+AAAAC3NzaC1lZDI1NTE5AAAAIMHuvKdtDvFdfKZ91D5pB6tM%2Fc80ZiwpVeGJBZfWOygU+t079%0Assh-ed25519+AAAAC3NzaC1lZDI1NTE5AAAAIFij5Knm0cqjtziBGCk3P49yteRZKWJ1JuHOp03EVi%2BO+J%C3%BCrgen&swap=512"
	m := sshKeyRE.FindAllStringSubmatch(form, -1)
	if len(m) != 2 || !strings.HasPrefix(m[1][3], "AAAAC3NzaC1lZDI1NTE5AAAAIFij5") {
		t.Fatalf("matches %q", m)
	}
	out := sshKeyRE.ReplaceAllString(form, "$1${2}AAAAtestkey")
	if strings.Contains(out, "AAAAC3Nza") || strings.Count(out, "AAAAtestkey") != 2 {
		t.Errorf("scrubbed %q", out)
	}
	body := `"plumb-ssh-key: ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIFij5Knm0cqjtziBGCk3P49yteRZKWJ1JuHOp03EVi+O Jürgen\n"`
	if got := sshKeyRE.ReplaceAllString(body, "$1${2}AAAAtestkey"); got != `"plumb-ssh-key: ssh-ed25519 AAAAtestkey Jürgen\n"` {
		t.Errorf("body %q", got)
	}
}
