//go:build pve

package pve_test

import (
	"encoding/json"
	"net/url"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
)

func hostRead(t *testing.T, n int, action string) string {
	t.Helper()
	out, ok := hostSSH(t, nil, n, action)
	if !ok {
		t.Fatalf("host read failed for VMID %d (output withheld)", n)
	}
	return out
}

func config(t *testing.T, n int) map[string]string {
	t.Helper()
	return configView(t, n, "config")
}

// configView parses "qm config" (the view after the next cold start) or
// "config-current" (the running view).
func configView(t *testing.T, n int, action string) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, line := range strings.Split(hostRead(t, n, action), "\n") {
		k, v, ok := strings.Cut(line, ": ")
		if ok {
			out[k] = v
		}
	}
	return out
}

func snapshot(t *testing.T) map[int]string {
	t.Helper()
	out := map[int]string{}
	for _, line := range strings.Split(hostRead(t, 9000, "list"), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		n, err := strconv.Atoi(fields[0])
		if err != nil || n < 9000 || n > 9099 {
			continue
		}
		c := config(t, n)
		out[n] = c["name"] + " template=" + c["template"]
	}
	return out
}

func sortedIDs(s map[int]string) []int {
	ids := []int{}
	for n := range s {
		ids = append(ids, n)
	}
	sort.Ints(ids)
	return ids
}

func poolContains(t *testing.T, n int) bool {
	t.Helper()
	var p struct {
		Members []struct {
			VMID int    `json:"vmid"`
			Type string `json:"type"`
		} `json:"members"`
	}
	check(t, json.Unmarshal([]byte(hostRead(t, n, "pool")), &p), "decode pool")
	for _, m := range p.Members {
		if m.VMID == n && m.Type == "qemu" {
			return true
		}
	}
	return false
}

func (a *apiClient) documents(kind string) map[string]bool {
	a.t.Helper()
	names := map[string]bool{}
	path := "/" + kind + "?limit=200"
	for {
		p := a.call("GET", path, nil, 200)
		for _, item := range p["items"].([]any) {
			d := item.(map[string]any)["document"].(map[string]any)
			names[d["name"].(string)] = true
		}
		next, _ := p["next"].(string)
		if next == "" {
			return names
		}
		path = "/" + kind + "?limit=200&cursor=" + url.QueryEscape(next)
	}
}

func cleanup(a *apiClient, before map[int]string, expected map[string]bool, refs []any, owned map[int]bool) {
	a.t.Helper()
	// Only delete documents registered by this run that actually reached head.
	defer cleanupTemplates(a, before, expected, owned)
	func() {
		c := *a
		present := map[string]map[string]bool{}
		for _, kind := range []string{"Target", "Image", "Block"} {
			present[kind] = c.documents(strings.ToLower(kind) + "s")
		}
		deletes := []any{}
		for _, ref := range refs {
			r := ref.(map[string]any)
			if present[r["kind"].(string)][r["name"].(string)] {
				deletes = append(deletes, r)
			}
		}
		if len(deletes) > 0 {
			p := c.call("POST", "/plans", object{"reason": "T-015 cleanup", "delete": deletes}, 201)
			c.approve(p, true)
		}
	}()
}

func cleanupTemplates(a *apiClient, before map[int]string, expected map[string]bool, owned map[int]bool) {
	a.t.Helper()
	for n := range snapshot(a.t) {
		if _, existed := before[n]; existed {
			continue
		}
		c := config(a.t, n)
		// Never destroy a guest, a foreign template, or a template outside the pool.
		if c["template"] != "1" || !expected[c["name"]] || !poolContains(a.t, n) {
			a.t.Errorf("template destroy guard refused VMID %d", n)
			continue
		}
		owned[n] = true
		if _, ok := hostSSH(a.t, owned, n, "template-delete"); !ok {
			a.t.Errorf("template destroy failed for VMID %d (output withheld)", n)
		}
	}
	after := snapshot(a.t)
	a.t.Logf("guest/template list after: %v", inventory(after))
	if !reflect.DeepEqual(before, after) {
		// IDs only: guest names and config can contain private data.
		a.t.Errorf("guest/template list differs: before %v; after %v", sortedIDs(before), sortedIDs(after))
	}
}
