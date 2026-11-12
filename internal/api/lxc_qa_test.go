package api

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Bernotese/plumb/internal/plan"
	"github.com/Bernotese/plumb/internal/state"
)

// D-235 must survive proposal storage and the API approval boundary.
func TestLXCSourceApprovalBoundary(t *testing.T) {
	e := newEnv(t, false)
	adm := e.admin()
	tok := e.token(TierApply, "disruptive")
	for _, tt := range []struct {
		name, url, pin, title string
		human                 bool
	}{
		{"create", "http://download.proxmox.com/a.tar.zst", strings.Repeat("a", 128), "base", true},
		{"title", "http://download.proxmox.com/a.tar.zst", strings.Repeat("a", 128), "renamed", false},
		{"url", "https://mirror.example.org/a.tar.zst", strings.Repeat("a", 128), "renamed", true},
		{"pin", "https://mirror.example.org/a.tar.zst", strings.Repeat("b", 128), "renamed", true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			spec, err := json.Marshal(map[string]any{"type": "lxc", "url": tt.url, "sha512": tt.pin, "title": tt.title})
			if err != nil {
				t.Fatal(err)
			}
			docs := []state.Doc{{Kind: state.KindImage, Name: "ct", Spec: spec}}
			p, _ := e.plan(adm, map[string]any{"reason": tt.name, "put": docs}, 201)
			if tt.human {
				if len(p.HumanOnly) != 1 || p.HumanOnly[0] != plan.HumanOnlyImageRecipe {
					t.Fatalf("human-only reasons = %v", p.HumanOnly)
				}
				if code, body := tok.do("POST", "/api/v1/plans/"+itoa(p.ID)+"/approve", nil); code != 403 || !strings.Contains(string(body), "human-required") {
					t.Fatalf("token approval: %d %s", code, body)
				}
			} else if len(p.HumanOnly) != 0 {
				t.Fatalf("title-only reasons = %v", p.HumanOnly)
			}
			// A rejected token approval must leave the plan available to a human.
			if code, body := adm.do("POST", "/api/v1/plans/"+itoa(p.ID)+"/approve", nil); code != 202 {
				t.Fatalf("human approval: %d %s", code, body)
			}
			e.eng.Wait()
		})
	}
}
