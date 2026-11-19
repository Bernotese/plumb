package api

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Bernotese/plumb/internal/plan"
)

// Row 74 of the T-260 log: when the follow-up of a restore cannot read the
// re-enroll Plans (a store error in openReenroll), Plumb's reason is in the
// guest:restore-failed Issue, not only in the log, and the Issue stays open
// in the "restored" state (D-440) with the way out. A broken pending
// re-enroll row (its time cannot be read) makes that store error.
func TestRestoreFollowUpStoreErrorIsInTheIssue(t *testing.T) {
	s := newSafetyEnv(t)
	cutRestore(t, s, "restored backup 3f2a9c0d1e4b5a67 of 2026-11-15T03:00:12Z")
	db, err := sql.Open("sqlite", "file:"+filepath.Join(s.e.dir, "plumb.db")+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`INSERT INTO plan (origin, proposal, changes, risk, status, reason, author, created_at)
		VALUES ('reenroll', '{}', '[]', 'disruptive', 'pending', 'broken row', 'system', 'not a time')`); err != nil {
		t.Fatal(err)
	}
	if err := s.e.eng.Recover(s.e.ctx); err != nil {
		t.Fatal(err)
	}
	iss, err := s.e.st.OpenIssue(s.e.ctx, "web1", plan.GuestRestoreFailedID)
	if err != nil {
		t.Fatalf("the Issue is not open: %v", err)
	}
	ch := string(iss.Changes)
	for _, want := range []string{plan.RestoredRefusedPrefix, "Plumb could not read its Plans", "POST /targets/web1/reenroll", `"restore_run"`} {
		if !strings.Contains(ch, want) {
			t.Errorf("the Issue does not say %q: %s", want, ch)
		}
	}
	if strings.Contains(ch, "not a time") || strings.Contains(strings.ToLower(ch), "parsing time") {
		t.Errorf("the store error text is in the Issue: %s", ch)
	}
	if !strings.Contains(s.e.logs.String(), "re-enroll after the restore") {
		t.Error("no log line for the store error")
	}
	// With the row fixed, a re-enroll Plan can be made from the Issue's way out.
	if _, err := db.Exec(`DELETE FROM plan WHERE reason = 'broken row'`); err != nil {
		t.Fatal(err)
	}
	if code, body := s.adm.do("POST", "/api/v1/targets/web1/reenroll", map[string]any{"reason": "the way out"}); code >= 300 {
		t.Errorf("the way out of the Issue: %d %s", code, body)
	}
}
