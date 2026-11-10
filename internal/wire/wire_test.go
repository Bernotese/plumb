package wire

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRiskExceeds(t *testing.T) {
	tests := []struct {
		risk, limit Risk
		want        bool
	}{
		{RiskRead, RiskRead, false},
		{RiskReversible, RiskDisruptive, false},
		{RiskDisruptive, RiskDisruptive, false},
		{RiskDestructive, RiskDisruptive, true},
		{RiskDisruptive, RiskReversible, true},
		{"unknown", RiskDestructive, true}, // fail closed
		{RiskRead, "unknown", true},        // fail closed
		{"", RiskDestructive, true},
	}
	for _, tt := range tests {
		if got := tt.risk.Exceeds(tt.limit); got != tt.want {
			t.Errorf("%q.Exceeds(%q) = %v, want %v", tt.risk, tt.limit, got, tt.want)
		}
	}
}

func TestMaxRisk(t *testing.T) {
	if got := MaxRisk(nil); got != RiskRead {
		t.Errorf("MaxRisk(nil) = %q, want %q", got, RiskRead)
	}
	changes := []Change{{Risk: RiskReversible}, {Risk: RiskDestructive}, {Risk: RiskDisruptive}}
	if got := MaxRisk(changes); got != RiskDestructive {
		t.Errorf("MaxRisk = %q, want %q", got, RiskDestructive)
	}
}

func TestSplitID(t *testing.T) {
	tests := []struct {
		id, typ, key string
		ok           bool
	}{
		{"file:/etc/motd", "file", "/etc/motd", true},
		{"file:C:\\x", "file", "C:\\x", true}, // only the first colon splits
		{GuestConfigID, "guest", "config", true},
		{"file:", "file", "", false},
		{":key", "", "key", false},
		{"nocolon", "nocolon", "", false},
	}
	for _, tt := range tests {
		typ, key, ok := SplitID(tt.id)
		if typ != tt.typ || key != tt.key || ok != tt.ok {
			t.Errorf("SplitID(%q) = %q, %q, %v", tt.id, typ, key, ok)
		}
	}
	if got := ID("package", "nginx"); got != "package:nginx" {
		t.Errorf("ID = %q", got)
	}
}

// A change of a sensitive attribute carries only the marker (D-037).
func TestSensitiveChangeHasNoValues(t *testing.T) {
	b, err := json.Marshal(Change{Attr: "content", Sensitive: true, Risk: RiskReversible})
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"attr":"content","sensitive":true,"risk":"reversible"}`
	if string(b) != want {
		t.Errorf("got %s, want %s", b, want)
	}
}

// Redact must remove values that a wrong or hostile agent sends for a
// sensitive attribute, however the agent marks them.
func TestRedact(t *testing.T) {
	reported := []Change{
		{Attr: "content", From: "old-secret", To: "new-secret", Risk: RiskReversible}, // in the list, not marked
		{Attr: "password", From: "x", To: "y", Sensitive: true, Risk: RiskReversible}, // marked, values sent anyway
		{Attr: "token", Sensitive: true, Risk: RiskDisruptive},                        // correct form
		{Attr: "mode", From: "0600", To: "0644", Risk: RiskReversible},                // not sensitive
	}
	got := Redact([]string{"content"}, reported)
	b, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	const want = `[{"attr":"content","sensitive":true,"risk":"reversible"},` +
		`{"attr":"password","sensitive":true,"risk":"reversible"},` +
		`{"attr":"token","sensitive":true,"risk":"disruptive"},` +
		`{"attr":"mode","from":"0600","to":"0644","risk":"reversible"}]`
	if string(b) != want {
		t.Errorf("got  %s\nwant %s", b, want)
	}
	for _, secret := range []string{"old-secret", "new-secret"} {
		if strings.Contains(string(b), secret) {
			t.Errorf("redacted output still holds %q", secret)
		}
	}
	if reported[0].From != "old-secret" {
		t.Error("Redact must not change its input")
	}
	if Redact(nil, nil) != nil {
		t.Error("Redact(nil) must be nil")
	}
}

func TestActionOf(t *testing.T) {
	tests := []struct {
		name    string
		changes []Change
		want    Action
	}{
		{"none", nil, ActionNone},
		{"create", []Change{{Attr: AttrState, From: StateAbsent, To: StatePresent}, {Attr: "content", To: "x"}}, ActionCreate},
		{"create without from", []Change{{Attr: AttrState, To: StatePresent}}, ActionCreate},
		{"create a running service", []Change{{Attr: AttrState, From: StateAbsent, To: "running"}}, ActionCreate},
		{"delete", []Change{{Attr: AttrState, From: StatePresent, To: StateAbsent}}, ActionDelete},
		{"update", []Change{{Attr: "content", From: "a", To: "b"}}, ActionUpdate},
		{"update of a state that is not presence", []Change{{Attr: AttrState, From: "stopped", To: "running"}}, ActionUpdate},
		{"update with restart", []Change{{Attr: "enabled", From: false, To: true}, {Attr: AttrRestart}}, ActionUpdate},
		{"restart", []Change{{Attr: AttrRestart, Risk: RiskDisruptive}}, ActionRestart},
		{"sensitive update", []Change{{Attr: "content", Sensitive: true}}, ActionUpdate},
	}
	for _, tt := range tests {
		if got := ActionOf(tt.changes); got != tt.want {
			t.Errorf("%s: ActionOf = %q, want %q", tt.name, got, tt.want)
		}
	}
}

func TestCommandBodyRoundTrip(t *testing.T) {
	body, err := json.Marshal(ApplyBody{
		PlanID:    7,
		MaxRisk:   RiskDisruptive,
		Resources: []Resource{{ID: "file:/etc/motd", Type: "file", Block: "motd", Attrs: map[string]any{"path": "/etc/motd"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	wireBytes, err := json.Marshal(Command{ID: "c1", Type: CommandApply, Body: body})
	if err != nil {
		t.Fatal(err)
	}
	var cmd Command
	if err := json.Unmarshal(wireBytes, &cmd); err != nil {
		t.Fatal(err)
	}
	var got ApplyBody
	if err := json.Unmarshal(cmd.Body, &got); err != nil {
		t.Fatal(err)
	}
	if cmd.Type != CommandApply || got.PlanID != 7 || got.MaxRisk != RiskDisruptive || got.Resources[0].ID != "file:/etc/motd" {
		t.Errorf("round trip lost data: %+v %+v", cmd, got)
	}
}
