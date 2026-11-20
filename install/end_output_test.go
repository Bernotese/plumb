package install

import (
	"strings"
	"testing"
)

// endBlock is the installer's last block: from "Plumb runs:" to the end.
func endBlock(t *testing.T, out string) string {
	t.Helper()
	i := strings.LastIndex(out, "\nPlumb runs: ")
	if i < 0 {
		t.Fatalf("no \"Plumb runs:\" line:\n%s", out)
	}
	return out[i+1:]
}

// T-270 O-5 (BRIEF 5.1): an install ends with the web UI address and the
// initial login. The first-start password is the only secret in that
// block; the token secret is never printed, and the recovery key only
// once, by plumb key init (D-180), before the block.
func TestInstallEndsWithAddressAndLogin(t *testing.T) {
	t.Parallel()
	h := newHost(t)
	code, out, errOut := h.run([]string{"PLUMB_INSTALL_ASSUME_ROOT=1"}, "--yes", "--plumb-binary", h.plumb)
	if code != 0 {
		t.Fatalf("install: %d\n%s\n%s", code, out, errOut)
	}
	want := "Plumb runs: https://192.0.2.50:8443 (certificate from the Plumb CA).\n" +
		"Initial login: user admin, password pw-from-journal (change it at the first login).\n"
	if got := endBlock(t, out); got != want {
		t.Errorf("end of install:\n%s\nwant:\n%s", got, want)
	}
	all := out + errOut
	for s, n := range map[string]int{fakeSecret: 0, "pw-from-journal": 1, "FAKERECOVERYKEY": 1} {
		if got := strings.Count(all, s); got != n {
			t.Errorf("%q printed %d times, want %d", s, got, n)
		}
	}
	if strings.Contains(endBlock(t, out), "FAKERECOVERYKEY") {
		t.Error("the recovery key is in the end block")
	}
}

// T-270 O-5: a restore ends with the web UI address and the login hint.
// The new admin password comes once, from plumb restore (shown once,
// D-290); the end block names no password, and neither the token secret
// nor the old first-start password is printed.
func TestRestoreEndsWithAddressAndLoginHint(t *testing.T) {
	t.Parallel()
	b, file := keptHost(t)
	code, out, errOut := b.restore(nil, file, "--lxc-ip", "192.0.2.50/24", "--lxc-gw", "192.0.2.1", "--backup-storage", "backups")
	if code != 0 {
		t.Fatalf("restore: %d\n%s\n%s", code, out, errOut)
	}
	want := "Plumb runs: https://192.0.2.50:8443 (certificate from the Plumb CA).\n" +
		"Restored from " + file + ". Log in as admin with the password that plumb restore showed. Plumb is in\n" +
		"restored mode: it observes, and changes nothing until you confirm that the old controller is off (D-290).\n"
	if got := endBlock(t, out); got != want {
		t.Errorf("end of restore:\n%s\nwant:\n%s", got, want)
	}
	notice := strings.Index(out, "plumb restore: it shows the NEW password of the user admin once.")
	pw := strings.Index(out, "Restored. User: admin  Password: NEWPW")
	if notice < 0 || pw < notice {
		t.Errorf("the one-time password does not follow its notice:\n%s", out)
	}
	all := out + errOut
	for s, n := range map[string]int{fakeSecret: 0, "pw-from-journal": 0, "FAKERECOVERYKEY": 0, "NEWPW": 1} {
		if got := strings.Count(all, s); got != n {
			t.Errorf("%q printed %d times, want %d", s, got, n)
		}
	}
}
