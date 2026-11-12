package proxmox

// The start-up check (T-053, D-183) against the fake PVE.

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// checkDriver is the fake driver with a short start wait.
func checkDriver(t *testing.T, f *fakePVE) *Driver {
	t.Helper()
	d := f.driver(t)
	d.cfg.StartWait, d.cfg.StartPoll = 200*time.Millisecond, time.Millisecond
	return d
}

func (f *fakePVE) requests(key string) int { return f.countExact(key) }

func TestCheckGoodHost(t *testing.T) {
	f := newFakePVE(t)
	v, err := checkDriver(t, f).Check(context.Background())
	if err != nil || v != "9.2.26" {
		t.Fatalf("Check = %q %v", v, err)
	}
}

func TestCheckVersions(t *testing.T) {
	for v, want := range map[string]string{
		"8.4.1": "too old", "9.0.12": "too old", "9.1.0": "", "9.9.3": "", "9.10.0": "", "10.0.1": "",
		"x": "cannot read", "9": "cannot read", "": "cannot read",
	} {
		f := newFakePVE(t)
		f.version = v
		_, err := checkDriver(t, f).Check(context.Background())
		var ve *VersionError
		switch want {
		case "":
			if err != nil {
				t.Errorf("%q: %v", v, err)
			}
		case "too old":
			if !errors.As(err, &ve) || !ve.Readable {
				t.Errorf("%q: %v", v, err)
			}
		default:
			if !errors.As(err, &ve) || ve.Readable {
				t.Errorf("%q: %v", v, err)
			}
		}
	}
}

// A wrong token: one request, never tried again.
func TestCheckBadToken(t *testing.T) {
	f := newFakePVE(t)
	c, err := NewClient(ClientConfig{URL: f.srv.URL, TokenID: fakeToken, Secret: "wrong-secret", CAFile: f.caFile})
	if err != nil {
		t.Fatal(err)
	}
	d := checkDriver(t, f)
	d.c = c
	_, err = d.Check(context.Background())
	var ae *APIError
	if !errors.As(err, &ae) || ae.Status != 401 {
		t.Fatalf("err = %v", err)
	}
	if strings.Contains(err.Error(), "wrong-secret") {
		t.Error("the secret is in the error")
	}
	if f.authFails != 1 {
		t.Errorf("requests with the bad token = %d, want 1 (no retry)", f.authFails)
	}
}

// 503 and 595 from pveproxy are tried again; then the check goes on.
func TestCheckRetriesVersion(t *testing.T) {
	f := newFakePVE(t)
	f.versionStatus = []int{503, 595}
	if _, err := checkDriver(t, f).Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := f.requests("GET /version"); n != 3 {
		t.Errorf("version requests = %d, want 3", n)
	}
}

// No answer within the start wait: UnreachableError (exit 1 in serve).
func TestCheckUnreachable(t *testing.T) {
	f := newFakePVE(t)
	d := checkDriver(t, f)
	f.srv.Close()
	_, err := d.Check(context.Background())
	var ue *UnreachableError
	if !errors.As(err, &ue) || !strings.HasPrefix(ue.URL, "https://127.0.0.1:") {
		t.Fatalf("err = %v", err)
	}
}

// The end of ctx (SIGTERM) ends the retries at once.
func TestCheckCancelDuringRetry(t *testing.T) {
	f := newFakePVE(t)
	d := checkDriver(t, f)
	d.cfg.StartWait, d.cfg.StartPoll = time.Hour, time.Second
	f.srv.Close()
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(50 * time.Millisecond); cancel() }()
	start := time.Now()
	_, err := d.Check(ctx)
	if err == nil || time.Since(start) > 2*time.Second {
		t.Fatalf("Check returned after %v: %v", time.Since(start), err)
	}
}

func problems(t *testing.T, err error) []HostProblem {
	t.Helper()
	var hp *HostProblems
	if !errors.As(err, &hp) {
		t.Fatalf("err = %v, want HostProblems", err)
	}
	return hp.Problems
}

func TestCheckPoolMissing(t *testing.T) {
	f := newFakePVE(t)
	f.noPool = true
	_, err := checkDriver(t, f).Check(context.Background())
	if p := problems(t, err); len(p) != 1 || p[0].Kind != ProblemPool || p[0].ID != "plumb" {
		t.Errorf("%+v", p)
	}
}

// A node name that does not exist: 595 on the storage list gives the node
// problem, with one request (no retry).
func TestCheckNodeMissing(t *testing.T) {
	f := newFakePVE(t)
	f.storageStatus = 595
	_, err := checkDriver(t, f).Check(context.Background())
	if p := problems(t, err); len(p) != 1 || p[0].Kind != ProblemNode {
		t.Errorf("%+v", p)
	}
	if n := f.requests("GET /nodes/pve/storage"); n != 1 {
		t.Errorf("storage requests = %d", n)
	}
}

func TestCheckStorages(t *testing.T) {
	set := func(f *fakePVE, id, key string, v any) {
		for _, s := range f.storages {
			if s["storage"] == id {
				s[key] = v
			}
		}
	}
	for name, tc := range map[string]struct {
		change func(f *fakePVE)
		kinds  []string
	}{
		"disk missing":        {func(f *fakePVE) { f.storages = f.storages[1:] }, []string{ProblemStorageMissing}},
		"image inactive":      {func(f *fakePVE) { set(f, "local", "active", 0) }, []string{ProblemStorageMissing}},
		"snippets disabled":   {func(f *fakePVE) { set(f, "plumb-snippets", "enabled", 0) }, []string{ProblemStorageMissing}},
		"disk with snippets":  {func(f *fakePVE) { set(f, "local-lvm", "content", "images,snippets") }, []string{ProblemStorageContent}},
		"disk without images": {func(f *fakePVE) { set(f, "local-lvm", "content", "rootdir") }, []string{ProblemStorageContent}},
		"image import,iso":    {func(f *fakePVE) { set(f, "local", "content", "import,iso") }, []string{ProblemStorageContent}},
		"snippets + vztmpl":   {func(f *fakePVE) { set(f, "plumb-snippets", "content", "snippets,vztmpl") }, []string{ProblemStorageContent}},
		"image vztmpl only":   {func(f *fakePVE) { set(f, "local", "content", "vztmpl") }, []string{ProblemStorageContent}},
		"image + vztmpl,iso":  {func(f *fakePVE) { set(f, "local", "content", "import,vztmpl,iso") }, []string{ProblemStorageContent}},
		"two problems": {func(f *fakePVE) {
			f.noPool = true
			set(f, "local", "content", "iso")
		}, []string{ProblemPool, ProblemStorageContent}},
		"no SDN.Use": {func(f *fakePVE) { f.perms["/sdn/zones/localnetwork/vmbr0"] = map[string]int{} }, []string{ProblemBridge}},
	} {
		f := newFakePVE(t)
		tc.change(f)
		_, err := checkDriver(t, f).Check(context.Background())
		p := problems(t, err)
		var kinds []string
		for _, x := range p {
			kinds = append(kinds, x.Kind)
		}
		if strings.Join(kinds, ",") != strings.Join(tc.kinds, ",") {
			t.Errorf("%s: %+v", name, p)
		}
		for _, x := range p {
			if x.Kind == ProblemStorageContent && (x.Content == "" || x.Rule == "") {
				t.Errorf("%s: no content or rule: %+v", name, x)
			}
		}
	}
}

// A token that cannot read its rights: step 4 is skipped, not a failure.
func TestCheckPermissionsUnreadable(t *testing.T) {
	f := newFakePVE(t)
	f.perms = nil
	if _, err := checkDriver(t, f).Check(context.Background()); err != nil {
		t.Fatal(err)
	}
}

// Review B1: /access/permissions holds the propagate flag as the value; the
// key is the grant (test host: {"SDN.Use": 0} on the bridge path).
func TestHasRight(t *testing.T) {
	const path = "/sdn/zones/localnetwork/vmbr0"
	for name, tc := range map[string]struct {
		perms map[string]map[string]int
		want  bool
	}{
		"on the path, propagate 0": {map[string]map[string]int{path: {"SDN.Use": 0}}, true},
		"on the path, propagate 1": {map[string]map[string]int{path: {"SDN.Use": 1}}, true},
		"parent, propagate 1":      {map[string]map[string]int{"/sdn": {"SDN.Use": 1}}, true},
		"root, propagate 1":        {map[string]map[string]int{"/": {"SDN.Use": 1}}, true},
		"parent, propagate 0":      {map[string]map[string]int{"/sdn/zones": {"SDN.Use": 0}}, false},
		"other right on the path":  {map[string]map[string]int{path: {"SDN.Audit": 0}}, false},
		"other bridge":             {map[string]map[string]int{"/sdn/zones/localnetwork/vmbr1": {"SDN.Use": 0}}, false},
	} {
		if got := hasRight(tc.perms, path, "SDN.Use"); got != tc.want {
			t.Errorf("%s: %v", name, got)
		}
	}
}

// A grant through a parent with propagate passes the check too.
func TestCheckBridgeRightFromParent(t *testing.T) {
	f := newFakePVE(t)
	f.perms = map[string]map[string]int{"/sdn/zones": {"SDN.Use": 1}}
	if _, err := checkDriver(t, f).Check(context.Background()); err != nil {
		t.Fatal(err)
	}
}

// Review S2: /version answered, then the connection is lost: "no answer"
// (exit 1), not a host problem (exit 78).
func TestCheckTransportErrorAfterVersion(t *testing.T) {
	for _, p := range []string{"/pools", "/nodes/pve/storage"} {
		f := newFakePVE(t)
		f.dropPath = p
		_, err := checkDriver(t, f).Check(context.Background())
		var ue *UnreachableError
		var hp *HostProblems
		if !errors.As(err, &ue) || errors.As(err, &hp) {
			t.Errorf("%s: %v", p, err)
		}
	}
}

// Review N1: a TLS alert is never tried again.
func TestTLSAlertNotRetried(t *testing.T) {
	if retryable(fmt.Errorf("proxmox: GET /version: %w", tls.AlertError(42))) {
		t.Error("a TLS alert is retried")
	}
}

// Every storage role must fail closed for missing, inactive or wrong content.
func TestCheckEachStorageFailure(t *testing.T) {
	for _, id := range []string{"local-lvm", "local", "plumb-snippets"} {
		for _, fault := range []string{"missing", "inactive", "disabled", "content"} {
			t.Run(id+"/"+fault, func(t *testing.T) {
				f := newFakePVE(t)
				for i, s := range f.storages {
					if s["storage"] != id {
						continue
					}
					switch fault {
					case "missing":
						f.storages = append(f.storages[:i], f.storages[i+1:]...)
					case "inactive":
						s["active"] = 0
					case "disabled":
						s["enabled"] = 0
					case "content":
						s["content"] = "iso"
					}
					break
				}
				_, err := checkDriver(t, f).Check(context.Background())
				p := problems(t, err)
				kind := ProblemStorageMissing
				if fault == "content" {
					kind = ProblemStorageContent
				}
				if len(p) != 1 || p[0].Kind != kind || p[0].ID != id {
					t.Errorf("problems = %+v, want %s for %s", p, kind, id)
				}
			})
		}
	}
}

// D-229: the image storage has exactly {import} (an install from before
// D-229: the check passes, LXC Images get the repair-run text) or exactly
// {import, vztmpl}, compared as sets (pvesm set reorders, host G0).
func TestCheckImageTemplateContent(t *testing.T) {
	for content, templates := range map[string]bool{
		"import":               false,
		"import,vztmpl":        true,
		"vztmpl,import":        true,
		" vztmpl, import":      true,
		"import,import":        false,
		"vztmpl,import,vztmpl": true,
	} {
		f := newFakePVE(t)
		for _, s := range f.storages {
			if s["storage"] == "local" {
				s["content"] = content
			}
		}
		d := checkDriver(t, f)
		if _, err := d.Check(context.Background()); err != nil {
			t.Fatalf("%q: %v", content, err)
		}
		err := d.TemplateContentError()
		if templates != (err == nil) {
			t.Errorf("%q: TemplateContentError = %v", content, err)
		}
		if err != nil && err.Error() != "local has no content vztmpl: run install.sh again (repair run)" {
			t.Errorf("%q: text %q", content, err)
		}
	}
}

// Check every subset, including empty, to guard against permitting an
// unrelated content type alongside either supported set.
func TestCheckImageContentSets(t *testing.T) {
	contents := []string{"import", "vztmpl", "images", "rootdir", "iso", "backup", "snippets"}
	for mask := 0; mask < 1<<len(contents); mask++ {
		var set []string
		for i, content := range contents {
			if mask&(1<<i) != 0 {
				set = append(set, content)
			}
		}
		content := strings.Join(set, ",")
		t.Run(content, func(t *testing.T) {
			f := newFakePVE(t)
			for _, storage := range f.storages {
				if storage["storage"] == "local" {
					storage["content"] = content
				}
			}
			_, err := checkDriver(t, f).Check(context.Background())
			if mask == 1 || mask == 3 {
				if err != nil {
					t.Fatalf("accepted set rejected: %v", err)
				}
				return
			}
			p := problems(t, err)
			if len(p) != 1 || p[0].Kind != ProblemStorageContent || p[0].ID != "local" {
				t.Fatalf("want image storage content problem, got %+v", p)
			}
		})
	}
}
