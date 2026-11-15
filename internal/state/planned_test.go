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
	// T-219: a VM NIC address waits for T-220; a container NIC has it.
	for _, c := range []struct{ spec, path string }{
		{`{"type":"vm","image":"deb","guest":{"cores":1,"memory":512,"disk":4,"bridge":"vmbr0","nics":[{"bridge":"vmbr9"},{"bridge":"vmbr9","tag":20,"ip":"10.9.20.5/24"}]}}`, "/spec/guest/nics/1/ip"},
		{`{"type":"vm","image":"deb","guest":{"cores":1,"memory":512,"disk":4,"bridge":"vmbr0","nics":[{"bridge":"vmbr9","ip":"10.9.20.5/24","gateway":"10.9.20.1"}]}}`, "/spec/guest/nics/0/gateway"},
	} {
		if _, err := Validate([]Doc{img, {Kind: KindTarget, Name: "web1", Spec: []byte(c.spec)}}); err == nil || !strings.Contains(err.Error(), c.path) || !strings.Contains(err.Error(), "T-220") {
			t.Errorf("VM NIC address: want a planned refusal at %s, got %v", c.path, err)
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
	for name, c := range map[string]struct{ spec, path string }{
		"backup": {`{"type":"vm","image":"deb","guest":{"cores":1,"memory":512,"disk":4,"bridge":"vmbr0"},"backup":{"schedule":"daily","time":"03:00","keep":7}}`, "/spec/backup"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := Validate([]Doc{img, {Kind: KindTarget, Name: "web1", Spec: []byte(c.spec)}})
			if err == nil || !strings.Contains(err.Error(), c.path) || !strings.Contains(err.Error(), "planned") {
				t.Fatalf("want a planned refusal at %s, got %v", c.path, err)
			}
		})
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
