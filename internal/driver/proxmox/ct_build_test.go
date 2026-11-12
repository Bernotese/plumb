package proxmox

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Bernotese/plumb/internal/driver"
)

var ctBuilt = driver.BuiltImage{Image: "web-ct", Version: 1, Digest: strings.Repeat("b", 64)}

// ctBuildEnv makes the build container of web-ct v1 and stops it, as the
// agent's seal does.
func ctBuildEnv(t *testing.T) (*fakePVE, *Driver, driver.GuestID) {
	t.Helper()
	f := newFakePVE(t)
	f.files[ctTemplate] = true
	d := f.driver(t)
	gid, err := d.CreateBuild(context.Background(), driver.BuildSpec{Image: "web-ct", Version: 1, Base: ctTemplate,
		Cores: 1, MemoryMB: 512, DiskGB: 4, Bridge: "vmbr0", IP: "dhcp"}, driver.Bootstrap{Token: ctToken})
	if err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.vms[9000].running = false
	f.mu.Unlock()
	return f, d, gid
}

// T-082 (D-234): build container, version template, clone Target.
func TestCTBuildToVersionToTarget(t *testing.T) {
	f, d, gid := ctBuildEnv(t)
	ctx := context.Background()
	p := f.lastParams("POST /nodes/pve/lxc")
	if p.Get("hostname") != "plumb-build-web-ct-v1" || p.Get("onboot") != "0" || p.Get("env") != "PLUMB_ENROLL_TOKEN="+ctToken ||
		p.Has("ssh-public-keys") || !strings.HasPrefix(p.Get("description"), "plumb-build: web-ct v1\n") {
		t.Errorf("build create %v", p)
	}
	if bs, err := d.ListBuilds(ctx); err != nil || len(bs) != 1 || bs[0].ID != gid || bs[0].Image != "web-ct" {
		t.Errorf("ListBuilds %+v %v", bs, err)
	}
	ref, err := d.MakeImage(ctx, gid, ctBuilt)
	if err != nil {
		t.Fatal(err)
	}
	tpl := f.vms[9000]
	if !tpl.template || tpl.cfg["env"] != "" || tpl.cfg["hostname"] != "plumb-img-web-ct-v1" || tpl.cfg["nameserver"] != "" ||
		!isVersionOf(tpl.cfg["description"], "web-ct", 1, ctBuilt.Digest) {
		t.Fatalf("template %+v", tpl.cfg)
	}
	if again, err := d.MakeImage(ctx, gid, ctBuilt); err != nil || again != ref {
		t.Errorf("MakeImage again: %s %v", again, err)
	}
	if found, err := d.FindImage(ctx, "web-ct", 1, ctBuilt.Digest); err != nil || found != ref {
		t.Errorf("FindImage %s %v", found, err)
	}
	// A Target of the version.
	s := ctSpec()
	s.Image, s.ImageName, s.ImageVersion, s.SSHKeys, s.DiskGB = ref, "web-ct", 1, nil, 8
	id, err := d.Create(ctx, s, driver.Bootstrap{Token: "clone-token-abcdefghijklmnopqrstuvwxyz0123456789"})
	if err != nil {
		t.Fatal(err)
	}
	cp := f.lastParams("PUT /nodes/pve/lxc/" + string(id) + "/config")
	if cp.Get("env") != "PLUMB_ENROLL_TOKEN=clone-token-abcdefghijklmnopqrstuvwxyz0123456789" || cp.Get("features") != "nesting=1" ||
		cp.Get("onboot") != "1" || cp.Get("digest") == "" || cp.Get("net0") != ctNet0(s) {
		t.Errorf("clone config %v", cp)
	}
	if f.lastParams("POST /nodes/pve/lxc/9000/clone").Get("full") != "1" || f.count("PUT /nodes/pve/lxc/"+string(id)+"/resize") != 1 {
		t.Error("not a full clone or not resized")
	}
	st, err := d.Observe(ctx, id)
	if err != nil || st.Power != driver.PowerRunning || !st.TokenInConfig || st.Spec.DiskGB != 8 || st.Spec.ImageVersion != 1 {
		t.Errorf("target %+v %v", st, err)
	}
	if c, err := d.Diff(s, st); err != nil || len(c) != 0 {
		t.Errorf("diff %+v %v", c, err)
	}
	// Retention sees the version; RemoveImage deletes a container template.
	objs, _ := d.ListImages(ctx, "web-ct")
	var v *driver.ImageObject
	for i := range objs {
		if objs[i].Kind == driver.ImageVersionTemplate {
			v = &objs[i]
		}
	}
	if v == nil || v.Version != 1 || v.SizeBytes != 4<<30 {
		t.Fatalf("objects %+v", objs)
	}
	if err := d.RemoveImage(ctx, *v); err != nil {
		t.Fatal(err)
	}
	if _, ok := f.vms[9000]; ok || f.count("DELETE /nodes/pve/lxc/9000") != 1 {
		t.Error("template not removed by the lxc path")
	}
}

// Review S8: no template while env is in either view or pending; a clone
// that inherited env is removed; a clone gets no SSH keys (D-251).
func TestCTBuildEnvGuards(t *testing.T) {
	ctx := context.Background()
	t.Run("pending env", func(t *testing.T) {
		f, d, gid := ctBuildEnv(t)
		f.mu.Lock()
		f.vms[9000].pending = map[string]string{"env": "PLUMB_ENROLL_TOKEN=late"}
		f.mu.Unlock()
		// ClearBootstrap sees the token in the next view and deletes it;
		// a stopped CT takes the delete at once.
		if _, err := d.MakeImage(ctx, gid, ctBuilt); err != nil {
			t.Fatal(err)
		}
		if f.vms[9000].cfg["env"] != "" || f.vms[9000].pending["env"] != "" {
			t.Error("env left in the template")
		}
	})
	t.Run("env that the clear cannot remove", func(t *testing.T) {
		f, d, gid := ctBuildEnv(t)
		f.failNextPUT = "env" // the delete of env is ignored by the fake
		_, err := d.MakeImage(ctx, gid, ctBuilt)
		if err == nil || !strings.Contains(err.Error(), "still has an env value") || f.vms[9000].template {
			t.Fatalf("MakeImage: %v, template %v", err, f.vms[9000].template)
		}
	})
	t.Run("inherited env", func(t *testing.T) {
		f, d, gid := ctBuildEnv(t)
		ref, err := d.MakeImage(ctx, gid, ctBuilt)
		if err != nil {
			t.Fatal(err)
		}
		f.mu.Lock()
		f.vms[9000].cfg["env"] = "PLUMB_ENROLL_TOKEN=someone-wrote-it"
		f.mu.Unlock()
		s := ctSpec()
		s.Image, s.SSHKeys = ref, nil
		_, err = d.Create(ctx, s, driver.Bootstrap{Token: ctToken})
		if err == nil || !strings.Contains(err.Error(), "inherited an env value") || strings.Contains(err.Error(), "someone-wrote-it") {
			t.Fatalf("clone: %v", err)
		}
		if len(f.vms) != 1 {
			t.Errorf("the clone is left: %d guests", len(f.vms))
		}
	})
	t.Run("ssh keys on a clone", func(t *testing.T) {
		_, d, gid := ctBuildEnv(t)
		ref, _ := d.MakeImage(ctx, gid, ctBuilt)
		s := ctSpec()
		s.Image = ref
		if _, err := d.Create(ctx, s, driver.Bootstrap{Token: ctToken}); err == nil || !strings.Contains(err.Error(), "D-251") {
			t.Errorf("keys on a clone: %v", err)
		}
	})
	t.Run("running build container", func(t *testing.T) {
		f, d, gid := ctBuildEnv(t)
		f.vms[9000].running = true
		if _, err := d.MakeImage(ctx, gid, ctBuilt); err == nil || !strings.Contains(err.Error(), "not stopped") {
			t.Errorf("running: %v", err)
		}
	})
}

func TestCTRemoveBuild(t *testing.T) {
	f, d, gid := ctBuildEnv(t)
	if err := d.RemoveBuild(context.Background(), gid, "web-ct", 2); err == nil {
		t.Error("removed with the wrong version")
	}
	if err := d.RemoveBuild(context.Background(), gid, "web-ct", 1); err != nil {
		t.Fatal(err)
	}
	if len(f.vms) != 0 {
		t.Error("build container left")
	}
	if _, err := d.Observe(context.Background(), gid); !errors.Is(err, driver.ErrNotFound) {
		t.Errorf("observe: %v", err)
	}
}

// runCTBuild against pvefake (the host runs it as TestHostCTBuild).
func TestCTBuildLifecycleFake(t *testing.T) {
	e := newCTFetch(t)
	t.Cleanup(func() { // registered first, so it runs after the cleanups of runCTBuild
		if len(e.f.vms) != 0 {
			t.Errorf("guests left: %d", len(e.f.vms))
		}
	})
	runCTBuild(t, e.d, e.spec, "vmbr0")
}

// Inspect the wire boundary: final state alone cannot prove S8 ordering.
type ctQATransport func(*http.Request) (*http.Response, error)

func (fn ctQATransport) RoundTrip(r *http.Request) (*http.Response, error) { return fn(r) }

func TestCTTemplateBoundaryQA(t *testing.T) {
	f, d, gid := ctBuildEnv(t)
	f.vms[9000].pending = map[string]string{"env": "PLUMB_ENROLL_TOKEN=pending"}
	f.ctTemplateReads = 8
	next := d.c.http.Transport
	called := false
	d.c.http.Transport = ctQATransport(func(r *http.Request) (*http.Response, error) {
		if r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/template") {
			called = true
			f.mu.Lock()
			vm := f.vms[9000]
			if vm.cfg["env"] != "" || vm.pending["env"] != "" || vm.delete["env"] {
				t.Error("template call before all env views were cleared")
			}
			f.mu.Unlock()
		}
		return next.RoundTrip(r)
	})
	if _, err := d.MakeImage(context.Background(), gid, ctBuilt); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if !called || !f.vms[9000].template || f.lateCT[9000] != 0 {
		t.Fatal("MakeImage returned before the late template worker completed")
	}
}

func TestCTPendingOnlyGuardsQA(t *testing.T) {
	for _, clone := range []bool{false, true} {
		t.Run(map[bool]string{false: "template", true: "clone"}[clone], func(t *testing.T) {
			f, d, gid := ctBuildEnv(t)
			ref := driver.ImageRef("")
			if clone {
				var err error
				ref, err = d.MakeImage(context.Background(), gid, ctBuilt)
				if err != nil {
					t.Fatal(err)
				}
			} else {
				delete(f.vms[9000].cfg, "env")
			}
			// Inject a pending row which neither config view exposes. Even a
			// pending delete must block use of the container as a clean template.
			next := d.c.http.Transport
			d.c.http.Transport = ctQATransport(func(r *http.Request) (*http.Response, error) {
				if r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/pending") && (!clone || !strings.Contains(r.URL.Path, "/9000/")) {
					return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"data":[{"key":"env","delete":1}]}`))}, nil
				}
				return next.RoundTrip(r)
			})
			var err error
			if clone {
				s := ctSpec()
				s.Image, s.SSHKeys = ref, nil
				_, err = d.Create(context.Background(), s, driver.Bootstrap{Token: ctToken})
			} else {
				_, err = d.MakeImage(context.Background(), gid, ctBuilt)
			}
			if err == nil || !strings.Contains(err.Error(), "pending") {
				t.Fatalf("pending env accepted: %v", err)
			}
			if clone {
				if len(f.vms) != 1 || f.count("PUT /nodes/pve/lxc/9001/config") != 0 || f.count("POST /nodes/pve/lxc/9001/status/start") != 0 {
					t.Fatal("tainted clone configured, started, or left behind")
				}
			} else if f.count("POST /nodes/pve/lxc/9000/template") != 0 {
				t.Fatal("tainted build converted")
			}
		})
	}
}
