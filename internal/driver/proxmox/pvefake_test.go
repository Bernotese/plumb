package proxmox

import (
	"crypto/rand"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakePVE is an in-memory model of the Proxmox VE API endpoints that the
// driver uses, written from the API schema (api-viewer). It is not a
// recording: every behaviour it shows is an assumption, listed in the
// T-012 card under "to verify on host".
type fakePVE struct {
	t      *testing.T
	srv    *httptest.Server
	caFile string
	auth   string
	node   string

	mu       sync.Mutex
	vms      map[int]*fakeVM
	files    map[string]bool // volids in the image storage
	tasks    map[string]*fakeTask
	failNext map[string]string // request key → task exit status (one shot)
	// refuseTags: a config write with tags (or a delete of them) is 403,
	// as with a datacenter user-tag-access other than free (D-299).
	refuseTags bool
	resizes    []string // "<vmid> <disk> <size>" of each resize
	// failCloudinit: the next PUT …/cloudinit (regenerate) fails.
	failCloudinit bool
	taskPolls     int      // polls before a task stops
	log           []string // "METHOD /path"
	params        map[string]url.Values

	// Fault injection for the review tests.
	allPools     bool              // answer /pools with every pool, ignoring poolid
	echoParam    map[string]string // request key → parameter whose value a 400 echoes
	notJSON      map[string]bool   // request key → 200 with an HTML page
	nullAnswer   map[string]bool   // request key → 200 with data null
	statusFails  int               // task status reads that fail with 500 first
	stoppedTasks []string          // UPIDs that got DELETE
	// raceVMID: right after nextid says this id is free, someone else
	// creates a guest with it (in another pool, or in ours with another name).
	raceVMID int
	racePool string
	// staleTemplate: /pools reports every guest with template 0, like the
	// cluster resource cache of Proxmox right after a conversion.
	staleTemplate bool
	// staleNames: /pools reports every guest without a name, like the
	// cache right after a clone (test host, T-073 A4).
	staleNames bool
	// cloneName: a clone gets this name instead of the asked one (someone
	// renames the new guest at once; T-073).
	cloneName string
	// lockReads: the config of a VM shows "lock: create" for this many
	// more reads, and a DELETE fails meanwhile (a stopped create task).
	lockReads map[int]int

	// Start-up check (T-053): the version, statuses that /version answers
	// first (503, 595 …), the storages of the node, a status for the node
	// storage list, a missing pool, and the token's rights (nil: the call
	// fails).
	version       string
	versionStatus []int
	storages      []map[string]any
	storageStatus int
	noPool        bool
	perms         map[string]map[string]int
	// hotplugFails: a netN write (N ≥ 1) on a running VM stays pending.
	hotplugFails bool
	// dropNICWrite: a netN write ends nowhere (neither running nor pending).
	dropNICWrite bool
	// storageConfigs: the answer of GET /storage (nil: 500).
	storageConfigs []map[string]any
	authFails      int    // requests refused with 401
	dropPath       string // a request to this path loses its connection
	// writeDuringRead: this many reads of the config without current=1
	// change the config first (a write between the reads of Observe).
	writeDuringRead     int
	ctWriteBeforeConfig bool // concurrent CT edit before the next config PUT
	// noDigest: config answers have no digest (a proxy or a broken API).
	noDigest bool
	// keepPending: a write of the running value on a running VM keeps
	// the pending entry (review B1: host check 2 is open).
	keepPending bool

	// Image builds (T-061). diskTotal and diskUsed are the status of the
	// disk storage; thinPools is the answer of /disks/lvmthin (nil: 403,
	// the token has no Sys.Audit). linkInConfig: the disk line of a
	// linked clone shows "base-…/vm-…" (lvmthin); parentInContent: the
	// volume list of the disk storage gives the parent. Both are open
	// host checks. writeOnStatus: this many status reads change the
	// config first (someone writes between the guard and the write).
	// failTemplate: the next conversion fails.
	diskType        string
	diskTotal       int64
	diskUsed        int64
	thinPools       []map[string]any
	linkInConfig    bool
	parentInContent bool
	writeOnStatus   int
	failTemplate    bool
	// lateConvert: the next conversion fails on our side but ends on the
	// host (review N1 of PR #125). "lock": the VM shows "lock: template"
	// for two config reads, then it is a template. "afterWrite": it is a
	// template right after the next config write.
	lateConvert   string
	lateID        int
	contentStatus int // a status for the volume list of the disk storage

	// Container templates (T-079): uploads holds each upload's file
	// bytes by name; imagesAvail is the free space of the image storage.
	uploads     map[string][]byte
	uploadOrder []string // the field names of the last upload, in order
	imagesAvail int64
	// failNextPUT: the next container config write ignores a delete of
	// this key (a write that Proxmox did not take, T-082 S8 guard).
	failNextPUT string
	// ctTemplateReads: a container template call converts only at this
	// many config reads later (default 2, as the host); lateCT counts.
	ctTemplateReads int
	lateCT          map[int]int
}

type fakeVM struct {
	// ct: a container (T-079); pvefake_lxc_test.go models it. pending
	// then holds its pending values, and delete its pending deletes.
	ct       bool
	delete   map[string]bool
	cfg      map[string]string
	template bool
	running  bool
	pool     string
	// pending holds values that a POST config on a running VM could not
	// apply live (T-015 run 1: memory is not in the default hotplug
	// list). A start applies them.
	pending map[string]string
	// parent is the base volume of a linked clone ("base-9001-disk-0").
	parent string
	// ci is what the cloud-init drive holds: a copy of the config when
	// Proxmox wrote it (at a start, and when the drive is added); nil for
	// an empty drive or none. A clone gets a new empty drive (T-061 log).
	ci map[string]string
}

// writeCI fills the cloud-init drive from the config, as Proxmox does.
func (vm *fakeVM) writeCI() {
	if !hasCloudinit(vm.cfg) {
		vm.ci = nil
		return
	}
	vm.ci = map[string]string{}
	for k, v := range vm.cfg {
		vm.ci[k] = v
	}
}

func hasCloudinit(cfg map[string]string) bool {
	for k, v := range cfg {
		if driveKey.MatchString(k) && strings.Contains(v, "-cloudinit") {
			return true
		}
	}
	return false
}

// coldKeys are the keys that the fake keeps pending on a running VM.
// net0 (the bridge) applies live.
var coldKeys = map[string]bool{"cores": true, "memory": true, "smbios1": true}

// next returns the config after the next stop and start.
func (vm *fakeVM) next() map[string]string {
	out := make(map[string]string, len(vm.cfg))
	for k, v := range vm.cfg {
		out[k] = v
	}
	for k, v := range vm.pending {
		out[k] = v
	}
	return out
}

// vmDigest covers the running and the pending values, like Proxmox.
func vmDigest(vm *fakeVM) string {
	all := make(map[string]string, len(vm.cfg)+len(vm.pending))
	for k, v := range vm.cfg {
		all[k] = v
	}
	for k, v := range vm.pending {
		all["pending:"+k] = v
	}
	return digest(all)
}

type fakeTask struct {
	polls int
	exit  string
	log   []string
}

const (
	fakeToken  = "plumb@pve!plumb"
	fakeSecret = "11111111-2222-3333-4444-555555555555"
	fakeNode   = "pve"
	fakePool   = "plumb"
)

func newFakePVE(t *testing.T) *fakePVE {
	t.Helper()
	f := &fakePVE{t: t, auth: "PVEAPIToken=" + fakeToken + "=" + fakeSecret, node: fakeNode,
		vms: map[int]*fakeVM{}, files: map[string]bool{}, tasks: map[string]*fakeTask{},
		failNext: map[string]string{}, lockReads: map[int]int{}, taskPolls: 2,
		imagesAvail: 30 << 30, uploads: map[string][]byte{}, ctTemplateReads: 2, lateCT: map[int]int{},
		version: "9.2.26", diskType: "lvmthin", diskTotal: 42 << 30, diskUsed: 1 << 30, linkInConfig: true, parentInContent: true,
		storages: []map[string]any{
			{"storage": "local-lvm", "content": "images,rootdir", "enabled": 1, "active": 1, "type": "lvmthin"},
			{"storage": "local", "content": "import", "enabled": 1, "active": 1, "type": "dir"},
			{"storage": "plumb-snippets", "content": "snippets", "enabled": 1, "active": 1, "type": "dir"},
		},
		// Like the test host: the value is the propagate flag; install.sh
		// sets the bridge ACL with propagate 0.
		perms: map[string]map[string]int{"/sdn/zones/localnetwork/vmbr0": {"SDN.Use": 0}, "/pool/plumb": {"VM.Allocate": 1}}, params: map[string]url.Values{},
		echoParam: map[string]string{}, notJSON: map[string]bool{}, nullAnswer: map[string]bool{}}
	f.srv = httptest.NewTLSServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	f.caFile = filepath.Join(t.TempDir(), "pve-root-ca.pem")
	cert := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: f.srv.Certificate().Raw})
	if err := os.WriteFile(f.caFile, cert, 0o644); err != nil {
		t.Fatal(err)
	}
	return f
}

// client returns a real client that trusts only the fake's CA file.
func (f *fakePVE) client(t *testing.T) *Client {
	t.Helper()
	c, err := NewClient(ClientConfig{URL: f.srv.URL, TokenID: fakeToken, Secret: fakeSecret, CAFile: f.caFile, TaskPoll: time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func (f *fakePVE) driver(t *testing.T) *Driver {
	t.Helper()
	d, err := New(Config{Client: f.client(t), Node: fakeNode, Pool: fakePool, Nameservers: []string{"192.0.2.53"}, SearchDomain: "plumb.test", Bridge: "vmbr0", DiskStorage: "local-lvm", ImageStorage: "local",
		SnippetStorage: "plumb-snippets", Snippet: "plumb-vendor.yaml", VMIDMin: 9000, VMIDMax: 9099, ShutdownTimeout: time.Second,
		ImageLockWait: 200 * time.Millisecond, ImageLockPoll: time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// addVM puts a guest into the model, for example a foreign one.
func (f *fakePVE) addVM(id int, pool string, cfg map[string]string, running bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c := map[string]string{"smbios1": "uuid=" + uuid()}
	for k, v := range cfg {
		c[k] = v
	}
	f.vms[id] = &fakeVM{cfg: c, pool: pool, running: running}
}

func uuid() string {
	b := make([]byte, 16)
	rand.Read(b)
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

func digest(cfg map[string]string) string {
	keys := make([]string, 0, len(cfg))
	for k := range cfg {
		if k != "digest" {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	h := sha1.New()
	for _, k := range keys {
		fmt.Fprintf(h, "%s=%s\n", k, cfg[k])
	}
	return hex.EncodeToString(h.Sum(nil))
}

func (f *fakePVE) count(prefix string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, l := range f.log {
		if strings.HasPrefix(l, prefix) {
			n++
		}
	}
	return n
}

func (f *fakePVE) lastParams(key string) url.Values {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.params[key]
}

// --- HTTP ---

func reply(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]any{"data": data})
}

func fail(w http.ResponseWriter, status int, msg string, errs map[string]string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]any{"data": nil, "message": msg + "\n", "errors": errs})
}

func (f *fakePVE) task(key string) string {
	upid := fmt.Sprintf("UPID:%s:%08X:%08X:%08X:qmtask:%d:root@pam:", f.node, len(f.tasks)+1, 1, time.Now().Unix(), len(f.tasks))
	t := &fakeTask{exit: "OK", log: []string{"starting " + key, "done"}}
	if exit, ok := f.failNext[key]; ok {
		t.exit = exit
		t.log = append(t.log, "TASK ERROR: "+exit)
		delete(f.failNext, key)
	}
	f.tasks[upid] = t
	return upid
}

var (
	reQemu    = regexp.MustCompile(`^/nodes/([^/]+)/qemu/(\d+)(/.*)?$`)
	reStorage = regexp.MustCompile(`^/nodes/([^/]+)/storage/([^/]+)/(content|download-url|status|upload)$`)
	reTask    = regexp.MustCompile(`^/nodes/([^/]+)/tasks/([^/]+)(?:/(status|log))?$`)
)

func (f *fakePVE) serve(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != f.auth {
		f.mu.Lock()
		f.authFails++
		f.mu.Unlock()
		fail(w, 401, "authentication failure", nil)
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/api2/json")
	r.ParseForm()
	f.mu.Lock()
	defer f.mu.Unlock()
	key := r.Method + " " + path
	if !strings.Contains(path, "/tasks/") {
		f.log = append(f.log, key)
	}
	f.params[key] = r.Form

	if param, ok := f.echoParam[key]; ok {
		delete(f.echoParam, key)
		fail(w, 400, "Parameter verification failed.", map[string]string{param: fmt.Sprintf("value '%s' does not match the format", r.Form.Get(param))})
		return
	}
	if f.notJSON[key] {
		delete(f.notJSON, key)
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(200)
		fmt.Fprint(w, "<html>proxy login page</html>")
		return
	}
	if f.nullAnswer[key] {
		delete(f.nullAnswer, key)
		reply(w, 200, nil)
		return
	}

	if f.dropPath != "" && path == f.dropPath {
		if hj, ok := w.(http.Hijacker); ok {
			if conn, _, err := hj.Hijack(); err == nil {
				conn.Close()
				return
			}
		}
	}
	switch {
	case r.Method == "GET" && path == "/version":
		if len(f.versionStatus) > 0 {
			st := f.versionStatus[0]
			f.versionStatus = f.versionStatus[1:]
			fail(w, st, "proxy: no connection", nil)
			return
		}
		reply(w, 200, map[string]any{"version": f.version, "release": "9.2", "repoid": "41a0e2e0"})
	case r.Method == "GET" && path == "/nodes/"+fakeNode+"/storage" || (r.Method == "GET" && strings.HasPrefix(path, "/nodes/") && strings.HasSuffix(path, "/storage")):
		if f.storageStatus != 0 || path != "/nodes/"+fakeNode+"/storage" {
			st := f.storageStatus
			if st == 0 {
				st = 595
			}
			fail(w, st, "no such node", nil)
			return
		}
		reply(w, 200, f.storages)
	case r.Method == "GET" && path == "/storage":
		// The storage index: every storage config the token may see (T-214
		// FACT 2). storageConfigs nil: the call fails.
		if f.storageConfigs == nil {
			fail(w, 500, "storage config not readable", nil)
			return
		}
		reply(w, 200, f.storageConfigs)
	case r.Method == "GET" && path == "/access/permissions":
		if f.perms == nil {
			fail(w, 403, "Permission check failed", nil)
			return
		}
		reply(w, 200, f.perms)
	case r.Method == "GET" && path == "/pools" && f.noPool:
		reply(w, 200, []any{})
	case r.Method == "GET" && path == "/pools":
		if r.Form.Get("poolid") == "" {
			fail(w, 400, "parameter verification failed", map[string]string{"poolid": "missing"})
			return
		}
		members := []map[string]any{}
		ids := make([]int, 0, len(f.vms))
		for id := range f.vms {
			ids = append(ids, id)
		}
		sort.Ints(ids)
		for _, id := range ids {
			vm := f.vms[id]
			if vm.pool == r.Form.Get("poolid") || f.allPools {
				t := 0
				if vm.template && !f.staleTemplate {
					t = 1
				}
				name, typ := vm.cfg["name"], "qemu"
				if vm.ct {
					name, typ = vm.cfg["hostname"], "lxc"
				}
				if f.staleNames {
					name = ""
				}
				members = append(members, map[string]any{"vmid": id, "type": typ, "name": name, "template": t, "id": typ + "/" + strconv.Itoa(id)})
			}
		}
		if f.allPools { // a server that ignores the filter: one entry per pool
			byPool := map[string][]map[string]any{}
			for _, m := range members {
				id := m["vmid"].(int)
				byPool[f.vms[id].pool] = append(byPool[f.vms[id].pool], m)
			}
			var all []any
			for p, ms := range byPool {
				all = append(all, map[string]any{"poolid": p, "members": ms})
			}
			reply(w, 200, all)
			return
		}
		reply(w, 200, []any{map[string]any{"poolid": r.Form.Get("poolid"), "members": members}})

	case r.Method == "GET" && path == "/nodes/"+fakeNode+"/disks/lvmthin":
		if f.thinPools == nil {
			fail(w, 403, "Permission check failed (/, Sys.Audit)", nil) // the real path (D-240)
			return
		}
		reply(w, 200, f.thinPools)
	case r.Method == "GET" && path == "/cluster/nextid":
		id, _ := strconv.Atoi(r.Form.Get("vmid"))
		if _, taken := f.vms[id]; taken {
			fail(w, 400, "Parameter verification failed.", map[string]string{"vmid": fmt.Sprintf("VM %d already exists", id)})
			return
		}
		reply(w, 200, strconv.Itoa(id))
		if id == f.raceVMID {
			f.raceVMID = 0
			f.vms[id] = &fakeVM{cfg: map[string]string{"name": "someone-else", "smbios1": "uuid=" + uuid()}, pool: f.racePool, running: true}
		}

	case reStorage.MatchString(path):
		m := reStorage.FindStringSubmatch(path)
		storage, op := m[2], m[3]
		if op == "upload" && r.Method == "POST" {
			f.upload(w, r, key, storage)
			return
		}
		if op == "status" && r.Method == "GET" && storage == "local" {
			reply(w, 200, map[string]any{"type": "dir", "total": 40 << 30, "avail": f.imagesAvail, "used": 1 << 30, "active": 1, "enabled": 1})
			return
		}
		if op == "status" && r.Method == "GET" {
			if storage != "local-lvm" {
				fail(w, 501, "not modelled", nil)
				return
			}
			reply(w, 200, map[string]any{"type": f.diskType, "total": f.diskTotal, "used": f.diskUsed,
				"avail": f.diskTotal - f.diskUsed, "active": 1, "enabled": 1, "content": "images,rootdir"})
			return
		}
		if op == "content" && r.Method == "GET" && storage == "local-lvm" {
			if f.contentStatus != 0 {
				fail(w, f.contentStatus, "Permission check failed", nil)
				return
			}
			list := []map[string]any{}
			ids := make([]int, 0, len(f.vms))
			for id := range f.vms {
				ids = append(ids, id)
			}
			sort.Ints(ids)
			for _, id := range ids {
				vm := f.vms[id]
				if vm.pool != fakePool {
					continue // the token sees only the pool
				}
				vol := parseProps(vm.cfg["scsi0"])[""]
				if !strings.HasPrefix(vol, "local-lvm:") {
					continue
				}
				e := map[string]any{"volid": vol, "vmid": id, "size": int64(sizeMB(parseProps(vm.cfg["scsi0"])["size"])) << 20, "format": "raw"}
				if vm.parent != "" && f.parentInContent {
					e["parent"] = vm.parent
				}
				list = append(list, e)
			}
			reply(w, 200, list)
			return
		}
		if op == "content" && r.Method == "GET" {
			list := []map[string]any{}
			for v := range f.files {
				typ := "import"
				if strings.Contains(v, ":vztmpl/") {
					typ = "vztmpl"
				}
				if strings.HasPrefix(v, storage+":") && (r.Form.Get("content") == "" || r.Form.Get("content") == typ) {
					list = append(list, map[string]any{"volid": v, "content": typ, "size": 700 << 20})
				}
			}
			reply(w, 200, list)
			return
		}
		if op == "download-url" && r.Method == "POST" {
			p := r.Form
			if p.Get("content") != "import" || !(p.Get("checksum-algorithm") == "sha256" && len(p.Get("checksum")) == 64 ||
				p.Get("checksum-algorithm") == "sha512" && len(p.Get("checksum")) == 128) ||
				!strings.HasSuffix(p.Get("filename"), ".qcow2") || !strings.HasPrefix(p.Get("url"), "https://") {
				fail(w, 400, "Parameter verification failed.", map[string]string{"download-url": "bad parameters"})
				return
			}
			upid := f.task(key)
			if f.tasks[upid].exit == "OK" {
				f.files[storage+":import/"+p.Get("filename")] = true
			}
			reply(w, 200, upid)
			return
		}
		fail(w, 501, "not implemented", nil)

	case r.Method == "POST" && path == "/nodes/"+f.node+"/qemu":
		id, _ := strconv.Atoi(r.Form.Get("vmid"))
		if _, taken := f.vms[id]; taken {
			fail(w, 500, fmt.Sprintf("unable to create VM %d - VM %d already exists on node '%s'", id, id, f.node), nil)
			return
		}
		scsi := parseProps(r.Form.Get("scsi0"))
		if src := scsi["import-from"]; src == "" || !f.files[src] {
			fail(w, 500, "import-from: volume does not exist", nil)
			return
		}
		cfg := map[string]string{"smbios1": "uuid=" + uuid(), "scsi0": fmt.Sprintf("local-lvm:vm-%d-disk-0,size=3584M", id)}
		for k, v := range r.Form {
			if k != "vmid" && k != "pool" && k != "scsi0" {
				cfg[k] = v[0]
				if st, ok := strings.CutSuffix(v[0], ":cloudinit"); ok && driveKey.MatchString(k) {
					cfg[k] = fmt.Sprintf("%s:vm-%d-cloudinit,media=cdrom", st, id) // empty until a start
				}
			}
		}
		f.vms[id] = &fakeVM{cfg: cfg, pool: r.Form.Get("pool")}
		reply(w, 200, f.task(key))

	case reQemu.MatchString(path):
		m := reQemu.FindStringSubmatch(path)
		id, _ := strconv.Atoi(m[2])
		vm, ok := f.vms[id]
		if !ok || vm.ct {
			fail(w, 500, fmt.Sprintf("Configuration file 'nodes/%s/qemu-server/%d.conf' does not exist", f.node, id), nil)
			return
		}
		f.qemu(w, r, key, id, vm, m[3])

	case r.Method == "POST" && path == "/nodes/"+f.node+"/lxc":
		f.createCT(w, r, key)

	case reLxc.MatchString(path):
		m := reLxc.FindStringSubmatch(path)
		id, _ := strconv.Atoi(m[2])
		vm, ok := f.vms[id]
		if !ok || !vm.ct {
			fail(w, 500, fmt.Sprintf("Configuration file 'nodes/%s/lxc/%d.conf' does not exist", f.node, id), nil)
			return
		}
		f.lxc(w, r, key, id, vm, m[3])

	case reTask.MatchString(path):
		m := reTask.FindStringSubmatch(path)
		t, ok := f.tasks[m[2]]
		if !ok {
			fail(w, 500, "no such task", nil)
			return
		}
		if r.Method == "DELETE" {
			f.stoppedTasks = append(f.stoppedTasks, m[2])
			t.exit = "interrupted by signal"
			t.polls = 1 << 30
			reply(w, 200, nil)
			return
		}
		if m[3] == "log" {
			lines := []map[string]any{}
			for i, l := range t.log {
				lines = append(lines, map[string]any{"n": i + 1, "t": l})
			}
			reply(w, 200, lines)
			return
		}
		if f.statusFails > 0 {
			f.statusFails--
			fail(w, 500, "temporary failure", nil)
			return
		}
		t.polls++
		if t.polls < f.taskPolls {
			reply(w, 200, map[string]any{"status": "running", "upid": m[2]})
			return
		}
		reply(w, 200, map[string]any{"status": "stopped", "exitstatus": t.exit, "upid": m[2]})

	default:
		fail(w, 501, "Method '"+key+"' not implemented", nil)
	}
}

// tagWrite reports whether a config write sets or deletes the tags while
// refuseTags is on.
func (f *fakePVE) tagWrite(form url.Values) bool {
	_, set := form["tags"]
	return f.refuseTags && (set || slices.Contains(strings.Split(form.Get("delete"), ","), "tags"))
}

func (f *fakePVE) qemu(w http.ResponseWriter, r *http.Request, key string, id int, vm *fakeVM, sub string) {
	switch {
	case r.Method == "GET" && sub == "/config":
		out := map[string]any{}
		if f.lockReads[id] > 0 {
			f.lockReads[id]--
			out["lock"] = "create"
			if f.lateConvert == "lock" && f.lateID == id {
				out["lock"] = "template"
				if f.lockReads[id] == 0 {
					f.convert(id, vm)
				}
			}
		}
		if r.Form.Get("current") != "1" && f.writeDuringRead > 0 {
			f.writeDuringRead-- // someone writes between the two reads
			vm.cfg["tags"] = uuid()
		}
		view := vm.cfg
		if r.Form.Get("current") != "1" {
			view = vm.next()
		}
		for k, v := range view {
			if n, err := strconv.Atoi(v); err == nil && (k == "cores" || k == "onboot" || k == "template") {
				out[k] = n
			} else {
				out[k] = v
			}
		}
		out["digest"] = vmDigest(vm)
		if f.noDigest {
			delete(out, "digest")
		}
		reply(w, 200, out)
	case r.Method == "POST" && sub == "/config":
		if d := r.Form.Get("digest"); d != "" && d != vmDigest(vm) {
			fail(w, 500, "detected modified configuration - file changed by other user? Try again.", nil)
			return
		}
		if f.tagWrite(r.Form) {
			fail(w, 403, "Permission check failed (user-tag-access)", nil)
			return
		}
		for _, k := range strings.Split(r.Form.Get("delete"), ",") {
			if k != "" {
				if driveKey.MatchString(k) && strings.Contains(vm.cfg[k], "-cloudinit") {
					vm.ci = nil // Proxmox removes the cloud-init volume, no unused line
				}
				delete(vm.cfg, k)
				delete(vm.pending, k)
			}
		}
		newCI := false
		for k, v := range r.Form {
			if k == "digest" || k == "delete" {
				continue
			}
			if st, ok := strings.CutSuffix(v[0], ":cloudinit"); ok && driveKey.MatchString(k) {
				vm.cfg[k] = fmt.Sprintf("%s:vm-%d-cloudinit,media=cdrom", st, id)
				newCI = true
				continue
			}
			switch {
			case f.dropNICWrite && nicUpdateKeys[k]:
				// Proxmox answers OK but the NIC is in no view.
			case vm.running && f.hotplugFails && nicUpdateKeys[k]:
				// a failed hot-plug: Proxmox keeps the NIC pending (D-402)
				if vm.pending == nil {
					vm.pending = map[string]string{}
				}
				vm.pending[k] = v[0]
			case vm.running && coldKeys[k] && vm.cfg[k] != v[0]:
				if vm.pending == nil {
					vm.pending = map[string]string{}
				}
				vm.pending[k] = v[0] // waits for the next stop and start
			case vm.running && coldKeys[k] && f.keepPending:
				// the running value again: a no-op that keeps the pending entry
			default:
				vm.cfg[k] = v[0]
				delete(vm.pending, k) // a write of the running value removes the pending one
			}
		}
		if newCI {
			vm.writeCI() // a new drive is written from the config at once
		}
		if f.lateConvert == "afterWrite" && f.lateID == id {
			f.convert(id, vm)
		}
		reply(w, 200, f.task(key))
	case r.Method == "PUT" && sub == "/cloudinit":
		// Regenerate: the drive is written from the current config; null answer.
		if f.failCloudinit {
			f.failCloudinit = false
			fail(w, 500, "unable to regenerate the cloud-init drive", nil)
			return
		}
		vm.writeCI()
		reply(w, 200, nil)
	case r.Method == "PUT" && sub == "/resize":
		want := r.Form.Get("size")
		have := parseProps(vm.cfg["scsi0"])["size"]
		if sizeGB(want) < sizeGB(have) {
			fail(w, 500, "shrinking disks is not supported", nil)
			return
		}
		vm.cfg["scsi0"] = replaceProp(vm.cfg["scsi0"], "size", want)
		f.resizes = append(f.resizes, fmt.Sprintf("%d %s %s", id, r.Form.Get("disk"), want))
		reply(w, 200, f.task(key))
	case r.Method == "POST" && sub == "/template":
		if f.failTemplate {
			f.failTemplate = false
			fail(w, 500, "convert to template failed", nil)
			return
		}
		if f.lateConvert != "" {
			f.lateID = id
			if f.lateConvert == "lock" {
				f.lockReads[id] = 2
			}
			fail(w, 500, "proxy timeout", nil)
			return
		}
		if vm.running {
			fail(w, 500, "you can't convert a running VM to a template", nil)
			return
		}
		f.convert(id, vm)
		reply(w, 200, nil) // synchronous in the schema
	case r.Method == "POST" && sub == "/clone":
		newid, _ := strconv.Atoi(r.Form.Get("newid"))
		if _, taken := f.vms[newid]; taken {
			fail(w, 500, "VM already exists", nil)
			return
		}
		if !vm.template && vm.running {
			fail(w, 500, "clone of a running VM", nil)
			return
		}
		cfg := map[string]string{}
		for k, v := range vm.cfg {
			if k != "template" {
				cfg[k] = v
			}
		}
		cfg["name"] = r.Form.Get("name")
		if f.cloneName != "" {
			cfg["name"] = f.cloneName
		}
		cfg["smbios1"] = "uuid=" + uuid() // a clone gets a new uuid
		for k, v := range cfg {
			// A clone gets its own, empty cloud-init volume (T-061 log).
			if driveKey.MatchString(k) && strings.Contains(v, "-cloudinit") {
				st, _, _ := strings.Cut(v, ":")
				cfg[k] = fmt.Sprintf("%s:vm-%d-cloudinit,media=cdrom,size=4M", st, newid)
			}
		}
		disk := parseProps(vm.cfg["scsi0"])
		src := disk[""]
		srcName := src[strings.Index(src, ":")+1:]
		own := fmt.Sprintf("vm-%d-disk-0", newid)
		nvm := &fakeVM{cfg: cfg, pool: r.Form.Get("pool")}
		switch {
		case r.Form.Get("full") == "1" || !vm.template:
			cfg["scsi0"] = strings.Replace(vm.cfg["scsi0"], src, "local-lvm:"+own, 1)
		default: // linked clone of a template
			nvm.parent = srcName
			vol := "local-lvm:" + own
			if f.linkInConfig {
				vol = "local-lvm:" + srcName + "/" + own
			}
			cfg["scsi0"] = strings.Replace(vm.cfg["scsi0"], src, vol, 1)
		}
		f.vms[newid] = nvm
		reply(w, 200, f.task(key))
	case r.Method == "GET" && sub == "/status/current":
		if f.writeOnStatus > 0 {
			f.writeOnStatus-- // someone writes between the guard and the write
			vm.cfg["tags"] = uuid()
		}
		st := "stopped"
		if vm.running {
			st = "running"
		}
		reply(w, 200, map[string]any{"status": st, "vmid": id})
	case r.Method == "POST" && strings.HasPrefix(sub, "/status/"):
		upid := f.task(key)
		switch sub {
		case "/status/start":
			if vm.running {
				f.tasks[upid].exit = fmt.Sprintf("VM %d already running", id)
			} else if vm.template {
				f.tasks[upid].exit = "you can't start a vm if it's a template"
			} else if f.tasks[upid].exit == "OK" {
				vm.running = true
				for k, v := range vm.pending { // a start applies the pending values
					vm.cfg[k] = v
				}
				vm.pending = nil
				vm.writeCI() // a start writes the cloud-init drive
			}
		case "/status/shutdown", "/status/stop":
			if f.tasks[upid].exit == "OK" {
				vm.running = false
			}
		default:
			fail(w, 501, "not implemented", nil)
			return
		}
		reply(w, 200, upid)
	case r.Method == "DELETE" && sub == "":
		if f.lockReads[id] > 0 {
			fail(w, 500, fmt.Sprintf("VM %d is locked (create)", id), nil)
			return
		}
		if vm.running {
			fail(w, 500, fmt.Sprintf("VM %d is running - destroy failed", id), nil)
			return
		}
		upid := f.task(key)
		if f.tasks[upid].exit == "OK" {
			delete(f.vms, id)
		}
		reply(w, 200, upid)
	default:
		fail(w, 501, "Method '"+key+"' not implemented", nil)
	}
}

func (f *fakePVE) countExact(key string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, l := range f.log {
		if l == key {
			n++
		}
	}
	return n
}

// convert makes a VM a template. Like lvmthin (host A1): the disk becomes
// base-<id>-disk-N; the cloud-init volume keeps its name.
func (f *fakePVE) convert(id int, vm *fakeVM) {
	vm.cfg["scsi0"] = strings.Replace(vm.cfg["scsi0"], fmt.Sprintf(":vm-%d-disk-", id), fmt.Sprintf(":base-%d-disk-", id), 1)
	vm.template = true
	vm.cfg["template"] = "1"
	f.lateConvert, f.lateID = "", 0
}
