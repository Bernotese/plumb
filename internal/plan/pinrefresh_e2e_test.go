//go:build plumbtest

package plan

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Bernotese/plumb/internal/driver"
	"github.com/Bernotese/plumb/internal/driver/fake"
	"github.com/Bernotese/plumb/internal/link"
	"github.com/Bernotese/plumb/internal/state"
	"github.com/Bernotese/plumb/internal/store"
	"github.com/Bernotese/plumb/internal/trust"
)

// Stop at the hypervisor boundary after recording the actual base request.
// Signature verification, scheduling, approval and revision storage are real.
type f5Driver struct {
	driver.Driver
	base driver.ImageSpec
}

func (d *f5Driver) FetchImage(_ context.Context, spec driver.ImageSpec) (driver.ImageRef, error) {
	d.base = spec
	return "", errors.New("F5: guest execution is outside this proof")
}

type f5Transport struct {
	base http.RoundTripper
	url  string
}

func (r f5Transport) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.URL.Scheme, clone.URL.Host = "https", strings.TrimPrefix(r.url, "https://")
	return r.base.RoundTrip(clone)
}

func TestF5SignedPinSchedule(t *testing.T) {
	ctx := context.Background()
	// list is read by the server goroutine and set by the test (#275 l1).
	var listMu sync.Mutex
	list := "old"
	setList := func(v string) { listMu.Lock(); list = v; listMu.Unlock() }
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "SHA512SUMS") {
			_, _ = w.Write([]byte(strings.Repeat("e", 128) + "  f5.img\n"))
			return
		}
		suffix := "-SHA256SUMS"
		if strings.HasSuffix(r.URL.Path, ".gpg") {
			suffix += ".gpg"
		}
		listMu.Lock()
		cur := list
		listMu.Unlock()
		if cur == "unsigned" && strings.HasSuffix(r.URL.Path, ".gpg") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		fixture := cur
		if fixture == "unsigned" {
			fixture = "new"
		}
		b, err := os.ReadFile("../trust/testdata/f5/" + fixture + suffix)
		if err != nil {
			t.Error(err)
			w.WriteHeader(500)
			return
		}
		_, _ = w.Write(b)
	}))
	defer srv.Close()
	pins := &trust.Client{HTTP: &http.Client{Transport: f5Transport{srv.Client().Transport, srv.URL}}}
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "f5.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	lk, err := link.New(link.Config{Store: st, Dir: filepath.Join(t.TempDir(), "link")})
	if err != nil {
		t.Fatal(err)
	}
	drv := &f5Driver{Driver: fake.New()}
	now := time.Now().UTC()
	e, err := New(Config{Store: st, Agents: lk, Driver: drv, Pins: pins, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	doc := state.Doc{Kind: state.KindImage, Name: "f5", Spec: []byte(`{"type":"vm","url":"https://cloud-images.ubuntu.com/f5.img","sha256":"` + strings.Repeat("a", 64) + `","verify":{"source":"ubuntu-cloud"},"build":{"blocks":[],"guest":{"disk":10,"bridge":"vmbr0"}},"schedule":{"every":"day","at":"03:00"}}`)}
	p, err := e.Propose(ctx, Input{Author: "user:qa", Reason: "F5 fixture", Proposal: Proposal{Put: []state.Doc{doc}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.Approve(ctx, p.ID, "user:qa", nil); err != nil {
		t.Fatal(err)
	}
	e.Wait()
	// A real due slot asks for the first signed list; no baseline means human-only.
	tick := func() {
		t.Helper()
		if err := st.SetImageScheduleSlot(ctx, "f5", now.Add(-48*time.Hour)); err != nil {
			t.Fatal(err)
		}
		if err := e.CheckSchedules(ctx); err != nil {
			t.Fatal(err)
		}
		e.Wait()
	}
	pending := func() store.Plan {
		t.Helper()
		ps, err := st.PendingPlans(ctx, OriginPinRefresh)
		if err != nil || len(ps) != 1 {
			t.Fatalf("pending refresh: %+v, %v", ps, err)
		}
		if ps[0].ApprovedBy != "" || len(ps[0].HumanOnly) == 0 {
			t.Fatalf("expected human-only: %+v", ps[0])
		}
		return ps[0]
	}
	tick()
	first := pending()
	if err := e.pinSystemCheck(ctx)(first); err == nil {
		t.Fatal("system accepted missing baseline")
	}
	if _, err = e.Approve(ctx, first.ID, "user:qa", nil); err != nil {
		t.Fatal(err)
	}
	e.Wait()
	setList("new")
	tick()
	approved, err := st.ApprovedPlans(ctx, OriginPinRefresh)
	if err != nil || len(approved) != 2 {
		t.Fatalf("approved: %+v %v", approved, err)
	}
	second := approved[1]
	if second.Author != PrincipalSystem || second.ApprovedBy != PrincipalSystem || second.Origin != OriginPinRefresh || len(second.HumanOnly) != 0 {
		t.Fatalf("system refresh: %+v", second)
	}
	versions, err := st.ImageVersions(ctx, "f5")
	if err != nil || len(versions) == 0 {
		t.Fatalf("versions: %+v %v", versions, err)
	}
	want := strings.Repeat("c", 64)
	if drv.base.SHA256 != want || versions[0].BaseSHA256 != want {
		t.Fatalf("build base: driver=%+v versions=%+v", drv.base, versions)
	}
	_, set, err := e.base(ctx)
	if err != nil {
		t.Fatal(err)
	}
	info, _ := set.ImageInfo("f5")
	if info.Pin() != want {
		t.Fatalf("head pin: %s", info.Pin())
	}
	for _, replay := range []string{"old", "equal", "older"} {
		list = replay
		tick()
		p := pending()
		if err := e.pinSystemCheck(ctx)(p); err == nil {
			t.Fatalf("system accepted %s", replay)
		}
		if drv.base.SHA256 != want {
			t.Fatalf("%s changed build base", replay)
		}
		if err := st.DiscardPlan(ctx, p.ID); err != nil {
			t.Fatal(err)
		}
	}
	// Missing signature fails closed: it cannot create any refresh Plan.
	setList("unsigned")
	tick()
	if ps, err := st.PendingPlans(ctx, OriginPinRefresh); err != nil || len(ps) != 0 {
		t.Fatalf("unsigned refresh: %+v %v", ps, err)
	}
	if drv.base.SHA256 != want {
		t.Fatal("unsigned list changed the build base")
	}
	// Debian intentionally publishes an unsigned HTTPS list: it needs a person.
	doc.Spec = []byte(strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(string(doc.Spec), "cloud-images.ubuntu.com/f5.img", "cloud.debian.org/images/cloud/f5.img"), "ubuntu-cloud", "debian-https"), `"sha256":"`+strings.Repeat("a", 64)+`"`, `"sha512":"`+strings.Repeat("d", 128)+`"`))
	head, err := st.Head(ctx)
	if err != nil {
		t.Fatal(err)
	}
	p, err = e.Propose(ctx, Input{Base: head.ID, Author: "user:qa", Reason: "unsigned fixture", Proposal: Proposal{Put: []state.Doc{doc}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.Approve(ctx, p.ID, "user:qa", nil); err != nil {
		t.Fatal(err)
	}
	e.Wait()
	tick()
	unsigned := pending()
	if err := e.pinSystemCheck(ctx)(unsigned); err == nil {
		t.Fatal("system accepted unsigned list")
	}
	if drv.base.SHA512 != strings.Repeat("d", 128) {
		t.Fatal("unsigned refresh changed build base")
	}
}
