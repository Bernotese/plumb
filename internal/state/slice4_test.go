package state

import "testing"

// TestSlice4GuestSchema: guest.power and guest.tags of the T-111 contract
// (D-291, D-299). The behaviour (grow only, stop needs allow_restart) is
// code of T-115; this test fixes the shape only.
func TestSlice4GuestSchema(t *testing.T) {
	g := func(extra string) Doc {
		return doc(KindTarget, "t1", `{"type":"vm","image":"img","guest":{"cores":1,"memory":512,"disk":4,"bridge":"vmbr0"`+extra+`}}`)
	}
	ok := []string{
		``,
		`,"power":"running"`, // "stopped" needs allow_restart: TestStopNeedsAllowRestart
		`,"tags":[]`,
		`,"tags":["web","prod","k8s_node-1.2+x"]`,
	}
	for _, e := range ok {
		if _, err := Validate(fixture(g(e))); err != nil {
			t.Errorf("%q: %v", e, err)
		}
	}
	bad := []string{
		`,"power":"paused"`,
		`,"power":true`,
		`,"tags":["Prod"]`,
		`,"tags":["a b"]`,
		`,"tags":["a;b"]`,
		`,"tags":["-x"]`,
		`,"tags":["web","web"]`,
		`,"tags":"web"`,
	}
	for _, e := range bad {
		if _, err := Validate(fixture(g(e))); err == nil {
			t.Errorf("%q: accepted", e)
		}
	}
}

// T-115 (D-291): power: stopped needs allow_restart, also by an override.
func TestStopNeedsAllowRestart(t *testing.T) {
	tgt := func(extra string) Doc {
		return doc(KindTarget, "t1", `{"type":"vm","image":"img","guest":{"cores":1,"memory":512,"disk":4,"bridge":"vmbr0"`+extra)
	}
	for _, tc := range []struct {
		extra string
		ok    bool
	}{
		{`,"power":"stopped"}}`, false},
		{`,"power":"stopped"},"allow_restart":true}`, true},
		{`,"power":"running"}}`, true},
		{`},"overrides":{"guest:config":{"power":"stopped"}}}`, false},
		{`},"allow_restart":true,"overrides":{"guest:config":{"power":"stopped"}}}`, true},
	} {
		_, err := Validate(fixture(tgt(tc.extra)))
		if (err == nil) != tc.ok {
			t.Errorf("%s: %v", tc.extra, err)
		}
	}
}
