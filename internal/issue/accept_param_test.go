package issue

import (
	"errors"
	"testing"

	"github.com/Bernotese/plumb/internal/state"
	"github.com/Bernotese/plumb/internal/wire"
)

func TestAcceptModeKinds(t *testing.T) {
	ch := []wire.Change{{Attr: "memory", From: 1024}}
	for _, c := range []struct {
		resource string
		allowed  bool
		want     string
	}{
		{wire.GuestConfigID, true, state.AcceptGuest},
		{wire.GuestConfigID, false, state.AcceptNone},
		{"firewall:main", false, state.AcceptNone},
		{"agent:binary", true, state.AcceptNone},
		{"file:/etc/motd", true, state.AcceptRaw}, // no set: raw, never a guess
	} {
		if got := AcceptMode(nil, "t1", c.resource, ch, c.allowed); got.Mode != c.want || len(got.Params) != 0 {
			t.Errorf("%s allowed=%v: %+v, want %s", c.resource, c.allowed, got, c.want)
		}
	}
}

func TestSetParams(t *testing.T) {
	spec := map[string]any{"blocks": []any{
		map[string]any{"use": "pair"},
		map[string]any{"use": "greet", "id": "g", "params": map[string]any{"host": "old", "n": 3}},
	}}
	if err := setParams(spec, []state.ParamValue{{Use: "g", Index: 1, Param: "host", Value: "new"}}); err != nil {
		t.Fatal(err)
	}
	p := spec["blocks"].([]any)[1].(map[string]any)["params"].(map[string]any)
	if p["host"] != "new" || p["n"] != 3 {
		t.Fatalf("%v", p)
	}
	if err := setParams(spec, []state.ParamValue{{Use: "pair", Index: 0, Param: "u", Value: "x"}}); err != nil {
		t.Fatal(err)
	}
	if spec["blocks"].([]any)[0].(map[string]any)["params"].(map[string]any)["u"] != "x" {
		t.Fatal("params not made")
	}
	for _, bad := range []state.ParamValue{{Use: "g", Index: 0, Param: "u"}, {Use: "g", Index: 5, Param: "u"}, {Use: "g", Index: -1}} {
		if err := setParams(spec, []state.ParamValue{bad}); !errors.Is(err, errParamUse) {
			t.Errorf("%+v: %v", bad, err)
		}
	}
}
