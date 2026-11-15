package backup

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Guest backups (T-221; D-405 … D-409, D-417, ARCHITECTURE §20): the
// notes marker of a Plumb backup, the kind of a volume and retention.
// Pure functions; the job runner calls them.

// Volume kinds (GuestBackup.kind).
const (
	KindPlumb      = "plumb"       // recorded by this controller: this Target, its current lineage
	KindOlderGuest = "older-guest" // recorded by this controller: this Target, an older lineage
	KindUnmarked   = "unmarked"    // not recorded by this controller (no marker, a marker without a job record): never touched
)

// Recorded is a volume that a finished job of this controller made (its
// run row): its lineage and trigger (M1 of #331).
type Recorded struct {
	Lineage, Trigger string
	// Size and CTime of the volume when the job read it back (F2 of
	// #331): a replaced archive of another size or time is never Plumb's.
	Size  int64
	CTime int64 // Unix seconds
}

// Triggers (GuestBackup.trigger).
const (
	TriggerScheduled = "scheduled"
	TriggerManual    = "manual"
	TriggerUnknown   = "unknown"
)

// MaxManualKept: retention keeps at most this many manual backups beside
// the `keep` scheduled ones (D-417), so "back up now" never pushes out a
// scheduled backup.
const MaxManualKept = 2

// Marker is the parsed notes marker plumb:<install>:<target>:<lineage>[:m].
type Marker struct {
	Install, Target, Lineage string
	Manual                   bool
}

var (
	reInstall = regexp.MustCompile(`^[A-Za-z0-9-]{1,64}$`)
	reTarget  = regexp.MustCompile(`^[a-z0-9]([a-z0-9.-]{0,61}[a-z0-9])?$`)
	reLineage = regexp.MustCompile(`^[0-9a-f]{16}$`)
)

// ErrBadMarker: a part of the marker would not survive vzdump's
// notes-template (one line; only {{…}} names and \n, \\ escapes, T-214).
var ErrBadMarker = errors.New("backup: bad marker part")

// String is the notes-template for vzdump. Its parts never hold '{', '\'
// or a line break, so vzdump writes it as it is.
func (m Marker) String() (string, error) {
	if !reInstall.MatchString(m.Install) || !reTarget.MatchString(m.Target) || !reLineage.MatchString(m.Lineage) {
		return "", ErrBadMarker
	}
	s := "plumb:" + m.Install + ":" + m.Target + ":" + m.Lineage
	if m.Manual {
		s += ":m"
	}
	return s, nil
}

// ParseMarker reads the first line of a volume's notes (Proxmox lists
// only the first line). ok is false for anything that is not exactly a
// Plumb marker.
func ParseMarker(notes string) (Marker, bool) {
	line, _, _ := strings.Cut(notes, "\n")
	parts := strings.Split(strings.TrimSpace(line), ":")
	if len(parts) < 4 || len(parts) > 5 || parts[0] != "plumb" {
		return Marker{}, false
	}
	m := Marker{Install: parts[1], Target: parts[2], Lineage: parts[3]}
	if len(parts) == 5 {
		if parts[4] != "m" {
			return Marker{}, false
		}
		m.Manual = true
	}
	if _, err := m.String(); err != nil {
		return Marker{}, false
	}
	return m, true
}

// VolumeID is Plumb's id of a volume (GuestBackup.id): the volume id never
// goes into a URL or to a token.
func VolumeID(volid string) string {
	h := sha256.Sum256([]byte("plumb-guest-backup\x00" + volid))
	return hex.EncodeToString(h[:8])
}

// Volume is one backup volume as Proxmox lists it.
type Volume struct {
	Volid string
	Time  time.Time
	Size  int64
	Notes string
}

// Classified is a Volume with its kind and trigger for one Target.
type Classified struct {
	Volume
	ID      string
	Kind    string
	Trigger string
}

// Classify sorts the volumes of a Target's guest by kind, newest first.
// A volume is Plumb's only when a finished job of this controller
// recorded it (recorded, by volume id) and its marker agrees with that
// record: this install, this Target, the recorded lineage and trigger.
// Notes alone prove nothing: anyone who can write the storage (a .notes
// file, a PBS comment) can copy a marker (M1 of #331). A volume whose
// record was lost (a controller restore) is unmarked, so the storage
// grows until a person deletes by hand: the safe direction.
func Classify(vols []Volume, install, target, lineage string, recorded map[string]Recorded) []Classified {
	out := make([]Classified, 0, len(vols))
	for _, v := range vols {
		c := Classified{Volume: v, ID: VolumeID(v.Volid), Kind: KindUnmarked, Trigger: TriggerUnknown}
		m, ok := ParseMarker(v.Notes)
		r, rec := recorded[v.Volid]
		if ok && rec && m.Install == install && m.Target == target && m.Lineage == r.Lineage && m.Manual == (r.Trigger == TriggerManual) &&
			v.Size == r.Size && v.Time.Unix() == r.CTime {
			c.Trigger = r.Trigger
			c.Kind = KindOlderGuest
			if r.Lineage == lineage {
				c.Kind = KindPlumb
			}
		}
		out = append(out, c)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Time.After(out[j].Time) })
	return out
}

// Retention returns the volumes to delete: of kind plumb only, the
// scheduled ones above keep and the manual ones above MaxManualKept,
// oldest first among each. Nothing else is ever returned (D-407, D-417).
// keep < 1 (no backup field): no scheduled volume goes, but the manual
// cap holds (M3 of #331).
func Retention(vols []Classified, keep int) []Classified {
	var del []Classified
	sched, manual := 0, 0
	for _, v := range vols { // newest first
		if v.Kind != KindPlumb {
			continue
		}
		switch v.Trigger {
		case TriggerScheduled:
			if sched++; keep >= 1 && sched > keep {
				del = append(del, v)
			}
		case TriggerManual:
			if manual++; manual > MaxManualKept {
				del = append(del, v)
			}
		}
	}
	return del
}

// LowerKeepDeletes is the number of volumes that the next retention
// deletes when keep goes from old to new (a Plan's diff, D-417): the
// scheduled plumb volumes above new.
func LowerKeepDeletes(vols []Classified, newKeep int) int {
	n := 0
	for _, v := range vols {
		if v.Kind == KindPlumb && v.Trigger == TriggerScheduled {
			n++
		}
	}
	return max(n-newKeep, 0)
}
