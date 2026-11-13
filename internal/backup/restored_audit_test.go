package backup

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Bernotese/plumb/internal/store"
)

func TestRestoredBackupSchedule(t *testing.T) {
	f := newFixture(t, nil)
	if due, err := f.svc.Due(f.ctx); err != nil || !due {
		t.Fatalf("fixture must be due: %v %v", due, err)
	}
	ctx, cancel := context.WithTimeout(f.ctx, time.Second)
	defer cancel()
	f.svc.RunSchedule(ctx, time.Millisecond, func() bool { cancel(); return true })
	if run, err := f.st.LastBackupRun(f.ctx, ""); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("paused schedule started a backup: %+v %v", run, err)
	}
}
