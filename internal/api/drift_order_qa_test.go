package api

import (
	"sync"
	"testing"
	"time"
)

// T-157 follow-up (flaky TestCopyRulePrunedEvidenceStillHumanQA): a watch
// that observes an old guest value must deliver its drift report before a
// newer hand change is reported, as the real agent does. Otherwise the old
// report lands after the change that ended the drift and opens a second
// Issue with the old value. The hook opens exactly that window: a watch
// (after a hello) has seen "copy-before-prune", and the hand change to the
// desired value starts before the watch report is sent.
func TestDriftOrderWatchBeforeChangeQA(t *testing.T) {
	e := newEnv(t, true)
	adm := e.admin()
	e.seed(adm, "web1")
	e.motd("copy-before-prune")
	first := e.motdIssue()

	ag := e.agents["web1"]
	var once sync.Once
	changed := make(chan error, 1)
	ag.SetBeforeWatchReport(func() {
		once.Do(func() {
			go func() {
				changed <- ag.Change(e.ctx, "file:/etc/motd", map[string]any{"path": "/etc/motd", "content": "hello", "mode": "0644"})
			}()
			// Without the drift order the change is reported now, before
			// the old observation.
			time.Sleep(200 * time.Millisecond)
		})
	})
	defer ag.SetBeforeWatchReport(nil)
	ag.SendHello(e.ctx) // the engine answers a hello with a watch
	select {
	case err := <-changed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("no watch with drift came, or the change never ended")
	}
	// The change to the desired value came last: no Issue is open, and the
	// drift never got a second Issue.
	if id := e.openIssueID(); id != 0 {
		t.Fatalf("Issue %d is open after the guest matched again (first Issue %d)", id, first)
	}
	// Issue ids count up: the next id must not be a second motd Issue
	// (open or already resolved).
	if iss, err := e.st.GetIssue(e.ctx, first+1); err == nil && iss.Resource == "file:/etc/motd" {
		t.Fatalf("a second Issue %d for the same drift (status %s)", iss.ID, iss.Status)
	}
	if iss, err := e.st.GetIssue(e.ctx, first); err != nil || iss.Status != "resolved" {
		t.Fatalf("the first Issue: %+v %v", iss, err)
	}
}
