package api

import (
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/Bernotese/plumb/internal/backup"
	"github.com/Bernotese/plumb/internal/store"
)

// Guest backups (T-221; D-405 … D-409, D-417, ARCHITECTURE §20). A token
// reads no notes and no volume id: only Plumb's id of a volume.

var reBackupID = regexp.MustCompile(`^[0-9a-f]{16}$`)

type guestBackupStorageView struct {
	ID      string `json:"id"`
	State   string `json:"state"`
	FreePct *int   `json:"free_pct"`
}

type guestBackupView struct {
	ID      string    `json:"id"`
	Time    time.Time `json:"time"`
	Size    int64     `json:"size"`
	Kind    string    `json:"kind"`
	Trigger string    `json:"trigger"`
	Mode    string    `json:"mode"`
}

type guestBackupLastView struct {
	At      time.Time `json:"at"`
	Result  string    `json:"result"`
	Message string    `json:"message"`
}

type guestBackupListView struct {
	Storage *guestBackupStorageView `json:"storage"`
	Items   []guestBackupView       `json:"items"`
	NextAt  *time.Time              `json:"next_at"`
	Last    *guestBackupLastView    `json:"last"`
}

// noGuestBackups answers for a controller without guest backup jobs
// (serve --dev, no Proxmox): like a Target without a storage. It writes
// the answer and returns true; an unknown Target is 404 first.
func (s *Server) noGuestBackups(w http.ResponseWriter, r *http.Request, empty func()) bool {
	if s.cfg.GuestBackups != nil {
		return false
	}
	if _, err := s.headTarget(r.Context(), r.PathValue("name")); err != nil {
		s.guestBackupProblem(w, err)
		return true
	}
	empty()
	return true
}

func (s *Server) guestBackupProblem(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		problem(w, http.StatusNotFound, "not-found", "Not found", "No such Target with a guest.")
	case errors.Is(err, backup.ErrGuestBackupBusy):
		problem(w, http.StatusConflict, "backup-running", "A backup is running", "A backup of this Target runs. Wait for guest_backup.done.")
	case errors.Is(err, backup.ErrGuestBackupRefused):
		problem(w, http.StatusConflict, "backup-refused", "Backup refused", strings.TrimPrefix(err.Error(), "backup: guest backup refused: "))
	case errors.Is(err, backup.ErrGuestBackupNotFound):
		problem(w, http.StatusNotFound, "not-found", "Not found", "No such backup of this Target.")
	case errors.Is(err, backup.ErrNotPlumbBackup):
		problem(w, http.StatusConflict, "not-plumb", "Not a Plumb backup", "This backup has no Plumb marker: Plumb never deletes it. Delete it in Proxmox.")
	default:
		s.internal(w, err)
	}
}

func (s *Server) listGuestBackups(w http.ResponseWriter, r *http.Request, _ *principal) {
	if s.noGuestBackups(w, r, func() { writeJSON(w, http.StatusOK, guestBackupListView{Items: []guestBackupView{}}) }) {
		return
	}
	l, err := s.cfg.GuestBackups.List(r.Context(), r.PathValue("name"))
	if err != nil {
		s.guestBackupProblem(w, err)
		return
	}
	v := guestBackupListView{Items: []guestBackupView{}}
	if l.Storage != nil {
		sv := &guestBackupStorageView{ID: l.Storage.ID, State: l.Storage.State}
		if l.Storage.FreePct >= 0 {
			f := l.Storage.FreePct
			sv.FreePct = &f
		}
		v.Storage = sv
	}
	for _, it := range l.Items {
		v.Items = append(v.Items, guestBackupView{ID: it.ID, Time: it.Time.UTC(), Size: it.Size, Kind: it.Kind, Trigger: it.Trigger, Mode: it.Mode})
	}
	if !l.NextAt.IsZero() {
		n := l.NextAt.UTC()
		v.NextAt = &n
	}
	if l.Last != nil {
		at := l.Last.FinishedAt
		if at.IsZero() {
			at = l.Last.StartedAt
		}
		v.Last = &guestBackupLastView{At: at.UTC(), Result: l.Last.Result, Message: l.Last.Message}
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *Server) backUpGuestNow(w http.ResponseWriter, r *http.Request, p *principal) {
	if s.restoredMode(w) || s.noGuestBackups(w, r, func() {
		problem(w, http.StatusConflict, "backup-refused", "Backup refused", "the install named no guest backup storage")
	}) {
		return
	}
	var req struct {
		Reason string `json:"reason"`
	}
	if !decode(w, r, &req, true) {
		return
	}
	name := r.PathValue("name")
	run, err := s.cfg.GuestBackups.Start(r.Context(), name, backup.TriggerManual, p.name)
	if err != nil {
		s.guestBackupProblem(w, err)
		return
	}
	s.cfg.GuestBackups.Background(name, run)
	w.WriteHeader(http.StatusAccepted)
}

func (s *Server) deleteGuestBackup(w http.ResponseWriter, r *http.Request, p *principal) {
	if s.noGuestBackups(w, r, func() { problem(w, http.StatusNotFound, "not-found", "Not found", "No such backup of this Target.") }) {
		return
	}
	id := r.PathValue("backup")
	if !reBackupID.MatchString(id) {
		problem(w, http.StatusNotFound, "not-found", "Not found", "No such backup of this Target.")
		return
	}
	if err := s.cfg.GuestBackups.Delete(r.Context(), r.PathValue("name"), id, p.name); err != nil {
		s.guestBackupProblem(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
