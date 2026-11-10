package state

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/Bernotese/plumb/internal/store"
)

func openStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "plumb.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func TestCommitLoadDiff(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)

	v1, err := Validate(fixture(target(`"blocks":[{"use":"motd"}]`)))
	if err != nil {
		t.Fatal(err)
	}
	r1, err := Commit(ctx, st, v1, CommitInfo{Author: "system", Reason: "seed"})
	if err != nil {
		t.Fatal(err)
	}
	// Same Target, only a number spelling and key order differ; one Block changes.
	v2, err := Validate([]Doc{
		doc(KindImage, "img", image),
		doc(KindBlock, "motd", motdB),
		doc(KindTarget, "t1", `{"blocks":[{"use":"motd"}],"guest":{"bridge":"vmbr0","disk":4.0,"memory":512,"cores":1},"image":"img","type":"vm"}`),
		doc(KindBlock, "extra", `{"resources":[{"file":{"path":"/e"}}]}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	r2, err := Commit(ctx, st, v2, CommitInfo{Author: "user:admin", Reason: "add extra"})
	if err != nil {
		t.Fatal(err)
	}
	if r1.ID != 1 || r2.ID != 2 {
		t.Fatalf("revision ids %d, %d", r1.ID, r2.ID)
	}

	diff, err := st.Diff(ctx, r1.ID, r2.ID)
	if err != nil {
		t.Fatal(err)
	}
	want := []struct {
		kind, name string
		op         store.DiffOp
	}{{KindBlock, "extra", store.DocAdded}, {KindBlock, "keyed", store.DocRemoved}}
	if len(diff) != len(want) {
		t.Fatalf("diff = %+v", diff)
	}
	for i, w := range want {
		if diff[i].Kind != w.kind || diff[i].Name != w.name || diff[i].Op != w.op {
			t.Errorf("diff[%d] = %+v, want %+v", i, diff[i], w)
		}
	}

	// Unchanged documents share one row: the Target of r1 and r2.
	a, _ := st.RevisionDoc(ctx, r1.ID, KindTarget, "t1")
	b, _ := st.RevisionDoc(ctx, r2.ID, KindTarget, "t1")
	if a.Hash == "" || a.Hash != b.Hash {
		t.Errorf("unchanged Target got a new hash: %s vs %s", a.Hash, b.Hash)
	}

	// Load returns the same documents, validated.
	loaded, err := Load(ctx, st, r2.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := len(loaded.Docs()), len(v2.Docs()); got != want {
		t.Errorf("loaded %d docs, want %d", got, want)
	}
	if _, err := loaded.Render("t1"); err != nil {
		t.Error(err)
	}
	if _, err := Load(ctx, st, 99); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("unknown revision: %v", err)
	}
	if _, err := Commit(ctx, st, nil, CommitInfo{Author: "system"}); err == nil {
		t.Error("commit of a nil set must fail")
	}
}
