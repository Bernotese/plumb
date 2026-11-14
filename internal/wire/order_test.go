package wire

import (
	"encoding/json"
	"errors"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func res(id, block string, after ...string) Resource {
	typ, _, _ := SplitID(id)
	return Resource{ID: id, Type: typ, Block: block, Attrs: map[string]any{}, After: after}
}

func svc(id, block string, restartOn ...any) Resource {
	r := res(id, block)
	r.Attrs["restart_on"] = restartOn
	return r
}

// nginxSites is the list that T-044 is about: the nginx Block and two
// nginx-site uses, as state.Render gives it (D-172, D-173). sitesFirst
// puts the site uses before the nginx use.
func nginxSites(sitesFirst bool) []Resource {
	nginx := []Resource{
		res("package:nginx", "nginx"),
		res("file:/etc/nginx/sites-available/default", "nginx"),
		res("symlink:/etc/nginx/sites-enabled/default", "nginx"),
		svc("service:nginx", "nginx",
			"file:/etc/nginx/sites-available/default", "symlink:/etc/nginx/sites-enabled/default",
			"file:/etc/nginx/sites-available/a", "symlink:/etc/nginx/sites-enabled/a",
			"file:/etc/nginx/sites-available/b", "symlink:/etc/nginx/sites-enabled/b"),
	}
	sites := []Resource{
		res("file:/etc/nginx/sites-available/a", "site-a", "package:nginx"),
		res("symlink:/etc/nginx/sites-enabled/a", "site-a"),
		res("file:/etc/nginx/sites-available/b", "site-b", "package:nginx"),
		res("symlink:/etc/nginx/sites-enabled/b", "site-b"),
	}
	if sitesFirst {
		return append(sites, nginx...)
	}
	return append(nginx, sites...)
}

// waits returns the edges of one resource as "kind→id", in edge order.
func waits(t *testing.T, list []Resource, edges [][]Edge, id string) []string {
	t.Helper()
	for i, r := range list {
		if r.ID != id {
			continue
		}
		out := []string{}
		for _, e := range edges[i] {
			out = append(out, string(e.Kind)+"→"+list[e.To].ID)
		}
		return out
	}
	t.Fatalf("no resource %s", id)
	return nil
}

func TestOrderNginxSites(t *testing.T) {
	for _, sitesFirst := range []bool{false, true} {
		list := nginxSites(sitesFirst)
		edges, err := Order(list)
		if err != nil {
			t.Fatalf("sitesFirst=%v: %v", sitesFirst, err)
		}
		if len(edges) != len(list) {
			t.Fatalf("sitesFirst=%v: %d edge lists for %d resources", sitesFirst, len(edges), len(list))
		}
		want := map[string][]string{
			"package:nginx": {},
			"file:/etc/nginx/sites-available/default": {"block→package:nginx"},
			// The site waits for the package only, not for the service:
			// that is why after names a resource and not a use (D-167).
			"file:/etc/nginx/sites-available/a":  {"after→package:nginx"},
			"symlink:/etc/nginx/sites-enabled/a": {"block→file:/etc/nginx/sites-available/a"},
			"file:/etc/nginx/sites-available/b":  {"after→package:nginx"},
			"service:nginx": {
				"block→symlink:/etc/nginx/sites-enabled/default",
				"restart_on→file:/etc/nginx/sites-available/default",
				// Same pair as the block edge, other kind: both stay.
				"restart_on→symlink:/etc/nginx/sites-enabled/default",
				"restart_on→file:/etc/nginx/sites-available/a",
				"restart_on→symlink:/etc/nginx/sites-enabled/a",
				"restart_on→file:/etc/nginx/sites-available/b",
				"restart_on→symlink:/etc/nginx/sites-enabled/b",
			},
		}
		for id, w := range want {
			if got := waits(t, list, edges, id); !reflect.DeepEqual(got, w) {
				t.Errorf("sitesFirst=%v: %s waits for %v, want %v", sitesFirst, id, got, w)
			}
		}
	}
}

func TestOrderEdges(t *testing.T) {
	list := []Resource{
		res("file:/a", "one"),
		res("file:/b", "two"),
		// A service that is first in its use and has after: the after edge
		// must not look like a restart_on edge (review S1).
		func() Resource {
			r := svc("service:x", "three", "file:/b", "file:/not-here", "service:x", 7)
			r.After = []string{"file:/a", "file:/a"}
			return r
		}(),
		// restart_on of a resource that is not a service gives no edge.
		func() Resource {
			r := res("file:/c", "three")
			r.Attrs["restart_on"] = []any{"file:/a"}
			return r
		}(),
	}
	edges, err := Order(list)
	if err != nil {
		t.Fatal(err)
	}
	for id, w := range map[string][]string{
		"file:/a":   {},
		"file:/b":   {},
		"service:x": {"restart_on→file:/b", "after→file:/a"},
		"file:/c":   {"block→service:x"},
	} {
		if got := waits(t, list, edges, id); !reflect.DeepEqual(got, w) {
			t.Errorf("%s waits for %v, want %v", id, got, w)
		}
	}
	if edges, err := Order(nil); err != nil || len(edges) != 0 {
		t.Errorf("Order(nil) = %v, %v", edges, err)
	}
}

func TestOrderErrors(t *testing.T) {
	for _, tc := range []struct {
		name   string
		list   []Resource
		reason string
		ids    []string
		text   string
	}{
		{"duplicate id",
			[]Resource{res("file:/a", "one"), res("file:/a", "two")},
			OrderDuplicate, []string{"file:/a"}, "resource file:/a is listed twice"},
		{"unknown after id",
			[]Resource{res("file:/a", "one", "package:nginx")},
			OrderUnknown, []string{"file:/a", "package:nginx"},
			`file:/a (use "one") comes after package:nginx, but package:nginx is not in the resource list`},
		{"after itself",
			[]Resource{res("file:/a", "one", "file:/a")},
			OrderCycle, []string{"file:/a"},
			`apply order has a cycle: file:/a (use "one") waits for itself; check after, restarts, restart_on and the order of the resources in each Block`},
		{"after and after",
			[]Resource{res("file:/a", "one", "file:/b"), res("file:/b", "two", "file:/a")},
			OrderCycle, []string{"file:/a", "file:/b"},
			`apply order has a cycle: file:/a (use "one") waits for file:/b (use "two"), which waits for file:/a; check after, restarts, restart_on and the order of the resources in each Block`},
		// A site that waits for the whole nginx use would wait for its last
		// step, the service, and the service waits for the site (D-167).
		{"after and restart_on",
			[]Resource{
				res("package:nginx", "nginx"),
				svc("service:nginx", "nginx", "file:/site"),
				res("file:/site", "site", "service:nginx"),
			},
			OrderCycle, []string{"service:nginx", "file:/site"},
			`apply order has a cycle: service:nginx (use "nginx") waits for file:/site (use "site"), which waits for service:nginx; check after, restarts, restart_on and the order of the resources in each Block`},
		// D-055: restart_on of a resource that is later in the same use.
		{"restart_on later in its own use",
			[]Resource{svc("service:x", "one", "file:/a"), res("file:/a", "one")},
			OrderCycle, []string{"service:x", "file:/a"}, ""},
		{"after a later resource of its own use",
			[]Resource{res("file:/a", "one", "file:/b"), res("file:/b", "one")},
			OrderCycle, []string{"file:/a", "file:/b"}, ""},
		{"chain of three uses",
			[]Resource{
				res("file:/a", "one", "file:/c2"),
				res("file:/b", "two", "file:/a"),
				res("file:/c", "three", "file:/b"),
				res("file:/c2", "three"),
				res("file:/free", "four"),
			},
			OrderCycle, []string{"file:/a", "file:/c2", "file:/c", "file:/b"},
			`apply order has a cycle: file:/a (use "one") waits for file:/c2 (use "three"), which waits for file:/c (use "three"), which waits for file:/b (use "two"), which waits for file:/a; check after, restarts, restart_on and the order of the resources in each Block`},
	} {
		edges, err := Order(tc.list)
		var oe *OrderError
		if edges != nil || !errors.As(err, &oe) {
			t.Errorf("%s: Order = %v, %v; want an *OrderError", tc.name, edges, err)
			continue
		}
		if oe.Reason != tc.reason || !reflect.DeepEqual(oe.IDs, tc.ids) || len(oe.Blocks) != len(oe.IDs) {
			t.Errorf("%s: got %s %v (blocks %v), want %s %v", tc.name, oe.Reason, oe.IDs, oe.Blocks, tc.reason, tc.ids)
		}
		if tc.text != "" && err.Error() != tc.text {
			t.Errorf("%s: text\n got %s\nwant %s", tc.name, err, tc.text)
		}
	}
}

// A cycle that is not reachable from the first resources is found too, and
// a long chain without a cycle is fine.
func TestOrderLongChain(t *testing.T) {
	var list []Resource
	for i := 0; i < 2000; i++ {
		list = append(list, res("file:/chain/"+strconv.Itoa(i), "chain"))
	}
	if _, err := Order(list); err != nil {
		t.Fatal(err)
	}
	list = append(list, res("file:/p", "p", "file:/q"), res("file:/q", "q", "file:/p"))
	var oe *OrderError
	if _, err := Order(list); !errors.As(err, &oe) || oe.Reason != OrderCycle {
		t.Fatalf("want a cycle, got %v", err)
	}
}

func TestRestartOn(t *testing.T) {
	for _, tc := range []struct {
		v    any
		want []string
	}{
		{nil, nil},
		{"file:/a", nil},
		{[]string{"file:/a", "file:/b"}, []string{"file:/a", "file:/b"}},
		{[]any{"file:/a", 7, "file:/b"}, []string{"file:/a", "file:/b"}},
		{[]any{}, []string{}},
	} {
		r := Resource{ID: "service:x", Type: "service", Attrs: map[string]any{"restart_on": tc.v}}
		if got := RestartOn(r); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("RestartOn(%#v) = %#v, want %#v", tc.v, got, tc.want)
		}
	}
	// The result is a copy: an append or a write does not reach the resource.
	own := []string{"file:/a", "file:/b"}
	r := Resource{Attrs: map[string]any{"restart_on": own[:1]}}
	got := RestartOn(r)
	got[0] = "file:/changed"
	_ = append(got, "file:/more")
	if !reflect.DeepEqual(own, []string{"file:/a", "file:/b"}) {
		t.Errorf("RestartOn gave the slice of the caller: %v", own)
	}
	if got := RestartOn(Resource{}); got != nil {
		t.Errorf("RestartOn of a resource without attrs = %#v", got)
	}
	// The JSON form of a rendered list and the Go form give the same ids.
	var fromJSON Resource
	if err := json.Unmarshal([]byte(`{"id":"service:x","type":"service","block":"b","attrs":{"restart_on":["file:/a"]}}`), &fromJSON); err != nil {
		t.Fatal(err)
	}
	if got := RestartOn(fromJSON); !reflect.DeepEqual(got, []string{"file:/a"}) {
		t.Errorf("JSON form: %#v", got)
	}
}

// after is on the wire only when it is set, so the body of a resource
// without it is the same as before (D-169).
func TestResourceAfterJSON(t *testing.T) {
	b, err := json.Marshal(Resource{ID: "file:/a", Type: "file", Block: "one", Attrs: map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "after") {
		t.Errorf("empty after is on the wire: %s", b)
	}
	b, _ = json.Marshal(res("file:/a", "one", "package:nginx"))
	var back Resource
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(back.After, []string{"package:nginx"}) || !strings.Contains(string(b), `"after":["package:nginx"]`) {
		t.Errorf("after round trip: %s → %#v", b, back.After)
	}
}

func TestUserGroupOrder(t *testing.T) {
	for _, native := range []bool{false, true} {
		for _, state := range []string{StatePresent, StateAbsent} {
			u, g := res("user:person", "user"), res("group:team", "group")
			u.Attrs["groups"] = []any{"team", "unmanaged"}
			if native {
				u.Attrs["groups"] = []string{"team", "unmanaged"}
			}
			g.Attrs[AttrState] = state
			edges, err := Order([]Resource{u, g})
			if err != nil {
				t.Fatal(err)
			}
			want := 1
			if state == StateAbsent {
				want = 0
			}
			if len(edges[0]) != want || len(edges[1]) != 0 {
				t.Fatalf("state %s edges: %+v", state, edges)
			}
			if want == 1 && (edges[0][0].To != 1 || edges[0][0].Kind != EdgeAfter) {
				t.Fatal(edges)
			}
			// Explicit after duplicates the inferred edge; it must not create a cycle.
			u.After = []string{g.ID}
			edges, err = Order([]Resource{g, u})
			if err != nil || len(edges[1]) != 1 {
				t.Fatalf("explicit: %+v %v", edges, err)
			}
		}
	}
}
