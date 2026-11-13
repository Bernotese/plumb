package proxmox

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Bernotese/plumb/internal/driver"
	"github.com/Bernotese/plumb/internal/wire"
)

// Review T-012 #1: a server that ignores the poolid filter does not make
// a foreign guest ours.
func TestPoolGuardFiltersTheAnswer(t *testing.T) {
	f := newFakePVE(t)
	f.allPools = true
	d := f.driver(t)
	ctx := context.Background()
	f.addVM(9010, "other", map[string]string{"name": "foreign"}, true)
	f.addVM(9011, fakePool, map[string]string{"name": "ours"}, true)
	for _, call := range []func() error{
		func() error { _, err := d.Observe(ctx, "9010"); return err },
		func() error { return d.Power(ctx, "9010", driver.PowerStop) },
		func() error { return d.Delete(ctx, "9010") },
		func() error {
			return d.Update(ctx, "9010", []wire.Change{{Attr: driver.AttrCores, To: 1}}, nil)
		},
	} {
		if err := call(); err == nil {
			t.Error("a guest of another pool was treated as ours")
		}
	}
	if !f.vms[9010].running || f.count("POST /nodes/pve/qemu/9010") != 0 || f.count("DELETE /nodes/pve/qemu/9010") != 0 {
		t.Errorf("the foreign guest was touched: %v", f.log)
	}
	if _, err := d.Observe(ctx, "9011"); err != nil {
		t.Errorf("our guest: %v", err)
	}
}

// Review T-012 #2: the token never shows in an error, raw or base64.
func TestTokenScrubbedFromErrors(t *testing.T) {
	f := newFakePVE(t)
	d := f.driver(t)
	ctx := context.Background()
	ref, _ := d.FetchImage(ctx, image)
	const token = "SECRET-enroll-token-0123456789"
	// Create: PVE echoes the smbios1 value in a parameter error.
	f.echoParam["POST /nodes/pve/qemu/9001/config"] = "smbios1"
	_, err := d.Create(ctx, guest(ref), driver.Bootstrap{Token: token})
	if err == nil {
		t.Fatal("want an error")
	}
	if strings.Contains(err.Error(), token) || strings.Contains(err.Error(), b64(token)) || !strings.Contains(err.Error(), "[redacted]") {
		t.Errorf("create error: %v", err)
	}
	var ae *APIError
	if !errors.As(err, &ae) {
		t.Errorf("the error lost its type: %T", err)
	}
	// Update (re-enrollment): the same, and a failed task log too.
	id, err := d.Create(ctx, guest(ref), driver.Bootstrap{Token: "first"})
	if err != nil {
		t.Fatal(err)
	}
	change := []wire.Change{{Attr: driver.AttrEnrollToken, Sensitive: true, Risk: wire.RiskDisruptive}}
	key := "POST /nodes/pve/qemu/" + string(id) + "/config"
	f.echoParam[key] = "smbios1"
	if err := d.Update(ctx, id, change, &driver.Bootstrap{Token: token}); err == nil || strings.Contains(err.Error(), b64(token)) {
		t.Errorf("update error: %v", err)
	}
	f.failNext[key] = "smbios1 " + b64(token) + " rejected"
	err = d.Update(ctx, id, change, &driver.Bootstrap{Token: token})
	var te *TaskError
	if !errors.As(err, &te) || strings.Contains(err.Error(), b64(token)) {
		t.Errorf("task error: %v", err)
	}
}

// Review T-012 #3: a 2xx answer without a task is no success where a task
// is required, and a non-JSON page is never success.
func TestTaskAnswerChecks(t *testing.T) {
	f := newFakePVE(t)
	c := f.client(t)
	ctx := context.Background()
	f.addVM(9001, fakePool, map[string]string{"name": "x"}, false)
	f.nullAnswer["POST /nodes/pve/qemu/9001/status/start"] = true
	if err := c.Task(ctx, fakeNode, "POST", "/nodes/pve/qemu/9001/status/start", nil); err == nil || !strings.Contains(err.Error(), "no task id") {
		t.Errorf("null answer for a task: %v", err)
	}
	f.notJSON["POST /nodes/pve/qemu/9001/status/start"] = true
	if err := c.Task(ctx, fakeNode, "POST", "/nodes/pve/qemu/9001/status/start", nil); !errors.Is(err, errNotJSON) {
		t.Errorf("HTML answer: %v", err)
	}
	f.notJSON["POST /nodes/pve/qemu/9001/config"] = true
	if err := c.MaybeTask(ctx, fakeNode, "POST", "/nodes/pve/qemu/9001/config", nil); !errors.Is(err, errNotJSON) {
		t.Errorf("HTML answer for config: %v", err)
	}
	f.nullAnswer["POST /nodes/pve/qemu/9001/config"] = true
	if err := c.MaybeTask(ctx, fakeNode, "POST", "/nodes/pve/qemu/9001/config", nil); err != nil {
		t.Errorf("null answer for config: %v", err)
	}
	if f.vms[9001].running {
		t.Error("the VM started although no call succeeded")
	}
}

// Review T-012 #4: failed status reads are retried; a task that does not
// end is stopped when the bounded wait gives up.
func TestTaskWaitBounds(t *testing.T) {
	f := newFakePVE(t)
	ctx := context.Background()
	f.addVM(9001, fakePool, map[string]string{"name": "x"}, false)
	c := f.client(t)
	f.statusFails = maxPollFailures - 1
	if err := c.Task(ctx, fakeNode, "POST", "/nodes/pve/qemu/9001/status/start", nil); err != nil {
		t.Errorf("transient status failures: %v", err)
	}
	f.statusFails = maxPollFailures
	if err := c.Task(ctx, fakeNode, "POST", "/nodes/pve/qemu/9001/status/shutdown", nil); err == nil || len(f.stoppedTasks) != 1 {
		t.Errorf("status never readable: %v, stopped %v", err, f.stoppedTasks)
	}
	slow, err := NewClient(ClientConfig{URL: f.srv.URL, TokenID: fakeToken, Secret: fakeSecret, CAFile: f.caFile,
		TaskPoll: time.Millisecond, TaskTimeout: 50 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	f.taskPolls = 1 << 30
	err = slow.Task(ctx, fakeNode, "POST", "/nodes/pve/qemu/9001/status/stop", nil)
	if !errors.Is(err, ErrTaskTimeout) || len(f.stoppedTasks) != 2 {
		t.Errorf("endless task: %v, stopped %v", err, f.stoppedTasks)
	}
	// The caller's context ending stops the task too.
	short, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
	defer cancel()
	if err := c.Task(short, fakeNode, "POST", "/nodes/pve/qemu/9001/status/stop", nil); !errors.Is(err, context.DeadlineExceeded) || len(f.stoppedTasks) != 3 {
		t.Errorf("context end: %v, stopped %v", err, f.stoppedTasks)
	}
}

// Review T-012 #5, #6, #7, #9, #11.
func TestReviewMinors(t *testing.T) {
	f := newFakePVE(t)
	d := f.driver(t)
	ctx := context.Background()
	ref, _ := d.FetchImage(ctx, image)

	// #5: property-string injection is refused before any call.
	for name, mutate := range map[string]func(*driver.GuestSpec){
		"bridge":  func(s *driver.GuestSpec) { s.Bridge = "vmbr0,firewall=0" },
		"ip":      func(s *driver.GuestSpec) { s.IP = "192.0.2.5/24,gw=1.1.1.1" },
		"gateway": func(s *driver.GuestSpec) { s.Gateway = "1.1.1.1,ip6=auto" },
		"user":    func(s *driver.GuestSpec) { s.User = "root,x" },
	} {
		spec := guest(ref)
		mutate(&spec)
		before := f.countExact("POST /nodes/pve/qemu/" + string(ref) + "/clone")
		if _, err := d.Create(ctx, spec, driver.Bootstrap{Token: "t"}); err == nil {
			t.Errorf("%s injection accepted", name)
		}
		if f.countExact("POST /nodes/pve/qemu/"+string(ref)+"/clone") != before {
			t.Errorf("%s: cloned before the check", name)
		}
	}

	// #6: a failed template conversion leaves no VM; the next try works.
	other := image
	other.SHA256 = strings.Repeat("c", 64)
	f.failNext["POST /nodes/pve/qemu"] = "storage full"
	if _, err := d.FetchImage(ctx, other); err == nil {
		t.Fatal("want an error")
	}
	for id, vm := range f.vms {
		if strings.HasPrefix(vm.cfg["name"], "plumb-img-") && !vm.template {
			t.Errorf("half-made template VM %d stayed", id)
		}
	}
	if _, err := d.FetchImage(ctx, other); err != nil {
		t.Errorf("second try: %v", err)
	}

	// #7: a 400 that is not "already exists" stops the search at once.
	f.echoParam["GET /cluster/nextid"] = "vmid"
	if _, err := d.nextID(ctx); err == nil || strings.Contains(err.Error(), "no free VMID") {
		t.Errorf("nextid parameter error: %v", err)
	}

	// #9: the secret never prints.
	cfg := ClientConfig{URL: f.srv.URL, TokenID: fakeToken, Secret: fakeSecret}
	c := f.client(t)
	for _, s := range []string{fmt.Sprintf("%v %+v %v %+v", cfg, cfg, c, c), fmt.Sprint(cfg.LogValue()), fmt.Sprint(c.LogValue())} {
		if strings.Contains(s, fakeSecret) {
			t.Errorf("secret printed: %s", s)
		}
	}

	// #11: sshkeys are path-unescaped: "+" stays "+".
	s := specOf("x", map[string]string{"sshkeys": "ssh-rsa%20AAAA+B%2Fc%3D%20me"})
	if len(s.SSHKeys) != 1 || s.SSHKeys[0] != "ssh-rsa AAAA+B/c= me" {
		t.Errorf("sshkeys: %q", s.SSHKeys)
	}
}

// Review T-012 #9: a redirect is not followed (it would carry the token).
func TestNoRedirect(t *testing.T) {
	f := newFakePVE(t)
	c := f.client(t)
	if c.http.CheckRedirect == nil {
		t.Fatal("no redirect rule")
	}
	if err := c.http.CheckRedirect(nil, nil); err == nil {
		t.Error("redirects are followed")
	}
}

// Review N1: when another party takes the VMID between nextid and the
// create, the failed create cleans up nothing of theirs.
func TestCleanupNeverTouchesARacedGuest(t *testing.T) {
	for _, pool := range []string{"other", fakePool} { // a foreign pool, and ours with another name
		f := newFakePVE(t)
		d := f.driver(t)
		ctx := context.Background()
		f.raceVMID, f.racePool = 9000, pool
		if _, err := d.FetchImage(ctx, image); err == nil || !strings.Contains(err.Error(), "already exists") {
			t.Fatalf("pool %s: %v", pool, err)
		}
		vm, ok := f.vms[9000]
		if !ok || !vm.running || f.countExact("DELETE /nodes/pve/qemu/9000") != 0 || f.countExact("POST /nodes/pve/qemu/9000/status/stop") != 0 {
			t.Errorf("pool %s: the raced guest was touched: %v", pool, f.log)
		}
		// Create: the same race on the clone target.
		f.raceVMID, f.racePool = 0, ""
		ref, err := d.FetchImage(ctx, image)
		if err != nil {
			t.Fatal(err)
		}
		next := 9002
		f.raceVMID, f.racePool = next, pool
		if _, err := d.Create(ctx, guest(ref), driver.Bootstrap{Token: "t"}); err == nil {
			t.Fatalf("pool %s: create on a raced id worked", pool)
		}
		if vm, ok := f.vms[next]; !ok || !vm.running || vm.cfg["name"] != "someone-else" {
			t.Errorf("pool %s: the raced clone target was touched", pool)
		}
	}
}

// Review N2: a clone that fails after Proxmox made the VM leaves nothing.
func TestCloneFailureCleansUp(t *testing.T) {
	f := newFakePVE(t)
	d := f.driver(t)
	ctx := context.Background()
	ref, _ := d.FetchImage(ctx, image)
	f.failNext["POST /nodes/pve/qemu/"+string(ref)+"/clone"] = "clone failed: storage full"
	if _, err := d.Create(ctx, guest(ref), driver.Bootstrap{Token: "t"}); err == nil {
		t.Fatal("want an error")
	}
	if _, ok := f.vms[9001]; ok {
		t.Error("the half-made clone stayed")
	}
}
