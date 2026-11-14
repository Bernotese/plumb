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
	rec := map[string]Recorded{}
	for i, v := range vols {
		switch {
		case i < 5:
			rec[v.Volid] = Recorded{tLineage, TriggerScheduled}
		case i < 12:
			rec[v.Volid] = Recorded{tLineage, TriggerManual}
		case i == 12:
			rec[v.Volid] = Recorded{"fedcba9876543210", TriggerScheduled}
		case i == 13:
			rec[v.Volid] = Recorded{tLineage, TriggerScheduled} // another install's marker: never ours
		}
	}
	c := Classify(vols, tInstall, "db1", tLineage, rec)
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
	// keep 0 (no backup field): no scheduled volume goes, the manual cap
	// holds (M3 of #331).
	for _, v := range Retention(c, 0) {
		if v.Trigger != TriggerManual {
			t.Fatalf("keep 0 deletes a %s volume", v.Trigger)
		}
	}
	if n := len(Retention(c, 0)); n != 5 {
		t.Fatalf("keep 0 deletes %d manual, want 5", n)
	}
}

// M1 of #331: notes alone prove nothing. keep newer volumes with a copied
// marker (a .notes file, a PBS comment) and no record leave every
// recorded volume; a record whose trigger or Target the marker does not
// match is never Plumb's (n1: classify by Target name).
func TestClassifyForgedMarkers(t *testing.T) {
	own := marker(t, Marker{tInstall, "db1", tLineage, false})
	var vols []Volume
	rec := map[string]Recorded{}
	for i := range 3 {
		v := vol(i, own)
		vols = append(vols, v)
		rec[v.Volid] = Recorded{tLineage, TriggerScheduled}
	}
	for i := 10; i < 13; i++ { // newer forgeries, not recorded
		vols = append(vols, vol(i, own))
	}
	trig := vol(20, marker(t, Marker{tInstall, "db1", tLineage, true})) // marker says manual, record says scheduled
	rec[trig.Volid] = Recorded{tLineage, TriggerScheduled}
	other := vol(21, marker(t, Marker{tInstall, "db2", tLineage, false})) // another Target's name, recorded here
	rec[other.Volid] = Recorded{tLineage, TriggerScheduled}
	vols = append(vols, trig, other)
	c := Classify(vols, tInstall, "db1", tLineage, rec)
	plumb := 0
	for _, v := range c {
		if v.Kind == KindPlumb {
			plumb++
			if _, ok := rec[v.Volid]; !ok || v.Volid == trig.Volid || v.Volid == other.Volid {
				t.Fatalf("not ours but plumb: %s", v.Volid)
			}
		}
	}
	if plumb != 3 {
		t.Fatalf("%d plumb volumes, want the 3 recorded", plumb)
	}
	if del := Retention(c, 3); len(del) != 0 {
		t.Fatalf("forgeries pushed out recorded volumes: %v", del)
	}
}

func TestVolumeIDStable(t *testing.T) {
	a1, a2, b := VolumeID("a"), VolumeID("a"), VolumeID("b")
	if a1 != a2 || a1 == b {
		t.Fatal("unstable id")
	}
}
