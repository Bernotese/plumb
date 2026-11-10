package resource

import (
	"context"
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Bernotese/plumb/internal/wire"
)

const secret = "db_password=Tr0ub4dor&3-very-secret"

// forbidden lists every form of the secret that must not leave the guest.
func forbidden(values ...string) []string {
	var out []string
	for _, v := range values {
		s256 := sha256.Sum256([]byte(v))
		s512 := sha512.Sum512([]byte(v))
		s1 := sha1.Sum([]byte(v))
		m5 := md5.Sum([]byte(v))
		out = append(out, v,
			hex.EncodeToString(s256[:]), hex.EncodeToString(s512[:]), hex.EncodeToString(s1[:]), hex.EncodeToString(m5[:]),
			base64.StdEncoding.EncodeToString(s256[:]), base64.StdEncoding.EncodeToString([]byte(v)))
	}
	return out
}

func assertClean(t *testing.T, what string, v any, values ...string) {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range forbidden(values...) {
		if strings.Contains(string(b), f) {
			t.Fatalf("%s holds the secret or a hash of it (%q):\n%s", what, f, b)
		}
	}
}

func TestSensitiveFileContent(t *testing.T) {
	g := newFakeGuest(t)
	const old = "db_password=an-older-secret-value"
	content := secret + "\n"
	r := res("file", "/etc/app.conf", map[string]any{"content": content, "mode": "0600"}, "content")
	rs := build(t, r, g.env())
	ctx := context.Background()

	check := func(stage string, wantChange bool) []wire.Change {
		t.Helper()
		changes := plan(t, rs)
		var found bool
		for _, c := range changes {
			if c.Attr != "content" {
				continue
			}
			found = true
			if !c.Sensitive || c.From != nil || c.To != nil || !c.Risk.Valid() {
				t.Fatalf("%s: sensitive change = %+v", stage, c)
			}
		}
		if found != wantChange {
			t.Fatalf("%s: content change = %v, want %v (%+v)", stage, found, wantChange, changes)
		}
		// A full report as the agent sends it, including Output.
		report := wire.Report{Type: wire.ReportResult, Items: []wire.Item{{
			Resource: r.ID,
			Status:   wire.StatusDiffers,
			Changes:  changes,
			Output:   SafeOutput(r, "apply failed near: "+content+" and "+old),
		}}}
		assertClean(t, stage+" report", report, secret, content)
		return changes
	}

	// Create: the value is compared locally and not reported.
	changes := check("create", true)
	if err := rs.Apply(ctx, changes); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(g.root, "etc/app.conf")); string(b) != content {
		t.Fatalf("Apply did not write the desired value: %q", b)
	}
	check("converged", false)

	// Drift: the old value in the guest is hidden, too.
	mustWrite(t, filepath.Join(g.root, "etc/app.conf"), old+"\n")
	if err := os.Chmod(filepath.Join(g.root, "etc/app.conf"), 0o600); err != nil {
		t.Fatal(err)
	}
	changes = check("drift", true)
	assertClean(t, "drift changes", changes, old, old+"\n")
	if err := rs.Apply(ctx, changes); err != nil {
		t.Fatal(err)
	}
	check("reverted", false)
}

func TestSafeOutput(t *testing.T) {
	r := res("user", "deploy", map[string]any{
		"ssh_keys": []any{keyA},
		"shell":    "/bin/bash",
	}, "ssh_keys")
	multi := res("file", "/etc/x", map[string]any{"content": "line-one-secret\nline-two-secret\n"}, "content")

	tests := []struct {
		name string
		r    wire.Resource
		in   string
		want string
	}{
		{"list value", r, "usermod: bad key " + keyA, "usermod: bad key [redacted]"},
		{"not sensitive stays", r, "shell /bin/bash", "shell /bin/bash"},
		{"each line of a multi-line value", multi, "got line-two-secret at 2", "got [redacted] at 2"},
		{"full multi-line value", multi, "x line-one-secret\nline-two-secret\n y", "x [redacted] y"},
		{"no sensitive attributes", res("file", "/etc/y", map[string]any{"content": "a"}), "a b", "a b"},
		{"empty value is ignored", res("file", "/etc/z", map[string]any{"content": ""}, "content"), "abc", "abc"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := SafeOutput(tt.r, tt.in); got != tt.want {
				t.Fatalf("SafeOutput = %q, want %q", got, tt.want)
			}
		})
	}

	long := SafeOutput(r, strings.Repeat("é", MaxOutput))
	if len(long) > MaxOutput+len("…") || !strings.HasSuffix(long, "…") {
		t.Fatalf("long output: len %d", len(long))
	}
	if !json.Valid([]byte(fmt.Sprintf("%q", long))) {
		t.Fatal("cut output is not valid text")
	}
}
