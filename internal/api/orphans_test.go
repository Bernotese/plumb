package api

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Bernotese/plumb/internal/state"
	"github.com/Bernotese/plumb/internal/store"
)

// T-212: the start sweep ends the Issues and marks of Targets deleted
// before D-397. Targets that exist (bound, or in the head without a
// binding) are untouched; the copy rule rows stay.
func TestSweepDeletedTargets(t *testing.T) {
	e := newEnv(t, true)
	adm := e.admin()
	e.seed(adm, "web1")
	e.seed(adm, "web2")
	// web2 is in the head but has no binding (a Target without a guest).
	if err := e.st.DeleteBinding(e.ctx, state.KindTarget, "web2"); err != nil {
		t.Fatal(err)
	}
	marks := func(target string) (open, ignored *store.Issue) {
		t.Helper()
		ig, _, err := e.st.UpsertIssue(e.ctx, target, "file:/etc/a", json.RawMessage(`[]`))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := e.st.IgnoreIssue(e.ctx, ig.ID, "user:admin", json.RawMessage(`[]`), json.RawMessage(`[]`)); err != nil {
			t.Fatal(err)
		}
		op, _, err := e.st.UpsertIssue(e.ctx, target, "file:/etc/b", json.RawMessage(`[]`))
		if err != nil {
			t.Fatal(err)
		}
		if err := e.st.RecordIgnored(e.ctx, target, "file:/etc/c", "r1", []string{"content"}); err != nil {
			t.Fatal(err)
		}
		if _, err := e.st.RecordGuestValues(e.ctx, target, []store.GuestValue{{Resource: "file:/etc/b", Attr: "content", Hash: strings.Repeat("a", 64)}}); err != nil {
			t.Fatal(err)
		}
		return &op, &ig
	}
	gone, _ := marks("gone1") // deleted before D-397: no binding, not in the head
	live, _ := marks("web1")
	headOnly, _ := marks("web2")
	// bound-only: a binding but no head document (a delete Plan whose Run
	// did not run yet). It exists.
	if err := e.st.PutBinding(e.ctx, store.Binding{Kind: state.KindTarget, Name: "bound-only", Ref: "9300"}); err != nil {
		t.Fatal(err)
	}
	boundOnly, _ := marks("bound-only")
	// gone2: deleted, with only a record of dropped drift.
	if err := e.st.RecordIgnored(e.ctx, "gone2", "file:/etc/c", "r1", []string{"content"}); err != nil {
		t.Fatal(err)
	}
	// gone1 also flooded the copy rule: the flag and agent:values stay.
	if err := e.st.FlagGuestValues(e.ctx, "gone1", time.Now().Add(24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	vals, _, _ := e.st.UpsertIssue(e.ctx, "gone1", "agent:values", json.RawMessage(`[]`))
	seq, _ := e.st.LastEventSeq(e.ctx)

	swept, err := e.iss.SweepDeletedTargets(e.ctx)
	if err != nil || !slices.Equal(swept, []string{"gone1", "gone2"}) {
		t.Fatalf("swept %v %v", swept, err)
	}
	if iss, _ := e.st.GetIssue(e.ctx, gone.ID); iss.Status != "resolved" || iss.Resolution != "gone" || iss.ResolvedBy != "system" {
		t.Fatalf("gone1 Issue: %+v", iss)
	}
	_, body := adm.do("GET", "/api/v1/issues/"+itoa(gone.ID), nil)
	if !strings.Contains(string(body), "Resolved: the Target was deleted.") {
		t.Fatalf("no comment: %s", body)
	}
	evs, _ := e.st.EventsAfter(e.ctx, seq, 100)
	n := 0
	for _, ev := range evs {
		if ev.Type == "issue.resolved" {
			n++
			if ev.Subject != "issue:"+itoa(gone.ID) {
				t.Errorf("event for %s", ev.Subject)
			}
		}
	}
	if n != 1 {
		t.Fatalf("%d issue.resolved events, want 1", n)
	}
	for _, id := range []int64{live.ID, headOnly.ID, boundOnly.ID, vals.ID} {
		if iss, _ := e.st.GetIssue(e.ctx, id); iss.Status == "resolved" {
			t.Errorf("Issue %d (%s %s) was resolved", id, iss.Target, iss.Resource)
		}
	}
	if dr, _ := e.st.IgnoredDriftOf(e.ctx, "gone2"); len(dr) != 0 {
		t.Errorf("gone2 ignored drift %v", dr)
	}
	for _, target := range []string{"gone1", "web1", "web2", "bound-only"} {
		ig, _ := e.st.IgnoredOnce(e.ctx, target, "file:/etc/a")
		dr, _ := e.st.IgnoredDriftOf(e.ctx, target)
		want := target != "gone1"
		if (ig != nil) != want || (len(dr) > 0) != want {
			t.Errorf("%s: ignore once %s, ignored drift %v; want kept=%v", target, ig, dr, want)
		}
	}
	// The copy rule rows stay (D-397).
	if c, _ := e.st.GuestValueCount(e.ctx, "gone1"); c != 1 {
		t.Errorf("gone1 values: %d", c)
	}
	if f, _ := e.st.GuestValueFlags(e.ctx, time.Now()); !slices.Contains(f, "gone1") {
		t.Errorf("gone1 flag: %v", f)
	}
	// Idempotent.
	if again, err := e.iss.SweepDeletedTargets(e.ctx); err != nil || len(again) != 0 {
		t.Fatalf("second sweep %v %v", again, err)
	}
}
