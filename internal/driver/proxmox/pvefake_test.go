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

	mu        sync.Mutex
	vms       map[int]*fakeVM
	files     map[string]bool // volids in the image storage
	tasks     map[string]*fakeTask
	failNext  map[string]string // request key → task exit status (one shot)
	taskPolls int               // polls before a task stops
	log       []string          // "METHOD /path"
	params    map[string]url.Values
}

type fakeVM struct {
	cfg      map[string]string
	template bool
	running  bool
	pool     string
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
		failNext: map[string]string{}, taskPolls: 2, params: map[string]url.Values{}}
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
	d, err := New(Config{Client: f.client(t), Node: fakeNode, Pool: fakePool, DiskStorage: "local-lvm", ImageStorage: "local",
		SnippetStorage: "plumb-snippets", Snippet: "plumb-vendor.yaml", VMIDMin: 9000, VMIDMax: 9099, ShutdownTimeout: time.Second})
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
	reStorage = regexp.MustCompile(`^/nodes/([^/]+)/storage/([^/]+)/(content|download-url)$`)
	reTask    = regexp.MustCompile(`^/nodes/([^/]+)/tasks/([^/]+)/(status|log)$`)
)

func (f *fakePVE) serve(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != f.auth {
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

	switch {
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
			if vm.pool == r.Form.Get("poolid") {
				t := 0
				if vm.template {
					t = 1
				}
				members = append(members, map[string]any{"vmid": id, "type": "qemu", "name": vm.cfg["name"], "template": t, "id": "qemu/" + strconv.Itoa(id)})
			}
		}
		reply(w, 200, []any{map[string]any{"poolid": r.Form.Get("poolid"), "members": members}})

	case r.Method == "GET" && path == "/cluster/nextid":
		id, _ := strconv.Atoi(r.Form.Get("vmid"))
		if _, taken := f.vms[id]; taken {
			fail(w, 400, "Parameter verification failed.", map[string]string{"vmid": fmt.Sprintf("VM %d already exists", id)})
			return
		}
		reply(w, 200, strconv.Itoa(id))

	case reStorage.MatchString(path):
		m := reStorage.FindStringSubmatch(path)
		storage, op := m[2], m[3]
		if op == "content" && r.Method == "GET" {
			list := []map[string]string{}
			for v := range f.files {
				if strings.HasPrefix(v, storage+":") {
					list = append(list, map[string]string{"volid": v, "content": "import"})
				}
			}
			reply(w, 200, list)
			return
		}
		if op == "download-url" && r.Method == "POST" {
			p := r.Form
			if p.Get("content") != "import" || p.Get("checksum-algorithm") != "sha256" || len(p.Get("checksum")) != 64 ||
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
			}
		}
		f.vms[id] = &fakeVM{cfg: cfg, pool: r.Form.Get("pool")}
		reply(w, 200, f.task(key))

	case reQemu.MatchString(path):
		m := reQemu.FindStringSubmatch(path)
		id, _ := strconv.Atoi(m[2])
		vm, ok := f.vms[id]
		if !ok {
			fail(w, 500, fmt.Sprintf("Configuration file 'nodes/%s/qemu-server/%d.conf' does not exist", f.node, id), nil)
			return
		}
		f.qemu(w, r, key, id, vm, m[3])

	case reTask.MatchString(path):
		m := reTask.FindStringSubmatch(path)
		t, ok := f.tasks[m[2]]
		if !ok {
			fail(w, 500, "no such task", nil)
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

func (f *fakePVE) qemu(w http.ResponseWriter, r *http.Request, key string, id int, vm *fakeVM, sub string) {
	switch {
	case r.Method == "GET" && sub == "/config":
		out := map[string]any{}
		for k, v := range vm.cfg {
			if n, err := strconv.Atoi(v); err == nil && (k == "cores" || k == "onboot" || k == "template") {
				out[k] = n
			} else {
				out[k] = v
			}
		}
		out["digest"] = digest(vm.cfg)
		reply(w, 200, out)
	case r.Method == "POST" && sub == "/config":
		if d := r.Form.Get("digest"); d != "" && d != digest(vm.cfg) {
			fail(w, 500, "detected modified configuration - file changed by other user? Try again.", nil)
			return
		}
		for k, v := range r.Form {
			if k != "digest" {
				vm.cfg[k] = v[0]
			}
		}
		reply(w, 200, f.task(key))
	case r.Method == "PUT" && sub == "/resize":
		want := r.Form.Get("size")
		have := parseProps(vm.cfg["scsi0"])["size"]
		if sizeGB(want) < sizeGB(have) {
			fail(w, 500, "shrinking disks is not supported", nil)
			return
		}
		vm.cfg["scsi0"] = replaceProp(vm.cfg["scsi0"], "size", want)
		reply(w, 200, f.task(key))
	case r.Method == "POST" && sub == "/template":
		vm.template = true
		vm.cfg["template"] = "1"
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
		cfg["smbios1"] = "uuid=" + uuid() // a clone gets a new uuid
		cfg["scsi0"] = strings.Replace(cfg["scsi0"], "vm-"+strconv.Itoa(id), "vm-"+strconv.Itoa(newid), 1)
		f.vms[newid] = &fakeVM{cfg: cfg, pool: r.Form.Get("pool")}
		reply(w, 200, f.task(key))
	case r.Method == "GET" && sub == "/status/current":
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
