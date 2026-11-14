package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"time"

	"github.com/Bernotese/plumb/internal/backup"
)

// Controller settings (T-174, D-361): GET /settings for every caller with
// tier read, the proxmox part only for a human session; PATCH /settings
// for a human admin session.

// ProxmoxView is the read-only Proxmox part of the settings. serve fills it
// from the driver; nil fields are JSON null.
type ProxmoxView struct {
	Node     *string `json:"node"`
	Pool     *string `json:"pool"`
	Storages struct {
		Images   *string `json:"images"`
		Snippets *string `json:"snippets"`
		Backup   *string `json:"backup"`
	} `json:"storages"`
	API struct {
		Host      *string `json:"host"`
		Port      *int    `json:"port"`
		TLSSHA256 *string `json:"tls_sha256"`
	} `json:"api"`
	Version   *string       `json:"version"`
	TokenUser *string       `json:"token_user"`
	LastOKAt  *time.Time    `json:"last_ok_at"`
	ACL       string        `json:"acl"`
	ACLError  string        `json:"acl_error"`
	ACLLines  []ACLLineView `json:"acl_lines"`
	// ExtraNets and GuestBackup are the install settings of slice 7
	// (D-401, D-405, D-406, T-218). GuestBackup is null without a guest
	// backup storage.
	ExtraNets   []ExtraNetView   `json:"extra_nets"`
	GuestBackup *GuestBackupView `json:"guest_backup"`
	// ACLExtra are rights of the token beyond the lines of the install
	// (review L4 of #330): path and role only.
	ACLExtra []ACLExtraView `json:"acl_extra"`
}

// ACLExtraView is a right of the token that the install does not name.
type ACLExtraView struct {
	Path string `json:"path"`
	Role string `json:"role"`
}

// ACLLineView is one ACL line of the install.
type ACLLineView struct {
	Path string `json:"path"`
	Role string `json:"role"`
	OK   bool   `json:"ok"`
}

// ExtraNetView is one extra bridge (tag null) or bridge/tag of the install.
type ExtraNetView struct {
	Bridge string `json:"bridge"`
	Tag    *int   `json:"tag"`
}

// GuestBackupView is the guest backup storage of the install: its state
// (pbs-key, stated-clear, clear-refused) and the other storages on which
// the token could write a vzdump (M2 of #321; must be empty).
type GuestBackupView struct {
	Storage             string   `json:"storage"`
	State               string   `json:"state"`
	OtherBackupStorages []string `json:"other_backup_storages"`
	DiskContent         []string `json:"disk_content"`
}

// noProxmox is the view of a controller without Proxmox (serve --dev).
func noProxmox() *ProxmoxView {
	return &ProxmoxView{ACL: "unknown", ACLError: "This controller runs without Proxmox (dev mode).", ACLLines: []ACLLineView{}, ExtraNets: []ExtraNetView{}, ACLExtra: []ACLExtraView{}}
}

type settingsView struct {
	Backup struct {
		Time   string `json:"time"`
		Keep   int    `json:"keep"`
		Source struct {
			Time string `json:"time"`
			Keep string `json:"keep"`
		} `json:"source"`
	} `json:"backup"`
	Proxmox *ProxmoxView `json:"proxmox"`
	// RepairKey is the public repair key view (T-191); missing without one.
	RepairKey json.RawMessage `json:"repair_key,omitempty"`
}

func (s *Server) settingsOf(ctx context.Context, p *principal) settingsView {
	var v settingsView
	sc := s.cfg.Backups.ScheduleInfo()
	v.Backup.Time, v.Backup.Keep = sc.Time, sc.Keep
	v.Backup.Source.Time, v.Backup.Source.Keep = sc.TimeSource, sc.KeepSource
	v.RepairKey = s.repairKeyView(ctx)
	// Host data only for a person: scripts and MCP clients need none
	// (review n2 of #276).
	if p.human() {
		if s.cfg.Proxmox != nil {
			v.Proxmox = s.cfg.Proxmox(ctx)
		}
		if v.Proxmox == nil {
			v.Proxmox = noProxmox()
		}
		if v.Proxmox.ACLLines == nil {
			v.Proxmox.ACLLines = []ACLLineView{}
		}
		if v.Proxmox.ExtraNets == nil {
			v.Proxmox.ExtraNets = []ExtraNetView{}
		}
		if v.Proxmox.ACLExtra == nil {
			v.Proxmox.ACLExtra = []ACLExtraView{}
		}
		if g := v.Proxmox.GuestBackup; g != nil {
			if g.OtherBackupStorages == nil {
				g.OtherBackupStorages = []string{}
			}
			if g.DiskContent == nil {
				g.DiskContent = []string{}
			}
		}
		// The backup storage is known only from the status file of the
		// host copy timer (T-208, host finding F1): null before its first
		// run, or when the file is bad.
		if s.cfg.Backups != nil {
			if name := s.cfg.Backups.CopyState().Storage; name != "" {
				v.Proxmox.Storages.Backup = &name
			}
		}
	}
	return v
}

func (s *Server) getSettings(w http.ResponseWriter, r *http.Request, p *principal) {
	if s.noBackups(w) {
		return
	}
	writeJSON(w, http.StatusOK, s.settingsOf(r.Context(), p))
}

// changeSettingsRequest keeps a field that is absent (nil) apart from a
// field that is null ("null"): null removes the stored value.
type changeSettingsRequest struct {
	Backup *struct {
		Time json.RawMessage `json:"time"`
		Keep json.RawMessage `json:"keep"`
	} `json:"backup"`
}

var reBackupTime = regexp.MustCompile(`^([01][0-9]|2[0-3]):[0-5][0-9]$`)

func (s *Server) changeSettings(w http.ResponseWriter, r *http.Request, p *principal) {
	if s.noBackups(w) {
		return
	}
	var req changeSettingsRequest
	if !decode(w, r, &req, false) {
		return
	}
	if req.Backup == nil || (req.Backup.Time == nil && req.Backup.Keep == nil) {
		problem(w, http.StatusBadRequest, "bad-request", "Bad request", "Give backup.time, backup.keep or both.")
		return
	}
	// D-325: the errors name the field, never the value.
	var c backup.ScheduleChange
	var errs []fieldError
	if raw := req.Backup.Time; raw != nil {
		c.SetTime = true
		if string(raw) != "null" {
			var t string
			if json.Unmarshal(raw, &t) != nil || !reBackupTime.MatchString(t) {
				errs = append(errs, fieldError{"/backup/time", "time is HH:MM in UTC, 00:00 … 23:59, or null."})
			} else {
				c.Time = &t
			}
		}
	}
	if raw := req.Backup.Keep; raw != nil {
		c.SetKeep = true
		if string(raw) != "null" {
			n, err := strconv.Atoi(string(raw))
			if err != nil || n < 1 || n > backup.MaxKeep {
				errs = append(errs, fieldError{"/backup/keep", "keep is a whole number from 1 to " + strconv.Itoa(backup.MaxKeep) + ", or null."})
			} else {
				c.Keep = &n
			}
		}
	}
	if len(errs) > 0 {
		writeProblem(w, problemBody{Type: "urn:plumb:problem:validation", Title: "Validation failed",
			Status: http.StatusUnprocessableEntity, Errors: errs})
		return
	}
	old, next, err := s.cfg.Backups.ChangeSchedule(r.Context(), p.name, c)
	if errors.Is(err, backup.ErrBadSchedule) {
		writeProblem(w, problemBody{Type: "urn:plumb:problem:validation", Title: "Validation failed", Status: http.StatusUnprocessableEntity})
		return
	}
	if err != nil {
		s.internal(w, err)
		return
	}
	s.settingsEvent(r, p, old, next)
	writeJSON(w, http.StatusOK, s.settingsOf(r.Context(), p))
}

// settingsEvent emits settings.changed with the old and new values of the
// fields that changed (value or source). No secret exists in them.
func (s *Server) settingsEvent(r *http.Request, p *principal, old, next backup.Schedule) {
	if s.cfg.Events == nil {
		return
	}
	o, n := map[string]any{}, map[string]any{}
	if old.Time != next.Time || old.TimeSource != next.TimeSource {
		o["time"], n["time"] = old.Time, next.Time
	}
	if old.Keep != next.Keep || old.KeepSource != next.KeepSource {
		o["keep"], n["keep"] = old.Keep, next.Keep
	}
	if len(n) == 0 {
		return
	}
	if _, err := s.cfg.Events.Emit(r.Context(), "settings.changed", "settings:backup", p.name, map[string]any{"old": o, "new": n}); err != nil {
		s.log.Error("settings event", "err", err)
	}
}
