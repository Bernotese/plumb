package plan

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/Bernotese/plumb/internal/store"
)

func TestBuildReservationBeforeVersionRow(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	e := &Engine{st: st, busy: map[string]int{}}
	images := []ImagePlan{{Image: "web", Build: &ImageBuildPlan{}}}
	slots, err := e.reserveImages(ctx, images)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.reserveImages(ctx, images); !errors.Is(err, store.ErrBuildRunning) {
		t.Fatalf("second reservation: %v", err)
	}
	e.release(slots)
	if _, err := e.reserveImages(ctx, images); err != nil {
		t.Fatalf("after release: %v", err)
	}
}
