package resource

import (
	"context"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"

	"github.com/Bernotese/plumb/internal/wire"
)

type groupRunner func(context.Context, string, ...string) ([]byte, error)

func (f groupRunner) Run(ctx context.Context, n string, a ...string) ([]byte, error) {
	return f(ctx, n, a...)
}

func TestGroupGuards(t *testing.T) {
	for _, tc := range []struct {
		name            string
		gid, want       int
		primary, absent bool
		risk            wire.Risk
	}{
		{"update", 1500, 1600, false, false, wire.RiskDisruptive},
		{"primary update", 1500, 1600, true, false, wire.RiskDestructive},
		{"system source", 999, 1600, false, false, wire.RiskDisruptive},
		{"system destination", 1500, 999, false, false, wire.RiskDisruptive},
		{"system removal", 999, 0, false, true, wire.RiskDestructive},
		{"primary removal", 1500, 0, true, true, wire.RiskDestructive},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := guestRoot(t)
			mustWrite(t, filepath.Join(root, "etc/group"), "team:x:"+strconv.Itoa(tc.gid)+":\n")
			if tc.primary {
				mustWrite(t, filepath.Join(root, "etc/passwd"), "person:x:1500:"+strconv.Itoa(tc.gid)+":Person:/home/person:/bin/sh\n")
			}
			calls := 0
			env := NewEnv(root, groupRunner(func(context.Context, string, ...string) ([]byte, error) { calls++; return nil, nil }))
			attrs := map[string]any{"name": "team", "gid": tc.want}
			if tc.want == 0 {
				delete(attrs, "gid")
			}
			if tc.absent {
				attrs["state"] = wire.StateAbsent
			}
			rs, err := New(wire.Resource{ID: "group:team", Type: "group", Attrs: attrs}, env)
			if err != nil {
				t.Fatal(err)
			}
			obs, err := rs.Observe(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			changes := rs.Diff(obs)
			if len(changes) != 1 || changes[0].Risk != tc.risk {
				t.Fatalf("changes: %+v", changes)
			}
			err = rs.Apply(context.Background(), changes)
			blocked := tc.gid < 1000 || tc.want > 0 && tc.want < 1000 || tc.absent && tc.primary
			if blocked && (err == nil || calls != 0) {
				t.Fatalf("guard: err=%v calls=%d", err, calls)
			}
			if !blocked && (err != nil || calls != 1) {
				t.Fatalf("apply: err=%v calls=%d", err, calls)
			}
		})
	}
}

func TestGroupCreateChanges(t *testing.T) {
	for _, attrs := range []map[string]any{
		{"name": "team"}, {"name": "team", "gid": 1500},
		{"name": "team", "system": true}, {"name": "team", "gid": 1500, "system": false},
	} {
		rs, err := New(wire.Resource{ID: "group:team", Type: "group", Attrs: attrs}, NewEnv(guestRoot(t), groupRunner(func(context.Context, string, ...string) ([]byte, error) {
			t.Fatal("create planning must not run a command")
			return nil, nil
		})))
		if err != nil {
			t.Fatal(err)
		}
		obs, err := rs.Observe(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		want := []wire.Change{change(wire.AttrState, wire.StateAbsent, wire.StatePresent, wire.RiskReversible)}
		for _, key := range []string{"gid", "system"} {
			if value, set := attrs[key]; set {
				want = append(want, change(key, nil, value, wire.RiskReversible))
			}
		}
		if got := rs.Diff(obs); !reflect.DeepEqual(got, want) {
			t.Fatalf("attrs=%v: changes=%+v, want=%+v", attrs, got, want)
		}
	}
}
