package link

import (
	"github.com/Bernotese/plumb/internal/wire"
	"testing"
)

func TestSealItemsOnlyFromBuildResult(t *testing.T) {
	for _, target := range []string{"web1", "build:web"} {
		t.Run(target, func(t *testing.T) {
			f := newFixture(t)
			cred := f.agent(target)
			id, err := f.link.Send(target, wire.CommandApply, wire.ApplyBody{})
			if err != nil {
				t.Fatal(err)
			}
			if code, body := f.do("GET", wire.PathPoll, cred, nil); code != 200 {
				t.Fatalf("poll: %d %s", code, body)
			}
			for _, typ := range []wire.ReportType{wire.ReportProgress, wire.ReportResult} {
				code, body := f.do("POST", wire.PathReport, cred, wire.Report{Type: typ, CommandID: id, Items: []wire.Item{{Resource: wire.SealItemID(wire.SealStepGuard), Status: wire.StatusApplied}}})
				want := 400
				if target == "build:web" && typ == wire.ReportResult {
					want = 204
				}
				if code != want {
					t.Errorf("%s: %d %s, want %d", typ, code, body, want)
				}
			}
		})
	}
}
