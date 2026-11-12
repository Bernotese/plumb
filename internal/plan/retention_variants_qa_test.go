package plan

import (
	"github.com/Bernotese/plumb/internal/driver"
	"github.com/Bernotese/plumb/internal/store"
	"github.com/Bernotese/plumb/internal/wire"
	"strings"
	"testing"
)

func TestRetentionVariantsQA(t *testing.T) {
	for _, tc := range []struct {
		name    string
		keep    int
		changed bool
	}{{"lower keep", 1, false}, {"changed base", 2, true}, {"deleted image", 0, false}} {
		t.Run(tc.name, func(t *testing.T) {
			f := newVersionFixture(t)
			for i := 0; i < 3; i++ {
				f.version()
			}
			sum := strings.Repeat("a", 64)
			if tc.changed {
				sum = strings.Repeat("b", 64)
				if _, err := f.drv.FetchImage(f.ctx, driver.ImageSpec{Name: "web", Kind: wire.KindVM, URL: "https://x.invalid/new.img", SHA256: sum}); err != nil {
					t.Fatal(err)
				}
			}
			removed, left, err := f.e.retain(f.ctx, "web", tc.keep, sum, 0)
			if err != nil || len(left) != 0 {
				t.Fatalf("retain: removed %v left %v err %v", removed, left, err)
			}
			for n := 1; n <= 3; n++ {
				v, err := f.st.GetImageVersion(f.ctx, "web", n)
				want := store.ImageReady
				if n <= 3-tc.keep {
					want = store.ImageRemoved
				}
				if err != nil || v.Status != want {
					t.Fatalf("v%d: %+v %v", n, v, err)
				}
			}
			objs, err := f.drv.ListImages(f.ctx, "web")
			if err != nil {
				t.Fatal(err)
			}
			versions, bases := 0, 0
			for _, o := range objs {
				switch o.Kind {
				case driver.ImageVersionTemplate:
					versions++
					if o.Version <= 3-tc.keep {
						t.Errorf("old version remains: %+v", o)
					}
				case driver.ImageBaseTemplate:
					bases++
					if o.Digest != sum || tc.keep == 0 {
						t.Errorf("obsolete base remains: %+v", o)
					}
				}
			}
			wantBases := 1
			if tc.keep == 0 {
				wantBases = 0
			}
			if versions != tc.keep || bases != wantBases {
				t.Fatalf("objects: %+v", objs)
			}
			wantRemoved := 3 - tc.keep
			if tc.changed || tc.keep == 0 {
				wantRemoved++
			}
			if len(removed) != wantRemoved {
				t.Fatalf("removed: %v", removed)
			}
		})
	}
}
