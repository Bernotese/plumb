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

// ipv6Candidate finds strings that may be IPv6 addresses; netip decides.
var ipv6Candidate = regexp.MustCompile(`(?i)\b[0-9a-f]{0,4}(?::[0-9a-f]{0,4}){2,7}\b`)

func TestRecordingsHoldNoSecret(t *testing.T) {
	ipv4 := regexp.MustCompile(`\b(\d{1,3}\.){3}\d{1,3}\b`)
	mac := regexp.MustCompile(`\b([0-9A-Fa-f]{2}:){5}[0-9A-Fa-f]{2}\b`)
	key := regexp.MustCompile(`(ssh-[a-z0-9-]+|ecdsa-[a-z0-9-]+)(%20|\s|\+)([A-Za-z0-9+/=%]+)`)
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
			if m[1] != "scrubbed" {
				t.Errorf("%s holds an enrollment token (env)", p)
			}
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
