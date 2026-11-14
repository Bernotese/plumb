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
	KindPlumb      = "plumb"       // this install, this Target, its current lineage
	KindOlderGuest = "older-guest" // this install's marker of another lineage or Target
	KindUnmarked   = "unmarked"    // no Plumb marker: never touched
)

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
func Classify(vols []Volume, install, target, lineage string) []Classified {
	out := make([]Classified, 0, len(vols))
	for _, v := range vols {
		c := Classified{Volume: v, ID: VolumeID(v.Volid), Kind: KindUnmarked, Trigger: TriggerUnknown}
		if m, ok := ParseMarker(v.Notes); ok && m.Install == install {
			c.Trigger = TriggerScheduled
			if m.Manual {
				c.Trigger = TriggerManual
			}
			c.Kind = KindOlderGuest
			if m.Target == target && m.Lineage == lineage {
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
func Retention(vols []Classified, keep int) []Classified {
	if keep < 1 {
		return nil // a missing or broken keep deletes nothing
	}
	var del []Classified
	sched, manual := 0, 0
	for _, v := range vols { // newest first
		if v.Kind != KindPlumb {
			continue
		}
		switch v.Trigger {
		case TriggerScheduled:
			if sched++; sched > keep {
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
