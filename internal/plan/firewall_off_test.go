package plan

import (
	"context"
	"encoding/json"
	"path/filepath"
	"slices"
	"testing"

	"github.com/Bernotese/plumb/internal/driver/fake"
	"github.com/Bernotese/plumb/internal/link"
	"github.com/Bernotese/plumb/internal/state"
	"github.com/Bernotese/plumb/internal/store"
)

// Documents for the firewall-off tests (D-368).
func fwDoc(kind, name, spec string) state.Doc {
	return state.Doc{Kind: kind, Name: name, Spec: json.RawMessage(spec)}
}

const (
	fwImage  = `{"type":"vm","url":"https://images.invalid/u.img","sha256":"0000000000000000000000000000000000000000000000000000000000000000"}`
	fwGuest  = `"guest":{"cores":1,"memory":1024,"disk":10,"bridge":"vmbr0"}`
	fwBlock  = `{"resources":[{"firewall":{"name":"main","tcp_ports":"80"}}]}`
	motdBlck = `{"resources":[{"file":{"path":"/etc/motd","content":"hi","mode":"0644"}}]}`
)

func fwTarget(blocks, extra string) state.Doc {
	return fwDoc(state.KindTarget, "web1", `{"type":"vm","image":"ubuntu",`+fwGuest+`,"blocks":[`+blocks+`]`+extra+`}`)
}

func fwSet(t *testing.T, docs ...state.Doc) *state.Set {
	t.Helper()
	all := append([]state.Doc{fwDoc(state.KindImage, "ubuntu", fwImage)}, docs...)
	s, err := state.Validate(all)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// Every way to remove a firewall that is on adds firewall-off; a change of
// its values, a firewall that was off already, and other changes do not.
func TestFirewallOff(t *testing.T) {
	fw := fwDoc(state.KindBlock, "fw", fwBlock)
	motd := fwDoc(state.KindBlock, "motd", motdBlck)
	fwDef := fwDoc(state.KindBlock, "fw", `{"resources":[{"firewall":{"name":"main"}}]}`)
	base := fwSet(t, fw, motd, fwTarget(`{"use":"fw"},{"use":"motd"}`, ""))
	baseOff := fwSet(t, fwDoc(state.KindBlock, "fw", `{"resources":[{"firewall":{"name":"main","state":"absent"}}]}`), motd,
		fwTarget(`{"use":"fw"},{"use":"motd"}`, ""))

	for _, c := range []struct {
		name string
		base *state.Set
		next *state.Set
		want bool
	}{
		{"state absent in the Block", base, fwSet(t, fwDoc(state.KindBlock, "fw", `{"resources":[{"firewall":{"name":"main","state":"absent"}}]}`), motd,
			fwTarget(`{"use":"fw"},{"use":"motd"}`, "")), true},
		{"resource removed from the Block", base, fwSet(t, fwDoc(state.KindBlock, "fw", `{"resources":[{"file":{"path":"/etc/fw-gone","content":"x","mode":"0644"}}]}`), motd,
			fwTarget(`{"use":"fw"},{"use":"motd"}`, "")), true},
		{"Block use removed from the Target", base, fwSet(t, fw, motd, fwTarget(`{"use":"motd"}`, "")), true},
		{"Block deleted with its use", base, fwSet(t, motd, fwTarget(`{"use":"motd"}`, "")), true},
		{"Target deleted", base, fwSet(t, fw, motd), true},
		// An override may set state absent only when the Block sets no
		// other firewall attribute (state validation): a defaults firewall.
		{"override state absent", fwSet(t, fwDef, motd, fwTarget(`{"use":"fw"},{"use":"motd"}`, "")), fwSet(t, fwDef, motd,
			fwTarget(`{"use":"fw"},{"use":"motd"}`, `,"overrides":{"firewall:main":{"state":"absent"}}`)), true},
		{"ports changed", base, fwSet(t, fwDoc(state.KindBlock, "fw", `{"resources":[{"firewall":{"name":"main","tcp_ports":"80,443"}}]}`), motd,
			fwTarget(`{"use":"fw"},{"use":"motd"}`, "")), false},
		{"another resource removed", base, fwSet(t, fw, motd, fwTarget(`{"use":"fw"}`, "")), false},
		{"firewall added", fwSet(t, fw, motd, fwTarget(`{"use":"motd"}`, "")), base, false},
		{"off already, Target deleted", baseOff, fwSet(t, motd), false},
		{"off already, use removed", baseOff, fwSet(t, motd, fwTarget(`{"use":"motd"}`, "")), false},
	} {
		got, err := firewallOff(c.base, c.next, []string{"web1"})
		if err != nil || got != c.want {
			t.Errorf("%s: %v %v, want %v", c.name, got, err, c.want)
		}
	}
	// A Target that the Plan does not touch is not looked at.
	if got, _ := firewallOff(base, fwSet(t, fw, motd), []string{"web2"}); got {
		t.Error("an untouched target counted")
	}
}

// Propose sets the reason on the Plan (the wiring of firewallOff), for
// every origin: here a plain proposal that drops the firewall's Block use.
func TestProposeFirewallOff(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "fw.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	lk, err := link.New(link.Config{Store: st, Dir: filepath.Join(t.TempDir(), "link")})
	if err != nil {
		t.Fatal(err)
	}
	e, err := New(Config{Store: st, Agents: lk, Driver: fake.New()})
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	fw := fwDoc(state.KindBlock, "fw", fwBlock)
	motd := fwDoc(state.KindBlock, "motd", motdBlck)
	rows, err := state.Rows(fwSet(t, fw, motd, fwTarget(`{"use":"fw"},{"use":"motd"}`, "")))
	if err != nil {
		t.Fatal(err)
	}
	head, err := st.CommitRevision(ctx, store.NewRevision{Author: "user:qa", Docs: rows})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name string
		prop Proposal
		want bool
	}{
		{"drop the use", Proposal{Put: []state.Doc{fwTarget(`{"use":"motd"}`, "")}}, true},
		{"delete the Target", Proposal{Delete: []DocKey{{Kind: state.KindTarget, Name: "web1"}}}, true},
		{"change the ports", Proposal{Put: []state.Doc{fwDoc(state.KindBlock, "fw", `{"resources":[{"firewall":{"name":"main","tcp_ports":"80,443"}}]}`)}}, false},
	} {
		p, err := e.Propose(ctx, Input{Base: head.ID, Author: "token:tok_x", Reason: c.name, Proposal: c.prop})
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if got := slices.Contains(p.HumanOnly, HumanOnlyFirewallOff); got != c.want {
			t.Errorf("%s: human_only %v, want firewall-off %v", c.name, p.HumanOnly, c.want)
		}
		if err := st.DiscardPlan(ctx, p.ID); err != nil {
			t.Fatal(err)
		}
	}
}
