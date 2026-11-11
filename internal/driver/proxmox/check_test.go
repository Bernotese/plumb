package proxmox

// The start-up check (T-053, D-183) against the fake PVE.

import (
	"context"
	"errors"
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
