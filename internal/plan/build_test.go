package plan

// Review S1 of #133: a create that clones a version and the retention of
// another lane that removes it hold each other off.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Bernotese/plumb/internal/driver"
	"github.com/Bernotese/plumb/internal/driver/fake"
	"github.com/Bernotese/plumb/internal/store"
	"github.com/Bernotese/plumb/internal/wire"
)

type versionFixture struct {
	t    *testing.T
	ctx  context.Context
	st   *store.Store
	drv  *fake.Driver
	e    *Engine
	base driver.ImageRef
	run  store.Run
	rev  store.Revision
}

func newVersionFixture(t *testing.T) *versionFixture {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	p, _ := st.CreatePlan(ctx, store.Plan{Origin: "proposal", Risk: "reversible", Reason: "r", Author: "user:a"})
	body := `{"kind":"Block","name":"a","spec":{}}`
	sum := sha256.Sum256([]byte(body))
	rev, run, err := st.ApprovePlan(ctx, p.ID, "user:a", store.NewRevision{Author: "user:a",
		Docs: []store.DocRow{{Kind: "Block", Name: "a", Hash: hex.EncodeToString(sum[:]), Body: []byte(body)}}})
	if err != nil {
		t.Fatal(err)
	}
	drv := fake.New()
	base, err := drv.FetchImage(ctx, driver.ImageSpec{Name: "web", Kind: wire.KindVM, URL: "https://x.invalid/u.img", SHA256: strings.Repeat("a", 64)})
	if err != nil {
		t.Fatal(err)
	}
	e := &Engine{st: st, cfg: Config{Driver: drv}, busy: map[string]int{}, pinned: map[imageKey]int{}, removing: map[imageKey]bool{}}
	return &versionFixture{t: t, ctx: ctx, st: st, drv: drv, e: e, base: base, run: run, rev: rev}
}

// version makes a ready version with its template.
func (f *versionFixture) version() int {
	f.t.Helper()
	sum := strings.Repeat("a", 64)
	v, err := f.st.AddImageVersion(f.ctx, store.ImageVersion{Image: "web", Trigger: store.TriggerManual, RunID: f.run.ID, RevisionID: f.rev.ID,
		RecipeSHA256: sum, BaseSHA256: sum, DiskGB: 10}, func(n int) string { return BuildDigest("web", n, sum, sum, f.run.ID) })
	if err != nil {
		f.t.Fatal(err)
	}
	gid, err := f.drv.CreateBuild(f.ctx, driver.BuildSpec{Image: "web", Version: v.Version, Base: f.base, Cores: 1, MemoryMB: 512, DiskGB: 10, Bridge: "vmbr0"},
		driver.Bootstrap{Token: "t"})
	if err != nil {
		f.t.Fatal(err)
	}
	f.drv.StopOutside(gid)
	if _, err := f.drv.MakeImage(f.ctx, gid, driver.BuiltImage{Image: "web", Version: v.Version, Digest: v.Digest}); err != nil {
		f.t.Fatal(err)
	}
	if err := f.st.FinishImageVersion(f.ctx, "web", v.Version, store.ImageReady); err != nil {
		f.t.Fatal(err)
	}
	return v.Version
}

func TestRetentionSkipsVersionInClone(t *testing.T) {
	f := newVersionFixture(t)
	f.version() // v1
	f.version() // v2
	// Lane A: a create picks v2 and clones it (pinned until unpin).
	_, picked, unpin, err := f.e.newestVersion(f.ctx, "web")
	if err != nil || picked != 2 {
		t.Fatalf("pick = %d %v", picked, err)
	}
	// Lane B: a build of v3 ends; retention with keep 1 would remove v2.
	f.version()
	removed, left, err := f.e.retain(f.ctx, "web", 1, strings.Repeat("a", 64), 3)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(removed, " "), "plumb-img-web-v2") || !strings.Contains(strings.Join(left, " "), "plumb-img-web-v2: a create clones from it now") {
		t.Fatalf("removed %v, left %v", removed, left)
	}
	if v, _ := f.st.GetImageVersion(f.ctx, "web", 2); v.Status != store.ImageReady {
		t.Fatalf("v2 = %s", v.Status)
	}
	// The clone ends; the next retention removes v2.
	unpin()
	removed, _, _ = f.e.retain(f.ctx, "web", 1, strings.Repeat("a", 64), 3)
	if !strings.Contains(strings.Join(removed, " "), "plumb-img-web-v2") {
		t.Fatalf("after the clone: removed %v", removed)
	}
}

func TestCreateSkipsVersionInRemoval(t *testing.T) {
	f := newVersionFixture(t)
	f.version() // v1
	f.version() // v2
	// Retention removes v2 now: a create picks v1.
	if !f.e.startRemove("web", 2) {
		t.Fatal("startRemove")
	}
	_, picked, unpin, err := f.e.newestVersion(f.ctx, "web")
	if err != nil || picked != 1 {
		t.Fatalf("pick = %d %v, want 1", picked, err)
	}
	// While v1 is pinned, retention cannot start on it.
	if f.e.startRemove("web", 1) {
		t.Fatal("a pinned version was marked for removal")
	}
	unpin()
	f.e.endRemove("web", 2)
	if !f.e.startRemove("web", 1) {
		t.Fatal("after unpin")
	}
}
