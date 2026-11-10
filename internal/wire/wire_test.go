package wire

import (
	"encoding/json"
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

// A change of a sensitive attribute must carry only the flag (D-037).
func TestSensitiveChangeHasNoValues(t *testing.T) {
	b, err := json.Marshal(Change{Attr: "content", Changed: true, Risk: RiskReversible})
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"attr":"content","changed":true,"risk":"reversible"}`
	if string(b) != want {
		t.Errorf("got %s, want %s", b, want)
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
