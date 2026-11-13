package plan

import (
	"strings"
	"testing"

	"github.com/Bernotese/plumb/internal/wire"
)

// T-148: which report items the copy rule stores.
func TestGuestValuesOfReport(t *testing.T) {
	e := &Engine{}
	differs := func(id string, c ...wire.Change) wire.Item {
		return wire.Item{Resource: id, Status: wire.StatusDiffers, Changes: c}
	}
	keys := wire.Change{Attr: "ssh_keys", From: []any{"good", "evil"}, To: []any{"good"}}
	if got := e.GuestValues(differs("user:alice", keys)); len(got) != 1 || got[0].Hash != e.guestHash("evil") || got[0].Attr != "ssh_keys" {
		t.Errorf("user keys: %+v", got)
	}
	for name, it := range map[string]wire.Item{
		"sensitive":    differs("file:/etc/x", wire.Change{Attr: "content", Sensitive: true}),
		"no from":      differs("file:/etc/x", wire.Change{Attr: "content", To: "x"}),
		"guest:config": differs("guest:config", wire.Change{Attr: "tags", From: "a", To: "b"}),
		"agent:values": differs("agent:values", wire.Change{Attr: "reported_values", From: "x"}),
		"unchanged":    {Resource: "file:/etc/x", Status: wire.StatusUnchanged, Changes: []wire.Change{{Attr: "content", From: "x"}}},
		"enum":         differs("firewall:main", wire.Change{Attr: "policy", From: "accept", To: "drop"}),
	} {
		if got := e.GuestValues(it); len(got) != 0 {
			t.Errorf("%s: stored %+v", name, got)
		}
	}
	addr := differs(wire.GuestAddressID, wire.Change{Attr: "ip", From: []any{"192.0.2.9"}, To: "192.0.2.10"})
	if got := e.GuestValues(addr); len(got) != 1 {
		t.Errorf("guest:address: %+v", got)
	}
}

func TestCutElement(t *testing.T) {
	if s := cutElement(strings.Repeat("ä", 150)); !strings.HasSuffix(s, "…") || len(s) > maxMatchElement+len("…") || !strings.HasPrefix(s, "ää") {
		t.Errorf("cut: %q", s)
	}
	if cutElement("short") != "short" {
		t.Error("short value cut")
	}
}
