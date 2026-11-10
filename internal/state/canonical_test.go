package state

import (
	"strings"
	"testing"
)

// T-003 re-review: number spelling must not be a document change.
func TestCanonicalNumbers(t *testing.T) {
	body := func(num string) string {
		b, err := Canonical(Doc{Kind: KindTarget, Name: "n", Spec: []byte(`{"memory":` + num + `}`)})
		if err != nil {
			t.Fatalf("%s: %v", num, err)
		}
		return string(b)
	}
	for want, spellings := range map[string][]string{
		"2048":                           {"2048", "2048.0", "2048.000", "2.048e3", "2.048E+3", "20480e-1", "2048e0"},
		"0":                              {"0", "-0", "0.0", "-0.0", "0e5", "0E-5"},
		"-7":                             {"-7", "-7.0", "-0.7e1"},
		"0.5":                            {"0.5", "0.50", "5e-1", "0.05e1", "50E-2"},
		"-12.25":                         {"-12.25", "-1225e-2", "-12.250"},
		"0.000001":                       {"1e-6", "0.000001", "0.0000010"},
		"12345678901234567890":           {"12345678901234567890", "1.2345678901234567890e19"},
		"0.1":                            {"0.1", "0.10000000000000000000000000000000"},
		"100000000000000000000000000000": {"1e29"},
	} {
		w := `{"kind":"Target","name":"n","spec":{"memory":` + want + `}}`
		for _, s := range spellings {
			if got := body(s); got != w {
				t.Errorf("%s: got %s, want %s", s, got, w)
			}
		}
	}
	// Exact values: no float64 rounding.
	if got := body("0.1000000000000000055511151231257827"); !strings.Contains(got, "0.1000000000000000055511151231257827") {
		t.Errorf("rounded: %s", got)
	}
	// Out of range and overlong numbers are errors, not slow work.
	for _, bad := range []string{"1e101", "1e-101", "1e999999999", "1" + strings.Repeat("0", 100)} {
		if _, err := Canonical(Doc{Kind: KindTarget, Name: "n", Spec: []byte(`{"x":` + bad + `}`)}); err == nil {
			t.Errorf("%s: want an error", bad)
		}
	}
	// Numbers inside arrays and nested objects are normalised too.
	got, err := Canonical(Doc{Kind: KindBlock, Name: "n", Spec: []byte(`{"a":[1.0,{"b":2.50}]}`)})
	if err != nil || string(got) != `{"kind":"Block","name":"n","spec":{"a":[1,{"b":2.5}]}}` {
		t.Errorf("nested: %s %v", got, err)
	}
}

func TestCheckCanonical(t *testing.T) {
	good := []byte(`{"kind":"Block","name":"n","spec":{"a":1}}`)
	if _, err := CheckCanonical(good); err != nil {
		t.Errorf("canonical body rejected: %v", err)
	}
	for _, bad := range []string{
		`{"kind":"Block","name":"n","spec":{"a":1.0}}`,
		`{"name":"n","kind":"Block","spec":{"a":1}}`,
		`{"kind":"Block","name":"n","spec":{"a": 1}}`,
		`{"kind":"Block","name":"n","spec":{"a":1}}` + "\n",
		`{"kind":"Block","name":"n","spec":{"a":"` + "\\" + `u003c"}}`, // escaped "<"
	} {
		if _, err := CheckCanonical([]byte(bad)); err == nil {
			t.Errorf("%s: want an error", bad)
		}
	}
}
