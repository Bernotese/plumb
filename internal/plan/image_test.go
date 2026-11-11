package plan

import (
	"regexp"
	"testing"
)

// The web offers Copy only for this form (T-064 review, safeFreeCommand).
var webSafe = regexp.MustCompile(`^pvesm free [A-Za-z][A-Za-z0-9._-]*:import/plumb-[a-z0-9][a-z0-9.-]*-[0-9a-f]{12}\.qcow2$`)

func TestFreeCommand(t *testing.T) {
	ok := map[string][2]string{
		"plain":       {"ubuntu-26.04", "plumb-images:import/plumb-ubuntu-26.04-0123456789ab.qcow2"},
		"storage dot": {"web", "img.store_1:import/plumb-web-ffffffffffff.qcow2"},
	}
	for name, c := range ok {
		got := FreeCommand(c[0], c[1])
		if got != "pvesm free "+c[1] || !webSafe.MatchString(got) {
			t.Errorf("%s: FreeCommand = %q", name, got)
		}
	}
	bad := map[string][2]string{
		"shell in file":     {"web", "plumb-images:import/plumb-web-$(reboot)-0123456789ab.qcow2"},
		"semicolon":         {"web", "plumb-images:import/plumb-web-0123456789ab.qcow2;rm -rf /"},
		"space":             {"web", "plumb-images:import/plumb-web 0123456789ab.qcow2"},
		"quote in storage":  {"web", "plumb'x:import/plumb-web-0123456789ab.qcow2"},
		"storage digit":     {"web", "1store:import/plumb-web-0123456789ab.qcow2"},
		"other image":       {"web", "plumb-images:import/plumb-webx-0123456789ab.qcow2"},
		"prefix of other":   {"web", "plumb-images:import/plumb-web-x-0123456789ab.qcow2"},
		"upper hex":         {"web", "plumb-images:import/plumb-web-0123456789AB.qcow2"},
		"short hex":         {"web", "plumb-images:import/plumb-web-0123456789a.qcow2"},
		"not import":        {"web", "plumb-images:iso/plumb-web-0123456789ab.qcow2"},
		"path":              {"web", "plumb-images:import/../plumb-web-0123456789ab.qcow2"},
		"not made by plumb": {"web", "plumb-images:import/debian-13-0123456789ab.qcow2"},
		"newline":           {"web", "plumb-images:import/plumb-web-0123456789ab.qcow2\npvesm free x"},
		"empty":             {"web", ""},
	}
	for name, c := range bad {
		if got := FreeCommand(c[0], c[1]); got != "" {
			t.Errorf("%s: FreeCommand = %q, want none", name, got)
		}
	}
}
