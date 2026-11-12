package agent

import (
	"testing"
	"time"

	"github.com/Bernotese/plumb/internal/wire"
)

// T-094: a command has no drift (D-260). Its creates path is missing, so
// an observe would say "absent", but the drift check skips it.
func TestCommandHasNoDrift(t *testing.T) {
	ca := newCA(t)
	ctl := newController(t, ca, "tok-1")
	ta := startAgent(t, ctl, ca, "tok-1")
	conf := fileRes("app", "/srv/conf", map[string]any{"content": "x"})
	cmd := wire.Resource{ID: "command:init", Type: "command", Block: "app",
		Attrs: map[string]any{"name": "init", "argv": []any{"/bin/true"}, "creates": "/srv/init.done"}}
	ctl.send(t, "w1", wire.CommandWatch, wire.WatchBody{Resources: []wire.Resource{conf, cmd}})
	if rep := ctl.result(t, "w1"); rep.Error != "" {
		t.Fatalf("watch = %+v", rep)
	}
	waitFor(t, "drift of the missing file", func() bool { return len(ctl.driftItems(conf.ID)) == 1 })
	time.Sleep(5 * ta.a.opt.Debounce)
	if items := ctl.driftItems(cmd.ID); len(items) != 0 {
		t.Fatalf("command drift = %+v", items)
	}
}
