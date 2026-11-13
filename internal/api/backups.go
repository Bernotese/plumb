package api

import (
	"errors"
	"net/http"
	"time"

	"github.com/Bernotese/plumb/internal/backup"
	"github.com/Bernotese/plumb/internal/store"
)

// --- backups (T-112; D-288, D-302) ---

type backupRunView struct {
	StartedAt  time.Time  `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at"`
	Result     string     `json:"result"`
	By         string     `json:"by"`
	Name       *string    `json:"name"`
	Message    string     `json:"message"`
}

type backupView struct {
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
	Size      int64     `json:"size"`
	SHA256    string    `json:"sha256"`
}

type backupCopyView struct {
	State   string     `json:"state"`
	At      *time.Time `json:"at"`
	Name    *string    `json:"name"`
	Storage *string    `json:"storage"`
	Message string     `json:"message"`
}

type backupListView struct {
	Schedule struct {
		Time string `json:"time"`
		Keep int    `json:"keep"`
	} `json:"schedule"`
	Running bool           `json:"running"`
	LastRun *backupRunView `json:"last_run"`
	Items   []backupView   `json:"items"`
	Copy    backupCopyView `json:"copy"`
}

func backupRunOf(r store.BackupRun) *backupRunView {
	v := &backupRunView{StartedAt: r.StartedAt.UTC(), FinishedAt: utc(r.FinishedAt), Result: r.Result, By: r.By, Message: r.Message}
	// The name is set when the file was written.
	if r.Result == store.BackupOK {
		name := r.Name
		v.Name = &name
	}
	return v
}

func optString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func (s *Server) noBackups(w http.ResponseWriter) bool {
	if s.cfg.Backups == nil {
		problem(w, http.StatusServiceUnavailable, "unavailable", "Backups are off", "This controller runs without backups.")
		return true
	}
	return false
}

func (s *Server) listBackups(w http.ResponseWriter, r *http.Request, _ *principal) {
	if s.noBackups(w) {
		return
	}
	b := s.cfg.Backups
	v := backupListView{Items: []backupView{}}
	v.Schedule.Time, v.Schedule.Keep = b.Schedule()
	last, err := s.st.LastBackupRun(r.Context(), "")
	switch {
	case errors.Is(err, store.ErrNotFound):
	case err != nil:
		s.internal(w, err)
		return
	default:
		v.LastRun = backupRunOf(last)
		v.Running = last.Result == store.BackupRunning
	}
	items, err := b.Items()
	if err != nil {
		s.internal(w, err)
		return
	}
	for _, it := range items {
		v.Items = append(v.Items, backupView{Name: it.Name, CreatedAt: it.CreatedAt, Size: it.Size, SHA256: it.SHA256})
	}
	c := b.CopyState()
	v.Copy = backupCopyView{State: c.State, Message: c.Message, Name: optString(c.Name), Storage: optString(c.Storage)}
	if !c.At.IsZero() {
		at := c.At
		v.Copy.At = &at
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *Server) createBackup(w http.ResponseWriter, r *http.Request, p *principal) {
	if s.noBackups(w) || s.restoredMode(w) {
		return
	}
	run, err := s.cfg.Backups.Start(r.Context(), p.name)
	if errors.Is(err, backup.ErrRunning) {
		problem(w, http.StatusConflict, "backup-running", "A backup is running", "Wait for backup.finished, then start again.")
		return
	}
	if err != nil {
		s.internal(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, backupRunOf(run))
}
