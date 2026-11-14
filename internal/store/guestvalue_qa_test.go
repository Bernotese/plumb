package store

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"
)

func TestGuestValueRetentionQA(t *testing.T) {
	s, _ := openTemp(t)
	ctx := t.Context()
	now := time.Date(2026, 11, 14, 0, 0, 0, 0, time.UTC)
	s.Now = func() time.Time { return now }
	v := GuestValue{Resource: "file:/motd", Attr: "content", Hash: "copy-hash"}
	iss, _, err := s.UpsertIssue(ctx, "source", v.Resource, json.RawMessage(`[]`))
	if err != nil {
		t.Fatal(err)
	}
	record := func() {
		t.Helper()
		if over, err := s.RecordGuestValues(ctx, "source", []GuestValue{v, v}); err != nil || over {
			t.Fatalf("record: %v %v", over, err)
		}
	}
	record()
	now = now.Add(31 * 24 * time.Hour)
	if n, err := s.PruneGuestValues(ctx, now.Add(-30*24*time.Hour)); err != nil || n != 0 {
		t.Fatalf("open issue lost its hash: %d %v", n, err)
	}
	if err := s.ResolveIssue(ctx, iss.ID, "gone", "system"); err != nil {
		t.Fatal(err)
	}
	record() // A fresh report renews retention, even after its Issue closed.
	rows, err := s.MatchGuestValues(ctx, []string{v.Hash})
	if err != nil || len(rows) != 1 || rows[0].IssueID != iss.ID || rows[0].Target != "source" {
		t.Fatalf("match after resolution: %+v %v", rows, err)
	}
	now = now.Add(29 * 24 * time.Hour)
	if n, err := s.PruneGuestValues(ctx, now.Add(-30*24*time.Hour)); err != nil || n != 0 {
		t.Fatalf("refreshed hash pruned early: %d %v", n, err)
	}
	now = now.Add(2 * 24 * time.Hour)
	if n, err := s.PruneGuestValues(ctx, now.Add(-30*24*time.Hour)); err != nil || n != 1 {
		t.Fatalf("expired hash retained: %d %v", n, err)
	}
}

func TestGuestValueFlagExtensionQA(t *testing.T) {
	s, _ := openTemp(t)
	ctx := t.Context()
	now := time.Date(2026, 11, 14, 0, 0, 0, 0, time.UTC)
	for _, until := range []time.Time{now.Add(24 * time.Hour), now.Add(72 * time.Hour), now.Add(48 * time.Hour)} {
		if err := s.FlagGuestValues(ctx, "source", until); err != nil {
			t.Fatal(err)
		}
	}
	if flags, err := s.GuestValueFlags(ctx, now.Add(60*time.Hour)); err != nil || len(flags) != 1 {
		t.Fatalf("flag shortened: %v %v", flags, err)
	}
	if ended, err := s.EndGuestValueFlags(ctx, now.Add(72*time.Hour)); err != nil || len(ended) != 1 {
		t.Fatalf("flag did not end: %v %v", ended, err)
	}
}

// A capped Target with stale known rows must fail closed without refreshing
// any timestamp or adding a row (#256 L1, L3).
func TestGuestValueStaleCapQA(t *testing.T) {
	s, _ := openTemp(t)
	ctx := t.Context()
	old := s.now()
	_, err := s.db.ExecContext(ctx, `WITH RECURSIVE hashes(n) AS (
 VALUES(0) UNION ALL SELECT n+1 FROM hashes WHERE n+1 < ?
 ) INSERT INTO guest_value (target,resource,attr,hash,first_seen,last_seen)
 SELECT 'full','file:/junk','content',printf('%064x',n),?,? FROM hashes`, GuestValueCap, old, old)
	if err != nil {
		t.Fatal(err)
	}
	now := s.Now().Add(2 * time.Hour)
	s.Now = func() time.Time { return now }
	for _, vals := range [][]GuestValue{junkValues(0, guestValueChunk+1), junkValues(GuestValueCap, 1)} {
		if over, err := s.RecordGuestValues(ctx, "full", vals); err != nil || !over {
			t.Fatalf("capped report: %v %v", over, err)
		}
		var count, changed int
		if err := s.db.QueryRowContext(ctx, `SELECT count(*),coalesce(sum(last_seen != ? OR first_seen != ?),0) FROM guest_value WHERE target='full'`, old, old).Scan(&count, &changed); err != nil {
			t.Fatal(err)
		}
		if count != GuestValueCap || changed != 0 {
			t.Fatalf("rows=%d changed=%d", count, changed)
		}
	}
}

// Independent Store pools must enforce the same cap when multi-chunk
// reports race for the last free rows (#248).
func TestGuestValueConcurrentChunksQA(t *testing.T) {
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "shared.db")
	a, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if over, err := a.RecordGuestValues(ctx, "source", junkValues(0, GuestValueCap-guestValueChunk)); err != nil || over {
		t.Fatalf("seed: %v %v", over, err)
	}
	start := make(chan struct{})
	type result struct {
		over bool
		err  error
	}
	results := make(chan result, 2)
	for i, s := range []*Store{a, b} {
		go func() {
			<-start
			over, err := s.RecordGuestValues(ctx, "source", junkValues(GuestValueCap+i*1000, 2*guestValueChunk+1))
			results <- result{over, err}
		}()
	}
	close(start)
	for range 2 {
		if r := <-results; r.err != nil || !r.over {
			t.Fatalf("report: %v %v", r.over, r.err)
		}
	}
	if n, err := a.GuestValueCount(ctx, "source"); err != nil || n != GuestValueCap {
		t.Fatalf("count %d: %v", n, err)
	}
	rows, err := a.db.QueryContext(ctx, `SELECT hash FROM guest_value WHERE target='source'`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	allowed := map[string]bool{}
	for _, v := range junkValues(0, GuestValueCap-guestValueChunk) {
		allowed[v.Hash] = true
	}
	for i := range 2 {
		for _, v := range junkValues(GuestValueCap+i*1000, 2*guestValueChunk+1) {
			allowed[v.Hash] = true
		}
	}
	for rows.Next() {
		var hash string
		if err := rows.Scan(&hash); err != nil {
			t.Fatal(err)
		}
		if !allowed[hash] {
			t.Fatalf("unexpected hash %q", hash)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
}
