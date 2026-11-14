package backup

import (
	"fmt"
	"testing"
	"time"
)

const (
	tInstall = "6f1c2a9e-3b4d-4e5f-8a7b-0c1d2e3f4a5b"
	tLineage = "0123456789abcdef"
)

func TestMarkerRoundTrip(t *testing.T) {
	for _, m := range []Marker{{tInstall, "db1", tLineage, false}, {tInstall, "web.lan", tLineage, true}} {
		s, err := m.String()
		if err != nil {
			t.Fatal(err)
		}
		got, ok := ParseMarker(s + "\nsecond line ignored")
		if !ok || got != m {
			t.Fatalf("%q → %+v %v", s, got, ok)
		}
	}
	for _, bad := range []Marker{
		{"", "db1", tLineage, false},                 // dev mode: no install id → no marker, no backup
		{tInstall, "{{guestname}}", tLineage, false}, // a template name
		{tInstall, "db1\\n", tLineage, false},
		{tInstall, "db1", "short", false},
		{"a:b", "db1", tLineage, false},
	} {
		if _, err := bad.String(); err == nil {
			t.Errorf("marker %+v accepted", bad)
		}
	}
	for _, notes := range []string{"", "plumb", "plumb:" + tInstall + ":db1", "plumb:" + tInstall + ":db1:" + tLineage + ":x",
		"plumb:" + tInstall + ":db1:" + tLineage + ":m:extra", "my notes", "Plumb:" + tInstall + ":db1:" + tLineage} {
		if _, ok := ParseMarker(notes); ok {
			t.Errorf("notes %q parsed as a marker", notes)
		}
	}
}

func vol(i int, notes string) Volume {
	return Volume{Volid: fmt.Sprintf("pb:backup/vzdump-qemu-9100-%02d.vma.zst", i), Time: time.Date(2026, 11, 1+i, 3, 0, 0, 0, time.UTC), Notes: notes}
}

func marker(t *testing.T, m Marker) string {
	s, err := m.String()
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// D-407, D-417: only this install's marker of the Target's current
// lineage is plumb; retention keeps keep scheduled + 2 manual and touches
// nothing else.
func TestClassifyAndRetention(t *testing.T) {
	own := marker(t, Marker{tInstall, "db1", tLineage, false})
	ownM := marker(t, Marker{tInstall, "db1", tLineage, true})
	oldLineage := marker(t, Marker{tInstall, "db1", "fedcba9876543210", false})
	otherInstall := marker(t, Marker{"7f1c2a9e-3b4d-4e5f-8a7b-0c1d2e3f4a5b", "db1", tLineage, false})
	var vols []Volume
	for i := range 5 {
		vols = append(vols, vol(i, own)) // days 1–5 scheduled
	}
	for i := 5; i < 12; i++ {
		vols = append(vols, vol(i, ownM)) // days 6–12 manual (7 "back up now" calls)
	}
	vols = append(vols, vol(12, oldLineage), vol(13, otherInstall), vol(14, "made by a person"), vol(15, ""))
	c := Classify(vols, tInstall, "db1", tLineage)
	kinds := map[string]int{}
	for _, v := range c {
		kinds[v.Kind+"/"+v.Trigger]++
		if v.ID != VolumeID(v.Volid) || len(v.ID) != 16 {
			t.Fatalf("id %q", v.ID)
		}
	}
	want := map[string]int{"plumb/scheduled": 5, "plumb/manual": 7, "older-guest/scheduled": 1, "unmarked/unknown": 3}
	if fmt.Sprint(kinds) != fmt.Sprint(want) {
		t.Fatalf("kinds %v, want %v", kinds, want)
	}
	if !c[0].Time.After(c[len(c)-1].Time) {
		t.Fatal("not newest first")
	}
	del := Retention(c, 3)
	got := map[string]int{}
	for _, v := range del {
		if v.Kind != KindPlumb {
			t.Fatalf("retention deletes a %s volume", v.Kind)
		}
		got[v.Trigger]++
	}
	// 5 scheduled keep 3 → 2 go (the oldest); 7 manual keep 2 → 5 go.
	if got[TriggerScheduled] != 2 || got[TriggerManual] != 5 {
		t.Fatalf("deleted %v", got)
	}
	for _, v := range del {
		if v.Trigger == TriggerScheduled && v.Time.After(vols[1].Time) {
			t.Fatalf("a newer scheduled volume went: %v", v.Time)
		}
	}
	// M1 of #325: keep+1 manual backups never push out a scheduled one.
	if n := LowerKeepDeletes(c, 3); n != 2 {
		t.Fatalf("lower keep deletes %d", n)
	}
	if Retention(c, 0) != nil {
		t.Fatal("keep 0 deletes")
	}
}

func TestVolumeIDStable(t *testing.T) {
	a1, a2, b := VolumeID("a"), VolumeID("a"), VolumeID("b")
	if a1 != a2 || a1 == b {
		t.Fatal("unstable id")
	}
}
