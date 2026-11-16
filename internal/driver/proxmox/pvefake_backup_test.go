package proxmox

import (
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// The guest backup part of the fake PVE (T-221). It models what T-214
// found on the host: the storage index with type, content and
// encryption-key; vzdump with notes-template, whose notes failure is only a
// warning (exit "WARNINGS: 1"); the content list filtered by vmid with the
// first line of the notes; the delete of one volume. Not modelled (proven
// only on the host, C9): the right checks of check_volume_access, PBS, the
// real archive and its content, exclude-path in the archive.
type fakeBackup struct {
	storage       string // the guest backup storage, for example plumb-backup
	typ           string // dir, pbs …
	encKey        string // encryption-key of a pbs storage
	content       string
	others        []storageEntry // more storages in the index
	total         int64
	avail         int64
	vols          map[string]*fakeVol
	seq           int
	notesFail     bool // vzdump writes no notes and ends with a warning
	failVzdump    string
	forbidden     []string // forbidden vzdump parameters seen
	restored      []string // "vmid volid storage" of each restore (T-222)
	restoreFail   string   // non-empty: the restore task ends with this exit status (T-232)
	restoreWipes  bool     // a failed restore task removed scsi0 first (T-241)
	restoreDropMP bool     // a failed restore task removed only a container's mp1 (T-253 L1)
	restoreRefuse bool     // the restore POST is refused with 403 (no task)
}

type fakeVol struct {
	vmid   int
	ctime  int64
	size   int64
	notes  string
	config string // the guest config in the archive (extractconfig, T-241)
}

func newFakeBackup() *fakeBackup {
	return &fakeBackup{storage: "plumb-backup", typ: "dir", content: "backup", total: 100 << 30, avail: 60 << 30, vols: map[string]*fakeVol{}}
}

// addVol puts a volume on the storage (a person's vzdump, an old one).
func (b *fakeBackup) addVol(vmid int, notes string, ctime int64) string {
	b.seq++
	volid := fmt.Sprintf("%s:backup/vzdump-qemu-%d-%04d.vma.zst", b.storage, vmid, b.seq)
	b.vols[volid] = &fakeVol{vmid: vmid, ctime: ctime, size: 1 << 20, notes: notes}
	return volid
}

var reContentVol = regexp.MustCompile(`^/nodes/([^/]+)/storage/([^/]+)/content/(.+)$`)

// vzdumpForbidden: options that need more rights or root (T-214, D-405).
var vzdumpForbidden = []string{"prune-backups", "protected", "bwlimit", "ionice", "performance", "tmpdir", "dumpdir", "script", "job-id", "stop", "remove"}

// serveBackup answers the backup calls; false for any other call. f.mu is
// held.
func (f *fakePVE) serveBackup(w http.ResponseWriter, r *http.Request, path, key string) bool {
	b := f.backup
	switch {
	case r.Method == "GET" && path == "/nodes/"+f.node+"/vzdump/extractconfig":
		v := b.vols[r.Form.Get("volume")]
		if v == nil {
			fail(w, 500, "unable to parse volume ID", nil)
			return true
		}
		conf := v.config
		if conf == "" {
			conf = "cores: 1\nnet0: virtio=02:00:00:00:00:01,bridge=vmbr0\n"
		}
		reply(w, 200, conf)
		return true
	case r.Method == "GET" && path == "/storage":
		list := append([]storageEntry{{Storage: b.storage, Type: b.typ, Content: b.content, EncryptionKey: b.encKey}}, b.others...)
		reply(w, 200, list)
		return true
	case r.Method == "GET" && path == "/nodes/"+f.node+"/storage/"+b.storage+"/status":
		reply(w, 200, map[string]any{"type": b.typ, "total": b.total, "avail": b.avail, "used": b.total - b.avail, "active": 1, "enabled": 1})
		return true
	case r.Method == "GET" && path == "/nodes/"+f.node+"/storage/"+b.storage+"/content":
		if r.Form.Get("content") != "backup" {
			fail(w, 400, "only backup content is modelled", nil)
			return true
		}
		vmid, _ := strconv.Atoi(r.Form.Get("vmid"))
		var out []map[string]any
		ids := make([]string, 0, len(b.vols))
		for id := range b.vols {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			v := b.vols[id]
			if vmid != 0 && v.vmid != vmid {
				continue
			}
			first, _, _ := strings.Cut(v.notes, "\n")
			e := map[string]any{"volid": id, "ctime": v.ctime, "size": v.size, "vmid": v.vmid, "content": "backup", "format": "vma.zst"}
			if first != "" {
				e["notes"] = first
			}
			out = append(out, e)
		}
		reply(w, 200, out)
		return true
	case r.Method == "DELETE" && reContentVol.MatchString(path):
		m := reContentVol.FindStringSubmatch(path)
		volid, _ := url.PathUnescape(m[3])
		if m[2] != b.storage || b.vols[volid] == nil {
			fail(w, 404, "volume does not exist", nil)
			return true
		}
		delete(b.vols, volid)
		reply(w, 200, nil)
		return true
	case r.Method == "POST" && (path == "/nodes/"+f.node+"/qemu" && r.Form.Has("archive") || path == "/nodes/"+f.node+"/lxc" && r.Form.Get("restore") == "1"):
		// A restore over a guest (T-222): like PVE, force=1 is needed for
		// an existing VMID, and the guest must be stopped.
		volid := r.Form.Get("archive")
		if volid == "" {
			volid = r.Form.Get("ostemplate")
		}
		vmid, _ := strconv.Atoi(r.Form.Get("vmid"))
		vm, v := f.vms[vmid], b.vols[volid]
		switch {
		case b.restoreRefuse:
			fail(w, 403, "Permission check failed (/vms/9001, VM.Backup)", nil)
		case vm == nil || v == nil || v.vmid != vmid:
			fail(w, 500, "restore: guest or volume not found", nil)
		case r.Form.Get("force") != "1":
			fail(w, 500, fmt.Sprintf("unable to restore VM %d - VM %d already exists", vmid, vmid), nil)
		case vm.running:
			fail(w, 500, "restore: the guest runs", nil)
		default:
			b.restored = append(b.restored, fmt.Sprintf("%d %s %s", vmid, volid, r.Form.Get("storage")))
			upid := f.task(key)
			if b.restoreFail != "" {
				f.tasks[upid].exit = b.restoreFail
				if b.restoreWipes {
					delete(vm.cfg, "scsi0") // the task removed the disk before it failed
					delete(vm.cfg, "rootfs")
				}
				if b.restoreDropMP {
					delete(vm.cfg, "mp1")
				}
			}
			reply(w, 200, upid)
		}
		return true
	case r.Method == "POST" && path == "/nodes/"+f.node+"/vzdump":
		for _, p := range vzdumpForbidden {
			if r.Form.Has(p) {
				b.forbidden = append(b.forbidden, p)
				fail(w, 403, "Permission check failed ("+p+")", nil)
				return true
			}
		}
		vmid, _ := strconv.Atoi(r.Form.Get("vmid"))
		vm := f.vms[vmid]
		if vm == nil || r.Form.Get("storage") != b.storage {
			fail(w, 500, "vzdump: guest or storage not found", nil)
			return true
		}
		// A vzdump task names its type and VMID in the UPID, as PVE does (T-248).
		qm := f.task(key)
		upid := fmt.Sprintf("UPID:%s:%08X:%08X:%08X:vzdump:%d:plumb@pve!plumb:", f.node, len(f.tasks)+1, 1, time.Now().Unix(), vmid)
		f.tasks[upid] = f.tasks[qm]
		delete(f.tasks, qm)
		if b.failVzdump != "" {
			f.tasks[upid].exit = b.failVzdump
			b.failVzdump = ""
			reply(w, 200, upid)
			return true
		}
		notes := r.Form.Get("notes-template")
		if b.notesFail {
			notes = ""
			f.tasks[upid].exit = "WARNINGS: 1"
			f.tasks[upid].log = append(f.tasks[upid].log, "unable to add notes")
		}
		b.seq++
		kind := "qemu"
		if vm.ct {
			kind = "lxc"
		}
		file := fmt.Sprintf("vzdump-%s-%d-%04d.vma.zst", kind, vmid, b.seq)
		volid := b.storage + ":backup/" + file
		b.vols[volid] = &fakeVol{vmid: vmid, ctime: time.Now().Unix() + int64(b.seq), size: 2 << 20, notes: notes}
		f.tasks[upid].log = append(f.tasks[upid].log, "INFO: creating vzdump archive '/mnt/pve/"+b.storage+"/dump/"+file+"'")
		reply(w, 200, upid)
		return true
	}
	return false
}
