package proxmox

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"
)

// Guest backups (T-221; D-405 … D-409, D-417, ARCHITECTURE §20). The
// driver only calls vzdump, lists, reads the storage and deletes one
// volume; which volume is Plumb's is decided above it by the notes
// marker. Rights: VM.Backup through the pool, Datastore.AllocateSpace and
// Datastore.Audit on the one guest backup storage (D-405). Never sent:
// prune-backups, protected, bwlimit, ionice, performance, tmpdir,
// dumpdir, script, job-id (T-214: more rights or root).

// BackupVolume is one backup volume as Proxmox lists it. Notes is the
// first line of its notes only (the marker).
type BackupVolume struct {
	Volid string
	CTime int64
	Size  int64
	Notes string
}

// BackupStorage is the state of the guest backup storage, from the
// storage index (Datastore.Audit, T-214 FACT 2) and its status.
type BackupStorage struct {
	Type      string // pbs, dir, nfs …
	Encrypted bool   // type pbs with encryption-key set (client-side)
	FreePct   int    // -1: unknown
	Backup    bool   // content holds backup
}

// ErrNoBackupStorage: the storage is not in the token's storage index (a
// wrong name, or no Audit right on it).
var ErrNoBackupStorage = errors.New("proxmox: the guest backup storage is not visible to the token")

// ErrBackupNotFound: the volume is not a backup of this guest.
var ErrBackupNotFound = errors.New("proxmox: no such backup of this guest")

type storageEntry struct {
	Storage       string `json:"storage"`
	Type          string `json:"type"`
	Content       string `json:"content"`
	EncryptionKey string `json:"encryption-key"`
}

func (d *Driver) storageIndex(ctx context.Context) ([]storageEntry, error) {
	var list []storageEntry
	if err := d.c.Get(ctx, "/storage", nil, &list); err != nil {
		return nil, err
	}
	return list, nil
}

func hasContent(content, kind string) bool {
	return slices.Contains(strings.Split(content, ","), kind)
}

// BackupStorageInfo reads the state of storage.
func (d *Driver) BackupStorageInfo(ctx context.Context, storage string) (BackupStorage, error) {
	list, err := d.storageIndex(ctx)
	if err != nil {
		return BackupStorage{}, err
	}
	i := slices.IndexFunc(list, func(e storageEntry) bool { return e.Storage == storage })
	if i < 0 {
		return BackupStorage{}, ErrNoBackupStorage
	}
	e := list[i]
	// encryption-key holds the key fingerprint (or 1) when client-side
	// encryption is set; the key itself never leaves /etc/pve/priv (T-214).
	enc := e.Type == "pbs" && e.EncryptionKey != "" && e.EncryptionKey != "0"
	out := BackupStorage{Type: e.Type, Encrypted: enc, FreePct: -1, Backup: hasContent(e.Content, "backup")}
	var st struct {
		Total int64 `json:"total"`
		Avail int64 `json:"avail"`
	}
	if err := d.c.Get(ctx, "/nodes/"+url.PathEscape(d.cfg.Node)+"/storage/"+url.PathEscape(storage)+"/status", nil, &st); err == nil && st.Total > 0 {
		out.FreePct = int(st.Avail * 100 / st.Total)
	}
	return out, nil
}

// OtherBackupStorages lists the storages in the token's index, other than
// except, whose content holds backup (M2 of #321): with VM.Backup the
// token could write a vzdump there.
func (d *Driver) OtherBackupStorages(ctx context.Context, except string) ([]string, error) {
	list, err := d.storageIndex(ctx)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range list {
		if e.Storage != except && hasContent(e.Content, "backup") {
			out = append(out, e.Storage)
		}
	}
	slices.Sort(out)
	return out, nil
}

// ListGuestBackups lists the backup volumes of a pool guest on storage.
// Proxmox lists only volumes whose owner VMID the token may back up (the
// pool now, T-214 FACT 1).
func (d *Driver) ListGuestBackups(ctx context.Context, storage, guest string) ([]BackupVolume, error) {
	id, _, err := d.own(ctx, guest)
	if err != nil {
		return nil, err
	}
	return d.listBackups(ctx, storage, id)
}

func (d *Driver) listBackups(ctx context.Context, storage string, id int) ([]BackupVolume, error) {
	var raw []struct {
		Volid string `json:"volid"`
		CTime int64  `json:"ctime"`
		Size  int64  `json:"size"`
		Notes string `json:"notes"`
		VMID  any    `json:"vmid"`
	}
	params := url.Values{"content": {"backup"}, "vmid": {strconv.Itoa(id)}}
	if err := d.c.Get(ctx, "/nodes/"+url.PathEscape(d.cfg.Node)+"/storage/"+url.PathEscape(storage)+"/content", params, &raw); err != nil {
		return nil, err
	}
	out := make([]BackupVolume, 0, len(raw))
	for _, v := range raw {
		// Proxmox filters by vmid already; a volume of another VMID is
		// dropped anyway (the filter is a query parameter).
		if fmt.Sprint(v.VMID) != strconv.Itoa(id) {
			continue
		}
		notes, _, _ := strings.Cut(v.Notes, "\n")
		out = append(out, BackupVolume{Volid: v.Volid, CTime: v.CTime, Size: v.Size, Notes: notes})
	}
	return out, nil
}

// BackupResult is the result of one vzdump.
type BackupResult struct {
	Volid string
	// Warned: the task ended with warnings (vzdump only warns when the
	// notes cannot be written, T-214): the caller reads the marker back.
	Warned bool
}

// BackUpGuest runs one vzdump of a pool guest to storage, with notes as
// the notes-template (the marker; one line, checked by the caller), in
// snapshot mode. For a container, excludePaths are left out of the
// archive (L1 of #321). It finds the new volume by the list before and
// after the task.
func (d *Driver) BackUpGuest(ctx context.Context, storage, guest, notes string, excludePaths []string) (BackupResult, error) {
	if notes == "" || strings.ContainsAny(notes, "\n\\{}") {
		return BackupResult{}, fmt.Errorf("proxmox: bad notes for vzdump")
	}
	id, m, err := d.own(ctx, guest)
	if err != nil {
		return BackupResult{}, err
	}
	before, err := d.listBackups(ctx, storage, id)
	if err != nil {
		return BackupResult{}, err
	}
	params := url.Values{
		"vmid":           {strconv.Itoa(id)},
		"storage":        {storage},
		"mode":           {"snapshot"},
		"compress":       {"zstd"},
		"notes-template": {notes},
	}
	if m.Type == typeCT {
		for _, p := range excludePaths {
			params.Add("exclude-path", p)
		}
	}
	res := BackupResult{}
	err = d.c.Task(ctx, d.cfg.Node, "POST", "/nodes/"+url.PathEscape(d.cfg.Node)+"/vzdump", params)
	var te *TaskError
	if errors.As(err, &te) && strings.HasPrefix(te.ExitStatus, "WARNINGS") {
		res.Warned, err = true, nil
	}
	if err != nil {
		return BackupResult{}, err
	}
	after, err := d.listBackups(ctx, storage, id)
	if err != nil {
		return BackupResult{}, err
	}
	var made []string
	for _, v := range after {
		if !slices.ContainsFunc(before, func(b BackupVolume) bool { return b.Volid == v.Volid }) {
			made = append(made, v.Volid)
		}
	}
	if len(made) != 1 {
		return BackupResult{}, fmt.Errorf("proxmox: vzdump of VMID %d made %d new volumes, want 1", id, len(made))
	}
	res.Volid = made[0]
	return res, nil
}

// DeleteGuestBackup deletes one backup volume of a pool guest. The volume
// must be in the guest's list (so never a volume of another VMID).
func (d *Driver) DeleteGuestBackup(ctx context.Context, storage, guest, volid string) error {
	id, _, err := d.own(ctx, guest)
	if err != nil {
		return err
	}
	vols, err := d.listBackups(ctx, storage, id)
	if err != nil {
		return err
	}
	if !slices.ContainsFunc(vols, func(v BackupVolume) bool { return v.Volid == volid }) {
		return ErrBackupNotFound
	}
	return d.c.MaybeTask(ctx, d.cfg.Node, "DELETE",
		"/nodes/"+url.PathEscape(d.cfg.Node)+"/storage/"+url.PathEscape(storage)+"/content/"+url.PathEscape(volid), nil)
}
