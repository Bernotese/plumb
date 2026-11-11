package state

import (
	"strings"
	"testing"
)

// TestAllowRestart: spec.allow_restart (D-194) defaults to false, renders
// into Rendered.AllowRestart, and is refused inside guest and overrides.
func TestAllowRestart(t *testing.T) {
	render := func(body string) *Rendered {
		t.Helper()
		set, err := Validate(fixture(target(body)))
		if err != nil {
			t.Fatal(err)
		}
		r, err := set.Render("t1")
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	if r := render(`"blocks":[]`); r.AllowRestart {
		t.Error("default is not false")
	}
	if r := render(`"allow_restart":true`); !r.AllowRestart {
		t.Error("allow_restart true is not rendered")
	}
	if r := render(`"allow_restart":false`); r.AllowRestart {
		t.Error("allow_restart false is rendered as true")
	}
	if _, err := Validate(fixture(target(`"allow_restart":"yes"`))); err == nil {
		t.Error("a string is accepted")
	}
	// Not a guest setting: not in guest, not in an override of guest:config.
	inGuest := doc(KindTarget, "t1", `{"type":"vm","image":"img","guest":{"cores":1,"memory":512,"disk":4,"bridge":"vmbr0","allow_restart":true}}`)
	if _, err := Validate(fixture(inGuest)); err == nil || !strings.Contains(err.Error(), "allow_restart") {
		t.Errorf("allow_restart inside guest: %v", err)
	}
	if _, err := Validate(fixture(target(`"overrides":{"guest:config":{"allow_restart":true}}`))); err == nil {
		t.Error("allow_restart as an override of guest:config is accepted")
	}
}
