package store

import (
	"encoding/json"
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
