package store

import (
	"errors"
	"testing"
	"time"
)

// T-222: a restore drops the pins and the repair report of the live
// binding of its Target only; the binding and its generation stay.
func TestDropHostKeys(t *testing.T) {
	s, ctx := genEnv(t)
	b := pinFor(t, s, ctx, "db1", "101", "SHA256:a", "SHA256:b")
	other := pinFor(t, s, ctx, "web1", "102", "SHA256:c")
	n, err := s.DropHostKeys(ctx, "db1")
	if err != nil || n != 3 { // two pins and one report
		t.Fatalf("drop: %d %v", n, err)
	}
	if pins, _ := s.HostKeyPins(ctx, "db1", b.GuestKey()); len(pins) != 0 {
		t.Errorf("pins after the drop: %v", pins)
	}
	if _, err := s.RepairReport(ctx, "db1"); !errors.Is(err, ErrNotFound) {
		t.Errorf("report after the drop: %v", err)
	}
	if after, _ := s.GetBinding(ctx, "Target", "db1"); after.Gen != b.Gen || after.Ref != b.Ref {
		t.Errorf("binding changed: %+v, was %+v", after, b)
	}
	if pins, _ := s.HostKeyPins(ctx, "web1", other.GuestKey()); len(pins) != 1 {
		t.Errorf("the other Target lost its pins: %v", pins)
	}
	if _, err := s.RepairReport(ctx, "web1"); err != nil {
		t.Errorf("the other Target lost its report: %v", err)
	}
	// Nothing left: no error, 0 rows. A new report pins again.
	if n, err := s.DropHostKeys(ctx, "db1"); err != nil || n != 0 {
		t.Errorf("second drop: %d %v", n, err)
	}
	if ok, err := s.PinFirstHostKeys(ctx, "db1", b.GuestKey(), []string{"SHA256:a"}, time.Now()); err != nil || !ok {
		t.Errorf("pin after the drop: %v %v", ok, err)
	}
}
