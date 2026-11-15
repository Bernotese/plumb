package state

import (
	"strings"
	"testing"
)

// T-217: the planned slice 7 fields pass the schema but are refused with
// a clear message until their card serves them.
func TestPlannedFieldsRefused(t *testing.T) {
	img := Doc{Kind: KindImage, Name: "deb", Spec: []byte(`{"type":"vm","url":"https://example.invalid/i.img","sha256":"` + strings.Repeat("0", 64) + `"}`)}
	// Positive control: the base documents are valid without the new fields.
	if _, err := Validate([]Doc{img, {Kind: KindTarget, Name: "web1", Spec: []byte(`{"type":"vm","image":"deb","guest":{"cores":1,"memory":512,"disk":4,"bridge":"vmbr0"}}`)}}); err != nil {
		t.Fatalf("base: %v", err)
	}
	// T-220 serves the address of a VM NIC (the agent's network resource).
	for _, spec := range []string{
		`{"type":"vm","image":"deb","guest":{"cores":1,"memory":512,"disk":4,"bridge":"vmbr0","nics":[{"bridge":"vmbr9"},{"bridge":"vmbr9","tag":20,"ip":"10.9.20.5/24"}]}}`,
		`{"type":"vm","image":"deb","guest":{"cores":1,"memory":512,"disk":4,"bridge":"vmbr0","nics":[{"bridge":"vmbr9","ip":"10.9.20.5/24","gateway":"10.9.20.1"}]}}`,
		`{"type":"vm","image":"deb","guest":{"cores":1,"memory":512,"disk":4,"bridge":"vmbr0","nics":[{"bridge":"vmbr9","ip":"dhcp"}]}}`,
	} {
		if _, err := Validate([]Doc{img, {Kind: KindTarget, Name: "web1", Spec: []byte(spec)}}); err != nil {
			t.Errorf("VM NIC address: %v", err)
		}
	}
	ctImg := Doc{Kind: KindImage, Name: "ct", Spec: []byte(`{"type":"lxc","url":"http://download.proxmox.com/a.tar.zst","sha512":"` + strings.Repeat("0", 128) + `"}`)}
	if _, err := Validate([]Doc{ctImg, {Kind: KindTarget, Name: "web2", Spec: []byte(`{"type":"lxc","image":"ct","guest":{"cores":1,"memory":512,"disk":4,"bridge":"vmbr0","nics":[{"bridge":"vmbr9","tag":20,"ip":"10.9.20.5/24","gateway":"10.9.20.1"}]}}`)}}); err != nil {
		t.Errorf("container NIC address: %v", err)
	}
	// T-219 serves guest.nics: no longer refused.
	if _, err := Validate([]Doc{img, {Kind: KindTarget, Name: "web1", Spec: []byte(`{"type":"vm","image":"deb","guest":{"cores":1,"memory":512,"disk":4,"bridge":"vmbr0","nics":[{"bridge":"vmbr9","tag":20}]}}`)}}); err != nil {
		t.Errorf("nics: %v", err)
	}
	// T-221 serves backup: no longer refused.
	if _, err := Validate([]Doc{img, {Kind: KindTarget, Name: "web1", Spec: []byte(`{"type":"vm","image":"deb","guest":{"cores":1,"memory":512,"disk":4,"bridge":"vmbr0"},"backup":{"schedule":"daily","time":"03:00","keep":7}}`)}}); err != nil {
		t.Errorf("backup: %v", err)
	}
	// The schema itself: a weekday with daily, a 4th extra NIC and a tag of 4095 are refused.
	for name, spec := range map[string]string{
		"weekday with daily": `{"type":"vm","image":"deb","guest":{"cores":1,"memory":512,"disk":4,"bridge":"vmbr0"},"backup":{"schedule":"daily","weekday":"mon","time":"03:00","keep":7}}`,
		"weekly without day": `{"type":"vm","image":"deb","guest":{"cores":1,"memory":512,"disk":4,"bridge":"vmbr0"},"backup":{"schedule":"weekly","time":"03:00","keep":7}}`,
		"keep 31":            `{"type":"vm","image":"deb","guest":{"cores":1,"memory":512,"disk":4,"bridge":"vmbr0"},"backup":{"schedule":"daily","time":"03:00","keep":31}}`,
		"4 nics":             `{"type":"vm","image":"deb","guest":{"cores":1,"memory":512,"disk":4,"bridge":"vmbr0","nics":[{"bridge":"a"},{"bridge":"b"},{"bridge":"c"},{"bridge":"d"}]}}`,
		"tag 4095":           `{"type":"vm","image":"deb","guest":{"cores":1,"memory":512,"disk":4,"bridge":"vmbr0","nics":[{"bridge":"vmbr9","tag":4095}]}}`,
		"gateway with dhcp":  `{"type":"vm","image":"deb","guest":{"cores":1,"memory":512,"disk":4,"bridge":"vmbr0","nics":[{"bridge":"vmbr9","ip":"dhcp","gateway":"10.0.0.1"}]}}`,
		"trunks":             `{"type":"vm","image":"deb","guest":{"cores":1,"memory":512,"disk":4,"bridge":"vmbr0","nics":[{"bridge":"vmbr9","trunks":"20;30"}]}}`,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := Validate([]Doc{img, {Kind: KindTarget, Name: "web1", Spec: []byte(spec)}})
			if err == nil || strings.Contains(err.Error(), "planned") {
				t.Fatalf("the schema must refuse it: %v", err)
			}
		})
	}
}

// T-237, T-238: guest.disks passes the schema and is served (the planned
// guard is gone); the schema itself refuses the shapes that must never
// reach the driver (a 4th disk, a bad slot or size, a storage, mount
// options, a host path that is not absolute).
func TestPlannedDisks(t *testing.T) {
	img := Doc{Kind: KindImage, Name: "deb", Spec: []byte(`{"type":"vm","url":"https://example.invalid/i.img","sha256":"` + strings.Repeat("0", 64) + `"}`)}
	ctImg := Doc{Kind: KindImage, Name: "ct", Spec: []byte(`{"type":"lxc","url":"http://download.proxmox.com/a.tar.zst","sha512":"` + strings.Repeat("0", 128) + `"}`)}
	target := func(typ, disks string) Doc {
		image := "deb"
		if typ == "lxc" {
			image = "ct"
		}
		return Doc{Kind: KindTarget, Name: "web1", Spec: []byte(`{"type":"` + typ + `","image":"` + image + `","guest":{"cores":1,"memory":512,"disk":4,"bridge":"vmbr0","disks":` + disks + `}}`)}
	}
	for name, d := range map[string]Doc{
		"vm":  target("vm", `[{"slot":1,"size_gb":20},{"slot":3,"size_gb":4096}]`),
		"lxc": target("lxc", `[{"slot":2,"size_gb":8,"path":"/srv/data"}]`),
	} {
		t.Run("served "+name, func(t *testing.T) {
			if _, err := Validate([]Doc{img, ctImg, d}); err != nil {
				t.Fatalf("guest.disks is served (T-238): %v", err)
			}
		})
	}
	for name, disks := range map[string]string{
		"4 disks":        `[{"slot":1,"size_gb":1},{"slot":2,"size_gb":1},{"slot":3,"size_gb":1},{"slot":1,"size_gb":1}]`,
		"no slot":        `[{"size_gb":20}]`,
		"slot 0":         `[{"slot":0,"size_gb":20}]`,
		"slot 4":         `[{"slot":4,"size_gb":20}]`,
		"no size":        `[{"slot":1}]`,
		"size 0":         `[{"slot":1,"size_gb":0}]`,
		"size 4097":      `[{"slot":1,"size_gb":4097}]`,
		"storage":        `[{"slot":1,"size_gb":20,"storage":"local"}]`,
		"mount options":  `[{"slot":1,"size_gb":20,"path":"/srv/a","mountoptions":"noatime"}]`,
		"read only":      `[{"slot":1,"size_gb":20,"path":"/srv/a","ro":true}]`,
		"root path":      `[{"slot":1,"size_gb":20,"path":"/"}]`,
		"relative path":  `[{"slot":1,"size_gb":20,"path":"srv/a"}]`,
		"trailing slash": `[{"slot":1,"size_gb":20,"path":"/srv/a/"}]`,
		"double slash":   `[{"slot":1,"size_gb":20,"path":"/srv//a"}]`,
		"host path":      `[{"slot":1,"size_gb":20,"path":"/srv/a,mp=/etc"}]`,
		"not a list":     `{"slot":1,"size_gb":20}`,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := Validate([]Doc{img, ctImg, target("lxc", disks)})
			if err == nil || strings.Contains(err.Error(), "planned") {
				t.Fatalf("the schema must refuse it: %v", err)
			}
		})
	}
}
