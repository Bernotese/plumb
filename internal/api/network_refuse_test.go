package api

import (
	"context"
	"errors"
	"net/netip"
	"strings"
	"testing"

	"github.com/Bernotese/plumb/internal/plan"
	"github.com/Bernotese/plumb/internal/state"
)

// T-150 (D-348, review G1 of #244): the server refuses a network address
// that is not the guest's.
func TestNetworkServerRefusals(t *testing.T) {
	resolveErr := error(nil)
	e := newEnv(t, true, func(c *plan.Config) {
		c.ReservedAddrs = func(context.Context) ([]state.ReservedAddr, error) {
			if resolveErr != nil {
				return nil, resolveErr
			}
			return []state.ReservedAddr{
				{Addr: netip.MustParseAddr("203.0.113.250"), What: "the controller address"},
				{Addr: netip.MustParseAddr("203.0.113.251"), What: "the Proxmox node address"}}, nil
		}
	})
	adm := e.admin()
	net := doc(state.KindBlock, "net", `{"params":{"type":"object","properties":{"a":{"type":"string"}}},`+
		`"resources":[{"network":{"name":"extra","addresses":"{{ .a }}"}}]}`)
	vm := func(name, ip, addr string) state.Doc {
		return doc(state.KindTarget, name, `{"type":"vm","image":"ubuntu","guest":{"cores":2,"memory":1024,"disk":10,"bridge":"vmbr0",`+
			`"ip":"`+ip+`","gateway":"192.0.2.1"},"blocks":[{"use":"net","params":{"a":"`+addr+`"}}]}`)
	}
	propose := func(docs ...state.Doc) (int, string) {
		code, body := adm.do("POST", "/api/v1/plans", map[string]any{"reason": "net",
			"put": append([]state.Doc{doc(state.KindImage, "ubuntu", imageSpec), net}, docs...)})
		return code, string(body)
	}
	web2 := vm("web2", "192.0.2.6/24", "192.0.2.60/24")
	for _, c := range []struct{ addr, want string }{
		{"203.0.113.250/24", "the controller address"},
		{"203.0.113.251/24", "the Proxmox node address"},
		{"192.0.2.5/24", "the main address of Target web1"},
		{"192.0.2.6/24", "the main address of Target web2"},
		{"192.0.2.1/24", "the gateway of Target web1"},
		{"192.0.2.60/24", "an extra address of Target web2"},
		{"169.254.1.1/16", "link-local"},
		{"192.0.2.70/24,192.0.2.70/25", "the same address twice"},
	} {
		code, body := propose(vm("web1", "192.0.2.5/24", c.addr), web2)
		if code != 422 || !strings.Contains(body, c.want) {
			t.Errorf("%s: %d %s, want 422 %q", c.addr, code, body, c.want)
		}
		if strings.Contains(body, strings.Split(c.addr, "/")[0]) {
			t.Errorf("%s: the error repeats the address: %s", c.addr, body)
		}
	}
	if code, body := propose(vm("web1", "192.0.2.5/24", "192.0.2.70/24"), web2); code != 201 {
		t.Fatalf("a free address: %d %s", code, body)
	}
	// A new static main address that is an extra address of another Target.
	if code, body := propose(vm("web1", "192.0.2.60/24", "192.0.2.70/24"), web2); code != 422 ||
		!strings.Contains(body, "the main address is an extra address of Target web2") {
		t.Errorf("main address taken as an extra: %d %s", code, body)
	}
	// Fail closed when the controller or node name does not resolve.
	resolveErr = errors.New("cannot resolve the controller address (plumb.invalid)")
	if code, body := propose(vm("web1", "192.0.2.5/24", "192.0.2.70/24"), web2); code != 422 ||
		!strings.Contains(body, "the network addresses cannot be checked") {
		t.Errorf("unresolved name: %d %s", code, body)
	}
	resolveErr = nil
	// network is for a VM only.
	ct := doc(state.KindImage, "ct", `{"type":"lxc","url":"http://download.proxmox.com/images/system/debian-13.tar.zst","sha512":"`+strings.Repeat("c", 128)+`"}`)
	c1 := doc(state.KindTarget, "c1", `{"type":"lxc","image":"ct","guest":{"cores":1,"memory":512,"disk":4,"bridge":"vmbr0"},"blocks":[{"use":"net","params":{"a":"192.0.2.80/24"}}]}`)
	if code, body := propose(ct, c1); code != 422 || !strings.Contains(body, "resource type network does not support lxc targets") {
		t.Errorf("network on an LXC: %d %s", code, body)
	}
}

func TestHostAddrs(t *testing.T) {
	ctx := context.Background()
	got, err := plan.HostAddrs([]string{"192.0.2.9", "::ffff:198.51.100.9"}, "https://203.0.113.4:8006")(ctx)
	if err != nil || len(got) != 3 || got[1].Addr != netip.MustParseAddr("198.51.100.9") || got[2].What != "the Proxmox node address" {
		t.Fatalf("literals: %+v %v", got, err)
	}
	if _, err := plan.HostAddrs([]string{"no-such-host.invalid"}, "")(ctx); err == nil {
		t.Error("an unresolvable name did not fail")
	}
	if _, err := plan.HostAddrs(nil, "https://")(ctx); err == nil {
		t.Error("a Proxmox URL without host did not fail")
	}
}
