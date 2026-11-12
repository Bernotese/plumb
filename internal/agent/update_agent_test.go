package agent

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/Bernotese/plumb/internal/wire"
)

// The whole path through the agent loop: hello holds the hash and the
// capabilities, the update command swaps and ends Run with ErrRestart, and
// the next process confirms after its hello.
func TestAgentUpdateCommand(t *testing.T) {
	ca := newCA(t)
	ctl := newController(t, ca, "tok-1", "tok-2")
	dir := t.TempDir()
	bin, units, cfgPath := filepath.Join(dir, "bin", "plumb-agent"), filepath.Join(dir, "units"), filepath.Join(dir, "agent.json")
	for _, d := range []string{filepath.Dir(bin), units} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	old, nb := fakeBin("v1", ""), fakeBin("v2", "")
	if err := os.WriteFile(bin, old, 0o755); err != nil {
		t.Fatal(err)
	}
	ctl.binary = nb
	paths := func(o *Options) {
		o.BinaryPath, o.UnitDir, o.ConfigPath, o.SelfExe = bin, units, cfgPath, bin
	}
	ta := startAgentWith(t, ctl, ca, "tok-1", paths)
	waitFor(t, "hello", func() bool { return len(ctl.reportsOf(wire.ReportHello)) == 1 })
	h := ctl.reportsOf(wire.ReportHello)[0].Hello
	if h.BinarySHA256 != shaOf(old) || h.Capabilities == nil || !h.Capabilities.CanUpdate() || h.Reverted != nil {
		t.Fatalf("hello %+v", h)
	}
	if !slices.Equal(h.Capabilities.Resources["file"], wire.BaselineCapabilities().Resources["file"]) {
		t.Errorf("file attrs %v", h.Capabilities.Resources["file"])
	}

	ctl.send(t, "u1", wire.CommandUpdate, wire.UpdateBody{PlanID: 1, SHA256: shaOf(nb)})
	rep := ctl.result(t, "u1")
	if len(rep.Items) != 1 || rep.Items[0].Resource != wire.AgentBinaryID || rep.Items[0].Status != wire.StatusApplied {
		t.Fatalf("result %+v", rep)
	}
	<-ta.done
	if !errors.Is(ta.err, ErrRestart) {
		t.Fatalf("Run ended with %v", ta.err)
	}
	if read(t, bin) != string(nb) || !present(filepath.Join(units, guardTimer)) {
		t.Fatal("not swapped or no guard")
	}

	// The "new process" (SelfExe is now v2) confirms after its hello. It
	// keeps the state dir of the first one.
	state := ta.cfg.StateDir
	ta2 := startAgentWith(t, ctl, ca, "tok-2", func(o *Options) { paths(o); o.Config.StateDir = state })
	waitFor(t, "confirm", func() bool { return !present(filepath.Join(state, updateMarkerFile)) })
	if present(filepath.Join(units, guardTimer)) {
		t.Error("guard left after confirm")
	}
	hs := ctl.reportsOf(wire.ReportHello)
	if last := hs[len(hs)-1].Hello; last.BinarySHA256 != shaOf(nb) {
		t.Errorf("hello of the new process %+v", last)
	}
	_ = ta2
}

func TestAgentUpdateFailedKeepsRunning(t *testing.T) {
	ca := newCA(t)
	ctl := newController(t, ca, "tok-1")
	dir := t.TempDir()
	bin := filepath.Join(dir, "plumb-agent")
	if err := os.WriteFile(bin, fakeBin("v1", ""), 0o755); err != nil {
		t.Fatal(err)
	}
	ctl.binary = fakeBin("v2", "")
	ta := startAgentWith(t, ctl, ca, "tok-1", func(o *Options) {
		o.BinaryPath, o.UnitDir, o.ConfigPath, o.SelfExe = bin, dir, filepath.Join(dir, "agent.json"), bin
	})
	ctl.send(t, "u1", wire.CommandUpdate, wire.UpdateBody{SHA256: shaOf([]byte("other"))})
	rep := ctl.result(t, "u1")
	if len(rep.Items) != 1 || rep.Items[0].Status != wire.StatusFailed || rep.Items[0].Output == "" {
		t.Fatalf("result %+v", rep)
	}
	// Still polling: the next command gets an answer.
	ctl.send(t, "o1", wire.CommandObserve, wire.ObserveBody{})
	ctl.result(t, "o1")
	select {
	case <-ta.done:
		t.Fatal("agent stopped")
	default:
	}
}
