package link

import (
	"errors"
	"testing"

	"github.com/Bernotese/plumb/internal/wire"
)

// In restored mode link sends no command but observe (D-303).
func TestSendRestoredMode(t *testing.T) {
	f := newFixture(t)
	on := true
	f.link.cfg.Restored = func() bool { return on }
	if _, err := f.link.Send("web1", wire.CommandObserve, wire.ObserveBody{}); err != nil {
		t.Errorf("observe: %v", err)
	}
	for _, typ := range []wire.CommandType{wire.CommandApply, wire.CommandWatch, wire.CommandUpdate, wire.CommandGrow} {
		var body any = wire.ApplyBody{}
		switch typ {
		case wire.CommandWatch:
			body = wire.WatchBody{}
		case wire.CommandUpdate:
			body = wire.UpdateBody{}
		case wire.CommandGrow:
			body = wire.GrowBody{}
		}
		if _, err := f.link.Send("web1", typ, body); !errors.Is(err, ErrRestoredMode) {
			t.Errorf("%s: %v", typ, err)
		}
	}
	if _, err := f.link.Send("build:img-v1", wire.CommandSeal, wire.SealBody{}); !errors.Is(err, ErrRestoredMode) {
		t.Errorf("seal: %v", err)
	}
	on = false
	if _, err := f.link.Send("web1", wire.CommandApply, wire.ApplyBody{}); err != nil {
		t.Errorf("after the confirm: %v", err)
	}
}
