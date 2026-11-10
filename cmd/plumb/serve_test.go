package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

// D-039: --dev never touches a real host; without --dev, no plain HTTP
// and no fake driver.
func TestServeFlags(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string // "" = valid
	}{
		{[]string{"--dev"}, ""},
		{[]string{"--dev", "--driver", "fake"}, ""},
		{[]string{"--dev", "--driver", "proxmox"}, "fake driver only"},
		{[]string{"--plain-http"}, "plain HTTP is refused"},
		{[]string{"--driver", "fake"}, "for --dev only"},
		{[]string{"--driver", "vmware"}, "unknown driver"},
		{[]string{}, ""},
	} {
		f, err := parseServe(tc.args, &bytes.Buffer{})
		switch {
		case tc.want == "" && err != nil:
			t.Errorf("%v: %v", tc.args, err)
		case tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)):
			t.Errorf("%v: got %v, want %q", tc.args, err, tc.want)
		}
		if err == nil && f.dev && (!f.plainHTTP || f.driver != "fake") {
			t.Errorf("%v: dev mode = %+v", tc.args, f)
		}
		if err == nil && !f.dev && (f.plainHTTP || f.driver != "proxmox") {
			t.Errorf("%v: production mode = %+v", tc.args, f)
		}
	}
	// Without --dev the controller refuses to start until the Proxmox
	// driver exists (T-012); it never falls back to the fake.
	var out, errOut bytes.Buffer
	if code := serve(context.Background(), []string{"--data", t.TempDir()}, &out, &errOut); code != 1 || !strings.Contains(errOut.String(), "Proxmox driver") {
		t.Errorf("serve without --dev: %d %q", code, errOut.String())
	}
	if code := serve(context.Background(), []string{"--dev", "--driver", "proxmox"}, &out, &errOut); code != 2 {
		t.Errorf("serve --dev --driver proxmox: %d", code)
	}
}
