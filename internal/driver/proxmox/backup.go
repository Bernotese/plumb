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

// ErrManyNewVolumes: the list after a vzdump has not exactly one new
// volume of the guest (for example a person's vzdump at the same time,
// L1 of #331). Plumb's volume stays.
var ErrManyNewVolumes = errors.New("proxmox: not exactly one new backup volume")

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
	// UPID is the vzdump task, also when the result is an error (T-248).
	UPID string
}

// BackUpGuest runs one vzdump of a pool guest to storage, with notes as
// the notes-template (the marker; one line, checked by the caller), in
// snapshot mode. For a container, excludePaths are left out of the
// archive (L1 of #321). It finds the new volume by the list before and
// after the task. onTask (may be nil) gets the task's UPID as soon as the
// task started, before the wait (T-248: a controller stop still knows it).
func (d *Driver) BackUpGuest(ctx context.Context, storage, guest, notes string, excludePaths []string, onTask func(upid string)) (BackupResult, error) {
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
	upid, err := d.c.start(ctx, "POST", "/nodes/"+url.PathEscape(d.cfg.Node)+"/vzdump", params)
	if err == nil && upid == "" {
		err = errors.New("proxmox: vzdump answered without a task") // as Client.Task (review T-012 #3)
	}
	if err != nil {
		return BackupResult{}, err
	}
	res.UPID = upid
	if onTask != nil {
		onTask(upid)
	}
	err = d.c.WaitTask(ctx, d.cfg.Node, upid)
	var te *TaskError
	if errors.As(err, &te) && strings.HasPrefix(te.ExitStatus, "WARNINGS") {
		res.Warned, err = true, nil
	}
	if err != nil {
		return BackupResult{UPID: upid}, err
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
		return BackupResult{UPID: upid}, fmt.Errorf("%w: vzdump of VMID %d made %d", ErrManyNewVolumes, id, len(made))
	}
	res.Volid = made[0]
	return res, nil
}

// BackupTask is what a vzdump task of a pool guest says about its volume
// (T-248).
type BackupTask struct {
	Running bool // not stopped yet: ask again later
	// OK: the task ended OK or with warnings. Any other end: the archive
	// may be incomplete, so the caller never claims it.
	OK     bool
	Status string // the exit status ("" while running)
	// Volid is the one volume of the guest whose archive name the task log
	// names ("INFO: creating … archive '…'"); "" when the log names none,
	// or more than one volume matches.
	Volid string
}

// ErrNotGuestTask: the UPID is not a vzdump task of this node and VMID.
var ErrNotGuestTask = errors.New("proxmox: not a vzdump task of this guest")

// GuestBackupTask reads a vzdump task of a pool guest: its state and the
// volume that its log names (T-248). The UPID must be a vzdump task of
// this node for exactly this VMID. The token reads its own task's status
// and log (it started the task; T-247 proves it on the host).
func (d *Driver) GuestBackupTask(ctx context.Context, storage, guest, upid string) (BackupTask, error) {
	id, _, err := d.own(ctx, guest)
	if err != nil {
		return BackupTask{}, err
	}
	// UPID:<node>:<pid>:<pstart>:<starttime>:<type>:<id>:<user>:
	f := strings.Split(upid, ":")
	if len(f) < 8 || f[0] != "UPID" || f[1] != d.cfg.Node || f[5] != "vzdump" || f[6] != strconv.Itoa(id) {
		return BackupTask{}, ErrNotGuestTask
	}
	path := "/nodes/" + url.PathEscape(d.cfg.Node) + "/tasks/" + url.PathEscape(upid)
	var st struct {
		Status     string `json:"status"`
		ExitStatus string `json:"exitstatus"`
	}
	if err := d.c.Get(ctx, path+"/status", nil, &st); err != nil {
		return BackupTask{}, err
	}
	if st.Status != "stopped" {
		return BackupTask{Running: true}, nil
	}
	out := BackupTask{Status: st.ExitStatus, OK: st.ExitStatus == "OK" || strings.HasPrefix(st.ExitStatus, "WARNINGS")}
	var lines []struct {
		T string `json:"t"`
	}
	if err := d.c.Get(ctx, path+"/log", url.Values{"start": {"0"}, "limit": {"5000"}}, &lines); err != nil {
		return BackupTask{}, err
	}
	var archives []string
	for _, l := range lines {
		if a, ok := archiveOf(l.T); ok {
			archives = append(archives, a)
		}
	}
	if len(archives) != 1 {
		return out, nil
	}
	vols, err := d.listBackups(ctx, storage, id)
	if err != nil {
		return BackupTask{}, err
	}
	var match []string
	for _, v := range vols {
		if archiveNames(archives[0], v.Volid) {
			match = append(match, v.Volid)
		}
	}
	if len(match) == 1 {
		out.Volid = match[0]
	}
	return out, nil
}

// archiveOf returns the archive of a vzdump log line "INFO: creating
// vzdump archive '<path>'" (a dir storage) or "INFO: creating Proxmox
// Backup Server archive '<vm|ct>/<id>/<time>'" (PBS).
func archiveOf(line string) (string, bool) {
	rest, ok := strings.CutPrefix(line, "INFO: creating ")
	if !ok {
		return "", false
	}
	_, quoted, ok := strings.Cut(rest, "archive '")
	if !ok {
		return "", false
	}
	a, _, ok := strings.Cut(quoted, "'")
	return a, ok && a != ""
}

// archiveNames reports whether the log's archive is the volume volid
// ("<storage>:backup/<name>"): the same name, or (a dir storage, whose log
// names the file path) the same file name.
func archiveNames(archive, volid string) bool {
	_, name, ok := strings.Cut(volid, ":")
	if !ok {
		return false
	}
	name = strings.TrimPrefix(name, "backup/")
	if name == "" {
		return false
	}
	return archive == name || (!strings.Contains(name, "/") && strings.HasSuffix(archive, "/"+name))
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
