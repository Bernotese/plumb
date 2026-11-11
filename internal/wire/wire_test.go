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

func TestCovers(t *testing.T) {
	planned := []Change{
		{Attr: "content", From: "old", To: "new", Risk: RiskReversible},
		{Attr: "mode", From: "0644", To: "0600", Risk: RiskReversible},
		{Attr: "cores", From: 2, To: 4, Risk: RiskDisruptive},
		{Attr: "secret", Sensitive: true, Risk: RiskReversible},
		{Attr: "groups", From: []any{"a", "b"}, To: map[string]any{"y": 1, "x": 2}, Risk: RiskReversible},
	}
	for _, tc := range []struct {
		name string
		now  []Change
		want bool
	}{
		{"nothing differs", nil, true},
		{"subset", []Change{{Attr: "content", From: "old", To: "new", Risk: RiskReversible}}, true},
		{"all, other order", []Change{planned[2], planned[0], planned[1], planned[3], planned[4]}, true},
		{"number forms", []Change{{Attr: "cores", From: 2.0, To: json.Number("4"), Risk: RiskDisruptive}}, true},
		{"nested values", []Change{{Attr: "groups", From: []string{"a", "b"}, To: map[string]int{"x": 2, "y": 1}, Risk: RiskReversible}}, true},
		{"unplanned attribute", []Change{{Attr: "owner", From: "a", To: "b", Risk: RiskReversible}}, false},
		{"other from", []Change{{Attr: "content", From: "edited", To: "new", Risk: RiskReversible}}, false},
		{"other to", []Change{{Attr: "content", From: "old", To: "newer", Risk: RiskReversible}}, false},
		{"other risk", []Change{{Attr: "content", From: "old", To: "new", Risk: RiskDisruptive}}, false},
		{"missing vs null", []Change{{Attr: "content", To: "new", Risk: RiskReversible}}, false},
		{"sensitive now, plain planned", []Change{{Attr: "content", Sensitive: true, Risk: RiskReversible}}, true},
		{"sensitive planned", []Change{{Attr: "secret", From: "x", To: "y", Risk: RiskReversible}}, true},
		{"sensitive, other risk", []Change{{Attr: "secret", Sensitive: true, Risk: RiskDisruptive}}, false},
		{"restart not planned", []Change{{Attr: AttrRestart, Risk: RiskDisruptive}}, false},
	} {
		if got := Covers(planned, tc.now); got != tc.want {
			t.Errorf("%s: Covers = %v, want %v", tc.name, got, tc.want)
		}
	}
	if Covers(nil, []Change{{Attr: "a", Risk: RiskRead}}) {
		t.Error("an empty plan covers a change")
	}
	// The JSON form of the body keeps planned.
	raw, _ := json.Marshal(ApplyBody{PlanID: 1, Planned: []Planned{{Resource: "file:/x", Unverified: true}}, MaxRisk: RiskReversible})
	if !strings.Contains(string(raw), `"planned":[{"resource":"file:/x","unverified":true}]`) {
		t.Errorf("ApplyBody JSON: %s", raw)
	}
}

func TestSealContract(t *testing.T) {
	if len(SealSteps) != 10 || SealSteps[0] != SealStepGuard || SealSteps[len(SealSteps)-1] != SealStepTrim {
		t.Fatalf("SealSteps = %v: guard first, trim last", SealSteps)
	}
	seen := map[string]bool{}
	for _, s := range SealSteps {
		if seen[s] {
			t.Fatalf("step %q twice", s)
		}
		seen[s] = true
	}
	if got := SealItemID(SealStepHostKeys); got != "seal:host-keys" {
		t.Fatalf("SealItemID = %q", got)
	}
	raw, _ := json.Marshal(SealBody{PlanID: 7, Upgraded: true})
	if string(raw) != `{"plan_id":7,"upgraded":true}` {
		t.Fatalf("SealBody JSON = %s", raw)
	}
	raw, _ = json.Marshal(EnrollResponse{AgentID: "a", Credential: "c"})
	if strings.Contains(string(raw), "build") {
		t.Fatalf("a normal enrollment must not carry the build mark: %s", raw)
	}
}
