package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Bernotese/plumb/internal/wire"
)

// D-075: the agent applies only what the approved Plan shows.
func TestApplyOnlyWhatIsPlanned(t *testing.T) {
	const path = "/srv/conf"
	want := fileRes("app", path, map[string]any{"content": "new\n", "mode": "0600"})
	after := fileRes("app", "/srv/after", map[string]any{"content": "x"})
	content := func(from, to string) wire.Change {
		return wire.Change{Attr: "content", From: from, To: to, Risk: wire.RiskReversible}
	}
	mode := wire.Change{Attr: "mode", From: "0644", To: "0600", Risk: wire.RiskReversible}

	tests := []struct {
		name     string
		guest    string // content on the guest before the apply ("" = 0600 file with "new\n")
		guestMod os.FileMode
		planned  []wire.Planned
		max      wire.Risk
		status   wire.Status
		output   string
		file     string // content after the apply
	}{
		{"unplanned drift is refused", "old\n", 0o644, nil, wire.RiskReversible, wire.StatusRefused, "differs from the approved Plan", "old\n"},
		{"planned with another from is refused", "edited by hand\n", 0o644,
			[]wire.Planned{{Resource: want.ID, Changes: []wire.Change{content("old\n", "new\n"), mode}}},
			wire.RiskReversible, wire.StatusRefused, "differs from the approved Plan", "edited by hand\n"},
		{"planned mode and content, only content differs", "old\n", 0o600,
			[]wire.Planned{{Resource: want.ID, Changes: []wire.Change{content("old\n", "new\n"), mode}}},
			wire.RiskReversible, wire.StatusApplied, "", "new\n"},
		{"planned, but nothing differs", "new\n", 0o600,
			[]wire.Planned{{Resource: want.ID, Changes: []wire.Change{content("old\n", "new\n")}}},
			wire.RiskReversible, wire.StatusUnchanged, "", "new\n"},
		{"unverified is applied", "anything\n", 0o644,
			[]wire.Planned{{Resource: want.ID, Unverified: true}},
			wire.RiskReversible, wire.StatusApplied, "", "new\n"},
		{"unverified still obeys max_risk", "anything\n", 0o644,
			[]wire.Planned{{Resource: want.ID, Unverified: true}},
			wire.RiskRead, wire.StatusRefused, "above the approved read", "anything\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ca := newCA(t)
			ctl := newController(t, ca, "tok-1")
			ta := startAgent(t, ctl, ca, "tok-1")
			p := filepath.Join(ta.root, path)
			write(t, p, tt.guest)
			if err := os.Chmod(p, tt.guestMod); err != nil {
				t.Fatal(err)
			}
			planned := append(tt.planned, wire.Planned{Resource: after.ID, Unverified: true})
			ctl.send(t, "a1", wire.CommandApply, wire.ApplyBody{Resources: []wire.Resource{want, after}, Planned: planned, MaxRisk: tt.max})
			rep := ctl.result(t, "a1")
			if rep.Error != "" || len(rep.Items) != 2 {
				t.Fatalf("result = %+v", rep)
			}
			it := rep.Items[0]
			if it.Status != tt.status || !strings.Contains(it.Output, tt.output) {
				t.Fatalf("item = %+v, want %s %q", it, tt.status, tt.output)
			}
			if b, _ := os.ReadFile(p); string(b) != tt.file {
				t.Fatalf("file = %q, want %q", b, tt.file)
			}
			if tt.status == wire.StatusRefused {
				if len(it.Changes) == 0 {
					t.Fatal("a refused item must carry the new difference")
				}
				if rep.Items[1].Status != wire.StatusSkipped {
					t.Fatalf("rest of the Block = %s, want skipped", rep.Items[1].Status)
				}
				// The refused difference stays: the drift check reports it.
				waitFor(t, "drift of the refused resource", func() bool { return len(ctl.driftItems(want.ID)) > 0 })
			}
		})
	}
}

func TestApplySensitiveCoveredByAttrAndRisk(t *testing.T) {
	ca := newCA(t)
	ctl := newController(t, ca, "tok-1")
	ta := startAgent(t, ctl, ca, "tok-1")
	secretRes := fileRes("app", "/srv/secret", map[string]any{"content": "s3cret\n", "mode": "0600"})
	secretRes.Sensitive = []string{"content"}
	p := filepath.Join(ta.root, "srv/secret")
	write(t, p, "old\n")
	if err := os.Chmod(p, 0o600); err != nil {
		t.Fatal(err)
	}
	ctl.send(t, "a1", wire.CommandApply, wire.ApplyBody{Resources: []wire.Resource{secretRes}, MaxRisk: wire.RiskReversible,
		Planned: []wire.Planned{{Resource: secretRes.ID, Changes: []wire.Change{{Attr: "content", Sensitive: true, Risk: wire.RiskReversible}}}}})
	if it := ctl.result(t, "a1").Items[0]; it.Status != wire.StatusApplied {
		t.Fatalf("item = %+v", it)
	}
	if b, _ := os.ReadFile(p); string(b) != "s3cret\n" {
		t.Fatalf("file = %q", b)
	}
}

func TestApplyBodyChecks(t *testing.T) {
	ca := newCA(t)
	ctl := newController(t, ca, "tok-1")
	startAgent(t, ctl, ca, "tok-1")
	r := fileRes("app", "/srv/x", nil)
	ctl.send(t, "a1", wire.CommandApply, wire.ApplyBody{Resources: []wire.Resource{r}, MaxRisk: wire.RiskReversible,
		Planned: []wire.Planned{{Resource: "file:/srv/other", Unverified: true}}})
	ctl.send(t, "a2", wire.CommandApply, wire.ApplyBody{Resources: []wire.Resource{r}, MaxRisk: wire.RiskReversible,
		Planned: []wire.Planned{{Resource: r.ID, Unverified: true}, {Resource: r.ID, Unverified: true}}})
	for id, want := range map[string]string{"a1": "not in the resource list", "a2": "listed twice"} {
		if rep := ctl.result(t, id); !strings.Contains(rep.Error, want) || len(rep.Items) != 0 {
			t.Errorf("%s: result = %+v, want error %q", id, rep, want)
		}
	}
}
