package plan

import (
	"github.com/Bernotese/plumb/internal/wire"
	"testing"
)

// A sensitive change with a non-nil From must never enter the hash store.
func TestGuestValueSensitiveFromQA(t *testing.T) {
	e := &Engine{}
	vals, over := e.GuestValues(wire.Item{Resource: "file:/etc/private", Status: wire.StatusDiffers,
		Changes: []wire.Change{{Attr: "content", From: "guest-secret", To: "desired-secret", Sensitive: true}}})
	if len(vals) != 0 || over {
		t.Fatalf("sensitive data was retained: count=%d over=%v", len(vals), over)
	}
}
