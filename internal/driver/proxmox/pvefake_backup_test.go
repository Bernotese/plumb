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
	storage    string // the guest backup storage, for example plumb-backup
	typ        string // dir, pbs …
	encKey     string // encryption-key of a pbs storage
	content    string
	others     []storageEntry // more storages in the index
	total      int64
	avail      int64
	vols       map[string]*fakeVol
	seq        int
	notesFail  bool // vzdump writes no notes and ends with a warning
	failVzdump string
	forbidden  []string // forbidden vzdump parameters seen
}

type fakeVol struct {
	vmid  int
	ctime int64
	size  int64
	notes string
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
		upid := f.task(key)
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
		volid := fmt.Sprintf("%s:backup/vzdump-%s-%d-%04d.vma.zst", b.storage, kind, vmid, b.seq)
		b.vols[volid] = &fakeVol{vmid: vmid, ctime: time.Now().Unix() + int64(b.seq), size: 2 << 20, notes: notes}
		reply(w, 200, upid)
		return true
	}
	return false
}
