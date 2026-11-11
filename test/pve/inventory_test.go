//go:build pve

package pve_test

import (
	"fmt"
	"sort"
	"strings"
	"testing"
)

func inventory(s map[int]string) []string {
	out := []string{}
	for _, n := range sortedIDs(s) {
		kind := "guest"
		if strings.HasSuffix(s[n], " template=1") {
			kind = "template"
		}
		out = append(out, fmt.Sprintf("%d %s", n, kind))
	}
	return out
}

// importFiles lists the import files of the image storage (read-only).
func importFiles(t *testing.T) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	for _, line := range strings.Split(hostRead(t, 9000, "imports"), "\n") {
		f := strings.Fields(line)
		if len(f) == 0 || !strings.Contains(f[0], ":import/") {
			continue
		}
		out[f[0][strings.Index(f[0], ":import/")+len(":import/"):]] = true
	}
	return out
}

// checkImports accepts as new import files exactly the pinned test images
// of this run (kept for reuse); any other new file fails the test.
func checkImports(t *testing.T, before, allowed map[string]bool) {
	t.Helper()
	var kept, foreign []string
	for f := range importFiles(t) {
		switch {
		case before[f]:
		case allowed[f]:
			kept = append(kept, f)
		default:
			foreign = append(foreign, f)
		}
	}
	sort.Strings(kept)
	t.Logf("import files kept for reuse: %v", kept)
	if len(foreign) > 0 {
		t.Errorf("unexpected new import files: %d", len(foreign))
	}
}
