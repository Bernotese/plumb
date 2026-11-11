package plan

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/Bernotese/plumb/internal/store"
)

func at(s string) time.Time {
	t, err := time.Parse("2006-01-02 15:04", s)
	if err != nil {
		panic(err)
	}
	return t
}

func TestScheduleSlots(t *testing.T) {
	day := Schedule{Every: "day", At: "03:00"}
	sun := Schedule{Every: "week", Weekday: "sun", At: "03:00"}
	d28 := Schedule{Every: "month", Day: 28, At: "23:30"}
	d1 := Schedule{Every: "month", Day: 1, At: "00:00"}
	tests := []struct {
		name       string
		s          Schedule
		now        string
		last, next string
	}{
		{"day before", day, "2026-11-12 02:59", "2026-11-11 03:00", "2026-11-12 03:00"},
		{"day at", day, "2026-11-12 03:00", "2026-11-12 03:00", "2026-11-13 03:00"},
		{"day after", day, "2026-11-12 23:59", "2026-11-12 03:00", "2026-11-13 03:00"},
		{"week on sunday before", sun, "2026-11-15 02:00", "2026-11-08 03:00", "2026-11-15 03:00"}, // 15 Nov 2026 is a Sunday
		{"week on sunday after", sun, "2026-11-15 04:00", "2026-11-15 03:00", "2026-11-22 03:00"},
		{"week midweek", sun, "2026-11-11 12:00", "2026-11-08 03:00", "2026-11-15 03:00"},
		{"month 28 before", d28, "2026-02-28 23:29", "2026-01-28 23:30", "2026-02-28 23:30"},
		{"month 28 after", d28, "2026-02-28 23:31", "2026-02-28 23:30", "2026-03-28 23:30"},
		{"month year change", d1, "2027-01-01 00:00", "2027-01-01 00:00", "2027-02-01 00:00"},
		{"month before year change", d1, "2026-12-31 23:59", "2026-12-01 00:00", "2027-01-01 00:00"},
	}
	for _, tt := range tests {
		now := at(tt.now)
		if got := tt.s.LastSlot(now); !got.Equal(at(tt.last)) {
			t.Errorf("%s: LastSlot = %v, want %s", tt.name, got, tt.last)
		}
		if got := tt.s.NextSlot(now); !got.Equal(at(tt.next)) {
			t.Errorf("%s: NextSlot = %v, want %s", tt.name, got, tt.next)
		}
	}
	// A clock in another zone: slots are UTC.
	cet := time.FixedZone("CET", 3600)
	if got := day.LastSlot(time.Date(2026, 11, 12, 3, 30, 0, 0, cet)); !got.Equal(at("2026-11-11 03:00")) {
		t.Errorf("zone: %v", got)
	}
	for _, bad := range []string{`{"every":"hour","at":"03:00"}`, `{"every":"day","at":"3"}`, `{"every":"week","at":"03:00","weekday":"xyz"}`,
		`{"every":"month","at":"03:00","day":29}`} {
		if _, err := parseSchedule(json.RawMessage(bad)); err == nil {
			t.Errorf("parseSchedule(%s) accepted", bad)
		}
	}
}

func TestSystemCheck(t *testing.T) {
	images := func(trigger string) json.RawMessage {
		raw, _ := json.Marshal([]ImagePlan{{Image: "web", Build: &ImageBuildPlan{Version: 1, Trigger: trigger}}})
		return raw
	}
	ok := store.Plan{Origin: OriginBuild, Proposal: json.RawMessage(`{}`), Changes: json.RawMessage(`[]`), Images: images(store.TriggerSchedule)}
	if err := SystemCheck(ok); err != nil {
		t.Fatalf("scheduled build refused: %v", err)
	}
	bad := map[string]func(*store.Plan){
		"proposal origin": func(p *store.Plan) { p.Origin = OriginProposal },
		"manual trigger":  func(p *store.Plan) { p.Images = images(store.TriggerManual) },
		"change trigger":  func(p *store.Plan) { p.Images = images(store.TriggerChange) },
		"human only":      func(p *store.Plan) { p.HumanOnly = []string{HumanOnlyImageRecipe} },
		"document change": func(p *store.Plan) { p.Proposal = json.RawMessage(`{"put":[{"kind":"Block","name":"a","spec":{}}]}`) },
		"delete":          func(p *store.Plan) { p.Proposal = json.RawMessage(`{"delete":[{"kind":"Image","name":"web"}]}`) },
		"target":          func(p *store.Plan) { p.Changes = json.RawMessage(`[{"target":"web1","resources":[]}]`) },
		"removals only":   func(p *store.Plan) { p.Images = json.RawMessage(`[{"image":"web","removes":[]}]`) },
		"no image":        func(p *store.Plan) { p.Images = json.RawMessage(`[]`) },
	}
	for name, mut := range bad {
		p := ok
		mut(&p)
		if err := SystemCheck(p); err == nil {
			t.Errorf("%s: system approves it", name)
		}
	}
}
