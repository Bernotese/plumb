package api

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Bernotese/plumb/internal/plan"
	"github.com/Bernotese/plumb/internal/wire"
)

// QA of the repair core (T-195 B6 for T-190; D-385, review R1 of #290).

// TestRepairQAOldAgentHelloIsNoSuccess: after exit 0 only a hello of a
// NEW agent (a new enrollment) passes the Run. The old agent that comes
// back and says hello during the wait is not a repaired agent: the Run
// fails and the old credential keeps working. The engine and the link use
// the same clock here (agent:silent instead of a time offset), so the old
// hello is after the session start (mutant R1 of docs/tasks/T-195.log.md).
func TestRepairQAOldAgentHelloIsNoSuccess(t *testing.T) {
	r := newRepairEnv(t, time.Second) // the old agent says hello every 100 ms inside it
	r.tr.mode = "silent"
	r.old.Pause(true)
	r.agents.off.Store(true)
	changes, _ := json.Marshal([]wire.Change{})
	if _, _, err := r.st.UpsertIssue(r.ctx, "web1", plan.AgentSilentID, changes); err != nil {
		t.Fatal(err)
	}
	p, _ := r.propose(r.adm, nil, 201)
	run := approve(t, r.adm, p.ID)
	// The old agent says hello again and again during the hello wait.
	stop := make(chan struct{})
	go func() {
		for {
			select {
			case <-stop:
				return
			case <-time.After(100 * time.Millisecond):
				r.oldCode()
			}
		}
	}()
	status, s := r.step(run)
	close(stop)
	if status != "failed" || s.Status != string(wire.StatusFailed) || !strings.Contains(s.Output, "no hello of the repaired agent") {
		t.Fatalf("a hello of the old agent passed the repair: run %s, step %+v", status, s)
	}
	if code := r.oldCode(); code != 204 {
		t.Fatalf("old credential: %d, want 204", code)
	}
	if code := r.enrollCode(bundleToken(t, r.tr.bundles[0])); code != 401 {
		t.Fatalf("the token of the failed repair: enroll %d, want 401", code)
	}
	r.noToken(p.ID, run)
}

// TestRepairQATokenNotInExport: the one-time token of a repair is in no
// export, with or without a passphrase (the store keeps only its hash).
func TestRepairQATokenNotInExport(t *testing.T) {
	for _, mode := range []string{"ok", "exit1"} {
		t.Run(mode, func(t *testing.T) {
			r := newRepairEnv(t)
			r.tr.mode = mode
			r.cutOff()
			p, _ := r.propose(r.adm, nil, 201)
			run := approve(t, r.adm, p.ID)
			r.step(run)
			tok := bundleToken(t, r.tr.bundles[0])
			for _, pass := range []string{"", "a-long-export-passphrase"} {
				code, _, body := r.export(r.adm, pass)
				if code != 200 {
					t.Fatalf("export %q: %d", pass, code)
				}
				if strings.Contains(body, tok) {
					t.Fatalf("export %q holds the repair token", pass)
				}
			}
			r.noToken(p.ID, run)
		})
	}
}
