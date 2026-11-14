package api

import (
	"testing"
	"time"
)

// Approval rechecks can add evidence, but pruning must never remove the
// human-only reason or evidence already recorded on a pending Plan.
func TestCopyRulePrunedEvidenceStillHumanQA(t *testing.T) {
	e := newEnv(t, true)
	adm := e.admin()
	e.seed(adm, "web1")
	e.motd("copy-before-prune")
	id := e.motdIssue()
	e.motd("hello")
	p := e.proposeDocs(e.token(TierPlan, ""), targetDoc("web1", "copy-before-prune"))
	guestOnly(t, p, id, "web1", "copy-before-prune")
	later := time.Now().Add(31 * 24 * time.Hour)
	e.st.Now = func() time.Time { return later }
	if err := e.iss.PruneGuestValues(e.ctx); err != nil {
		t.Fatal(err)
	}
	if n, err := e.st.GuestValueCount(e.ctx, "web1"); err != nil || n != 0 {
		t.Fatalf("pruned rows: %d %v", n, err)
	}
	e.tokenRefused(p.ID)
	guestOnly(t, e.getGuestPlan(adm, p.ID), id, "web1", "copy-before-prune")
}
