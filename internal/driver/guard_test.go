package driver_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Bernotese/plumb/internal/driver"
	"github.com/Bernotese/plumb/internal/driver/fake"
)

// In restored mode every write fails before the driver gets the call;
// reads pass (D-303).
func TestReadOnlyWhen(t *testing.T) {
	ctx := context.Background()
	f := fake.New()
	on := true
	g := driver.ReadOnlyWhen(f, func() bool { return on })
	writes := map[fake.Op]func() error{
		fake.OpFetchImage:  func() error { _, err := g.FetchImage(ctx, driver.ImageSpec{}); return err },
		fake.OpCreate:      func() error { _, err := g.Create(ctx, driver.GuestSpec{}, driver.Bootstrap{}); return err },
		fake.OpUpdate:      func() error { return g.Update(ctx, "1", nil, nil) },
		fake.OpPower:       func() error { return g.Power(ctx, "1", driver.PowerStop) },
		fake.OpDelete:      func() error { return g.Delete(ctx, "1") },
		fake.OpCreateBuild: func() error { _, err := g.CreateBuild(ctx, driver.BuildSpec{}, driver.Bootstrap{}); return err },
		fake.OpMakeImage:   func() error { _, err := g.MakeImage(ctx, "1", driver.BuiltImage{}); return err },
		fake.OpRemoveImage: func() error { return g.RemoveImage(ctx, driver.ImageObject{}) },
		fake.OpRemoveBuild: func() error { return g.RemoveBuild(ctx, "1", "img", 1) },
	}
	for op, call := range writes {
		if err := call(); !errors.Is(err, driver.ErrReadOnly) {
			t.Errorf("%s: %v", op, err)
		}
		if n := f.Calls(op); n != 0 {
			t.Errorf("%s reached the driver %d times", op, n)
		}
	}
	if err := g.ClearBootstrap(ctx, "1"); !errors.Is(err, driver.ErrReadOnly) {
		t.Errorf("ClearBootstrap: %v", err)
	}
	if _, err := g.ListGuests(ctx); err != nil {
		t.Errorf("ListGuests: %v", err)
	}
	if _, err := g.ListBuilds(ctx); err != nil {
		t.Errorf("ListBuilds: %v", err)
	}
	if _, err := g.Observe(ctx, "1"); !errors.Is(err, driver.ErrNotFound) {
		t.Errorf("Observe passes to the driver: %v", err)
	}
	on = false
	if err := g.Power(ctx, "1", driver.PowerStop); errors.Is(err, driver.ErrReadOnly) {
		t.Error("guard after the confirm")
	}
}
