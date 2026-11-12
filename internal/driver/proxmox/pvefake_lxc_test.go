package proxmox

// The container part of the fake Proxmox (T-079). It follows the host
// facts of T-038 and T-079 H10: cores, memory and net0 apply live on a
// running container (C5); env and features wait for the next start, and
// so does a delete of env (C4); the token may not set other features,
// mount points with a host path, devices or unprivileged=0 (H10).

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
)

var (
	reLxc = regexp.MustCompile(`^/nodes/([^/]+)/lxc/(\d+)(/.*)?$`)
	// The env pattern of PVE 9.2 (host C2).
	reCTEnv = regexp.MustCompile(`^\w+=[^\x00-\x08\x0a-\x1f\x7f]*(\x00\w+=[^\x00-\x08\x0a-\x1f\x7f]*)*$`)
	reMPDev = regexp.MustCompile(`^(mp|dev)\d+$`)
)

// ctPerm answers like the permission check of PVE for the token (H10).
// It returns a status and a message, or 0.
func ctPerm(form map[string][]string) (int, string) {
	for k, v := range form {
		switch {
		case k == "features" && v[0] != "nesting=1" && v[0] != "nesting=0":
			return 403, "Permission check failed (changing feature flags (except nesting) is only allowed for root@pam)"
		case strings.HasPrefix(k, "dev") && reMPDev.MatchString(k):
			return 403, "Permission check failed (configuring device passthrough is only allowed for root@pam)"
		case strings.HasPrefix(k, "mp") && reMPDev.MatchString(k) && strings.HasPrefix(v[0], "/"):
			return 403, "Permission check failed (mount point type bind is only allowed for root@pam)"
		}
	}
	return 0, ""
}

// ctDigest covers the config and the pending values, as Proxmox gives one
// digest for both views (host H1).
func ctDigest(vm *fakeVM) string {
	all := map[string]string{}
	for k, v := range vm.cfg {
		all[k] = v
	}
	for k, v := range vm.pending {
		all["pending:"+k] = v
	}
	for k := range vm.delete {
		all["delete:"+k] = "1"
	}
	return digest(all)
}

func (f *fakePVE) createCT(w http.ResponseWriter, r *http.Request, key string) {
	var id int
	fmt.Sscan(r.Form.Get("vmid"), &id)
	if _, taken := f.vms[id]; taken {
		fail(w, 500, fmt.Sprintf("CT %d already exists on node '%s'", id, f.node), nil)
		return
	}
	if r.Form.Get("unprivileged") == "0" {
		fail(w, 403, "Permission check failed (/, Sys.Modify)", nil)
		return
	}
	if st, msg := ctPerm(r.Form); st != 0 {
		fail(w, st, msg, nil)
		return
	}
	if env := r.Form.Get("env"); env != "" && !reCTEnv.MatchString(env) {
		fail(w, 400, "Parameter verification failed.", map[string]string{"env": "value does not match the regex pattern"})
		return
	}
	if !f.files[r.Form.Get("ostemplate")] {
		fail(w, 500, "volume '"+r.Form.Get("ostemplate")+"' does not exist", nil)
		return
	}
	size := strings.TrimPrefix(r.Form.Get("rootfs"), "local-lvm:")
	cfg := map[string]string{"arch": "amd64", "ostype": "debian", "unprivileged": "1",
		"rootfs": fmt.Sprintf("local-lvm:vm-%d-disk-0,size=%sG", id, size)}
	for k, v := range r.Form {
		switch k {
		case "vmid", "pool", "ostemplate", "rootfs", "ssh-public-keys", "unprivileged", "start":
		case "net0":
			cfg[k] = strings.Replace(v[0], "name=eth0,", "name=eth0,hwaddr=BC:24:11:00:00:01,", 1)
		default:
			cfg[k] = v[0]
		}
	}
	f.vms[id] = &fakeVM{ct: true, cfg: cfg, pool: r.Form.Get("pool")}
	reply(w, 200, f.task(key))
}

// addCT puts a container into the model, for example one that a person made.
func (f *fakePVE) addCT(id int, pool string, cfg map[string]string, running bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.vms[id] = &fakeVM{ct: true, cfg: cfg, pool: pool, running: running}
}

// ctNext is the view after the next start.
func ctNext(vm *fakeVM) map[string]string {
	out := map[string]string{}
	for k, v := range vm.cfg {
		if !vm.delete[k] {
			out[k] = v
		}
	}
	for k, v := range vm.pending {
		out[k] = v
	}
	return out
}

func (f *fakePVE) lxc(w http.ResponseWriter, r *http.Request, key string, id int, vm *fakeVM, sub string) {
	switch {
	case r.Method == "GET" && sub == "/config":
		if n, ok := f.lateCT[id]; ok {
			if n <= 1 {
				delete(f.lateCT, id)
				f.convertCT(id, vm)
			} else {
				f.lateCT[id] = n - 1
			}
		}
		view := ctNext(vm)
		if r.Form.Get("current") == "1" {
			view = vm.cfg
		}
		out := map[string]any{}
		for k, v := range view {
			out[k] = v
			if k == "lxc" { // raw lxc.* lines: [[key, value], …]
				var pairs [][]string
				for _, l := range strings.Split(v, "\n") {
					kk, vv, _ := strings.Cut(l, ": ")
					pairs = append(pairs, []string{kk, vv})
				}
				out[k] = pairs
			}
		}
		if f.lockReads[id] > 0 {
			f.lockReads[id]--
			out["lock"] = "create"
		}
		out["digest"] = ctDigest(vm)
		reply(w, 200, out)

	case r.Method == "PUT" && sub == "/config":
		if f.ctWriteBeforeConfig {
			f.ctWriteBeforeConfig = false
			vm.cfg["hostname"] = "edited-before-write"
		}
		if d := r.Form.Get("digest"); d != "" && d != ctDigest(vm) {
			fail(w, 500, "detected modified configuration - file changed by other user? Try again.", nil)
			return
		}
		if _, ok := r.Form["unprivileged"]; ok {
			fail(w, 500, "unable to modify read-only option: 'unprivileged'", nil)
			return
		}
		if st, msg := ctPerm(r.Form); st != 0 {
			fail(w, st, msg, nil)
			return
		}
		if env := r.Form.Get("env"); env != "" && !reCTEnv.MatchString(env) {
			fail(w, 400, "Parameter verification failed.", map[string]string{"env": "value does not match the regex pattern"})
			return
		}
		cold := map[string]bool{"env": true, "features": true}
		for k, v := range r.Form {
			switch {
			case k == "digest":
			case k == "delete":
				for _, d := range strings.Split(v[0], ",") {
					if d == f.failNextPUT {
						f.failNextPUT = ""
						continue
					}
					switch {
					case vm.running && cold[d]:
						if vm.delete == nil {
							vm.delete = map[string]bool{}
						}
						vm.delete[d] = true
						delete(vm.pending, d)
					default:
						// A stopped CT takes the delete at once, also of
						// a pending value of the key.
						delete(vm.cfg, d)
						delete(vm.pending, d)
					}
				}
			case vm.running && cold[k]:
				if vm.pending == nil {
					vm.pending = map[string]string{}
				}
				vm.pending[k] = v[0]
				delete(vm.delete, k)
			default:
				vm.cfg[k] = v[0]
			}
		}
		reply(w, 200, nil)

	case r.Method == "GET" && sub == "/status/current":
		st := "stopped"
		if vm.running {
			st = "running"
		}
		reply(w, 200, map[string]any{"status": st, "vmid": id, "type": "lxc"})

	case r.Method == "GET" && sub == "/pending":
		// Like PVE: one row per key, "pending" for a new value, "delete" for
		// a pending delete (host C4).
		rows := []map[string]any{}
		for k, v := range vm.cfg {
			row := map[string]any{"key": k, "value": v}
			if p, ok := vm.pending[k]; ok {
				row["pending"] = p
			}
			if vm.delete[k] {
				row["delete"] = 1
			}
			rows = append(rows, row)
		}
		for k, p := range vm.pending {
			if _, ok := vm.cfg[k]; !ok {
				rows = append(rows, map[string]any{"key": k, "pending": p})
			}
		}
		reply(w, 200, rows)

	case r.Method == "POST" && sub == "/template":
		if vm.running {
			fail(w, 500, "CT is running", nil)
			return
		}
		// Like PVE 9.2: null, and a worker converts later (host T-082
		// H-B1): the next reads still show no template.
		f.lateCT[id] = f.ctTemplateReads
		if f.lateCT[id] == 0 {
			f.convertCT(id, vm)
		}
		reply(w, 200, nil)

	case r.Method == "POST" && sub == "/clone":
		var nid int
		fmt.Sscan(r.Form.Get("newid"), &nid)
		if _, taken := f.vms[nid]; taken {
			fail(w, 500, fmt.Sprintf("CT %d already exists", nid), nil)
			return
		}
		cfg := map[string]string{}
		for k, v := range vm.cfg {
			if k != "template" {
				cfg[k] = v
			}
		}
		cfg["hostname"] = r.Form.Get("hostname")
		cfg["rootfs"] = fmt.Sprintf("local-lvm:vm-%d-disk-0,size=%s", nid, parseProps(vm.cfg["rootfs"])["size"])
		f.vms[nid] = &fakeVM{ct: true, cfg: cfg, pool: r.Form.Get("pool")}
		reply(w, 200, f.task(key))

	case r.Method == "PUT" && sub == "/resize":
		vm.cfg["rootfs"] = strings.Replace(vm.cfg["rootfs"], "size="+parseProps(vm.cfg["rootfs"])["size"], "size="+r.Form.Get("size"), 1)
		reply(w, 200, f.task(key))

	case r.Method == "GET" && sub == "/interfaces":
		if !vm.running {
			reply(w, 200, nil)
			return
		}
		ip := strings.SplitN(parseProps(vm.cfg["net0"])["ip"], "/", 2)[0]
		if ip == "dhcp" {
			ip = "192.0.2.77"
		}
		reply(w, 200, []map[string]any{
			{"name": "lo", "ip-addresses": []map[string]any{{"ip-address-type": "inet", "ip-address": "127.0.0.1", "prefix": 8}}},
			{"name": "eth0", "hwaddr": "bc:24:11:00:00:01", "ip-addresses": []map[string]any{{"ip-address-type": "inet", "ip-address": ip, "prefix": 24}}},
		})

	case r.Method == "POST" && strings.HasPrefix(sub, "/status/"):
		upid := f.task(key)
		switch sub {
		case "/status/start":
			if vm.running {
				f.tasks[upid].exit = fmt.Sprintf("CT %d already running", id)
			} else if f.tasks[upid].exit == "OK" {
				vm.running = true
				vm.cfg = ctNext(vm) // a start applies the pending values
				vm.pending, vm.delete = nil, nil
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
		if r.URL.Query().Get("purge") != "1" {
			fail(w, 501, "Unexpected content for method 'DELETE'", nil)
			return
		}
		if f.lockReads[id] > 0 {
			fail(w, 500, fmt.Sprintf("CT %d is locked (create)", id), nil)
			return
		}
		if vm.running {
			fail(w, 500, fmt.Sprintf("CT %d is running - destroy failed", id), nil)
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

// upload is the multipart upload of a template (host B1): the field order
// of the web UI, a Content-Length, the SHA-256 checked by the task, and
// .tar.gz/.tar.zst/.tar.xz/.tar names only (B2).
func (f *fakePVE) upload(w http.ResponseWriter, r *http.Request, key, storage string) {
	if r.ContentLength < 0 || len(r.TransferEncoding) > 0 {
		fail(w, 501, "chunked transfer encoding not supported", nil)
		return
	}
	mr, err := r.MultipartReader()
	if err != nil {
		fail(w, 400, "bad multipart", nil)
		return
	}
	fields := map[string]string{}
	var order []string
	var data []byte
	var name string
	for {
		p, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			fail(w, 400, "bad multipart", nil)
			return
		}
		order = append(order, p.FormName())
		b, _ := io.ReadAll(p)
		if p.FileName() != "" {
			data, name = b, p.FileName()
		} else {
			fields[p.FormName()] = string(b)
		}
	}
	f.uploadOrder = order
	if strings.Join(order, ",") != "content,checksum-algorithm,checksum,filename" {
		fail(w, 400, "", nil) // pveproxy: an empty body, the reason only in its journal
		return
	}
	if fields["content"] != "vztmpl" || !regexp.MustCompile(`\.tar(\.(gz|zst|xz))?$`).MatchString(name) {
		fail(w, 400, "Parameter verification failed.", map[string]string{"filename": "wrong file extension"})
		return
	}
	upid := f.task(key)
	sum := sha256.Sum256(data)
	if fields["checksum-algorithm"] != "sha256" || fields["checksum"] != hex.EncodeToString(sum[:]) {
		f.tasks[upid].exit = "checksum mismatch"
	}
	if f.tasks[upid].exit == "OK" {
		f.files[storage+":vztmpl/"+name] = true
		f.uploads[name] = data
	}
	reply(w, 200, upid)
}

// convertCT makes a container a template (lvmthin: base- volume).
func (f *fakePVE) convertCT(id int, vm *fakeVM) {
	vm.template = true
	vm.cfg["template"] = "1"
	vm.cfg["rootfs"] = strings.Replace(vm.cfg["rootfs"], fmt.Sprintf(":vm-%d-disk-", id), fmt.Sprintf(":base-%d-disk-", id), 1)
}
